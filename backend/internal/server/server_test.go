package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/kaushlendra/combio/backend/internal/auth"
	"github.com/kaushlendra/combio/backend/internal/engine"
	"github.com/kaushlendra/combio/backend/internal/protocol"
	"github.com/kaushlendra/combio/backend/internal/room"
)

type harness struct {
	srv    *httptest.Server
	server *Server
	dev    *auth.DevTokens
	rooms  *room.Manager
	clock  *room.FakeClock
}

func newDevAuth() *auth.DevTokens { return auth.NewDevTokens() }

// serve wraps an already-built Server in a test HTTP server.
func serve(t *testing.T, s *Server, dev *auth.DevTokens, rooms *room.Manager, clock *room.FakeClock) *harness {
	t.Helper()
	ts := httptest.NewServer(s.Handler())
	t.Cleanup(func() {
		ts.Close()
		rooms.CloseAll()
	})
	return &harness{srv: ts, server: s, dev: dev, rooms: rooms, clock: clock}
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	dev := auth.NewDevTokens()
	clock := room.NewFakeClock()
	rooms := room.NewManager(room.ManagerOptions{
		Clock: clock, Timers: room.DefaultTimers(), Seed: 7,
	})
	s := New(Options{Dev: dev, Rooms: rooms})
	return serve(t, s, dev, rooms, clock)
}

// token asks the dev endpoint for a token, exactly as a browser would.
func (h *harness) token(t *testing.T, name string) (string, engine.PlayerID) {
	t.Helper()
	res, err := http.Post(h.srv.URL+"/auth/dev?name="+name, "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("auth returned %d", res.StatusCode)
	}
	var body struct {
		Token    string          `json:"token"`
		PlayerID engine.PlayerID `json:"player_id"`
		Name     string          `json:"name"`
	}
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	return body.Token, body.PlayerID
}

func (h *harness) dial(t *testing.T, name string) *client {
	t.Helper()
	tok, id := h.token(t, name)
	return h.dialToken(t, tok, id, name)
}

func (h *harness) dialToken(t *testing.T, tok string, id engine.PlayerID, name string) *client {
	t.Helper()
	url := "ws" + strings.TrimPrefix(h.srv.URL, "http") + "/ws?token=" + tok
	c, _, err := websocket.DefaultDialer.Dial(url, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })
	cl := &client{t: t, c: c, id: id, name: name}
	go cl.pump()
	if m := cl.await(protocol.SWelcome); m.Type != protocol.SWelcome {
		t.Fatal("no welcome")
	}
	return cl
}

// client is a test browser. A background pump drains the socket into a buffer,
// because in gorilla a read deadline that fires poisons the connection for
// good: tests must never let one hit a socket they still want to use.
type client struct {
	t    *testing.T
	c    *websocket.Conn
	id   engine.PlayerID
	name string

	mu     sync.Mutex
	raw    []string
	cursor int
	dead   bool
}

func (c *client) pump() {
	for {
		_, data, err := c.c.ReadMessage()
		if err != nil {
			c.mu.Lock()
			c.dead = true
			c.mu.Unlock()
			return
		}
		c.mu.Lock()
		c.raw = append(c.raw, string(data))
		c.mu.Unlock()
	}
}

// frames returns every frame this client has received so far.
func (c *client) frames() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.raw...)
}

// mark records how many frames have arrived, for windowed assertions.
func (c *client) mark() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.raw)
}

func (c *client) framesFrom(n int) []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	if n > len(c.raw) {
		return nil
	}
	return append([]string(nil), c.raw[n:]...)
}

func (c *client) closed() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.dead
}

// settle waits for the server to go quiet.
func (c *client) settle(d time.Duration) { time.Sleep(d) }

// next returns the next unconsumed frame, waiting for it to arrive.
func (c *client) next() (protocol.ServerMessage, bool) {
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		c.mu.Lock()
		if c.cursor < len(c.raw) {
			body := c.raw[c.cursor]
			c.cursor++
			c.mu.Unlock()
			var m protocol.ServerMessage
			if err := json.Unmarshal([]byte(body), &m); err != nil {
				c.t.Fatalf("undecodable frame %q", body)
			}
			return m, true
		}
		dead := c.dead
		c.mu.Unlock()
		if dead {
			return protocol.ServerMessage{}, false
		}
		time.Sleep(2 * time.Millisecond)
	}
	return protocol.ServerMessage{}, false
}

func (c *client) read() protocol.ServerMessage {
	c.t.Helper()
	m, ok := c.next()
	if !ok {
		c.t.Fatal("no further frames arrived")
	}
	return m
}

// await consumes frames until one of the requested type shows up.
func (c *client) await(typ string) protocol.ServerMessage {
	c.t.Helper()
	for i := 0; i < 200; i++ {
		m, ok := c.next()
		if !ok {
			break
		}
		if m.Type == typ {
			return m
		}
	}
	c.t.Fatalf("never saw a %s message; got:\n%s", typ, strings.Join(c.frames(), "\n"))
	return protocol.ServerMessage{}
}

// awaitEvent consumes frames until an events frame contains the named event.
func (c *client) awaitEvent(name string) {
	c.t.Helper()
	for i := 0; i < 200; i++ {
		m, ok := c.next()
		if !ok {
			break
		}
		if m.Type != protocol.SEvents {
			continue
		}
		var d protocol.EventsData
		c.into(m, &d)
		for _, e := range d.Events {
			if e.Name == name {
				return
			}
		}
	}
	c.t.Fatalf("never saw the %s event", name)
}

func (c *client) send(typ string, data any) {
	c.t.Helper()
	raw, err := json.Marshal(data)
	if err != nil {
		c.t.Fatal(err)
	}
	m := protocol.ClientMessage{V: protocol.Version, Type: typ, ID: "x", Data: raw}
	if data == nil {
		m.Data = nil
	}
	if err := c.c.WriteJSON(m); err != nil {
		c.t.Fatalf("write: %v", err)
	}
}

// sendRaw lets a test post a hand-rolled, possibly hostile payload.
func (c *client) sendRaw(body string) {
	c.t.Helper()
	if err := c.c.WriteMessage(websocket.TextMessage, []byte(body)); err != nil {
		c.t.Fatalf("write: %v", err)
	}
}

func (c *client) into(m protocol.ServerMessage, v any) {
	c.t.Helper()
	raw, err := json.Marshal(m.Data)
	if err != nil {
		c.t.Fatal(err)
	}
	if err := json.Unmarshal(raw, v); err != nil {
		c.t.Fatal(err)
	}
}

// ---------- tests ----------

func TestAuthIsRequiredBeforeTheSocketExists(t *testing.T) {
	h := newHarness(t)
	url := "ws" + strings.TrimPrefix(h.srv.URL, "http") + "/ws"
	if _, res, err := websocket.DefaultDialer.Dial(url, nil); err == nil {
		t.Fatal("an unauthenticated socket was accepted")
	} else if res == nil || res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %v", res)
	}
	if _, res, err := websocket.DefaultDialer.Dial(url+"?token=made-up", nil); err == nil {
		t.Fatal("a bogus token was accepted")
	} else if res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", res.StatusCode)
	}
}

func TestFullRoomFlowOverWebSocket(t *testing.T) {
	h := newHarness(t)
	ann := h.dial(t, "Ann")
	ann.send(protocol.CCreateRoom, protocol.CreateRoomData{Public: true, CardsPerPlayer: 4, MaxPlayers: 4})

	var rs protocol.RoomStateData
	ann.into(ann.await(protocol.SRoomState), &rs)
	code := rs.Code
	if code == "" || rs.Status != string(room.StatusWaiting) {
		t.Fatalf("bad room state: %+v", rs)
	}

	bo := h.dial(t, "Bo")
	bo.send(protocol.CListRooms, nil)
	var list protocol.RoomListData
	bo.into(bo.await(protocol.SRoomList), &list)
	if len(list.Rooms) != 1 || list.Rooms[0].Code != code {
		t.Fatalf("the public room should be listed, got %+v", list)
	}

	bo.send(protocol.CJoinRoom, protocol.JoinRoomData{Code: code})
	bo.into(bo.await(protocol.SRoomState), &rs)
	if len(rs.Players) != 2 {
		t.Fatalf("expected two players, got %d", len(rs.Players))
	}

	ann.send(protocol.CReady, protocol.ReadyData{Ready: true})
	bo.send(protocol.CReady, protocol.ReadyData{Ready: true})
	for i := 0; i < 10; i++ {
		ann.into(ann.await(protocol.SRoomState), &rs)
		if rs.Status == string(room.StatusReady) {
			break
		}
	}
	if rs.Status != string(room.StatusReady) {
		t.Fatalf("status is %s, want READY", rs.Status)
	}

	ann.send(protocol.CStartGame, nil)
	ann.await(protocol.SGameStarted)
	bo.await(protocol.SGameStarted)

	// Each side gets its own filtered view.
	var su protocol.StateUpdateData
	ann.into(ann.await(protocol.SStateUpdate), &su)
	if su.View.Me != ann.id {
		t.Fatalf("Ann received a view for %s", su.View.Me)
	}
	if len(su.View.MyCards) != 4 || len(su.View.Opponents) != 1 {
		t.Fatalf("unexpected view: %+v", su.View)
	}
	for _, hcard := range su.View.MyCards {
		if hcard.ID == 0 {
			t.Fatal("card handles should carry a physical id")
		}
	}

	// Memory phase, then the first draw is private to whoever drew.
	ann.send(protocol.CAction, protocol.ActionData{
		Kind: protocol.AInspectMemory, Args: json.RawMessage(`{"positions":[0,1]}`),
	})
	bo.send(protocol.CAction, protocol.ActionData{
		Kind: protocol.AInspectMemory, Args: json.RawMessage(`{"positions":[0,1]}`),
	})
	ann.awaitEvent("memory_revealed")
}

// The heart of Phase 3C, exercised over a real socket.
func TestForgedPlayerIdInThePayloadIsIgnored(t *testing.T) {
	h := newHarness(t)
	ann := h.dial(t, "Ann")
	ann.send(protocol.CCreateRoom, protocol.CreateRoomData{CardsPerPlayer: 4})
	var rs protocol.RoomStateData
	ann.into(ann.await(protocol.SRoomState), &rs)

	bo := h.dial(t, "Bo")
	bo.send(protocol.CJoinRoom, protocol.JoinRoomData{Code: rs.Code})
	bo.await(protocol.SRoomState)
	ann.send(protocol.CReady, protocol.ReadyData{Ready: true})
	bo.send(protocol.CReady, protocol.ReadyData{Ready: true})
	ann.send(protocol.CStartGame, nil)
	ann.await(protocol.SGameStarted)
	bo.await(protocol.SGameStarted)

	for _, c := range []*client{ann, bo} {
		c.send(protocol.CAction, protocol.ActionData{
			Kind: protocol.AInspectMemory, Args: json.RawMessage(`{"positions":[0,1]}`),
		})
	}

	// Work out whose turn it is from Bo's view, then have the OTHER player try
	// to act as them by stuffing a player id into the payload.
	var su protocol.StateUpdateData
	turn := engine.PlayerID("")
	for i := 0; i < 30 && turn == ""; i++ {
		m := bo.read()
		if m.Type == protocol.SStateUpdate {
			bo.into(m, &su)
			if su.View.Phase == "turn_start" {
				turn = su.View.TurnPlayer
			}
		}
	}
	if turn == "" {
		t.Fatal("never saw the first turn")
	}
	impostor, victim := ann, bo
	if turn == ann.id {
		impostor, victim = bo, ann
	}

	// Every shape of forgery we can think of, all from the wrong socket.
	impostor.sendRaw(`{"v":1,"type":"action","id":"f1","data":{"kind":"draw_card","player":"` + string(turn) + `"}}`)
	impostor.sendRaw(`{"v":1,"type":"action","id":"f2","data":{"kind":"draw_card","actor":"` + string(turn) + `"},"player":"` + string(turn) + `"}`)
	impostor.sendRaw(`{"v":1,"type":"action","id":"f3","data":{"kind":"draw_card","args":{"player":"` + string(turn) + `"}}}`)

	for i := 0; i < 3; i++ {
		m := impostor.await(protocol.SError)
		var e protocol.ErrorData
		impostor.into(m, &e)
		if e.Code != "not_your_turn" {
			t.Fatalf("forged action returned %q, want not_your_turn", e.Code)
		}
	}

	// The game did not move: the real player still has the turn.
	victim.send(protocol.CAction, protocol.ActionData{Kind: protocol.ADrawCard})
	for i := 0; i < 30; i++ {
		m := victim.read()
		if m.Type == protocol.SStateUpdate {
			victim.into(m, &su)
			if su.View.Phase == "draw_decision" {
				if su.View.MyDrawnCard == nil {
					t.Fatal("the legitimate drawer should see their card")
				}
				return
			}
		}
	}
	t.Fatal("the legitimate draw never went through")
}

func TestReconnectOverWebSocketRestoresTheSeat(t *testing.T) {
	h := newHarness(t)
	ann := h.dial(t, "Ann")
	ann.send(protocol.CCreateRoom, protocol.CreateRoomData{CardsPerPlayer: 4})
	var rs protocol.RoomStateData
	ann.into(ann.await(protocol.SRoomState), &rs)

	tok, boID := h.token(t, "Bo")
	bo := h.dialToken(t, tok, boID, "Bo")
	bo.send(protocol.CJoinRoom, protocol.JoinRoomData{Code: rs.Code})
	bo.await(protocol.SRoomState)
	ann.send(protocol.CReady, protocol.ReadyData{Ready: true})
	bo.send(protocol.CReady, protocol.ReadyData{Ready: true})
	ann.send(protocol.CStartGame, nil)
	ann.await(protocol.SGameStarted)

	// Bo's socket dies.
	_ = bo.c.Close()

	// Ann should see the pause.
	paused := false
	for i := 0; i < 40 && !paused; i++ {
		m := ann.read()
		if m.Type == protocol.SStateUpdate {
			var su protocol.StateUpdateData
			ann.into(m, &su)
			paused = su.View.Paused
		}
	}
	if !paused {
		t.Fatal("the table should have been paused by the disconnect")
	}

	// Bo comes back with the same token: same identity, same seat.
	bo2 := h.dialToken(t, tok, boID, "Bo")
	bo2.send(protocol.CReconnect, protocol.JoinRoomData{})
	var su protocol.StateUpdateData
	bo2.into(bo2.await(protocol.SStateUpdate), &su)
	if su.View.Me != boID {
		t.Fatalf("reconnected as %s, want %s", su.View.Me, boID)
	}
	if su.View.Paused {
		t.Fatal("the game should have resumed")
	}
	if len(su.View.MyCards) != 4 {
		t.Fatalf("Bo's hand came back as %d cards", len(su.View.MyCards))
	}
}

// Nothing on the socket may contain raw engine state.
func TestWebSocketFramesNeverCarryRawState(t *testing.T) {
	h := newHarness(t)
	ann := h.dial(t, "Ann")
	ann.send(protocol.CCreateRoom, protocol.CreateRoomData{CardsPerPlayer: 4})
	var rs protocol.RoomStateData
	ann.into(ann.await(protocol.SRoomState), &rs)
	bo := h.dial(t, "Bo")
	bo.send(protocol.CJoinRoom, protocol.JoinRoomData{Code: rs.Code})
	bo.await(protocol.SRoomState)
	ann.send(protocol.CReady, protocol.ReadyData{Ready: true})
	bo.send(protocol.CReady, protocol.ReadyData{Ready: true})
	ann.send(protocol.CStartGame, nil)

	for _, c := range []*client{ann, bo} {
		c.send(protocol.CAction, protocol.ActionData{
			Kind: protocol.AInspectMemory, Args: json.RawMessage(`{"positions":[0,1]}`),
		})
	}
	bo.settle(500 * time.Millisecond)

	frames := bo.frames()
	if len(frames) == 0 {
		t.Fatal("no frames were captured")
	}
	for _, body := range frames {
		for _, forbidden := range []string{
			`"Deck":`, `"Discard":`, `"Cards":`, `"RoundPending":`, `"TurnIdx":`,
			`"PendingPenalty":`, `"DrawnBy":`, `"MatchExtensions":`, `"shuffler"`,
		} {
			if strings.Contains(body, forbidden) {
				t.Fatalf("frame leaked raw engine state (%s): %s", forbidden, body)
			}
		}
		if strings.Contains(body, `"type":"state_update"`) {
			if n := strings.Count(body, `"rank"`); n > 2 {
				t.Fatalf("state update exposed %d card identities: %s", n, body)
			}
		}
	}
}

func TestUnknownMessageTypeIsRejectedPolitely(t *testing.T) {
	h := newHarness(t)
	ann := h.dial(t, "Ann")
	ann.sendRaw(`{"v":1,"type":"drop_tables","id":"z"}`)
	m := ann.await(protocol.SError)
	var e protocol.ErrorData
	ann.into(m, &e)
	if e.Message == "" {
		t.Fatal("expected an explanation")
	}
	// The socket stays usable.
	ann.send(protocol.CPing, nil)
	ann.await(protocol.SPong)
}

func TestHealthAndDevAuthEndpoints(t *testing.T) {
	h := newHarness(t)
	res, err := http.Get(h.srv.URL + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("healthz returned %d", res.StatusCode)
	}
	// GET on the auth endpoint is refused.
	res2, err := http.Get(h.srv.URL + "/auth/dev")
	if err != nil {
		t.Fatal(err)
	}
	defer res2.Body.Close()
	if res2.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", res2.StatusCode)
	}
	// Two players get different ids from the same name.
	_, a := h.token(t, "Same")
	_, b := h.token(t, "Same")
	if a == b {
		t.Fatal("two sessions must not share a player id")
	}
}
