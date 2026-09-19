package engine

import (
	"errors"
	"fmt"
	"strings"
)

type PlayerID string

// Phase is the engine's state-machine node (§16).
type Phase uint8

const (
	PhaseMemory       Phase = iota // players are inspecting half their cards
	PhaseTurnStart                 // current player may declare or draw
	PhaseDrawDecision              // current player holds a private drawn card
	PhaseMatching                  // matching window open to everyone
	PhasePower                     // discarded drawn power card is resolving
	PhaseKingDecision              // King peeked; actor chooses swap or cancel
	PhaseFinished
)

func (p Phase) String() string {
	switch p {
	case PhaseMemory:
		return "memory"
	case PhaseTurnStart:
		return "turn_start"
	case PhaseDrawDecision:
		return "draw_decision"
	case PhaseMatching:
		return "matching"
	case PhasePower:
		return "power"
	case PhaseKingDecision:
		return "king_decision"
	default:
		return "finished"
	}
}

// Config is fixed at table creation.
type Config struct {
	CardsPerPlayer int // 4, 6 or 8
	NumDecks       int // derived from player count unless overridden
}

// PlayerState is everything the server knows about one seat.
type PlayerState struct {
	ID             PlayerID
	Seat           int
	Layout         Layout
	PendingPenalty []CardID // §6 tray: held, not yet placed, untargetable
	Connected      bool
	Removed        bool
	TurnsCompleted int
	MemoryDone     bool
}

func (p *PlayerState) Active() bool { return !p.Removed }

// Targetable implements §7: a player holding zero cards cannot be the target of
// Jack, Queen, King or opponent-card matching. Tray cards do not count.
func (p *PlayerState) Targetable() bool { return p.Active() && p.Layout.Len() > 0 }

func (p *PlayerState) clone() PlayerState {
	n := *p
	n.Layout = p.Layout.clone()
	n.PendingPenalty = append([]CardID(nil), p.PendingPenalty...)
	return n
}

// kingPeekState remembers what the King user privately saw so the follow-up
// swap decision refers to the same physical card.
type kingPeekState struct {
	Target     PlayerID
	TargetCard CardID
}

// State is the complete authoritative game. It is treated as a value: Apply
// clones it, mutates the clone, and returns the clone, so a rejected action can
// never corrupt the caller's state.
type State struct {
	Config Config

	// Cards is the immutable master table of every physical card in the game.
	// It is never mutated after setup and is shared across clones.
	Cards map[CardID]Card

	Players []PlayerState // index == seat, clockwise
	TurnIdx int

	Phase Phase

	Deck    []CardID // draw deck; last element is the top
	Discard []CardID // index 0 is the bottom, last element is the active top

	ActiveRank      Rank // non-zero only while PhaseMatching
	MatchExtensions int  // accepted matches this window (+2s each, §5)

	Drawn   CardID // private drawn card; non-zero only in PhaseDrawDecision
	DrawnBy PlayerID

	PendingPower Rank // power awaiting resolution after the window closes
	PowerActor   PlayerID
	KingPeek     *kingPeekState

	Declared bool
	Declarer PlayerID

	Paused       bool
	Disconnected map[PlayerID]bool

	// Round accounting for the COMBIO gate (§10). RoundsCompleted counts rounds
	// that were ACTUALLY completed by real turns. RoundPending is who still owes
	// a real turn in the round currently in progress.
	RoundsCompleted int
	RoundPending    []PlayerID

	DeclareUnlockedFlag bool // latched once the two-round gate opens

	Scores  map[PlayerID]int
	Winners []PlayerID

	// shuffler is the injected randomness source. It lives on the State so the
	// transition API stays exactly Apply(State, Action) -> (State, []Event, error).
	shuffler Shuffler
}

// SetShuffler swaps the randomness source (used by the scenario builder).
func (s *State) SetShuffler(sh Shuffler) { s.shuffler = sh }

// Clone returns a deep copy. It exists so a caller can take a stable snapshot
// of a live game — for diagnostics, or to inspect state off the goroutine that
// owns it — without any risk of racing the owner.
func (s State) Clone() State { return s.clone() }

func (s *State) clone() State {
	n := *s
	n.Players = make([]PlayerState, len(s.Players))
	for i := range s.Players {
		n.Players[i] = s.Players[i].clone()
	}
	n.Deck = append([]CardID(nil), s.Deck...)
	n.Discard = append([]CardID(nil), s.Discard...)
	n.RoundPending = append([]PlayerID(nil), s.RoundPending...)
	n.Winners = append([]PlayerID(nil), s.Winners...)
	n.Disconnected = map[PlayerID]bool{}
	for k, v := range s.Disconnected {
		n.Disconnected[k] = v
	}
	if s.Scores != nil {
		n.Scores = map[PlayerID]int{}
		for k, v := range s.Scores {
			n.Scores[k] = v
		}
	}
	if s.KingPeek != nil {
		kp := *s.KingPeek
		n.KingPeek = &kp
	}
	return n
}

// ---------- lookups ----------

func (s *State) Player(id PlayerID) *PlayerState {
	for i := range s.Players {
		if s.Players[i].ID == id {
			return &s.Players[i]
		}
	}
	return nil
}

func (s *State) Card(id CardID) (Card, bool) {
	c, ok := s.Cards[id]
	return c, ok
}

func (s *State) MustCard(id CardID) Card { return s.Cards[id] }

func (s *State) Current() *PlayerState {
	if s.TurnIdx < 0 || s.TurnIdx >= len(s.Players) {
		return nil
	}
	return &s.Players[s.TurnIdx]
}

func (s *State) CurrentID() PlayerID {
	if p := s.Current(); p != nil {
		return p.ID
	}
	return ""
}

func (s *State) ActivePlayers() []*PlayerState {
	out := make([]*PlayerState, 0, len(s.Players))
	for i := range s.Players {
		if s.Players[i].Active() {
			out = append(out, &s.Players[i])
		}
	}
	return out
}

func (s *State) ActiveCount() int { return len(s.ActivePlayers()) }

// DiscardTop is the active/visible card in the middle. The discard pile is
// never empty after setup.
func (s *State) DiscardTop() CardID {
	if len(s.Discard) == 0 {
		return NoCard
	}
	return s.Discard[len(s.Discard)-1]
}

// DeclareUnlocked implements §10: two COMPLETE global rounds must actually have
// been played.
//
// A round completes only when every player who owed a turn in it has really
// taken one. A removal never completes a round: if the player who left still
// owed a turn, that round is discarded as broken and a fresh one starts with the
// remaining players. Removing someone therefore cannot retroactively turn an
// unfinished round into a finished one, and the gate never opens early.
//
// RoundsCompleted only ever increases, so the gate never closes again either.
func (s *State) DeclareUnlocked() bool { return s.RoundsCompleted >= 2 }

// startRound seeds the pending set for a new round from the live roster.
func (s *State) startRound() {
	s.RoundPending = nil
	for _, p := range s.ActivePlayers() {
		s.RoundPending = append(s.RoundPending, p.ID)
	}
}

// owesTurn reports whether this player still has a turn outstanding this round.
func (s *State) owesTurn(id PlayerID) bool {
	for _, p := range s.RoundPending {
		if p == id {
			return true
		}
	}
	return false
}

// recordTurn marks a REAL completed turn against the current round, completing
// the round when nobody is left owing one.
func (s *State) recordTurn(id PlayerID) {
	out := s.RoundPending[:0]
	for _, p := range s.RoundPending {
		if p != id {
			out = append(out, p)
		}
	}
	s.RoundPending = out
	if len(s.RoundPending) == 0 {
		s.RoundsCompleted++
		s.startRound()
	}
}

func (s *State) Debug() string {
	var b strings.Builder
	fmt.Fprintf(&b, "phase=%s turn=%s deck=%d discard=%d rank=%s declared=%v paused=%v rounds=%d pending=%v",
		s.Phase, s.CurrentID(), len(s.Deck), len(s.Discard), s.ActiveRank, s.Declared, s.Paused,
		s.RoundsCompleted, s.RoundPending)
	for i := range s.Players {
		p := &s.Players[i]
		fmt.Fprintf(&b, " | %s(seat%d active=%v cards=%d tray=%d turns=%d)",
			p.ID, p.Seat, p.Active(), p.Layout.Len(), len(p.PendingPenalty), p.TurnsCompleted)
	}
	return b.String()
}

// ---------- pile helpers ----------

// pushDiscardTop puts a card on top of the pile, making it the visible card.
func (s *State) pushDiscardTop(id CardID) { s.Discard = append(s.Discard, id) }

// buryDiscard slides a card UNDER the current top (§5). Matched cards and wrong
// attempts go here: they join the pile but never become the active rank and
// never open a new window.
func (s *State) buryDiscard(id CardID) {
	if len(s.Discard) == 0 {
		s.Discard = append(s.Discard, id)
		return
	}
	top := s.Discard[len(s.Discard)-1]
	s.Discard = append(s.Discard[:len(s.Discard)-1], id, top)
}

// sinkToBottom places cards face-down at the very BOTTOM of the pile (§12).
// This is where a removed player's cards go: never revealed, never a matching
// window, but still physically in the game and returned by the next reshuffle.
func (s *State) sinkToBottom(ids []CardID) {
	if len(ids) == 0 {
		return
	}
	s.Discard = append(append([]CardID(nil), ids...), s.Discard...)
}

// reshuffle implements §9: keep the active top card, shuffle everything else
// back into the draw deck.
func (s *State) reshuffle() bool {
	sh := s.shuffler
	if sh == nil {
		sh = NoShuffle{}
	}
	if len(s.Discard) <= 1 {
		return false
	}
	top := s.Discard[len(s.Discard)-1]
	rest := append([]CardID(nil), s.Discard[:len(s.Discard)-1]...)
	sh.Shuffle(rest)
	s.Deck = append(s.Deck, rest...)
	s.Discard = []CardID{top}
	return true
}

// drawOne pops the top of the draw deck, reshuffling first if needed.
// Returns NoCard when no card can physically be produced; callers decide
// whether that is a legal short-draw (penalties, §6) or an impossible state
// (a turn draw, §9).
func (s *State) drawOne() (CardID, bool) {
	reshuffled := false
	if len(s.Deck) == 0 {
		reshuffled = s.reshuffle()
	}
	if len(s.Deck) == 0 {
		return NoCard, reshuffled
	}
	id := s.Deck[len(s.Deck)-1]
	s.Deck = s.Deck[:len(s.Deck)-1]
	return id, reshuffled
}

// ---------- setup ----------

var (
	ErrPlayerCount = errors.New("COMBIO supports 2-8 players")
	ErrHandSize    = errors.New("cards per player must be 4, 6 or 8")
)

// NewGame deals a fresh table and stops in PhaseMemory.
func NewGame(cfg Config, ids []PlayerID, sh Shuffler) (State, []Event, error) {
	if len(ids) < 2 || len(ids) > 8 {
		return State{}, nil, ErrPlayerCount
	}
	if cfg.CardsPerPlayer != 4 && cfg.CardsPerPlayer != 6 && cfg.CardsPerPlayer != 8 {
		return State{}, nil, ErrHandSize
	}
	if cfg.NumDecks == 0 {
		cfg.NumDecks = DecksFor(len(ids))
	}
	cards, err := BuildDeck(cfg.NumDecks)
	if err != nil {
		return State{}, nil, err
	}
	if len(cards) < len(ids)*cfg.CardsPerPlayer+1 {
		return State{}, nil, errors.New("not enough cards for that table")
	}

	s := State{
		Config:       cfg,
		shuffler:     sh,
		Cards:        make(map[CardID]Card, len(cards)),
		Disconnected: map[PlayerID]bool{},
		Phase:        PhaseMemory,
	}
	deck := make([]CardID, 0, len(cards))
	for _, c := range cards {
		s.Cards[c.ID] = c
		deck = append(deck, c.ID)
	}
	sh.Shuffle(deck)
	s.Deck = deck

	for seat, id := range ids {
		hand := make([]CardID, 0, cfg.CardsPerPlayer)
		for i := 0; i < cfg.CardsPerPlayer; i++ {
			id, _ := s.drawOne()
			hand = append(hand, id)
		}
		s.Players = append(s.Players, PlayerState{
			ID:        id,
			Seat:      seat,
			Layout:    NewLayout(hand),
			Connected: true,
		})
	}

	s.startRound()

	// §2: the initial face-up discard sets no rank and triggers no power.
	first, _ := s.drawOne()
	s.pushDiscardTop(first)
	s.ActiveRank = RankNone

	evs := []Event{EvGameStarted{Players: append([]PlayerID(nil), ids...), CardsPerPlayer: cfg.CardsPerPlayer, NumDecks: cfg.NumDecks}}
	evs = append(evs, EvMemoryPhaseStarted{Inspect: cfg.CardsPerPlayer / 2})
	return s, evs, nil
}
