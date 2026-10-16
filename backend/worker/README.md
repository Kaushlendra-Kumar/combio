# COMBIO backend on Cloudflare

The multiplayer server, ported from Go to TypeScript so it can run on
Cloudflare's free Workers plan (no credit card). The browser client is
unchanged: same HTTP routes, same WebSocket protocol, same JSON on the wire.

```
src/
├── engine/      the rules: a line-by-line port of internal/engine
├── protocol.ts  wire format and action decoding (internal/protocol)
├── room.ts      one live game: seats, timers, reconnects (internal/room)
├── manager.ts   the room registry
├── auth.ts      dev tokens (internal/auth)
├── server.ts    HTTP routes and WebSocket sessions (internal/server, internal/ws)
└── index.ts     Cloudflare entry point and the Durable Object
```

## How it runs

Every request goes to a single Durable Object. It does the job the Go process
did: it holds every room in memory and runs every timer. It is single-threaded,
so a game is never touched by two requests at once.

Only the dev-auth tokens are written to storage (the object's built-in SQLite).
A saved browser session therefore keeps working after Cloudflare restarts the
object. Games in progress live in memory, just as they did in Go: a redeploy
ends them.

## Run it locally

```bash
cd backend/worker
npm install
npm run dev                 # http://localhost:8080, same port the Go server used
```

The frontend's default URLs already point at port 8080, so `npm run dev` in
`frontend/` connects without any configuration.

## Tests

```bash
npm run typecheck   # TypeScript 7
npm test
```

- `test/diff.test.ts` is the main safety net. It runs `go run ./cmd/difftrace`,
  which plays random games through the **Go** engine: legal moves, illegal
  moves, stale clicks, timers, disconnects and garbage arguments. It records
  every player's view and events after each step. The test replays the same
  inputs through the TypeScript engine and requires identical output, error
  messages included. It is skipped when Go is not installed.
- `test/server.test.ts` covers rooms, timers, pausing, reconnects, removal,
  rematch, frame handling, auth and CORS, using fake sockets and a fake clock.

## Deploy

### 1. Backend: Cloudflare (free, no card)

```bash
cd backend/worker
npx wrangler login      # opens the browser once
npx wrangler deploy
```

Wrangler prints a URL such as `https://combio-backend.<your-subdomain>.workers.dev`.
Check it with `<url>/healthz`, which should return `{"ok":true,"protocol":1}`.

### 2. Frontend: Vercel

Import the repository on Vercel, set the **Root Directory** to `frontend`, and
add two environment variables (Next.js reads them at build time):

| Name | Value |
|---|---|
| `NEXT_PUBLIC_API_URL` | `https://combio-backend.<your-subdomain>.workers.dev` |
| `NEXT_PUBLIC_WS_URL`  | `wss://combio-backend.<your-subdomain>.workers.dev/ws` |

### 3. Lock the API to your site

Once you know the Vercel URL, put it in `wrangler.jsonc`:

```jsonc
"vars": { "ALLOWED_ORIGINS": "https://your-app.vercel.app" }
```

Then run `npx wrangler deploy` again. Separate several origins with commas.

## Settings

All settings are optional and go under `vars` in `wrangler.jsonc`.

| Variable | Default | Meaning |
|---|---|---|
| `ALLOWED_ORIGINS` | empty (any) | browser origins allowed to call the HTTP API |
| `TURN_TIMER` | `60s` | time to take a turn |
| `MATCH_TIMER` | `5s` | matching window |
| `MATCH_EXTENSION` | `2s` | added per accepted match |
| `POWER_TIMER` | `15s` | time to use a power |
| `MEMORY_TIMER` | `60s` | memory phase |
| `RECONNECT_TIMER` | `30s` | time to come back before removal |
| `SEED` | random | fixed deal seed for every room |

## Where it differs from the Go server

- **Second connection on the same seat.** In Go, when a newer connection
  replaced an older one, the old socket's close handler later marked the seat
  disconnected. That knocked the new connection off and paused the game. Here
  only the connection currently holding the seat can disconnect it.
- **Tokens survive restarts**, as described above. In Go, a restart signed
  everyone out.
- **Dead-connection detection.** The Go server pinged every 25s and dropped a
  silent client after 60s. Cloudflare doesn't let a Worker send ping frames, so
  a client whose network vanishes without closing the socket is noticed when
  Cloudflare drops the connection. That can take longer than 60s. A normal
  close or tab exit is noticed immediately.
- **Shuffle algorithm.** Decks are shuffled with splitmix64 instead of Go's
  `math/rand`, so a given `SEED` deals a different (but equally fixed) game than
  `--seed` did in Go.

## Free plan limits

The Workers free plan has daily caps on requests and on Durable Object time.
A single always-on object fits inside them, and casual play with friends stays
well within them. Usage is visible in the Cloudflare dashboard.
