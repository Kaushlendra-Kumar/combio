package engine

import "testing"

func plainTurn(t *testing.T, s State, p PlayerID) State {
	t.Helper()
	s, _ = step(t, s, DrawCard{Base: For(p)})
	s, _ = step(t, s, DiscardDrawn{Base: For(p)})
	if s.Phase == PhaseMatching {
		s, _ = step(t, s, TimerExpired{Kind: TimerMatchWindow})
	}
	if s.Phase == PhasePower || s.Phase == PhaseKingDecision {
		s, _ = step(t, s, TimerExpired{Kind: TimerPower})
	}
	return s
}

func TestDeclarationLockedForTwoCompleteRounds(t *testing.T) {
	s, err := BuildScenario(Scenario{
		Players:    []PlayerID{"A", "B"},
		Hands:      map[PlayerID][]string{"A": {"2S", "3S"}, "B": {"4D", "5D"}},
		Deck:       []string{"2C", "3H", "4C", "5C", "6C", "7C"}, // 7C drawn first
		DiscardTop: "9H",
	})
	if err != nil {
		t.Fatal(err)
	}
	rejects(t, s, DeclareCombio{Base: For("A")}, ErrDeclareLocked)

	s = plainTurn(t, s, "A") // round 1
	rejects(t, s, DeclareCombio{Base: For("B")}, ErrDeclareLocked)
	s = plainTurn(t, s, "B")
	rejects(t, s, DeclareCombio{Base: For("A")}, ErrDeclareLocked)
	s = plainTurn(t, s, "A") // round 2
	rejects(t, s, DeclareCombio{Base: For("B")}, ErrDeclareLocked)
	s = plainTurn(t, s, "B")

	// Round 3: the gate opens for everyone.
	if !s.DeclareUnlocked() {
		t.Fatal("declaration should be unlocked from round 3")
	}
	if !s.ViewFor("A").DeclareAvailable {
		t.Fatal("A's view should offer the COMBIO button")
	}
	s, evs := step(t, s, DeclareCombio{Base: For("A")})
	if !HasEvent(evs, "combio_declared") {
		t.Fatal("declaration should be accepted")
	}
}

func TestDeclarationIsTimingOnlyNeverScoreGated(t *testing.T) {
	// A holds a catastrophic hand. The button is still available: the risk is
	// the whole point (§10), and the server must not leak the score.
	s, err := BuildScenario(Scenario{
		Players:    []PlayerID{"A", "B"},
		Hands:      map[PlayerID][]string{"A": {"KS", "KD", "QC"}, "B": {"AS"}},
		Deck:       []string{"2C", "3C"},
		DiscardTop: "9H",
		Unlocked:   true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !s.ViewFor("A").DeclareAvailable {
		t.Fatal("a bad hand must not disable the COMBIO button")
	}
	if _, _, err := Apply(s, DeclareCombio{Base: For("A")}); err != nil {
		t.Fatalf("declaration must be accepted regardless of score: %v", err)
	}
}

func TestDeclarationTimingRestrictions(t *testing.T) {
	s, err := BuildScenario(Scenario{
		Players:    []PlayerID{"A", "B", "C"},
		Hands:      map[PlayerID][]string{"A": {"2C"}, "B": {"3D"}, "C": {"4H"}},
		Deck:       []string{"5C", "6D", "7H", "8S"},
		DiscardTop: "9H",
		Unlocked:   true,
	})
	if err != nil {
		t.Fatal(err)
	}
	rejects(t, s, DeclareCombio{Base: For("B")}, ErrNotYourTurn)

	drawn, _ := step(t, s, DrawCard{Base: For("A")})
	rejects(t, drawn, DeclareCombio{Base: For("A")}, ErrAlreadyDrawn)

	matching, _ := step(t, drawn, DiscardDrawn{Base: For("A")})
	rejects(t, matching, DeclareCombio{Base: For("A")}, ErrWrongPhase)

	declared, _ := step(t, s, DeclareCombio{Base: For("A")})
	rejects(t, declared, DeclareCombio{Base: For("B")}, ErrAlreadyDeclared)
}

func TestFinalTurnsThenReveal(t *testing.T) {
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
	s, evs := step(t, s, DeclareCombio{Base: For("A")})
	ts, _ := FindEvent(evs, "turn_started").(EvTurnStarted)
	if ts.Player != "B" || !ts.FinalTurn {
		t.Fatalf("final turns should start with B, got %+v", ts)
	}
	s = plainTurn(t, s, "B")
	if s.Phase == PhaseFinished {
		t.Fatal("C has not had a final turn yet")
	}
	if s.CurrentID() != "C" {
		t.Fatalf("expected C, got %s", s.CurrentID())
	}
	s = plainTurn(t, s, "C")
	if s.Phase != PhaseFinished {
		t.Fatalf("game should be over, phase=%s", s.Phase)
	}
	if len(s.Winners) != 1 || s.Winners[0] != "A" {
		t.Fatalf("A had -1 and should win, winners=%v", s.Winners)
	}
}

func TestDeclarerRemainsTargetableDuringFinalTurns(t *testing.T) {
	s, err := BuildScenario(Scenario{
		Players:    []PlayerID{"A", "B"},
		Hands:      map[PlayerID][]string{"A": {"AS", "2D"}, "B": {"KC", "8H"}},
		Deck:       []string{"7C", "6C", "8D"}, // 8D drawn first, two left to pay penalties
		DiscardTop: "9H",
		Unlocked:   true,
	})
	if err != nil {
		t.Fatal(err)
	}
	s, _ = step(t, s, DeclareCombio{Base: For("A")})
	// B's final turn: discard an 8 and dump their King onto the declarer.
	s, _ = step(t, s, DrawCard{Base: For("B")})
	s, _ = step(t, s, DiscardDrawn{Base: For("B")})
	s, evs := step(t, s, AttemptOpponentMatch{Base: For("B"), Target: "A", TargetIndex: 0, GiveIndex: 0})
	if HasEvent(evs, "match_accepted") {
		t.Fatal("A's Ace is not an 8; that attempt should fail")
	}
	// Now match B's own 8 out, then hand the declarer a King via a real target.
	s, _ = step(t, s, AttemptOwnMatch{Base: For("B"), Index: 1})
	if s.Player("B").Layout.Len() != 1 {
		t.Fatalf("B should have discarded their 8, holds %d", s.Player("B").Layout.Len())
	}
	if len(s.Player("B").PendingPenalty) != 2 {
		t.Fatal("B should be carrying the penalty from the failed attempt")
	}
	s, _ = step(t, s, TimerExpired{Kind: TimerMatchWindow})
	if s.Phase != PhaseFinished {
		t.Fatalf("phase = %s", s.Phase)
	}
	// The declarer was still exposed to everything during the final turn.
	if _, ok := s.Scores["A"]; !ok {
		t.Fatal("scores missing")
	}
}

// ---- winner determination, every row of the §11 table ----

func TestDetermineWinnersTable(t *testing.T) {
	e := func(p PlayerID, v int) ScoreEntry { return ScoreEntry{Player: p, Score: v} }
	cases := []struct {
		name        string
		order       []ScoreEntry
		declarer    PlayerID
		wantWinners []PlayerID
		wantFailed  bool
	}{
		{"declarer strictly lowest", []ScoreEntry{e("D", -2), e("B", -1), e("C", 0)}, "D", []PlayerID{"D"}, false},
		{"declarer not lowest", []ScoreEntry{e("D", 0), e("B", -1), e("C", 4)}, "D", []PlayerID{"B"}, true},
		{"declarer ties for lowest", []ScoreEntry{e("D", -2), e("B", -2), e("C", 1)}, "D", []PlayerID{"B"}, true},
		{"original doc example", []ScoreEntry{e("D", 8), e("B", 8), e("C", 11), e("E", 16)}, "D", []PlayerID{"B"}, true},
		{"two non-declarers tie", []ScoreEntry{e("D", 3), e("B", -1), e("C", -1), e("E", 5)}, "D", []PlayerID{"B", "C"}, true},
		{"everyone ties", []ScoreEntry{e("D", 4), e("B", 4), e("C", 4)}, "D", []PlayerID{"B", "C"}, true},
		{"two player tie", []ScoreEntry{e("D", 2), e("B", 2)}, "D", []PlayerID{"B"}, true},
		{"zero cards loses to an ace", []ScoreEntry{e("D", 5), e("B", 0), e("C", -1)}, "D", []PlayerID{"C"}, true},
		{"multiple aces", []ScoreEntry{e("D", -3), e("B", -2), e("C", -1)}, "D", []PlayerID{"D"}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, failed := DetermineWinners(tc.order, true, tc.declarer, true)
			if failed != tc.wantFailed {
				t.Fatalf("failed = %v, want %v", failed, tc.wantFailed)
			}
			if len(got) != len(tc.wantWinners) {
				t.Fatalf("winners = %v, want %v", got, tc.wantWinners)
			}
			for i := range got {
				if got[i] != tc.wantWinners[i] {
					t.Fatalf("winners = %v, want %v", got, tc.wantWinners)
				}
			}
		})
	}
}

func TestDetermineWinnersWithIneligibleDeclarer(t *testing.T) {
	order := []ScoreEntry{{Player: "B", Score: 3}, {Player: "C", Score: 1}}
	got, failed := DetermineWinners(order, true, "A", false)
	if !failed || len(got) != 1 || got[0] != "C" {
		t.Fatalf("a removed declarer cannot win; got %v failed=%v", got, failed)
	}
}

func TestAceIsMinusOneEndToEnd(t *testing.T) {
	s, err := BuildScenario(Scenario{
		Players:    []PlayerID{"A", "B"},
		Hands:      map[PlayerID][]string{"A": {"AS", "AD"}, "B": {}},
		Deck:       []string{"2C", "3C"},
		DiscardTop: "9H",
		Unlocked:   true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := s.Score("A"); got != -2 {
		t.Fatalf("two aces should score -2, got %d", got)
	}
	if got := s.Score("B"); got != 0 {
		t.Fatalf("zero cards should score 0, got %d", got)
	}
	s, _ = step(t, s, DeclareCombio{Base: For("A")})
	s = plainTurn(t, s, "B")
	if s.Phase != PhaseFinished {
		t.Fatalf("phase = %s", s.Phase)
	}
	if len(s.Winners) != 1 || s.Winners[0] != "A" {
		t.Fatalf("-2 must beat 0; winners = %v", s.Winners)
	}
}
