package server

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/kaushlendra/combio/backend/internal/engine"
	"github.com/kaushlendra/combio/backend/internal/protocol"
	"github.com/kaushlendra/combio/backend/internal/room"
	"github.com/kaushlendra/combio/backend/internal/ws"
)

// ---------- 6: reconnect security ----------

// A token identifies exactly one player. It can never be used to take over
// somebody else's seat, and it cannot be steered by anything in the payload.
func TestATokenCannotReconnectAsAnotherPlayer(t *testing.T) {
	h := newHarness(t)

	annTok, annID := h.token(t, "Ann")
	boTok, boID := h.token(t, "Bo")
	if annID == boID {
		t.Fatal("two identities must not share a player id")
	}

	ann := h.dialToken(t, annTok, annID, "Ann")
	ann.send(protocol.CCreateRoom, protocol.CreateRoomData{CardsPerPlayer: 4})
	var rs protocol.RoomStateData
	ann.into(ann.await(protocol.SRoomState), &rs)

	bo := h.dialToken(t, boTok, boID, "Bo")
	bo.send(protocol.CJoinRoom, protocol.JoinRoomData{Code: rs.Code})
	bo.await(protocol.SRoomState)
	ann.send(protocol.CReady, protocol.ReadyData{Ready: true})
	bo.send(protocol.CReady, protocol.ReadyData{Ready: true})
	ann.send(protocol.CStartGame, nil)
	ann.await(protocol.SGameStarted)

	// Bo drops.
	_ = bo.c.Close()

	// Ann tries to come back AS Bo, every way she can phrase it.
	impostor := h.dialToken(t, annTok, annID, "Ann")
	impostor.sendRaw(`{"v":1,"type":"reconnect","id":"i1","data":{"code":"` + rs.Code +
		`","player":"` + string(boID) + `"}}`)
	impostor.sendRaw(`{"v":1,"type":"reconnect","id":"i2","data":{"code":"` + rs.Code +
		`"},"player":"` + string(boID) + `"}`)
	impostor.sendRaw(`{"v":1,"type":"join_room","id":"i3","data":{"code":"` + rs.Code +
		`","player_id":"` + string(boID) + `"}}`)

	// She is still Ann on every frame she receives, and never sees Bo's view.
	impostor.settle(400 * time.Millisecond)
	frames := impostor.frames()
	if len(frames) == 0 {
		t.Fatal("the impostor received nothing at all")
	}
	for _, f := range frames {
		if strings.Contains(f, `"me":"`+string(boID)+`"`) {
			t.Fatalf("the impostor was handed Bo's view: %s", f)
		}
		if strings.Contains(f, `"player_id":"`+string(boID)+`"`) {
			t.Fatalf("the impostor was welcomed as Bo: %s", f)
		}
	}

	// And Bo's seat is still Bo's, still waiting for Bo.
	r, ok := h.rooms.Get(rs.Code)
	if !ok {
		t.Fatal("room vanished")
	}
	for _, p := range r.Snapshot().Players {
		if p.ID == boID && p.Connected {
			t.Fatal("Bo's seat was taken over by Ann's token")
		}
	}
	// The real token still works.
	bo2 := h.dialToken(t, boTok, boID, "Bo")
	bo2.send(protocol.CReconnect, protocol.JoinRoomData{Code: rs.Code})
	var su protocol.StateUpdateData
	bo2.into(bo2.await(protocol.SStateUpdate), &su)
	if su.View.Me != boID {
		t.Fatalf("Bo reconnected as %s", su.View.Me)
	}
}

// ---------- 7: every private reveal, inspected on the wire ----------

// startTwoPlayerGame opens a room, seats two players and starts the game,
// leaving it in the memory phase.
func startTwoPlayerGame(t *testing.T, h *harness) (*client, *client, *room.Room) {
	t.Helper()
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

	r, ok := h.rooms.Get(rs.Code)
	if !ok {
		t.Fatal("room vanished")
	}
	return ann, bo, r
}

// runMemoryPhase has both players inspect their own cards for real.
func runMemoryPhase(ann, bo *client) {
	for _, c := range []*client{ann, bo} {
		c.send(protocol.CAction, protocol.ActionData{
			Kind: protocol.AInspectMemory, Args: json.RawMessage(`{"positions":[0,1]}`)})
	}
	time.Sleep(150 * time.Millisecond)
}

// pinDeal replaces the room's state so a test can trigger exact powers.
func pinDeal(t *testing.T, r *room.Room, ann, bo *client, deck []string) {
	t.Helper()
	st, err := engine.BuildScenario(engine.Scenario{
		Players: []engine.PlayerID{ann.id, bo.id},
		Hands: map[engine.PlayerID][]string{
			ann.id: {"2C", "3D", "4S", "5H"},
			bo.id:  {"KS", "QD", "JH", "TC"},
		},
		Deck:       deck,
		DiscardTop: "9C",
		Unlocked:   true,
	})
	if err != nil {
		t.Fatal(err)
	}
	r.ReplaceStateForTesting(st)
}

// handsOf reads every player's cards straight from the authoritative state.
func handsOf(r *room.Room, id engine.PlayerID) []string {
	var out []string
	r.WithState(func(s *engine.State) {
		for _, cid := range s.Player(id).Layout.Cards() {
			out = append(out, cardJSON(s.MustCard(cid)))
		}
	})
	return out
}

func TestEveryPrivateRevealStaysOffTheOtherPlayersSocket(t *testing.T) {
	h := newHarness(t)
	ann, bo, r := startTwoPlayerGame(t, h)

	// The memory reveal is the first of the five, and it happens on the real
	// deal, so capture that deal before anyone looks at it.
	annOriginal := handsOf(r, ann.id)
	annMark, boMark := ann.mark(), bo.mark()

	runMemoryPhase(ann, bo)
	ann.settle(300 * time.Millisecond)
	memAnn := strings.Join(ann.framesFrom(annMark), "\n")
	memBo := strings.Join(bo.framesFrom(boMark), "\n")
	if !strings.Contains(memAnn, "memory_revealed") {
		t.Fatal("Ann never received her memory reveal")
	}
	for _, secret := range annOriginal[:2] {
		if !strings.Contains(memAnn, secret) {
			t.Fatalf("Ann's own reveal did not include %s", secret)
		}
		if strings.Contains(memBo, secret) {
			t.Fatalf("Bo's socket carried Ann's memorised card %s", secret)
		}
	}

	// Now pin a deal that hands Ann a 10 and then a Jack.
	pinDeal(t, r, ann, bo, []string{"7S", "KD", "JC", "TD"})
	secrets := append(handsOf(r, ann.id), handsOf(r, bo.id)...)
	annMark, boMark = ann.mark(), bo.mark()

	// private draw -> the 10 peek
	ann.send(protocol.CAction, protocol.ActionData{Kind: protocol.ADrawCard})
	ann.send(protocol.CAction, protocol.ActionData{Kind: protocol.ADiscardDrawn})
	time.Sleep(100 * time.Millisecond)
	h.clock.Advance(room.DefaultTimers().Match)
	time.Sleep(100 * time.Millisecond)
	ann.send(protocol.CAction, protocol.ActionData{
		Kind: protocol.APeek10, Args: json.RawMessage(`{"index":2}`)})

	// Bo takes a quiet turn so play comes back round to Ann.
	time.Sleep(100 * time.Millisecond)
	bo.send(protocol.CAction, protocol.ActionData{Kind: protocol.ADrawCard})
	bo.send(protocol.CAction, protocol.ActionData{Kind: protocol.ADiscardDrawn})
	time.Sleep(100 * time.Millisecond)
	h.clock.Advance(room.DefaultTimers().Match)
	time.Sleep(100 * time.Millisecond)

	// Jack peek at one of Bo's cards, and a King peek after it.
	ann.send(protocol.CAction, protocol.ActionData{Kind: protocol.ADrawCard})
	ann.send(protocol.CAction, protocol.ActionData{Kind: protocol.ADiscardDrawn})
	time.Sleep(100 * time.Millisecond)
	h.clock.Advance(room.DefaultTimers().Match)
	time.Sleep(100 * time.Millisecond)
	ann.send(protocol.CAction, protocol.ActionData{
		Kind: protocol.AJackPeek,
		Args: json.RawMessage(`{"target":"` + string(bo.id) + `","target_index":0}`)})
	time.Sleep(150 * time.Millisecond)

	ann.settle(300 * time.Millisecond)
	annFrames := ann.framesFrom(annMark)
	boFrames := bo.framesFrom(boMark)

	annBlob := strings.Join(annFrames, "\n")
	for _, want := range []string{"private_draw", "private_reveal", "power_available"} {
		if !strings.Contains(annBlob, want) {
			t.Fatalf("Ann never received %s; the test did not exercise it", want)
		}
	}
	// Bo has private events of their own (Bo drew a card), so the test is not
	// "Bo received no private events" but "Bo received none of ANN's".
	boBlob := strings.Join(boFrames, "\n")
	if strings.Contains(boBlob, `"owner":"`+string(ann.id)+`"`) {
		t.Fatalf("Bo was told what Ann saw: %s", boBlob)
	}
	if strings.Contains(boBlob, "memory_revealed") {
		t.Fatalf("Bo received a memory reveal after the memory phase: %s", boBlob)
	}
	// Every private_reveal Bo received (if any) must be about Bo.
	for _, f := range boFrames {
		if strings.Contains(f, "private_reveal") && !strings.Contains(f, `"owner":"`+string(bo.id)+`"`) {
			t.Fatalf("Bo received a reveal that was not theirs: %s", f)
		}
	}

	// No hidden identity anywhere in Bo's frames. The only identities Bo may
	// legitimately see are cards that reached the discard pile.
	var public []string
	r.WithState(func(s *engine.State) {
		for _, id := range s.Discard {
			public = append(public, cardJSON(s.MustCard(id)))
		}
	})
	isPublic := func(c string) bool {
		for _, p := range public {
			if p == c {
				return true
			}
		}
		return false
	}
	for _, frame := range boFrames {
		for _, secret := range secrets {
			if isPublic(secret) {
				continue
			}
			if strings.Contains(frame, secret) {
				t.Fatalf("Bo's socket carried a hidden card %s: %s", secret, frame)
			}
		}
		for _, forbidden := range []string{`"Deck":`, `"Cards":`, `"RoundPending":`, `"shuffler"`} {
			if strings.Contains(frame, forbidden) {
				t.Fatalf("Bo's socket carried raw engine state (%s)", forbidden)
			}
		}
	}
}

// waitClosed reports whether the server hung up within a couple of seconds.
func waitClosed(c *client) bool {
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if c.closed() {
			return true
		}
		time.Sleep(5 * time.Millisecond)
	}
	return false
}

// cardJSON renders a card exactly as the wire encoder would, so a frame scan
// looks for the real bytes rather than a prose description.
func cardJSON(c engine.Card) string {
	raw, _ := json.Marshal(c)
	return string(raw)
}

// Errors, room_state and reconnect frames must never carry a card identity.
func TestControlFramesCarryNoCardIdentities(t *testing.T) {
	h := newHarness(t)
	ann, bo, r := startTwoPlayerGame(t, h)
	runMemoryPhase(ann, bo)
	pinDeal(t, r, ann, bo, []string{"7S", "8D"})
	secrets := append(handsOf(r, ann.id), handsOf(r, bo.id)...)
	annMark, boMark := ann.mark(), bo.mark()

	// Provoke a pile of rejections of every shape.
	bo.send(protocol.CAction, protocol.ActionData{Kind: protocol.ADrawCard})      // not your turn
	bo.send(protocol.CAction, protocol.ActionData{Kind: protocol.ADeclareCombio}) // not your turn
	ann.send(protocol.CAction, protocol.ActionData{
		Kind: protocol.APeek10, Args: json.RawMessage(`{"index":99}`)}) // wrong phase
	ann.send(protocol.CAction, protocol.ActionData{Kind: "nonsense"})
	ann.send(protocol.CStartGame, nil) // already started
	ann.send(protocol.CReady, protocol.ReadyData{Ready: false})
	bo.send(protocol.CReconnect, protocol.JoinRoomData{Code: r.Code()})

	ann.settle(400 * time.Millisecond)
	frames := append(ann.framesFrom(annMark), bo.framesFrom(boMark)...)
	if len(frames) == 0 {
		t.Fatal("no frames captured")
	}
	sawError := false
	for _, f := range frames {
		if strings.Contains(f, `"type":"error"`) {
			sawError = true
		}
		for _, secret := range secrets {
			if strings.Contains(f, secret) {
				t.Fatalf("a control frame leaked %s: %s", secret, f)
			}
		}
	}
	if !sawError {
		t.Fatal("the rejections were never reported")
	}
}

// ---------- 10: protocol robustness ----------

func TestProtocolVersioningAndMalformedFrames(t *testing.T) {
	h := newHarness(t)
	c := h.dial(t, "Ann")

	// Wrong version, missing version, and a version from the future.
	for _, body := range []string{
		`{"v":2,"type":"ping","id":"v2"}`,
		`{"type":"ping","id":"v0"}`,
		`{"v":99,"type":"create_room","id":"v99","data":{}}`,
	} {
		c.sendRaw(body)
		var e protocol.ErrorData
		c.into(c.await(protocol.SError), &e)
		if e.Code != "unsupported_version" {
			t.Fatalf("%s returned %q, want unsupported_version", body, e.Code)
		}
	}

	// Malformed JSON is answered, not fatal: the socket keeps working.
	for _, body := range []string{`{`, `not json at all`, `[1,2,3]`, `{"v":1,"type":`} {
		c.sendRaw(body)
		var e protocol.ErrorData
		c.into(c.await(protocol.SError), &e)
		if e.Code != "malformed" {
			t.Fatalf("%q returned %q, want malformed", body, e.Code)
		}
	}
	c.send(protocol.CPing, nil)
	c.await(protocol.SPong)

	// Well-formed JSON with a nonsense body is rejected without hurting anyone.
	c.sendRaw(`{"v":1,"type":"create_room","id":"bad","data":{"cards_per_player":"lots"}}`)
	c.into(c.await(protocol.SError), &protocol.ErrorData{})
	c.send(protocol.CPing, nil)
	c.await(protocol.SPong)
}

// A client that keeps sending garbage is eventually hung up on, cleanly.
func TestRepeatedGarbageClosesTheConnection(t *testing.T) {
	h := newHarness(t)
	c := h.dial(t, "Ann")
	for i := 0; i < 10; i++ {
		if err := c.c.WriteMessage(websocket.TextMessage, []byte(`{`)); err != nil {
			break
		}
	}
	if !waitClosed(c) {
		t.Fatal("a client spraying garbage should be disconnected")
	}
	// The server is unharmed.
	c2 := h.dial(t, "Bo")
	c2.send(protocol.CPing, nil)
	c2.await(protocol.SPong)
}

// One client cannot make the server allocate without bound.
func TestOversizedFrameIsRejected(t *testing.T) {
	h := newHarness(t)
	c := h.dial(t, "Ann")

	big := strings.Repeat("A", ws.MaxMessageSize+4096)
	_ = c.c.WriteMessage(websocket.TextMessage,
		[]byte(`{"v":1,"type":"create_room","id":"big","data":{"pad":"`+big+`"}}`))

	if !waitClosed(c) {
		t.Fatal("an oversized frame should close the connection")
	}
	// The server keeps serving everyone else.
	c2 := h.dial(t, "Bo")
	c2.send(protocol.CPing, nil)
	c2.await(protocol.SPong)
	if h.rooms.Count() != 0 {
		t.Fatal("the oversized frame should not have created a room")
	}
}

// ---------- 11: room limits ----------

func TestRoomLimitIsEnforcedOverTheSocket(t *testing.T) {
	dev := newDevAuth()
	rooms := room.NewManager(room.ManagerOptions{
		Clock: room.NewFakeClock(), Timers: room.DefaultTimers(), MaxRooms: 2,
	})
	s := New(Options{Dev: dev, Rooms: rooms})
	h := serve(t, s, dev, rooms, nil)

	c := h.dial(t, "Ann")
	for i := 0; i < 2; i++ {
		c.send(protocol.CCreateRoom, protocol.CreateRoomData{CardsPerPlayer: 4})
		c.await(protocol.SRoomState)
	}
	c.send(protocol.CCreateRoom, protocol.CreateRoomData{CardsPerPlayer: 4})
	var e protocol.ErrorData
	c.into(c.await(protocol.SError), &e)
	if e.Message == "" {
		t.Fatal("the room limit should be reported")
	}
	if rooms.Count() != 2 {
		t.Fatalf("%d rooms exist, the limit was 2", rooms.Count())
	}
}

// ---------- 12: graceful shutdown ----------

func TestGracefulShutdown(t *testing.T) {
	h := newHarness(t)

	ann := h.dial(t, "Ann")
	ann.send(protocol.CCreateRoom, protocol.CreateRoomData{CardsPerPlayer: 4})
	var rs protocol.RoomStateData
	ann.into(ann.await(protocol.SRoomState), &rs)
	bo := h.dial(t, "Bo")
	bo.send(protocol.CJoinRoom, protocol.JoinRoomData{Code: rs.Code})
	bo.await(protocol.SRoomState)

	r, ok := h.rooms.Get(rs.Code)
	if !ok {
		t.Fatal("room vanished")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	done := make(chan error, 1)
	go func() { done <- h.server.Shutdown(ctx) }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("shutdown returned %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("shutdown deadlocked")
	}

	// Rooms are gone and their goroutines have been told to stop.
	select {
	case <-r.Done():
	default:
		t.Fatal("the room loop was never stopped")
	}
	if h.rooms.Count() != 0 {
		t.Fatalf("%d rooms survived shutdown", h.rooms.Count())
	}

	// Open sockets were closed.
	for _, c := range []*client{ann, bo} {
		if !waitClosed(c) {
			t.Fatal("an open socket survived shutdown")
		}
	}

	// New connections are refused, cleanly.
	tok, _ := h.token(t, "Late")
	url := "ws" + strings.TrimPrefix(h.srv.URL, "http") + "/ws?token=" + tok
	if _, res, err := websocket.DefaultDialer.Dial(url, nil); err == nil {
		t.Fatal("a new socket was accepted during shutdown")
	} else if res == nil || res.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %v", res)
	}

	// And a second shutdown is a no-op rather than a panic.
	if err := h.server.Shutdown(ctx); err != nil {
		t.Fatalf("the second shutdown returned %v", err)
	}
}

// ---------- browser cross-origin access ----------

// The frontend runs on :3000 and the server on :8080, so every call from the
// page is cross-origin. Without these headers the browser receives the token
// and then refuses to hand it to JavaScript, which surfaces as an opaque
// "Failed to fetch". Node's fetch does not enforce CORS, so only a test that
// checks the headers explicitly catches this.
func TestBrowserCanReadTheAuthResponseCrossOrigin(t *testing.T) {
	h := newHarness(t)
	const origin = "http://localhost:3000"

	req, err := http.NewRequest(http.MethodPost, h.srv.URL+"/auth/dev?name=Ann", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Origin", origin)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()

	if res.StatusCode != http.StatusOK {
		t.Fatalf("auth returned %d", res.StatusCode)
	}
	if got := res.Header.Get("Access-Control-Allow-Origin"); got != origin {
		t.Fatalf("Access-Control-Allow-Origin = %q; the browser would discard this response", got)
	}
	if !strings.Contains(res.Header.Get("Vary"), "Origin") {
		t.Fatal("responses that depend on Origin must say so in Vary")
	}
	// A token really was issued.
	var body struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil || body.Token == "" {
		t.Fatalf("no token in the response: %v", err)
	}
}

func TestPreflightIsAnswered(t *testing.T) {
	h := newHarness(t)
	req, err := http.NewRequest(http.MethodOptions, h.srv.URL+"/auth/dev", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Origin", "http://localhost:3000")
	req.Header.Set("Access-Control-Request-Method", "POST")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()

	if res.StatusCode != http.StatusNoContent {
		t.Fatalf("preflight returned %d, want 204", res.StatusCode)
	}
	if !strings.Contains(res.Header.Get("Access-Control-Allow-Methods"), "POST") {
		t.Fatal("preflight did not allow POST")
	}
	if res.Header.Get("Access-Control-Allow-Origin") == "" {
		t.Fatal("preflight did not allow the origin")
	}
}

// An explicit allow-list is honoured, and nothing is granted to an origin
// outside it.
func TestOriginAllowListIsEnforced(t *testing.T) {
	dev := newDevAuth()
	rooms := room.NewManager(room.ManagerOptions{Clock: room.NewFakeClock()})
	s := New(Options{Dev: dev, Rooms: rooms, AllowedOrigins: []string{"https://combio.example"}})
	h := serve(t, s, dev, rooms, nil)

	check := func(origin string) string {
		t.Helper()
		req, err := http.NewRequest(http.MethodPost, h.srv.URL+"/auth/dev?name=Ann", nil)
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Origin", origin)
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		return res.Header.Get("Access-Control-Allow-Origin")
	}

	if got := check("https://combio.example"); got != "https://combio.example" {
		t.Fatalf("an allowed origin got %q", got)
	}
	if got := check("https://evil.example"); got != "" {
		t.Fatalf("a disallowed origin was granted %q", got)
	}
}

// A request with no Origin header is not a browser, and gets no CORS headers.
func TestNonBrowserRequestsAreUntouched(t *testing.T) {
	h := newHarness(t)
	res, err := http.Post(h.srv.URL+"/auth/dev?name=Ann", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status %d", res.StatusCode)
	}
	if res.Header.Get("Access-Control-Allow-Origin") != "" {
		t.Fatal("a request without an Origin should not get CORS headers")
	}
}
