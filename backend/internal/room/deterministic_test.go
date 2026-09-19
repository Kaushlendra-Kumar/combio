package room

import (
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/kaushlendra/combio/backend/internal/engine"
	"github.com/kaushlendra/combio/backend/internal/protocol"
)

// raceSetup: a1 is on turn and about to discard an 8. b1, c1 and d1 each hold
// an 8, and c1's is the one everybody is going to fight over.
func raceSetup(t *testing.T) *table {
	t.Helper()
	tb := newTable(t, 4, 4, 7)
	tb.startGame(t)
	tb.inject(t, engine.Scenario{
		Players: []engine.PlayerID{"a1", "b1", "c1", "d1"},
		Hands: map[engine.PlayerID][]string{
			"a1": {"2C", "3D"},
			"b1": {"8S", "9C"},
			"c1": {"8H", "4D"},
			"d1": {"8C", "5S"},
		},
		Deck:       []string{"8D"},
		DiscardTop: "TH",
		Unlocked:   true,
	})
	tb.act("a1", protocol.ADrawCard, nil)
	tb.act("a1", protocol.ADiscardDrawn, nil)
	if tb.phase() != "matching" {
		t.Fatalf("expected a matching window, phase is %s", tb.phase())
	}
	return tb
}

// Two clients go for the same physical card at the same instant. The room loop
// picks an order; the loser is stale and pays nothing.
func TestTwoSimultaneousMatchRequestsOnOneCard(t *testing.T) {
	tb := raceSetup(t)
	var version uint64
	tb.stateOf(func(s *engine.State) { version = s.Player("c1").Layout.Version() })
	tb.resetAll()

	args, _ := json.Marshal(map[string]any{
		"target": "c1", "target_index": 0, "target_version": version, "give_index": 0,
	})
	var wg sync.WaitGroup
	for _, id := range []engine.PlayerID{"b1", "d1"} {
		wg.Add(1)
		go func(p engine.PlayerID) {
			defer wg.Done()
			tb.room.Act(p, protocol.ActionData{Kind: protocol.AAttemptOpponentMatch, Args: args}, "race")
		}(id)
	}
	wg.Wait()
	tb.settle()

	wins, stale := 0, 0
	for _, id := range []string{"b1", "d1"} {
		c := tb.client(id)
		switch {
		case len(c.errors()) == 0:
			wins++
		case c.lastErrorCode() == "stale":
			stale++
		default:
			t.Fatalf("%s got %q; a lost race must be stale", id, c.lastErrorCode())
		}
	}
	if wins != 1 || stale != 1 {
		t.Fatalf("race resolved as %d wins / %d stale", wins, stale)
	}
	tb.stateOf(func(s *engine.State) {
		// c1's 8 left play, and c1 received a card into the exact gap.
		if s.Player("c1").Layout.Len() != 2 {
			t.Fatalf("c1 holds %d cards, want 2", s.Player("c1").Layout.Len())
		}
		if s.Hand("c1")[0] == "8H" {
			t.Fatal("c1's 8 should have been knocked out")
		}
		for _, id := range []engine.PlayerID{"b1", "d1"} {
			if n := len(s.Player(id).PendingPenalty); n != 0 {
				t.Fatalf("%s was penalised (%d cards) for a legitimate race", id, n)
			}
		}
		// Exactly one giver is a card lighter.
		light := 0
		for _, id := range []engine.PlayerID{"b1", "d1"} {
			if s.Player(id).Layout.Len() == 1 {
				light++
			}
		}
		if light != 1 {
			t.Fatalf("%d players gave a card away, want 1", light)
		}
	})
}

func TestThreeWayRaceOnTheSamePhysicalCard(t *testing.T) {
	tb := raceSetup(t)
	// Everyone including the discarder goes for c1's 8.
	var version uint64
	tb.stateOf(func(s *engine.State) { version = s.Player("c1").Layout.Version() })
	tb.resetAll()

	args, _ := json.Marshal(map[string]any{
		"target": "c1", "target_index": 0, "target_version": version, "give_index": 0,
	})
	start := make(chan struct{})
	var wg sync.WaitGroup
	contenders := []engine.PlayerID{"a1", "b1", "d1"}
	for _, id := range contenders {
		wg.Add(1)
		go func(p engine.PlayerID) {
			defer wg.Done()
			<-start
			tb.room.Act(p, protocol.ActionData{Kind: protocol.AAttemptOpponentMatch, Args: args}, "race")
		}(id)
	}
	close(start)
	wg.Wait()
	tb.settle()

	wins := 0
	for _, id := range contenders {
		c := tb.client(string(id))
		if len(c.errors()) == 0 {
			wins++
			continue
		}
		if code := c.lastErrorCode(); code != "stale" {
			t.Fatalf("%s got %q, want stale", id, code)
		}
	}
	if wins != 1 {
		t.Fatalf("%d contenders won the same physical card", wins)
	}
	tb.stateOf(func(s *engine.State) {
		if err := engine.CheckInvariants(s); err != nil {
			t.Fatal(err)
		}
	})
}

// Submitting the same match twice is one success and one stale, never a
// double removal and never a penalty.
func TestDoubleSubmitOfTheSameOwnMatch(t *testing.T) {
	tb := raceSetup(t)
	var version uint64
	tb.stateOf(func(s *engine.State) { version = s.Player("b1").Layout.Version() })
	tb.resetAll()

	args, _ := json.Marshal(map[string]any{"index": 0, "version": version})
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			tb.room.Act("b1", protocol.ActionData{Kind: protocol.AAttemptOwnMatch, Args: args}, "dbl")
		}()
	}
	wg.Wait()
	tb.settle()

	if got := tb.client("b1").lastErrorCode(); got != "stale" {
		t.Fatalf("the duplicate submission returned %q, want stale", got)
	}
	tb.stateOf(func(s *engine.State) {
		if s.Player("b1").Layout.Len() != 1 {
			t.Fatalf("b1 holds %d cards; only one 8 should have left", s.Player("b1").Layout.Len())
		}
		if n := len(s.Player("b1").PendingPenalty); n != 0 {
			t.Fatalf("the duplicate cost %d penalty cards", n)
		}
	})
}

// Every accepted match buys the window another two seconds.
func TestSuccessfulMatchExtendsTheWindow(t *testing.T) {
	tb := raceSetup(t)

	// Four seconds of the five-second window gone.
	tb.advance(4 * time.Second)
	if tb.phase() != "matching" {
		t.Fatal("the window closed early")
	}
	tb.act("b1", protocol.AAttemptOwnMatch, map[string]any{"index": 0})
	if tb.phase() != "matching" {
		t.Fatal("the match should have kept the window open")
	}

	// One second later the original deadline passes; the extension holds it.
	tb.advance(1500 * time.Millisecond)
	if tb.phase() != "matching" {
		t.Fatal("the +2s extension was not applied")
	}
	// A second match extends it again.
	tb.act("d1", protocol.AAttemptOwnMatch, map[string]any{"index": 0})
	tb.advance(1500 * time.Millisecond)
	if tb.phase() != "matching" {
		t.Fatal("the second extension was not applied")
	}
	tb.advance(2 * time.Second)
	if tb.phase() == "matching" {
		t.Fatal("the window never closed once the extensions ran out")
	}
}

// A wrong attempt must not extend anything.
func TestWrongAttemptDoesNotExtendTheWindow(t *testing.T) {
	tb := raceSetup(t)
	tb.advance(4 * time.Second)
	tb.act("b1", protocol.AAttemptOwnMatch, map[string]any{"index": 1}) // 9C against rank 8
	tb.advance(1500 * time.Millisecond)
	if tb.phase() == "matching" {
		t.Fatal("a wrong attempt bought extra time")
	}
	tb.stateOf(func(s *engine.State) {
		if s.Player("b1").Layout.Len() != 3 {
			t.Fatalf("b1 should be net +1 after a wrong throw, holds %d", s.Player("b1").Layout.Len())
		}
	})
}

// The power decision has its own timer, and letting it lapse skips the power.
func TestPowerTimeoutSkipsThePower(t *testing.T) {
	tb := newTable(t, 3, 4, 7)
	tb.startGame(t)
	tb.inject(t, engine.Scenario{
		Players: []engine.PlayerID{"a1", "b1", "c1"},
		Hands: map[engine.PlayerID][]string{
			"a1": {"2C", "3D"}, "b1": {"KS", "4H"}, "c1": {"9D", "6S"},
		},
		Deck:       []string{"JC"},
		DiscardTop: "2H",
		Unlocked:   true,
	})
	tb.act("a1", protocol.ADrawCard, nil)
	tb.act("a1", protocol.ADiscardDrawn, nil)
	tb.advance(testTimers().Match)

	if tb.phase() != "power" {
		t.Fatalf("expected the Jack power to be waiting, phase is %s", tb.phase())
	}
	tb.resetAll()
	tb.advance(testTimers().Power - time.Second)
	if tb.phase() != "power" {
		t.Fatal("the power timer fired early")
	}
	tb.advance(2 * time.Second)
	if tb.phase() != "turn_start" {
		t.Fatalf("the power timeout did not end the turn, phase is %s", tb.phase())
	}
	if !tb.client("a1").sawEvent("power_skipped") {
		t.Fatal("the actor should be told their power lapsed")
	}
	// Nobody learned anything.
	for _, c := range tb.clients {
		if c.sawEvent("private_reveal") {
			t.Fatalf("%s received a reveal from a skipped power", c.id)
		}
	}
}

// A declaration settles the hand in the room as well as the engine: the status
// goes straight to FINISHED, everyone is told, and nothing further is accepted.
func TestDeclarationFinishesTheRoomImmediately(t *testing.T) {
	tb := newTable(t, 4, 4, 7)
	tb.startGame(t)
	tb.room.ReplaceStateForTesting(mustScenario(t, engine.Scenario{
		Players: []engine.PlayerID{"a1", "b1", "c1", "d1"},
		Hands: map[engine.PlayerID][]string{
			"a1": {"AS"}, "b1": {"5D"}, "c1": {"7H"}, "d1": {"9C"},
		},
		Deck:       []string{"2C", "3D", "4H", "6S"},
		DiscardTop: "9H",
		Unlocked:   true,
	}))
	tb.resetAll()

	tb.act("a1", protocol.ADeclareCombio, nil)

	if got := tb.room.Snapshot().Status; got != StatusFinished {
		t.Fatalf("status is %s, want FINISHED", got)
	}
	// Everyone gets the result, with every hand revealed.
	for _, c := range tb.clients {
		m, ok := c.last(protocol.SGameFinished)
		if !ok {
			t.Fatalf("%s never received the result", c.id)
		}
		d := m.Data.(protocol.GameFinishedData)
		if len(d.View.Reveal) != 4 {
			t.Fatalf("%s saw %d hands revealed, want 4", c.id, len(d.View.Reveal))
		}
		if len(d.View.Winners) != 1 || d.View.Winners[0] != "a1" {
			t.Fatalf("winners = %v, want a1 who declared on an Ace", d.View.Winners)
		}
	}
	// Nobody can play on.
	tb.resetAll()
	for _, id := range []string{"b1", "c1", "d1"} {
		tb.act(id, protocol.ADrawCard, nil)
		if code := tb.client(id).lastErrorCode(); code != "game_over" {
			t.Fatalf("%s acting after the hand returned %q", id, code)
		}
	}
	tb.stateOf(func(s *engine.State) {
		if err := engine.CheckInvariants(s); err != nil {
			t.Fatal(err)
		}
		if len(s.Scores) != 4 {
			t.Fatalf("every player should be scored, got %v", s.Scores)
		}
	})
	if got := tb.room.Stats().Conflicts; got != 0 {
		t.Fatalf("engine.Apply overlapped %d times", got)
	}
}

// A player who drops right before someone declares is out of contention; the
// declaration still settles the hand for everyone left.
func TestDeclarationAfterARemovalScoresOnlyTheSurvivors(t *testing.T) {
	tb := newTable(t, 4, 4, 7)
	tb.startGame(t)
	tb.room.ReplaceStateForTesting(mustScenario(t, engine.Scenario{
		Players: []engine.PlayerID{"a1", "b1", "c1", "d1"},
		Hands: map[engine.PlayerID][]string{
			"a1": {"AS"}, "b1": {"5D"}, "c1": {"7H"}, "d1": {"9C"},
		},
		Deck:       []string{"2C", "3D", "4H", "6S"},
		DiscardTop: "9H",
		Unlocked:   true,
	}))

	tb.room.Disconnected("c1")
	tb.settle()
	tb.advance(testTimers().Reconnect + time.Second)

	tb.act("a1", protocol.ADeclareCombio, nil)
	if got := tb.room.Snapshot().Status; got != StatusFinished {
		t.Fatalf("status is %s", got)
	}
	tb.stateOf(func(s *engine.State) {
		if _, scored := s.Scores["c1"]; scored {
			t.Fatal("a removed player was scored")
		}
		if len(s.Scores) != 3 {
			t.Fatalf("expected three scored players, got %v", s.Scores)
		}
		if err := engine.CheckInvariants(s); err != nil {
			t.Fatal(err)
		}
	})
}

// The layout version exists so a real client can tell a lost race from a wrong
// guess. Phase 4 found that a browser had no way to obtain one, so a race loser
// was charged 2 penalty cards instead of being told "stale". These two tests
// pin both halves of that.

// Without a version (what a client had to send before the view exposed one) the
// loser is judged against whatever card now occupies that index.
func TestRaceLoserWithoutAVersionIsJudgedAgainstTheNewCard(t *testing.T) {
	tb := raceSetup(t)
	// b1 hands over their 9C, so the slot the loser clicked now holds a card
	// of the wrong rank.
	winner, _ := json.Marshal(map[string]any{
		"target": "c1", "target_index": 0, "give_index": 1,
	})
	loser, _ := json.Marshal(map[string]any{
		"target": "c1", "target_index": 0, "give_index": 0, // no version
	})
	tb.resetAll()
	tb.room.Act("b1", protocol.ActionData{Kind: protocol.AAttemptOpponentMatch, Args: winner}, "r")
	tb.settle()
	tb.room.Act("d1", protocol.ActionData{Kind: protocol.AAttemptOpponentMatch, Args: loser}, "r")
	tb.settle()

	// Documented, not desired: with no version to send, the loser of a race is
	// judged against whatever card now sits at that index and is charged for
	// it. This is precisely why the view exposes a version.
	if code := tb.client("d1").lastErrorCode(); code != "" {
		t.Fatalf("expected a silent wrong match, got %q", code)
	}
	tb.stateOf(func(s *engine.State) {
		if n := len(s.Player("d1").PendingPenalty); n != 2 {
			t.Fatalf("expected the version-less loser to be penalised, got %d cards", n)
		}
	})
}

// With the version taken from the view, the loser is correctly stale and free.
func TestRaceLoserUsingTheViewVersionIsStaleAndFree(t *testing.T) {
	tb := raceSetup(t)

	// Exactly what a browser has: its own PlayerView.
	st := tb.room.CloneState()
	bView := st.ViewFor("b1")
	dView := st.ViewFor("d1")

	versionOf := func(v engine.PlayerView, id engine.PlayerID) uint64 {
		for _, o := range v.Opponents {
			if o.ID == id {
				return o.Version
			}
		}
		t.Fatalf("%s is not in the view", id)
		return 0
	}
	bVer, dVer := versionOf(bView, "c1"), versionOf(dView, "c1")
	if bVer == 0 || bVer != dVer {
		t.Fatalf("both clients should see the same layout version, got %d and %d", bVer, dVer)
	}

	tb.resetAll()
	for _, actor := range []engine.PlayerID{"b1", "d1"} {
		args, _ := json.Marshal(map[string]any{
			"target": "c1", "target_index": 0, "target_version": bVer, "give_index": 0,
		})
		tb.room.Act(actor, protocol.ActionData{Kind: protocol.AAttemptOpponentMatch, Args: args}, "r")
		tb.settle()
	}

	if got := len(tb.client("b1").errors()); got != 0 {
		t.Fatalf("the winner should not have been refused: %v", tb.client("b1").errors())
	}
	if code := tb.client("d1").lastErrorCode(); code != "stale" {
		t.Fatalf("the race loser got %q, want stale", code)
	}
	tb.stateOf(func(s *engine.State) {
		if n := len(s.Player("d1").PendingPenalty); n != 0 {
			t.Fatalf("the race loser was charged %d penalty cards", n)
		}
	})
}

// The version says nothing about any card. It is a revision counter.
func TestLayoutVersionCarriesNoCardIdentity(t *testing.T) {
	tb := raceSetup(t)
	st := tb.room.CloneState()
	v := st.ViewFor("b1")

	if v.MyVersion == 0 {
		t.Fatal("my own layout version should be exposed too")
	}
	for _, o := range v.Opponents {
		if o.Version == 0 {
			t.Fatalf("%s has no layout version", o.ID)
		}
		if o.Version > 1000 {
			t.Fatalf("%s's version looks like a card id, not a revision", o.ID)
		}
	}
	// A version changes only when that layout changes.
	before := v.MyVersion
	tb.act("b1", protocol.AAttemptOwnMatch, map[string]any{"index": 0})
	st = tb.room.CloneState()
	if after := st.ViewFor("b1").MyVersion; after <= before {
		t.Fatalf("the version did not advance after a change: %d -> %d", before, after)
	}
	if after := st.ViewFor("a1").MyVersion; after != v.Opponents[0].Version && v.Opponents[0].ID == "a1" {
		t.Fatal("an untouched layout's version should not move")
	}
}
