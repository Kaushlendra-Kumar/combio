# COMBIO — browser client

Next.js + TypeScript + Tailwind. Talks to the Go server over WebSocket; the
server owns every rule.

## Run it

Two terminals.

```bash
# 1. the game server
cd backend
go run ./cmd/server --addr :8080

# 2. the browser client
cd frontend
cp .env.example .env.local     # only needed once
npm install
npm run dev                    # http://localhost:3000
```

Open http://localhost:3000, pick a name, create a room, then open a **second
tab** and join with the code. Sessions live in `sessionStorage`, so each tab is
a separate player and you can play both seats yourself.

## Environment

| Variable | Meaning | Default |
|---|---|---|
| `NEXT_PUBLIC_API_URL` | HTTP origin of the Go server | `http://localhost:8080` |
| `NEXT_PUBLIC_WS_URL` | WebSocket endpoint | `ws://localhost:8080/ws` |

Nothing else hardcodes a host. Both are read in `lib/env.ts`.

## Checks

```bash
npm run typecheck        # tsc --noEmit
npm run lint             # eslint via next
npm test                 # unit tests (vitest + jsdom)
npm run test:integration # two clients against a real Go server
npm run build            # production build
```

The integration suite builds and runs the actual backend binary, so Go must be
on the PATH.

## Layout

```
app/            routes: home, /room/[code]
components/
  common/       connection status, banner, modal, timer
  lobby/        home screen, room lobby, player list
  game/         table, cards, panels
lib/
  protocol.ts   the wire format, typed from the Go source
  websocket.ts  one socket per session, reconnect with backoff
  game-state.ts the single reducer over server messages
  store.tsx     React binding + the action surface
  auth.ts       DEV auth only — replace this file for real accounts
  errors.ts     server error codes to human sentences
tests/
  unit/         protocol, client, reducer, rendering
  integration/  real server, real sockets, wire-level checks
```

## Notes

- **Development sign-in only.** `/auth/dev` mints a token for any name. There is
  no password and no account.
- **Games live in server memory.** Restarting the backend ends every game.
- The server owns all timers. This client renders state and never runs a clock
  that could act on its own.
