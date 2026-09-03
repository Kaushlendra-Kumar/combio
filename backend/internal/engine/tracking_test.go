package engine

import "testing"

// TestPhysicalIdentitySurvivesMultipleSwaps is Example 4: a player learns a
// physical King with the Jack, that card then moves B -> C -> D through two
// different powers, and it is still the same physical card and still targetable
// by whoever managed to track it.
func TestPhysicalIdentitySurvivesMultipleSwaps(t *testing.T) {
	s, err := BuildScenario(Scenario{
		Players: []PlayerID{"A", "B", "C", "D"},
		Hands: map[PlayerID][]string{
			"A": {"2S", "3S"},
			"B": {"KH", "4D"},
			"C": {"5C", "6D"},
			"D": {"7H", "8S"},
		},
		// Draw order: JC, QD, KS, 2C, KD
		Deck:       []string{"KD", "2C", "KS", "QD", "JC"},
		DiscardTop: "9H",
		Unlocked:   true,
	})
	if err != nil {
		t.Fatal(err)
	}
	king := s.Player("B").Layout.Cards()[0]
	if s.MustCard(king).Rank != King {
		t.Fatal("setup: B should start with the King")
	}

	// A learns it with the Jack.
	s, _ = step(t, s, DrawCard{Base: For("A")})
	s, _ = step(t, s, DiscardDrawn{Base: For("A")})
	s, _ = step(t, s, TimerExpired{Kind: TimerMatchWindow})
	s, evs := step(t, s, JackPeek{Base: For("A"), Target: "B", TargetIndex: 0})
	rev, _ := FindEvent(evs, "private_reveal").(EvPrivateReveal)
	if rev.Card.ID != king {
		t.Fatal("the Jack should have shown A that exact physical card")
	}

	// B blind-swaps it to C with the Queen.
	s, _ = step(t, s, DrawCard{Base: For("B")})
	s, _ = step(t, s, DiscardDrawn{Base: For("B")})
	s, _ = step(t, s, TimerExpired{Kind: TimerMatchWindow})
	s, _ = step(t, s, QueenSwap{Base: For("B"), OwnIndex: 0, Target: "C", TargetIndex: 0})
	if !s.Player("C").Layout.Contains(king) {
		t.Fatal("the King should now be with C")
	}

	// C open-swaps it onward to D with the King power.
	s, _ = step(t, s, DrawCard{Base: For("C")})
	s, _ = step(t, s, DiscardDrawn{Base: For("C")})
	s, _ = step(t, s, TimerExpired{Kind: TimerMatchWindow})
	s, _ = step(t, s, KingPeek{Base: For("C"), Target: "D", TargetIndex: 0})
	s, _ = step(t, s, KingSwap{Base: For("C"), OwnIndex: 0})
	if !s.Player("D").Layout.Contains(king) {
		t.Fatal("the King should now be with D")
	}
	if s.MustCard(king).Rank != King {
		t.Fatal("a physical card must never change rank")
	}

	// D takes a quiet turn.
	s, _ = step(t, s, DrawCard{Base: For("D")})
	s, _ = step(t, s, DiscardDrawn{Base: For("D")})
	s, _ = step(t, s, TimerExpired{Kind: TimerMatchWindow})

	// Back to A, who discards a King and cashes in the knowledge from turn one.
	s, _ = step(t, s, DrawCard{Base: For("A")})
	s, _ = step(t, s, DiscardDrawn{Base: For("A")})
	pos := s.Player("D").Layout.IndexOf(king)
	s, evs = step(t, s, AttemptOpponentMatch{
		Base: For("A"), Target: "D", TargetIndex: pos, GiveIndex: 0,
	})
	acc, ok := FindEvent(evs, "match_accepted").(EvMatchAccepted)
	if !ok || acc.Card.ID != king {
		t.Fatalf("A should have removed that exact physical King, got %+v", acc)
	}
	if s.Player("D").Layout.Contains(king) {
		t.Fatal("the King should be out of play")
	}
}

func TestReshuffleKeepsTopAndRecyclesTheRest(t *testing.T) {
	s, err := BuildScenario(Scenario{
		Players:    []PlayerID{"A", "B"},
		Hands:      map[PlayerID][]string{"A": {"2C", "3D"}, "B": {"KS", "4H"}},
		Deck:       []string{"8H"},
		DiscardTop: "9C",
	})
	if err != nil {
		t.Fatal(err)
	}
	s, _ = step(t, s, DrawCard{Base: For("A")})
	s, _ = step(t, s, DiscardDrawn{Base: For("A")}) // active rank 8, deck now empty
	top := s.DiscardTop()

	// B throws the King at rank 8: wrong. The King is buried, then the penalty
	// draw forces a reshuffle of everything except the active 8.
	s, evs := step(t, s, AttemptOwnMatch{Base: For("B"), Index: 0})
	if !HasEvent(evs, "deck_reshuffled") {
		t.Fatal("expected the penalty draw to trigger a reshuffle")
	}
	if s.DiscardTop() != top {
		t.Fatal("the active top card must survive the reshuffle in place")
	}
	if s.MustCard(s.DiscardTop()).Rank != Eight {
		t.Fatal("the visible rank changed")
	}
	if err := CheckInvariants(&s); err != nil {
		t.Fatal(err)
	}
}

func TestTwoDeckDuplicatesAreDistinctPhysicalCards(t *testing.T) {
	s, err := BuildScenario(Scenario{
		Players:    []PlayerID{"A", "B"},
		Hands:      map[PlayerID][]string{"A": {"8S:R", "8S:B"}, "B": {"3D"}},
		Deck:       []string{"8H:B"},
		DiscardTop: "9C",
	})
	if err != nil {
		t.Fatal(err)
	}
	cards := s.Player("A").Layout.Cards()
	if cards[0] == cards[1] {
		t.Fatal("two copies of 8S must be different physical cards")
	}
	red, blue := s.MustCard(cards[0]), s.MustCard(cards[1])
	if red.Deck == blue.Deck {
		t.Fatal("the duplicates should carry different deck colours")
	}
	// Deck colour is public, so both handles expose it to opponents.
	handles := s.ViewFor("B").Opponents[0].Cards
	if handles[0].Deck == handles[1].Deck {
		t.Fatal("opponents must be able to tell the two backs apart")
	}
	// Both still match rank 8 regardless of colour.
	s, _ = step(t, s, DrawCard{Base: For("A")})
	s, _ = step(t, s, DiscardDrawn{Base: For("A")})
	s, _ = step(t, s, AttemptOwnMatch{Base: For("A"), Index: 0})
	s, evs := step(t, s, AttemptOwnMatch{Base: For("A"), Index: 0})
	if !HasEvent(evs, "match_accepted") {
		t.Fatal("both duplicates should be valid matches")
	}
	if s.Player("A").Layout.Len() != 0 {
		t.Fatal("A should have thrown both eights")
	}
}
