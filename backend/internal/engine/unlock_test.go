package engine

import "testing"

// fourHanded builds A -> B -> C -> D with a deck of harmless non-power cards so
// turns can be driven without power resolution getting in the way.
func fourHanded(t *testing.T) State {
	t.Helper()
	s, err := BuildScenario(Scenario{
		Players: []PlayerID{"A", "B", "C", "D"},
		Hands: map[PlayerID][]string{
			"A": {"2S", "3S"},
			"B": {"4D", "5D"},
			"C": {"6C", "7C"},
			"D": {"8H", "9H"},
		},
		Deck: []string{
			"2D", "3H", "4S", "5C", "6D", "7S", "8C", "9S",
			"2C", "3C", "4C", "5S", "6H", "7H", "8D", "9D",
		},
		DiscardTop: "5H",
	})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func drop(t *testing.T, s State, p PlayerID) State {
	t.Helper()
	s, _ = step(t, s, PlayerDisconnected{Base: For(p)})
	s, _ = step(t, s, RemovePlayer{Base: For(p)})
	return s
}

func assertGate(t *testing.T, s State, want bool, why string) {
	t.Helper()
	if got := s.DeclareUnlocked(); got != want {
		t.Fatalf("%s: DeclareUnlocked()=%v want %v (rounds=%d pending=%v)",
			why, got, want, s.RoundsCompleted, s.RoundPending)
	}
	cur := s.CurrentID()
	if s.Phase == PhaseTurnStart && !s.Paused {
		if av := s.ViewFor(cur).DeclareAvailable; av != want {
			t.Fatalf("%s: view DeclareAvailable=%v want %v", why, av, want)
		}
		if want {
			if _, _, err := Apply(s, DeclareCombio{Base: For(cur)}); err != nil {
				t.Fatalf("%s: declaration should be accepted: %v", why, err)
			}
		} else {
			rejects(t, s, DeclareCombio{Base: For(cur)}, ErrDeclareLocked)
		}
	}
}

// TestGateNeedsTwoRoundsActuallyPlayed is the baseline: no removals at all.
func TestGateNeedsTwoRoundsActuallyPlayed(t *testing.T) {
	s := fourHanded(t)
	order := []PlayerID{"A", "B", "C", "D"}
	for round := 1; round <= 2; round++ {
		for _, p := range order {
			assertGate(t, s, false, "mid-round")
			s = plainTurn(t, s, p)
		}
		if s.RoundsCompleted != round {
			t.Fatalf("after round %d, RoundsCompleted=%d", round, s.RoundsCompleted)
		}
	}
	assertGate(t, s, true, "after two complete rounds")
}

// TestRemovalNeverRetroactivelyCompletesARound is the scenario from the review:
//
//	round 1 complete, then A and B play round 2, then C and D are removed.
//
// C and D never took their round-2 turns, so round 2 was NOT completed and the
// gate must stay shut — even though every surviving player has two turns each.
func TestRemovalNeverRetroactivelyCompletesARound(t *testing.T) {
	s := fourHanded(t)
	for _, p := range []PlayerID{"A", "B", "C", "D"} {
		s = plainTurn(t, s, p) // round 1 complete
	}
	if s.RoundsCompleted != 1 {
		t.Fatalf("round 1 should be complete, got %d", s.RoundsCompleted)
	}
	s = plainTurn(t, s, "A")
	s = plainTurn(t, s, "B") // round 2 half done: C and D still owe turns

	s = drop(t, s, "C")
	s = drop(t, s, "D")

	// Both survivors have personally completed two turns...
	if s.Player("A").TurnsCompleted != 2 || s.Player("B").TurnsCompleted != 2 {
		t.Fatalf("A=%d B=%d turns", s.Player("A").TurnsCompleted, s.Player("B").TurnsCompleted)
	}
	// ...but only ONE round was ever actually completed.
	if s.RoundsCompleted != 1 {
		t.Fatalf("removals must not complete round 2, RoundsCompleted=%d", s.RoundsCompleted)
	}
	assertGate(t, s, false, "after C and D are removed mid-round-2")

	// The broken round restarted with the survivors: one more full A,B round
	// opens the gate, and not a turn sooner.
	s = plainTurn(t, s, "A")
	assertGate(t, s, false, "A has played the restarted round, B has not")
	s = plainTurn(t, s, "B")
	assertGate(t, s, true, "after the restarted round completes")
}

func TestRemovalTimingMatrix(t *testing.T) {
	cases := []struct {
		name       string
		playBefore []PlayerID
		remove     []PlayerID
		// turns played after the removals, then the expected gate state
		playAfter []PlayerID
		wantGate  bool
		wantRound int
	}{
		{
			name:      "removed before anyone has played",
			remove:    []PlayerID{"D"},
			playAfter: []PlayerID{"A", "B", "C"},
			wantGate:  false,
			wantRound: 1,
		},
		{
			name:      "removed before anyone has played, two full rounds after",
			remove:    []PlayerID{"D"},
			playAfter: []PlayerID{"A", "B", "C", "A", "B", "C"},
			wantGate:  true,
			wantRound: 2,
		},
		{
			name:       "removed during round 1",
			playBefore: []PlayerID{"A", "B"},
			remove:     []PlayerID{"D"},
			playAfter:  []PlayerID{"C", "A", "B"},
			wantGate:   false,
			wantRound:  1,
		},
		{
			name:       "removed during round 1, gate opens only after two clean rounds",
			playBefore: []PlayerID{"A", "B"},
			remove:     []PlayerID{"D"},
			playAfter:  []PlayerID{"C", "A", "B", "C", "A", "B"},
			wantGate:   true,
			wantRound:  2,
		},
		{
			name:       "removed during round 2",
			playBefore: []PlayerID{"A", "B", "C", "D", "A"},
			remove:     []PlayerID{"C"},
			playAfter:  []PlayerID{"B", "D"},
			wantGate:   false,
			wantRound:  1,
		},
		{
			name:       "removed during round 2 then one clean round",
			playBefore: []PlayerID{"A", "B", "C", "D", "A"},
			remove:     []PlayerID{"C"},
			playAfter:  []PlayerID{"B", "D", "A"},
			wantGate:   true,
			wantRound:  2,
		},
		{
			name:       "removed immediately before their own turn",
			playBefore: []PlayerID{"A", "B", "C", "D", "A", "B"},
			remove:     []PlayerID{"C"},
			playAfter:  []PlayerID{"D"},
			wantGate:   false,
			wantRound:  1,
		},
		{
			name:       "player who already played this round is removed",
			playBefore: []PlayerID{"A", "B", "C", "D", "A", "B", "C", "D"},
			remove:     []PlayerID{"C"},
			playAfter:  nil,
			wantGate:   true,
			wantRound:  2,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := fourHanded(t)
			for _, p := range tc.playBefore {
				s = plainTurn(t, s, p)
			}
			for _, p := range tc.remove {
				s = drop(t, s, p)
			}
			for _, p := range tc.playAfter {
				if s.CurrentID() != p {
					t.Fatalf("expected %s to be on turn, got %s", p, s.CurrentID())
				}
				s = plainTurn(t, s, p)
			}
			if s.RoundsCompleted != tc.wantRound {
				t.Fatalf("RoundsCompleted=%d want %d (pending=%v)",
					s.RoundsCompleted, tc.wantRound, s.RoundPending)
			}
			assertGate(t, s, tc.wantGate, tc.name)
		})
	}
}

// A player removed while it is their own turn forfeits it: the forfeit must not
// count as a played turn, or a mid-turn disconnect could complete a round.
func TestForfeitedTurnDoesNotCount(t *testing.T) {
	s := fourHanded(t)
	s = plainTurn(t, s, "A")
	s = plainTurn(t, s, "B")
	s = plainTurn(t, s, "C") // D is now on turn, owing a round-1 turn

	before := s.Player("D").TurnsCompleted
	s = drop(t, s, "D")
	if s.Player("D").TurnsCompleted != before {
		t.Fatal("a forfeited turn must not be credited")
	}
	if s.RoundsCompleted != 0 {
		t.Fatalf("round 1 was never completed, got RoundsCompleted=%d", s.RoundsCompleted)
	}
	if s.CurrentID() != "A" {
		t.Fatalf("turn should pass to A, got %s", s.CurrentID())
	}
	assertGate(t, s, false, "after a mid-turn forfeit")
}

// A declaration ends the hand, so there is no post-declaration window in which
// round accounting could still be disturbed.
func TestDeclarationEndsTheHandSoTheGateStopsMattering(t *testing.T) {
	s := fourHanded(t)
	for i := 0; i < 2; i++ {
		for _, p := range []PlayerID{"A", "B", "C", "D"} {
			s = plainTurn(t, s, p)
		}
	}
	rounds := s.RoundsCompleted
	s, _ = step(t, s, DeclareCombio{Base: For("A")})

	if s.Phase != PhaseFinished {
		t.Fatalf("phase = %s", s.Phase)
	}
	if s.RoundsCompleted != rounds {
		t.Fatalf("declaring moved the round counter: %d -> %d", rounds, s.RoundsCompleted)
	}
	// Nothing at all can follow.
	rejects(t, s, DeclareCombio{Base: For("B")}, ErrGameOver)
	rejects(t, s, PlayerDisconnected{Base: For("C")}, ErrGameOver)
}

// The gate is a latch: once two rounds are genuinely complete, later removals
// can never close it again.
func TestGateNeverClosesOnceOpen(t *testing.T) {
	s := fourHanded(t)
	for i := 0; i < 2; i++ {
		for _, p := range []PlayerID{"A", "B", "C", "D"} {
			s = plainTurn(t, s, p)
		}
	}
	assertGate(t, s, true, "two rounds complete")
	s = plainTurn(t, s, "A") // round 3 in progress
	s = drop(t, s, "B")
	s = drop(t, s, "C")
	assertGate(t, s, true, "after removals in round 3")
}
