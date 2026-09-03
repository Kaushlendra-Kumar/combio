package engine

import (
	"testing"
)

// This file exists because a stress run reported that a game could reach a
// state with an empty draw deck and nothing to recycle. The question it answers
// is where the cards actually go: whether the reshuffle is failing to recover
// discarded cards, or whether they are leaving circulation somewhere else.

// census is where every physical card in the game currently sits.
type census struct {
	Deck, Discard, Hands, Trays, Drawn int
}

func (c census) total() int { return c.Deck + c.Discard + c.Hands + c.Trays + c.Drawn }

// circulation is what can still be drawn: the deck plus everything in the
// discard pile except the active top card, which never recycles.
func (c census) circulation() int { return c.Deck + c.Discard - 1 }

func take(s *State) census {
	c := census{Deck: len(s.Deck), Discard: len(s.Discard)}
	for i := range s.Players {
		c.Hands += s.Players[i].Layout.Len()
		c.Trays += len(s.Players[i].PendingPenalty)
	}
	if s.Drawn != NoCard {
		c.Drawn = 1
	}
	return c
}

// locate says where a physical card is right now. Anything other than
// "missing" means it is still fully accounted for.
func locate(s *State, id CardID) string {
	for _, x := range s.Deck {
		if x == id {
			return "deck"
		}
	}
	for _, x := range s.Discard {
		if x == id {
			if x == s.DiscardTop() {
				return "discard:top"
			}
			return "discard"
		}
	}
	for i := range s.Players {
		p := &s.Players[i]
		if p.Layout.Contains(id) {
			return "hand:" + string(p.ID)
		}
		for _, x := range p.PendingPenalty {
			if x == id {
				return "tray:" + string(p.ID)
			}
		}
	}
	if s.Drawn == id {
		return "drawn"
	}
	return "missing"
}

// A wrongly thrown card is a discarded card. It must reach the pile, and the
// next reshuffle must put it back into the draw deck where it can be drawn
// again.
func TestWrongMatchCardIsRecycledByTheNextReshuffle(t *testing.T) {
	s, err := BuildScenario(Scenario{
		Players:    []PlayerID{"A", "B"},
		Hands:      map[PlayerID][]string{"A": {"2C", "3D"}, "B": {"KS", "QH"}},
		Deck:       []string{"4C", "5C", "6C", "7C", "8H"}, // 8H drawn first
		DiscardTop: "9H",
		Unlocked:   true,
	})
	if err != nil {
		t.Fatal(err)
	}
	s.SetShuffler(NoShuffle{})

	s, _ = step(t, s, DrawCard{Base: For("A")})
	s, _ = step(t, s, DiscardDrawn{Base: For("A")}) // rank 8 window

	// B throws the King at rank 8: wrong. Track that exact physical card.
	thrown, _ := s.Player("B").Layout.At(0)
	before := s.MustCard(thrown)
	s, _ = step(t, s, AttemptOwnMatch{Base: For("B"), Index: 0})

	// It left the hand and went into the pile, buried under the active card.
	if got := locate(&s, thrown); got != "discard" {
		t.Fatalf("the wrongly thrown card is %s, want buried in the discard pile", got)
	}
	if s.DiscardTop() == thrown {
		t.Fatal("a wrongly thrown card became the active card")
	}

	// Now drain the deck through ordinary play. When it runs out, the pile is
	// recycled and this card must come back with it.
	recycled := false
	for turn := 0; turn < 30 && !recycled; turn++ {
		if s.Phase == PhaseMatching {
			s, _ = step(t, s, TimerExpired{Kind: TimerMatchWindow})
			continue
		}
		if s.Phase == PhasePower || s.Phase == PhaseKingDecision {
			s, _ = step(t, s, TimerExpired{Kind: TimerPower})
			continue
		}
		cur := s.CurrentID()
		next, evs, err := Apply(s, DrawCard{Base: For(cur)})
		if err != nil {
			t.Fatalf("turn %d: %v", turn, err)
		}
		s = next
		if HasEvent(evs, "deck_reshuffled") {
			// The pile has just been recycled into the deck.
			switch locate(&s, thrown) {
			case "deck", "drawn":
				recycled = true
			default:
				t.Fatalf("after the reshuffle the card is %s, not back in the deck",
					locate(&s, thrown))
			}
			break
		}
		s, _ = step(t, s, DiscardDrawn{Base: For(cur)})
	}
	if !recycled {
		t.Fatal("the deck never ran out, so the recycling was never exercised")
	}

	// Same physical card, unchanged, still exactly once in the game.
	if after := s.MustCard(thrown); after != before {
		t.Fatalf("the card changed identity: %+v -> %+v", before, after)
	}
	if err := CheckInvariants(&s); err != nil {
		t.Fatal(err)
	}
}

// The reshuffle must keep the active top card exactly where it is, recycle
// every other pile card, and change no card's identity. Checked for a
// one-deck and a two-deck game.
func TestReshuffleKeepsTheTopAndRecyclesEverythingElse(t *testing.T) {
	cases := []struct {
		name  string
		hands map[PlayerID][]string
		deck  []string
		top   string
	}{
		{
			name:  "one deck",
			hands: map[PlayerID][]string{"A": {"2C", "3D"}, "B": {"KS", "QH"}},
			deck:  []string{"4C", "5D", "6H", "7S", "8C", "9D"},
			top:   "TH",
		},
		{
			name: "two decks",
			hands: map[PlayerID][]string{
				"A": {"2C", "3D:B"}, "B": {"KS:B", "QH"},
			},
			deck: []string{"4C", "4C:B", "8S", "8S:B", "AD", "AD:B"},
			top:  "TH:B",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, err := BuildScenario(Scenario{
				Players: []PlayerID{"A", "B"}, Hands: tc.hands,
				Deck: tc.deck, DiscardTop: tc.top, Unlocked: true,
			})
			if err != nil {
				t.Fatal(err)
			}
			s.SetShuffler(NewSeededShuffler(99))

			// Push the whole draw deck under the active card, so the next draw
			// has nothing but the pile to work with.
			s.Discard = append(append([]CardID(nil), s.Deck...), s.Discard...)
			s.Deck = nil
			if err := CheckInvariants(&s); err != nil {
				t.Fatal(err)
			}

			top := s.DiscardTop()
			topCard := s.MustCard(top)
			recyclable := map[CardID]Card{}
			for _, id := range s.Discard[:len(s.Discard)-1] {
				recyclable[id] = s.MustCard(id)
			}
			if len(recyclable) != len(tc.deck) {
				t.Fatalf("setup: %d recyclable cards, want %d", len(recyclable), len(tc.deck))
			}
			totalBefore := take(&s).total()

			s, evs := step(t, s, DrawCard{Base: For("A")})
			if !HasEvent(evs, "deck_reshuffled") {
				t.Fatal("the empty deck should have triggered a reshuffle")
			}

			// The active card did not move and did not change.
			if s.DiscardTop() != top {
				t.Fatal("the active top card was recycled")
			}
			if len(s.Discard) != 1 {
				t.Fatalf("the pile holds %d cards, want just the active one", len(s.Discard))
			}
			if s.MustCard(top) != topCard {
				t.Fatal("the active card changed identity")
			}

			// Everything else came back, exactly once each, unchanged.
			got := map[CardID]bool{}
			for _, id := range s.Deck {
				if got[id] {
					t.Fatalf("card %d was duplicated by the reshuffle", id)
				}
				got[id] = true
			}
			if s.Drawn != NoCard {
				got[s.Drawn] = true
			}
			if len(got) != len(recyclable) {
				t.Fatalf("%d cards came back, want %d", len(got), len(recyclable))
			}
			for id, want := range recyclable {
				if !got[id] {
					t.Fatalf("card %d was lost by the reshuffle", id)
				}
				if s.MustCard(id) != want {
					t.Fatalf("card %d changed from %+v to %+v", id, want, s.MustCard(id))
				}
			}
			if n := take(&s).total(); n != totalBefore {
				t.Fatalf("card conservation broken: %d -> %d", totalBefore, n)
			}
			if err := CheckInvariants(&s); err != nil {
				t.Fatal(err)
			}

			// And play carries on normally from there.
			s, _ = step(t, s, DiscardDrawn{Base: For("A")})
			s, _ = step(t, s, TimerExpired{Kind: TimerMatchWindow})
			if p := s.Phase; p != PhaseTurnStart && p != PhasePower && p != PhaseKingDecision {
				t.Fatalf("the game did not continue after the reshuffle, phase is %s", p)
			}
		})
	}
}

// Reshuffles happen repeatedly in a real game, and every one of them must leave
// the active card alone.
func TestRepeatedReshufflesNeverTouchTheActiveCard(t *testing.T) {
	for _, players := range []int{4, 6} { // one deck, then two
		s, _, err := NewGame(Config{CardsPerPlayer: 4}, ids(players), NewSeededShuffler(int64(players)))
		if err != nil {
			t.Fatal(err)
		}
		wantDecks := 1
		if players >= 5 {
			wantDecks = 2
		}
		if len(s.Cards) != 52*wantDecks {
			t.Fatalf("%d players should use %d deck(s)", players, wantDecks)
		}
		for _, p := range ids(players) {
			s, _ = step(t, s, InspectMemory{Base: For(p), Positions: []int{0, 1}})
		}

		total := len(s.Cards)
		reshuffles := 0
		for turn := 0; turn < 300 && s.Phase != PhaseFinished; turn++ {
			topBefore := s.DiscardTop()
			var evs []Event
			switch s.Phase {
			case PhaseTurnStart:
				s, evs = step(t, s, DrawCard{Base: For(s.CurrentID())})
			case PhaseDrawDecision:
				s, evs = step(t, s, DiscardDrawn{Base: For(s.DrawnBy)})
			case PhaseMatching:
				s, evs = step(t, s, TimerExpired{Kind: TimerMatchWindow})
			default:
				s, evs = step(t, s, TimerExpired{Kind: TimerPower})
			}
			if HasEvent(evs, "deck_reshuffled") {
				reshuffles++
				// A reshuffle happens during a draw, so the card that was on
				// top must still be on top (the draw does not touch the pile).
				if s.DiscardTop() != topBefore {
					t.Fatalf("%d players: a reshuffle moved the active card", players)
				}
			}
			if n := take(&s).total(); n != total {
				t.Fatalf("%d players: conservation broken at turn %d (%d of %d)",
					players, turn, n, total)
			}
			if err := CheckInvariants(&s); err != nil {
				t.Fatalf("%d players: %v", players, err)
			}
		}
		if reshuffles == 0 {
			t.Fatalf("%d players: the deck was never exhausted, so nothing was tested", players)
		}
		t.Logf("%d players (%d deck): %d reshuffles, all clean", players, wantDecks, reshuffles)
	}
}

// The regression test for the stress-run report.
//
// It reproduces the reported drain deterministically and pins down exactly what
// causes it. After every wrong match the accounting must balance:
//
//	circulation_after = circulation_before + 1 (the thrown card returns)
//	                                       - penalty cards issued
//
// If the reshuffle were losing cards, this identity would not hold. It does
// hold, every time, which places the cause squarely on the penalty rule:
// two cards leave circulation for a player's hand and only one comes back.
func TestCirculationDrainIsCausedByPenaltiesNotTheReshuffle(t *testing.T) {
	s, err := BuildScenario(Scenario{
		Players: []PlayerID{"A", "B"},
		Hands: map[PlayerID][]string{
			"A": {"2C", "3D"},
			"B": {"KS", "QH", "JD", "TS", "9S", "7D"},
		},
		Deck:       []string{"5C", "6C", "7C", "9C", "4C", "3C", "2S", "8H"},
		DiscardTop: "9H",
		Unlocked:   true,
	})
	if err != nil {
		t.Fatal(err)
	}
	s.SetShuffler(NoShuffle{})
	total := len(s.Cards)

	s, _ = step(t, s, DrawCard{Base: For("A")})
	s, _ = step(t, s, DiscardDrawn{Base: For("A")}) // rank 8 window

	drained := 0
	for attempt := 1; attempt <= 5; attempt++ {
		if s.Player("B").Layout.Len() == 0 {
			break
		}
		before := take(&s)
		thrown, _ := s.Player("B").Layout.At(0)

		next, evs, err := Apply(s, AttemptOwnMatch{Base: For("B"), Index: 0})
		if err != nil {
			break
		}
		s = next

		penalty := 0
		if e, ok := FindEvent(evs, "penalty_applied").(EvPenaltyApplied); ok {
			penalty = e.Count
		}
		if penalty == 0 {
			continue // that one actually matched
		}
		after := take(&s)

		// Nothing was created or destroyed.
		if after.total() != total {
			t.Fatalf("attempt %d: conservation broken, %d of %d", attempt, after.total(), total)
		}
		// The thrown card is still fully accounted for. It is normally buried
		// in the pile, but if the penalty draw triggered a reshuffle it can
		// legitimately come straight back out as one of the penalty cards.
		if where := locate(&s, thrown); where == "missing" {
			t.Fatalf("attempt %d: the thrown card %d was destroyed", attempt, thrown)
		} else if where == "discard:top" {
			t.Fatalf("attempt %d: the thrown card became the active card", attempt)
		}
		// The books balance exactly.
		want := before.circulation() + 1 - penalty
		if after.circulation() != want {
			t.Fatalf("attempt %d: circulation went %d -> %d, but the rules say %d\n"+
				"(if these disagree the reshuffle is losing cards)",
				attempt, before.circulation(), after.circulation(), want)
		}
		drained += penalty - 1

		// The cards that left circulation are in a hand or a tray, not missing.
		if after.Hands+after.Trays != before.Hands+before.Trays+penalty-1 {
			t.Fatalf("attempt %d: %d cards left circulation but hands grew by %d",
				attempt, penalty-1, after.Hands+after.Trays-before.Hands-before.Trays)
		}
	}

	if drained == 0 {
		t.Fatal("the scenario did not produce any wrong matches")
	}
	final := take(&s)
	t.Logf("after %d cards drained: deck=%d pile=%d (recyclable=%d) hands=%d tray=%d",
		drained, final.Deck, final.Discard, final.Discard-1, final.Hands, final.Trays)
	if final.total() != total {
		t.Fatal("conservation broken overall")
	}
}

// The same accounting for a failed OPPONENT target, which buries nothing at
// all: two cards leave circulation and none come back.
func TestFailedOpponentTargetDrainsTwo(t *testing.T) {
	s, err := BuildScenario(Scenario{
		Players:    []PlayerID{"A", "B"},
		Hands:      map[PlayerID][]string{"A": {"2C", "3D"}, "B": {"KS", "QH"}},
		Deck:       []string{"5C", "6C", "7C", "8H"},
		DiscardTop: "9H",
		Unlocked:   true,
	})
	if err != nil {
		t.Fatal(err)
	}
	s.SetShuffler(NoShuffle{})
	s, _ = step(t, s, DrawCard{Base: For("A")})
	s, _ = step(t, s, DiscardDrawn{Base: For("A")})

	before := take(&s)
	s, evs := step(t, s, AttemptOpponentMatch{
		Base: For("B"), Target: "A", TargetIndex: 0, GiveIndex: 0,
	})
	e, ok := FindEvent(evs, "penalty_applied").(EvPenaltyApplied)
	if !ok {
		t.Fatal("expected the wrong target to be penalised")
	}
	after := take(&s)
	if after.circulation() != before.circulation()-e.Count {
		t.Fatalf("circulation went %d -> %d after a %d-card penalty with nothing buried",
			before.circulation(), after.circulation(), e.Count)
	}
	if after.total() != before.total() {
		t.Fatal("conservation broken")
	}
}

// The regression test for the reported scenario, at its end point.
//
// It drives the pile dry and then asserts precisely where every physical card
// is. If the reshuffle were dropping cards this would fail on the accounting;
// instead every single card is present, and all of them are in players' hands.
// That is the whole finding in one assertion.
func TestDeckExhaustionLeavesEveryCardAccountedForInHands(t *testing.T) {
	s, err := BuildScenario(Scenario{
		Players: []PlayerID{"A", "B"},
		Hands: map[PlayerID][]string{
			"A": {"2C", "3D"},
			"B": {"KS", "QH", "JD", "TS", "9S", "7D", "6S", "5H"},
		},
		Deck:       []string{"5C", "6C", "7C", "9C", "4C", "3C", "2S", "8H"},
		DiscardTop: "9H",
		Unlocked:   true,
	})
	if err != nil {
		t.Fatal(err)
	}
	s.SetShuffler(NoShuffle{})
	total := len(s.Cards)

	s, _ = step(t, s, DrawCard{Base: For("A")})
	s, _ = step(t, s, DiscardDrawn{Base: For("A")})

	// Throw wrong cards until the pile cannot pay a penalty any more.
	for i := 0; i < 12 && s.Player("B").Layout.Len() > 0; i++ {
		next, _, err := Apply(s, AttemptOwnMatch{Base: For("B"), Index: 0})
		if err != nil {
			break
		}
		s = next
		if err := CheckInvariants(&s); err != nil {
			t.Fatalf("attempt %d: %v", i, err)
		}
	}
	s, _ = step(t, s, TimerExpired{Kind: TimerMatchWindow})

	// Drain whatever is left with ordinary turns until a draw is impossible.
	var drawErr error
	for i := 0; i < 40; i++ {
		switch s.Phase {
		case PhaseMatching:
			s, _ = step(t, s, TimerExpired{Kind: TimerMatchWindow})
			continue
		case PhasePower, PhaseKingDecision:
			s, _ = step(t, s, TimerExpired{Kind: TimerPower})
			continue
		}
		cur := s.CurrentID()
		next, _, err := Apply(s, DrawCard{Base: For(cur)})
		if err != nil {
			drawErr = err
			break
		}
		s = next
		s, _ = step(t, s, DiscardDrawn{Base: For(cur)})
	}
	if drawErr == nil {
		t.Skip("this deal never ran the pile dry")
	}

	// The refusal is the documented defensive one, and nothing changed.
	if !errorsIsImpossible(drawErr) {
		t.Fatalf("the refused draw returned %v, want the impossible-state error", drawErr)
	}

	// Now the important part: account for every physical card in the game.
	c := take(&s)
	if c.total() != total {
		t.Fatalf("cards were lost: %d of %d accounted for", c.total(), total)
	}
	if c.Deck != 0 {
		t.Fatalf("the deck is not actually empty (%d cards)", c.Deck)
	}
	if c.Discard != 1 {
		t.Fatalf("the pile still holds %d cards, so there WAS something to recycle "+
			"and the reshuffle failed to run", c.Discard)
	}
	if c.Hands+c.Trays != total-1 {
		t.Fatalf("%d cards are in hands and trays; with an empty deck and only the "+
			"active card in the pile it should be %d", c.Hands+c.Trays, total-1)
	}
	// Every card is somewhere, exactly once.
	for id := range s.Cards {
		if where := locate(&s, id); where == "missing" {
			t.Fatalf("card %d is missing from the game entirely", id)
		}
	}
	if err := CheckInvariants(&s); err != nil {
		t.Fatal(err)
	}
	t.Logf("at exhaustion: deck=%d pile=%d hands=%d trays=%d — all %d cards present, "+
		"every one of them in a player's hand", c.Deck, c.Discard, c.Hands, c.Trays, total)
}

func errorsIsImpossible(err error) bool {
	for err != nil {
		if err == ErrImpossibleState {
			return true
		}
		u, ok := err.(interface{ Unwrap() error })
		if !ok {
			return false
		}
		err = u.Unwrap()
	}
	return false
}
