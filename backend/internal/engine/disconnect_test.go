package engine

import (
	"errors"
	"testing"
)

func threeHanded(t *testing.T) State {
	t.Helper()
	s, err := BuildScenario(Scenario{
		Players: []PlayerID{"A", "B", "C"},
		Hands: map[PlayerID][]string{
			"A": {"2C", "3D"},
			"B": {"KS", "4H"},
			"C": {"9D", "6S"},
		},
		Deck:       []string{"5C", "7D", "8H", "QS"},
		DiscardTop: "TH",
		Unlocked:   true,
	})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestDisconnectPausesEverything(t *testing.T) {
	s := threeHanded(t)
	s, evs := step(t, s, PlayerDisconnected{Base: For("B")})
	if !s.Paused || !HasEvent(evs, "game_paused") {
		t.Fatal("a disconnect must pause the game")
	}
	// Every gameplay action, including timers, is frozen.
	rejects(t, s, DrawCard{Base: For("A")}, ErrPaused)
	rejects(t, s, TimerExpired{Kind: TimerTurn}, ErrPaused)

	s, evs = step(t, s, PlayerReconnected{Base: For("B")})
	if s.Paused || !HasEvent(evs, "game_resumed") {
		t.Fatal("reconnecting should resume the game")
	}
	if _, _, err := Apply(s, DrawCard{Base: For("A")}); err != nil {
		t.Fatalf("play should continue after a reconnect: %v", err)
	}
}

func TestReconnectRestoresAFrozenDrawnCard(t *testing.T) {
	s := threeHanded(t)
	s, _ = step(t, s, DrawCard{Base: For("A")})
	drawn := s.Drawn
	s, _ = step(t, s, PlayerDisconnected{Base: For("A")})
	s, _ = step(t, s, PlayerReconnected{Base: For("A")})
	if s.Drawn != drawn || s.Phase != PhaseDrawDecision {
		t.Fatal("the private drawn card must survive the freeze exactly")
	}
	if v := s.ViewFor("A"); v.MyDrawnCard == nil || v.MyDrawnCard.ID != drawn {
		t.Fatal("A should still privately see their drawn card")
	}
}

func TestRemovalSinksCardsToTheBottomAndDestroysNothing(t *testing.T) {
	s := threeHanded(t)
	total := len(s.Cards)
	bCards := s.Player("B").Layout.Cards()

	s, _ = step(t, s, PlayerDisconnected{Base: For("B")})
	s, evs := step(t, s, RemovePlayer{Base: For("B")})

	rm, _ := FindEvent(evs, "player_removed").(EvPlayerRemoved)
	if rm.CardsToBottom != len(bCards) {
		t.Fatalf("expected %d cards to sink, got %d", len(bCards), rm.CardsToBottom)
	}
	if !s.Player("B").Removed || s.Player("B").Layout.Len() != 0 {
		t.Fatal("B should be removed and holding nothing")
	}
	if s.Paused {
		t.Fatal("removal should lift the pause")
	}
	// The cards are at the BOTTOM of the pile, under everything, unrevealed.
	for i, id := range bCards {
		if s.Discard[i] != id {
			t.Fatalf("B's card %d is not at discard position %d", id, i)
		}
	}
	if s.MustCard(s.DiscardTop()).Rank != Ten {
		t.Fatal("the active top card must be unchanged")
	}
	// Nothing was destroyed: conservation still holds against the same total.
	if len(s.Cards) != total {
		t.Fatal("the card universe changed size")
	}
	if err := CheckInvariants(&s); err != nil {
		t.Fatal(err)
	}
	// No matching window, no power, no reveal came from the removal.
	if HasEvent(evs, "matching_window_opened") || HasEvent(evs, "card_discarded") {
		t.Fatal("sunk cards must not behave like a discard")
	}
}

func TestSunkCardsComeBackThroughTheReshuffle(t *testing.T) {
	s, err := BuildScenario(Scenario{
		Players:    []PlayerID{"A", "B", "C"},
		Hands:      map[PlayerID][]string{"A": {"2C"}, "B": {"KS", "4H"}, "C": {"9D"}},
		Deck:       nil, // the draw deck is already empty
		DiscardTop: "TH",
		Unlocked:   true,
	})
	if err != nil {
		t.Fatal(err)
	}
	bCards := map[CardID]bool{}
	for _, id := range s.Player("B").Layout.Cards() {
		bCards[id] = true
	}
	s, _ = step(t, s, PlayerDisconnected{Base: For("B")})
	s, _ = step(t, s, RemovePlayer{Base: For("B")})

	// A draws: the deck is empty, so the pile reshuffles and B's cards return.
	s, evs := step(t, s, DrawCard{Base: For("A")})
	if !HasEvent(evs, "deck_reshuffled") {
		t.Fatal("expected a reshuffle")
	}
	if !bCards[s.Drawn] {
		t.Fatal("the drawn card should be one of the removed player's returned cards")
	}
	if err := CheckInvariants(&s); err != nil {
		t.Fatal(err)
	}
}

func TestRemovingTheCurrentPlayerPassesTheTurn(t *testing.T) {
	s := threeHanded(t)
	s, _ = step(t, s, PlayerDisconnected{Base: For("A")})
	s, _ = step(t, s, RemovePlayer{Base: For("A")})
	if s.CurrentID() != "B" {
		t.Fatalf("turn should pass to B, got %s", s.CurrentID())
	}
	if s.Phase != PhaseTurnStart {
		t.Fatalf("phase = %s", s.Phase)
	}
}

// A declaration settles the hand before a disconnect can matter: once the game
// is finished, connection changes are refused outright.
func TestAFinishedHandCannotBeDisturbedByADisconnect(t *testing.T) {
	s, err := BuildScenario(Scenario{
		Players:    []PlayerID{"A", "B", "C"},
		Hands:      map[PlayerID][]string{"A": {"AS"}, "B": {"5D"}, "C": {"7H"}},
		Deck:       []string{"2C", "3D", "4H", "6S"},
		DiscardTop: "9H",
		Unlocked:   true,
	})
	if err != nil {
		t.Fatal(err)
	}
	s, _ = step(t, s, DeclareCombio{Base: For("A")})
	if s.Phase != PhaseFinished {
		t.Fatalf("phase = %s", s.Phase)
	}
	winners := append([]PlayerID(nil), s.Winners...)

	for _, a := range []Action{
		PlayerDisconnected{Base: For("A")},
		PlayerReconnected{Base: For("B")},
		RemovePlayer{Base: For("C")},
	} {
		rejects(t, s, a, ErrGameOver)
	}
	if len(s.Winners) != len(winners) || s.Winners[0] != winners[0] {
		t.Fatal("the result changed after the hand was over")
	}
}

func TestLastPlayerStandingWins(t *testing.T) {
	s, err := BuildScenario(Scenario{
		Players:    []PlayerID{"A", "B"},
		Hands:      map[PlayerID][]string{"A": {"KS", "KD"}, "B": {"AS"}},
		Deck:       []string{"2C"},
		DiscardTop: "9H",
	})
	if err != nil {
		t.Fatal(err)
	}
	s, _ = step(t, s, PlayerDisconnected{Base: For("B")})
	s, evs := step(t, s, RemovePlayer{Base: For("B")})
	if s.Phase != PhaseFinished {
		t.Fatalf("phase = %s", s.Phase)
	}
	if len(s.Winners) != 1 || s.Winners[0] != "A" {
		t.Fatalf("the last player standing wins even with 26 points, got %v", s.Winners)
	}
	if !HasEvent(evs, "game_finished") {
		t.Fatal("missing game_finished")
	}
}

func TestRemovalCancelsAPowerTargetingThem(t *testing.T) {
	s := armPower(t, "KD", standardHands())
	s, _ = step(t, s, KingPeek{Base: For("A"), Target: "B", TargetIndex: 0})
	if s.Phase != PhaseKingDecision {
		t.Fatal("expected the King decision phase")
	}
	s, _ = step(t, s, PlayerDisconnected{Base: For("B")})
	s, evs := step(t, s, RemovePlayer{Base: For("B")})
	if s.Phase == PhaseKingDecision {
		t.Fatal("the pending swap must be cancelled when its target leaves")
	}
	sk, _ := FindEvent(evs, "power_skipped").(EvPowerSkipped)
	if sk.Reason != "target removed" {
		t.Fatalf("skip reason = %q", sk.Reason)
	}
	if len(s.Player("A").PendingPenalty) != 0 {
		t.Fatal("cancelling must not penalise the acting player")
	}
}

func TestImpossibleStateIsReportedNotPanicked(t *testing.T) {
	// Empty deck AND a discard pile holding only the active card. Unreachable
	// through legal play; the engine must report it and change nothing.
	s, err := BuildScenario(Scenario{
		Players:    []PlayerID{"A", "B"},
		Hands:      map[PlayerID][]string{"A": {"2C"}, "B": {"3D"}},
		Deck:       nil,
		DiscardTop: "9H",
	})
	if err != nil {
		t.Fatal(err)
	}
	ns, evs, err := Apply(s, DrawCard{Base: For("A")})
	if !errors.Is(err, ErrImpossibleState) {
		t.Fatalf("expected ErrImpossibleState, got %v", err)
	}
	if evs != nil {
		t.Fatal("no events should be emitted")
	}
	if ns.Phase != PhaseTurnStart || len(ns.Deck) != 0 || len(ns.Discard) != 1 {
		t.Fatal("the state must come back untouched")
	}
	// The error carries the full state for the operator's log.
	if len(err.Error()) < 40 {
		t.Fatal("the impossible-state error should include a state dump")
	}
}

func TestPenaltyShortDrawNeverDuplicatesACard(t *testing.T) {
	// Only one card is obtainable, so a penalty pays out one instead of two.
	s, err := BuildScenario(Scenario{
		Players:    []PlayerID{"A", "B"},
		Hands:      map[PlayerID][]string{"A": {"2C", "3D"}, "B": {"KS"}},
		Deck:       []string{"8H"},
		DiscardTop: "9C",
		Unlocked:   true,
	})
	if err != nil {
		t.Fatal(err)
	}
	s, _ = step(t, s, DrawCard{Base: For("A")})     // 8H
	s, _ = step(t, s, DiscardDrawn{Base: For("A")}) // rank 8
	// The deck is empty and only 9C sits under the active 8H, so a reshuffle
	// can produce exactly one card. A wrong OPPONENT target buries nothing, so
	// nothing else is available to pay with.
	s, evs := step(t, s, AttemptOpponentMatch{Base: For("A"), Target: "B", TargetIndex: 0, GiveIndex: 0})
	pen, _ := FindEvent(evs, "penalty_applied").(EvPenaltyApplied)
	if pen.Count != 1 {
		t.Fatalf("expected a short-draw penalty of 1, got %d", pen.Count)
	}
	if err := CheckInvariants(&s); err != nil {
		t.Fatal(err)
	}
}
