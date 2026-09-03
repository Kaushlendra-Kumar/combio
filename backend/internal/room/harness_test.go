package room

import (
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kaushlendra/combio/backend/internal/engine"
	"github.com/kaushlendra/combio/backend/internal/protocol"
)

// fakeClient records everything the room sends it. Send must never block, and
// this one never does.
type fakeClient struct {
	id     engine.PlayerID
	name   string
	mu     sync.Mutex
	msgs   []protocol.ServerMessage
	closed bool
}

func newClient(id, name string) *fakeClient {
	return &fakeClient{id: engine.PlayerID(id), name: name}
}

func (c *fakeClient) ID() engine.PlayerID { return c.id }
func (c *fakeClient) Name() string        { return c.name }

func (c *fakeClient) Send(m protocol.ServerMessage) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.msgs = append(c.msgs, m)
}

func (c *fakeClient) Close() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.closed = true
}

func (c *fakeClient) all() []protocol.ServerMessage {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]protocol.ServerMessage(nil), c.msgs...)
}

func (c *fakeClient) reset() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.msgs = nil
}

func (c *fakeClient) ofType(typ string) []protocol.ServerMessage {
	out := []protocol.ServerMessage{}
	for _, m := range c.all() {
		if m.Type == typ {
			out = append(out, m)
		}
	}
	return out
}

func (c *fakeClient) last(typ string) (protocol.ServerMessage, bool) {
	all := c.ofType(typ)
	if len(all) == 0 {
		return protocol.ServerMessage{}, false
	}
	return all[len(all)-1], true
}

// view returns the newest per-player view this client was sent.
func (c *fakeClient) view(t *testing.T) engine.PlayerView {
	t.Helper()
	m, ok := c.last(protocol.SStateUpdate)
	if !ok {
		t.Fatalf("%s never received a state update", c.id)
	}
	d, ok := m.Data.(protocol.StateUpdateData)
	if !ok {
		t.Fatalf("state update payload was %T", m.Data)
	}
	return d.View
}

func (c *fakeClient) errors() []protocol.ErrorData {
	out := []protocol.ErrorData{}
	for _, m := range c.ofType(protocol.SError) {
		if d, ok := m.Data.(protocol.ErrorData); ok {
			out = append(out, d)
		}
	}
	return out
}

func (c *fakeClient) lastErrorCode() string {
	e := c.errors()
	if len(e) == 0 {
		return ""
	}
	return e[len(e)-1].Code
}

// eventNames lists every event this client received, in order.
func (c *fakeClient) eventNames() []string {
	out := []string{}
	for _, m := range c.ofType(protocol.SEvents) {
		d, ok := m.Data.(protocol.EventsData)
		if !ok {
			continue
		}
		for _, e := range d.Events {
			out = append(out, e.Name)
		}
	}
	return out
}

func (c *fakeClient) sawEvent(name string) bool {
	for _, n := range c.eventNames() {
		if n == name {
			return true
		}
	}
	return false
}

// json renders everything this client was sent, for leak scanning.
func (c *fakeClient) json(t *testing.T) string {
	t.Helper()
	var b strings.Builder
	for _, m := range c.all() {
		raw, err := json.Marshal(m)
		if err != nil {
			t.Fatal(err)
		}
		b.Write(raw)
		b.WriteByte('\n')
	}
	return b.String()
}

// ---------- room setup ----------

func testTimers() Timers {
	return Timers{
		Memory:         60 * time.Second,
		Turn:           30 * time.Second,
		Match:          5 * time.Second,
		MatchExtension: 2 * time.Second,
		Power:          15 * time.Second,
		Reconnect:      30 * time.Second,
	}
}

type table struct {
	room    *Room
	clock   *FakeClock
	clients []*fakeClient
}

func (tb *table) client(id string) *fakeClient {
	for _, c := range tb.clients {
		if string(c.id) == id {
			return c
		}
	}
	return nil
}

// reattach swaps in a fresh client for a seat, the way a reconnecting browser
// replaces the old socket.
func (tb *table) reattach(id string, name string) *fakeClient {
	c := newClient(id, name)
	for i, old := range tb.clients {
		if old.id == c.id {
			tb.clients[i] = c
			break
		}
	}
	tb.room.Join(c, "j")
	tb.settle()
	return c
}

func (tb *table) resetAll() {
	for _, c := range tb.clients {
		c.reset()
	}
}

// settle blocks until the room loop has drained everything queued so far.
func (tb *table) settle() { tb.room.sync(func() {}) }

// advance moves the fake clock and lets the room process what fired.
func (tb *table) advance(d time.Duration) {
	tb.clock.Advance(d)
	tb.settle()
}

func (tb *table) act(id string, kind string, args any) {
	raw := json.RawMessage(nil)
	if args != nil {
		b, err := json.Marshal(args)
		if err != nil {
			panic(err)
		}
		raw = b
	}
	tb.room.Act(engine.PlayerID(id), protocol.ActionData{Kind: kind, Args: raw}, "c1")
	tb.settle()
}

// newTable opens a room with n joined, ready players and a fake clock.
func newTable(t *testing.T, n int, cards int, seed int64) *table {
	t.Helper()
	clock := NewFakeClock()
	r := New(Config{
		Code: "TEST", Public: true, CardsPerPlayer: cards, MaxPlayers: 8,
		Seed: seed, Timers: testTimers(), Clock: clock,
	})
	t.Cleanup(r.Close)

	tb := &table{room: r, clock: clock}
	names := []string{"Ann", "Bo", "Cy", "Di", "Ed", "Fi", "Gus", "Hal"}
	for i := 0; i < n; i++ {
		c := newClient(playerID(i), names[i])
		tb.clients = append(tb.clients, c)
		r.Join(c, "join")
	}
	tb.settle()
	return tb
}

func playerID(i int) string { return string(rune('a'+i)) + "1" }

func (tb *table) readyAll() {
	for _, c := range tb.clients {
		tb.room.SetReady(c.id, true, "r")
	}
	tb.settle()
}

// startGame joins, readies and starts, leaving the room in the memory phase.
func (tb *table) startGame(t *testing.T) {
	t.Helper()
	tb.readyAll()
	tb.room.Start(tb.clients[0].id, "s")
	tb.settle()
	if got := tb.room.Snapshot().Status; got != StatusStarting {
		t.Fatalf("status after start is %s, want STARTING", got)
	}
}

// finishMemory inspects for everyone so play can begin.
func (tb *table) finishMemory(t *testing.T) {
	t.Helper()
	half := tb.room.Snapshot().CardsPerPlayer / 2
	pos := make([]int, 0, half)
	for i := 0; i < half; i++ {
		pos = append(pos, i)
	}
	for _, c := range tb.clients {
		tb.act(string(c.id), protocol.AInspectMemory, map[string]any{"positions": pos})
	}
	if got := tb.room.Snapshot().Phase; got != "turn_start" {
		t.Fatalf("phase after the memory phase is %s", got)
	}
}

// inject replaces the room's authoritative state with a scripted one so a test
// can pin exactly which cards are where. Test-only: it reaches inside the loop
// through the same sync path everything else uses.
func (tb *table) inject(t *testing.T, sc engine.Scenario) {
	t.Helper()
	st, err := engine.BuildScenario(sc)
	if err != nil {
		t.Fatal(err)
	}
	tb.room.sync(func() {
		tb.room.st = st
		tb.room.refreshStatus()
		tb.room.syncTimers()
	})
}

// stateOf inspects the authoritative state.
//
// It works on a snapshot so assertions run on the TEST goroutine. Running them
// inside the room loop would mean a failing t.Fatal killed the loop and every
// later sync would block forever, turning a plain test failure into a deadlock.
func (tb *table) stateOf(fn func(*engine.State)) {
	st := tb.room.CloneState()
	fn(&st)
}

func (tb *table) currentPlayer() engine.PlayerID {
	var id engine.PlayerID
	tb.stateOf(func(s *engine.State) { id = s.CurrentID() })
	return id
}

func (tb *table) phase() string { return tb.room.Snapshot().Phase }
