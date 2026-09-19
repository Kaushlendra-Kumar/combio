package room

import (
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/kaushlendra/combio/backend/internal/engine"
	"github.com/kaushlendra/combio/backend/internal/protocol"
)

// ---------- 5: disconnect during a live matching window ----------

func TestDisconnectDuringMatchingPreservesTheWindowThenResumes(t *testing.T) {
	tb := fiveWaySetup(t)

	// Burn 3 of the 5 seconds, then b1 drops mid-window.
	tb.advance(3 * time.Second)
	before := map[string][]string{}
	tb.stateOf(func(s *engine.State) {
		for i := range s.Players {
			before[string(s.Players[i].ID)] = s.Hand(s.Players[i].ID)
		}
	})

	tb.room.Disconnected("b1")
	tb.settle()

	info := tb.room.Snapshot()
	if !info.Paused {
		t.Fatal("the disconnect must pause the game")
	}
	if info.Phase != "matching" {
		t.Fatalf("the window must survive the pause, phase is %s", info.Phase)
	}
	if info.ActiveRank != engine.Eight {
		t.Fatalf("the active rank changed to %s", info.ActiveRank)
	}

	// The frozen remainder must not tick away while paused. (Stay inside the
	// reconnect window: that timer is deliberately still running.)
	tb.advance(testTimers().Reconnect - 10*time.Second)
	if tb.phase() != "matching" {
		t.Fatalf("a frozen window timer fired, phase is %s", tb.phase())
	}
	// Nobody can act, and no card moved.
	tb.resetAll()
	tb.act("c1", protocol.AAttemptOwnMatch, map[string]any{"index": 0})
	if code := tb.client("c1").lastErrorCode(); code != "paused" {
		t.Fatalf("acting while paused returned %q", code)
	}
	tb.stateOf(func(s *engine.State) {
		for i := range s.Players {
			id := string(s.Players[i].ID)
			got := s.Hand(s.Players[i].ID)
			if len(got) != len(before[id]) {
				t.Fatalf("%s's hand changed during the pause", id)
			}
			for j := range got {
				if got[j] != before[id][j] {
					t.Fatalf("%s's hand changed during the pause", id)
				}
			}
		}
	})

	// b1 returns: the window resumes with its remaining 2 seconds.
	back := tb.reattach("b1", "Bo")

	if tb.room.Snapshot().Paused {
		t.Fatal("the game should have resumed")
	}
	if tb.phase() != "matching" || tb.room.Snapshot().ActiveRank != engine.Eight {
		t.Fatalf("the window did not survive the reconnect: %+v", tb.room.Snapshot())
	}
	// b1's own view is restored.
	if v := back.view(t); v.Me != "b1" || len(v.MyCards) != 2 {
		t.Fatalf("b1's state was not restored: %+v", v)
	}
	// The remainder was preserved: 1s more is fine, 2s more closes it.
	tb.advance(time.Second)
	if tb.phase() != "matching" {
		t.Fatal("the resumed window closed too early")
	}
	tb.advance(2 * time.Second)
	if tb.phase() == "matching" {
		t.Fatal("the resumed window never closed")
	}
}

func TestRemovalDuringMatchingSinksCardsAndResumes(t *testing.T) {
	tb := fiveWaySetup(t)
	var (
		total    int
		theirs   []engine.CardID
		discardN int
		version  uint64
	)
	tb.stateOf(func(s *engine.State) {
		total = len(s.Cards)
		theirs = append([]engine.CardID(nil), s.Player("d1").Layout.Cards()...)
		discardN = len(s.Discard)
		version = s.Player("d1").Layout.Version()
	})

	tb.room.Disconnected("d1")
	tb.settle()
	tb.resetAll()
	tb.advance(testTimers().Reconnect + time.Second)

	// The window is still live on rank 8, and nothing behaved like a discard.
	info := tb.room.Snapshot()
	if info.Phase != "matching" || info.ActiveRank != engine.Eight {
		t.Fatalf("the window did not survive the removal: %+v", info)
	}
	if info.Paused {
		t.Fatal("play should resume once the player is gone")
	}
	for _, c := range tb.clients {
		for _, n := range c.eventNames() {
			if n == "matching_window_opened" || n == "card_discarded" {
				t.Fatalf("sinking cards produced a %s event", n)
			}
		}
	}

	tb.stateOf(func(s *engine.State) {
		if !s.Player("d1").Removed {
			t.Fatal("d1 should have been removed")
		}
		if len(s.Cards) != total {
			t.Fatalf("card count changed from %d to %d", total, len(s.Cards))
		}
		if len(s.Discard) != discardN+len(theirs) {
			t.Fatalf("discard grew by %d, expected %d", len(s.Discard)-discardN, len(theirs))
		}
		for i, id := range theirs {
			if s.Discard[i] != id {
				t.Fatalf("d1's card %d is not at discard bottom slot %d", id, i)
			}
		}
		if err := engine.CheckInvariants(s); err != nil {
			t.Fatal(err)
		}
	})

	// A queued action against the removed player is stale and free.
	tb.resetAll()
	args, _ := json.Marshal(map[string]any{
		"target": "d1", "target_index": 0, "target_version": version, "give_index": 0,
	})
	tb.room.Act("a1", protocol.ActionData{Kind: protocol.AAttemptOpponentMatch, Args: args}, "late")
	tb.settle()
	if code := tb.client("a1").lastErrorCode(); code != "stale" {
		t.Fatalf("targeting a removed player returned %q, want stale", code)
	}
	tb.stateOf(func(s *engine.State) {
		if n := len(s.Player("a1").PendingPenalty); n != 0 {
			t.Fatalf("a stale target cost %d penalty cards", n)
		}
	})

	// And the survivors can still play the window out.
	tb.act("c1", protocol.AAttemptOwnMatch, map[string]any{"index": 0})
	if len(tb.client("c1").errors()) != 0 {
		t.Fatalf("c1 could not match: %v", tb.client("c1").errors())
	}
	tb.advance(testTimers().Match + 3*time.Second)
	if tb.phase() == "matching" {
		t.Fatal("the window never closed")
	}
}

// ---------- 11: abuse guards ----------

func TestActionsFromDetachedOrRemovedSeatsAreRefused(t *testing.T) {
	tb := fiveWaySetup(t)

	// A seat whose socket is gone cannot act, even before removal.
	tb.room.Disconnected("e1")
	tb.settle()
	tb.act("e1", protocol.ADrawCard, nil)
	tb.stateOf(func(s *engine.State) {
		if s.Phase != engine.PhaseMatching {
			t.Fatalf("a detached seat acted, phase is %s", s.Phase)
		}
	})

	// After removal the seat is refused explicitly.
	tb.advance(testTimers().Reconnect + time.Second)
	back := tb.reattach("e1", "Ed")
	back.reset()
	tb.act("e1", protocol.ADrawCard, nil)
	if back.lastErrorCode() == "" {
		t.Fatal("a removed player should be told they cannot act")
	}
}

func TestDuplicateReconnectIsANoOp(t *testing.T) {
	tb := newTable(t, 3, 4, 7)
	tb.startGame(t)
	tb.finishMemory(t)

	c := tb.client("c1")
	c.reset()
	// Replaying join on an already-attached socket must not emit a reconnect or
	// disturb the game.
	before := tb.room.Stats().Applies
	for i := 0; i < 5; i++ {
		tb.room.Join(c, "dup")
	}
	tb.settle()

	if got := tb.room.Stats().Applies; got != before {
		t.Fatalf("%d engine transitions ran for replayed reconnects", got-before)
	}
	for _, name := range c.eventNames() {
		if name == "player_reconnected" {
			t.Fatal("a replayed reconnect emitted a reconnect event")
		}
	}
	if tb.room.Snapshot().Paused {
		t.Fatal("a replayed reconnect paused the game")
	}
}

func TestManagerRefusesRoomsBeyondTheLimitAndAfterClose(t *testing.T) {
	m := NewManager(ManagerOptions{Clock: NewFakeClock(), Timers: testTimers(), MaxRooms: 3})
	t.Cleanup(m.CloseAll)
	for i := 0; i < 3; i++ {
		if _, err := m.Create(protocol.CreateRoomData{CardsPerPlayer: 4}); err != nil {
			t.Fatalf("room %d: %v", i, err)
		}
	}
	if _, err := m.Create(protocol.CreateRoomData{CardsPerPlayer: 4}); err != ErrTooManyRooms {
		t.Fatalf("expected the room limit to bite, got %v", err)
	}

	m.Close()
	if _, err := m.Create(protocol.CreateRoomData{CardsPerPlayer: 4}); err != ErrShuttingDown {
		t.Fatalf("a closed manager should refuse rooms, got %v", err)
	}
	if m.Count() != 0 {
		t.Fatalf("%d rooms survived the close", m.Count())
	}
}

// ---------- 13: full-game stress ----------

// playFullGame drives one room to completion with concurrent submissions,
// timer churn and a disconnect/reconnect along the way.
func playFullGame(t *testing.T, tb *table, withChaos, waitForReshuffle bool) {
	t.Helper()
	tb.startGame(t)
	tb.finishMemory(t)

	dropped := false
	reshuffled := false
	sawReshuffle := func() bool {
		for _, c := range tb.clients {
			for _, n := range c.eventNames() {
				if n == "deck_reshuffled" {
					return true
				}
			}
		}
		return false
	}
	for step := 0; step < 400 && tb.room.Snapshot().Status != StatusFinished; step++ {
		info := tb.room.Snapshot()
		cur := string(tb.currentPlayer())

		// Halfway through, drop somebody and bring them back.
		if withChaos && !dropped && step == 12 {
			victim := tb.clients[len(tb.clients)-1].id
			if string(victim) != cur {
				tb.room.Disconnected(victim)
				tb.settle()
				tb.advance(5 * time.Second) // frozen, nothing moves
				tb.reattach(string(victim), "back")
				dropped = true
			}
		}

		switch info.Phase {
		case "turn_start":
			var unlocked bool
			tb.stateOf(func(s *engine.State) { unlocked = s.DeclareUnlocked() })
			if !reshuffled && sawReshuffle() {
				reshuffled = true
			}
			ready := unlocked && info.Status != StatusFinished
			if waitForReshuffle && !reshuffled && step < 250 {
				ready = false
			}
			if ready {
				tb.act(cur, protocol.ADeclareCombio, nil)
				continue
			}
			tb.act(cur, protocol.ADrawCard, nil)
		case "draw_decision":
			tb.act(cur, protocol.ADiscardDrawn, nil)
		case "matching":
			// Three players swing at the pile simultaneously, on every third
			// window. Having everyone throw a wrong card every single window
			// would drain the deck out of circulation faster than the discard
			// refills it, which is its own (documented) rule question rather
			// than a concurrency one.
			if withChaos && step%3 == 0 {
				var wg sync.WaitGroup
				args, _ := json.Marshal(map[string]any{"index": 0})
				for i, c := range tb.clients {
					if i >= 3 {
						break
					}
					wg.Add(1)
					go func(id engine.PlayerID) {
						defer wg.Done()
						tb.room.Act(id, protocol.ActionData{
							Kind: protocol.AAttemptOwnMatch, Args: args,
						}, "chaos")
					}(c.id)
				}
				wg.Wait()
				tb.settle()
			}
			tb.advance(testTimers().Match + 8*time.Second)
		case "power", "king_decision":
			tb.advance(testTimers().Power)
		default:
			tb.advance(testTimers().Turn)
		}

		tb.stateOf(func(s *engine.State) {
			if err := engine.CheckInvariants(s); err != nil {
				t.Fatalf("step %d: %v", step, err)
			}
		})
	}

	if tb.room.Snapshot().Status != StatusFinished {
		t.Fatalf("the game never finished: %+v", tb.room.Snapshot())
	}
	if got := tb.room.Stats().Conflicts; got != 0 {
		t.Fatalf("engine.Apply overlapped %d times", got)
	}
	tb.stateOf(func(s *engine.State) {
		if len(s.Winners) == 0 {
			t.Fatal("a finished game must have a winner")
		}
		if err := engine.CheckInvariants(s); err != nil {
			t.Fatal(err)
		}
	})
	for _, c := range tb.clients {
		if _, ok := c.last(protocol.SGameFinished); !ok {
			t.Fatalf("%s never got the result", c.id)
		}
	}
}

func TestStressEightPlayerGameWithRacesAndChaos(t *testing.T) {
	tb := newTable(t, 8, 4, 31)
	playFullGame(t, tb, true, true)

	// The game was held open until the deck had to be recycled, so reshuffling
	// is exercised end to end alongside the races and the disconnect.
	reshuffled := false
	for _, c := range tb.clients {
		for _, n := range c.eventNames() {
			if n == "deck_reshuffled" {
				reshuffled = true
			}
		}
	}
	if !reshuffled {
		t.Fatal("the eight-player stress game never exhausted the deck")
	}
}

func TestStressManyRoomsToCompletionConcurrently(t *testing.T) {
	const rooms = 6
	var wg sync.WaitGroup
	errs := make(chan string, rooms)

	for i := 0; i < rooms; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			defer func() {
				if r := recover(); r != nil {
					errs <- "room panicked"
				}
			}()
			players := 2 + n%7
			cards := []int{4, 6, 8}[n%3]
			tb := newTable(t, players, cards, int64(100+n))
			playFullGame(t, tb, n%2 == 0, false)
			if tb.room.Stats().Conflicts != 0 {
				errs <- "concurrent apply"
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		t.Fatal(e)
	}
}

// A rejected system action must not leave the room without a timer.
//
// Reaching this needs the draw deck AND the recyclable discard to run dry,
// which repeated wrong matches can do: each one moves a card into a hand while
// pulling two out of circulation. The engine correctly refuses the draw with
// ErrImpossibleState; the room's job is to stay alive.
func TestExhaustedDeckDoesNotFreezeTheRoom(t *testing.T) {
	tb := newTable(t, 2, 4, 7)
	tb.startGame(t)
	tb.room.ReplaceStateForTesting(mustScenario(t, engine.Scenario{
		Players:    []engine.PlayerID{"a1", "b1"},
		Hands:      map[engine.PlayerID][]string{"a1": {"2C", "3D"}, "b1": {"KS", "4H"}},
		Deck:       []string{"8H"},
		DiscardTop: "9C",
		Unlocked:   true,
	}))
	// a1 draws the last deck card and discards it; b1 throws the wrong card,
	// and the penalty draw eats what the reshuffle produced.
	tb.act("a1", protocol.ADrawCard, nil)
	tb.act("a1", protocol.ADiscardDrawn, nil)
	tb.act("b1", protocol.AAttemptOwnMatch, map[string]any{"index": 0})
	tb.advance(testTimers().Match)

	tb.stateOf(func(s *engine.State) {
		if len(s.Deck) != 0 || len(s.Discard) != 1 {
			t.Skipf("this deal did not exhaust the pile: deck=%d discard=%d",
				len(s.Deck), len(s.Discard))
		}
	})

	// The draw is refused, and the refusal is private and identity-free.
	tb.resetAll()
	tb.act("b1", protocol.ADrawCard, nil)
	if code := tb.client("b1").lastErrorCode(); code != "internal" {
		t.Fatalf("the impossible draw returned %q", code)
	}
	for _, e := range tb.client("b1").errors() {
		for _, secret := range []string{"KS", "4H", "2C", "3D"} {
			if contains(e.Message, secret) {
				t.Fatalf("the error text leaked %s: %s", secret, e.Message)
			}
		}
	}

	// The room is still alive: a timer is armed, it fires, and it re-arms.
	assertArmed := func(when string) {
		t.Helper()
		var armed bool
		tb.room.sync(func() { armed = tb.room.phaseTimer != nil })
		if !armed {
			t.Fatalf("the room has no timer %s; nothing would ever wake it", when)
		}
	}
	assertArmed("after the refused draw")
	tb.advance(testTimers().Turn + time.Second)
	assertArmed("after the forced-move timeout also failed")
	tb.advance(testTimers().Turn + time.Second)
	assertArmed("after a second timeout")

	if tb.clock.Pending() == 0 {
		t.Fatal("no callbacks are scheduled; the room is frozen")
	}
	// And the players can still end the game themselves.
	tb.act(string(tb.currentPlayer()), protocol.ADeclareCombio, nil)
	if tb.room.Snapshot().Status != StatusFinished {
		t.Fatalf("a declaration should still be possible, status is %s",
			tb.room.Snapshot().Status)
	}
	if got := tb.room.Stats().Conflicts; got != 0 {
		t.Fatalf("engine.Apply overlapped %d times", got)
	}
}

// The reshuffle over the live server path, for a one-deck and a two-deck table.
//
// The recycling happens inside a draw, so the check is made across that single
// action: the card that was face-up in the middle must still be face-up in the
// middle, and it must be the only card left in the pile.
func TestReshuffleWorksAcrossARealMultiplayerGame(t *testing.T) {
	for _, players := range []int{4, 6} {
		wantDecks := 1
		if players >= 5 {
			wantDecks = 2
		}
		tb := newTable(t, players, 4, int64(500+players))
		tb.startGame(t)
		tb.finishMemory(t)

		var total int
		identity := map[engine.CardID]engine.Card{}
		tb.stateOf(func(s *engine.State) {
			total = len(s.Cards)
			for id, c := range s.Cards {
				identity[id] = c
			}
		})
		if total != 52*wantDecks {
			t.Fatalf("%d players: %d cards, want %d", players, total, 52*wantDecks)
		}

		reshuffles := 0
		for step := 0; step < 400 && reshuffles == 0; step++ {
			if tb.room.Snapshot().Status == StatusFinished {
				break
			}
			switch tb.phase() {
			case "turn_start":
				var topBefore engine.CardID
				var deckBefore, pileBefore int
				tb.stateOf(func(s *engine.State) {
					topBefore = s.DiscardTop()
					deckBefore, pileBefore = len(s.Deck), len(s.Discard)
				})
				cur := string(tb.currentPlayer())
				before := tb.client(cur).eventNames()
				tb.act(cur, protocol.ADrawCard, nil)
				after := tb.client(cur).eventNames()

				fired := false
				for _, n := range after[len(before):] {
					if n == "deck_reshuffled" {
						fired = true
					}
				}
				if !fired {
					continue
				}
				reshuffles++
				if deckBefore != 0 {
					t.Fatalf("%d players: a reshuffle ran with %d cards still in the deck",
						players, deckBefore)
				}
				if pileBefore < 2 {
					t.Fatalf("%d players: a reshuffle ran with nothing recyclable", players)
				}
				tb.stateOf(func(s *engine.State) {
					if s.DiscardTop() != topBefore {
						t.Fatalf("%d players: the active card was recycled", players)
					}
					if len(s.Discard) != 1 {
						t.Fatalf("%d players: the pile still holds %d cards after recycling",
							players, len(s.Discard))
					}
					if len(s.Deck) != pileBefore-2 {
						t.Fatalf("%d players: %d cards came back from a pile of %d (one stays "+
							"face up, one was just drawn)", players, len(s.Deck), pileBefore)
					}
					if len(s.Cards) != total {
						t.Fatalf("%d players: %d cards after the reshuffle, want %d",
							players, len(s.Cards), total)
					}
					for id, want := range identity {
						if s.MustCard(id) != want {
							t.Fatalf("%d players: card %d changed identity across the reshuffle",
								players, id)
						}
					}
					if err := engine.CheckInvariants(s); err != nil {
						t.Fatalf("%d players: %v", players, err)
					}
				})
			case "draw_decision":
				tb.act(string(tb.currentPlayer()), protocol.ADiscardDrawn, nil)
			case "matching":
				tb.advance(testTimers().Match + 4*time.Second)
			default:
				tb.advance(testTimers().Power)
			}
			tb.stateOf(func(s *engine.State) {
				if err := engine.CheckInvariants(s); err != nil {
					t.Fatalf("%d players step %d: %v", players, step, err)
				}
			})
		}
		if reshuffles == 0 {
			t.Fatalf("%d players: the deck never ran out, so nothing was verified", players)
		}

		// Play continues normally from there.
		for i := 0; i < 6 && tb.room.Snapshot().Status != StatusFinished; i++ {
			tb.playPlainTurn(t, string(tb.currentPlayer()))
		}
		tb.stateOf(func(s *engine.State) {
			if err := engine.CheckInvariants(s); err != nil {
				t.Fatalf("%d players: %v", players, err)
			}
		})
		if tb.room.Stats().Conflicts != 0 {
			t.Fatalf("%d players: engine.Apply overlapped", players)
		}
		t.Logf("%d players (%d deck): reshuffled and continued cleanly", players, wantDecks)
	}
}

// ---------- rematch ----------

// A finished table can play again without everyone re-sharing the code.
func TestRematchReturnsTheSameTableToTheLobby(t *testing.T) {
	tb := newTable(t, 3, 4, 7)
	tb.startGame(t)
	tb.finishMemory(t)

	// Play until the game ends.
	for i := 0; i < 80 && tb.room.Snapshot().Status != StatusFinished; i++ {
		cur := string(tb.currentPlayer())
		var unlocked bool
		tb.stateOf(func(s *engine.State) { unlocked = s.DeclareUnlocked() })
		if unlocked && tb.phase() == "turn_start" && tb.room.Snapshot().Status != StatusFinished {
			tb.act(cur, protocol.ADeclareCombio, nil)
			continue
		}
		tb.playPlainTurn(t, cur)
	}
	if tb.room.Snapshot().Status != StatusFinished {
		t.Fatalf("the game never finished: %+v", tb.room.Snapshot())
	}

	// A rematch before the game is over is refused.
	before := newTable(t, 2, 4, 7)
	before.room.Rematch(before.clients[0].id, "r")
	before.settle()
	if before.client(string(before.clients[0].id)).lastErrorCode() == "" {
		t.Fatal("a rematch should be refused while a game is unfinished")
	}

	// And an outsider cannot trigger one.
	tb.room.Rematch("ghost", "r")
	tb.settle()
	if tb.room.Snapshot().Status != StatusFinished {
		t.Fatal("an outsider reset the room")
	}

	tb.resetAll()
	tb.room.Rematch(tb.clients[0].id, "r")
	tb.settle()

	info := tb.room.Snapshot()
	if info.Status != StatusWaiting {
		t.Fatalf("status after a rematch is %s, want WAITING", info.Status)
	}
	if len(info.Players) != 3 {
		t.Fatalf("%d seats survived the rematch, want 3", len(info.Players))
	}
	for _, p := range info.Players {
		if p.Ready {
			t.Fatalf("%s is still marked ready", p.ID)
		}
		if p.Removed {
			t.Fatalf("%s is still marked removed", p.ID)
		}
	}
	if info.Started {
		t.Fatal("the room should not be mid-game after a rematch")
	}
	// Everyone was told.
	for _, c := range tb.clients {
		if _, ok := c.last(protocol.SRoomState); !ok {
			t.Fatalf("%s was not sent the new room state", c.id)
		}
	}

	// The whole lobby flow works again, and deals a different game.
	tb.readyAll()
	if tb.room.Snapshot().Status != StatusReady {
		t.Fatalf("status is %s, want READY", tb.room.Snapshot().Status)
	}
	tb.room.Start(tb.clients[0].id, "s")
	tb.settle()
	if tb.phase() != "memory" {
		t.Fatalf("the rematch did not deal a new game, phase is %s", tb.phase())
	}
	tb.stateOf(func(s *engine.State) {
		if err := engine.CheckInvariants(s); err != nil {
			t.Fatal(err)
		}
		if len(s.Winners) != 0 || len(s.Scores) != 0 {
			t.Fatal("the new game inherited the old result")
		}
	})
}

// A player whose connection died before the rematch loses their seat: nobody is
// there to ready up.
func TestRematchDropsDisconnectedSeats(t *testing.T) {
	tb := newTable(t, 3, 4, 7)
	tb.startGame(t)
	tb.finishMemory(t)
	for i := 0; i < 80 && tb.room.Snapshot().Status != StatusFinished; i++ {
		cur := string(tb.currentPlayer())
		var unlocked bool
		tb.stateOf(func(s *engine.State) { unlocked = s.DeclareUnlocked() })
		if unlocked && tb.phase() == "turn_start" && tb.room.Snapshot().Status != StatusFinished {
			tb.act(cur, protocol.ADeclareCombio, nil)
			continue
		}
		tb.playPlainTurn(t, cur)
	}
	if tb.room.Snapshot().Status != StatusFinished {
		t.Skip("the game did not finish for this deal")
	}

	tb.room.Disconnected(tb.clients[2].id)
	tb.settle()
	tb.room.Rematch(tb.clients[0].id, "r")
	tb.settle()

	info := tb.room.Snapshot()
	if len(info.Players) != 2 {
		t.Fatalf("%d seats survived, want 2", len(info.Players))
	}
	for _, p := range info.Players {
		if p.ID == tb.clients[2].id {
			t.Fatal("a disconnected seat survived the rematch")
		}
	}
}
