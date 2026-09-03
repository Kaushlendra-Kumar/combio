package engine

import "testing"

// armPower makes A draw the named power card, discard it, and closes the
// matching window so the power is ready to resolve.
func armPower(t *testing.T, spec string, hands map[PlayerID][]string) State {
	t.Helper()
	s, err := BuildScenario(Scenario{
		Players:    []PlayerID{"A", "B", "C"},
		Hands:      hands,
		Deck:       []string{spec},
		DiscardTop: "2H",
		Unlocked:   true,
	})
	if err != nil {
		t.Fatal(err)
	}
	s, _ = step(t, s, DrawCard{Base: For("A")})
	s, _ = step(t, s, DiscardDrawn{Base: For("A")})
	s, evs := step(t, s, TimerExpired{Kind: TimerMatchWindow})
	if s.Phase == PhasePower {
		pa, _ := FindEvent(evs, "power_available").(EvPowerAvailable)
		if aud := pa.Audience(); len(aud) != 1 || aud[0] != "A" {
			t.Fatalf("power_available audience = %v, must be private to the actor", aud)
		}
	}
	return s
}

func standardHands() map[PlayerID][]string {
	return map[PlayerID][]string{
		"A": {"2C", "3D"},
		"B": {"KS", "4H"},
		"C": {"9D", "6S"},
	}
}

func TestPowerResolvesOnlyAfterTheWindowCloses(t *testing.T) {
	s, err := BuildScenario(Scenario{
		Players:    []PlayerID{"A", "B", "C"},
		Hands:      standardHands(),
		Deck:       []string{"JC"},
		DiscardTop: "2H",
	})
	if err != nil {
		t.Fatal(err)
	}
	s, _ = step(t, s, DrawCard{Base: For("A")})
	s, _ = step(t, s, DiscardDrawn{Base: For("A")})
	if s.Phase != PhaseMatching {
		t.Fatalf("expected the matching window first, got %s", s.Phase)
	}
	// The power must not be usable while the window is open (§8: never raced).
	rejects(t, s, JackPeek{Base: For("A"), Target: "B", TargetIndex: 0}, ErrWrongPhase)

	s, _ = step(t, s, TimerExpired{Kind: TimerMatchWindow})
	if s.Phase != PhasePower || s.PendingPower != Jack {
		t.Fatalf("phase=%s power=%s", s.Phase, s.PendingPower)
	}
	// The special card stays the visible top discard after resolution.
	if s.MustCard(s.DiscardTop()).Rank != Jack {
		t.Fatal("the discarded Jack should remain the top card")
	}
}

func TestPeek10IsPrivateAndOwnOnly(t *testing.T) {
	s := armPower(t, "TD", standardHands())
	s, evs := step(t, s, Peek10{Base: For("A"), Index: 1})
	rev, ok := FindEvent(evs, "private_reveal").(EvPrivateReveal)
	if !ok || rev.Card.Rank != Three || rev.Owner != "A" {
		t.Fatalf("expected a private reveal of A's own 3D, got %+v", rev)
	}
	if aud := rev.Audience(); len(aud) != 1 || aud[0] != "A" {
		t.Fatalf("reveal audience = %v", aud)
	}
	if s.Phase != PhaseTurnStart || s.CurrentID() != "B" {
		t.Fatalf("turn should have passed to B, phase=%s turn=%s", s.Phase, s.CurrentID())
	}
}

func TestJackSeesOneOpponentCard(t *testing.T) {
	s := armPower(t, "JC", standardHands())
	rejects(t, s, JackPeek{Base: For("A"), Target: "A", TargetIndex: 0}, ErrSelfTarget)
	rejects(t, s, JackPeek{Base: For("B"), Target: "C", TargetIndex: 0}, ErrNotYourTurn)

	_, evs := step(t, s, JackPeek{Base: For("A"), Target: "B", TargetIndex: 0})
	rev, _ := FindEvent(evs, "private_reveal").(EvPrivateReveal)
	if rev.Card.Rank != King || rev.Owner != "B" {
		t.Fatalf("expected to see B's King, got %+v", rev)
	}
	if len(FilterEvents(evs, "B")) != len(evs)-1 {
		t.Fatal("B must not receive the reveal event")
	}
}

func TestQueenIsBlindAndOptional(t *testing.T) {
	s := armPower(t, "QH", standardHands())
	aBefore := s.Player("A").Layout.Cards()
	bBefore := s.Player("B").Layout.Cards()

	s2, evs := step(t, s, QueenSwap{Base: For("A"), OwnIndex: 0, Target: "B", TargetIndex: 1})
	if HasEvent(evs, "private_reveal") {
		t.Fatal("a Queen swap must be blind: no reveal to anyone")
	}
	if s2.Player("A").Layout.Cards()[0] != bBefore[1] {
		t.Fatal("A should now hold B's card in the exact vacated slot")
	}
	if s2.Player("B").Layout.Cards()[1] != aBefore[0] {
		t.Fatal("B should now hold A's card in the exact vacated slot")
	}
	if s2.Player("A").Layout.Len() != 2 || s2.Player("B").Layout.Len() != 2 {
		t.Fatal("a swap must not change any card count")
	}

	// Optional: the Queen may be declined.
	s3, evs := step(t, s, SkipPower{Base: For("A")})
	sk, _ := FindEvent(evs, "power_skipped").(EvPowerSkipped)
	if sk.Reason != "declined" {
		t.Fatalf("skip reason = %q", sk.Reason)
	}
	if s3.Player("A").Layout.Cards()[0] != aBefore[0] {
		t.Fatal("declining must move nothing")
	}
}

func TestKingPeekThenSwapOrCancel(t *testing.T) {
	s := armPower(t, "KD", standardHands())
	aBefore := s.Player("A").Layout.Cards()
	bBefore := s.Player("B").Layout.Cards()

	s, evs := step(t, s, KingPeek{Base: For("A"), Target: "B", TargetIndex: 0})
	rev, _ := FindEvent(evs, "private_reveal").(EvPrivateReveal)
	if rev.Card.Rank != King {
		t.Fatalf("King peek showed %s", rev.Card.Rank)
	}
	if s.Phase != PhaseKingDecision {
		t.Fatalf("phase = %s", s.Phase)
	}

	swapped, _ := step(t, s, KingSwap{Base: For("A"), OwnIndex: 1})
	if swapped.Player("A").Layout.Cards()[1] != bBefore[0] {
		t.Fatal("King swap should place the peeked card in A's chosen slot")
	}
	if swapped.Player("B").Layout.Cards()[0] != aBefore[1] {
		t.Fatal("B should receive A's card in the vacated slot")
	}

	cancelled, _ := step(t, s, CancelKingSwap{Base: For("A")})
	if cancelled.Player("A").Layout.Cards()[1] != aBefore[1] {
		t.Fatal("cancelling must move nothing")
	}
	if cancelled.Phase != PhaseTurnStart {
		t.Fatal("cancelling should end the turn")
	}
}

func TestMandatoryPowersCannotBeDeclined(t *testing.T) {
	for _, spec := range []string{"TD", "JC", "KD"} {
		s := armPower(t, spec, standardHands())
		rejects(t, s, SkipPower{Base: For("A")}, ErrPowerMandatory)
	}
}

func TestPowerTimeoutSkipsEvenMandatoryPowers(t *testing.T) {
	s := armPower(t, "TD", standardHands())
	s, evs := step(t, s, TimerExpired{Kind: TimerPower})
	sk, _ := FindEvent(evs, "power_skipped").(EvPowerSkipped)
	if sk.Reason != "timeout" {
		t.Fatalf("skip reason = %q", sk.Reason)
	}
	if s.Phase != PhaseTurnStart || s.PendingPower != RankNone {
		t.Fatal("the turn should have ended cleanly")
	}
}

// ---- zero-card power behaviour (§8, Example 8) ----

func zeroCardHands() map[PlayerID][]string {
	return map[PlayerID][]string{
		"A": {}, // A holds nothing
		"B": {"KS", "4H"},
		"C": {"9D", "6S"},
	}
}

func TestZeroCard10Fizzles(t *testing.T) {
	s := armPower(t, "TD", zeroCardHands())
	if s.Phase != PhaseTurnStart {
		t.Fatalf("a zero-card 10 should fizzle straight past the power, phase=%s", s.Phase)
	}
}

func TestZeroCardJackStillWorks(t *testing.T) {
	s := armPower(t, "JC", zeroCardHands())
	if s.Phase != PhasePower || s.PendingPower != Jack {
		t.Fatalf("a zero-card player must still be able to use the Jack, phase=%s", s.Phase)
	}
	_, evs := step(t, s, JackPeek{Base: For("A"), Target: "C", TargetIndex: 0})
	if !HasEvent(evs, "private_reveal") {
		t.Fatal("expected the peek to work")
	}
}

func TestZeroCardQueenFizzles(t *testing.T) {
	s := armPower(t, "QH", zeroCardHands())
	if s.Phase != PhaseTurnStart {
		t.Fatalf("a zero-card Queen has nothing to give, phase=%s", s.Phase)
	}
}

func TestZeroCardKingIsPeekOnly(t *testing.T) {
	s := armPower(t, "KD", zeroCardHands())
	if s.Phase != PhasePower {
		t.Fatalf("phase = %s", s.Phase)
	}
	s, evs := step(t, s, KingPeek{Base: For("A"), Target: "B", TargetIndex: 0})
	if !HasEvent(evs, "private_reveal") {
		t.Fatal("the peek must still happen")
	}
	sk, _ := FindEvent(evs, "power_skipped").(EvPowerSkipped)
	if sk.Reason != "no own card to swap" {
		t.Fatalf("expected the swap to be unavailable, got %q", sk.Reason)
	}
	if s.Phase != PhaseTurnStart {
		t.Fatal("a zero-card King should end the turn after the peek")
	}
}

func TestPowerFizzlesWhenEveryOpponentIsEmpty(t *testing.T) {
	s := armPower(t, "JC", map[PlayerID][]string{"A": {"2C"}, "B": {}, "C": {}})
	if s.Phase != PhaseTurnStart {
		t.Fatalf("Jack should fizzle with no targetable opponent, phase=%s", s.Phase)
	}
}

func TestZeroCardPlayerKeepsDrawnCardWithoutDiscarding(t *testing.T) {
	s, err := BuildScenario(Scenario{
		Players:    []PlayerID{"A", "B"},
		Hands:      map[PlayerID][]string{"A": {}, "B": {"3C", "4D"}},
		Deck:       []string{"5H"},
		DiscardTop: "2H",
	})
	if err != nil {
		t.Fatal(err)
	}
	s, _ = step(t, s, DrawCard{Base: For("A")})
	s, evs := step(t, s, KeepDrawn{Base: For("A")})
	if s.Player("A").Layout.Len() != 1 {
		t.Fatal("A should now hold exactly one card")
	}
	if HasEvent(evs, "card_discarded") || HasEvent(evs, "matching_window_opened") {
		t.Fatal("nothing was removed, so nothing may be discarded or matched")
	}
	if s.Phase != PhaseTurnStart || s.CurrentID() != "B" {
		t.Fatal("the turn should simply pass")
	}
	// Others still cannot see what it is.
	if len(s.ViewFor("B").Opponents[0].Cards) != 1 {
		t.Fatal("B should see one face-down handle")
	}
}
