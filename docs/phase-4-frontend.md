# COMBIO — Phase 4: Browser Client

Destination in repo: `combio/docs/phase-4-frontend.md`

A Next.js client so people can play COMBIO in a browser against the Phase 3 Go
server. The engine, the room actor and the authority model are unchanged. One
additive protocol field was needed; §3 explains exactly why and what it cost.

---

## 1. Architecture

```
Browser
  ↓  React components (presentation only)
lib/store.tsx        one GameProvider per session
  ↓  actions, none of which carry an actor
lib/websocket.ts     one socket, typed send/receive, bounded backoff
  ↓
Go server → room actor → engine.Apply()
  ↓
ViewFor(me) + FilterEvents(events, me)
  ↓
lib/game-state.ts    a single reducer
  ↓
React state
```

```
frontend/
├── app/
│   ├── page.tsx              dev sign-in, then create/join
│   ├── room/[code]/page.tsx  the room route
│   ├── layout.tsx  globals.css
├── components/
│   ├── common/     ConnectionStatus, ErrorBanner, Modal, Timer
│   ├── lobby/      Home (create/join), RoomScreen, RoomLobby, PlayerList
│   └── game/       GameTable, Card, Table (hands/piles), Panels (turn/power/score)
├── lib/
│   ├── protocol.ts     the wire format, typed from the Go source
│   ├── websocket.ts    the connection manager
│   ├── game-state.ts   the reducer and the presentational selectors
│   ├── store.tsx       React binding and the action surface
│   ├── auth.ts         DEV auth, isolated so it can be swapped whole
│   ├── errors.ts       stable error codes to human sentences
│   └── env.ts          the only file that names a host
└── tests/
    ├── unit/           protocol, client, reducer, rendering
    └── integration/    two clients against a real Go server
```

Stack: Next.js 15.5.25 (App Router), React 19, TypeScript strict with
`noUncheckedIndexedAccess`, Tailwind. No state-management library: a `useReducer`
plus one context is enough, and adding Redux or Zustand for this would be
weight without a reason.

---

## 2. Protocol consumption

`lib/protocol.ts` mirrors `backend/internal/protocol` and the exported engine
view types field for field. It is hand-written rather than generated, and the
integration suite round-trips it against a real server, so drift shows up as a
failing test rather than a runtime surprise.

Two things the type system enforces:

- **A hidden card is a different type from a known one.** `CardHandle` is
  `{position, id, deck}` — there is no rank field to leak. `Card` (rank, suit)
  only ever appears where the server has decided you may see it: the discard
  top, your own drawn card, a private reveal, and the final reveal.
- **A client message has nowhere to put an actor.** `ClientMessage` and
  `ActionArgs` have no `player`, `actor` or `Base` field. `target` is there,
  because choosing whom to peek at *is* client input, and the server validates
  it.

`parseServerMessage` rejects non-JSON, non-objects, a missing or wrong `v`, an
unknown type and a non-object payload — each with a typed `ProtocolError` whose
`kind` distinguishes `malformed` from `unsupported_version`. A frame the client
cannot read produces a banner; it does not drop the session.

---

## 3. The one backend change

**Symptom.** A browser can only send what the view gives it. `PlayerView`
carried no layout version, so a real client had to send `version: 0`, meaning
"resolve against the current layout".

**Why that mattered.** Two players target the same opponent card. The first wins;
that card leaves and the winner's card slides into the vacated slot. The second
player's message, resolved against the *current* layout, is now judged against a
different physical card — a wrong match, worth **+2 penalty cards**. Phase 0 §5
says losing a legitimate race costs nothing.

**Proved before changing anything.** A room-level test drove exactly that race
with no version and asserted the loser was charged two cards.

**The change.** Additive, no engine rule touched:

```go
type OpponentView struct { ... Version uint64 `json:"version"` }
type PlayerView   struct { ... MyVersion uint64 `json:"my_version"` }
```

**Why it is safe.** A version is a revision counter. It says how often a hand has
changed, which the public event feed already reveals, and it names no card. A
test asserts it never looks like a card id and only moves when that layout moves.

**Tests added.** `TestRaceLoserWithoutAVersionIsJudgedAgainstTheNewCard` (the
documented bad path), `TestRaceLoserUsingTheViewVersionIsStaleAndFree` (the
corrected one, driven from the view exactly as a browser would),
`TestLayoutVersionCarriesNoCardIdentity`. Full Phase 3 suite re-run: 277 tests,
0 failures.

The client sends `target_version` and `give_version` from the view it rendered,
so a lost race comes back as `stale` and is shown as *"Too late — that card was
already taken."* with no penalty implied.

---

## 4. State flow

One reducer, `lib/game-state.ts`, handles every server message. Components read
state and dispatch actions; none of them touches a raw frame.

| Message | Effect |
|---|---|
| `welcome` | records the session the server assigned |
| `room_state` | replaces the roster wholesale |
| `player_joined` / `player_left` | a feed line |
| `game_started` | enters the game, stores the inspect count |
| `state_update` | **replaces** the view wholesale |
| `events` | feed lines, plus queued private reveals |
| `game_finished` | final view for the scoreboard |
| `reconnect_required` | flags a re-join |
| `error` | one banner, de-duplicated by code |
| `pong` / `room_list` | no state change |

**Replacement, not merging.** `state_update` swaps the whole view. That is the
mechanism that stops a card the server has stopped showing us from surviving in
React state — the case the brief specifically calls out. A unit test shrinks a
hand from two cards to one and asserts the departed card's id is nowhere in
state.

Private reveals are queued, shown once in a modal, and dropped on
acknowledgement. A test confirms the revealed rank is not retained anywhere
afterwards. Nothing in this client stores what you were shown; the game is a
memory game and the UI offers no memory aid.

The selectors (`canAct`, `isMatchingOpen`, `myTurnPhase`) read facts the server
already sent — is it my turn, is a rank active, which power is pending. None of
them decides legality. There is no `isValidMatch`, no `calculateScore`, no
`canDeclareCombio`: the COMBIO button renders when `declare_available` is true
and nothing else.

---

## 5. Private-information handling

Three layers, checked at each:

1. **Type.** `FaceDownCard` accepts a `CardHandle`. There is no rank in its
   props, so there is nothing to render, label or serialise.
2. **DOM.** Tests scan rendered text and every `aria-label`, `title` and `alt`
   for suit glyphs and "N of" patterns. (Scanning raw `innerHTML` is useless
   here — Tailwind class names contain `A`, `K` and so on. The first version of
   the test made that mistake and was corrected.)
3. **Wire.** The integration suite records the raw frames each client receives
   and asserts that Ann's private cards never appear in Bo's bytes, that no
   `private_reveal` addressed to Ann reaches Bo, that no frame contains
   `"Deck":`, `"Cards":` or `"shuffler"`, and that no `state_update` carries
   more than two card identities.

Deck colour *is* public and is rendered — with two decks that is how players
track physical cards, and it never hints at rank.

---

## 6. Reconnect behaviour

`GameClient` reconnects with bounded backoff (`500ms → 8s`, then it stops). It
never opens a second socket: `connect()` is idempotent, which matters because
React Strict Mode mounts every component twice. `close()` is terminal — a closed
tab cannot leave a retry loop spinning — and it nulls every listener.

On the room route, the client sends `join_room` once the socket is open.
`join_room` doubles as reconnect on the server, so a refresh or a dropped
connection puts you back in your seat with your private view restored, including
a card you were holding mid-turn.

While disconnected the UI shows a "Connection lost / your game is paused" modal
and states plainly that the server owns the countdown. **There is no client-side
30-second timer.** If the server drops the player, the next authoritative update
says so.

---

## 7. Timers

The Phase 3 protocol carries no deadline field, so this client displays no
countdown. `Timer.tsx` accepts an optional `deadlineMs` and renders an idle
label without one.

That is a deliberate choice, not an oversight. A client-side estimate would
become a second, wrong clock the moment a disconnect froze the real one, and the
brief is explicit that the server performs every transition. Adding a deadline
to the protocol would be a second backend change that is not needed to play, so
it was not made. If one is ever added, pass it to `Timer` — even then the
component only displays; it never acts on reaching zero.

---

## 8. Environment and local development

| Variable | Meaning | Default |
|---|---|---|
| `NEXT_PUBLIC_API_URL` | HTTP origin of the Go server | `http://localhost:8080` |
| `NEXT_PUBLIC_WS_URL` | WebSocket endpoint | `ws://localhost:8080/ws` |

Read once in `lib/env.ts`; nothing else names a host, so the same build points
at a local or deployed backend.

```bash
# terminal 1
cd backend && go run ./cmd/server --addr :8080

# terminal 2
cd frontend && cp .env.example .env.local && npm install && npm run dev
```

Checks: `npm run typecheck`, `npm run lint`, `npm test`,
`npm run test:integration` (builds and runs the real Go binary, so Go must be on
the PATH), `npm run build`.

---

## 9. Manual multiplayer test procedure

1. Start the backend, then the frontend.
2. Open `http://localhost:3000`, enter a name, **Continue**.
3. Create a room. Note the four-letter code.
4. Open a **second tab** (sessions are per-tab, so this is a second player).
   Enter a different name, paste the code, **Join**.
5. Both tabs: **I'm ready**. The host's **Start game** enables when the server
   sets the room to `READY`.
6. **Memory phase** — each tab picks half its cards and sees them once. Confirm
   the other tab is not shown them.
7. **A turn** — the player on turn draws. Confirm only that tab sees the card,
   and the other sees "drew a card" with no value. Keep it (pick a card to swap
   out, then a slot) or discard it.
8. **Matching** — after a discard, both tabs show `MATCH n!`. Tap one of your
   own cards to throw it, or an opponent card to target it and choose what to
   hand over. A wrong guess adds a pending penalty tray; place the cards or let
   the window close.
9. **Powers** — discard a drawn 10, J, Q or K and resolve the panel.
10. **Race** — with three tabs, have two target the same opponent card at once.
    The loser sees *"Too late — that card was already taken."* and takes no
    penalty.
11. **Reconnect** — refresh a tab mid-game. The others show "paused"; the
    refreshed tab returns to its seat with its cards and any held card intact.
12. **COMBIO** — the button appears only once the server unlocks it. Declare,
    watch the final turns, and read the result screen.

---

## 10. Known limitations

| Area | Limitation |
|---|---|
| Countdown | Not displayed; the protocol carries no deadline (§7) |
| Auth | Development sign-in only. No password, no account, no expiry |
| Persistence | Games live in server memory; a backend restart ends them |
| Origin | The Go upgrader accepts any origin — a later hardening phase |
| Targeting | Every opponent card is clickable during a window. The server deliberately does not tell you what you remember, so targeting is from memory, as the rules intend |
| Animation | Minimal (a pop and a slide). Nothing waits on an animation before applying an authoritative update |
| Spectating | None |
| Mobile | Usable and free of horizontal overflow; desktop is the primary target |

---

## 11. Results

```
Frontend
  tsc --noEmit          clean
  next lint             clean
  npm test              48 unit tests, 0 failures
  npm run test:integration   7 tests against a real Go server, 0 failures
  npm run build         passes
  no `any` anywhere in lib/, components/, app/ or tests/

Backend (regression after the additive change)
  gofmt -l .            clean
  go vet ./...          clean
  go test -race ./...   pass
  go test ./...         277 tests/subtests, 0 failures, 0 skipped
```
