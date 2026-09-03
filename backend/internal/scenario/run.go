package scenario

import (
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/kaushlendra/combio/backend/internal/cli"
	"github.com/kaushlendra/combio/backend/internal/engine"
)

// Step is one scripted action.
type Step struct {
	Note    string        // printed before the action, explains what is being shown
	Action  engine.Action // nil means "note only"
	WantErr error         // when set, the action is EXPECTED to be rejected with this
}

// Scenario is a named, fully deterministic game fragment.
type Scenario struct {
	Name  string
	Desc  string
	Setup engine.Scenario
	Steps []Step
	// Check runs at the end and returns an error if the outcome is wrong.
	Check func(s *engine.State) error
}

// Run plays the scenario and writes a transcript.
//
// Unlike normal play this prints private reveals too, labelled with their
// recipient. A scenario is a development transcript, not a game being played
// on a shared screen.
func Run(w io.Writer, sc Scenario) error {
	names := map[engine.PlayerID]string{}
	for _, p := range sc.Setup.Players {
		names[p] = string(p)
	}

	st, err := engine.BuildScenario(sc.Setup)
	if err != nil {
		return fmt.Errorf("%s: setup: %w", sc.Name, err)
	}

	fmt.Fprintf(w, "\n══ scenario: %s ══\n%s\n", sc.Name, sc.Desc)
	fmt.Fprintf(w, "(scripted transcript for development — it shows private reveals,\n"+
		" attributed to their recipient. It is not a game being played on a shared screen.)\n\n")
	fmt.Fprintf(w, "  opening hands:\n")
	ids := append([]engine.PlayerID(nil), sc.Setup.Players...)
	for _, p := range ids {
		fmt.Fprintf(w, "    %-4s %s\n", p, strings.Join(st.Hand(p), " "))
	}
	if len(sc.Setup.Deck) > 0 {
		fmt.Fprintf(w, "    deck (top first): %s\n", strings.Join(reversed(sc.Setup.Deck), " "))
	}
	fmt.Fprintf(w, "    middle: %s\n\n", sc.Setup.DiscardTop)

	for i, step := range sc.Steps {
		if step.Note != "" {
			fmt.Fprintf(w, "  · %s\n", step.Note)
		}
		if step.Action == nil {
			continue
		}
		next, evs, err := engine.Apply(st, step.Action)
		if step.WantErr != nil {
			if err == nil {
				return fmt.Errorf("%s step %d (%s): expected rejection %v, got none",
					sc.Name, i, step.Action.Name(), step.WantErr)
			}
			if !errorsIs(err, step.WantErr) {
				return fmt.Errorf("%s step %d (%s): expected %v, got %v",
					sc.Name, i, step.Action.Name(), step.WantErr, err)
			}
			fmt.Fprintf(w, "      %s -> rejected: %v  (no state change, no penalty)\n",
				step.Action.Name(), err)
			continue
		}
		if err != nil {
			return fmt.Errorf("%s step %d (%s): %w", sc.Name, i, step.Action.Name(), err)
		}
		st = next
		fmt.Fprintf(w, "      %s\n", step.Action.Name())
		for _, e := range evs {
			line := cli.Describe(e, names)
			if line == "" {
				continue
			}
			if aud := e.Audience(); aud != nil {
				fmt.Fprintf(w, "        [private -> %s] %s\n", aud[0], line)
			} else {
				fmt.Fprintf(w, "        %s\n", line)
			}
		}
		if err := engine.CheckInvariants(&st); err != nil {
			return fmt.Errorf("%s step %d: invariant broken: %w", sc.Name, i, err)
		}
	}

	fmt.Fprintf(w, "\n  final hands:\n")
	for _, p := range ids {
		hand := st.Hand(p)
		note := ""
		if pl := st.Player(p); pl != nil && !pl.Active() {
			note = "  (removed)"
		}
		fmt.Fprintf(w, "    %-4s %-28s score %4d%s\n", p, strings.Join(hand, " "), st.Score(p), note)
	}
	if st.Phase == engine.PhaseFinished {
		ws := make([]string, 0, len(st.Winners))
		for _, w2 := range st.Winners {
			ws = append(ws, string(w2))
		}
		sort.Strings(ws)
		fmt.Fprintf(w, "  winner(s): %s   scores: %v\n", strings.Join(ws, ", "), st.Scores)
	}

	if sc.Check != nil {
		if err := sc.Check(&st); err != nil {
			return fmt.Errorf("%s: %w", sc.Name, err)
		}
	}
	fmt.Fprintf(w, "  ✓ %s passed\n", sc.Name)
	return nil
}

func reversed(in []string) []string {
	out := make([]string, 0, len(in))
	for i := len(in) - 1; i >= 0; i-- {
		out = append(out, in[i])
	}
	return out
}

func errorsIs(err, target error) bool {
	for err != nil {
		if err == target {
			return true
		}
		u, ok := err.(interface{ Unwrap() error })
		if !ok {
			return false
		}
		err = u.Unwrap()
	}
	return false
}
