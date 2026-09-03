package engine

import (
	"errors"
	"testing"
)

func step(t *testing.T, s State, a Action) (State, []Event) {
	t.Helper()
	ns, evs, err := Apply(s, a)
	if err != nil {
		t.Fatalf("action %s unexpectedly failed: %v", a.Name(), err)
	}
	return ns, evs
}

func rejects(t *testing.T, s State, a Action, want error) {
	t.Helper()
	ns, evs, err := Apply(s, a)
	if !errors.Is(err, want) {
		t.Fatalf("action %s: expected error %v, got %v", a.Name(), want, err)
	}
	if evs != nil {
		t.Fatalf("rejected action emitted events")
	}
	if ns.Phase != s.Phase || len(ns.Deck) != len(s.Deck) || len(ns.Discard) != len(s.Discard) {
		t.Fatalf("rejected action mutated the returned state")
	}
}

func TestCardValues(t *testing.T) {
	cases := map[Rank]int{Ace: -1, Two: 2, Nine: 9, Ten: 10, Jack: 11, Queen: 12, King: 13}
	for r, want := range cases {
		if got := r.Value(); got != want {
			t.Errorf("%s: want %d got %d", r, want, got)
		}
	}
	for _, r := range []Rank{Ten, Jack, Queen, King} {
		if !r.IsPower() {
			t.Errorf("%s should be a power rank", r)
		}
	}
	for _, r := range []Rank{Ace, Two, Nine} {
		if r.IsPower() {
			t.Errorf("%s should not be a power rank", r)
		}
	}
}

func TestBuildDeckUniqueAndColoured(t *testing.T) {
	one, err := BuildDeck(1)
	if err != nil || len(one) != 52 {
		t.Fatalf("one deck: %d cards, err %v", len(one), err)
	}
	two, err := BuildDeck(2)
	if err != nil || len(two) != 104 {
		t.Fatalf("two decks: %d cards, err %v", len(two), err)
	}
	seen := map[CardID]bool{}
	red, blue := 0, 0
	perRank := map[Rank]int{}
	for _, c := range two {
		if seen[c.ID] {
			t.Fatalf("duplicate CardID %d", c.ID)
		}
		seen[c.ID] = true
		perRank[c.Rank]++
		if c.Deck == DeckRed {
			red++
		} else {
			blue++
		}
	}
	if red != 52 || blue != 52 {
		t.Fatalf("deck colours not split evenly: red=%d blue=%d", red, blue)
	}
	for r := Ace; r <= King; r++ {
		if perRank[r] != 8 {
			t.Fatalf("rank %s appears %d times across two decks, want 8", r, perRank[r])
		}
	}
	if _, err := BuildDeck(3); err == nil {
		t.Fatal("expected error for 3 decks")
	}
}

func TestDecksForPlayerCount(t *testing.T) {
	for n := 2; n <= 4; n++ {
		if DecksFor(n) != 1 {
			t.Fatalf("%d players should use 1 deck", n)
		}
	}
	for n := 5; n <= 8; n++ {
		if DecksFor(n) != 2 {
			t.Fatalf("%d players should use 2 decks", n)
		}
	}
}

func TestLayoutInsertAnywhere(t *testing.T) {
	l := NewLayout([]CardID{1, 2, 3})
	if err := l.Insert(0, 9); err != nil {
		t.Fatal(err)
	}
	assertOrder(t, &l, 9, 1, 2, 3)

	l = NewLayout([]CardID{1, 2, 3})
	if err := l.Insert(2, 9); err != nil {
		t.Fatal(err)
	}
	assertOrder(t, &l, 1, 2, 9, 3)

	l = NewLayout([]CardID{1, 2, 3})
	if err := l.Insert(3, 9); err != nil {
		t.Fatal(err)
	}
	assertOrder(t, &l, 1, 2, 3, 9)

	if err := l.Insert(99, 5); err == nil {
		t.Fatal("expected out-of-range insert to fail")
	}
}

func TestLayoutGapClosesNoHoles(t *testing.T) {
	l := NewLayout([]CardID{1, 2, 3, 4, 5, 6})
	if _, err := l.RemoveAt(1); err != nil {
		t.Fatal(err)
	}
	assertOrder(t, &l, 1, 3, 4, 5, 6)
	if l.Len() != 5 {
		t.Fatalf("length after removal = %d", l.Len())
	}
	// Relative order of survivors is preserved.
	pos, err := l.RemoveCard(5)
	if err != nil || pos != 3 {
		t.Fatalf("RemoveCard returned pos=%d err=%v", pos, err)
	}
	assertOrder(t, &l, 1, 3, 4, 6)
}

func TestLayoutVersioningAndStaleResolution(t *testing.T) {
	l := NewLayout([]CardID{10, 20, 30})
	v0 := l.Version()

	// Someone else's action shifts everything left.
	if _, err := l.RemoveAt(0); err != nil {
		t.Fatal(err)
	}
	if l.Version() == v0 {
		t.Fatal("version did not advance after mutation")
	}

	// A click on "index 1" made against v0 meant physical card 20, which is
	// still present at index 0. Physical identity wins over index.
	id, ok := l.Resolve(v0, 1)
	if !ok || id != 20 {
		t.Fatalf("stale resolve gave %d ok=%v, want card 20", id, ok)
	}
	if !l.Contains(id) {
		t.Fatal("card 20 should still be in the layout")
	}

	// A click on the card that already left resolves to it, and Contains says
	// it is gone: that is the stale case, not a wrong match.
	gone, ok := l.Resolve(v0, 0)
	if !ok || gone != 10 {
		t.Fatalf("resolve of departed card gave %d ok=%v", gone, ok)
	}
	if l.Contains(gone) {
		t.Fatal("card 10 should be gone")
	}
}

func TestLayoutSwapKeepsPosition(t *testing.T) {
	l := NewLayout([]CardID{1, 2, 3})
	out, err := l.Swap(1, 99)
	if err != nil || out != 2 {
		t.Fatalf("swap returned %d err=%v", out, err)
	}
	assertOrder(t, &l, 1, 99, 3)
}

func assertOrder(t *testing.T, l *Layout, want ...CardID) {
	t.Helper()
	got := l.Cards()
	if len(got) != len(want) {
		t.Fatalf("layout = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("layout = %v, want %v", got, want)
		}
	}
}

func TestParseCardSpec(t *testing.T) {
	r, s, c, err := ParseCardSpec("10D:B")
	if err != nil || r != Ten || s != Diamonds || c != DeckBlue {
		t.Fatalf("got %s %s %s err=%v", r, s, c, err)
	}
	r, _, c, _ = ParseCardSpec("AH")
	if r != Ace || c != DeckRed {
		t.Fatalf("AH parsed as %s %s", r, c)
	}
	if _, _, _, err := ParseCardSpec("ZZ"); err == nil {
		t.Fatal("expected parse failure")
	}
}
