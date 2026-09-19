package cli

import (
	"fmt"
	"math/rand"
	"strings"
	"time"

	"github.com/kaushlendra/combio/backend/internal/engine"
)

// Player is one seat at the local table.
type Player struct {
	ID   engine.PlayerID
	Name string
	Bot  bool
}

// Session drives a local game. It holds engine.State because it must call
// Apply, but every screen it draws comes from ViewFor / FilterEvents. The only
// code allowed to read the raw state is debug.go.
type Session struct {
	st      engine.State
	players []Player
	names   nameBook
	io      *IO
	debug   bool
	rng     *rand.Rand
	feed    []string
	quit    bool
	bots    map[engine.PlayerID]*BotBrain
	// onEvents is a test hook; nil in normal play.
	onEvents func([]engine.Event)
	// lastAction records the most recent accepted action name, for diagnostics
	// and for tests that assert how a phase ended.
	lastAction string
}

// Setup asks the questions and deals the game.
func Setup(t *IO, debug bool, seed int64) (*Session, bool) {
	t.ClearScreen()
	t.Println(banner)
	if debug {
		t.Println("*** DEBUG MODE — HIDDEN CARDS VISIBLE ***")
	}

	n := t.Int("Number of players", 2, 8)
	cards := 0
	for cards != 4 && cards != 6 && cards != 8 {
		cards = t.Int("Cards per player (4, 6 or 8)", 4, 8)
		if cards != 4 && cards != 6 && cards != 8 {
			t.Println("  ?? cards per player must be 4, 6 or 8")
		}
		if t.EOF() {
			return nil, false
		}
	}

	players := make([]Player, 0, n)
	ids := make([]engine.PlayerID, 0, n)
	for i := 1; i <= n; i++ {
		name := t.Line(fmt.Sprintf("Name of player %d: ", i))
		if name == "" {
			name = fmt.Sprintf("Player%d", i)
		}
		bot := t.YesNo(fmt.Sprintf("Is %s a bot?", name))
		id := engine.PlayerID(fmt.Sprintf("p%d", i))
		players = append(players, Player{ID: id, Name: name, Bot: bot})
		ids = append(ids, id)
		if t.EOF() {
			return nil, false
		}
	}

	if seed == 0 {
		s := t.Line("Deterministic seed (blank for random): ")
		if s == "" {
			seed = time.Now().UnixNano()
		} else {
			var v int64
			if _, err := fmt.Sscanf(s, "%d", &v); err == nil {
				seed = v
			} else {
				seed = time.Now().UnixNano()
			}
		}
	}
	t.Printf("\nseed: %d\n", seed)

	st, evs, err := engine.NewGame(engine.Config{CardsPerPlayer: cards}, ids, engine.NewSeededShuffler(seed))
	if err != nil {
		t.Error(err)
		return nil, false
	}
	s := &Session{
		st:      st,
		players: players,
		names:   nameBook{},
		io:      t,
		debug:   debug,
		rng:     rand.New(rand.NewSource(seed)),
	}
	for _, p := range players {
		s.names[p.ID] = p.Name
	}
	s.record(evs)
	return s, true
}

const banner = `
 ██  COMBIO  ██   local table
 lowest total wins · Ace = -1 · you must remember your own cards
 cards are shown as position:[deck colour] — never their rank
`

// ---------- plumbing ----------

func (s *Session) player(id engine.PlayerID) Player {
	for _, p := range s.players {
		if p.ID == id {
			return p
		}
	}
	return Player{ID: id, Name: string(id)}
}

// view is the ONLY way this file reads game state.
func (s *Session) view(id engine.PlayerID) engine.PlayerView { return s.st.ViewFor(id) }

// anyView is used for public facts (phase, whose turn, the middle card).
func (s *Session) anyView() engine.PlayerView { return s.view(s.players[0].ID) }

func (s *Session) phase() string { return s.anyView().Phase }

// apply runs an action and files the public events into the feed. Private
// events are returned to the caller, who shows them to their owner alone.
func (s *Session) apply(a engine.Action) ([]engine.Event, bool) {
	ns, evs, err := engine.Apply(s.st, a)
	if err != nil {
		s.io.Error(err)
		return nil, false
	}
	s.st = ns
	s.lastAction = a.Name()
	s.record(evs)
	if s.onEvents != nil {
		s.onEvents(evs)
	}
	// Each bot privately files away only the events addressed to it.
	for _, p := range s.players {
		if p.Bot {
			s.brainFor(p.ID).Remember(engine.FilterEvents(evs, p.ID))
		}
	}
	return evs, true
}

func (s *Session) record(evs []engine.Event) {
	for _, e := range evs {
		if e.Audience() != nil {
			continue // private: only its recipient ever sees it
		}
		if line := Describe(e, s.names); line != "" {
			s.feed = append(s.feed, line)
		}
	}
}

// showPrivate prints the events this player is entitled to, then waits.
func (s *Session) showPrivate(p Player, evs []engine.Event) {
	mine := engine.FilterEvents(evs, p.ID)
	shown := false
	for _, e := range mine {
		if e.Audience() == nil {
			continue
		}
		if line := Describe(e, s.names); line != "" {
			if !shown {
				s.io.Println("")
				shown = true
			}
			s.io.Printf("  >>> %s\n", line)
		}
	}
	if shown && !p.Bot {
		s.io.Pause("  (press ENTER once you have memorised it) ")
	}
}

// feedTail prints the last few public events.
func (s *Session) feedTail(n int) {
	if len(s.feed) == 0 {
		return
	}
	start := len(s.feed) - n
	if start < 0 {
		start = 0
	}
	s.io.Println(" recent:")
	for _, l := range s.feed[start:] {
		s.io.Printf("   · %s\n", l)
	}
}

// handOver clears the screen and waits until only this player is looking.
func (s *Session) handOver(p Player) {
	if p.Bot {
		return
	}
	s.io.ClearScreen()
	s.io.Printf("\n\n   Pass the device to: %s\n\n", p.Name)
	s.io.Pause("   Press ENTER when only you can see the screen. ")
	s.io.ClearScreen()
}

// handBack wipes the private screen before the device moves on.
func (s *Session) handBack(p Player) {
	if p.Bot {
		return
	}
	s.io.Pause("\n   Done. Press ENTER to hide your screen. ")
	s.io.ClearScreen()
	s.io.Printf("\n   %s's turn is over. Pass the device on.\n", p.Name)
}

// privateBoard draws the table from this player's seat.
func (s *Session) privateBoard(p Player) engine.PlayerView {
	v := s.view(p.ID)
	s.io.Println(Board(v, s.names, true))
	if s.debug {
		s.io.Println(DebugDump(&s.st))
	}
	return v
}

func (s *Session) publicBoard() {
	s.io.ClearScreen()
	v := s.anyView()
	// Drawn from seat 0's view with private = false, so nothing personal shows.
	s.io.Println(Board(v, s.names, false))
	s.feedTail(6)
}

// ---------- main loop ----------

func (s *Session) Run() {
	for !s.quit && !s.io.EOF() {
		v := s.anyView()
		if v.Finished {
			s.results()
			return
		}
		switch v.Phase {
		case "memory":
			s.memoryPhase()
		case "turn_start":
			s.turn()
		case "draw_decision":
			s.drawDecision(s.player(v.TurnPlayer))
		case "matching":
			s.matchingWindow()
		case "power", "king_decision":
			s.resolvePower()
		default:
			s.results()
			return
		}
	}
	if s.io.EOF() {
		s.io.Println("\n(session ended early)")
	}
}

func (s *Session) memoryPhase() {
	for _, p := range s.players {
		if s.io.EOF() || s.phase() != "memory" {
			return
		}
		v := s.view(p.ID)
		need := len(v.MyCards) / 2
		s.handOver(p)
		if p.Bot {
			pos := make([]int, 0, need)
			for i := 0; i < need; i++ {
				pos = append(pos, i)
			}
			s.apply(engine.InspectMemory{Base: engine.For(p.ID), Positions: pos})
			s.io.Printf(" %s memorised %d cards.\n", p.Name, need)
			continue
		}
		s.privateBoard(p)
		s.io.Printf(" MEMORY PHASE — choose %d of your %d cards to look at.\n", need, len(v.MyCards))
		pos := s.io.Ints(" positions", need, 0, len(v.MyCards)-1)
		evs, ok := s.apply(engine.InspectMemory{Base: engine.For(p.ID), Positions: pos})
		if !ok {
			continue
		}
		s.showPrivate(p, evs)
		s.handBack(p)
	}
}

func (s *Session) turn() {
	v := s.anyView()
	cur := s.player(v.TurnPlayer)
	s.publicBoard()
	if !cur.Bot {
		s.io.Printf("\n It is %s's turn.\n", cur.Name)
	}
	s.handOver(cur)

	if cur.Bot {
		s.botTurnStart(cur)
		return
	}

	pv := s.privateBoard(cur)
	s.feedTail(5)

	opts := []Option{{"d", "Draw a card"}}
	if pv.DeclareAvailable {
		opts = append(opts, Option{"c", "Declare COMBIO"})
	}
	opts = append(opts,
		Option{"x", "Simulate a disconnect"},
		Option{"q", "Quit the game"},
	)
	switch s.io.Choose(" your move", opts) {
	case "c":
		if _, ok := s.apply(engine.DeclareCombio{Base: engine.For(cur.ID)}); ok {
			s.io.Printf("\n *** %s declares COMBIO! Cards down \u2014 that settles it. ***\n", cur.Name)
		}
		s.handBack(cur)
	case "x":
		s.handBack(cur)
		s.simulateDisconnect()
	case "q":
		s.quit = true
	default:
		evs, ok := s.apply(engine.DrawCard{Base: engine.For(cur.ID)})
		if !ok {
			return
		}
		s.showPrivate(cur, evs)
		s.drawDecisionFor(cur)
	}
}

func (s *Session) drawDecision(p Player) {
	s.handOver(p)
	s.drawDecisionFor(p)
}

func (s *Session) drawDecisionFor(p Player) {
	if p.Bot {
		s.botDrawDecision(p)
		return
	}
	v := s.view(p.ID)
	if v.MyDrawnCard == nil {
		return
	}
	s.io.Printf("\n You are holding %s. Your cards: %s\n", CardText(*v.MyDrawnCard), handles(v.MyCards))

	if len(v.MyCards) == 0 {
		if s.io.YesNo(" Keep it as your first card?") {
			s.apply(engine.KeepDrawn{Base: engine.For(p.ID)})
		} else {
			s.apply(engine.DiscardDrawn{Base: engine.For(p.ID)})
		}
		s.handBack(p)
		return
	}

	switch s.io.Choose(" keep or discard", []Option{
		{"k", "Keep it — swap out one of your cards"},
		{"d", "Discard it (a 10/J/Q/K discarded this way fires its power)"},
	}) {
	case "k":
		rm := s.io.Int(" which of your positions leaves the hand", 0, len(v.MyCards)-1)
		s.io.Printf(" after removal you will have %d cards; the new card can go anywhere.\n", len(v.MyCards)-1)
		ins := s.io.Int(" insert the new card at position", 0, len(v.MyCards)-1)
		s.apply(engine.KeepDrawn{Base: engine.For(p.ID), RemoveIndex: rm, InsertIndex: ins})
	default:
		s.apply(engine.DiscardDrawn{Base: engine.For(p.ID)})
	}
	s.handBack(p)
}

// simulateDisconnect exercises the pause / reconnect / removal path locally.
func (s *Session) simulateDisconnect() {
	s.io.Println("\n DISCONNECT SIMULATION")
	for i, p := range s.players {
		s.io.Printf("   %d) %s\n", i, p.Name)
	}
	idx := s.io.Int(" which player drops", 0, len(s.players)-1)
	victim := s.players[idx]
	if _, ok := s.apply(engine.PlayerDisconnected{Base: engine.For(victim.ID)}); !ok {
		return
	}
	s.io.Printf(" %s dropped. The game is paused and every timer is frozen.\n", victim.Name)
	if s.io.YesNo(" Do they reconnect within 30 seconds?") {
		s.apply(engine.PlayerReconnected{Base: engine.For(victim.ID)})
		s.io.Printf(" %s is back; play resumes exactly where it stopped.\n", victim.Name)
		return
	}
	s.apply(engine.RemovePlayer{Base: engine.For(victim.ID)})
	s.io.Printf(" %s is out. Their cards sank to the bottom of the discard pile,\n", victim.Name)
	s.io.Println(" face down — they will come back through the next reshuffle.")
	s.io.Pause(" (press ENTER) ")
}

func (s *Session) results() {
	s.io.ClearScreen()
	v := s.anyView()
	s.io.Println(Results(v, s.names))
	if s.debug {
		s.io.Println(DebugDump(&s.st))
	}
	s.io.Println(" full event log:")
	for _, l := range s.feed {
		s.io.Printf("   · %s\n", l)
	}
	s.quit = true
}

// Feed exposes the public event log (used by the replay command).
func (s *Session) Feed() []string { return append([]string(nil), s.feed...) }

// Summary is a one-line result, used by scripted runs.
func (s *Session) Summary() string {
	v := s.anyView()
	if !v.Finished {
		return "unfinished"
	}
	ws := make([]string, 0, len(v.Winners))
	for _, w := range v.Winners {
		ws = append(ws, s.names.of(w))
	}
	return fmt.Sprintf("winner(s): %s  scores: %v", strings.Join(ws, ", "), v.Scores)
}
