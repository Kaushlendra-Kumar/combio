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

docs/
├── phase-0-rules.md         the locked rules — source of truth
├── phase-1-engine.md        engine design and rule→component map
├── phase-2-cli.md           the local CLI
└── phase-3-multiplayer.md   the multiplayer backend
```

## Play it locally

```bash
cd backend
go run ./cmd/combio play              # pass-the-device, 2–8 players
go run ./cmd/combio scenario all      # every rule path, transcripted
go run ./cmd/combio replay --seed 7   # a whole bot game in about a second
```

## Run the server

```bash
cd backend
go run ./cmd/server --addr :8080

curl -X POST 'localhost:8080/auth/dev?name=You'   # DEV AUTH ONLY
# then connect a socket to ws://localhost:8080/ws?token=…
```

## Checks

```bash
gofmt -l .
go vet ./...
go test -race ./...
```

## Status

Phases 0–3 complete. Frontend (Phase 4) not started.
