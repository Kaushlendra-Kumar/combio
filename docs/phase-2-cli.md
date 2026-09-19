# COMBIO — Phase 2: Playable Local CLI

Destination in repo: `combio/docs/phase-2-cli.md`

Phase 2 makes COMBIO playable on one machine, with no networking, no database
and no frontend. It uses the Phase 1 engine unchanged.

---

## 1. Structure

```
backend/
├── cmd/combio/main.go             command dispatch: play / scenario / replay
└── internal/
    ├── engine/                    (Phase 1, unchanged except one export)
    ├── cli/
    │   ├── io.go        prompts, validation, screen clearing, TTY detection
    │   ├── render.go    every screen — built ONLY from PlayerView + events
    │   ├── session.go   setup, main loop, pass-the-device, turns, results
    │   ├── matching.go  the local matching-window protocol
    │   ├── powers.go    private prompts for 10 / J / Q / K
    │   ├── bot.go       the non-cheating bot
    │   ├── replay.go    headless deterministic bot game
    │   └── debug.go     the ONLY file that reads raw engine.State
    └── scenario/
        ├── run.go       scripted runner + transcript
        └── library.go   16 deterministic scenarios
```

One engine change was needed: the action structs embedded an unexported `base`,
so no package outside `engine` could construct an action. It is now the exported
`engine.Base`, with `engine.For(id)` as shorthand. Nothing else moved.

---

## 2. Commands

```bash
go run ./cmd/combio play                 # local game, pass-the-device
go run ./cmd/combio play --debug         # same, all hidden cards visible
go run ./cmd/combio play --seed 42       # deterministic deal
go run ./cmd/combio play --no-clear      # never clear (for piped input)

go run ./cmd/combio scenarios            # list the deterministic scenarios
go run ./cmd/combio scenario power-king  # run one, with a full transcript
go run ./cmd/combio scenario all         # run all 16

go run ./cmd/combio replay --seed 7                        # bot-only game
go run ./cmd/combio replay --seed 7 --players 8 --cards 6
```

Setup asks for player count (2–8), cards each (4/6/8), a name per seat, whether
each seat is a bot, and an optional seed.

---

## 3. Hidden information

The rule enforced in code: **`render.go`, `session.go`, `matching.go`,
`powers.go` and `bot.go` never read `engine.State`.** Every screen is built from
`state.ViewFor(playerID)`, and every private line comes from
`engine.FilterEvents(events, playerID)`. Even "what phase are we in" is read
through a view. `debug.go` is the single exception and is reachable only via
`--debug`.

`CardHandle` has no rank field, so a face-down card can only ever render as
`position:[deck colour]` — for example `0:[R] 1:[R] 2:[B]`. Deck colour is
public by design in two-deck games; rank never is.

The pass-the-device cycle:

```
   ┌ public board shown to the room
   │
   ├ ClearScreen()  ("\033[3J\033[H\033[2J" — wipes scrollback too)
   ├ "Pass the device to: Ann.  Press ENTER when only you can see the screen."
   ├ ClearScreen()
   ├ Ann's private board + her action
   ├ private reveal, then "press ENTER once you have memorised it"
   ├ ClearScreen()
   └ "Ann's turn is over. Pass the device on."
```

A revealed card is printed once and never again. Nothing stores it, no screen
redraws it, and the scrollback is wiped, so the only place it persists is the
player's head. When stdin is not a terminal the CLI stops clearing and echoes
input instead, so piped runs produce readable transcripts.

---

## 4. Matching windows, locally

Online, a window is a few seconds of everyone clicking at once. On one shared
device that is impossible, so the window is walked in a fixed order: **clockwise
starting with the player after whoever discarded, with the discarder last**
(they may act too). Each player, in private, may act as many times as they like
before passing:

```
=== MATCHING WINDOW — active rank is J ===

  [p] Place one of my 2 pending penalty cards
  [t] Target a card in someone ELSE's hand
  [o] Throw one of MY cards at the pile
  [n] Pass — I'm done with this window
```

Only the turn-taking is local. The actions sent to the engine are exactly
`AttemptOwnMatch`, `AttemptOpponentMatch`, `InsertPenalty` and
`TimerExpired{TimerMatchWindow}` — the same ones the future WebSocket server
will send. Nothing about the rules is special-cased for the CLI.

---

## 5. The bot

`BotBrain` receives an `engine.PlayerView` and, through
`engine.FilterEvents`, only the events addressed to that bot. Each bot has its
own brain, so one bot cannot learn another's cards; it is structurally unable to
see `engine.State`.

It remembers what it was legitimately shown (its memory-phase cards, its own
draws, its own Jack/King peeks) and uses that: it throws cards it remembers
holding at the active rank, strips opponent cards it remembers, and otherwise
takes an occasional blind punt. It keeps low draws and discards high ones, and
declares COMBIO on a gamble — it cannot see its own score, exactly like a
forgetful human.

`TestBotsCanOnlyRememberWhatTheyWereShown` plays a whole game while recording
which card ids appeared in each bot's filtered events, then asserts every
remembered card is on that ledger.

---

## 6. Scenarios

`scenario <name>` prints a header saying it is a development transcript, then a
step-by-step account with the opening hands, every
action, the events it produced (private ones labelled with their recipient),
the final hands and a pass/fail check. Invariants are asserted after every step.

```
matching-race          a race: the loser is stale, not wrong, and pays nothing
wrong-match            wrong throw = lose the card + 2 penalties, net +1
multi-match            two eights in one window; matching never chains
opponent-transfer      knock out a remembered card, hand one back into the gap
power-10 / -jack / -queen / -king
zero-card              10 and Queen fizzle, Jack still works, keep and bluff
combio-success         declarer strictly lowest
combio-failure         declaration fails, declarer out of contention
declarer-tie           a tie is not strictly lower
declaration-is-final   declaring settles the hand on the spot; nothing follows
disconnect             pause, removal, cards sink to the pile bottom
reshuffle              empty deck refilled, active card kept
unlock-interrupted     removals never retroactively complete a round
```

---

## 7. Results

```
gofmt -l            clean
go vet ./...        clean
go test -race ./... pass
go test ./...       189 tests/subtests, 0 failures
                    engine 88.8%  ·  cli 86.1%  ·  scenario 82.2%
```

The CLI verification pass added integration tests that drive real sessions with
scripted input: the pass-the-device flow for all five kinds of private reveal,
the local matching protocol, every interactive power prompt, draw decisions,
disconnect handling, the results screen, and the input guards. What remains
uncovered is prompt plumbing (`Line`, `NewIO`'s TTY probe) and one branch each
in a few helpers.

Manual runs completed:

1. **Full game** — 2 seats (human + bot), seed 42: setup, memory phase, two
   complete rounds, unlock, declaration, immediate reveal, winner.
2. **Matching** — seed 13: a correct own-card match, a wrong throw with the
   +2 penalty, a penalty card placed by position sight unseen, the rest
   auto-appended at close.
3. **Special power** — seed 13: Jack drawn, discarded, window resolved first,
   then a private peek at an opponent card.
4. **Zero-card** — `scenario zero-card`: 10 and Queen fizzle, Jack works, a card
   kept as a first card.
5. **Declaration and scoring** — from run 1, including a failed declaration.
6. **Disconnect** — the in-game simulator: pause, no reconnect, removal, cards
   sunk to the bottom of the pile.
