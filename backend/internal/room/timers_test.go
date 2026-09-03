package room

import (
	"testing"
	"time"

	"github.com/kaushlendra/combio/backend/internal/engine"
	"github.com/kaushlendra/combio/backend/internal/protocol"
)

// ---------- 13-16: timers ----------

func TestTurnTimeoutForcesALegalMove(t *testing.T) {
	tb := newTable(t, 2, 4, 7)
	tb.startGame(t)
	tb.finishMemory(t)

	before := tb.currentPlayer()
	tb.resetAll()
	tb.advance(testTimers().Turn)

	if !tb.client(string(before)).sawEvent("turn_timed_out") {
		t.Fatal("the timed-out player should be told")
	}
	if tb.phase() != "matching" {
		t.Fatalf("a turn timeout should draw and discard, phase is %s", tb.phase())
	}
	// The forced discard never arms a power.
	tb.stateOf(func(s *engine.State) {
		if s.PendingPower != engine.RankNone {
			t.Fatalf("a timed-out turn armed the %s power", s.PendingPower)
		}
	})
}

func TestMatchingWindowTimeoutClosesTheWindow(t *testing.T) {
	tb := newTable(t, 2, 4, 7)
	tb.startGame(t)
	tb.finishMemory(t)
	cur := string(tb.currentPlayer())
	tb.act(cur, protocol.ADrawCard, nil)
	tb.act(cur, protocol.ADiscardDrawn, nil)
	if tb.phase() != "matching" {
		t.Fatalf("phase is %s", tb.phase())
	}

	// Not yet.
	tb.advance(testTimers().Match - time.Second)
	if tb.phase() != "matching" {
		t.Fatal("the window closed early")
	}
	tb.advance(time.Second)
	if tb.phase() == "matching" {
		t.Fatal("the window should have closed")
	}
}

// A timer belonging to a window that has already closed must not be able to
// close the window that replaced it.
func TestStaleTimerCannotCloseTheNextPhase(t *testing.T) {
	tb := newTable(t, 2, 4, 7)
	tb.startGame(t)
	tb.finishMemory(t)

	openWindow := func() uint64 {
		t.Helper()
		cur := string(tb.currentPlayer())
		if tb.phase() == "turn_start" {
			tb.act(cur, protocol.ADrawCard, nil)
		}
		if tb.phase() == "draw_decision" {
			tb.act(cur, protocol.ADiscardDrawn, nil)
		}
		if tb.phase() != "matching" {
			t.Fatalf("expected a matching window, phase is %s", tb.phase())
		}
		var gen uint64
		tb.room.sync(func() { gen = tb.room.phaseTimer.gen })
		return gen
	}

	genA := openWindow()

	// The live generation closes its own window, as it should.
	tb.room.sync(func() { tb.room.firePhaseTimer(genA) })
	if tb.phase() == "matching" {
		t.Fatal("the live timer failed to close its own window")
	}
	// Firing it a second time must do nothing at all.
	after := tb.phase()
	tb.room.sync(func() { tb.room.firePhaseTimer(genA) })
	if tb.phase() != after {
		t.Fatalf("a spent timer fired again: %s -> %s", after, tb.phase())
	}

	// Let the turn finish and open window B, then deliver window A's callback
	// late. It must not touch window B.
	if p := tb.phase(); p == "power" || p == "king_decision" {
		tb.advance(testTimers().Power)
	}
	genB := openWindow()
	if genB == genA {
		t.Fatal("a new window must get a new generation")
	}
	tb.room.sync(func() { tb.room.firePhaseTimer(genA) })
	if tb.phase() != "matching" {
		t.Fatal("the previous window's timer closed the new window")
	}
	// The real one still works.
	tb.advance(testTimers().Match)
	if tb.phase() == "matching" {
		t.Fatal("window B never closed")
	}
}

func TestTimersFreezeWhilePausedAndResumeAfterwards(t *testing.T) {
	tb := newTable(t, 3, 4, 7)
	tb.startGame(t)
	tb.finishMemory(t)
	cur := tb.currentPlayer()

	// Burn most of the turn timer, then drop somebody else.
	tb.advance(testTimers().Turn - 2*time.Second)
	other := tb.clients[1].id
	if other == cur {
		other = tb.clients[2].id
	}
	tb.room.Disconnected(other)
	tb.settle()
	if !tb.room.Snapshot().Paused {
		t.Fatal("a disconnect must pause the game")
	}

	// Time passes; the frozen turn timer must not fire.
	tb.advance(10 * time.Second)
	if tb.phase() != "turn_start" {
		t.Fatalf("a frozen timer fired, phase is %s", tb.phase())
	}

	// On reconnect the remaining 2s resumes, and only then does it fire.
	tb.reattach(string(other), "back")
	if tb.room.Snapshot().Paused {
		t.Fatal("reconnecting should resume the game")
	}
	tb.advance(time.Second)
	if tb.phase() != "turn_start" {
		t.Fatal("the resumed timer fired too early")
	}
	tb.advance(2 * time.Second)
	if tb.phase() == "turn_start" {
		t.Fatal("the resumed timer never fired")
	}
}

// ---------- 17-22: disconnect, reconnect, removal ----------

func TestDisconnectPausesAndBlocksEveryone(t *testing.T) {
	tb := newTable(t, 3, 4, 7)
	tb.startGame(t)
	tb.finishMemory(t)
	cur := string(tb.currentPlayer())

	victim := tb.clients[1].id
	if string(victim) == cur {
		victim = tb.clients[2].id
	}
	tb.room.Disconnected(victim)
	tb.settle()

	info := tb.room.Snapshot()
	if !info.Paused {
		t.Fatal("the game should be paused")
	}
	for _, p := range info.Players {
		if p.ID == victim && p.Connected {
			t.Fatal("the victim should be marked disconnected")
		}
	}
	// Everyone still connected learns about it.
	if !tb.client(cur).sawEvent("player_disconnected") {
		t.Fatal("the table should be told")
	}
	// And nobody can act.
	tb.resetAll()
	tb.act(cur, protocol.ADrawCard, nil)
	if code := tb.client(cur).lastErrorCode(); code != "paused" {
		t.Fatalf("acting while paused returned %q, want paused", code)
	}
}

func TestReconnectRestoresTheSessionAndPrivateState(t *testing.T) {
	tb := newTable(t, 3, 4, 7)
	tb.startGame(t)
	tb.finishMemory(t)

	cur := tb.currentPlayer()
	// The current player draws, then drops while holding a private card.
	tb.act(string(cur), protocol.ADrawCard, nil)
	var drawn engine.CardID
	tb.stateOf(func(s *engine.State) { drawn = s.Drawn })

	tb.room.Disconnected(cur)
	tb.settle()
	if !tb.room.Snapshot().Paused {
		t.Fatal("expected a pause")
	}

	back := tb.reattach(string(cur), "back")

	if tb.room.Snapshot().Paused {
		t.Fatal("the game should have resumed")
	}
	v := back.view(t)
	if v.MyDrawnCard == nil || v.MyDrawnCard.ID != drawn {
		t.Fatal("the returning player should get their frozen drawn card back")
	}
	// Nobody else can see it.
	for _, c := range tb.clients {
		if c.id == cur || c == back {
			continue
		}
		if c.view(t).MyDrawnCard != nil {
			t.Fatalf("%s can see the drawn card", c.id)
		}
	}
	// And they can carry on from exactly where they were.
	tb.act(string(cur), protocol.ADiscardDrawn, nil)
	if tb.phase() != "matching" {
		t.Fatalf("play did not resume, phase is %s", tb.phase())
	}
}

func TestReconnectTimeoutRemovesThePlayerAndSinksTheirCards(t *testing.T) {
	tb := newTable(t, 3, 4, 7)
	tb.startGame(t)
	tb.finishMemory(t)

	victim := tb.clients[2].id
	if victim == tb.currentPlayer() {
		victim = tb.clients[1].id
	}
	var (
		total    int
		theirs   []engine.CardID
		discardN int
	)
	tb.stateOf(func(s *engine.State) {
		total = len(s.Cards)
		theirs = append([]engine.CardID(nil), s.Player(victim).Layout.Cards()...)
		discardN = len(s.Discard)
	})

	tb.room.Disconnected(victim)
	tb.settle()
	tb.advance(testTimers().Reconnect - time.Second)
	tb.stateOf(func(s *engine.State) {
		if s.Player(victim).Removed {
			t.Fatal("removed before the reconnect window expired")
		}
	})

	tb.advance(2 * time.Second)

	tb.stateOf(func(s *engine.State) {
		if !s.Player(victim).Removed {
			t.Fatal("the player should have been removed")
		}
		if len(s.Cards) != total {
			t.Fatalf("card count changed from %d to %d", total, len(s.Cards))
		}
		if len(s.Discard) != discardN+len(theirs) {
			t.Fatalf("discard grew by %d, expected %d sunk cards",
				len(s.Discard)-discardN, len(theirs))
		}
		for i, id := range theirs {
			if s.Discard[i] != id {
				t.Fatalf("their card %d is not at the bottom position %d", id, i)
			}
		}
		if err := engine.CheckInvariants(s); err != nil {
			t.Fatal(err)
		}
		if s.Paused {
			t.Fatal("play should resume once the player is gone")
		}
	})

	// And the room reflects it.
	for _, p := range tb.room.Snapshot().Players {
		if p.ID == victim && !p.Removed {
			t.Fatal("the room still lists them as active")
		}
	}
}

func TestPlayResumesAfterRemovalAndTheFinalQueueIsCorrect(t *testing.T) {
	tb := newTable(t, 3, 4, 7)
	tb.startGame(t)
	tb.finishMemory(t)

	// Remove the player who is NOT on turn, then keep playing.
	cur := tb.currentPlayer()
	victim := tb.clients[2].id
	if victim == cur {
		victim = tb.clients[1].id
	}
	tb.room.Disconnected(victim)
	tb.settle()
	tb.advance(testTimers().Reconnect + time.Second)

	if tb.room.Snapshot().Paused {
		t.Fatal("the game should be running again")
	}
	// Play on until COMBIO is available, then declare and finish.
	for i := 0; i < 60; i++ {
		info := tb.room.Snapshot()
		if info.Status == StatusFinished {
			break
		}
		c := string(tb.currentPlayer())
		if c == string(victim) {
			t.Fatal("a removed player was given a turn")
		}
		var unlocked bool
		tb.stateOf(func(s *engine.State) { unlocked = s.DeclareUnlocked() })
		if unlocked && tb.phase() == "turn_start" && info.Status != StatusFinalTurns {
			tb.act(c, protocol.ADeclareCombio, nil)
			continue
		}
		tb.playPlainTurn(t, c)
	}
	if tb.room.Snapshot().Status != StatusFinished {
		t.Fatalf("the game never finished: %+v", tb.room.Snapshot())
	}
	// Exactly one final turn each for the survivors, and the removed player is
	// not scored.
	tb.stateOf(func(s *engine.State) {
		if _, ok := s.Scores[victim]; ok {
			t.Fatal("a removed player was scored")
		}
		if len(s.Scores) != 2 {
			t.Fatalf("expected two scored players, got %v", s.Scores)
		}
	})
}

func TestGameCompletionNotifiesEveryone(t *testing.T) {
	tb := newTable(t, 2, 4, 7)
	tb.startGame(t)
	tb.finishMemory(t)

	for i := 0; i < 60 && tb.room.Snapshot().Status != StatusFinished; i++ {
		c := string(tb.currentPlayer())
		var unlocked bool
		tb.stateOf(func(s *engine.State) { unlocked = s.DeclareUnlocked() })
		if unlocked && tb.phase() == "turn_start" && tb.room.Snapshot().Status != StatusFinalTurns {
			tb.act(c, protocol.ADeclareCombio, nil)
			continue
		}
		tb.playPlainTurn(t, c)
	}
	if tb.room.Snapshot().Status != StatusFinished {
		t.Fatalf("the game never finished: %+v", tb.room.Snapshot())
	}
	for _, c := range tb.clients {
		m, ok := c.last(protocol.SGameFinished)
		if !ok {
			t.Fatalf("%s never received the result", c.id)
		}
		d := m.Data.(protocol.GameFinishedData)
		if len(d.View.Winners) == 0 || len(d.View.Scores) == 0 {
			t.Fatalf("%s got an empty result: %+v", c.id, d.View)
		}
		if len(d.View.Reveal) == 0 {
			t.Fatal("the final reveal should be public")
		}
	}
	// Nothing more may be played.
	tb.resetAll()
	tb.act(string(tb.clients[0].id), protocol.ADrawCard, nil)
	if code := tb.clients[0].lastErrorCode(); code != "game_over" {
		t.Fatalf("acting after the game returned %q", code)
	}
}

func TestDisconnectBeforeStartJustLeavesTheRoom(t *testing.T) {
	tb := newTable(t, 3, 4, 7)
	tb.room.Disconnected(tb.clients[1].id)
	tb.settle()

	info := tb.room.Snapshot()
	if len(info.Players) != 2 {
		t.Fatalf("a pre-game disconnect should free the seat, %d remain", len(info.Players))
	}
	if info.Status != StatusWaiting {
		t.Fatalf("status is %s", info.Status)
	}
}

func TestHostPassesOnWhenTheHostLeaves(t *testing.T) {
	tb := newTable(t, 3, 4, 7)
	host := tb.room.Snapshot().Host
	tb.room.Leave(host)
	tb.settle()
	info := tb.room.Snapshot()
	if info.Host == host {
		t.Fatal("the host should have changed")
	}
	if len(info.Players) != 2 {
		t.Fatalf("%d players remain", len(info.Players))
	}
}
