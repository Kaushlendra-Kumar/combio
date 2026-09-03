package room

import (
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/kaushlendra/combio/backend/internal/engine"
	"github.com/kaushlendra/combio/backend/internal/protocol"
)

// fiveWaySetup: a1 opens a rank-8 window, and c1 holds the single physical 8
// that everybody at the table remembers.
func fiveWaySetup(t *testing.T) *table {
	t.Helper()
	tb := newTable(t, 5, 4, 7)
	tb.startGame(t)
	tb.room.ReplaceStateForTesting(mustScenario(t, engine.Scenario{
		Players: []engine.PlayerID{"a1", "b1", "c1", "d1", "e1"},
		Hands: map[engine.PlayerID][]string{
			"a1": {"2C", "3D"},
			"b1": {"9C", "4S"},
			"c1": {"8H", "4D"},
			"d1": {"KS", "5S"},
			"e1": {"QC", "6H"},
		},
		Deck:       []string{"7S", "6C", "8D"}, // 8D drawn first
		DiscardTop: "TH",
		Unlocked:   true,
	}))
	tb.act("a1", protocol.ADrawCard, nil)
	tb.act("a1", protocol.ADiscardDrawn, nil)
	if tb.phase() != "matching" {
		t.Fatalf("expected a matching window, phase is %s", tb.phase())
	}
	if r := tb.room.Snapshot().ActiveRank; r != engine.Eight {
		t.Fatalf("active rank is %s, want 8", r)
	}
	return tb
}

func mustScenario(t *testing.T, sc engine.Scenario) engine.State {
	t.Helper()
	st, err := engine.BuildScenario(sc)
	if err != nil {
		t.Fatal(err)
	}
	return st
}

// raceInvariants is the checklist every race must satisfy, whatever order the
// room loop happened to pick.
func (tb *table) raceInvariants(t *testing.T, contenders []engine.PlayerID, wins int) {
	t.Helper()
	if wins != 1 {
		t.Fatalf("%d contenders won the same physical card, want exactly 1", wins)
	}
	if r := tb.room.Snapshot().ActiveRank; r != engine.Eight {
		t.Fatalf("the active rank changed to %s", r)
	}
	if tb.phase() != "matching" {
		t.Fatalf("the window should still be open, phase is %s", tb.phase())
	}
	// The window that opened is the one the discard opened; a match must never
	// have opened another.
	for _, c := range tb.clients {
		opened := 0
		for _, name := range c.eventNames() {
			if name == "matching_window_opened" {
				opened++
			}
		}
		if opened > 0 {
			t.Fatalf("%s saw %d new windows open during the race", c.id, opened)
		}
	}
	tb.stateOf(func(s *engine.State) {
		if err := engine.CheckInvariants(s); err != nil {
			t.Fatalf("card conservation broken: %v", err)
		}
		for _, id := range contenders {
			if n := len(s.Player(id).PendingPenalty); n != 0 {
				t.Fatalf("%s took %d penalty cards for a legitimate race", id, n)
			}
		}
	})
	if got := tb.room.Stats().Conflicts; got != 0 {
		t.Fatalf("engine.Apply overlapped %d times", got)
	}
}

// Five concurrent submissions for the same physical card: one wins, four are
// stale, nobody is penalised, one card leaves play.
func TestFiveWayRaceForOnePhysicalCard(t *testing.T) {
	tb := fiveWaySetup(t)
	var version uint64
	tb.stateOf(func(s *engine.State) { version = s.Player("c1").Layout.Version() })
	tb.resetAll()

	// c1 owns the card, so c1 throws it; everyone else targets it.
	own, _ := json.Marshal(map[string]any{"index": 0, "version": version})
	opp, _ := json.Marshal(map[string]any{
		"target": "c1", "target_index": 0, "target_version": version, "give_index": 0,
	})

	contenders := []engine.PlayerID{"a1", "b1", "c1", "d1", "e1"}
	start := make(chan struct{})
	var wg sync.WaitGroup
	for _, id := range contenders {
		wg.Add(1)
		go func(p engine.PlayerID) {
			defer wg.Done()
			<-start
			if p == "c1" {
				tb.room.Act(p, protocol.ActionData{Kind: protocol.AAttemptOwnMatch, Args: own}, "race")
				return
			}
			tb.room.Act(p, protocol.ActionData{Kind: protocol.AAttemptOpponentMatch, Args: opp}, "race")
		}(id)
	}
	close(start)
	wg.Wait()
	tb.settle()

	wins, stale := 0, 0
	for _, id := range contenders {
		c := tb.client(string(id))
		switch {
		case len(c.errors()) == 0:
			wins++
		case c.lastErrorCode() == "stale":
			stale++
		default:
			t.Fatalf("%s got %q; a lost race must be stale", id, c.lastErrorCode())
		}
	}
	if stale != 4 {
		t.Fatalf("%d contenders were told stale, want 4", stale)
	}
	tb.raceInvariants(t, contenders, wins)

	// Exactly one physical card left play, and it was the 8.
	tb.stateOf(func(s *engine.State) {
		for _, cid := range s.Player("c1").Layout.Cards() {
			if s.MustCard(cid).Rank == engine.Eight {
				t.Fatal("c1 still holds the contested 8")
			}
		}
		total := 0
		for i := range s.Players {
			total += s.Players[i].Layout.Len()
		}
		// 5 players x 2 cards = 10 at the start. One 8 left play; if an
		// opponent won, they also handed a card over, so the total is 9.
		if total != 9 {
			t.Fatalf("%d cards in hands, want 9 (exactly one removal)", total)
		}
	})
}

// Five concurrent AttemptOwnMatch submissions for the SAME card by its owner:
// the duplicate clicks must not remove five cards.
func TestFiveDuplicateOwnMatchSubmissions(t *testing.T) {
	tb := fiveWaySetup(t)
	var version uint64
	tb.stateOf(func(s *engine.State) { version = s.Player("c1").Layout.Version() })
	tb.resetAll()

	args, _ := json.Marshal(map[string]any{"index": 0, "version": version})
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			tb.room.Act("c1", protocol.ActionData{Kind: protocol.AAttemptOwnMatch, Args: args}, "dup")
		}()
	}
	close(start)
	wg.Wait()
	tb.settle()

	if got := len(tb.client("c1").errors()); got != 4 {
		t.Fatalf("%d duplicate submissions were refused, want 4", got)
	}
	if code := tb.client("c1").lastErrorCode(); code != "stale" {
		t.Fatalf("duplicates returned %q, want stale", code)
	}
	tb.stateOf(func(s *engine.State) {
		if s.Player("c1").Layout.Len() != 1 {
			t.Fatalf("c1 holds %d cards; exactly one 8 should have left", s.Player("c1").Layout.Len())
		}
		if n := len(s.Player("c1").PendingPenalty); n != 0 {
			t.Fatalf("duplicate clicks cost %d penalty cards", n)
		}
	})
	tb.raceInvariants(t, []engine.PlayerID{"c1"}, 1)
}

// Four opponents race to transfer into the same slot: exactly one transfer.
func TestConcurrentOpponentTargetingYieldsOneTransfer(t *testing.T) {
	tb := fiveWaySetup(t)
	var version uint64
	tb.stateOf(func(s *engine.State) { version = s.Player("c1").Layout.Version() })
	tb.resetAll()

	args, _ := json.Marshal(map[string]any{
		"target": "c1", "target_index": 0, "target_version": version, "give_index": 0,
	})
	contenders := []engine.PlayerID{"a1", "b1", "d1", "e1"}
	start := make(chan struct{})
	var wg sync.WaitGroup
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
	transfers := 0
	for _, id := range contenders {
		if len(tb.client(string(id)).errors()) == 0 {
			wins++
		}
	}
	for _, c := range tb.clients {
		for _, n := range c.eventNames() {
			if n == "card_transferred" {
				transfers++
				break // count once per client; the event is public
			}
		}
	}
	if transfers == 0 {
		t.Fatal("the winning transfer was never announced")
	}
	tb.raceInvariants(t, contenders, wins)
	tb.stateOf(func(s *engine.State) {
		if s.Player("c1").Layout.Len() != 2 {
			t.Fatalf("c1 holds %d cards; a transfer refills the gap", s.Player("c1").Layout.Len())
		}
		light := 0
		for _, id := range contenders {
			if s.Player(id).Layout.Len() == 1 {
				light++
			}
		}
		if light != 1 {
			t.Fatalf("%d contenders gave a card away, want exactly 1", light)
		}
	})
}

// The same five requests delivered in a fixed order produce one exact state,
// every time.
func TestDeterministicOrderGivesADeterministicState(t *testing.T) {
	run := func() string {
		tb := fiveWaySetup(t)
		var version uint64
		tb.stateOf(func(s *engine.State) { version = s.Player("c1").Layout.Version() })
		own, _ := json.Marshal(map[string]any{"index": 0, "version": version})
		opp, _ := json.Marshal(map[string]any{
			"target": "c1", "target_index": 0, "target_version": version, "give_index": 0,
		})
		// Fixed submission order: b1, d1, c1, e1, a1.
		for _, id := range []engine.PlayerID{"b1", "d1", "c1", "e1", "a1"} {
			kind, args := protocol.AAttemptOpponentMatch, opp
			if id == "c1" {
				kind, args = protocol.AAttemptOwnMatch, own
			}
			tb.room.Act(id, protocol.ActionData{Kind: kind, Args: args}, "seq")
			tb.settle()
		}
		var out string
		tb.stateOf(func(s *engine.State) { out = s.Debug() })
		return out
	}
	first := run()
	for i := 0; i < 3; i++ {
		if got := run(); got != first {
			t.Fatalf("the same order produced a different state:\n%s\n%s", first, got)
		}
	}
	// b1 submitted first, so b1 is the one who gave a card away.
	if !contains(first, "b1(seat1 active=true cards=1") {
		t.Fatalf("expected b1 to have won the race: %s", first)
	}
}

func contains(hay, needle string) bool {
	return len(hay) >= len(needle) && (func() bool {
		for i := 0; i+len(needle) <= len(hay); i++ {
			if hay[i:i+len(needle)] == needle {
				return true
			}
		}
		return false
	})()
}

// ---------- 2: serialization under load ----------

func TestEngineApplyIsNeverConcurrentForARoom(t *testing.T) {
	tb := newTable(t, 8, 4, 11)
	tb.startGame(t)

	// Everyone hammers the room at once with a mix of legal and illegal
	// actions, from every seat, while timers are also firing.
	const rounds = 40
	var wg sync.WaitGroup
	for _, c := range tb.clients {
		wg.Add(1)
		go func(id engine.PlayerID) {
			defer wg.Done()
			pos, _ := json.Marshal(map[string]any{"positions": []int{0, 1}})
			idx, _ := json.Marshal(map[string]any{"index": 0})
			for i := 0; i < rounds; i++ {
				tb.room.Act(id, protocol.ActionData{Kind: protocol.AInspectMemory, Args: pos}, "s")
				tb.room.Act(id, protocol.ActionData{Kind: protocol.ADrawCard}, "s")
				tb.room.Act(id, protocol.ActionData{Kind: protocol.ADiscardDrawn}, "s")
				tb.room.Act(id, protocol.ActionData{Kind: protocol.AAttemptOwnMatch, Args: idx}, "s")
				tb.room.Act(id, protocol.ActionData{Kind: protocol.ADeclareCombio}, "s")
			}
		}(c.id)
	}
	// Timers churn alongside the flood.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 20; i++ {
			tb.clock.Advance(6 * time.Second)
		}
	}()
	wg.Wait()
	tb.settle()

	st := tb.room.Stats()
	if st.Conflicts != 0 {
		t.Fatalf("engine.Apply ran concurrently %d times out of %d transitions",
			st.Conflicts, st.Applies)
	}
	if st.Applies < 50 {
		t.Fatalf("only %d transitions ran; the stress did not bite", st.Applies)
	}
	tb.stateOf(func(s *engine.State) {
		if err := engine.CheckInvariants(s); err != nil {
			t.Fatalf("state corrupted under load: %v", err)
		}
	})
}

// ---------- 3: isolation ----------

func TestRoomsAreFullyIsolated(t *testing.T) {
	clock := NewFakeClock()
	m := NewManager(ManagerOptions{Clock: clock, Timers: testTimers(), Seed: 7})
	t.Cleanup(m.CloseAll)

	build := func(t *testing.T, prefix string) (*Room, []*fakeClient) {
		t.Helper()
		r, err := m.Create(protocol.CreateRoomData{Public: true, CardsPerPlayer: 4, MaxPlayers: 4})
		if err != nil {
			t.Fatal(err)
		}
		cs := []*fakeClient{newClient(prefix+"1", "One"), newClient(prefix+"2", "Two")}
		for _, c := range cs {
			r.Join(c, "j")
			r.SetReady(c.ID(), true, "r")
		}
		r.Start(cs[0].ID(), "s")
		r.sync(func() {})
		for _, c := range cs {
			args, _ := json.Marshal(map[string]any{"positions": []int{0, 1}})
			r.Act(c.ID(), protocol.ActionData{Kind: protocol.AInspectMemory, Args: args}, "m")
		}
		r.sync(func() {})
		return r, cs
	}

	roomA, clientsA := build(t, "a")
	roomB, clientsB := build(t, "b")

	// Snapshot everything about B, then batter A.
	beforeB := roomB.Snapshot()
	var beforeStateB string
	roomB.WithState(func(s *engine.State) { beforeStateB = s.Debug() })
	countB := 0
	for _, c := range clientsB {
		countB += len(c.all())
	}

	for i := 0; i < 30; i++ {
		id := clientsA[i%len(clientsA)].ID()
		roomA.Act(id, protocol.ActionData{Kind: protocol.ADrawCard}, "x")
		roomA.Act(id, protocol.ActionData{Kind: protocol.ADiscardDrawn}, "x")
		roomA.Act(id, protocol.ActionData{Kind: protocol.ADeclareCombio}, "x")
	}
	roomA.Disconnected(clientsA[1].ID())
	roomA.sync(func() {})
	roomB.sync(func() {})

	// B is untouched: state, status, players, connections and messages.
	var afterStateB string
	roomB.WithState(func(s *engine.State) { afterStateB = s.Debug() })
	if afterStateB != beforeStateB {
		t.Fatalf("room B's state changed:\n%s\n%s", beforeStateB, afterStateB)
	}
	afterB := roomB.Snapshot()
	if afterB.Status != beforeB.Status || afterB.Paused != beforeB.Paused {
		t.Fatalf("room B's status changed: %+v -> %+v", beforeB, afterB)
	}
	if len(afterB.Players) != len(beforeB.Players) {
		t.Fatal("room B's roster changed")
	}
	for _, p := range afterB.Players {
		if !p.Connected {
			t.Fatalf("room B's player %s was disconnected by room A", p.ID)
		}
	}
	countAfterB := 0
	for _, c := range clientsB {
		countAfterB += len(c.all())
	}
	if countAfterB != countB {
		t.Fatalf("room B's clients received %d extra messages", countAfterB-countB)
	}

	// B's timers are its own: its turn timer still runs its full length.
	roomB.sync(func() {})
	clock.Advance(testTimers().Turn - time.Second)
	roomB.sync(func() {})
	if roomB.Snapshot().Phase != "turn_start" {
		t.Fatal("room B's timer fired early because of room A")
	}
	clock.Advance(2 * time.Second)
	roomB.sync(func() {})
	if roomB.Snapshot().Phase == "turn_start" {
		t.Fatal("room B's own timer never fired")
	}
}

// ---------- 4: stale timers, every kind ----------

func TestStaleTimersOfEveryKindAreInert(t *testing.T) {
	t.Run("turn timer", func(t *testing.T) {
		tb := newTable(t, 3, 4, 7)
		tb.startGame(t)
		tb.finishMemory(t)

		gen := tb.currentGen(t)
		// Player acts, so the turn timer is replaced by the window's timer.
		cur := string(tb.currentPlayer())
		tb.act(cur, protocol.ADrawCard, nil)
		tb.act(cur, protocol.ADiscardDrawn, nil)
		before := tb.phase()
		tb.room.sync(func() { tb.room.firePhaseTimer(gen) })
		if tb.phase() != before {
			t.Fatalf("a stale turn timer changed the phase %s -> %s", before, tb.phase())
		}
	})

	t.Run("matching timer", func(t *testing.T) {
		tb := newTable(t, 3, 4, 7)
		tb.startGame(t)
		tb.finishMemory(t)
		cur := string(tb.currentPlayer())
		tb.act(cur, protocol.ADrawCard, nil)
		tb.act(cur, protocol.ADiscardDrawn, nil)
		gen := tb.currentGen(t)
		tb.advance(testTimers().Match) // the window closes for real
		before := tb.phase()
		tb.room.sync(func() { tb.room.firePhaseTimer(gen) })
		if tb.phase() != before {
			t.Fatalf("a stale matching timer changed the phase %s -> %s", before, tb.phase())
		}
	})

	t.Run("power timer", func(t *testing.T) {
		tb := newTable(t, 3, 4, 7)
		tb.startGame(t)
		tb.room.ReplaceStateForTesting(mustScenario(t, engine.Scenario{
			Players: []engine.PlayerID{"a1", "b1", "c1"},
			Hands: map[engine.PlayerID][]string{
				"a1": {"2C", "3D"}, "b1": {"KS", "4H"}, "c1": {"9D", "6S"},
			},
			Deck:       []string{"5C", "JC"},
			DiscardTop: "2H",
			Unlocked:   true,
		}))
		tb.act("a1", protocol.ADrawCard, nil)
		tb.act("a1", protocol.ADiscardDrawn, nil)
		tb.advance(testTimers().Match)
		if tb.phase() != "power" {
			t.Fatalf("expected a power to be waiting, phase is %s", tb.phase())
		}
		gen := tb.currentGen(t)
		tb.act("a1", protocol.AJackPeek, map[string]any{"target": "b1", "target_index": 0})
		before := tb.phase()
		tb.room.sync(func() { tb.room.firePhaseTimer(gen) })
		if tb.phase() != before {
			t.Fatalf("a stale power timer changed the phase %s -> %s", before, tb.phase())
		}
	})

	t.Run("reconnect timer", func(t *testing.T) {
		tb := newTable(t, 3, 4, 7)
		tb.startGame(t)
		tb.finishMemory(t)

		victim := tb.clients[2].id
		tb.room.Disconnected(victim)
		tb.settle()
		var gen uint64
		tb.room.sync(func() { gen = tb.room.reconnectGen[victim] })

		// They come back, which cancels the timer.
		tb.reattach(string(victim), "back")

		// The old callback arrives late and must not remove them.
		tb.room.sync(func() { tb.room.fireReconnect(victim, gen) })
		tb.stateOf(func(s *engine.State) {
			if s.Player(victim).Removed {
				t.Fatal("a stale reconnect timer removed a player who had returned")
			}
		})
		if tb.room.Snapshot().Paused {
			t.Fatal("the game should be running")
		}
	})
}

// currentGen reads the live phase timer's generation.
func (tb *table) currentGen(t *testing.T) uint64 {
	t.Helper()
	var gen uint64
	tb.room.sync(func() {
		if tb.room.phaseTimer == nil {
			t.Fatal("no phase timer is running")
		}
		gen = tb.room.phaseTimer.gen
	})
	return gen
}
