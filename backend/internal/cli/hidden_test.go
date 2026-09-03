package cli

import (
	"strings"
	"testing"

	"github.com/kaushlendra/combio/backend/internal/engine"
)

// The complete hand-over, driven through the real session code:
// Ann memorises two cards, the screen clears, Bo takes over. Nothing Ann saw
// may appear anywhere from Bo's hand-over onwards.
func TestPassTheDeviceMemoryPhaseLeaksNothing(t *testing.T) {
	st, _, err := engine.NewGame(engine.Config{CardsPerPlayer: 4},
		[]engine.PlayerID{"p1", "p2"}, engine.NewSeededShuffler(42))
	if err != nil {
		t.Fatal(err)
	}
	annSecrets := []string{
		CardText(cardAt(&st, "p1", 0)),
		CardText(cardAt(&st, "p1", 1)),
	}
	boSecrets := []string{
		CardText(cardAt(&st, "p2", 0)),
		CardText(cardAt(&st, "p2", 1)),
	}

	s, buf := newTestSession(st, script(
		"", "0 1", "", "", // Ann: hand over, choose, memorise, hide
		"", "0 1", "", "", // Bo: the same
	), true, "Ann", "Bo")
	s.memoryPhase()

	out := buf.String()
	annScreen := afterHandOverTo(t, out, "Ann")
	boScreen := afterHandOverTo(t, out, "Bo")

	mustContain(t, "Ann's own screen", annScreen, annSecrets...)
	mustNotContain(t, "everything from Bo's hand-over onwards", boScreen, annSecrets...)
	mustContain(t, "Bo's own screen", boScreen, boSecrets...)

	// The screen really was wiped between them, scrollback included.
	if strings.Count(out, clearMarker) < 4 {
		t.Fatalf("expected the screen to be cleared around each hand-over, saw %d clears",
			strings.Count(out, clearMarker))
	}
	// And the public feed never carried any of it.
	for _, line := range s.feed {
		mustNotContain(t, "the public feed", line, append(annSecrets, boSecrets...)...)
	}
}

// A kept drawn card stays secret forever: the player knows it, nobody else can
// see it on any later screen.
func TestPassTheDevicePrivateDrawLeaksNothing(t *testing.T) {
	st, _, err := engine.NewGame(engine.Config{CardsPerPlayer: 4},
		[]engine.PlayerID{"p1", "p2"}, engine.NewSeededShuffler(42))
	if err != nil {
		t.Fatal(err)
	}
	st, _, err = engine.Apply(st, engine.InspectMemory{Base: engine.For("p1"), Positions: []int{0, 1}})
	if err != nil {
		t.Fatal(err)
	}
	st, _, err = engine.Apply(st, engine.InspectMemory{Base: engine.For("p2"), Positions: []int{0, 1}})
	if err != nil {
		t.Fatal(err)
	}
	drawn := CardText(topOfDeck(&st))

	// Ann draws and KEEPS it, so it never becomes public through the pile.
	s, buf := newTestSession(st, script(
		"", "d", "", "k", "0", "0", "", // Ann's turn
		"", "", "n", "", "", // window: Bo passes, then Ann, then close
	), true, "Ann", "Bo")
	s.turn()
	if s.phase() == "matching" {
		s.matchingWindow()
	}

	out := buf.String()
	mustContain(t, "Ann's screen", afterHandOverTo(t, out, "Ann"), drawn)
	mustNotContain(t, "everything from Bo's hand-over onwards", afterHandOverTo(t, out, "Bo"), drawn)
	for _, line := range s.feed {
		mustNotContain(t, "the public feed", line, drawn)
	}
	// Ann really did keep it.
	if !s.st.Player("p1").Layout.Contains(topOfDeckID(st)) {
		t.Fatal("Ann should be holding the card she kept")
	}
}

func topOfDeckID(st engine.State) engine.CardID { return st.Deck[len(st.Deck)-1] }

// The three peeking powers, each driven through the real private prompt.
func TestPassTheDevicePowerRevealsLeakNothing(t *testing.T) {
	cases := []struct {
		name   string
		drawn  string
		script []string
		// secret returns the card the power reveals, read from the state
		// before the power resolves.
		secret func(st *engine.State) string
	}{
		{
			name:   "10 peek at own card",
			drawn:  "TD",
			script: []string{"", "1", "", ""}, // hand over, position 1, memorise, hide
			secret: func(st *engine.State) string { return CardText(cardAt(st, "p1", 1)) },
		},
		{
			name:   "Jack peek at an opponent",
			drawn:  "JC",
			script: []string{"", "0", "0", "", ""}, // hand over, target Bo, position 0, memorise, hide
			secret: func(st *engine.State) string { return CardText(cardAt(st, "p2", 0)) },
		},
		{
			name:   "King peek then cancel",
			drawn:  "KD",
			script: []string{"", "0", "1", "", "n", ""}, // ... position 1, memorise, do not swap, hide
			secret: func(st *engine.State) string { return CardText(cardAt(st, "p2", 1)) },
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			st, err := engine.BuildScenario(engine.Scenario{
				Players: []engine.PlayerID{"p1", "p2", "p3"},
				Hands: map[engine.PlayerID][]string{
					"p1": {"2C", "3D"}, "p2": {"KS", "4H"}, "p3": {"9D", "6S"},
				},
				Deck:       []string{tc.drawn},
				DiscardTop: "2H",
				Unlocked:   true,
			})
			if err != nil {
				t.Fatal(err)
			}
			// Get to the power: draw, discard, close the window.
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
			secret := tc.secret(&st)

			s, buf := newTestSession(st, script(tc.script...), true, "Ann", "Bo", "Cy")
			s.resolvePower()

			out := buf.String()
			mustContain(t, "Ann's screen", afterHandOverTo(t, out, "Ann"), secret)

			// Now hand the device on and render the next player's world.
			s.handOver(s.players[1])
			s.privateBoard(s.players[1])
			out = buf.String()
			mustNotContain(t, "everything from Bo's hand-over onwards",
				afterHandOverTo(t, out, "Bo"), secret)

			// Nothing public ever mentioned it either.
			for _, line := range s.feed {
				mustNotContain(t, "the public feed", line, secret)
			}
			// And no other player's view or filtered events carry it.
			for _, other := range []engine.PlayerID{"p2", "p3"} {
				board := Board(s.st.ViewFor(other), s.names, true)
				mustNotContain(t, "the board of "+string(other), board, secret)
			}
		})
	}
}

// The same guarantee stated at the boundary rather than through the terminal:
// for every kind of private reveal, only the recipient's filtered events carry
// the identity, and no other player's view does.
func TestPrivateRevealsNeverReachAnotherPlayersViewOrEvents(t *testing.T) {
	build := func(deck string) engine.State {
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
		return st
	}
	names := nameBook{"p1": "Ann", "p2": "Bo", "p3": "Cy"}

	check := func(t *testing.T, st engine.State, evs []engine.Event, secret string) {
		t.Helper()
		var mine, theirs strings.Builder
		for _, e := range engine.FilterEvents(evs, "p1") {
			mine.WriteString(Describe(e, names) + "\n")
		}
		for _, who := range []engine.PlayerID{"p2", "p3"} {
			for _, e := range engine.FilterEvents(evs, who) {
				theirs.WriteString(Describe(e, names) + "\n")
			}
			theirs.WriteString(Board(st.ViewFor(who), names, true))
		}
		mustContain(t, "the recipient's events", mine.String(), secret)
		mustNotContain(t, "the other players' events and views", theirs.String(), secret)
	}

	t.Run("memory reveal", func(t *testing.T) {
		st, _, err := engine.NewGame(engine.Config{CardsPerPlayer: 4},
			[]engine.PlayerID{"p1", "p2", "p3"}, engine.NewSeededShuffler(9))
		if err != nil {
			t.Fatal(err)
		}
		secret := CardText(cardAt(&st, "p1", 0))
		next, evs, err := engine.Apply(st, engine.InspectMemory{Base: engine.For("p1"), Positions: []int{0, 1}})
		if err != nil {
			t.Fatal(err)
		}
		check(t, next, evs, secret)
	})

	t.Run("private draw", func(t *testing.T) {
		st := build("7H")
		secret := CardText(topOfDeck(&st))
		next, evs, err := engine.Apply(st, engine.DrawCard{Base: engine.For("p1")})
		if err != nil {
			t.Fatal(err)
		}
		check(t, next, evs, secret)
	})

	powers := []struct {
		name   string
		deck   string
		action func(st engine.State) engine.Action
		secret func(st *engine.State) string
	}{
		{"10 peek", "TD",
			func(engine.State) engine.Action { return engine.Peek10{Base: engine.For("p1"), Index: 1} },
			func(st *engine.State) string { return CardText(cardAt(st, "p1", 1)) }},
		{"jack peek", "JC",
			func(engine.State) engine.Action {
				return engine.JackPeek{Base: engine.For("p1"), Target: "p2", TargetIndex: 0}
			},
			func(st *engine.State) string { return CardText(cardAt(st, "p2", 0)) }},
		{"king peek", "KD",
			func(engine.State) engine.Action {
				return engine.KingPeek{Base: engine.For("p1"), Target: "p2", TargetIndex: 1}
			},
			func(st *engine.State) string { return CardText(cardAt(st, "p2", 1)) }},
	}
	for _, tc := range powers {
		t.Run(tc.name, func(t *testing.T) {
			st := build(tc.deck)
			var err error
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
			secret := tc.secret(&st)
			next, evs, err := engine.Apply(st, tc.action(st))
			if err != nil {
				t.Fatal(err)
			}
			check(t, next, evs, secret)
		})
	}
}
