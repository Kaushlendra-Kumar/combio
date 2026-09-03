package scenario

import (
	"fmt"
	"sort"

	"github.com/kaushlendra/combio/backend/internal/engine"
)

func p(id string) engine.Base { return engine.For(engine.PlayerID(id)) }

// plainTurn is draw -> discard -> close window, for a deck stacked with
// non-power cards so nothing else interrupts.
func plainTurn(id string, note string) []Step {
	return []Step{
		{Note: note, Action: engine.DrawCard{Base: p(id)}},
		{Action: engine.DiscardDrawn{Base: p(id)}},
		{Action: engine.TimerExpired{Kind: engine.TimerMatchWindow}},
	}
}

func steps(groups ...[]Step) []Step {
	out := []Step{}
	for _, g := range groups {
		out = append(out, g...)
	}
	return out
}

func wantCards(id string, n int) func(*engine.State) error {
	return func(s *engine.State) error {
		got := s.Player(engine.PlayerID(id)).Layout.Len()
		if got != n {
			return fmt.Errorf("%s holds %d cards, want %d", id, got, n)
		}
		return nil
	}
}

func wantWinners(want ...string) func(*engine.State) error {
	return func(s *engine.State) error {
		got := make([]string, 0, len(s.Winners))
		for _, w := range s.Winners {
			got = append(got, string(w))
		}
		sort.Strings(got)
		sort.Strings(want)
		if fmt.Sprint(got) != fmt.Sprint(want) {
			return fmt.Errorf("winners %v, want %v", got, want)
		}
		return nil
	}
}

// All returns every scenario, in a stable order.
func All() []Scenario {
	return []Scenario{
		matchingRace(), wrongMatch(), multiMatch(), opponentTransfer(),
		power10(), powerJack(), powerQueen(), powerKing(),
		zeroCard(), combioSuccess(), combioFailure(), declarerTie(),
		finalTurnTargeting(), disconnectRemoval(), reshuffle(), unlockInterrupted(),
	}
}

// Find looks a scenario up by name.
func Find(name string) (Scenario, bool) {
	for _, s := range All() {
		if s.Name == name {
			return s, true
		}
	}
	return Scenario{}, false
}

// Names lists every scenario name.
func Names() []string {
	out := []string{}
	for _, s := range All() {
		out = append(out, s.Name)
	}
	return out
}

// ---------------------------------------------------------------- scenarios

func matchingRace() Scenario {
	return Scenario{
		Name: "matching-race",
		Desc: "Two players go for the same physical card. The loser is stale, not wrong, and pays nothing.",
		Setup: engine.Scenario{
			Players: []engine.PlayerID{"A", "B", "C", "D"},
			Hands: map[engine.PlayerID][]string{
				"A": {"2C", "3D", "4H"},
				"B": {"9C", "5H", "6D"},
				"C": {"8H", "AS", "5C"},
				"D": {"7S", "KD", "6S"},
			},
			Deck:       []string{"8D:B"},
			DiscardTop: "TH",
			Unlocked:   true,
		},
		Steps: []Step{
			{Note: "A draws an 8 and throws it into the middle", Action: engine.DrawCard{Base: p("A")}},
			{Action: engine.DiscardDrawn{Base: p("A")}},
			{Note: "B and D both remember C's 8 at position 0. B's click lands first."},
			{Action: engine.AttemptOpponentMatch{Base: p("B"), Target: "C", TargetIndex: 0, TargetVersion: 1, GiveIndex: 0}},
			{Note: "D clicks the same physical card a moment later"},
			{Action: engine.AttemptOpponentMatch{Base: p("D"), Target: "C", TargetIndex: 0, TargetVersion: 1, GiveIndex: 0},
				WantErr: engine.ErrStale},
			{Note: "D's hand is untouched: losing a race costs nothing"},
			{Action: engine.TimerExpired{Kind: engine.TimerMatchWindow}},
		},
		Check: wantCards("D", 3),
	}
}

func wrongMatch() Scenario {
	return Scenario{
		Name: "wrong-match",
		Desc: "A wrong throw loses the card AND takes two penalties: net +1. The active rank never changes.",
		Setup: engine.Scenario{
			Players:    []engine.PlayerID{"A", "B"},
			Hands:      map[engine.PlayerID][]string{"A": {"2C", "3D"}, "B": {"KS", "8H", "4D"}},
			Deck:       []string{"5S", "6S", "7S", "9S", "8D"},
			DiscardTop: "TH",
			Unlocked:   true,
		},
		Steps: []Step{
			{Note: "A discards an 8, opening the window", Action: engine.DrawCard{Base: p("A")}},
			{Action: engine.DiscardDrawn{Base: p("A")}},
			{Note: "B misremembers position 0 as an 8 — it is the King"},
			{Action: engine.AttemptOwnMatch{Base: p("B"), Index: 0}},
			{Note: "the rank is still 8, and B may try again in the same window"},
			{Action: engine.AttemptOwnMatch{Base: p("B"), Index: 0}},
			{Note: "window closes; the unplaced penalty cards are appended"},
			{Action: engine.TimerExpired{Kind: engine.TimerMatchWindow}},
		},
		// started with 3, threw 2 away (one wrong, one right), took 2 penalties
		Check: wantCards("B", 3),
	}
}

func multiMatch() Scenario {
	return Scenario{
		Name: "multi-match",
		Desc: "One player throws two eights in a single window. Matching never chains: the rank stays 8.",
		Setup: engine.Scenario{
			Players:    []engine.PlayerID{"A", "B"},
			Hands:      map[engine.PlayerID][]string{"A": {"2C", "3D"}, "B": {"8S", "8D:B", "9C"}},
			Deck:       []string{"8H"},
			DiscardTop: "TH",
			Unlocked:   true,
		},
		Steps: []Step{
			{Action: engine.DrawCard{Base: p("A")}},
			{Note: "A discards 8H", Action: engine.DiscardDrawn{Base: p("A")}},
			{Note: "B throws their first 8 (different suit — only rank matters)"},
			{Action: engine.AttemptOwnMatch{Base: p("B"), Index: 0}},
			{Note: "and the second, from the other deck (different back colour, still an 8)"},
			{Action: engine.AttemptOwnMatch{Base: p("B"), Index: 0}},
			{Action: engine.TimerExpired{Kind: engine.TimerMatchWindow}},
		},
		Check: wantCards("B", 1),
	}
}

func opponentTransfer() Scenario {
	return Scenario{
		Name: "opponent-transfer",
		Desc: "Knocking out a card you remember in someone else's hand, and handing them one of yours into the exact gap.",
		Setup: engine.Scenario{
			Players:    []engine.PlayerID{"A", "B"},
			Hands:      map[engine.PlayerID][]string{"A": {"2C", "KD", "4H"}, "B": {"5C", "8S", "6D"}},
			Deck:       []string{"8H"},
			DiscardTop: "TH",
			Unlocked:   true,
		},
		Steps: []Step{
			{Action: engine.DrawCard{Base: p("A")}},
			{Action: engine.DiscardDrawn{Base: p("A")}},
			{Note: "A remembers B's 8 at position 1 and gives away their King"},
			{Action: engine.AttemptOpponentMatch{Base: p("A"), Target: "B", TargetIndex: 1, GiveIndex: 1}},
			{Note: "A is a card lighter; B still has three, with the King in the vacated slot"},
			{Action: engine.TimerExpired{Kind: engine.TimerMatchWindow}},
		},
		Check: func(s *engine.State) error {
			if err := wantCards("A", 2)(s); err != nil {
				return err
			}
			if err := wantCards("B", 3)(s); err != nil {
				return err
			}
			got := s.Hand("B")[1]
			if got != "KD" {
				return fmt.Errorf("B position 1 is %s, want the transferred KD", got)
			}
			return nil
		},
	}
}

func powerScenario(name, desc, drawn string, tail []Step, check func(*engine.State) error) Scenario {
	return Scenario{
		Name: name,
		Desc: desc,
		Setup: engine.Scenario{
			Players: []engine.PlayerID{"A", "B", "C"},
			Hands: map[engine.PlayerID][]string{
				"A": {"2C", "3D"}, "B": {"KS", "4H"}, "C": {"9D", "6S"},
			},
			Deck:       []string{drawn},
			DiscardTop: "2H",
			Unlocked:   true,
		},
		Steps: steps([]Step{
			{Note: "A draws the power card", Action: engine.DrawCard{Base: p("A")}},
			{Note: "and discards it — only a discarded DRAWN card arms a power",
				Action: engine.DiscardDrawn{Base: p("A")}},
			{Note: "the matching window runs first and is never raced by the power",
				Action: engine.TimerExpired{Kind: engine.TimerMatchWindow}},
		}, tail),
		Check: check,
	}
}

func power10() Scenario {
	return powerScenario("power-10",
		"A discarded 10 lets you peek at one of your OWN cards, privately.",
		"TD",
		[]Step{{Note: "A peeks at their own position 1", Action: engine.Peek10{Base: p("A"), Index: 1}}},
		wantCards("A", 2))
}

func powerJack() Scenario {
	return powerScenario("power-jack",
		"A discarded Jack shows you one opponent card. Only you see it.",
		"JC",
		[]Step{{Note: "A looks at B's position 0", Action: engine.JackPeek{Base: p("A"), Target: "B", TargetIndex: 0}}},
		wantCards("B", 2))
}

func powerQueen() Scenario {
	return powerScenario("power-queen",
		"The Queen is a blind swap: nobody sees either card, not even the swapper. It is optional.",
		"QH",
		[]Step{
			{Note: "A swaps their position 0 with B's position 1, sight unseen",
				Action: engine.QueenSwap{Base: p("A"), OwnIndex: 0, Target: "B", TargetIndex: 1}},
		},
		func(s *engine.State) error {
			if s.Hand("A")[0] != "4H" {
				return fmt.Errorf("A position 0 is %s, want B's old 4H", s.Hand("A")[0])
			}
			if s.Hand("B")[1] != "2C" {
				return fmt.Errorf("B position 1 is %s, want A's old 2C", s.Hand("B")[1])
			}
			return nil
		})
}

func powerKing() Scenario {
	return powerScenario("power-king",
		"The King shows you an opponent card first, then you choose to swap or cancel.",
		"KD",
		[]Step{
			{Note: "A privately sees B's position 0", Action: engine.KingPeek{Base: p("A"), Target: "B", TargetIndex: 0}},
			{Note: "it is the King — A takes it and gives away their 3D",
				Action: engine.KingSwap{Base: p("A"), OwnIndex: 1}},
		},
		func(s *engine.State) error {
			if s.Hand("A")[1] != "KS" {
				return fmt.Errorf("A position 1 is %s, want the swapped-in KS", s.Hand("A")[1])
			}
			if s.Hand("B")[0] != "3D" {
				return fmt.Errorf("B position 0 is %s, want A's old 3D", s.Hand("B")[0])
			}
			return nil
		})
}

func zeroCard() Scenario {
	return Scenario{
		Name: "zero-card",
		Desc: "Zero cards is a normal state, not a win. The 10 and Queen fizzle; the Jack still works; you can keep a card and bluff.",
		Setup: engine.Scenario{
			Players:    []engine.PlayerID{"A", "B"},
			Hands:      map[engine.PlayerID][]string{"A": {}, "B": {"KS", "4H"}},
			Deck:       []string{"5H", "JC", "QD", "TD"}, // TD drawn first
			DiscardTop: "2H",
			Unlocked:   true,
		},
		Steps: []Step{
			{Note: "A holds nothing and discards a 10: nothing to peek at, so it fizzles",
				Action: engine.DrawCard{Base: p("A")}},
			{Action: engine.DiscardDrawn{Base: p("A")}},
			{Action: engine.TimerExpired{Kind: engine.TimerMatchWindow}},
			{Note: "B takes a quiet turn", Action: engine.DrawCard{Base: p("B")}},
			{Action: engine.DiscardDrawn{Base: p("B")}},
			{Action: engine.TimerExpired{Kind: engine.TimerMatchWindow}},
			{Note: "A discards a Jack: it targets someone else, so it works fine at zero cards",
				Action: engine.DrawCard{Base: p("A")}},
			{Action: engine.DiscardDrawn{Base: p("A")}},
			{Action: engine.TimerExpired{Kind: engine.TimerMatchWindow}},
			{Action: engine.JackPeek{Base: p("A"), Target: "B", TargetIndex: 0}},
			{Note: "B again", Action: engine.DrawCard{Base: p("B")}},
			{Action: engine.DiscardDrawn{Base: p("B")}},
			{Action: engine.TimerExpired{Kind: engine.TimerMatchWindow}},
			{Note: "A keeps a 5 as their first card — only A knows what it is",
				Action: engine.DrawCard{Base: p("A")}},
			{Action: engine.KeepDrawn{Base: p("A")}},
		},
		Check: wantCards("A", 1),
	}
}

func combioSuccess() Scenario {
	return Scenario{
		Name: "combio-success",
		Desc: "A declares holding a single Ace (-1) and is strictly lower than everyone else.",
		Setup: engine.Scenario{
			Players:    []engine.PlayerID{"A", "B"},
			Hands:      map[engine.PlayerID][]string{"A": {"AS"}, "B": {"KC"}},
			Deck:       []string{"2C", "3D"},
			DiscardTop: "9H",
			Unlocked:   true,
		},
		Steps: steps(
			[]Step{{Note: "A declares before drawing", Action: engine.DeclareCombio{Base: p("A")}}},
			plainTurn("B", "B takes their one final turn"),
		),
		Check: wantWinners("A"),
	}
}

func combioFailure() Scenario {
	return Scenario{
		Name: "combio-failure",
		Desc: "A declares on a King. The declaration fails and A is out of contention entirely.",
		Setup: engine.Scenario{
			Players:    []engine.PlayerID{"A", "B"},
			Hands:      map[engine.PlayerID][]string{"A": {"KC"}, "B": {"AS"}},
			Deck:       []string{"2C", "3D"},
			DiscardTop: "9H",
			Unlocked:   true,
		},
		Steps: steps(
			[]Step{{Note: "A gambles and declares", Action: engine.DeclareCombio{Base: p("A")}}},
			plainTurn("B", "B's final turn"),
		),
		Check: wantWinners("B"),
	}
}

func declarerTie() Scenario {
	return Scenario{
		Name: "declarer-tie",
		Desc: "A ties with B on 5. A tie is not strictly lower, so the declaration fails and B wins.",
		Setup: engine.Scenario{
			Players:    []engine.PlayerID{"A", "B"},
			Hands:      map[engine.PlayerID][]string{"A": {"5C"}, "B": {"5D"}},
			Deck:       []string{"2C", "3D"},
			DiscardTop: "9H",
			Unlocked:   true,
		},
		Steps: steps(
			[]Step{{Action: engine.DeclareCombio{Base: p("A")}}},
			plainTurn("B", "B's final turn"),
		),
		Check: wantWinners("B"),
	}
}

func finalTurnTargeting() Scenario {
	return Scenario{
		Name: "final-turn-targeting",
		Desc: "Declaring is not protection: B strips the declarer's 8 and hands back a King, turning a 7 into a 12.",
		Setup: engine.Scenario{
			Players:    []engine.PlayerID{"A", "B"},
			Hands:      map[engine.PlayerID][]string{"A": {"AS", "8H"}, "B": {"KC", "3D"}},
			Deck:       []string{"7C", "6C", "8D"},
			DiscardTop: "9H",
			Unlocked:   true,
		},
		Steps: []Step{
			{Note: "A declares on 7 (Ace + 8)", Action: engine.DeclareCombio{Base: p("A")}},
			{Note: "B's final turn: discard an 8 to open a rank-8 window",
				Action: engine.DrawCard{Base: p("B")}},
			{Action: engine.DiscardDrawn{Base: p("B")}},
			{Note: "B knocks out the declarer's 8 and gives them the King instead",
				Action: engine.AttemptOpponentMatch{Base: p("B"), Target: "A", TargetIndex: 1, GiveIndex: 0}},
			{Action: engine.TimerExpired{Kind: engine.TimerMatchWindow}},
		},
		Check: func(s *engine.State) error {
			if s.Scores["A"] != 12 || s.Scores["B"] != 3 {
				return fmt.Errorf("scores %v, want A=12 B=3", s.Scores)
			}
			return wantWinners("B")(s)
		},
	}
}

func disconnectRemoval() Scenario {
	return Scenario{
		Name: "disconnect",
		Desc: "Disconnect pauses everything; removal sinks the player's cards to the BOTTOM of the pile, unrevealed and undestroyed.",
		Setup: engine.Scenario{
			Players:    []engine.PlayerID{"A", "B", "C"},
			Hands:      map[engine.PlayerID][]string{"A": {"2C", "3D"}, "B": {"KS", "4H"}, "C": {"9D"}},
			Deck:       []string{"5S", "6S", "8H"},
			DiscardTop: "TH",
			Unlocked:   true,
		},
		Steps: []Step{
			{Note: "A opens a rank-8 window", Action: engine.DrawCard{Base: p("A")}},
			{Action: engine.DiscardDrawn{Base: p("A")}},
			{Note: "B drops out mid-window: the whole game freezes",
				Action: engine.PlayerDisconnected{Base: p("B")}},
			{Note: "nobody can act while paused, not even the timer",
				Action: engine.TimerExpired{Kind: engine.TimerMatchWindow}, WantErr: engine.ErrPaused},
			{Note: "B does not come back and is removed after 30s",
				Action: engine.RemovePlayer{Base: p("B")}},
			{Note: "the window resumes on the same rank; B's cards are now stale targets"},
			{Action: engine.AttemptOpponentMatch{Base: p("C"), Target: "B", TargetIndex: 0, GiveIndex: 0},
				WantErr: engine.ErrStale},
			{Action: engine.TimerExpired{Kind: engine.TimerMatchWindow}},
		},
		Check: func(s *engine.State) error {
			if !s.Player("B").Removed {
				return fmt.Errorf("B should be removed")
			}
			if len(s.Discard) < 2 {
				return fmt.Errorf("B's cards should be at the bottom of the pile")
			}
			return nil
		},
	}
}

func reshuffle() Scenario {
	return Scenario{
		Name: "reshuffle",
		Desc: "An empty draw deck is refilled from the discard pile, keeping the active top card in place.",
		Setup: engine.Scenario{
			Players:    []engine.PlayerID{"A", "B"},
			Hands:      map[engine.PlayerID][]string{"A": {"2C", "3D"}, "B": {"KS", "4H"}},
			Deck:       []string{"8H"},
			DiscardTop: "9C",
			Unlocked:   true,
		},
		Steps: []Step{
			{Note: "A draws the last deck card and discards it", Action: engine.DrawCard{Base: p("A")}},
			{Action: engine.DiscardDrawn{Base: p("A")}},
			{Note: "B throws the King at rank 8 — wrong, so the penalty draw must reshuffle"},
			{Action: engine.AttemptOwnMatch{Base: p("B"), Index: 0}},
			{Action: engine.TimerExpired{Kind: engine.TimerMatchWindow}},
		},
		Check: func(s *engine.State) error {
			top, _ := s.Card(s.DiscardTop())
			if top.Rank != engine.Eight {
				return fmt.Errorf("the active card should survive the reshuffle, got %s", top.Rank)
			}
			return nil
		},
	}
}

func unlockInterrupted() Scenario {
	return Scenario{
		Name: "unlock-interrupted",
		Desc: "Round 1 completes; A and B play round 2; C and D are removed. Round 2 was never finished, so COMBIO stays locked.",
		Setup: engine.Scenario{
			Players: []engine.PlayerID{"A", "B", "C", "D"},
			Hands: map[engine.PlayerID][]string{
				"A": {"2S", "3S"}, "B": {"4D", "5D"}, "C": {"6C", "7C"}, "D": {"8H", "9H"},
			},
			Deck: []string{
				"2D", "3H", "4S", "5C", "6D", "7S", "8C", "9S", "2C", "3C", "4C", "5S",
			},
			DiscardTop: "5H",
		},
		Steps: steps(
			plainTurn("A", "round 1"), plainTurn("B", ""), plainTurn("C", ""), plainTurn("D", ""),
			[]Step{{Note: "round 1 is complete — one down, one to go"}},
			plainTurn("A", "round 2 begins"), plainTurn("B", ""),
			[]Step{
				{Note: "C and D vanish before their round-2 turns"},
				{Action: engine.PlayerDisconnected{Base: p("C")}},
				{Action: engine.RemovePlayer{Base: p("C")}},
				{Action: engine.PlayerDisconnected{Base: p("D")}},
				{Action: engine.RemovePlayer{Base: p("D")}},
				{Note: "A and B have two turns each, but only ONE round ever completed"},
				{Action: engine.DeclareCombio{Base: p("A")}, WantErr: engine.ErrDeclareLocked},
			},
			plainTurn("A", "the broken round restarts with just A and B"),
			[]Step{{Note: "still shut until B has played it too"},
				{Action: engine.DeclareCombio{Base: p("B")}, WantErr: engine.ErrDeclareLocked}},
			plainTurn("B", ""),
			[]Step{{Note: "now two rounds have genuinely been completed"}},
		),
		Check: func(s *engine.State) error {
			if !s.DeclareUnlocked() {
				return fmt.Errorf("COMBIO should be unlocked after the restarted round")
			}
			if s.RoundsCompleted != 2 {
				return fmt.Errorf("RoundsCompleted=%d, want 2", s.RoundsCompleted)
			}
			return nil
		},
	}
}
