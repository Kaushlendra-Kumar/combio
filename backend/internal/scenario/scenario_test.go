package scenario

import (
	"io"
	"strings"
	"testing"
)

// Every scenario must run clean: no rejected action other than the ones it
// explicitly expects, invariants intact at every step, and its own Check
// satisfied.
func TestAllScenariosPass(t *testing.T) {
	all := All()
	if len(all) < 16 {
		t.Fatalf("expected the full library, got %d scenarios", len(all))
	}
	seen := map[string]bool{}
	for _, sc := range all {
		if seen[sc.Name] {
			t.Fatalf("duplicate scenario name %q", sc.Name)
		}
		seen[sc.Name] = true
		if sc.Desc == "" {
			t.Fatalf("%s has no description", sc.Name)
		}
		t.Run(sc.Name, func(t *testing.T) {
			if err := Run(io.Discard, sc); err != nil {
				t.Fatal(err)
			}
		})
	}
	for _, want := range []string{
		"matching-race", "wrong-match", "multi-match", "opponent-transfer",
		"power-10", "power-jack", "power-queen", "power-king", "zero-card",
		"combio-success", "combio-failure", "declarer-tie", "final-turn-targeting",
		"disconnect", "reshuffle", "unlock-interrupted",
	} {
		if !seen[want] {
			t.Errorf("missing required scenario %q", want)
		}
	}
}

func TestFindAndNames(t *testing.T) {
	if _, ok := Find("power-king"); !ok {
		t.Fatal("Find should locate a known scenario")
	}
	if _, ok := Find("nope"); ok {
		t.Fatal("Find should reject an unknown name")
	}
	if len(Names()) != len(All()) {
		t.Fatal("Names and All disagree")
	}
}

// Scenario mode is a scripted development transcript: it deliberately shows
// private reveals, labelled with their recipient. It must NOT quietly turn into
// a debug dump — no card ids, no layout versions, no deck order, no banner.
func TestScenarioOutputIsNotADebugDump(t *testing.T) {
	for _, sc := range All() {
		var buf strings.Builder
		if err := Run(&buf, sc); err != nil {
			t.Fatalf("%s: %v", sc.Name, err)
		}
		out := buf.String()
		for _, forbidden := range []string{
			"DEBUG MODE", ")#", "layout:", "invariants:", "v=", "RoundPending",
		} {
			if strings.Contains(out, forbidden) {
				t.Errorf("%s: transcript contains debug-only output %q", sc.Name, forbidden)
			}
		}
		if !strings.Contains(out, "scripted transcript for development") {
			t.Errorf("%s: transcript should say what it is", sc.Name)
		}
		// Private event lines must always name who they were for. Only the
		// indented event lines count; prose descriptions are not events.
		for _, line := range strings.Split(out, "\n") {
			if !strings.HasPrefix(line, "        ") {
				continue
			}
			if strings.Contains(line, "you drew") || strings.Contains(line, "you see") ||
				strings.Contains(line, "you memorise") {
				if !strings.Contains(line, "[private -> ") {
					t.Errorf("%s: unattributed private line %q", sc.Name, line)
				}
			}
		}
	}
}
