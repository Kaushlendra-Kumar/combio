package engine

import (
	"encoding/json"
	"math/rand"
	"reflect"
	"strings"
	"testing"
)

func TestCardHandleCarriesNoIdentity(t *testing.T) {
	// Structural guarantee: a hidden card is described by a type that has no
	// rank or suit field, so it cannot leak even through a future refactor.
	tp := reflect.TypeOf(CardHandle{})
	allowed := map[string]bool{"Position": true, "ID": true, "Deck": true}
	for i := 0; i < tp.NumField(); i++ {
		if !allowed[tp.Field(i).Name] {
			t.Fatalf("CardHandle gained a field that could leak identity: %s", tp.Field(i).Name)
		}
	}
}

func TestViewNeverLeaksHiddenCards(t *testing.T) {
	s := threeHanded(t)
	s, _ = step(t, s, DrawCard{Base: For("A")})

	v := s.ViewFor("B")
	if v.MyDrawnCard != nil {
		t.Fatal("B must not see A's drawn card")
	}
	if len(v.Opponents) != 2 {
		t.Fatalf("expected 2 opponents, got %d", len(v.Opponents))
	}
	for _, o := range v.Opponents {
		if o.Count != len(o.Cards) {
			t.Fatal("opponent card count and handles disagree")
		}
	}
	// Serialise the whole view: no rank or suit key may appear for any card
	// other than the publicly visible discard top.
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Count(string(raw), "\"rank\""); got != 1 {
		t.Fatalf("view exposes %d rank fields, expected exactly one (the discard top): %s", got, raw)
	}

	// Deck colour, by contrast, IS public: that is what makes two-toned decks
	// trackable.
	for _, o := range v.Opponents {
		for _, h := range o.Cards {
			if h.ID == NoCard {
				t.Fatal("handles must carry a physical card id")
			}
		}
	}
}

func TestFilterEventsRespectsAudience(t *testing.T) {
	s := threeHanded(t)
	_, evs := step(t, s, DrawCard{Base: For("A")})
	forA := FilterEvents(evs, "A")
	forB := FilterEvents(evs, "B")
	if !HasEvent(forA, "private_draw") {
		t.Fatal("A should receive their own private draw")
	}
	if HasEvent(forB, "private_draw") {
		t.Fatal("B must never receive A's private draw")
	}
	if !HasEvent(forB, "card_drawn") {
		t.Fatal("B should still see that a draw happened")
	}
}

func TestPausedViewIsFlagged(t *testing.T) {
	s := threeHanded(t)
	s, _ = step(t, s, PlayerDisconnected{Base: For("B")})
	v := s.ViewFor("A")
	if !v.Paused || v.DeclareAvailable {
		t.Fatal("a paused game must not offer the COMBIO button")
	}
}

// ---- full-game integration ----

// gameStats records which terminal and mid-game paths a scripted game actually
// exercised, so the random suite can prove its own coverage instead of assuming
// it.
type gameStats struct {
	declaredSuccess bool
	declaredFailed  bool
	declarerTied    bool
	zeroCardEnding  bool
	negativeScore   bool
	aceInFinalHand  bool
	coWinners       bool
	reshuffled      bool
}

func (g *gameStats) merge(o gameStats) {
	g.declaredSuccess = g.declaredSuccess || o.declaredSuccess
	g.declaredFailed = g.declaredFailed || o.declaredFailed
	g.declarerTied = g.declarerTied || o.declarerTied
	g.zeroCardEnding = g.zeroCardEnding || o.zeroCardEnding
	g.negativeScore = g.negativeScore || o.negativeScore
	g.aceInFinalHand = g.aceInFinalHand || o.aceInFinalHand
	g.coWinners = g.coWinners || o.coWinners
	g.reshuffled = g.reshuffled || o.reshuffled
}

// playRandomGame drives a complete game with a deterministic policy, asserting
// the engine's invariants after every single transition.
func playRandomGame(t *testing.T, players int, cards int, seed int64) State {
	s, _ := playRandomGameStats(t, players, cards, seed)
	return s
}

func playRandomGameStats(t *testing.T, players int, cards int, seed int64) (State, gameStats) {
	t.Helper()
	var stats gameStats
	rng := rand.New(rand.NewSource(seed))
	s, _, err := NewGame(Config{CardsPerPlayer: cards}, ids(players), NewSeededShuffler(seed))
	if err != nil {
		t.Fatal(err)
	}
	reshuffled := false

	do := func(a Action) bool {
		ns, evs, err := Apply(s, a)
		if err != nil {
			return false
		}
		s = ns
		if HasEvent(evs, "deck_reshuffled") {
			reshuffled = true
		}
		if err := CheckInvariants(&s); err != nil {
			t.Fatalf("invariant broken after %s: %v", a.Name(), err)
		}
		return true
	}

	firstTargetable := func(except PlayerID) (PlayerID, bool) {
		for _, p := range s.ActivePlayers() {
			if p.ID != except && p.Targetable() {
				return p.ID, true
			}
		}
		return "", false
	}

	for turn := 0; turn < 20000 && s.Phase != PhaseFinished; turn++ {
		switch s.Phase {
		case PhaseMemory:
			for _, p := range s.ActivePlayers() {
				if p.MemoryDone {
					continue
				}
				pos := make([]int, 0, cards/2)
				for i := 0; i < cards/2; i++ {
					pos = append(pos, i)
				}
				do(InspectMemory{Base: For(p.ID), Positions: pos})
				break
			}

		case PhaseTurnStart:
			cur := s.CurrentID()
			// Declare only once the deck has cycled, so long games and
			// reshuffles are actually exercised.
			if reshuffled && s.DeclareUnlocked() && rng.Intn(4) == 0 {
				if do(DeclareCombio{Base: For(cur)}) {
					continue
				}
			}
			if !do(DrawCard{Base: For(cur)}) {
				t.Fatalf("could not draw at turn %d: %s", turn, s.Debug())
			}

		case PhaseDrawDecision:
			cur := s.DrawnBy
			p := s.Player(cur)
			// A plausible player policy: keep low cards, throw high ones. The
			// drawer is entitled to see their own card, so this uses only
			// information the engine legitimately gives them. It also means
			// Aces survive to scoring, which the coverage assertions rely on.
			val := s.MustCard(s.Drawn).Value()
			keep := val <= 4 || (val <= 7 && rng.Intn(3) == 0)
			switch {
			case keep && p.Layout.Len() == 0:
				do(KeepDrawn{Base: For(cur)})
			case keep:
				rm := rng.Intn(p.Layout.Len())
				ins := rng.Intn(p.Layout.Len())
				do(KeepDrawn{Base: For(cur), RemoveIndex: rm, InsertIndex: ins})
			default:
				do(DiscardDrawn{Base: For(cur)})
			}

		case PhaseMatching:
			// Occasionally throw a card at the pile, right or wrong, then close.
			// Pick a random claimant each time rather than always the first
			// seat, so penalties and transfers spread across the table.
			pick := func() *PlayerState {
				act := s.ActivePlayers()
				if len(act) == 0 {
					return nil
				}
				p := act[rng.Intn(len(act))]
				if p.Layout.Len() == 0 {
					return nil
				}
				return p
			}
			if rng.Intn(3) == 0 {
				if p := pick(); p != nil {
					idx := rng.Intn(p.Layout.Len())
					do(AttemptOwnMatch{Base: For(p.ID), Index: idx, Version: p.Layout.Version()})
				}
			}
			if rng.Intn(4) == 0 {
				if p := pick(); p != nil {
					if tgt, ok := firstTargetable(p.ID); ok {
						do(AttemptOpponentMatch{
							Base: For(p.ID), Target: tgt, TargetIndex: 0,
							TargetVersion: s.Player(tgt).Layout.Version(),
							GiveIndex:     0, GiveVersion: p.Layout.Version(),
						})
					}
				}
			}
			do(TimerExpired{Kind: TimerMatchWindow})

		case PhasePower:
			actor := s.PowerActor
			p := s.Player(actor)
			tgt, hasTgt := firstTargetable(actor)
			switch s.PendingPower {
			case Ten:
				do(Peek10{Base: For(actor), Index: 0})
			case Jack:
				if hasTgt {
					do(JackPeek{Base: For(actor), Target: tgt, TargetIndex: 0})
				}
			case Queen:
				if hasTgt && p.Layout.Len() > 0 && rng.Intn(2) == 0 {
					do(QueenSwap{Base: For(actor), OwnIndex: 0, Target: tgt, TargetIndex: 0})
				} else {
					do(SkipPower{Base: For(actor)})
				}
			case King:
				if hasTgt {
					do(KingPeek{Base: For(actor), Target: tgt, TargetIndex: 0})
				}
			}
			if s.Phase == PhasePower {
				do(TimerExpired{Kind: TimerPower})
			}

		case PhaseKingDecision:
			actor := s.PowerActor
			if rng.Intn(2) == 0 && s.Player(actor).Layout.Len() > 0 {
				do(KingSwap{Base: For(actor), OwnIndex: 0})
			} else {
				do(CancelKingSwap{Base: For(actor)})
			}
		}
	}

	if s.Phase != PhaseFinished {
		t.Fatalf("game did not finish: %s", s.Debug())
	}
	if !reshuffled {
		t.Fatal("expected the draw deck to be exhausted and reshuffled at least once")
	}
	if len(s.Winners) == 0 {
		t.Fatal("a finished game must have at least one winner")
	}
	if len(s.Scores) != s.ActiveCount() {
		t.Fatal("every active player must be scored")
	}

	stats.reshuffled = reshuffled
	stats.coWinners = len(s.Winners) > 1
	for id, sc := range s.Scores {
		if sc < 0 {
			stats.negativeScore = true
		}
		if sc == 0 && s.Player(id).Layout.Len() == 0 {
			stats.zeroCardEnding = true
		}
		sum := 0
		for _, cid := range s.Player(id).Layout.Cards() {
			c := s.MustCard(cid)
			if c.Rank == Ace {
				stats.aceInFinalHand = true
			}
			sum += c.Value()
		}
		if sum != sc {
			t.Fatalf("%s scored %d but their revealed hand sums to %d", id, sc, sum)
		}
	}
	if s.Declared {
		if len(s.Winners) == 1 && s.Winners[0] == s.Declarer {
			stats.declaredSuccess = true
		} else {
			stats.declaredFailed = true
			if ds, ok := s.Scores[s.Declarer]; ok {
				for id, sc := range s.Scores {
					if id != s.Declarer && sc == ds {
						stats.declarerTied = true
					}
				}
			}
		}
	}
	return s, stats
}

func TestFullGameTwoPlayersOneDeck(t *testing.T) {
	s := playRandomGame(t, 2, 4, 11)
	if len(s.Cards) != 52 {
		t.Fatalf("two players should use one deck, got %d cards", len(s.Cards))
	}
}

func TestFullGameEightPlayersTwoDecks(t *testing.T) {
	s := playRandomGame(t, 8, 8, 23)
	if len(s.Cards) != 104 {
		t.Fatalf("eight players should use two decks, got %d cards", len(s.Cards))
	}
}

func TestFullGamesAcrossEveryTableSize(t *testing.T) {
	var all gameStats
	for players := 2; players <= 8; players++ {
		for _, cards := range []int{4, 6, 8} {
			seed := int64(players*100 + cards)
			_, st := playRandomGameStats(t, players, cards, seed)
			all.merge(st)
		}
	}
	// The random suite must actually reach the interesting terminal paths, not
	// merely finish. Anything it cannot guarantee has a deterministic test of
	// its own (see verify_purity_test.go).
	for _, c := range []struct {
		name string
		got  bool
	}{
		{"successful declaration", all.declaredSuccess},
		{"failed declaration", all.declaredFailed},
		{"an Ace counted as -1 in a real final score", all.aceInFinalHand},
		{"deck exhaustion and reshuffle", all.reshuffled},
	} {
		if !c.got {
			t.Errorf("the 21 scripted games never exercised: %s", c.name)
		}
	}
	// Paths the scripted policy does not reliably reach have deterministic
	// tests of their own: declarer ties (TestTerminalPathDeclarerTieLoses),
	// zero-card declarers (TestTerminalPathZeroCardDeclarerWinsAndLoses),
	// co-winners (TestTerminalPathCoWinnersAmongNonDeclarers) and negative
	// totals (TestAceIsMinusOneEndToEnd).
	t.Logf("paths covered: success=%v failed=%v declarerTie=%v zeroCardEnding=%v ace=%v negativeTotal=%v coWinners=%v",
		all.declaredSuccess, all.declaredFailed, all.declarerTied, all.zeroCardEnding,
		all.aceInFinalHand, all.negativeScore, all.coWinners)
}

func TestApplyIsPureOnRejection(t *testing.T) {
	s := threeHanded(t)
	before := s.Debug()
	handBefore := append([]string(nil), s.Hand("A")...)

	// A pile of illegal actions must leave the caller's state byte-identical.
	illegal := []Action{
		KeepDrawn{Base: For("A"), RemoveIndex: 0, InsertIndex: 0},
		AttemptOwnMatch{Base: For("B"), Index: 0},
		Peek10{Base: For("A"), Index: 0},
		DeclareCombio{Base: For("B")},
		InsertPenalty{Base: For("C"), TrayIndex: 0, Position: 0},
	}
	for _, a := range illegal {
		if _, _, err := Apply(s, a); err == nil {
			t.Fatalf("%s should have been rejected", a.Name())
		}
	}
	if s.Debug() != before {
		t.Fatal("a rejected action mutated the caller's state")
	}
	for i, c := range s.Hand("A") {
		if c != handBefore[i] {
			t.Fatal("a rejected action mutated a layout")
		}
	}
}

func TestApplyDoesNotAliasTheCallerState(t *testing.T) {
	s := threeHanded(t)
	snapshot := append([]string(nil), s.Hand("A")...)

	next, _ := step(t, s, DrawCard{Base: For("A")})
	next, _ = step(t, next, KeepDrawn{Base: For("A"), RemoveIndex: 0, InsertIndex: 1})

	for i, c := range s.Hand("A") {
		if c != snapshot[i] {
			t.Fatal("the original state changed when the successor was mutated")
		}
	}
	if next.Hand("A")[0] == snapshot[0] {
		t.Fatal("the successor state should differ")
	}
}
