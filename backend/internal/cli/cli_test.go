package cli

import (
	"regexp"
	"strings"
	"testing"

	"github.com/kaushlendra/combio/backend/internal/engine"
)

func TestReplayFinishesAtEveryTableSize(t *testing.T) {
	for players := 2; players <= 8; players++ {
		for _, cards := range []int{4, 6, 8} {
			out, err := Replay(int64(players*10+cards), players, cards, false)
			if err != nil {
				t.Fatalf("%d players %d cards: %v", players, cards, err)
			}
			if !strings.Contains(out, "GAME OVER") {
				t.Fatalf("%d players %d cards: no result screen", players, cards)
			}
		}
	}
}

func TestReplayIsDeterministic(t *testing.T) {
	a, err := Replay(4242, 4, 6, false)
	if err != nil {
		t.Fatal(err)
	}
	b, err := Replay(4242, 4, 6, false)
	if err != nil {
		t.Fatal(err)
	}
	if a != b {
		t.Fatal("the same seed must produce the same game")
	}
	c, _ := Replay(4243, 4, 6, false)
	if a == c {
		t.Fatal("different seeds should produce different games")
	}
}

// A bot may only remember cards it was personally shown. This drives a whole
// game while recording, per player, every card id that appeared in an event
// addressed to them, then checks each bot's memory against that ledger.
func TestBotsCanOnlyRememberWhatTheyWereShown(t *testing.T) {
	s, _, err := newReplaySession(31337, 4, 6, false)
	if err != nil {
		t.Fatal(err)
	}
	allowed := map[engine.PlayerID]map[engine.CardID]bool{}
	for _, p := range s.players {
		allowed[p.ID] = map[engine.CardID]bool{}
	}
	s.onEvents = func(evs []engine.Event) {
		for _, p := range s.players {
			for _, e := range engine.FilterEvents(evs, p.ID) {
				if e.Audience() == nil {
					continue // public events carry no hidden identity
				}
				switch ev := e.(type) {
				case engine.EvMemoryRevealed:
					for _, c := range ev.Cards {
						allowed[p.ID][c.ID] = true
					}
				case engine.EvPrivateDraw:
					allowed[p.ID][ev.Card.ID] = true
				case engine.EvPrivateReveal:
					allowed[p.ID][ev.Card.ID] = true
				}
			}
		}
	}
	if err := s.runHeadless(); err != nil {
		t.Fatal(err)
	}

	total := 0
	for id, brain := range s.bots {
		if len(brain.seen) == 0 {
			t.Fatalf("%s remembered nothing at all — the test is not exercising memory", id)
		}
		for cardID := range brain.seen {
			if !allowed[id][cardID] {
				t.Fatalf("%s remembers card %d, which was never revealed to them", id, cardID)
			}
			total++
		}
	}
	if total < 10 {
		t.Fatalf("only %d remembered cards across the table; too weak a check", total)
	}

	// And the brains really are separate objects.
	if len(s.bots) != 4 {
		t.Fatalf("expected one brain per bot, got %d", len(s.bots))
	}
}

// fullCard matches a rendered identity such as 8D(R) or 10H(B).
var fullCard = regexp.MustCompile(`(10|[2-9AJQK])[CDHS]\((R|B)\)`)

func TestBoardShowsNoRankExceptTheMiddleCard(t *testing.T) {
	st, err := engine.BuildScenario(engine.Scenario{
		Players: []engine.PlayerID{"A", "B", "C"},
		Hands: map[engine.PlayerID][]string{
			"A": {"2C", "3D"}, "B": {"KS", "4H"}, "C": {"9D", "6S"},
		},
		Deck:       []string{"JC"},
		DiscardTop: "2H",
		Unlocked:   true,
	})
	if err != nil {
		t.Fatal(err)
	}
	names := nameBook{"A": "Ann", "B": "Bo", "C": "Cy"}

	// Public board: only the middle card may show an identity.
	out := Board(st.ViewFor("B"), names, false)
	if got := fullCard.FindAllString(out, -1); len(got) != 1 || got[0] != "2H(R)" {
		t.Fatalf("public board exposed %v, want only the middle card", got)
	}

	// After A draws, B's private board still shows only the middle card.
	st, _, err = engine.Apply(st, engine.DrawCard{Base: engine.For("A")})
	if err != nil {
		t.Fatal(err)
	}
	out = Board(st.ViewFor("B"), names, true)
	if got := fullCard.FindAllString(out, -1); len(got) != 1 {
		t.Fatalf("B's board exposed %v after A drew", got)
	}
	// A's own board shows the middle card and the card A is holding.
	out = Board(st.ViewFor("A"), names, true)
	got := fullCard.FindAllString(out, -1)
	if len(got) != 2 {
		t.Fatalf("A's board showed %v, want the middle card and their own draw", got)
	}
	if !strings.Contains(out, "YOU DREW") {
		t.Fatal("A should be told what they drew")
	}
	// Face-down cards are rendered as position + deck colour only.
	if !strings.Contains(out, "0:[R]") {
		t.Fatalf("expected position/colour handles, got:\n%s", out)
	}
}

func TestDescribeKeepsPrivateEventsOutOfThePublicFeed(t *testing.T) {
	st, err := engine.BuildScenario(engine.Scenario{
		Players:    []engine.PlayerID{"A", "B"},
		Hands:      map[engine.PlayerID][]string{"A": {"2C"}, "B": {"KS"}},
		Deck:       []string{"JC"},
		DiscardTop: "2H",
	})
	if err != nil {
		t.Fatal(err)
	}
	_, evs, err := engine.Apply(st, engine.DrawCard{Base: engine.For("A")})
	if err != nil {
		t.Fatal(err)
	}
	s := &Session{names: nameBook{"A": "Ann", "B": "Bo"}}
	s.record(evs)
	for _, line := range s.feed {
		if strings.Contains(line, "you drew") || fullCard.MatchString(line) {
			t.Fatalf("the public feed leaked a private draw: %q", line)
		}
	}
	if len(s.feed) == 0 {
		t.Fatal("the public feed should still say that a draw happened")
	}
}

func TestDebugDumpIsOptInAndComplete(t *testing.T) {
	st, err := engine.BuildScenario(engine.Scenario{
		Players:    []engine.PlayerID{"A", "B"},
		Hands:      map[engine.PlayerID][]string{"A": {"2C", "3D"}, "B": {"KS"}},
		Deck:       []string{"JC"},
		DiscardTop: "2H",
	})
	if err != nil {
		t.Fatal(err)
	}
	out := DebugDump(&st)
	for _, want := range []string{
		"DEBUG MODE", "rounds completed", "invariants: ok", "2C", "KS",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("debug dump missing %q", want)
		}
	}
}
