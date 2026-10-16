// Command difftrace plays random games through the Go engine and writes every
// step as JSON lines: the wire action that went in, and either the error that
// came out or each player's filtered view and events.
//
// The TypeScript port in worker/ replays the same trace and must reproduce
// every line exactly. That is how the port is held to "same rules, same wire
// format" without hand-translating every Go test.
//
//	go run ./cmd/difftrace -games 500 -seed 1 > trace.jsonl
package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"math/rand"
	"os"

	"github.com/kaushlendra/combio/backend/internal/engine"
	"github.com/kaushlendra/combio/backend/internal/protocol"
)

type gameLine struct {
	Type    string            `json:"type"`
	Players []engine.PlayerID `json:"players"`
	Cards   int               `json:"cards"`
	Order   []engine.CardID   `json:"order"`
	Views   map[engine.PlayerID]engine.PlayerView     `json:"views"`
	Events  map[engine.PlayerID][]protocol.WireEvent `json:"events"`
}

type wireAction struct {
	Actor  engine.PlayerID `json:"actor,omitempty"`
	Kind   string          `json:"kind,omitempty"`
	Args   json.RawMessage `json:"args,omitempty"`
	System string          `json:"system,omitempty"`
	Player engine.PlayerID `json:"player,omitempty"`
	Timer  string          `json:"timer,omitempty"`
}

type stepLine struct {
	Type   string                                   `json:"type"`
	Action wireAction                               `json:"action"`
	Error  *protocol.ErrorData                      `json:"error,omitempty"`
	Views  map[engine.PlayerID]engine.PlayerView     `json:"views,omitempty"`
	Events map[engine.PlayerID][]protocol.WireEvent `json:"events,omitempty"`
}

var kinds = []string{
	protocol.AInspectMemory, protocol.ADeclareCombio, protocol.ADrawCard, protocol.AKeepDrawn,
	protocol.ADiscardDrawn, protocol.AAttemptOwnMatch, protocol.AAttemptOpponentMatch,
	protocol.AInsertPenalty, protocol.APeek10, protocol.AJackPeek, protocol.AQueenSwap,
	protocol.AKingPeek, protocol.AKingSwap, protocol.ACancelKingSwap, protocol.ASkipPower,
}

// phaseKinds are the actions that make sense in each phase, so random play
// actually progresses instead of bouncing off wrong_phase forever.
var phaseKinds = map[engine.Phase][]string{
	engine.PhaseMemory:       {protocol.AInspectMemory},
	engine.PhaseTurnStart:    {protocol.ADrawCard, protocol.ADrawCard, protocol.ADeclareCombio},
	engine.PhaseDrawDecision: {protocol.AKeepDrawn, protocol.ADiscardDrawn},
	engine.PhaseMatching: {protocol.AAttemptOwnMatch, protocol.AAttemptOpponentMatch,
		protocol.AInsertPenalty},
	engine.PhasePower: {protocol.APeek10, protocol.AJackPeek, protocol.AQueenSwap,
		protocol.AKingPeek, protocol.ASkipPower},
	engine.PhaseKingDecision: {protocol.AKingSwap, protocol.ACancelKingSwap},
}

var timerFor = map[engine.Phase]engine.TimerKind{
	engine.PhaseMemory:       engine.TimerMemory,
	engine.PhaseTurnStart:    engine.TimerTurn,
	engine.PhaseDrawDecision: engine.TimerTurn,
	engine.PhaseMatching:     engine.TimerMatchWindow,
	engine.PhasePower:        engine.TimerPower,
	engine.PhaseKingDecision: engine.TimerPower,
}

func main() {
	games := flag.Int("games", 200, "how many games to play")
	seed := flag.Int64("seed", 1, "random seed")
	maxSteps := flag.Int("steps", 600, "step limit per game")
	flag.Parse()

	rng := rand.New(rand.NewSource(*seed))
	out := bufio.NewWriter(os.Stdout)
	defer out.Flush()
	enc := json.NewEncoder(out)

	for g := 0; g < *games; g++ {
		n := 2 + rng.Intn(7)
		ids := make([]engine.PlayerID, n)
		for i := range ids {
			ids[i] = engine.PlayerID(fmt.Sprintf("p%d", i+1))
		}
		cards := []int{4, 6, 8}[rng.Intn(3)]
		deckSize := 52 * engine.DecksFor(n)
		order := make([]engine.CardID, deckSize)
		for i, p := range rng.Perm(deckSize) {
			order[i] = engine.CardID(p + 1)
		}
		st, evs, err := engine.NewGame(engine.Config{CardsPerPlayer: cards}, ids,
			&engine.StackedShuffler{Order: order})
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		views, events := render(&st, evs)
		_ = enc.Encode(gameLine{Type: "game", Players: ids, Cards: cards, Order: order,
			Views: views, Events: events})

		for step := 0; step < *maxSteps && st.Phase != engine.PhaseFinished; step++ {
			wa, a := pick(rng, &st, ids)
			line := stepLine{Type: "step", Action: wa}
			if a == nil {
				// The wire action does not even build; record what the server says.
				_, berr := protocol.BuildAction(wa.Kind, wa.Args, wa.Actor)
				line.Error = &protocol.ErrorData{Code: protocol.ErrorCode(berr), Message: berr.Error()}
				_ = enc.Encode(line)
				continue
			}
			ns, evs, err := engine.Apply(st, a)
			if err != nil {
				line.Error = &protocol.ErrorData{Code: protocol.ErrorCode(err), Message: err.Error()}
			} else {
				st = ns
				line.Views, line.Events = render(&st, evs)
			}
			_ = enc.Encode(line)
		}
	}
}

func render(st *engine.State, evs []engine.Event) (map[engine.PlayerID]engine.PlayerView, map[engine.PlayerID][]protocol.WireEvent) {
	views := map[engine.PlayerID]engine.PlayerView{}
	events := map[engine.PlayerID][]protocol.WireEvent{}
	for _, p := range st.Players {
		views[p.ID] = st.ViewFor(p.ID)
		events[p.ID] = protocol.EncodeEvents(engine.FilterEvents(evs, p.ID))
	}
	return views, events
}

// pick chooses the next input: usually a plausible player action, sometimes a
// timer or a connection change, occasionally garbage arguments.
func pick(rng *rand.Rand, st *engine.State, ids []engine.PlayerID) (wireAction, engine.Action) {
	r := rng.Float64()
	switch {
	case r < 0.04:
		p := ids[rng.Intn(len(ids))]
		sys := []string{"player_disconnected", "player_reconnected", "remove_player"}[rng.Intn(3)]
		if sys == "remove_player" && rng.Intn(3) != 0 {
			sys = "player_reconnected"
		}
		wa := wireAction{System: sys, Player: p}
		switch sys {
		case "player_disconnected":
			return wa, engine.PlayerDisconnected{Base: engine.For(p)}
		case "player_reconnected":
			return wa, engine.PlayerReconnected{Base: engine.For(p)}
		default:
			return wa, engine.RemovePlayer{Base: engine.For(p)}
		}
	case r < 0.07 && st.Paused:
		// Keep paused games moving: bring everyone back.
		for p := range st.Disconnected {
			return wireAction{System: "player_reconnected", Player: p}, engine.PlayerReconnected{Base: engine.For(p)}
		}
	case r < 0.16:
		kind := timerFor[st.Phase]
		if rng.Intn(5) == 0 {
			kind = engine.TimerKind(rng.Intn(4))
		}
		return wireAction{System: "timer_expired", Timer: kind.String()}, engine.TimerExpired{Kind: kind}
	}

	actor := ids[rng.Intn(len(ids))]
	if rng.Intn(2) == 0 {
		switch {
		case st.PowerActor != "":
			actor = st.PowerActor
		case st.Phase != engine.PhaseMemory && st.Phase != engine.PhaseMatching:
			actor = st.CurrentID()
		}
	}
	kind := kinds[rng.Intn(len(kinds))]
	if ks := phaseKinds[st.Phase]; len(ks) > 0 && rng.Intn(10) < 8 {
		kind = ks[rng.Intn(len(ks))]
	}

	args := randomArgs(rng, st, actor, ids)
	raw, _ := json.Marshal(args)
	if rng.Intn(40) == 0 {
		raw = garbage(rng)
	}
	if rng.Intn(30) == 0 {
		raw = nil
	}
	wa := wireAction{Actor: actor, Kind: kind, Args: raw}
	if rng.Intn(60) == 0 {
		wa.Kind = "fly_away"
	}
	a, err := protocol.BuildAction(wa.Kind, wa.Args, actor)
	if err != nil {
		return wa, nil
	}
	return wa, a
}

func randomArgs(rng *rand.Rand, st *engine.State, actor engine.PlayerID, ids []engine.PlayerID) map[string]any {
	me := st.Player(actor)
	myLen := 0
	var myVer uint64
	if me != nil {
		myLen = me.Layout.Len()
		myVer = me.Layout.Version()
	}
	idx := func(n int) int {
		if rng.Intn(8) == 0 {
			return rng.Intn(12) - 2
		}
		if n <= 0 {
			return 0
		}
		return rng.Intn(n)
	}
	ver := func(cur uint64) uint64 {
		switch rng.Intn(6) {
		case 0:
			return 0
		case 1:
			if cur > 1 {
				return cur - 1
			}
			return cur
		case 2:
			return uint64(rng.Intn(40))
		default:
			return cur
		}
	}
	target := ids[rng.Intn(len(ids))]
	if rng.Intn(15) == 0 {
		target = "nobody"
	}
	t := st.Player(target)
	tLen := 0
	var tVer uint64
	if t != nil {
		tLen = t.Layout.Len()
		tVer = t.Layout.Version()
	}
	positions := rng.Perm(myLen + 1)
	half := myLen / 2
	if rng.Intn(6) == 0 {
		half = rng.Intn(myLen + 2)
	}
	if half > len(positions) {
		half = len(positions)
	}
	positions = positions[:half]
	if rng.Intn(10) == 0 && len(positions) > 1 {
		positions[1] = positions[0]
	}
	tray := 0
	if me != nil {
		tray = len(me.PendingPenalty)
	}
	return map[string]any{
		"positions":      positions,
		"remove_index":   idx(myLen),
		"insert_index":   idx(myLen),
		"index":          idx(myLen),
		"version":        ver(myVer),
		"target":         target,
		"target_index":   idx(tLen),
		"target_version": ver(tVer),
		"give_index":     idx(myLen),
		"give_version":   ver(myVer),
		"own_index":      idx(myLen),
		"tray_index":     idx(tray),
		"position":       idx(myLen + 1),
		"player":         ids[0], // must be ignored: the actor is never client input
	}
}

func garbage(rng *rand.Rand) json.RawMessage {
	options := []string{
		`{"index":"two"}`, `{"version":-1}`, `{"positions":[1,"x"]}`, `{"positions":5}`,
		`{"target":7}`, `[1,2]`, `"hello"`, `null`, `{"Index":1,"OWN_INDEX":0}`, `{"index":1.5}`,
		`{"remove_index":null,"insert_index":0}`, `true`, `{}`,
	}
	return json.RawMessage(options[rng.Intn(len(options))])
}
