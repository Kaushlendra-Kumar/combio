package cli

import (
	"strings"
	"testing"

	"github.com/kaushlendra/combio/backend/internal/engine"
)

// matchTable: A discards an 8. B holds two eights plus a nine, C holds one
// eight, so both of them have real work to do in the window.
func matchTable(t *testing.T) engine.State {
	t.Helper()
	st, err := engine.BuildScenario(engine.Scenario{
		Players: []engine.PlayerID{"p1", "p2", "p3"},
		Hands: map[engine.PlayerID][]string{
			"p1": {"2C", "3D"},
			"p2": {"8S", "8D:B", "9C"},
			"p3": {"8C", "4H"},
		},
		Deck:       []string{"5S", "6S", "7S", "8H"}, // 8H drawn first
		DiscardTop: "TH",
		Unlocked:   true,
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range []engine.Action{
		engine.DrawCard{Base: engine.For("p1")},
		engine.DiscardDrawn{Base: engine.For("p1")},
	} {
		st, _, err = engine.Apply(st, a)
		if err != nil {
			t.Fatal(err)
		}
	}
	if st.ActiveRank != engine.Eight {
		t.Fatalf("setup: active rank is %s", st.ActiveRank)
	}
	return st
}

// The local clockwise protocol must not change any engine semantics: repeated
// attempts, a wrong attempt that does not end the player's turn at the window,
// a correct attempt that does not open a new window, and a pass that ends only
// that player's opportunity.
func TestLocalMatchingProtocolPreservesEngineSemantics(t *testing.T) {
	st := matchTable(t)

	s, buf := newTestSession(st, script(
		"", // press ENTER to open the window
		// Bo (p2) goes first: after the discarder, clockwise.
		"",       // hand over
		"o", "2", // throw the 9C at rank 8 — WRONG, +2 penalty
		"o", "0", // still allowed to act: throw 8S — correct
		"o", "0", // and again: throw 8D — correct
		"p", "0", "0", // place one penalty card, sight unseen
		"n", // done
		"",  // hide
		// Cy (p3) must still get their opportunity even though Bo passed.
		"", "o", "0", "n", "",
		// Ann (p1), the discarder, comes last and passes.
		"", "n", "",
		"", // press ENTER after the window closes
	), true, "Ann", "Bo", "Cy")

	var opened, closed, accepted, failed int
	var rankSeen []engine.Rank
	s.onEvents = func(evs []engine.Event) {
		for _, e := range evs {
			switch e.(type) {
			case engine.EvMatchingWindowOpened:
				opened++
			case engine.EvMatchingWindowClosed:
				closed++
			case engine.EvMatchAccepted:
				accepted++
			case engine.EvMatchFailed:
				failed++
			}
		}
		rankSeen = append(rankSeen, s.st.ActiveRank)
	}

	s.matchingWindow()
	out := buf.String()

	if opened != 0 {
		t.Fatalf("a correct match must not open a new window (saw %d)", opened)
	}
	if closed != 1 {
		t.Fatalf("the window must close exactly once, saw %d", closed)
	}
	if accepted != 3 || failed != 1 {
		t.Fatalf("expected 3 accepted and 1 failed match, got %d and %d", accepted, failed)
	}
	// The active rank held steady at 8 for the whole window and only cleared
	// when the window closed.
	for i, r := range rankSeen[:len(rankSeen)-1] {
		if r != engine.Eight {
			t.Fatalf("active rank changed to %s at step %d", r, i)
		}
	}
	if s.st.ActiveRank != engine.RankNone {
		t.Fatalf("rank should be cleared after the close, got %s", s.st.ActiveRank)
	}

	// Every player was offered the window, in the documented order.
	iBo := strings.Index(out, "Pass the device to: Bo")
	iCy := strings.Index(out, "Pass the device to: Cy")
	iAnn := strings.Index(out, "Pass the device to: Ann")
	if iBo < 0 || iCy < 0 || iAnn < 0 {
		t.Fatal("every active player must be offered the window")
	}
	if !(iBo < iCy && iCy < iAnn) {
		t.Fatal("order must be clockwise from after the discarder, discarder last")
	}
	// Bo passing did not end the window: Cy's successful match came afterwards.
	iClosed := strings.Index(out, "Window closed.")
	if iClosed < iCy {
		t.Fatal("the window closed before Cy had their opportunity")
	}

	// Final counts: Bo started with 3, lost the 9C to a wrong throw, threw two
	// eights, and took 2 penalties -> 2 cards. Cy threw their 8 -> 1 card.
	if got := s.st.Player("p2").Layout.Len(); got != 2 {
		t.Fatalf("Bo holds %d cards, want 2", got)
	}
	if got := s.st.Player("p3").Layout.Len(); got != 1 {
		t.Fatalf("Cy holds %d cards, want 1", got)
	}
	if got := len(s.st.Player("p2").PendingPenalty); got != 0 {
		t.Fatalf("the tray should be empty after the close, holds %d", got)
	}
	if s.phase() != "turn_start" {
		t.Fatalf("phase after the window is %s", s.phase())
	}
}

// The window must not end early just because somebody declines, and it must not
// end by any route other than the engine's own close action.
func TestWindowClosesOnlyThroughTheEngineTimer(t *testing.T) {
	st := matchTable(t)
	s, _ := newTestSession(st, script(
		"",          // open
		"", "n", "", // Bo passes immediately
		"", "n", "", // Cy passes immediately
		"", "n", "", // Ann passes
		"", // close acknowledgement
	), false, "Ann", "Bo", "Cy")

	closedBy := ""
	s.onEvents = func(evs []engine.Event) {
		for _, e := range evs {
			if _, ok := e.(engine.EvMatchingWindowClosed); ok {
				closedBy = s.lastAction
			}
		}
	}
	s.matchingWindow()

	if closedBy != "timer_expired" {
		t.Fatalf("the window closed via %q, expected the engine timer action", closedBy)
	}
	if s.st.Player("p2").Layout.Len() != 3 || s.st.Player("p3").Layout.Len() != 2 {
		t.Fatal("passing must not cost anybody a card")
	}
}

// A player who declines and a player who is removed must both leave the window
// intact for everyone else.
func TestRemovedPlayerIsSkippedAndTheWindowSurvives(t *testing.T) {
	st := matchTable(t)
	var err error
	for _, a := range []engine.Action{
		engine.PlayerDisconnected{Base: engine.For("p2")},
		engine.RemovePlayer{Base: engine.For("p2")},
	} {
		st, _, err = engine.Apply(st, a)
		if err != nil {
			t.Fatal(err)
		}
	}
	s, buf := newTestSession(st, script(
		"",                    // open
		"", "o", "0", "n", "", // Cy matches their 8
		"", "n", "", // Ann passes
		"", // close
	), true, "Ann", "Bo", "Cy")
	s.matchingWindow()

	out := buf.String()
	if strings.Contains(out, "Pass the device to: Bo") {
		t.Fatal("a removed player must not be offered the window")
	}
	if s.st.ActiveRank != engine.RankNone || s.phase() != "turn_start" {
		t.Fatalf("the window should have closed cleanly, phase=%s", s.phase())
	}
	if got := s.st.Player("p3").Layout.Len(); got != 1 {
		t.Fatalf("Cy should have matched their 8, holds %d", got)
	}
}

// Opponent targeting through the prompt, including the card hand-over.
func TestLocalOpponentTargetingUsesTheEngineRules(t *testing.T) {
	st := matchTable(t)
	s, _ := newTestSession(st, script(
		"",                              // open
		"", "t", "1", "0", "0", "n", "", // Bo targets Cy's position 0, giving their own 0
		"", "n", "", // Cy passes
		"", "n", "", // Ann passes
		"", // close
	), false, "Ann", "Bo", "Cy")
	s.matchingWindow()

	// Cy keeps two cards (one swapped in), Bo is one lighter.
	if got := s.st.Player("p3").Layout.Len(); got != 2 {
		t.Fatalf("Cy holds %d cards, want 2", got)
	}
	if got := s.st.Player("p2").Layout.Len(); got != 2 {
		t.Fatalf("Bo holds %d cards, want 2 after giving one away", got)
	}
	if s.st.Hand("p3")[0] != "8S" {
		t.Fatalf("Cy position 0 is %s, want Bo's transferred 8S in the exact gap", s.st.Hand("p3")[0])
	}
}
