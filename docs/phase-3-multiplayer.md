# COMBIO — Phase 3: Real-Time Multiplayer Backend

Destination in repo: `combio/docs/phase-3-multiplayer.md`
(replaces the earlier `phase-3-server.md`)

The Phase 1 engine is unchanged as a rule system. Phase 3 adds sockets, rooms,
identity, timers and concurrency around it. No database, no frontend, no
matchmaking, no social features.

---

## 1. Architecture

```
WebSocket frame
     ↓  ws.Conn          transport only: upgrade, frames, keepalive, size cap
authenticated session    PlayerID fixed at connect time, from the token
     ↓  server.session   decode, version check, dispatch
room actor               one goroutine per room, serialized command channel
     ↓  protocol.BuildAction(kind, args, actor)   ← the only place Base is set
engine.Apply(State, Action) → (State, []Event, error)
     ↓
ViewFor(player) + FilterEvents(events, player)     ← per player, every time
     ↓
WebSocket frames
```

```
backend/
├── cmd/
│   ├── combio/        Phase 2 CLI
│   └── server/        flags, HTTP server, room reaper, graceful shutdown
└── internal/
    ├── engine/        Phase 1 — the sole gameplay authority
    ├── cli/           Phase 2
    ├── protocol/      versioned wire schema + action decoding + error codes
    ├── auth/          token → identity (DEV ONLY today; see §9)
    ├── room/          room actor, manager, injectable clock
    ├── ws/            transport
    └── server/        HTTP routes, session dispatch, shutdown
```

Only new dependency: `github.com/gorilla/websocket`.

---

## 2. Room actor model

One room is one goroutine.

```
client goroutines ─┐
timer callbacks    ─┼─► r.cmds (buffered chan func()) ─► loop ─► engine.Apply
manager            ─┘                                      │
                                                           └─► per-player Send
```

Every mutation of `engine.State` happens in that loop. There is no mutex on the
state: the channel *is* the serialization. Callers use `do` (fire and forget) or
`sync` (queue and wait).

A `serialGuard` is compiled in permanently — two atomic operations per action —
and counts any overlap. `Room.Stats().Conflicts` must always be zero, and the
tests assert it after every stress run.

`Client.Send` must never block the loop. The socket implementation drops a
client that cannot keep up rather than stalling the game for everyone.

The manager's mutex protects only its map, and it is never held while talking to
a room loop, so the two lock domains never meet.

---

## 3. WebSocket protocol

Versioned envelope. Frames whose `v` is not `1` are rejected with
`unsupported_version` and the socket stays open.

```jsonc
// client -> server        // note: no player id anywhere
{ "v": 1, "type": "action", "id": "c-7", "data": {"kind": "draw_card", "args": {}} }

// server -> client
{ "v": 1, "type": "state_update", "id": "c-7", "data": {"view": { ... }} }
```

| Client → server | data |
|---|---|
| `create_room` | `{public, cards_per_player, max_players}` |
| `join_room` | `{code}` |
| `reconnect` | `{code}`, or empty and the server finds your seat |
| `leave_room` · `start_game` · `ping` · `list_rooms` | — |
| `ready` | `{ready}` |
| `action` | `{kind, args}` |

| Server → client | data |
|---|---|
| `welcome` | `{version, player_id, name}` |
| `room_state` | `{code, public, status, cards_per_player, max_players, players[]}` |
| `player_joined` / `player_left` | `{player, name}` |
| `game_started` | `{cards_per_player, inspect}` |
| `state_update` | `{view}` — output of `ViewFor(you)` |
| `events` | `{events:[{name, data}]}` — output of `FilterEvents(evs, you)` |
| `game_finished` | `{view}` |
| `reconnect_required` · `room_list` · `error` · `pong` | — |

Action kinds mirror the engine one-for-one. Error codes are stable strings:
`stale`, `not_your_turn`, `wrong_phase`, `paused`, `game_over`, `declare_locked`,
`power_mandatory`, `target_empty`, `self_target`, `no_card_to_give`,
`no_pending_tray`, `bad_index`, `unknown_action`, `unsupported_version`,
`malformed`, `internal`.

**`stale` is special for the UI: it means "you lost a click race, nothing
happened, and you were not penalised."**

Every engine event carries lowercase json tags, and the `pub`/`priv` audience
embeds are tagged `json:"-"`, so engine plumbing never reaches the wire. A
reflection test fails the build if a new event field is added without a tag.

---

## 4. Authentication boundary

```
GET /ws?token=…
   ↓ auth.Authenticate(token) → Identity{PlayerID, Name}   BEFORE the upgrade
   ↓ session stores PlayerID for the connection's lifetime
   ↓ room.Act(session.PlayerID, data, correlationID)
   ↓ protocol.BuildAction(kind, args, actor)               only Base source
   ↓ engine.Apply — the engine decides if that actor may do that
```

Three properties hold it:

1. **The schema has no actor field.** `args` is a fixed struct, so
   `encoding/json` silently drops `player`, `actor`, `Base`, `player_id`.
2. **`BuildAction` takes the actor as a Go parameter** and is the only function
   in the server that constructs an `engine.Action`.
3. **Authentication precedes the upgrade**, so an unauthenticated socket never
   exists.

A *target* is legitimate client input — you choose whom to peek at — and the
engine validates it. Only the actor is server-controlled.

Reconnect inherits this: a token resolves to exactly one PlayerID, so one
player's token can never re-attach to another player's seat, however the request
is phrased.

---

## 5. Timer architecture

The engine holds no clock. The server owns every duration and feeds
`engine.TimerExpired{Kind}` through `Apply`.

| Timer | Default |
|---|---|
| Memory phase | 60s |
| Turn | 30s |
| Matching window | 5s |
| Successful-match extension | +2s |
| Power decision | 15s |
| Reconnect | 30s |

The memory timer is an addition to the Phase 0 list: without it a player who
never inspects would stall the table forever.

**Generation tokens.** One phase timer plus a counter. Start, stop, freeze,
resume and extend all bump the counter, and each callback carries the value it
was scheduled with; `firePhaseTimer(gen)` returns immediately unless it still
matches. A window's timer can never close the window that replaced it. Reconnect
timers have their own per-player generations.

**Scopes.** A timer restarts when `(kind, current player)` changes, so one 30s
timer spans `turn_start → draw_decision` but a fresh one starts when the turn
passes on.

**Freezing.** A pause records the remaining time, stops the timer and bumps the
generation; resuming reschedules the remainder. Reconnect timers deliberately
keep running during a pause — they *are* the pause countdown.

`Clock` is an interface; `FakeClock` advances on command, so every timer test is
deterministic rather than a sleep.

---

## 6. Reconnect lifecycle

```
socket drops → room.Disconnected(playerID)
   ↓ engine.Apply(PlayerDisconnected) → the game pauses (Phase 0 §12)
   ↓ freeze the phase timer, start the 30s reconnect timer
   ├─ same token returns → cancel timer, Apply(PlayerReconnected), resume with
   │                       the frozen remainder, push their private view
   └─ 30s elapses       → Apply(RemovePlayer): cards sink to the bottom of the
                          discard pile, rotation closes, play resumes
```

The server decides *when*; the engine decides *what*. No bots. Before the game
starts, a disconnect is simply a `leave`.

Re-attaching a socket that is already attached is treated as a replay and does
nothing — it does not emit a reconnect or run an engine transition.

---

## 7. Hidden information model

There is exactly one path from engine output to the wire:

```go
for _, seat := range r.seats {
    seat.client.Send(state_update{ View: r.st.ViewFor(seat.id) })
    if mine := engine.FilterEvents(evs, seat.id); len(mine) > 0 {
        seat.client.Send(events{ EncodeEvents(mine) })
    }
}
```

`engine.State` has no wire representation in `protocol`, so there is nothing to
marshal by accident. `CardHandle` has no rank field, so a face-down card can
only ever be `{position, id, deck}`.

Tests scan the **serialized frames**, not Go structs, for all five kinds of
private reveal: memory reveal, private draw, 10 peek, Jack peek, King peek. They
also check `error`, `room_state`, `state_update`, `events` and reconnect frames
for card identities, and for capitalised `engine.State` field names.

---

## 8. Current in-memory limitation

**Rooms and games live only in the server process's memory.**

A restart, crash, or redeploy destroys every active room and every game in
progress. Players will see their sockets close and will have to create a new
room. There is no persistence, no recovery and no migration between processes,
and there is no attempt to run more than one server process: two instances would
not share rooms.

This is accepted for the MVP. **Do not add a database merely to solve it.**
Render's free tier also sleeps an idle service, which has the same effect.

---

## 9. Development authentication

```
DEV AUTH → local development and testing only
```

`POST /auth/dev?name=You` mints an opaque random token bound to a
server-generated PlayerID. It is exposed only when the server is built with a
`*auth.DevTokens` provider, and it is **not production authentication**:

- anyone who can reach the endpoint can mint an identity
- there is no password, no email, no proof of anything
- tokens live in memory and die with the process
- there is no expiry, revocation or refresh

Everything upstream depends only on `auth.Authenticator`, so replacing it with
real authentication is a change to one package. That happens in a later phase.

---

## 10. Known limitations before production

| Area | Limitation |
|---|---|
| Persistence | In-memory only; a restart destroys live games (§8) |
| Scaling | Single process; rooms are not shared between instances |
| Auth | Dev tokens only (§9) |
| Rate limiting | None beyond a frame-size cap, a room cap and a bad-frame cap. No per-IP or per-action throttling |
| Abuse | No spectator mode, no reporting, no ban list |
| Origin checks | The upgrader accepts any origin; the deployment origin is not known yet |
| TLS | Terminated upstream; the server speaks plain HTTP |
| Observability | A stdlib logger only: no metrics, no tracing, no structured logs |
| Deck exhaustion | See below — a rule question, not a server bug |

### The deck-exhaustion finding — investigated

The earlier report suggested wrong-match cards were leaving circulation. That
was investigated card by card. **The reshuffle is correct and loses nothing.**

What the trace shows:

- A wrongly thrown card **does** reach the discard pile, buried under the active
  card, and **is** recovered by the next reshuffle. It can even come straight
  back out as one of the penalty cards if the penalty draw is what triggered the
  recycling.
- The active top card is never recycled, in one-deck and two-deck games alike.
- Card identity survives every reshuffle: same `CardID`, same rank, suit and
  deck colour.
- Total card count is constant, verified after every transition.

The cards that leave circulation are the **penalty cards**, and that is the rule
working as written. Per wrong own-match: one card goes from a hand to the pile,
two come out of the deck into that hand. Circulation (`deck + pile − 1`) falls
by exactly one; the player's hand grows by exactly one. A failed *opponent*
target buries nothing, so it drains two.

The accounting is now pinned by a test that asserts, after every wrong match:

```
circulation_after == circulation_before + 1 − penalty_cards_issued
```

If the reshuffle were dropping cards, that identity would fail. It holds every
time. A second test drives the pile dry and reports where everything ended up:
**all 19 cards present, 18 of them in players' hands.** Nothing is lost; there
is simply nothing left to recycle.

So the mechanical rules are consistent and correctly implemented, and no rule
was changed. What does not follow is the *claim* in Phase 0 §9:

> "The card count is constant for the whole game... This is what makes unbounded
> play safe: the pile can always refill."

A constant total does not imply a refillable pile, because hands are part of the
total and only ever grow through penalties. §9 asserts liveness that §6's
penalty rule does not provide. That is the inconsistency — in the stated
rationale, not in any individual rule — and it is **left for a Phase 0
decision** rather than patched here.

Reaching it needs roughly `deckSize − players×cards − 1` collective wrong
matches (43 for 2×4 on one deck, 39 for 8×8 on two), so it is rare in real play
but no longer hypothetical.

The server side is fixed and hardened: a rejected system action re-arms its
timer, so the room stays alive and cycling and players can still declare COMBIO
to end the game. Before this pass a failed forced move left the room with no
timer at all and it froze permanently.

## 11. Results

```
gofmt -l ./cmd ./internal   clean
go vet ./...                clean
go test ./...               274 tests/subtests, 0 failures, 0 skipped
go test -race ./...         pass, three consecutive clean runs
coverage (-coverpkg=./internal/...)   88.7% overall
  auth 98.7% · protocol 97.0% · engine 94.2% · cli 88.5%
  scenario 88.7% · room 87.3% · server 85.1% · ws 59.6%
```

`ws` is lowest because it is transport: its remaining branches are socket
failure paths the end-to-end tests do not provoke.
