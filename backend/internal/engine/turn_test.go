package engine

import "testing"

func TestNewGameDealsAndSeedsDiscard(t *testing.T) {
	for _, n := range []int{2, 4, 5, 8} {
		for _, hand := range []int{4, 6, 8} {
			s, evs, err := NewGame(Config{CardsPerPlayer: hand}, ids(n), NewSeededShuffler(7))
			if err != nil {
				t.Fatalf("%d players %d cards: %v", n, hand, err)
			}
			if s.Phase != PhaseMemory {
				t.Fatalf("expected memory phase, got %s", s.Phase)
			}
			if got := len(s.Cards); got != 52*DecksFor(n) {
				t.Fatalf("%d players: %d cards in game", n, got)
			}
			for i := range s.Players {
				if s.Players[i].Layout.Len() != hand {
					t.Fatalf("player %d dealt %d cards", i, s.Players[i].Layout.Len())
				}
			}
			if len(s.Discard) != 1 {
				t.Fatalf("discard should hold exactly the initial face-up card")
			}
			// The initial face-up card sets no rank and arms no power.
			if s.ActiveRank != RankNone || s.PendingPower != RankNone {
				t.Fatalf("initial discard armed something: rank=%s power=%s", s.ActiveRank, s.PendingPower)
			}
			if !HasEvent(evs, "memory_phase_started") {
				t.Fatal("missing memory_phase_started")
			}
			if err := CheckInvariants(&s); err != nil {
				t.Fatal(err)
			}
		}
	}
}

func TestNewGameRejectsBadTables(t *testing.T) {
	if _, _, err := NewGame(Config{CardsPerPlayer: 4}, ids(1), NoShuffle{}); err != ErrPlayerCount {
		t.Fatal("expected player count rejection")
	}
	if _, _, err := NewGame(Config{CardsPerPlayer: 4}, ids(9), NoShuffle{}); err != ErrPlayerCount {
		t.Fatal("expected player count rejection")
	}
	if _, _, err := NewGame(Config{CardsPerPlayer: 5}, ids(4), NoShuffle{}); err != ErrHandSize {
		t.Fatal("expected hand size rejection")
	}
}

func TestMemoryPhaseInspectsExactlyHalf(t *testing.T) {
	s, _, err := NewGame(Config{CardsPerPlayer: 6}, ids(3), NewSeededShuffler(1))
	if err != nil {
		t.Fatal(err)
	}
	rejects(t, s, InspectMemory{Base: For("p1"), Positions: []int{0, 1}}, ErrMemoryCount)
	rejects(t, s, InspectMemory{Base: For("p1"), Positions: []int{0, 1, 2, 3}}, ErrMemoryCount)
	rejects(t, s, InspectMemory{Base: For("p1"), Positions: []int{0, 0, 1}}, ErrMemoryCount)
	rejects(t, s, InspectMemory{Base: For("p1"), Positions: []int{0, 1, 99}}, ErrBadIndex)

	s, evs := step(t, s, InspectMemory{Base: For("p1"), Positions: []int{0, 2, 4}})
	rev, _ := FindEvent(evs, "memory_revealed").(EvMemoryRevealed)
	if len(rev.Cards) != 3 {
		t.Fatalf("expected 3 revealed cards, got %d", len(rev.Cards))
	}
	if aud := rev.Audience(); len(aud) != 1 || aud[0] != "p1" {
		t.Fatalf("memory reveal audience = %v, must be private", aud)
	}
	rejects(t, s, InspectMemory{Base: For("p1"), Positions: []int{1, 3, 5}}, ErrAlreadyInspected)

	s, _ = step(t, s, InspectMemory{Base: For("p2"), Positions: []int{0, 1, 2}})
	if s.Phase != PhaseMemory {
		t.Fatal("phase advanced before every player inspected")
	}
	s, evs = step(t, s, InspectMemory{Base: For("p3"), Positions: []int{0, 1, 2}})
	if s.Phase != PhaseTurnStart {
		t.Fatalf("expected turn start, got %s", s.Phase)
	}
	if !HasEvent(evs, "memory_phase_ended") || !HasEvent(evs, "turn_started") {
		t.Fatal("missing phase transition events")
	}
	if s.CurrentID() != "p1" {
		t.Fatalf("first turn should be seat 0, got %s", s.CurrentID())
	}
}

func TestMemoryTimerForcesPhaseEnd(t *testing.T) {
	s, _, _ := NewGame(Config{CardsPerPlayer: 4}, ids(2), NewSeededShuffler(3))
	s, _ = step(t, s, TimerExpired{Kind: TimerMemory})
	if s.Phase != PhaseTurnStart {
		t.Fatalf("memory timer should force turn start, got %s", s.Phase)
	}
}

func baseScenario(t *testing.T) State {
	t.Helper()
	s, err := BuildScenario(Scenario{
		Players: []PlayerID{"A", "B", "C"},
		Hands: map[PlayerID][]string{
			"A": {"2C", "5D", "KH"},
			"B": {"5S", "9C", "8D"},
			"C": {"AS", "8H", "3C"},
		},
		Deck:       []string{"4C", "QD", "7S"}, // 7S is drawn first
		DiscardTop: "2H",
	})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestDrawIsPrivate(t *testing.T) {
	s := baseScenario(t)
	rejects(t, s, DrawCard{Base: For("B")}, ErrNotYourTurn)

	s, evs := step(t, s, DrawCard{Base: For("A")})
	if s.Phase != PhaseDrawDecision {
		t.Fatalf("phase = %s", s.Phase)
	}
	pd, ok := FindEvent(evs, "private_draw").(EvPrivateDraw)
	if !ok || pd.Card.Rank != Seven {
		t.Fatalf("expected private draw of 7S, got %+v", pd)
	}
	if aud := pd.Audience(); len(aud) != 1 || aud[0] != "A" {
		t.Fatalf("private draw audience = %v", aud)
	}
	pub, ok := FindEvent(evs, "card_drawn").(EvCardDrawn)
	if !ok || pub.Audience() != nil {
		t.Fatal("card_drawn must be public and identity-free")
	}
	if s.ViewFor("B").MyDrawnCard != nil {
		t.Fatal("opponent view leaked the drawn card")
	}
	if v := s.ViewFor("A"); v.MyDrawnCard == nil || v.MyDrawnCard.Rank != Seven {
		t.Fatal("drawer's own view should show the drawn card")
	}
}

func TestKeepDrawnInsertsAnywhereAndOpensWindow(t *testing.T) {
	for _, tc := range []struct {
		insert int
		want   []string
	}{
		{0, []string{"7S", "2C", "KH"}},
		{1, []string{"2C", "7S", "KH"}},
		{2, []string{"2C", "KH", "7S"}},
	} {
		s := baseScenario(t)
		s, _ = step(t, s, DrawCard{Base: For("A")})
		s, evs := step(t, s, KeepDrawn{Base: For("A"), RemoveIndex: 1, InsertIndex: tc.insert})

		got := s.Hand("A")
		if len(got) != 3 {
			t.Fatalf("hand size changed: %v", got)
		}
		for i := range tc.want {
			if got[i] != tc.want[i] {
				t.Fatalf("insert at %d gave %v, want %v", tc.insert, got, tc.want)
			}
		}
		// The removed 5D is revealed, becomes the active rank, opens a window.
		if s.ActiveRank != Five {
			t.Fatalf("active rank = %s", s.ActiveRank)
		}
		if s.Phase != PhaseMatching {
			t.Fatalf("phase = %s", s.Phase)
		}
		kept, _ := FindEvent(evs, "card_kept").(EvCardKept)
		if kept.Removed == nil || kept.Removed.Rank != Five {
			t.Fatal("card_kept must reveal the removed card")
		}
		if !HasEvent(evs, "matching_window_opened") {
			t.Fatal("Option A must open a matching window")
		}
	}
}

func TestOptionANeverArmsAPower(t *testing.T) {
	s, err := BuildScenario(Scenario{
		Players:    []PlayerID{"A", "B"},
		Hands:      map[PlayerID][]string{"A": {"KH", "2C"}, "B": {"3D", "4S"}},
		Deck:       []string{"6C"},
		DiscardTop: "9H",
	})
	if err != nil {
		t.Fatal(err)
	}
	s, _ = step(t, s, DrawCard{Base: For("A")})
	// Removing the King from hand discards a King, but hand cards never fire.
	s, _ = step(t, s, KeepDrawn{Base: For("A"), RemoveIndex: 0, InsertIndex: 0})
	if s.ActiveRank != King {
		t.Fatalf("active rank should be King, got %s", s.ActiveRank)
	}
	if s.PendingPower != RankNone {
		t.Fatal("a hand card discarded via Option A must not arm a power")
	}
	s, _ = step(t, s, TimerExpired{Kind: TimerMatchWindow})
	if s.Phase != PhaseTurnStart {
		t.Fatalf("expected the turn to end, got phase %s", s.Phase)
	}
}

func TestDiscardDrawnSetsRankAndArmsPower(t *testing.T) {
	s := baseScenario(t)
	s, _ = step(t, s, DrawCard{Base: For("A")}) // 7S
	s, evs := step(t, s, DiscardDrawn{Base: For("A")})
	if s.ActiveRank != Seven || s.PendingPower != RankNone {
		t.Fatalf("rank=%s power=%s", s.ActiveRank, s.PendingPower)
	}
	cd, _ := FindEvent(evs, "card_discarded").(EvCardDiscarded)
	if !cd.FromDrawn {
		t.Fatal("discard should be flagged as coming from the drawn card")
	}
}

func TestTurnTimeoutForcesDrawDiscardAndSkipsPower(t *testing.T) {
	s, err := BuildScenario(Scenario{
		Players:    []PlayerID{"A", "B"},
		Hands:      map[PlayerID][]string{"A": {"2C", "3D"}, "B": {"4S", "5H"}},
		Deck:       []string{"9C", "KD"}, // KD drawn first
		DiscardTop: "6H",
	})
	if err != nil {
		t.Fatal(err)
	}
	s, evs := step(t, s, TimerExpired{Kind: TimerTurn})
	if !HasEvent(evs, "turn_timed_out") || !HasEvent(evs, "card_discarded") {
		t.Fatal("turn timeout must force a draw and a discard")
	}
	if s.ActiveRank != King {
		t.Fatalf("forced discard should set the rank, got %s", s.ActiveRank)
	}
	if s.PendingPower != RankNone {
		t.Fatal("a timed-out turn must not arm the King power")
	}
	sk, _ := FindEvent(evs, "power_skipped").(EvPowerSkipped)
	if sk.Reason != "turn timeout" {
		t.Fatalf("power_skipped reason = %q", sk.Reason)
	}
}

func ids(n int) []PlayerID {
	out := make([]PlayerID, 0, n)
	for i := 1; i <= n; i++ {
		out = append(out, PlayerID("p"+string(rune('0'+i))))
	}
	return out
}
