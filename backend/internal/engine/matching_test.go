package engine

import "testing"

// openWindowOn8 puts an 8 on the pile via A's turn and returns the state with a
// live matching window.
func openWindowOn8(t *testing.T) State {
	t.Helper()
	s, err := BuildScenario(Scenario{
		Players: []PlayerID{"A", "B", "C", "D"},
		Hands: map[PlayerID][]string{
			"A": {"2C", "3D", "4H"},
			"B": {"8S", "8D", "9C"},
			"C": {"8H", "AS", "5C"},
			"D": {"8C", "KD", "6S"},
		},
		Deck:       []string{"7D", "QS", "8D:B"}, // 8D:B drawn first
		DiscardTop: "TH",
		Unlocked:   true,
	})
	if err != nil {
		t.Fatal(err)
	}
	s, _ = step(t, s, DrawCard{Base: For("A")})
	s, _ = step(t, s, DiscardDrawn{Base: For("A")})
	if s.ActiveRank != Eight {
		t.Fatalf("expected active rank 8, got %s", s.ActiveRank)
	}
	return s
}

func TestMatchingDoesNotChain(t *testing.T) {
	s := openWindowOn8(t)
	before := s.ActiveRank

	// A correct match does not create a new window or change the rank.
	s, evs := step(t, s, AttemptOwnMatch{Base: For("B"), Index: 0})
	if s.ActiveRank != before {
		t.Fatalf("rank changed after a successful match: %s", s.ActiveRank)
	}
	if HasEvent(evs, "matching_window_opened") {
		t.Fatal("a match must not open a new window")
	}

	// A wrong throw also leaves the rank alone (Example 2).
	s, _ = step(t, s, AttemptOwnMatch{Base: For("C"), Index: 1}) // AS against rank 8
	if s.ActiveRank != Eight {
		t.Fatalf("wrong throw changed the rank to %s", s.ActiveRank)
	}
	// ...and the same player may try again in the same window and succeed.
	c := s.Player("C")
	idx := c.Layout.IndexOf(findCard(t, &s, "C", Eight))
	s, evs = step(t, s, AttemptOwnMatch{Base: For("C"), Index: idx})
	if !HasEvent(evs, "match_accepted") {
		t.Fatal("second attempt with a real 8 should succeed")
	}
}

func TestMultipleOwnMatchesInOneWindow(t *testing.T) {
	s := openWindowOn8(t)
	if got := s.Player("B").Layout.Len(); got != 3 {
		t.Fatalf("B starts with %d cards", got)
	}
	s, _ = step(t, s, AttemptOwnMatch{Base: For("B"), Index: 0}) // 8S
	s, _ = step(t, s, AttemptOwnMatch{Base: For("B"), Index: 0}) // 8D, now at 0
	if got := s.Player("B").Layout.Len(); got != 1 {
		t.Fatalf("B should have discarded both eights, has %d cards", got)
	}
	if s.MatchExtensions != 2 {
		t.Fatalf("expected two window extensions, got %d", s.MatchExtensions)
	}
}

func TestWrongMatchPenaltyIsNetPlusOne(t *testing.T) {
	s := openWindowOn8(t)
	before := s.Player("C").Layout.Len()

	s, evs := step(t, s, AttemptOwnMatch{Base: For("C"), Index: 1}) // AS, wrong
	pen, _ := FindEvent(evs, "penalty_applied").(EvPenaltyApplied)
	if pen.Count != 2 {
		t.Fatalf("penalty gave %d cards", pen.Count)
	}
	if HasEvent(evs, "matching_window_extended") {
		t.Fatal("a wrong attempt must not extend the window")
	}
	c := s.Player("C")
	if len(c.PendingPenalty) != 2 {
		t.Fatalf("tray holds %d", len(c.PendingPenalty))
	}
	if c.Layout.Len() != before-1 {
		t.Fatal("the wrongly thrown card must leave the layout")
	}
	// Tray cards are not in the layout, so they cannot be targeted yet.
	if s.ViewFor("A").Opponents[1].Count != before-1 {
		t.Fatal("tray cards must not appear in the public card count")
	}

	// Window closes: unplaced tray cards are auto-appended. Net +1.
	s, evs = step(t, s, TimerExpired{Kind: TimerMatchWindow})
	if s.Player("C").Layout.Len() != before+1 {
		t.Fatalf("expected net +1 card, got %d from %d", s.Player("C").Layout.Len(), before)
	}
	if !HasEvent(evs, "penalty_placed") {
		t.Fatal("auto-placement should be reported")
	}
}

func TestPenaltyInsertionIsNonBlockingAndPositional(t *testing.T) {
	s := openWindowOn8(t)
	s, _ = step(t, s, AttemptOwnMatch{Base: For("C"), Index: 1}) // wrong -> 2 in tray

	// Place one tray card at the front, sight unseen.
	s, _ = step(t, s, InsertPenalty{Base: For("C"), TrayIndex: 0, Position: 0})
	if len(s.Player("C").PendingPenalty) != 1 {
		t.Fatal("one card should remain in the tray")
	}

	// Non-blocking: C can still attempt a match with a card left in the tray.
	idx := s.Player("C").Layout.IndexOf(findCard(t, &s, "C", Eight))
	s, evs := step(t, s, AttemptOwnMatch{Base: For("C"), Index: idx})
	if !HasEvent(evs, "match_accepted") {
		t.Fatal("matching must remain possible while the tray is non-empty")
	}

	s, _ = step(t, s, TimerExpired{Kind: TimerMatchWindow})
	if len(s.Player("C").PendingPenalty) != 0 {
		t.Fatal("tray must be empty once the window closes")
	}
	rejects(t, s, InsertPenalty{Base: For("C"), TrayIndex: 0, Position: 0}, ErrWrongPhase)
}

func TestOpponentMatchTransfersIntoTheExactGap(t *testing.T) {
	s := openWindowOn8(t)
	// C's layout is [8H, AS, 5C]; A targets index 0 and gives their own index 2.
	give := s.Player("A").Layout.Cards()[2] // 4H
	s, evs := step(t, s, AttemptOpponentMatch{
		Base: For("A"), Target: "C", TargetIndex: 0, GiveIndex: 2,
	})
	if !HasEvent(evs, "match_accepted") || !HasEvent(evs, "card_transferred") {
		t.Fatal("expected an accepted opponent match with a transfer")
	}
	tr, _ := FindEvent(evs, "card_transferred").(EvCardTransferred)
	if tr.Position != 0 {
		t.Fatalf("transfer landed at %d, must be the vacated gap 0", tr.Position)
	}
	c := s.Player("C")
	if c.Layout.Cards()[0] != give {
		t.Fatal("the given card must occupy the exact vacated position")
	}
	if c.Layout.Len() != 3 {
		t.Fatalf("target card count changed: %d", c.Layout.Len())
	}
	if s.Player("A").Layout.Len() != 2 {
		t.Fatal("the giver should be one card lighter")
	}
	// The transfer event must not reveal what was handed over.
	if FindEvent(evs, "card_transferred").Audience() != nil {
		t.Fatal("transfer event should be public but identity-free")
	}
}

func TestOpponentMatchWrongLeavesTargetUntouched(t *testing.T) {
	s := openWindowOn8(t)
	// C index 1 is the Ace, not an 8.
	before := s.Player("C").Layout.Cards()
	s, evs := step(t, s, AttemptOpponentMatch{
		Base: For("A"), Target: "C", TargetIndex: 1, GiveIndex: 0,
	})
	if !HasEvent(evs, "match_failed") {
		t.Fatal("expected a failed match")
	}
	after := s.Player("C").Layout.Cards()
	if len(after) != len(before) || after[1] != before[1] {
		t.Fatal("the target's card must stay exactly where it was")
	}
	if len(s.Player("A").PendingPenalty) != 2 {
		t.Fatal("the matcher must take the penalty")
	}
	if s.Player("A").Layout.Len() != 3 {
		t.Fatal("a failed opponent match must not cost the matcher a card")
	}
}

func TestMultipleOpponentTargetsDownToZeroCards(t *testing.T) {
	s := openWindowOn8(t) // A has 3 cards; B, C, D each hold an 8
	s, _ = step(t, s, AttemptOpponentMatch{Base: For("A"), Target: "B", TargetIndex: 0, GiveIndex: 0})
	s, _ = step(t, s, AttemptOpponentMatch{Base: For("A"), Target: "C", TargetIndex: 0, GiveIndex: 0})
	s, _ = step(t, s, AttemptOpponentMatch{Base: For("A"), Target: "D", TargetIndex: 0, GiveIndex: 0})
	if got := s.Player("A").Layout.Len(); got != 0 {
		t.Fatalf("A should be down to zero cards, has %d", got)
	}
	// With no card to give, the mechanic is unavailable (§7).
	rejects(t, s, AttemptOpponentMatch{Base: For("A"), Target: "B", TargetIndex: 0, GiveIndex: 0}, ErrNoCardToGive)
}

func TestZeroCardPlayerCannotBeTargeted(t *testing.T) {
	s := openWindowOn8(t)
	s, _ = step(t, s, AttemptOpponentMatch{Base: For("A"), Target: "B", TargetIndex: 0, GiveIndex: 0})
	s, _ = step(t, s, AttemptOpponentMatch{Base: For("A"), Target: "C", TargetIndex: 0, GiveIndex: 0})
	s, _ = step(t, s, AttemptOpponentMatch{Base: For("A"), Target: "D", TargetIndex: 0, GiveIndex: 0})
	// A now has zero cards, so nobody can target A.
	rejects(t, s, AttemptOpponentMatch{Base: For("B"), Target: "A", TargetIndex: 0, GiveIndex: 0}, ErrTargetEmpty)
}

func TestSimultaneousRaceSecondIsStaleNotWrong(t *testing.T) {
	s := openWindowOn8(t)
	c := s.Player("C")
	version := c.Layout.Version()

	// B wins the race for C's 8 at index 0.
	s, _ = step(t, s, AttemptOpponentMatch{
		Base: For("B"), Target: "C", TargetIndex: 0, TargetVersion: version, GiveIndex: 2,
	})
	// D clicked the same physical card a moment later, at the same version.
	rejects(t, s, AttemptOpponentMatch{
		Base: For("D"), Target: "C", TargetIndex: 0, TargetVersion: version, GiveIndex: 2,
	}, ErrStale)
	// Crucially, a stale loss costs nothing (Example 5).
	if len(s.Player("D").PendingPenalty) != 0 {
		t.Fatal("a stale action must not carry a penalty")
	}
}

func TestStaleIndexShiftStillResolvesToTheSamePhysicalCard(t *testing.T) {
	s := openWindowOn8(t)
	c := s.Player("C")
	v0 := c.Layout.Version()
	ace := c.Layout.Cards()[1]

	// B matches C's 8 at index 0; C's remaining cards shift left.
	s, _ = step(t, s, AttemptOpponentMatch{Base: For("B"), Target: "C", TargetIndex: 0, TargetVersion: v0, GiveIndex: 2})
	// The card that was at index 1 (the Ace) is a real card that is still
	// present, so a click on (v0, 1) must be judged as a WRONG match against
	// rank 8, not silently rejected.
	s, evs := step(t, s, AttemptOpponentMatch{Base: For("D"), Target: "C", TargetIndex: 1, TargetVersion: v0, GiveIndex: 0})
	if !HasEvent(evs, "match_failed") {
		t.Fatal("resolving an older version must still find the physical card")
	}
	if !s.Player("C").Layout.Contains(ace) {
		t.Fatal("the Ace should still be with C")
	}
}

func TestSelfTargetRejected(t *testing.T) {
	s := openWindowOn8(t)
	rejects(t, s, AttemptOpponentMatch{Base: For("A"), Target: "A", TargetIndex: 0, GiveIndex: 1}, ErrSelfTarget)
}

func TestSuitAndDeckColourAreIrrelevantToMatching(t *testing.T) {
	s, err := BuildScenario(Scenario{
		Players:    []PlayerID{"A", "B"},
		Hands:      map[PlayerID][]string{"A": {"2C", "3D"}, "B": {"8S:B", "9C"}},
		Deck:       []string{"8H:R"},
		DiscardTop: "4D",
	})
	if err != nil {
		t.Fatal(err)
	}
	s, _ = step(t, s, DrawCard{Base: For("A")})
	s, _ = step(t, s, DiscardDrawn{Base: For("A")}) // red 8H
	// B matches with a BLUE 8S: different suit, different deck, same rank.
	s, evs := step(t, s, AttemptOwnMatch{Base: For("B"), Index: 0})
	if !HasEvent(evs, "match_accepted") {
		t.Fatal("rank-only matching must accept a different suit and deck colour")
	}
}

func findCard(t *testing.T, s *State, owner PlayerID, rank Rank) CardID {
	t.Helper()
	for _, id := range s.Player(owner).Layout.Cards() {
		if s.MustCard(id).Rank == rank {
			return id
		}
	}
	t.Fatalf("%s has no %s", owner, rank)
	return NoCard
}
