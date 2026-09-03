package engine

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

// ---- 7. the declarer is fully exposed during final turns ----

func TestFinalTurnOpponentMatchRewritesTheDeclarersScore(t *testing.T) {
	s, err := BuildScenario(Scenario{
		Players:    []PlayerID{"A", "B"},
		Hands:      map[PlayerID][]string{"A": {"AS", "8H"}, "B": {"KC", "3D"}},
		Deck:       []string{"7C", "6C", "8D"}, // 8D drawn first
		DiscardTop: "9H",
		Unlocked:   true,
	})
	if err != nil {
		t.Fatal(err)
	}
	ace := s.Player("A").Layout.Cards()[0]
	eight := s.Player("A").Layout.Cards()[1]
	king := s.Player("B").Layout.Cards()[0]

	// A declares holding A♠ + 8♥ = 7.
	if s.Score("A") != 7 {
		t.Fatalf("setup: A scores %d", s.Score("A"))
	}
	s, _ = step(t, s, DeclareCombio{Base: For("A")})

	// B's final turn: discard an 8, then use remembered knowledge to strip the
	// declarer's 8 and hand them the King instead.
	s, _ = step(t, s, DrawCard{Base: For("B")})
	s, _ = step(t, s, DiscardDrawn{Base: For("B")})
	s, evs := step(t, s, AttemptOpponentMatch{
		Base: For("B"), Target: "A", TargetIndex: 1, GiveIndex: 0,
	})
	if !HasEvent(evs, "match_accepted") || !HasEvent(evs, "card_transferred") {
		t.Fatal("B should have removed the declarer's 8")
	}

	got := s.Player("A").Layout.Cards()
	if len(got) != 2 || got[0] != ace || got[1] != king {
		t.Fatalf("A's layout = %v, want [ace king] with the King in the vacated slot", s.Hand("A"))
	}
	if s.Player("A").Layout.Contains(eight) {
		t.Fatal("the declarer's 8 should be out of play")
	}
	// A went from 7 to -1 + 13 = 12; B went from 16 to 3.
	if s.Score("A") != 12 {
		t.Fatalf("A now scores %d, want 12", s.Score("A"))
	}
	if s.Score("B") != 3 {
		t.Fatalf("B now scores %d, want 3", s.Score("B"))
	}

	s, _ = step(t, s, TimerExpired{Kind: TimerMatchWindow})
	if s.Phase != PhaseFinished {
		t.Fatalf("phase = %s", s.Phase)
	}
	// Scoring must use the MODIFIED layout, so the declaration fails.
	if s.Scores["A"] != 12 || s.Scores["B"] != 3 {
		t.Fatalf("final scores %v, want A=12 B=3", s.Scores)
	}
	fin, _ := FindEvent([]Event{}, "").(EvGameFinished)
	_ = fin
	if len(s.Winners) != 1 || s.Winners[0] != "B" {
		t.Fatalf("winners = %v, want B (A's declaration was sabotaged)", s.Winners)
	}
}

// ---- 8. private reveals leave no residue anywhere ----

func TestPrivateRevealDoesNotFollowTheCardAsMetadata(t *testing.T) {
	s, err := BuildScenario(Scenario{
		Players: []PlayerID{"A", "B", "C"},
		Hands: map[PlayerID][]string{
			"A": {"2S", "3S"},
			"B": {"KH", "4D"},
			"C": {"5C", "6D"},
		},
		Deck:       []string{"QD", "JC"}, // JC drawn first
		DiscardTop: "9H",
		Unlocked:   true,
	})
	if err != nil {
		t.Fatal(err)
	}
	king := s.Player("B").Layout.Cards()[0]

	// 1. A privately learns B's King with the Jack.
	s, _ = step(t, s, DrawCard{Base: For("A")})
	s, _ = step(t, s, DiscardDrawn{Base: For("A")})
	s, _ = step(t, s, TimerExpired{Kind: TimerMatchWindow})
	s, evs := step(t, s, JackPeek{Base: For("A"), Target: "B", TargetIndex: 0})
	if len(FilterEvents(evs, "B")) != len(evs)-1 || len(FilterEvents(evs, "C")) != len(evs)-1 {
		t.Fatal("only A may receive the reveal")
	}

	// 2. B blind-swaps that physical card to C with a Queen.
	s, _ = step(t, s, DrawCard{Base: For("B")})
	s, _ = step(t, s, DiscardDrawn{Base: For("B")})
	s, _ = step(t, s, TimerExpired{Kind: TimerMatchWindow})
	s, evs = step(t, s, QueenSwap{Base: For("B"), OwnIndex: 0, Target: "C", TargetIndex: 0})
	if !s.Player("C").Layout.Contains(king) {
		t.Fatal("the King should now be with C")
	}
	// The swap event names positions only, for everybody.
	sw, ok := FindEvent(evs, "cards_swapped").(EvCardsSwapped)
	if !ok || sw.Audience() != nil {
		t.Fatal("the swap should be a public, identity-free event")
	}
	if HasEvent(evs, "private_reveal") {
		t.Fatal("a Queen swap must reveal nothing, not even to the swapper")
	}
	raw, err := json.Marshal(struct {
		E EvCardsSwapped
	}{sw})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), fmt.Sprintf("%d", king)) {
		t.Fatalf("the swap event leaked the physical card id: %s", raw)
	}

	// 3. Every player's view of the moved card is identical and identity-free.
	for _, who := range []PlayerID{"A", "B", "C"} {
		v := s.ViewFor(who)
		blob, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		// Exactly one rank field in the whole view: the public discard top.
		if n := strings.Count(string(blob), "\"rank\""); n != 1 {
			t.Fatalf("%s's view exposes %d rank fields: %s", who, n, blob)
		}
		// No field anywhere records that the card was once revealed to A.
		for _, word := range []string{"revealed", "known", "seen", "peek"} {
			if strings.Contains(strings.ToLower(string(blob)), word) {
				t.Fatalf("%s's view carries reveal metadata (%q)", who, word)
			}
		}
	}

	// The original viewer gets no engine help either: A's view of C's card is
	// byte-identical to the third party's view of it.
	find := func(v PlayerView, owner PlayerID, id CardID) (CardHandle, bool) {
		for _, o := range v.Opponents {
			if o.ID != owner {
				continue
			}
			for _, h := range o.Cards {
				if h.ID == id {
					return h, true
				}
			}
		}
		return CardHandle{}, false
	}
	hA, okA := find(s.ViewFor("A"), "C", king)
	hB, okB := find(s.ViewFor("B"), "C", king)
	if !okA || !okB || hA != hB {
		t.Fatalf("the viewer who peeked sees something different: %+v vs %+v", hA, hB)
	}

	// And the state itself holds no reveal ledger.
	sBlob, err := json.Marshal(s.Players)
	if err != nil {
		t.Fatal(err)
	}
	for _, word := range []string{"revealed", "knownby", "seenby"} {
		if strings.Contains(strings.ToLower(string(sBlob)), word) {
			t.Fatalf("player state carries reveal metadata (%q)", word)
		}
	}
}

// ---- 9. deterministic terminal declaration paths ----

func TestTerminalPathSuccessfulDeclaration(t *testing.T) {
	s := finishTwoHanded(t, []string{"AS"}, []string{"KC"})
	if len(s.Winners) != 1 || s.Winners[0] != "A" {
		t.Fatalf("-1 beats 13; winners = %v scores = %v", s.Winners, s.Scores)
	}
}

func TestTerminalPathFailedDeclaration(t *testing.T) {
	s := finishTwoHanded(t, []string{"KC"}, []string{"AS"})
	if len(s.Winners) != 1 || s.Winners[0] != "B" {
		t.Fatalf("the declaration should fail; winners = %v scores = %v", s.Winners, s.Scores)
	}
	if s.Scores["A"] != 13 {
		t.Fatalf("A should still be scored, got %v", s.Scores)
	}
}

func TestTerminalPathDeclarerTieLoses(t *testing.T) {
	s := finishTwoHanded(t, []string{"5C"}, []string{"5D"})
	if len(s.Winners) != 1 || s.Winners[0] != "B" {
		t.Fatalf("a tie must fail the declaration; winners = %v", s.Winners)
	}
}

func TestTerminalPathZeroCardDeclarerWinsAndLoses(t *testing.T) {
	// Zero cards = 0, which beats a positive hand...
	s := finishTwoHanded(t, nil, []string{"3D"})
	if len(s.Winners) != 1 || s.Winners[0] != "A" || s.Scores["A"] != 0 {
		t.Fatalf("zero cards should score 0 and win here; winners=%v scores=%v", s.Winners, s.Scores)
	}
	// ...but loses to an Ace.
	s = finishTwoHanded(t, nil, []string{"AS"})
	if len(s.Winners) != 1 || s.Winners[0] != "B" {
		t.Fatalf("an Ace (-1) must beat zero cards (0); winners=%v scores=%v", s.Winners, s.Scores)
	}
}

func TestTerminalPathCoWinnersAmongNonDeclarers(t *testing.T) {
	s, err := BuildScenario(Scenario{
		Players:    []PlayerID{"A", "B", "C"},
		Hands:      map[PlayerID][]string{"A": {"KC"}, "B": {"AS"}, "C": {"AD"}},
		Deck:       []string{"2C", "3D", "4H", "5S"},
		DiscardTop: "9H",
		Unlocked:   true,
	})
	if err != nil {
		t.Fatal(err)
	}
	s, _ = step(t, s, DeclareCombio{Base: For("A")})
	s = plainTurn(t, s, "B")
	s = plainTurn(t, s, "C")
	if s.Phase != PhaseFinished {
		t.Fatalf("phase = %s", s.Phase)
	}
	if len(s.Winners) != 2 {
		t.Fatalf("B and C both hold an Ace and should co-win; winners = %v scores = %v", s.Winners, s.Scores)
	}
}

// finishTwoHanded runs A declares -> B takes their final turn -> reveal, with
// the two hands given. B's turn is a plain draw-and-discard so the hands stand.
func finishTwoHanded(t *testing.T, aHand, bHand []string) State {
	t.Helper()
	s, err := BuildScenario(Scenario{
		Players:    []PlayerID{"A", "B"},
		Hands:      map[PlayerID][]string{"A": aHand, "B": bHand},
		Deck:       []string{"2C", "3D"},
		DiscardTop: "9H",
		Unlocked:   true,
	})
	if err != nil {
		t.Fatal(err)
	}
	s, _ = step(t, s, DeclareCombio{Base: For("A")})
	s = plainTurn(t, s, "B")
	if s.Phase != PhaseFinished {
		t.Fatalf("game did not finish: %s", s.Debug())
	}
	return s
}

// ---- 10. rejected actions must not disturb the injected randomness ----

func fingerprint(s *State) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s|deck=%v|discard=%v|rounds=%d|pending=%v|rank=%s|drawn=%d",
		s.Phase, s.Deck, s.Discard, s.RoundsCompleted, s.RoundPending, s.ActiveRank, s.Drawn)
	for i := range s.Players {
		p := &s.Players[i]
		fmt.Fprintf(&b, "||%s:%v:v%d:tray%v:turns%d:removed%v",
			p.ID, p.Layout.Cards(), p.Layout.Version(), p.PendingPenalty, p.TurnsCompleted, p.Removed)
	}
	return b.String()
}

// The shuffler lives on the State as an unexported field and is shared across
// clones, so a rejected action must never consume randomness. The check uses a
// state whose next draw forces a reshuffle: if the RNG had advanced, the
// recycled deck order would differ.
func TestRejectedActionsDoNotAdvanceRandomness(t *testing.T) {
	build := func() State {
		s, err := BuildScenario(Scenario{
			Players:    []PlayerID{"A", "B"},
			Hands:      map[PlayerID][]string{"A": {"2C", "3D"}, "B": {"KS", "4H"}},
			Deck:       []string{"5S", "6S", "7S", "8H", "9C", "TD"},
			DiscardTop: "TH",
			Unlocked:   true,
		})
		if err != nil {
			t.Fatal(err)
		}
		// Move the whole draw deck under the active discard card so the very
		// next draw must reshuffle, consuming the injected RNG.
		s.Discard = append(append([]CardID(nil), s.Deck...), s.Discard...)
		s.Deck = nil
		s.SetShuffler(NewSeededShuffler(1234))
		if err := CheckInvariants(&s); err != nil {
			t.Fatal(err)
		}
		return s
	}

	pristine := build()
	poked := build()
	if fingerprint(&pristine) != fingerprint(&poked) {
		t.Fatal("the two builds should start identical")
	}

	// Throw a pile of illegal actions at the second state.
	illegal := []Action{
		KeepDrawn{Base: For("A"), RemoveIndex: 0, InsertIndex: 0},
		DiscardDrawn{Base: For("A")},
		AttemptOwnMatch{Base: For("B"), Index: 0},
		AttemptOpponentMatch{Base: For("A"), Target: "B", TargetIndex: 0, GiveIndex: 0},
		Peek10{Base: For("A"), Index: 0},
		JackPeek{Base: For("A"), Target: "B", TargetIndex: 0},
		SkipPower{Base: For("A")},
		InsertPenalty{Base: For("A"), TrayIndex: 0, Position: 0},
		DrawCard{Base: For("B")},        // not B's turn
		DeclareCombio{Base: For("B")},   // not B's turn
		TimerExpired{Kind: TimerPower},  // wrong phase
		TimerExpired{Kind: TimerMemory}, // wrong phase
		InspectMemory{Base: For("A"), Positions: []int{0}},
		RemovePlayer{Base: For("C")}, // unknown player
	}
	for _, a := range illegal {
		if _, _, err := Apply(poked, a); err == nil {
			t.Fatalf("%s should have been rejected", a.Name())
		}
	}
	if fingerprint(&poked) != fingerprint(&pristine) {
		t.Fatal("a rejected action mutated the state")
	}

	// Now the same valid, reshuffle-forcing action against both.
	a := DrawCard{Base: For("A")}
	p1, _ := step(t, pristine, a)
	p2, _ := step(t, poked, a)
	if fingerprint(&p1) != fingerprint(&p2) {
		t.Fatalf("randomness diverged after rejected actions:\n pristine: %s\n poked:    %s",
			fingerprint(&p1), fingerprint(&p2))
	}
	if len(p1.Deck) == 0 {
		t.Fatal("the test needs the draw to have triggered a reshuffle")
	}

	// And a longer valid sequence stays in lockstep.
	for _, act := range []Action{
		KeepDrawn{Base: For("A"), RemoveIndex: 0, InsertIndex: 1},
		TimerExpired{Kind: TimerMatchWindow},
		DrawCard{Base: For("B")},
		DiscardDrawn{Base: For("B")},
		TimerExpired{Kind: TimerMatchWindow},
	} {
		p1, _ = step(t, p1, act)
		p2, _ = step(t, p2, act)
		if fingerprint(&p1) != fingerprint(&p2) {
			t.Fatalf("divergence after %s", act.Name())
		}
	}
}

// A failed draw on a genuinely exhausted table must not shuffle either.
func TestImpossibleDrawDoesNotConsumeRandomness(t *testing.T) {
	s, err := BuildScenario(Scenario{
		Players:    []PlayerID{"A", "B"},
		Hands:      map[PlayerID][]string{"A": {"2C"}, "B": {"3D"}},
		DiscardTop: "9H",
	})
	if err != nil {
		t.Fatal(err)
	}
	s.SetShuffler(NewSeededShuffler(7))
	before := fingerprint(&s)
	if _, _, err := Apply(s, DrawCard{Base: For("A")}); err == nil {
		t.Fatal("expected the impossible-state error")
	}
	if fingerprint(&s) != before {
		t.Fatal("the failed draw mutated the state")
	}
}
