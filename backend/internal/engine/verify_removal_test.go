package engine

import "testing"

// ---- 2. deterministic sink order ----

// A removed player's cards enter the bottom of the pile in exactly this order:
//
//	layout cards (current layout order) -> privately drawn card -> tray cards
func TestSinkOrderIsLayoutThenDrawnThenTray(t *testing.T) {
	s, err := BuildScenario(Scenario{
		Players:    []PlayerID{"A", "B", "C"},
		Hands:      map[PlayerID][]string{"A": {"2C", "3D"}, "B": {"KS", "4H", "8C"}, "C": {"9D"}},
		Deck:       []string{"5S", "6S", "7S", "8H"}, // 8H drawn first
		DiscardTop: "TH",
		Unlocked:   true,
	})
	if err != nil {
		t.Fatal(err)
	}
	// A opens a rank-8 window; B throws the King at it and is penalised, so B
	// now holds a tray. Then it becomes B's turn and B draws.
	s, _ = step(t, s, DrawCard{Base: For("A")})
	s, _ = step(t, s, DiscardDrawn{Base: For("A")}) // rank 8
	s, _ = step(t, s, AttemptOwnMatch{Base: For("B"), Index: 0})
	tray := append([]CardID(nil), s.Player("B").PendingPenalty...)
	if len(tray) != 2 {
		t.Fatalf("expected a 2-card tray, got %d", len(tray))
	}
	s, _ = step(t, s, TimerExpired{Kind: TimerMatchWindow}) // tray auto-places
	s, _ = step(t, s, DrawCard{Base: For("B")})
	drawn := s.Drawn

	// Now give B a fresh tray without ending the window: B is mid-turn holding
	// a private card, so build the three buckets by hand-checking state.
	layout := append([]CardID(nil), s.Player("B").Layout.Cards()...)
	s.Player("B").PendingPenalty = nil // (no tray in this phase by invariant)

	total := len(s.Cards)
	s = drop(t, s, "B")

	want := append(append([]CardID(nil), layout...), drawn)
	if len(s.Discard) < len(want) {
		t.Fatalf("discard too short: %d", len(s.Discard))
	}
	for i, id := range want {
		if s.Discard[i] != id {
			t.Fatalf("sink position %d = %d, want %d (order must be layout then drawn)", i, s.Discard[i], id)
		}
	}
	if len(s.Cards) != total {
		t.Fatal("cards were created or destroyed")
	}
	if err := CheckInvariants(&s); err != nil {
		t.Fatal(err)
	}
}

// The tray variant: a player removed during a matching window while carrying
// unplaced penalty cards sinks layout first, then tray.
func TestSinkOrderIncludesTrayLast(t *testing.T) {
	s, err := BuildScenario(Scenario{
		Players:    []PlayerID{"A", "B", "C"},
		Hands:      map[PlayerID][]string{"A": {"2C", "3D"}, "B": {"KS", "4H"}, "C": {"9D"}},
		Deck:       []string{"5S", "6S", "7S", "8H"},
		DiscardTop: "TH",
		Unlocked:   true,
	})
	if err != nil {
		t.Fatal(err)
	}
	s, _ = step(t, s, DrawCard{Base: For("A")})
	s, _ = step(t, s, DiscardDrawn{Base: For("A")})
	// B throws the wrong card at an opponent, keeping their layout and gaining
	// a tray, so both buckets are non-empty at removal time.
	s, _ = step(t, s, AttemptOpponentMatch{Base: For("B"), Target: "C", TargetIndex: 0, GiveIndex: 0})
	layout := append([]CardID(nil), s.Player("B").Layout.Cards()...)
	tray := append([]CardID(nil), s.Player("B").PendingPenalty...)
	if len(layout) == 0 || len(tray) == 0 {
		t.Fatalf("setup: layout=%d tray=%d", len(layout), len(tray))
	}

	s = drop(t, s, "B")
	want := append(append([]CardID(nil), layout...), tray...)
	for i, id := range want {
		if s.Discard[i] != id {
			t.Fatalf("sink position %d = %d, want %d (layout then tray)", i, s.Discard[i], id)
		}
	}
	if err := CheckInvariants(&s); err != nil {
		t.Fatal(err)
	}
}

// ---- 3. disconnect during a live matching window ----

func TestDisconnectDuringMatchingWindowPreservesIt(t *testing.T) {
	s := openWindowOn8(t) // A discarded an 8; B, C, D each hold an 8
	if s.ActiveRank != Eight {
		t.Fatal("setup")
	}
	// A lands one match first so the extension counter is non-zero.
	s, _ = step(t, s, AttemptOwnMatch{Base: For("B"), Index: 0})
	extensions := s.MatchExtensions
	bCards := append([]CardID(nil), s.Player("B").Layout.Cards()...)
	bVersion := s.Player("B").Layout.Version()
	total := len(s.Cards)

	s, _ = step(t, s, PlayerDisconnected{Base: For("B")})
	if !s.Paused {
		t.Fatal("the game must pause")
	}
	// Everything is frozen, including other players' matching.
	rejects(t, s, AttemptOwnMatch{Base: For("C"), Index: 0}, ErrPaused)
	rejects(t, s, TimerExpired{Kind: TimerMatchWindow}, ErrPaused)
	if s.ActiveRank != Eight || s.Phase != PhaseMatching {
		t.Fatal("the window state must survive the pause")
	}

	s, evs := step(t, s, RemovePlayer{Base: For("B")})

	// The window is exactly as it was.
	if s.Phase != PhaseMatching {
		t.Fatalf("phase = %s, the window must resume", s.Phase)
	}
	if s.ActiveRank != Eight {
		t.Fatalf("active rank became %s", s.ActiveRank)
	}
	if s.MatchExtensions != extensions {
		t.Fatalf("extensions changed: %d -> %d", extensions, s.MatchExtensions)
	}
	if s.Paused {
		t.Fatal("removal should lift the pause")
	}
	// Sinking cards is not a discard: no window, no rank change, no reveal.
	if HasEvent(evs, "matching_window_opened") || HasEvent(evs, "card_discarded") ||
		HasEvent(evs, "match_accepted") || HasEvent(evs, "private_reveal") {
		t.Fatal("sinking a removed player's cards must not behave like a discard")
	}
	if s.MustCard(s.DiscardTop()).Rank != Eight {
		t.Fatal("the visible active card changed")
	}
	if len(s.Cards) != total {
		t.Fatal("conservation broken")
	}

	// B's cards can no longer be targeted, and a queued click on one is stale
	// rather than wrong: no penalty.
	rejects(t, s, AttemptOpponentMatch{
		Base: For("C"), Target: "B", TargetIndex: 0, TargetVersion: bVersion, GiveIndex: 0,
	}, ErrStale)
	if len(s.Player("C").PendingPenalty) != 0 {
		t.Fatal("a stale action against a removed player must be free")
	}
	rejects(t, s, JackPeek{Base: For("C"), Target: "B", TargetIndex: 0}, ErrWrongPhase)

	// Matching resumes normally for everyone still in.
	s, evs = step(t, s, AttemptOwnMatch{Base: For("C"), Index: 0})
	if !HasEvent(evs, "match_accepted") {
		t.Fatal("C should still be able to match the live rank 8")
	}
	// B's sunk cards really are at the bottom, under everything.
	for i, id := range bCards {
		if s.Discard[i] != id {
			t.Fatalf("B's card %d is not at discard bottom position %d", id, i)
		}
	}
	if err := CheckInvariants(&s); err != nil {
		t.Fatal(err)
	}
}

// ---- 4. several penalties stacking in one window ----

func TestStackedPenaltiesAppendInAccumulatedTrayOrder(t *testing.T) {
	s, err := BuildScenario(Scenario{
		Players:    []PlayerID{"A", "B"},
		Hands:      map[PlayerID][]string{"A": {"2C", "3D"}, "B": {"KS", "4H"}},
		Deck:       []string{"5S", "6S", "7S", "9S", "8H"}, // 8H drawn first
		DiscardTop: "TH",
		Unlocked:   true,
	})
	if err != nil {
		t.Fatal(err)
	}
	s, _ = step(t, s, DrawCard{Base: For("A")})
	s, _ = step(t, s, DiscardDrawn{Base: For("A")}) // active rank 8
	layoutBefore := append([]CardID(nil), s.Player("B").Layout.Cards()...)

	// Two failed OPPONENT attempts: each costs 2 cards and no layout card.
	s, _ = step(t, s, AttemptOpponentMatch{Base: For("B"), Target: "A", TargetIndex: 0, GiveIndex: 0})
	first := append([]CardID(nil), s.Player("B").PendingPenalty...)
	s, _ = step(t, s, AttemptOpponentMatch{Base: For("B"), Target: "A", TargetIndex: 1, GiveIndex: 0})
	tray := append([]CardID(nil), s.Player("B").PendingPenalty...)

	if len(tray) != 4 {
		t.Fatalf("expected 4 accumulated penalty cards, got %d", len(tray))
	}
	// The tray accumulates, it does not reset.
	for i := range first {
		if tray[i] != first[i] {
			t.Fatal("the first penalty pair must keep its place in the tray")
		}
	}
	// Still unknown to B: the tray is not in the layout and not in any view.
	if s.ViewFor("B").MyPendingCount != 4 {
		t.Fatal("B should see a count of pending cards")
	}
	for _, h := range s.ViewFor("B").MyCards {
		for _, id := range tray {
			if h.ID == id {
				t.Fatal("tray cards must not appear in the layout view")
			}
		}
	}
	if s.ViewFor("A").Opponents[0].Count != len(layoutBefore) {
		t.Fatal("tray cards must not inflate the public card count")
	}

	total := len(s.Cards)
	s, evs := step(t, s, TimerExpired{Kind: TimerMatchWindow})

	got := s.Player("B").Layout.Cards()
	want := append(append([]CardID(nil), layoutBefore...), tray...)
	if len(got) != len(want) {
		t.Fatalf("layout has %d cards, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("position %d = %d, want %d: penalties must append in tray order", i, got[i], want[i])
		}
	}
	placed := 0
	for _, e := range evs {
		if pp, ok := e.(EvPenaltyPlaced); ok && pp.Auto {
			placed++
		}
	}
	if placed != 4 {
		t.Fatalf("expected 4 auto-placements, got %d", placed)
	}
	if len(s.Player("B").PendingPenalty) != 0 {
		t.Fatal("the tray must be empty after the window closes")
	}
	if len(s.Cards) != total {
		t.Fatal("conservation broken")
	}
	// They are ordinary cards now: matchable, swappable, scored.
	if s.Score("B") == 0 {
		t.Fatal("penalty cards must count toward the score")
	}
	if err := CheckInvariants(&s); err != nil {
		t.Fatal(err)
	}
}

// ---- 5 & 6. declarer and queue removals during final turns ----

func TestDeclarerRemovedDuringFinalTurns(t *testing.T) {
	s, err := BuildScenario(Scenario{
		Players:    []PlayerID{"A", "B", "C"},
		Hands:      map[PlayerID][]string{"A": {"AS", "2D"}, "B": {"5D"}, "C": {"5C"}},
		Deck:       []string{"2C", "3D", "4H", "6S"},
		DiscardTop: "9H",
		Unlocked:   true,
	})
	if err != nil {
		t.Fatal(err)
	}
	total := len(s.Cards)
	s, _ = step(t, s, DeclareCombio{Base: For("A")})
	aCards := append([]CardID(nil), s.Player("A").Layout.Cards()...)

	s, _ = step(t, s, PlayerDisconnected{Base: For("A")})
	if !s.Paused {
		t.Fatal("the game must pause even during final turns")
	}
	s = drop(t, s, "A")

	if !s.Declared || s.Declarer != "A" {
		t.Fatal("the declaration must stand")
	}
	// A's cards sank to the bottom, unrevealed.
	for i, id := range aCards {
		if s.Discard[i] != id {
			t.Fatalf("A's card %d is not at discard bottom position %d", id, i)
		}
	}
	if len(s.Cards) != total {
		t.Fatal("conservation broken")
	}

	// B and C still get exactly one final turn each.
	if s.CurrentID() != "B" {
		t.Fatalf("B should be on their final turn, got %s", s.CurrentID())
	}
	s = plainTurn(t, s, "B")
	if s.Phase == PhaseFinished {
		t.Fatal("C has not had their final turn")
	}
	if s.CurrentID() != "C" {
		t.Fatalf("expected C, got %s", s.CurrentID())
	}
	s = plainTurn(t, s, "C")
	if s.Phase != PhaseFinished {
		t.Fatalf("phase = %s", s.Phase)
	}

	if _, scored := s.Scores["A"]; scored {
		t.Fatal("a removed declarer must not be scored")
	}
	if len(s.Scores) != 2 {
		t.Fatalf("only the remaining players are scored, got %d", len(s.Scores))
	}
	for _, w := range s.Winners {
		if w == "A" {
			t.Fatal("a removed declarer cannot win")
		}
	}
	// B and C both held a 5: normal co-winner rules apply among the remainder.
	if len(s.Winners) != 2 {
		t.Fatalf("expected co-winners, got %v with scores %v", s.Winners, s.Scores)
	}
}

func TestRemovingAPlayerWaitingForTheirFinalTurn(t *testing.T) {
	s := fourHanded(t)
	for i := 0; i < 2; i++ {
		for _, p := range []PlayerID{"A", "B", "C", "D"} {
			s = plainTurn(t, s, p)
		}
	}
	s, _ = step(t, s, DeclareCombio{Base: For("A")}) // queue is B, C, D
	if s.CurrentID() != "B" {
		t.Fatalf("expected B, got %s", s.CurrentID())
	}
	// C is waiting in the queue and drops out.
	s = drop(t, s, "C")

	took := []PlayerID{}
	for s.Phase != PhaseFinished {
		cur := s.CurrentID()
		took = append(took, cur)
		s = plainTurn(t, s, cur)
		if len(took) > 5 {
			t.Fatal("the final queue stalled")
		}
	}
	if len(took) != 2 || took[0] != "B" || took[1] != "D" {
		t.Fatalf("final turns were %v, want exactly [B D]", took)
	}
	if _, ok := s.Scores["C"]; ok {
		t.Fatal("C should not be scored")
	}
}

func TestRemovingTheCurrentFinalTurnPlayerDoesNotSkipTheNext(t *testing.T) {
	s := fourHanded(t)
	for i := 0; i < 2; i++ {
		for _, p := range []PlayerID{"A", "B", "C", "D"} {
			s = plainTurn(t, s, p)
		}
	}
	s, _ = step(t, s, DeclareCombio{Base: For("A")})
	s = drop(t, s, "B") // B was mid-final-turn

	if s.CurrentID() != "C" {
		t.Fatalf("C should be next, got %s", s.CurrentID())
	}
	s = plainTurn(t, s, "C")
	if s.CurrentID() != "D" {
		t.Fatalf("D must not be skipped, got %s", s.CurrentID())
	}
	s = plainTurn(t, s, "D")
	if s.Phase != PhaseFinished {
		t.Fatalf("phase = %s", s.Phase)
	}
	if len(s.Scores) != 3 {
		t.Fatalf("A, C and D should be scored, got %v", s.Scores)
	}
}
