package cli

import (
	"bufio"
	"bytes"
	"math/rand"
	"strings"
	"testing"

	"github.com/kaushlendra/combio/backend/internal/engine"
)

// clearMarker is exactly what IO.ClearScreen writes when clearing is on. Tests
// split captured output on it to reason about what each player could see.
const clearMarker = "\033[3J\033[H\033[2J"

func testIO(script string, clear bool) (*IO, *bytes.Buffer) {
	buf := &bytes.Buffer{}
	return &IO{
		in:    bufio.NewReader(strings.NewReader(script)),
		out:   buf,
		clear: clear,
		echo:  false,
	}, buf
}

// newTestSession wires a Session around a given state with scripted input.
// Names are assigned in seat order.
func newTestSession(st engine.State, script string, clear bool, names ...string) (*Session, *bytes.Buffer) {
	t, buf := testIO(script, clear)
	ps := make([]Player, 0, len(st.Players))
	nb := nameBook{}
	for i := range st.Players {
		name := string(st.Players[i].ID)
		if i < len(names) {
			name = names[i]
		}
		ps = append(ps, Player{ID: st.Players[i].ID, Name: name})
		nb[st.Players[i].ID] = name
	}
	return &Session{
		st:      st,
		players: ps,
		names:   nb,
		io:      t,
		rng:     rand.New(rand.NewSource(1)),
	}, buf
}

// script joins lines the way a piped stdin would deliver them.
func script(lines ...string) string { return strings.Join(lines, "\n") + "\n" }

// afterHandOverTo returns everything the terminal showed from the moment the
// device was handed to this player onwards. Because ClearScreen wipes
// scrollback, this is genuinely all that player could see.
func afterHandOverTo(t *testing.T, out string, name string) string {
	t.Helper()
	segs := strings.Split(out, clearMarker)
	for i, seg := range segs {
		if strings.Contains(seg, "Pass the device to: "+name) {
			return strings.Join(segs[i:], "\n")
		}
	}
	t.Fatalf("the device was never handed to %s; output was:\n%s", name, out)
	return ""
}

func mustNotContain(t *testing.T, where, haystack string, needles ...string) {
	t.Helper()
	for _, n := range needles {
		if n == "" {
			continue
		}
		if strings.Contains(haystack, n) {
			t.Fatalf("%s leaked %q", where, n)
		}
	}
}

func mustContain(t *testing.T, where, haystack string, needles ...string) {
	t.Helper()
	for _, n := range needles {
		if !strings.Contains(haystack, n) {
			t.Fatalf("%s should contain %q but does not:\n%s", where, n, haystack)
		}
	}
}

// cardAt reads a real card out of the state. Only test code may do this.
func cardAt(st *engine.State, owner engine.PlayerID, pos int) engine.Card {
	id, _ := st.Player(owner).Layout.At(pos)
	return st.MustCard(id)
}

func topOfDeck(st *engine.State) engine.Card {
	return st.MustCard(st.Deck[len(st.Deck)-1])
}
