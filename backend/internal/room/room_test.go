package room

import (
	"encoding/json"
	"strings"
	"sync"
	"testing"

	"github.com/kaushlendra/combio/backend/internal/engine"
	"github.com/kaushlendra/combio/backend/internal/protocol"
)

// ---------- 1-5: lifecycle ----------

func TestRoomCreationAndJoining(t *testing.T) {
	tb := newTable(t, 3, 4, 7)
	info := tb.room.Snapshot()

	if info.Status != StatusWaiting {
		t.Fatalf("a fresh room should be WAITING, got %s", info.Status)
	}
	if len(info.Players) != 3 {
		t.Fatalf("expected 3 seats, got %d", len(info.Players))
	}
	if info.Host != tb.clients[0].id {
		t.Fatalf("the first player should host, got %s", info.Host)
	}
	// Everyone learns about everyone.
	for _, c := range tb.clients {
		if len(c.ofType(protocol.SRoomState)) == 0 {
			t.Fatalf("%s never received the room state", c.id)
		}
	}
	if len(tb.clients[0].ofType(protocol.SPlayerJoined)) != 3 {
		t.Fatalf("the host should have seen three joins, saw %d",
			len(tb.clients[0].ofType(protocol.SPlayerJoined)))
	}
}

func TestRoomIsFullAtMaxPlayers(t *testing.T) {
	clock := NewFakeClock()
	r := New(Config{Code: "FULL", CardsPerPlayer: 4, MaxPlayers: 2, Timers: testTimers(), Clock: clock})
	t.Cleanup(r.Close)
	a, b, c := newClient("a1", "Ann"), newClient("b1", "Bo"), newClient("c1", "Cy")
	r.Join(a, "1")
	r.Join(b, "2")
	r.Join(c, "3")
	r.sync(func() {})

	if got := len(r.Snapshot().Players); got != 2 {
		t.Fatalf("room holds %d players, max was 2", got)
	}
	if c.lastErrorCode() == "" {
		t.Fatal("the third player should have been refused")
	}
}

func TestReadyStateGatesStarting(t *testing.T) {
	tb := newTable(t, 2, 4, 7)

	// Not ready yet: start is refused and the status stays WAITING.
	tb.room.Start(tb.clients[0].id, "s")
	tb.settle()
	if tb.room.Snapshot().Status != StatusWaiting {
		t.Fatal("an unready room must not start")
	}
	if tb.clients[0].lastErrorCode() == "" {
		t.Fatal("the host should have been told why")
	}

	tb.room.SetReady(tb.clients[0].id, true, "r")
	tb.settle()
	if tb.room.Snapshot().Status != StatusWaiting {
		t.Fatal("one ready player is not enough")
	}
	tb.room.SetReady(tb.clients[1].id, true, "r")
	tb.settle()
	if tb.room.Snapshot().Status != StatusReady {
		t.Fatalf("status is %s, want READY", tb.room.Snapshot().Status)
	}

	// Only the host may start.
	tb.clients[1].reset()
	tb.room.Start(tb.clients[1].id, "s")
	tb.settle()
	if tb.clients[1].lastErrorCode() == "" {
		t.Fatal("a non-host must not be able to start the game")
	}
	if tb.room.Snapshot().Started {
		t.Fatal("the game started without the host")
	}

	tb.room.Start(tb.clients[0].id, "s")
	tb.settle()
	if !tb.room.Snapshot().Started {
		t.Fatal("the host should have started the game")
	}
}

func TestInvalidRoomTransitions(t *testing.T) {
	tb := newTable(t, 2, 4, 7)
	tb.startGame(t)

	// Cannot start twice.
	tb.clients[0].reset()
	tb.room.Start(tb.clients[0].id, "s")
	tb.settle()
	if tb.clients[0].lastErrorCode() == "" {
		t.Fatal("starting twice should be refused")
	}

	// Cannot toggle ready mid-game.
	tb.clients[1].reset()
	tb.room.SetReady(tb.clients[1].id, false, "r")
	tb.settle()
	if tb.clients[1].lastErrorCode() == "" {
		t.Fatal("ready should be refused once the game is running")
	}

	// A stranger cannot join a running game.
	stranger := newClient("zz", "Zed")
	tb.room.Join(stranger, "j")
	tb.settle()
	if stranger.lastErrorCode() == "" {
		t.Fatal("a new player must not join a game in progress")
	}
	if len(tb.room.Snapshot().Players) != 2 {
		t.Fatal("the stranger got a seat")
	}

	// But an existing player may re-attach: that is a reconnect, not a join.
	tb.room.Disconnected(tb.clients[1].id)
	tb.settle()
	back := newClient(string(tb.clients[1].id), "Bo")
	tb.room.Join(back, "j")
	tb.settle()
	if back.lastErrorCode() != "" {
		t.Fatalf("an existing player should be let back in, got %q", back.lastErrorCode())
	}
}

func TestLifecycleReachesEveryStatus(t *testing.T) {
	tb := newTable(t, 2, 4, 7)
	seen := map[Status]bool{tb.room.Snapshot().Status: true}

	tb.readyAll()
	seen[tb.room.Snapshot().Status] = true
	tb.room.Start(tb.clients[0].id, "s")
	tb.settle()
	seen[tb.room.Snapshot().Status] = true // STARTING
	tb.finishMemory(t)
	seen[tb.room.Snapshot().Status] = true // PLAYING

	// Play until COMBIO unlocks, then declare.
	for i := 0; i < 40 && tb.room.Snapshot().Status == StatusPlaying; i++ {
		cur := string(tb.currentPlayer())
		var unlocked bool
		tb.stateOf(func(s *engine.State) { unlocked = s.DeclareUnlocked() })
		if unlocked && tb.phase() == "turn_start" {
			tb.act(cur, protocol.ADeclareCombio, nil)
			break
		}
		tb.playPlainTurn(t, cur)
	}
	seen[tb.room.Snapshot().Status] = true // FINAL_TURNS

	for i := 0; i < 20 && tb.room.Snapshot().Status != StatusFinished; i++ {
		tb.playPlainTurn(t, string(tb.currentPlayer()))
	}
	seen[tb.room.Snapshot().Status] = true // FINISHED

	for _, want := range []Status{
		StatusWaiting, StatusReady, StatusStarting, StatusPlaying, StatusFinished,
	} {
		if !seen[want] {
			t.Errorf("the lifecycle never reached %s", want)
		}
	}
}

// playPlainTurn draws, discards and lets every timed phase lapse.
func (tb *table) playPlainTurn(t *testing.T, id string) {
	t.Helper()
	if tb.phase() == "turn_start" {
		tb.act(id, protocol.ADrawCard, nil)
	}
	if tb.phase() == "draw_decision" {
		tb.act(id, protocol.ADiscardDrawn, nil)
	}
	if tb.phase() == "matching" {
		tb.advance(testTimers().Match)
	}
	if p := tb.phase(); p == "power" || p == "king_decision" {
		tb.advance(testTimers().Power)
	}
}

// ---------- 6-7: actor authority ----------

func TestActionsUseTheAuthenticatedActor(t *testing.T) {
	tb := newTable(t, 2, 4, 7)
	tb.startGame(t)
	tb.finishMemory(t)

	first := tb.currentPlayer()
	other := tb.clients[1].id
	if first == other {
		other = tb.clients[0].id
	}

	// The room is told who is acting; nothing in the payload is consulted.
	// A forged "player" field is simply not part of the schema.
	forged, err := json.Marshal(map[string]any{"player": string(first), "kind": "draw_card"})
	if err != nil {
		t.Fatal(err)
	}
	tb.room.Act(other, protocol.ActionData{Kind: protocol.ADrawCard, Args: forged}, "c1")
	tb.settle()

	if code := tb.client(string(other)).lastErrorCode(); code != "not_your_turn" {
		t.Fatalf("the forged draw returned %q, want not_your_turn", code)
	}
	if tb.phase() != "turn_start" {
		t.Fatal("the forged action changed the game")
	}

	// The same payload from the real current player works.
	tb.room.Act(first, protocol.ActionData{Kind: protocol.ADrawCard, Args: forged}, "c2")
	tb.settle()
	if tb.phase() != "draw_decision" {
		t.Fatalf("phase is %s, the legitimate draw should have gone through", tb.phase())
	}
	var drawnBy engine.PlayerID
	tb.stateOf(func(s *engine.State) { drawnBy = s.DrawnBy })
	if drawnBy != first {
		t.Fatalf("the card was drawn by %s, want %s", drawnBy, first)
	}
}

func TestBuildActionAlwaysStampsTheGivenActor(t *testing.T) {
	raw := json.RawMessage(`{"player":"villain","target":"b1","target_index":1,"index":2}`)
	for _, kind := range []string{
		protocol.ADrawCard, protocol.ADeclareCombio, protocol.AAttemptOwnMatch,
		protocol.AAttemptOpponentMatch, protocol.AJackPeek, protocol.AKingSwap,
		protocol.ASkipPower, protocol.AInsertPenalty, protocol.APeek10,
		protocol.AQueenSwap, protocol.AKingPeek, protocol.ACancelKingSwap,
		protocol.AKeepDrawn, protocol.ADiscardDrawn, protocol.AInspectMemory,
	} {
		a, err := protocol.BuildAction(kind, raw, "hero")
		if err != nil {
			t.Fatalf("%s: %v", kind, err)
		}
		if a.Actor() != "hero" {
			t.Fatalf("%s: actor is %q, the payload forged it", kind, a.Actor())
		}
	}
	if _, err := protocol.BuildAction("rm -rf", nil, "hero"); err == nil {
		t.Fatal("an unknown action kind should be refused")
	}
}

func TestActionsFromOutsideTheRoomAreRefused(t *testing.T) {
	tb := newTable(t, 2, 4, 7)
	tb.startGame(t)
	tb.finishMemory(t)
	tb.room.Act("nobody", protocol.ActionData{Kind: protocol.ADrawCard}, "c1")
	tb.settle()
	if tb.phase() != "turn_start" {
		t.Fatal("an outsider changed the game")
	}
}

// ---------- 8-9: filtering ----------

func TestEachPlayerGetsOnlyTheirOwnView(t *testing.T) {
	tb := newTable(t, 3, 4, 7)
	tb.startGame(t)

	// Memory reveals: only the owner sees the cards.
	tb.resetAll()
	tb.act("a1", protocol.AInspectMemory, map[string]any{"positions": []int{0, 1}})

	ann := tb.client("a1")
	if !ann.sawEvent("memory_revealed") {
		t.Fatal("Ann should have received her own memory reveal")
	}
	for _, other := range []string{"b1", "c1"} {
		if tb.client(other).sawEvent("memory_revealed") {
			t.Fatalf("%s received Ann's private memory reveal", other)
		}
	}

	// Views are per-player: each sees their own cards as MyCards and everyone
	// else as opponents, with no rank anywhere.
	for _, c := range tb.clients {
		v := c.view(t)
		if v.Me != c.id {
			t.Fatalf("%s received a view addressed to %s", c.id, v.Me)
		}
		if len(v.Opponents) != 2 {
			t.Fatalf("%s sees %d opponents", c.id, len(v.Opponents))
		}
	}
}

func TestPrivateDrawAndPeeksNeverReachOtherPlayers(t *testing.T) {
	tb := newTable(t, 3, 4, 7)
	tb.startGame(t)
	tb.finishMemory(t)
	cur := string(tb.currentPlayer())

	tb.resetAll()
	tb.act(cur, protocol.ADrawCard, nil)

	if !tb.client(cur).sawEvent("private_draw") {
		t.Fatal("the drawer should see their own card")
	}
	for _, c := range tb.clients {
		if string(c.id) == cur {
			continue
		}
		if c.sawEvent("private_draw") {
			t.Fatalf("%s received someone else's private draw", c.id)
		}
		if v := c.view(t); v.MyDrawnCard != nil {
			t.Fatalf("%s's view carries a drawn card", c.id)
		}
	}
}

// Nothing that goes out over the wire may contain raw engine.State, and no
// state update may carry a card identity beyond the ones the recipient is
// entitled to (the middle card and their own drawn card).
func TestNoRawStateOrHiddenIdentitiesOnTheWire(t *testing.T) {
	tb := newTable(t, 3, 4, 7)
	tb.startGame(t)
	tb.finishMemory(t)

	for i := 0; i < 12; i++ {
		tb.playPlainTurn(t, string(tb.currentPlayer()))
	}

	for _, c := range tb.clients {
		blob := c.json(t)
		// engine.State has no json tags, so these capitalised keys would show
		// up verbatim if it were ever marshalled.
		for _, field := range []string{
			`"Deck":`, `"Discard":`, `"Cards":`, `"RoundPending":`, `"TurnIdx":`,
			`"PendingPenalty":`, `"DrawnBy":`, `"MatchExtensions":`,
		} {
			if strings.Contains(blob, field) {
				t.Fatalf("%s received raw engine state (%s)", c.id, field)
			}
		}
		// Per state update, at most two full card identities.
		for _, m := range c.ofType(protocol.SStateUpdate) {
			raw, err := json.Marshal(m)
			if err != nil {
				t.Fatal(err)
			}
			if n := strings.Count(string(raw), `"rank"`); n > 2 {
				t.Fatalf("%s got a state update exposing %d card identities: %s", c.id, n, raw)
			}
		}
	}
}

// ---------- 23-24: many rooms ----------

func TestManyRoomsRunConcurrentlyAndStayIsolated(t *testing.T) {
	m := NewManager(ManagerOptions{Clock: NewFakeClock(), Timers: testTimers(), Seed: 5})
	t.Cleanup(m.CloseAll)

	const rooms = 8
	var wg sync.WaitGroup
	codes := make([]string, rooms)
	for i := 0; i < rooms; i++ {
		r, err := m.Create(protocol.CreateRoomData{Public: true, CardsPerPlayer: 4, MaxPlayers: 4})
		if err != nil {
			t.Fatal(err)
		}
		codes[i] = r.Code()
		wg.Add(1)
		go func(r *Room, n int) {
			defer wg.Done()
			a := newClient("a"+string(rune('0'+n)), "Ann")
			b := newClient("b"+string(rune('0'+n)), "Bo")
			r.Join(a, "j")
			r.Join(b, "j")
			r.SetReady(a.id, true, "r")
			r.SetReady(b.id, true, "r")
			r.Start(a.id, "s")
			r.sync(func() {})
			for _, c := range []*fakeClient{a, b} {
				args, _ := json.Marshal(map[string]any{"positions": []int{0, 1}})
				r.Act(c.id, protocol.ActionData{Kind: protocol.AInspectMemory, Args: args}, "m")
			}
			r.sync(func() {})
		}(r, i)
	}
	wg.Wait()

	if m.Count() != rooms {
		t.Fatalf("manager holds %d rooms, want %d", m.Count(), rooms)
	}
	seen := map[string]bool{}
	for _, code := range codes {
		if seen[code] {
			t.Fatalf("duplicate room code %s", code)
		}
		seen[code] = true
		r, ok := m.Get(code)
		if !ok {
			t.Fatalf("room %s vanished", code)
		}
		info := r.Snapshot()
		if len(info.Players) != 2 || info.Phase != "turn_start" {
			t.Fatalf("room %s is in a bad state: %+v", code, info)
		}
	}

	// Isolation: acting in one room cannot touch another.
	first, _ := m.Get(codes[0])
	second, _ := m.Get(codes[1])
	beforePhase := second.Snapshot().Phase
	cur := first.Snapshot().Players[0].ID
	first.Act(cur, protocol.ActionData{Kind: protocol.ADrawCard}, "x")
	first.sync(func() {})
	if second.Snapshot().Phase != beforePhase {
		t.Fatal("an action in one room changed another")
	}
}

func TestManagerListsOnlyJoinablePublicRooms(t *testing.T) {
	m := NewManager(ManagerOptions{Clock: NewFakeClock(), Timers: testTimers(), Seed: 5})
	t.Cleanup(m.CloseAll)

	pub, _ := m.Create(protocol.CreateRoomData{Public: true, CardsPerPlayer: 4})
	priv, _ := m.Create(protocol.CreateRoomData{Public: false, CardsPerPlayer: 4})
	if len(m.List()) != 1 || m.List()[0].Code != pub.Code() {
		t.Fatalf("only the public room should be listed, got %+v", m.List())
	}
	if _, ok := m.Get(priv.Code()); !ok {
		t.Fatal("a private room must still be reachable by code")
	}
	if _, ok := m.Get("NOPE"); ok {
		t.Fatal("an unknown code should not resolve")
	}

	// Once it starts, a public room drops off the list.
	a, b := newClient("a1", "Ann"), newClient("b1", "Bo")
	pub.Join(a, "j")
	pub.Join(b, "j")
	pub.SetReady(a.id, true, "r")
	pub.SetReady(b.id, true, "r")
	pub.Start(a.id, "s")
	pub.sync(func() {})
	if len(m.List()) != 0 {
		t.Fatal("a running game should not be advertised as joinable")
	}
}

func TestManagerRejectsBadSettings(t *testing.T) {
	m := NewManager(ManagerOptions{Clock: NewFakeClock()})
	t.Cleanup(m.CloseAll)
	if _, err := m.Create(protocol.CreateRoomData{CardsPerPlayer: 5}); err == nil {
		t.Fatal("5 cards per player should be refused")
	}
	if _, err := m.Create(protocol.CreateRoomData{CardsPerPlayer: 4, MaxPlayers: 9}); err == nil {
		t.Fatal("9 players should be refused")
	}
}
