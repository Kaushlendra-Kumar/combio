# COMBIO

A 2–8 player memory card game. You cannot see your own cards; you have to
remember them. Lowest total wins, Ace is −1.

## Where things are

```
backend/
├── cmd/combio/     local pass-the-device CLI + deterministic scenarios
├── cmd/server/     real-time multiplayer WebSocket server
└── internal/
    ├── engine/     the rules — pure, deterministic, the sole authority
    ├── cli/        terminal UI (Phase 2)
    ├── protocol/   versioned WebSocket schema
    ├── auth/       token → identity (DEV ONLY today)
    ├── room/       one goroutine per live game
    ├── ws/         socket transport
    └── server/     HTTP routes and session dispatch

frontend/          Next.js browser client (Phase 4)
├── app/           routes: home, /room/[code]
├── components/    lobby and game table
├── lib/           protocol, socket client, reducer, dev auth
└── tests/         unit + two-client integration against the real server

docs/
├── phase-0-rules.md         the locked rules — source of truth
├── phase-1-engine.md        engine design and rule→component map
├── phase-2-cli.md           the local CLI
├── phase-3-multiplayer.md   the multiplayer backend
└── phase-4-frontend.md      the browser client
```

## Play it locally

```bash
cd backend
go run ./cmd/combio play              # pass-the-device, 2–8 players
go run ./cmd/combio scenario all      # every rule path, transcripted
go run ./cmd/combio replay --seed 7   # a whole bot game in about a second
```

## Play it in a browser

Two terminals:

```bash
cd backend  && go run ./cmd/server --addr :8080
#   (add --origins https://your.site to restrict browser origins)
cd frontend && cp .env.example .env.local && npm install && npm run dev
```

Open http://localhost:3000, pick a name, create a room, then open a **second
tab** and join with the code. Each tab is its own player, so you can test
multiplayer on one machine.

Dev sign-in only: `/auth/dev` mints a token for any name. No password, no
account. Real authentication comes later.

## Checks

```bash
# backend
cd backend
gofmt -l . && go vet ./... && go test -race ./...

# frontend
cd frontend
npm run typecheck && npm run lint && npm test && npm run build
npm run test:integration   # boots the real Go server; needs Go on PATH
```

## Status

Phases 0–4 complete: rules, engine, local CLI, multiplayer backend, browser
client. Games live in server memory and authentication is development-only.
