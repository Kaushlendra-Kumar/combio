package cli

import (
	"strings"
	"testing"

	"github.com/kaushlendra/combio/backend/internal/engine"
)

const debugBanner = "*** DEBUG MODE — HIDDEN CARDS VISIBLE ***"

func powerTable(t *testing.T, deck string) engine.State {
	t.Helper()
	st, err := engine.BuildScenario(engine.Scenario{
		Players: []engine.PlayerID{"p1", "p2", "p3"},
		Hands: map[engine.PlayerID][]string{
			"p1": {"2C", "3D"}, "p2": {"KS", "4H"}, "p3": {"9D", "6S"},
		},
		Deck:       []string{deck},
		DiscardTop: "2H",
		Unlocked:   true,
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range []engine.Action{
		engine.DrawCard{Base: engine.For("p1")},
		engine.DiscardDrawn{Base: engine.For("p1")},
		engine.TimerExpired{Kind: engine.TimerMatchWindow},
	} {
		st, _, err = engine.Apply(st, a)
		if err != nil {
			t.Fatal(err)
		}
	}
	return st
}

// ---------- 4. debug is opt-in only ----------

func TestDebugNeverAppearsWithoutTheFlag(t *testing.T) {
	// A whole interactive turn with debug off.
	st, _, err := engine.NewGame(engine.Config{CardsPerPlayer: 4},
		[]engine.PlayerID{"p1", "p2"}, engine.NewSeededShuffler(42))
	if err != nil {
		t.Fatal(err)
	}
	s, buf := newTestSession(st, script(
		"", "0 1", "", "",
		"", "0 1", "", "",
		"", "d", "", "d", "",
		"", "", "n", "", "",
	), false, "Ann", "Bo")
	s.memoryPhase()
	s.turn()
	if s.phase() == "matching" {
		s.matchingWindow()
	}
	out := buf.String()
	if strings.Contains(out, debugBanner) {
		t.Fatal("the debug banner appeared without --debug")
	}
	if strings.Contains(out, "invariants:") || strings.Contains(out, "layout:") {
		t.Fatal("raw state was dumped without --debug")
	}
	// Card ids only ever appear in the debug dump.
	if strings.Contains(out, ")#") {
		t.Fatal("card ids leaked without --debug")
	}

	// With the flag, and only with the flag, the dump appears.
	s2, buf2 := newTestSession(st, script("", "0 1", "", "", "", "0 1", "", ""), false, "Ann", "Bo")
	s2.debug = true
	s2.memoryPhase()
	if !strings.Contains(buf2.String(), debugBanner) {
		t.Fatal("--debug should show the dump")
	}
}

func TestReplayAndScenarioNeverDumpRawStateByDefault(t *testing.T) {
	out, err := Replay(11, 3, 4, false)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, debugBanner) || strings.Contains(out, ")#") {
		t.Fatal("a normal replay dumped raw state")
	}
	withDebug, err := Replay(11, 3, 4, true)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(withDebug, debugBanner) {
		t.Fatal("replay --debug should include the dump")
	}
}

// An error surfaced on the shared screen must not carry a card identity.
func TestSessionErrorOutputCarriesNoIdentities(t *testing.T) {
	st, err := engine.BuildScenario(engine.Scenario{
		Players:    []engine.PlayerID{"p1", "p2"},
		Hands:      map[engine.PlayerID][]string{"p1": {"KD", "QS"}, "p2": {"JH", "AC"}},
		Deck:       []string{"5S"},
		DiscardTop: "9H",
		Unlocked:   true,
	})
	if err != nil {
		t.Fatal(err)
	}
	s, buf := newTestSession(st, "", false, "Ann", "Bo")
	// Several rejected actions, including the impossible-state path.
	s.apply(engine.DrawCard{Base: engine.For("p2")})
	s.apply(engine.Peek10{Base: engine.For("p1"), Index: 0})
	s.apply(engine.DrawCard{Base: engine.For("p1")})
	s.apply(engine.DrawCard{Base: engine.For("p1")})
	out := buf.String()
	if !strings.Contains(out, "!!") {
		t.Fatal("errors should be reported to the player")
	}
	mustNotContain(t, "error output", out, "KD", "QS", "JH", "AC")
}

// ---------- interactive powers ----------

func TestInteractivePowerPrompts(t *testing.T) {
	t.Run("10 peeks at an own card", func(t *testing.T) {
		st := powerTable(t, "TD")
		s, buf := newTestSession(st, script("", "1", "", ""), false, "Ann", "Bo", "Cy")
		s.resolvePower()
		mustContain(t, "the peek", buf.String(), "you see Ann's position 1: 3D(R)")
		if s.phase() != "turn_start" {
			t.Fatalf("phase = %s", s.phase())
		}
	})

	t.Run("jack peeks at an opponent", func(t *testing.T) {
		st := powerTable(t, "JC")
		s, buf := newTestSession(st, script("", "0", "0", "", ""), false, "Ann", "Bo", "Cy")
		s.resolvePower()
		mustContain(t, "the peek", buf.String(), "you see Bo's position 0: KS(R)")
	})

	t.Run("queen swaps blind", func(t *testing.T) {
		st := powerTable(t, "QH")
		s, buf := newTestSession(st, script("", "y", "0", "0", "1", ""), false, "Ann", "Bo", "Cy")
		s.resolvePower()
		if s.st.Hand("p1")[0] != "4H" || s.st.Hand("p2")[1] != "2C" {
			t.Fatalf("swap did not land in the vacated slots: %v / %v", s.st.Hand("p1"), s.st.Hand("p2"))
		}
		if strings.Contains(buf.String(), "you see") {
			t.Fatal("a Queen swap must reveal nothing")
		}
	})

	t.Run("queen may be declined", func(t *testing.T) {
		st := powerTable(t, "QH")
		s, _ := newTestSession(st, script("", "n", ""), false, "Ann", "Bo", "Cy")
		before := s.st.Hand("p1")[0]
		s.resolvePower()
		if s.st.Hand("p1")[0] != before {
			t.Fatal("declining must move nothing")
		}
	})

	t.Run("king peeks then swaps", func(t *testing.T) {
		st := powerTable(t, "KD")
		s, buf := newTestSession(st, script("", "0", "0", "", "y", "1", ""), false, "Ann", "Bo", "Cy")
		s.resolvePower()
		mustContain(t, "the peek", buf.String(), "you see Bo's position 0: KS(R)")
		if s.st.Hand("p1")[1] != "KS" || s.st.Hand("p2")[0] != "3D" {
			t.Fatalf("king swap wrong: %v / %v", s.st.Hand("p1"), s.st.Hand("p2"))
		}
	})

	t.Run("king may cancel", func(t *testing.T) {
		st := powerTable(t, "KD")
		s, _ := newTestSession(st, script("", "0", "0", "", "n", ""), false, "Ann", "Bo", "Cy")
		s.resolvePower()
		if s.st.Hand("p1")[1] != "3D" {
			t.Fatal("cancelling must move nothing")
		}
		if s.phase() != "turn_start" {
			t.Fatalf("phase = %s", s.phase())
		}
	})
}

// ---------- draw decisions ----------

func TestInteractiveDrawDecisions(t *testing.T) {
	base := func(t *testing.T, hand []string) engine.State {
		t.Helper()
		st, err := engine.BuildScenario(engine.Scenario{
			Players:    []engine.PlayerID{"p1", "p2"},
			Hands:      map[engine.PlayerID][]string{"p1": hand, "p2": {"KS", "4H"}},
			Deck:       []string{"7H"},
			DiscardTop: "2H",
			Unlocked:   true,
		})
		if err != nil {
			t.Fatal(err)
		}
		return st
	}

	t.Run("keep and insert anywhere", func(t *testing.T) {
		s, _ := newTestSession(base(t, []string{"2C", "3D", "9S"}), script(
			"", "d", "", "k", "1", "0", "",
		), false, "Ann", "Bo")
		s.turn()
		if s.st.Hand("p1")[0] != "7H" {
			t.Fatalf("the kept card should sit at position 0, hand is %v", s.st.Hand("p1"))
		}
		if s.st.ActiveRank != engine.Three {
			t.Fatalf("the removed 3D should set the rank, got %s", s.st.ActiveRank)
		}
	})

	t.Run("discard the drawn card", func(t *testing.T) {
		s, _ := newTestSession(base(t, []string{"2C", "3D"}), script("", "d", "", "d", ""), false, "Ann", "Bo")
		s.turn()
		if s.st.ActiveRank != engine.Seven {
			t.Fatalf("active rank = %s", s.st.ActiveRank)
		}
		if s.st.Player("p1").Layout.Len() != 2 {
			t.Fatal("discarding the drawn card must not touch the hand")
		}
	})

	t.Run("zero-card player keeps a first card", func(t *testing.T) {
		s, buf := newTestSession(base(t, nil), script("", "d", "", "y", ""), false, "Ann", "Bo")
		s.turn()
		if s.st.Player("p1").Layout.Len() != 1 {
			t.Fatal("Ann should now hold one card")
		}
		if s.st.ActiveRank != engine.RankNone {
			t.Fatal("nothing was removed, so no window should open")
		}
		mustNotContain(t, "the public feed", strings.Join(s.feed, "\n"), "7H(R)")
		mustContain(t, "Ann's screen", buf.String(), "you drew 7H(R)")
	})

	t.Run("declaration from the turn menu", func(t *testing.T) {
		s, buf := newTestSession(base(t, []string{"AS"}), script("", "c", ""), false, "Ann", "Bo")
		s.turn()
		if !s.st.Declared || s.st.Declarer != "p1" {
			t.Fatal("the declaration should have been applied")
		}
		mustContain(t, "the screen", buf.String(), "declares COMBIO")
	})
}

// ---------- disconnect and results ----------

func TestInteractiveDisconnectPaths(t *testing.T) {
	build := func(t *testing.T) engine.State {
		t.Helper()
		st, err := engine.BuildScenario(engine.Scenario{
			Players: []engine.PlayerID{"p1", "p2", "p3"},
			Hands: map[engine.PlayerID][]string{
				"p1": {"2C", "3D"}, "p2": {"KS", "4H"}, "p3": {"9D"},
			},
			Deck:       []string{"5S", "6S"},
			DiscardTop: "TH",
			Unlocked:   true,
		})
		if err != nil {
			t.Fatal(err)
		}
		return st
	}

	t.Run("reconnect resumes", func(t *testing.T) {
		s, buf := newTestSession(build(t), script("", "x", "", "1", "y"), false, "Ann", "Bo", "Cy")
		s.turn()
		if s.st.Paused {
			t.Fatal("a reconnect should lift the pause")
		}
		if s.st.Player("p2").Removed {
			t.Fatal("Bo should still be in the game")
		}
		mustContain(t, "the screen", buf.String(), "is back; play resumes")
	})

	t.Run("removal sinks their cards", func(t *testing.T) {
		s, buf := newTestSession(build(t), script("", "x", "", "1", "n", ""), false, "Ann", "Bo", "Cy")
		total := len(s.st.Cards)
		bo := append([]engine.CardID(nil), s.st.Player("p2").Layout.Cards()...)
		s.turn()
		if !s.st.Player("p2").Removed {
			t.Fatal("Bo should be out")
		}
		for i, id := range bo {
			if s.st.Discard[i] != id {
				t.Fatalf("Bo's card %d is not at discard bottom position %d", id, i)
			}
		}
		if len(s.st.Cards) != total {
			t.Fatal("cards were destroyed")
		}
		if err := engine.CheckInvariants(&s.st); err != nil {
			t.Fatal(err)
		}
		mustContain(t, "the screen", buf.String(), "sank to the bottom of the discard pile")
	})
}

func TestResultsScreenAndSummary(t *testing.T) {
	st, err := engine.BuildScenario(engine.Scenario{
		Players:    []engine.PlayerID{"p1", "p2"},
		Hands:      map[engine.PlayerID][]string{"p1": {"AS"}, "p2": {"KC"}},
		Deck:       []string{"2C", "3D"},
		DiscardTop: "9H",
		Unlocked:   true,
	})
	if err != nil {
		t.Fatal(err)
	}
	s, buf := newTestSession(st, "", false, "Ann", "Bo")
	s.apply(engine.DeclareCombio{Base: engine.For("p1")})
	for _, a := range []engine.Action{
		engine.DrawCard{Base: engine.For("p2")},
		engine.DiscardDrawn{Base: engine.For("p2")},
		engine.TimerExpired{Kind: engine.TimerMatchWindow},
	} {
		s.apply(a)
	}
	s.results()

	out := buf.String()
	mustContain(t, "the results screen", out, "GAME OVER", "WINNER: Ann", "AS(R)", "KC(R)", "(declared)")
	if !strings.Contains(s.Summary(), "Ann") {
		t.Fatalf("summary = %q", s.Summary())
	}
	if len(s.Feed()) == 0 {
		t.Fatal("the feed should be populated")
	}
	if !s.quit {
		t.Fatal("results should end the session")
	}
}

// ---------- input guards ----------

// Bad input must never reach the engine as an out-of-range action.
func TestPromptsClampAndRetryBeforeAnyActionIsBuilt(t *testing.T) {
	io1, buf := testIO(script("nine", "99", "-1", "2"), false)
	if got := io1.Int("pick", 0, 3); got != 2 {
		t.Fatalf("Int returned %d, want 2 after three rejects", got)
	}
	mustContain(t, "the prompt", buf.String(), "??")

	io2, _ := testIO(script("0 0", "0 1 2", "1 2"), false)
	got := io2.Ints("positions", 2, 0, 3)
	if len(got) != 2 || got[0] != 1 || got[1] != 2 {
		t.Fatalf("Ints returned %v, want [1 2]", got)
	}

	io3, _ := testIO(script("maybe", "y"), false)
	if !io3.YesNo("ok?") {
		t.Fatal("YesNo should accept y after a reject")
	}

	io4, _ := testIO(script("z", "b"), false)
	if k := io4.Choose("pick", []Option{{"a", "A"}, {"b", "B"}}); k != "b" {
		t.Fatalf("Choose returned %q", k)
	}

	// Exhausted input must stop the session rather than spin.
	io5, _ := testIO("", false)
	io5.Int("pick", 0, 3)
	if !io5.EOF() {
		t.Fatal("an empty stream should set EOF")
	}

	// Endless rubbish gives up instead of looping forever.
	io6, _ := testIO(strings.Repeat("nope\n", 40), false)
	io6.Int("pick", 0, 3)
	if !io6.EOF() {
		t.Fatal("too many invalid inputs should abort")
	}
}

func TestSessionStopsCleanlyWhenInputRunsOut(t *testing.T) {
	st, _, err := engine.NewGame(engine.Config{CardsPerPlayer: 4},
		[]engine.PlayerID{"p1", "p2"}, engine.NewSeededShuffler(3))
	if err != nil {
		t.Fatal(err)
	}
	s, buf := newTestSession(st, script("", "0 1"), false, "Ann", "Bo")
	s.Run()
	mustContain(t, "the transcript", buf.String(), "input ended")
}

// ---------- setup wizard ----------

func TestSetupBuildsTheRequestedTable(t *testing.T) {
	io1, buf := testIO(script(
		"3", // players
		"5", // invalid cards per player
		"6", // valid
		"Ann", "n",
		"Bo", "y",
		"Cy", "n",
		"77", // seed
	), false)
	s, ok := Setup(io1, false, 0)
	if !ok {
		t.Fatalf("setup failed:\n%s", buf.String())
	}
	if len(s.players) != 3 {
		t.Fatalf("dealt %d seats", len(s.players))
	}
	if s.players[1].Name != "Bo" || !s.players[1].Bot {
		t.Fatalf("seat 2 is %+v, want a bot named Bo", s.players[1])
	}
	if s.players[0].Bot || s.players[2].Bot {
		t.Fatal("only Bo should be a bot")
	}
	for i := range s.st.Players {
		if s.st.Players[i].Layout.Len() != 6 {
			t.Fatalf("seat %d was dealt %d cards", i, s.st.Players[i].Layout.Len())
		}
	}
	if s.phase() != "memory" {
		t.Fatalf("a new game should start in the memory phase, got %s", s.phase())
	}
	mustContain(t, "setup", buf.String(), "cards per player must be 4, 6 or 8", "seed: 77")
	if strings.Contains(buf.String(), debugBanner) {
		t.Fatal("setup showed the debug banner without the flag")
	}

	// A seed passed on the command line is not asked for again.
	io2, buf2 := testIO(script("2", "4", "Ann", "n", "Bo", "n"), false)
	s2, ok := Setup(io2, true, 123)
	if !ok {
		t.Fatalf("seeded setup failed:\n%s", buf2.String())
	}
	mustContain(t, "seeded setup", buf2.String(), "seed: 123", debugBanner)
	if strings.Contains(buf2.String(), "Deterministic seed (blank") {
		t.Fatal("a supplied seed should not be asked for")
	}
	// Same seed, same deal.
	io3, _ := testIO(script("2", "4", "Ann", "n", "Bo", "n"), false)
	s3, _ := Setup(io3, false, 123)
	if s2.st.Hand("p1")[0] != s3.st.Hand("p1")[0] {
		t.Fatal("the same seed must deal the same game")
	}
}

// The main loop can land on a resumed draw_decision, which hands the device
// over before asking.
func TestDrawDecisionResumesWithAHandOver(t *testing.T) {
	st, err := engine.BuildScenario(engine.Scenario{
		Players:    []engine.PlayerID{"p1", "p2"},
		Hands:      map[engine.PlayerID][]string{"p1": {"2C", "3D"}, "p2": {"KS"}},
		Deck:       []string{"7H"},
		DiscardTop: "2H",
		Unlocked:   true,
	})
	if err != nil {
		t.Fatal(err)
	}
	st, _, err = engine.Apply(st, engine.DrawCard{Base: engine.For("p1")})
	if err != nil {
		t.Fatal(err)
	}
	s, buf := newTestSession(st, script("", "d", ""), true, "Ann", "Bo")
	s.drawDecision(s.players[0])
	mustContain(t, "the resumed prompt", buf.String(), "Pass the device to: Ann", "You are holding 7H(R)")
	if s.st.ActiveRank != engine.Seven {
		t.Fatalf("active rank = %s", s.st.ActiveRank)
	}
}
