# COMBIO — Phase 1: Game Engine

Destination in repo: `combio/docs/phase-1-engine.md`
Code: `combio/backend/internal/engine`

No WebSockets, no HTTP, no database, no auth, no frontend. The engine imports
only `errors`, `fmt`, `math/rand` and `strings` from the standard library.

---

## 1. File structure

```
backend/
├── go.mod                                  module github.com/kaushlendra/combio/backend
└── internal/engine/
    ├── card.go          119   CardID, Rank, Suit, DeckColor, Card, Value()
    ├── layout.go        161   ordered hand: insertion, gap collapse, versioned history
    ├── deck.go           91   deck construction + the Shuffler abstraction
    ├── state.go         362   Config, PlayerState, State, phases, piles, NewGame
    ├── action.go        195   the typed action union (client + system inputs)
    ├── event.go         271   the typed event union with per-event audience
    ├── errors.go         44   sentinel errors, incl. ErrStale and ErrImpossibleState
    ├── transition.go    906   Apply() and every handler — the state machine
    ├── scoring.go       151   DetermineWinners, finish(), Score()
    ├── view.go          166   PlayerView, CardHandle, ViewFor, FilterEvents
    ├── invariants.go     86   CheckInvariants, run after every transition
    ├── testhelper.go    203   ParseCardSpec, Scenario/BuildScenario (not _test.go:
    │                          Phase 2's simulator needs the same builder)
    └── *_test.go       2800   117 deterministic tests and subtests
```

Public surface is deliberately tiny:

```go
NewGame(cfg Config, ids []PlayerID, sh Shuffler) (State, []Event, error)
Apply(s State, a Action)                        (State, []Event, error)
(s *State) ViewFor(me PlayerID)                 PlayerView
FilterEvents(evs []Event, me PlayerID)          []Event
CheckInvariants(s *State)                       error
```

---

## 2. Rule → component map

| Phase 0 rule | Enforced by |
|---|---|
| §1 physical card identity, knowledge follows the card | `CardID` in `card.go`; every structure stores IDs only, rank is never an identifier |
| §1 two decks, distinguishable backs | `BuildDeck(2)` assigns `DeckRed`/`DeckBlue`; `CardHandle.Deck` publishes it |
| §1 matching is rank-only | `ownMatch` / `opponentMatch` compare `card.Rank == s.ActiveRank`; suit and deck colour are never read |
| §1 Ace = −1 | `Rank.Value()` |
| §2 deal, half-hand memory phase, one-shot reveal | `NewGame`, `inspectMemory`, `EvMemoryRevealed` (private audience) |
| §2 no persistent memory assistance | there is no "known" field anywhere in `State` or `PlayerView` — the engine cannot render one |
| §2 initial face-up discard is inert | `NewGame` pushes it with `ActiveRank = RankNone` and no pending power |
| §3 free insertion of a kept card | `Layout.Insert(pos, id)`, `pos ∈ 0..Len` |
| §3 gaps always close | `Layout.RemoveAt` splices; `invariants.go` forbids `NoCard` inside a layout |
| §3 swaps and transfers land in the exact vacated slot | `Layout.Swap`; `opponentMatch` inserts at the position returned by `RemoveCard` |
| §4 declare-or-draw, Option A / Option B | `PhaseTurnStart → PhaseDrawDecision` in `transition.go` |
| §4 Option A never arms a power | `keepDrawn` hard-sets `PendingPower = RankNone` |
| §5 one active rank per window, no chaining | `ActiveRank` is written only by `keepDrawn`/`discardDrawn`; matches use `buryDiscard` |
| §5 unlimited attempts, multiple matches | no per-player counter exists in `PhaseMatching` |
| §5 +2s per accepted match | `MatchExtensions` + `EvMatchingWindowExtended` (driver owns the clock) |
| §5 stale ≠ wrong | `Layout.Resolve(version, index)` → `CardID`, then `Contains`; missing → `ErrStale`, no penalty |
| §6 penalty tray, player-chosen slots, non-blocking | `PendingPenalty` + `InsertPenalty`; `closeWindow` auto-appends the remainder |
| §6 short draw never duplicates | `applyPenalty` breaks out when `drawOne` returns `NoCard` |
| §7 opponent targeting + hand-over | `opponentMatch`, guarded by `ErrNoCardToGive` / `ErrTargetEmpty` |
| §8 power only from a discarded drawn card | `PendingPower` is set solely in `discardDrawn` |
| §8 window first, power second, never raced | `closeWindow → resolvePower`; power actions in `PhaseMatching` return `ErrWrongPhase` |
| §8 zero-card fizzles, Queen optional, rest mandatory | `powerFizzles`, `skipPower` → `ErrPowerMandatory` |
| §9 reshuffle keeps the active top card | `State.reshuffle` |
| §9 no forced end; defensive fallback only | `draw` returns `impossible(...)` wrapping `ErrImpossibleState` with a full state dump, and mutates nothing |
| §10 two complete global rounds, historically | `RoundsCompleted` + `RoundPending` in `state.go`; `recordTurn` completes a round only on a real turn, `removePlayer` breaks and restarts one instead |
| §10 a forfeited turn is not a played turn | `endTurn(counted bool)`; removal passes `false` |
| §10 availability is timing-only | `declare` and `PlayerView.DeclareAvailable` never read a score |
| §10 final turns, declarer still targetable | `FinalQueue`; matching guards check `Active()`, not "is the declarer" |
| §11 strict-lower rule, co-winners on tie | `DetermineWinners` over an ordered `[]ScoreEntry` (deterministic, map-free) |
| §12 pause on disconnect, all timers frozen | `dispatch` returns `ErrPaused` for everything except connection actions (timers included) |
| §12 removal sinks cards to the discard **bottom** | `removePlayer` → `sinkToBottom`; nothing is ever deleted |
| §12 declaration survives the declarer's removal | `finish` passes `declarerEligible = false` |
| §12 last player standing wins | `endTurn`/`removePlayer` check `ActiveCount() <= 1` |
| §13 turn timeout = forced legal move | `timerExpired(TimerTurn)` → draw + discard + `EvPowerSkipped{"turn timeout"}` |
| §14 filtered per-player state | `ViewFor` + `CardHandle` (a type with no rank field) + `FilterEvents` |
| §17 card conservation | `CheckInvariants` invariant 1, asserted after **every** accepted transition |

---

## 3. Design decisions worth knowing

**Purity.** `Apply` clones the state, mutates the clone, runs `CheckInvariants`,
and only then returns it. Any error — rule violation, stale click, invariant
failure — returns the caller's **original** state. The engine never panics and
never half-applies. Two tests pin this: `TestApplyIsPureOnRejection` and
`TestApplyDoesNotAliasTheCallerState`.

**No clock.** Time enters as discrete `TimerExpired{Kind}` actions. The engine
has no goroutines, no `time` import, and no wall-clock dependency, which is why
every test is exact rather than flaky.

**Shuffler on the State.** The randomness source is stored as an unexported
field so the transition signature stays exactly
`Apply(State, Action) → (State, []Event, error)` as specified.

**Layout versions only where they matter.** Matching is the sole phase with
concurrent actors, so only the matching actions carry a `Version`. Everywhere
else the actor is the only mover. The layout keeps 32 past orderings; older than
that resolves as stale, which is always the safe answer.

**Events carry an audience.** `Audience() == nil` is public; otherwise it is an
explicit recipient list. `FilterEvents` is the only thing transport code needs
to call, and a private reveal cannot be broadcast by accident.

---

## 4. Verification pass (post-review)

Three implementation changes came out of the Phase 1 review:

1. **The COMBIO gate is now historical.** It was `every currently-active player
   has ≥2 turns`, which let a removal retroactively "complete" an unfinished
   round and unlock declaration early. It is now `RoundsCompleted >= 2`, where a
   round completes only through real turns; a removal breaks and restarts the
   round instead of finishing it, and a forfeited turn is not counted.
   This changes Phase 0 §10 — the doc has been updated to match.
2. **Sink order is now specified**: layout → privately drawn card → tray.
   It was layout → tray → drawn, which was deterministic but undocumented.
3. **A removed player is a stale target, not an "inactive" one.** Clicking one
   of their cards now returns `ErrStale` (free) instead of `ErrPlayerInactive`.

## 4b. The authority boundary created by exported `Base`

Phase 2 exported the embedded action `Base` so packages outside the engine can
construct actions. That does **not** weaken any rule: every check still runs
against the real state, so a claimed actor who is not allowed to do a thing
right now is rejected and nothing changes. `authority_test.go` pins this for
actor identity, turn ownership, layout versions, matching permissions, power
permissions, pause, and game-over, and includes a probe that throws all 18
action types from a non-existent player at every phase of a real game and
asserts each is rejected with the state byte-identical.

What the engine cannot do is tell **who sent** a message. It is a pure function
with no notion of connections, so it authorises the actor a message *claims* to
be. During a matching window every active player is legitimately allowed to
act, so a message naming another player is a legal move as far as the engine can
see.

**Requirement for Phase 3:** the transport must fill `Base.Player` from the
authenticated session and never from client-supplied data. On a shared local
device this is trivially satisfied. Over a socket it is the server's job, and
it is the one thing the engine cannot check for you.

## 5. Test results

```
gofmt -l            clean
go vet ./...        clean
go test -race ./... pass
go test -cover      engine: 88.8% of statements
```

Uncovered statements are `String()`/`Name()` boilerplate on action and card
types.

### Added in the verification pass

`unlock_test.go` — the two-round gate under every removal timing: baseline with
no removals, the review's own A→B→C→D scenario, an 8-case removal matrix
(before round 1, during round 1, during round 2, immediately before the removed
player's own turn, and a player who had already played that round), forfeited
turns not counting, removal during final turns not touching the gate, and the
gate never closing once open.

`verify_removal_test.go` — deterministic sink order for layout/drawn and
layout/tray; disconnect during a live matching window (rank, phase and extension
counter preserved across the pause, no window or reveal created by sinking, B's
cards stale-but-free afterwards, matching resumes); four penalty cards
accumulating across two failed attempts and appending in tray order; declarer
removed mid-final-turns (declaration stands, cards sink, only survivors scored,
co-winner rules apply); a queued player removed from the final queue; and the
current final-turn player removed without skipping the next.

`verify_purity_test.go` — a final-turn opponent match rewriting the declarer's
layout and score (7 → 12) so the declaration fails; a Jack-revealed card moving
through a Queen swap with no reveal residue in any view, event or state;
deterministic terminal paths for successful, failed, tied, zero-card and
co-winner endings; and two randomness-purity tests proving that a barrage of
rejected actions leaves a reshuffle-forcing draw byte-identical to the same draw
on an untouched state.

The 21 scripted games now assert their own coverage rather than assuming it, and
verify that each revealed hand sums to the score it was given. They cover
successful declarations, failed declarations, an Ace counted as −1 in a real
final score, matching during the final-turn sequence, and deck exhaustion. Paths
they do not reliably reach — declarer ties, zero-card declarers, co-winners,
negative totals — have deterministic tests of their own.

### What the suite covers

deck build ×1/×2 and 8-per-rank across two decks · deal for 2–8 players at 4/6/8
cards · memory phase count enforcement and privacy · draw privacy · Option A
insertion at every position · Option A never arming a power · Option B arming
one · no chaining on correct or wrong throws · multiple own matches in one
window · wrong-match net +1 · non-blocking tray insertion · auto-append at
window close · opponent targeting into the exact gap · failed targeting leaving
the victim untouched · three opponent targets down to zero cards · zero-card
players untargetable · click races resolving as stale with no penalty · an
older version still resolving to the right physical card · suit and deck colour
irrelevance · all four powers · every zero-card fizzle case · mandatory vs
optional powers · power timeout · a physical King tracked through Jack → Queen
swap → King swap and then targeted · the two-round global gate opening exactly
at round 3 · declaration timing restrictions · declaration never score-gated ·
final turns · declarer still targetable · all nine winner rows including
co-winners and Ace arithmetic · disconnect pause freezing timers · frozen drawn
card surviving a reconnect · removal sinking cards to the pile bottom ·
conservation across removal · sunk cards returning through the reshuffle ·
removal unblocking the gate · declaration surviving the declarer's removal ·
last player standing · pending power cancelled when its target leaves ·
short-draw penalties · the impossible-state fallback · view leak checks
(structural and JSON) · event audience filtering · and full random games at
every table size (2–8 players × 4/6/8 cards, 21 complete games) with invariants
asserted after every single transition and a reshuffle required in each.
