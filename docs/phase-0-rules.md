# COMBIO — Phase 0: Consolidated Rule Decisions

**Status:** locked. Revised once during Phase 1 verification — see §10 (unlock gate) and §12 (sink order).
**Scope:** this document is the single source of truth for game rules. The Go engine implements exactly this and nothing else. Where this document and the original prose rules disagree, this document wins.

Destination in repo: `combio/docs/phase-0-rules.md`

---

## 1. Card model

| Property | Decision |
|---|---|
| Identity | Every physical card is a unique `CardID`. Rank and suit are attributes of a card, never its identity. |
| Decks | 2–4 players → 1 deck (52). 5–8 players → 2 decks (104). |
| Deck marking | Two decks have **visually distinct backs** (Deck A = red, Deck B = blue). Deck colour is **public information** for every card in play, including face-down cards. |
| Duplicates | `8♠` from Deck A and `8♠` from Deck B are two different physical cards. Both match active rank 8. |
| Matching | Rank only. Suit and deck colour are irrelevant to matching. |
| Knowledge | Knowledge attaches to the physical card, not to a position or an owner. It survives swaps, transfers and ownership changes. Knowledge is held in the player's head only — the server tracks reveals solely to authorise them, never to display them. |

### Values

| Card | Value |
|---|---|
| Ace | **−1** |
| 2–9 | face value |
| 10 | 10 |
| Jack | 11 |
| Queen | 12 |
| King | 13 |

Score = sum of all cards remaining in the layout. Lower is better. Negative scores are normal. Zero cards = 0, which loses to any player holding an Ace.

---

## 2. Setup and memory phase

1. Host selects 4, 6 or 8 cards per player.
2. Deal face-down to every player, left to right.
3. Turn one card face-up as the initial discard. It sets **no** active rank and triggers **no** power.
4. Memory phase: each player privately selects exactly half their cards (2 / 3 / 4).
5. All selected cards are revealed to that player **simultaneously for 15 seconds**, then hidden permanently.
6. No persistent memory assistance anywhere in the UI, ever. No "known" badges, no history panel, no highlight, no card-count-by-rank helper.

---

## 3. Layout rules

- Positions are an **ordered list**, not fixed slots.
- A player may never drag or reorder cards at will.
- Order changes only through legitimate actions: keeping a drawn card (free insertion), losing a card, receiving a card, receiving penalty cards, swaps.
- **Gaps always close.** When a card leaves a layout, the remaining cards collapse preserving relative order. Empty slots never persist as memory markers.
- Layout size is unbounded upward (penalties) and can reach 0.

### Where incoming cards land

| Event | Position |
|---|---|
| Keep drawn card (Option A) | **Anywhere the player chooses**, including the gap just vacated |
| Penalty cards (×2) | **Player chooses both slots and the order**, sight unseen |
| Card received via opponent-match transfer | Exactly the gap left by the removed card. Receiver has no choice. |
| Queen swap | Each card takes the exact position vacated by the card it replaced |
| King swap | Same as Queen |

---

## 4. Turn structure

Clockwise. On your turn, in order:

1. **Declare COMBIO** — allowed only here, only before drawing, only if the global unlock condition is met. Declaring ends your turn and you take no further turns.
2. Otherwise **draw one card** from the draw deck. Only you see it. Once you have drawn, declaration is impossible for this turn.
3. Choose exactly one:

**Option A — Keep the drawn card**
- Choose one existing card to remove.
- Removal + insertion of the drawn card resolve **atomically**.
- The removed card is revealed, goes to the discard pile, and **sets the active rank**.
- The removed card **never** triggers a special power, even if it is 10/J/Q/K.
- Matching window opens.

**Option B — Discard the drawn card**
- The drawn card goes to the discard pile and sets the active rank.
- Matching window opens.
- If it is 10/J/Q/K, the power resolves **after** the matching window closes.

---

## 5. Matching system

- A matching window opens on **every game discard** (Option A removal, Option B discard) and on nothing else.
- The card that opened the window sets the **active rank** and stays the visible top discard for the whole window.
- **Windows never chain.** Cards thrown during a window — correct or incorrect — are buried in the pile and do not change the active rank or open a new window.
- Every player may participate, including the player whose turn it is.
- **Multiple successful matches per player per window are allowed**, own cards and opponent cards, in any mix.
- **Unlimited attempts per player per window**, including repeated wrong attempts.
- Swaps (Queen/King) and transfers never open windows.

### Timing

- Window = **5 seconds**.
- Each **accepted successful match extends it by 2 seconds**.
- Wrong attempts do **not** extend it.

### Own-card match

Target one of your own cards. Correct → discarded, gap closes. Wrong → penalty (§6), card stays in your layout, revealed to nobody.

### Opponent-card match (§7)

### Race resolution

- All attempts resolve in **server receive order**.
- Every client action carries the `layoutVersion` it was rendered against. The server resolves `(player, index, version)` back to a `CardID` using that version's snapshot.
- If the resolved `CardID` is still in that player's layout → the action is **valid**, even if the index has since shifted. Physical card identity wins over index.
- If the resolved `CardID` has already left play (matched, swapped away, or sunk because its owner was removed) → **stale**. Rejected with **no penalty**. Targeting a removed player at all is stale for the same reason.
- Wrong rank on a card that is genuinely still there → **wrong attempt**, penalty applies.

---

## 6. Penalties

A wrong attempt costs **+2 cards** drawn from the deck, face-down, never inspectable by the receiver.

- The penalised player chooses both insertion positions and the insertion order, sight unseen.
- Penalty cards are ordinary cards once inserted: matchable, swappable, scoreable.
- A zero-card player can be penalised and thereby re-enter with 2 cards.
- If the draw deck is short, reshuffle per §9 first. No card ever leaves the game permanently, so the reshuffle supplies them in every non-pathological case. If fewer than 2 cards physically exist outside layouts, issue as many as exist. Never duplicate a card.

---

## 7. Opponent-card matching

If you believe you know the identity of an opponent's physical card and it matches the active rank, you may target it.

**Success:** the opponent's card is discarded and buried. You must **give them one of your own cards** (your choice which, known or unknown) and it lands in the exact gap. Your layout collapses. Net: you −1 card, they ±0.

**Failure:** their card stays, unrevealed. You take +2 penalty cards. Active rank unchanged. You may attempt again.

**Constraints:**
- You must have at least one card to give. A zero-card player cannot use this mechanic at all.
- Multiple opponent targets per window are legal, including down to your last card (§19 of the spec: remove B's 8, C's 8, D's 8 → you reach 0 cards).
- Players with zero cards cannot be targeted by anything: not Jack, King, Queen, nor opponent-matching.

---

## 8. Special powers

Only a **drawn card discarded directly** (Option B) activates a power. Order is strict and never raced:

```
discard → active rank set → matching window (5s+) → window closes → power resolves
```

The special card remains the top discard after resolution.

| Card | Power | Mandatory? | Zero-card user |
|---|---|---|---|
| **10** | Peek at one of **your own** cards. Any own card — the server does not police whether you already know it. | Mandatory if you have ≥1 card | Fizzles |
| **Jack** | Peek at one card of another player. Private to you. | Mandatory if a legal target exists | Works normally |
| **Queen** | Blind swap: one of your cards ↔ one opponent card. Neither is revealed. | **Optional** — may be skipped | Fizzles (nothing to give) |
| **King** | Peek at one opponent card privately, then swap it with one of your cards **or** cancel | Peek mandatory, swap optional | Peek only, cannot swap |

- Private reveals display for **5 seconds**, then hide forever.
- If no legal target exists (e.g. every opponent has 0 cards), the power fizzles silently after the window.
- Power decision timeout = **15 seconds** → the power is skipped entirely, including the "mandatory" 10 and Jack.

---

## 9. Draw deck exhaustion

When the draw deck empties:

1. Keep the current top/active discard card in place.
2. Take **every** other card in the discard pile — buried matches, wrong attempts, and cards deposited at the bottom by disconnected players (§12).
3. Shuffle them into the new draw deck.
4. Play continues.

**No round limit and no forced end.** The game runs until someone declares COMBIO. Reshuffling is expected and unbounded.

**The card count is constant for the whole game.** Nothing is ever destroyed or withdrawn — a disconnect moves cards, it does not delete them. This is what makes unbounded play safe: the pile can always refill.

*Engine safety net (not a game rule):* if a draw is required and the draw deck is empty **and** the discard pile holds nothing but the active top card, the draw yields nothing and the turn passes. This state is effectively unreachable — every turn deposits at least one card into the discard pile, and every wrong attempt deposits one more — but the engine asserts rather than panics.

---

## 10. COMBIO declaration

### Unlock condition

Declaration is locked for the **first two complete global rounds**. With 4 players:

```
Round 1:  A → B → C → D
Round 2:  A → B → C → D
Round 3:  A may declare, and everyone thereafter
```

Formally: declaration is available once **two global rounds have actually been completed**. A round completes only when every player who owed a turn in it has really taken one.

**A removal never completes a round.** If a player leaves still owing a turn, the round in progress is discarded as *broken* and a fresh round starts with the remaining players — the counter does not advance. This means a disconnect can never retroactively turn an unfinished round into a finished one, and can never unlock COMBIO early. A player removed mid-turn forfeits that turn; a forfeit is not a played turn.

Worked example. Round 1 completes with A→B→C→D. In round 2, A and B play, then C and D both disconnect and are removed. A and B have each personally completed two turns, but **only one round was ever completed**, so COMBIO stays locked. The broken round restarts with A and B; once both have played it again, the gate opens.

The counter only ever increases, so once the gate is open, later removals can never close it.

### Availability is timing-only

The COMBIO button's enabled state depends **exclusively** on: it is your turn, you have not yet drawn, and the global unlock has passed. It **never** consults your real score. There is no score threshold. Declaring on a bad hand is the intended risk.

### Restrictions (unchanged)

Cannot declare: after drawing, during another player's turn, during a matching window, after matching outside your turn, or during power resolution.

### After declaration

**The hand ends immediately.** Every layout is revealed, scores are totalled and
the winner is decided. Nobody takes another turn, no matching window opens, and
no card moves after the declaration.

This replaces the earlier rule, which gave every other active player one final
turn before the reveal. That version was in the original rules document and in
§10 of this one; it is superseded. The consequence is deliberate: the score you
declare on is the score you are judged by, so a declaration is a bet on the
table exactly as it stands.

What that changes, in plain terms:

- A declaration cannot be sabotaged. Opponents can no longer strip the
  declarer's cards or hand them a King on the way out.
- Declaring is stronger, because nobody can react. It still costs you the hand
  if you tie or lose (§11), so it is a real risk, not a free move.
- "The declarer remains targetable" no longer applies — there is no window in
  which to target anyone.

---

## 11. Scoring and winner

```
declarerScore  = sum(declarer layout)
othersMin      = min(score of every other active player)

if declarerScore < othersMin:
        winners = [declarer]                       // strictly lower than everyone
else:
        declaration fails
        declarer is removed from winning consideration entirely
        winners = every other active player whose score == othersMin
```

Ties among remaining players produce **co-winners**. The failed declarer is always placed last regardless of raw score.

### Required test cases

| Scores | Result |
|---|---|
| D=−2, B=−1, C=0 | D wins (strictly lowest) |
| D=0, B=−1, C=4 | D fails → B wins |
| D=−2, B=−2, C=1 | D fails on tie → B wins |
| D=8, B=8, C=11, E=16 | D fails on tie → B wins |
| D=3, B=−1, C=−1, E=5 | D fails → **B and C co-win** |
| D=4, B=4, C=4 | D fails → **B and C co-win** |
| D=2, B=2 (2 players) | D fails → B wins |
| D=5, B=0 (zero cards), C=−1 | D fails → C wins (−1 beats 0) |
| D=−3 (3 Aces), B=−2, C=−1 | D wins |

---

## 12. Disconnect and pause

| # | Situation | Behaviour |
|---|---|---|
| 1 | Any disconnect | **The whole game pauses.** All timers freeze: turn, matching window, power decision. A 30s reconnect countdown starts. |
| 2 | Reconnect within 30s | Restore the exact frozen state, including a privately drawn card still in hand. Hidden info stays hidden. Frozen timers resume with their remaining time. |
| 3 | No reconnect | Player is removed from the active game. Everything they hold is removed from play **and placed face-down at the bottom of the discard pile** — never revealed, no matching window, no power activation. They are inert discarded cards and re-enter play through the normal reshuffle (§9). |
| 3b | Sink order | Deterministic, bottom-first: **layout cards in current layout order → the privately drawn card, if any → pending tray cards in tray order.** Physical identity is preserved throughout; nothing is created or destroyed. |
| 3c | Their cards afterwards | Untargetable. A click on one resolves as **stale** (free, no penalty), never as a wrong match. |
| 4 | Removed player was the declarer | The declaration **stands**. Final turns continue. The removed declarer cannot win. Scoring proceeds among the remaining players. |
| 5 | Action was targeting the disconnected player's card | Frozen. On reconnect it continues. On removal it is cancelled with **no penalty**, and the acting player is returned to a valid action state. |
| 6 | One active player remains | They win automatically; game ends. |
| 7 | Removed player | Out of that game. No spectator mode in MVP. |
| 8 | Multiple simultaneous disconnects | Each gets its own 30s window; the game resumes only when all are resolved. |
| 9 | Bots | None. Ever. Timeouts produce forced legal moves, not play. |

---

## 13. Timers (fixed in MVP, not room-configurable)

| Timer | Value |
|---|---|
| Turn | 30s |
| Matching window | 5s |
| Successful-match extension | +2s per accepted match |
| Power decision | 15s |
| Private reveal display | 5s |
| Memory-phase reveal | 15s |
| Reconnect window | 30s |

**Turn timeout behaviour:** if the player has not drawn, auto-draw and auto-discard that card. Any resulting power is skipped. The matching window opens normally for everyone else. This is a forced legal move, not bot play.

---

## 14. Information security

The server holds the complete state. Each client receives a filtered view containing only:

- Its own layout as opaque card handles: `{position, cardId, deckColor}` — **never rank or suit**
- Every opponent's layout as the same opaque handles, plus card counts
- The discard pile top (full identity) and pile size
- Draw deck count
- Whose turn it is, phase, active rank, timers
- Its own private reveals, transiently, when a rule authorises them

Never sent to any client: deck order, unknown card identities, another player's drawn card, another player's private reveal, penalty card values, the outcome of a blind Queen swap.

Server validates everything. A client claiming "that card is an 8" is checked against the real card; wrong → penalty.

---

## 15. Resolved decisions (formerly open)

### O1 — Deck exhaustion → **resolved: no forced end, no card destruction**

Disconnected players' cards go to the bottom of the discard pile instead of leaving the game, so the total card count is invariant and the pile can always refill. There is no `ForcedDeckExhaustion` state and no exhaustion-triggered game end. See §9 and §12.

Rationale: a disconnect among friends should not shrink the deck or abruptly end everyone's game.

### O2 — Penalty insertion during a live window → **resolved: non-blocking**

The 2 penalty cards sit in a pending tray. The penalised player may keep attempting matches while they are there. They must place both before the window closes; anything unplaced is auto-appended to the end of their layout when the window shuts.

Cards in a pending tray are not part of the layout: they cannot be targeted, matched, swapped or peeked by anyone, and they do not count toward that player's card count for targeting purposes.

### Adopted defaults

- A card that is in a pending penalty tray cannot be targeted, matched, swapped or peeked.
- The draw pile renders a single generic back (its top card's real deck colour is not exposed), while every card in a player's layout and the discard pile shows its true deck colour.
- Deck colour of a face-down card is public and shown to all players — that is the entire point of two-toned decks.
- A player at 0 cards may declare on their turn like anyone else, scoring 0.

---

## 16. State machine

### Top-level phases

```
Lobby
  → MemoryPhase
  → RoundLoop  ←──────────────┐
  → FinalTurns  (on declare)  │
  → Scoring                   │
  → Finished                  │
                              │
Paused  ── overlay on any phase, freezes all timers, resumes to the exact prior node
```

### Per-turn machine

```
TurnStart(player)
  ├── Declare            [guard: !hasDrawn && globalUnlock]     → FinalTurns
  └── DrawCard                                                   → AwaitDrawDecision

AwaitDrawDecision  (private card held by actor only)
  ├── KeepDrawn{removeIdx, insertIdx}
  │        atomic: remove → insert → set activeRank → bump layoutVersion
  │        pendingPower = none                                   → MatchingWindow
  └── DiscardDrawn
           set activeRank
           pendingPower = rank ∈ {10,J,Q,K} ? rank : none        → MatchingWindow

MatchingWindow  (open to ALL active players, 5s, +2s per accepted match)
  ├── AttemptOwnMatch{idx, version}
  │        stale → reject, no penalty
  │        rank match → remove, collapse gap
  │        else → PenaltyPending(+2)
  ├── AttemptOpponentMatch{target, idx, version, giveIdx}
  │        guard: actor has ≥1 card; target has ≥1 card
  │        stale → reject, no penalty
  │        rank match → remove target card, transfer giveIdx into that exact gap
  │        else → PenaltyPending(+2)
  ├── InsertPenalty{slotA, slotB}   (non-blocking; pending tray)
  └── TimerExpired                  → auto-place any pending penalties → PowerResolution

PowerResolution
  ├── none                                                        → EndTurn
  ├── Peek10{ownIdx}            private reveal 5s                 → EndTurn
  ├── JackPeek{target, idx}     private reveal 5s                 → EndTurn
  ├── QueenSwap{ownIdx, target, idx} | SkipQueen                  → EndTurn
  ├── KingPeek{target, idx}     private reveal 5s                 → AwaitKingDecision
  │        └── KingSwap{ownIdx} | CancelKingSwap                  → EndTurn
  └── TimerExpired / no legal target → fizzle                     → EndTurn

EndTurn
  turnsCompleted[player]++
  recompute globalUnlock = ∀ active p: turnsCompleted[p] ≥ 2
  advance clockwise → TurnStart      (or next FinalTurns seat, or Scoring)
```

### FinalTurns

Each non-declarer takes exactly one pass through the per-turn machine with the `Declare` branch disabled. Matching windows stay fully live for everyone including the declarer. After the last seat → `Scoring`.

---

## 17. Engine architecture and invariants

**Purity.** The engine is `Apply(State, Action) → (State, []Event, error)`. No clock, no network, no goroutines, no I/O. Time enters only as explicit `TimerExpired{kind}` actions, which makes every test deterministic.

**Injected randomness.** A `Shuffler` interface, with a `StackedDeck` test helper that pins exact hands, deck order and the initial discard.

**Invariants asserted after every transition:**

1. Card conservation: `deck + discard + all layouts + pendingTrays + drawnCard` = the full deck constant, **at every single transition, for the entire game**. There is no removal bucket. A disconnect is a move from a layout into the discard pile, nothing more.
2. No `CardID` appears twice anywhere.
3. `activeRank` is set only by a game discard; matches, wrong throws, transfers and swaps never touch it.
4. Every player's `layoutVersion` increments on any mutation to that layout.
5. No event carries a rank to a player not authorised to see it — enforced by a view-filter function with its own tests.
6. Layouts contain no holes.

**Proposed package layout for Phase 1:**

```
backend/internal/engine/
  card.go        CardID, Rank, Suit, DeckColor, Value()
  deck.go        build, shuffle, deal, reshuffle
  layout.go      ordered list, insert, remove+collapse, versioning
  state.go       GameState, PlayerState, phases
  action.go      the typed action union
  event.go       the typed event union
  transition.go  Apply()
  view.go        per-player filtered view
  scoring.go     score + winner determination
  invariants.go  assertions used by tests
  testhelper.go  StackedDeck, scenario builders
```

**Phase 1 test matrix:** deck build ×1/×2, deal 4/6/8, memory phase enforcement, draw, Option A insert-anywhere, Option B, active-rank rules, no-chain, multi-match, wrong match, penalty insertion, opponent match success/fail/stale, transfer positioning, all four powers incl. zero-card fizzles, Queen/King positional swaps, knowledge-follows-card across 3 swaps, unlock gate at exactly round 3, declaration guards, all nine scoring rows in §11, reshuffle, disconnect removal and card conservation after removal, 2-player and 8-player games.
