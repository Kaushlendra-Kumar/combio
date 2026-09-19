package engine

import "testing"

func TestStackedShufflerForcesExactOrder(t *testing.T) {
	ids := []CardID{1, 2, 3, 4, 5}
	sh := &StackedShuffler{Order: []CardID{4, 2}}
	sh.Shuffle(ids)
	if ids[0] != 4 || ids[1] != 2 {
		t.Fatalf("stacked cards should come first, got %v", ids)
	}
	// Unnamed cards keep their relative order behind the stacked ones.
	rest := ids[2:]
	want := []CardID{1, 3, 5}
	for i := range want {
		if rest[i] != want[i] {
			t.Fatalf("tail = %v, want %v", rest, want)
		}
	}
}

func TestNoShuffleIsIdentity(t *testing.T) {
	ids := []CardID{3, 1, 2}
	NoShuffle{}.Shuffle(ids)
	if ids[0] != 3 || ids[1] != 1 || ids[2] != 2 {
		t.Fatalf("NoShuffle changed the order: %v", ids)
	}
}

func TestSeededShufflerIsReproducible(t *testing.T) {
	build := func() []CardID {
		cards, _ := BuildDeck(1)
		ids := make([]CardID, 0, len(cards))
		for _, c := range cards {
			ids = append(ids, c.ID)
		}
		return ids
	}
	a, b := build(), build()
	NewSeededShuffler(99).Shuffle(a)
	NewSeededShuffler(99).Shuffle(b)
	for i := range a {
		if a[i] != b[i] {
			t.Fatal("the same seed must produce the same order")
		}
	}
	c := build()
	NewSeededShuffler(100).Shuffle(c)
	same := true
	for i := range a {
		if a[i] != c[i] {
			same = false
			break
		}
	}
	if same {
		t.Fatal("different seeds should produce different orders")
	}
}

func TestDealtGameIsReproducibleFromASeed(t *testing.T) {
	s1, _, _ := NewGame(Config{CardsPerPlayer: 4}, ids(3), NewSeededShuffler(42))
	s2, _, _ := NewGame(Config{CardsPerPlayer: 4}, ids(3), NewSeededShuffler(42))
	for i := range s1.Players {
		a := s1.Players[i].Layout.Cards()
		b := s2.Players[i].Layout.Cards()
		for j := range a {
			if a[j] != b[j] {
				t.Fatal("identical seeds must deal identical hands")
			}
		}
	}
}

func TestFinishedViewRevealsEveryHand(t *testing.T) {
	s, err := BuildScenario(Scenario{
		Players:    []PlayerID{"A", "B"},
		Hands:      map[PlayerID][]string{"A": {"AS"}, "B": {"KD"}},
		Deck:       []string{"2C", "3C"},
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
	v := s.ViewFor("B")
	if !v.Finished || len(v.Winners) != 1 || v.Winners[0] != "A" {
		t.Fatalf("finished view is wrong: %+v", v)
	}
	if len(v.Reveal) != 2 || len(v.Scores) != 2 {
		t.Fatal("the reveal should cover every active player")
	}
	if v.Scores["A"] != -1 {
		t.Fatalf("A's ace should score -1, got %d", v.Scores["A"])
	}
}

func TestStateCardLookup(t *testing.T) {
	s := threeHanded(t)
	id := s.Player("A").Layout.Cards()[0]
	if c, ok := s.Card(id); !ok || c.ID != id {
		t.Fatal("known card should resolve")
	}
	if _, ok := s.Card(CardID(9999)); ok {
		t.Fatal("unknown card should not resolve")
	}
	l := NewLayout(nil)
	if !l.Empty() {
		t.Fatal("a layout with no cards is empty")
	}
}
