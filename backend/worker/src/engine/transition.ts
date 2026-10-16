import type { Action } from "./actions";
import { type CardID, isPower, Jack, King, NoCard, Queen, type Rank, RankNone, Ten } from "./card";
import {
  ErrAlreadyDeclared,
  ErrAlreadyDrawn,
  ErrAlreadyInspected,
  ErrBadIndex,
  ErrDeclareLocked,
  ErrGameOver,
  ErrImpossibleState,
  ErrMemoryCount,
  ErrNoCardToGive,
  ErrNoPendingTray,
  ErrNotYourTurn,
  ErrPaused,
  ErrPlayerInactive,
  ErrPowerMandatory,
  ErrSelfTarget,
  ErrStale,
  ErrTargetEmpty,
  ErrUnknownPlayer,
  ErrWrongPhase,
  GameError,
  wrap,
} from "./errors";
import { type GameEvent, priv, pub } from "./events";
import { checkInvariants } from "./invariants";
import { finish } from "./scoring";
import { Phase, type PlayerID, type PlayerState, type State } from "./state";

export type ApplyResult =
  | { ok: true; state: State; events: GameEvent[] }
  | { ok: false; error: GameError };

/**
 * apply is the entire engine API.
 *
 * It is pure: the incoming State is cloned, the clone is mutated, and the clone
 * is returned. If anything fails — a rule violation, a stale click, or the
 * defensive impossible-state check — the ORIGINAL state is left untouched.
 * The engine never throws past this point and never half-applies an action.
 */
export function apply(s: State, a: Action): ApplyResult {
  const ns = s.clone();
  let events: GameEvent[];
  try {
    events = dispatch(ns, a);
  } catch (e) {
    return { ok: false, error: e instanceof GameError ? e : new GameError(String(e)) };
  }
  const bad = checkInvariants(ns);
  if (bad) return { ok: false, error: bad };
  return { ok: true, state: ns, events };
}

function impossible(s: State, reason: string): GameError {
  return wrap(ErrImpossibleState, `${reason} | ${s.debug()}`);
}

/** Runs a layout mutation, turning a bad position into the given engine error. */
function must<T>(fn: () => T, err: GameError): T {
  try {
    return fn();
  } catch {
    throw err;
  }
}

function dispatch(s: State, a: Action): GameEvent[] {
  if (s.phase === Phase.Finished) throw ErrGameOver;
  // §12: a disconnect freezes the whole game. Only connection actions pass.
  if (
    s.paused &&
    a.kind !== "player_disconnected" &&
    a.kind !== "player_reconnected" &&
    a.kind !== "remove_player"
  ) {
    throw ErrPaused;
  }

  switch (a.kind) {
    case "inspect_memory":
      return inspectMemory(s, a.player, a.positions);
    case "declare_combio":
      return declare(s, a.player);
    case "draw_card":
      return draw(s, a.player);
    case "keep_drawn":
      return keepDrawn(s, a.player, a.removeIndex, a.insertIndex);
    case "discard_drawn":
      return discardDrawn(s, a.player);
    case "attempt_own_match":
      return ownMatch(s, a.player, a.index, a.version);
    case "attempt_opponent_match":
      return opponentMatch(s, a);
    case "insert_penalty":
      return insertPenalty(s, a.player, a.trayIndex, a.position);
    case "peek_10":
      return peek10(s, a.player, a.index);
    case "jack_peek":
      return jackPeek(s, a.player, a.target, a.targetIndex);
    case "queen_swap":
      return queenSwap(s, a.player, a.ownIndex, a.target, a.targetIndex);
    case "king_peek":
      return kingPeek(s, a.player, a.target, a.targetIndex);
    case "king_swap":
      return kingSwap(s, a.player, a.ownIndex);
    case "cancel_king_swap":
      return cancelKing(s, a.player);
    case "skip_power":
      return skipPower(s, a.player);
    case "timer_expired":
      return timerExpired(s, a.timer);
    case "player_disconnected":
      return disconnected(s, a.player);
    case "player_reconnected":
      return reconnected(s, a.player);
    case "remove_player":
      return removePlayer(s, a.player);
  }
  throw ErrWrongPhase;
}

// ---------- guards ----------

function requireActive(s: State, id: PlayerID): PlayerState {
  const p = s.player(id);
  if (!p) throw ErrUnknownPlayer;
  if (!p.active()) throw ErrPlayerInactive;
  return p;
}

function requireCurrent(s: State, id: PlayerID): PlayerState {
  const p = requireActive(s, id);
  if (s.currentID() !== id) throw ErrNotYourTurn;
  return p;
}

// ---------- memory phase ----------

function inspectMemory(s: State, player: PlayerID, positions: number[]): GameEvent[] {
  if (s.phase !== Phase.Memory) throw ErrWrongPhase;
  const p = requireActive(s, player);
  if (p.memoryDone) throw ErrAlreadyInspected;
  const need = Math.floor(p.layout.len() / 2);
  if (positions.length !== need) throw ErrMemoryCount;
  const seen = new Set<number>();
  const cards = [];
  for (const pos of positions) {
    const id = p.layout.at(pos);
    if (id === undefined) throw ErrBadIndex;
    if (seen.has(pos)) throw ErrMemoryCount;
    seen.add(pos);
    cards.push(s.mustCard(id));
  }
  p.memoryDone = true;
  const evs = [priv(p.id, "memory_revealed", { cards, positions: [...positions] })];

  if (s.activePlayers().every((q) => q.memoryDone)) evs.push(...endMemoryPhase(s));
  return evs;
}

function endMemoryPhase(s: State): GameEvent[] {
  s.phase = Phase.TurnStart;
  const first = s.players.findIndex((p) => p.active());
  if (first >= 0) s.turnIdx = first;
  return [pub("memory_phase_ended"), pub("turn_started", { player: s.currentID() })];
}

// ---------- turn ----------

function declare(s: State, player: PlayerID): GameEvent[] {
  if (s.phase !== Phase.TurnStart) {
    if (s.phase === Phase.DrawDecision) throw ErrAlreadyDrawn;
    throw ErrWrongPhase;
  }
  const p = requireCurrent(s, player);
  if (s.declared) throw ErrAlreadyDeclared;
  // §10: availability is timing-only. The real score is never consulted here.
  if (!s.declareUnlocked()) throw ErrDeclareLocked;

  s.declared = true;
  s.declarer = p.id;
  p.turnsCompleted++;

  // Declaring ends the hand there and then: every layout is revealed, scored
  // and the winner decided (§10).
  return [pub("combio_declared", { player: p.id }), ...finish(s)];
}

function draw(s: State, player: PlayerID): GameEvent[] {
  if (s.phase !== Phase.TurnStart) throw ErrWrongPhase;
  const p = requireCurrent(s, player);
  const { id, reshuffled } = s.drawOne();
  if (id === NoCard) {
    // Defensive only. Legal play always leaves something to reshuffle.
    throw impossible(s, "turn draw required but no card obtainable after reshuffle");
  }
  s.drawn = id;
  s.drawnBy = p.id;
  s.phase = Phase.DrawDecision;

  const evs: GameEvent[] = [];
  if (reshuffled) evs.push(pub("deck_reshuffled", { deck_size: s.deck.length + 1 }));
  evs.push(
    pub("card_drawn", { player: p.id }),
    priv(p.id, "private_draw", { card: s.mustCard(id) }),
  );
  return evs;
}

function keepDrawn(
  s: State,
  player: PlayerID,
  removeIndex: number,
  insertIndex: number,
): GameEvent[] {
  if (s.phase !== Phase.DrawDecision) throw ErrWrongPhase;
  if (player !== s.drawnBy) throw ErrNotYourTurn;
  const p = s.player(player)!;
  const drawn = s.drawn;

  // §29: a zero-card player keeping the drawn card simply gains their first
  // card. Nothing is removed, so no discard and no matching window.
  if (p.layout.len() === 0) {
    must(() => p.layout.insert(0, drawn), ErrBadIndex);
    s.drawn = NoCard;
    s.drawnBy = "";
    return [pub("card_kept", { player: p.id, removed: null, insert_index: 0 }), ...endTurn(s, true)];
  }

  const removed = must(() => p.layout.removeAt(removeIndex), ErrBadIndex);
  // apply discards this clone on error, so the half-done removal never escapes.
  must(() => p.layout.insert(insertIndex, drawn), ErrBadIndex);
  s.drawn = NoCard;
  s.drawnBy = "";

  const card = s.mustCard(removed);
  s.pushDiscardTop(removed);
  s.activeRank = card.rank;
  // §4: an Option A removal NEVER activates a power, even for 10/J/Q/K.
  s.pendingPower = RankNone;
  s.powerActor = "";

  return [
    pub("card_kept", { player: p.id, removed: card, insert_index: insertIndex }),
    pub("card_discarded", { player: p.id, card, active_rank: card.rank, from_drawn: false }),
    ...openWindow(s),
  ];
}

function discardDrawn(s: State, player: PlayerID): GameEvent[] {
  if (s.phase !== Phase.DrawDecision) throw ErrWrongPhase;
  if (player !== s.drawnBy) throw ErrNotYourTurn;
  const drawn = s.drawn;
  const card = s.mustCard(drawn);
  s.pushDiscardTop(drawn);
  s.activeRank = card.rank;
  s.drawn = NoCard;
  s.drawnBy = "";

  // §8: only a directly discarded DRAWN card arms a power, and it resolves
  // after the matching window, never racing it.
  if (isPower(card.rank)) {
    s.pendingPower = card.rank;
    s.powerActor = player;
  }

  return [
    pub("card_discarded", { player, card, active_rank: card.rank, from_drawn: true }),
    ...openWindow(s),
  ];
}

function openWindow(s: State): GameEvent[] {
  s.phase = Phase.Matching;
  s.matchExtensions = 0;
  return [pub("matching_window_opened", { active_rank: s.activeRank })];
}

// ---------- matching ----------

function ownMatch(s: State, player: PlayerID, index: number, version: number): GameEvent[] {
  if (s.phase !== Phase.Matching) throw ErrWrongPhase;
  const p = requireActive(s, player);
  const id = p.layout.resolve(version, index);
  if (id === undefined || !p.layout.contains(id)) throw ErrStale;
  const card = s.mustCard(id);
  const pos = p.layout.indexOf(id);
  must(() => p.layout.removeAt(pos), ErrBadIndex);
  // Right or wrong, the thrown card is discarded, buried under the active card:
  // matching never chains (§5).
  s.buryDiscard(id);

  if (card.rank === s.activeRank) {
    s.matchExtensions++;
    return [
      pub("match_accepted", { player: p.id, owner: p.id, card }),
      pub("matching_window_extended", { seconds: 2 }),
    ];
  }
  return [pub("match_failed", { player: p.id, thrown: card, target: "" }), ...applyPenalty(s, p)];
}

function opponentMatch(
  s: State,
  a: Extract<Action, { kind: "attempt_opponent_match" }>,
): GameEvent[] {
  if (s.phase !== Phase.Matching) throw ErrWrongPhase;
  if (a.target === a.player) throw ErrSelfTarget;
  const actor = requireActive(s, a.player);
  const target = s.player(a.target);
  if (!target) throw ErrUnknownPlayer;
  // A removed player's cards have left play, so a click on one is stale (and
  // therefore free), never a wrong match.
  if (!target.active()) throw ErrStale;
  if (!target.targetable()) throw ErrTargetEmpty;
  // §7: you must have a card to hand over.
  if (actor.layout.len() === 0) throw ErrNoCardToGive;

  const targetID = target.layout.resolve(a.targetVersion, a.targetIndex);
  if (targetID === undefined || !target.layout.contains(targetID)) throw ErrStale;
  const giveID = actor.layout.resolve(a.giveVersion, a.giveIndex);
  if (giveID === undefined || !actor.layout.contains(giveID)) throw ErrStale;

  const card = s.mustCard(targetID);
  if (card.rank !== s.activeRank) {
    // §7: their card stays, unrevealed. Only the matcher pays.
    return [
      pub("match_failed", { player: actor.id, thrown: null, target: target.id }),
      ...applyPenalty(s, actor),
    ];
  }

  const pos = must(() => target.layout.removeCard(targetID), ErrStale);
  s.buryDiscard(targetID);
  must(() => actor.layout.removeCard(giveID), ErrStale);
  // §3: the transferred card lands in the exact gap the removed card left.
  must(() => target.layout.insert(pos, giveID), ErrBadIndex);
  s.matchExtensions++;
  return [
    pub("match_accepted", { player: actor.id, owner: target.id, card }),
    pub("card_transferred", { from: actor.id, to: target.id, position: pos }),
    pub("matching_window_extended", { seconds: 2 }),
  ];
}

/** §6: +2 face-down cards into the pending tray, untargetable until placed. */
function applyPenalty(s: State, p: PlayerState): GameEvent[] {
  let got = 0;
  const evs: GameEvent[] = [];
  for (let i = 0; i < 2; i++) {
    const { id, reshuffled } = s.drawOne();
    if (reshuffled) evs.push(pub("deck_reshuffled", { deck_size: s.deck.length + 1 }));
    if (id === NoCard) break; // legal short-draw: never duplicate a card
    p.pendingPenalty.push(id);
    got++;
  }
  evs.push(pub("penalty_applied", { player: p.id, count: got }));
  return evs;
}

function insertPenalty(
  s: State,
  player: PlayerID,
  trayIndex: number,
  position: number,
): GameEvent[] {
  if (s.phase !== Phase.Matching) throw ErrWrongPhase;
  const p = requireActive(s, player);
  if (trayIndex < 0 || trayIndex >= p.pendingPenalty.length) throw ErrNoPendingTray;
  const id = p.pendingPenalty[trayIndex];
  p.pendingPenalty.splice(trayIndex, 1);
  must(() => p.layout.insert(position, id), ErrBadIndex);
  return [pub("penalty_placed", { player: p.id, position, auto: false })];
}

function closeWindow(s: State): GameEvent[] {
  const evs: GameEvent[] = [];
  // Anything still in a tray is auto-appended when the window shuts (§6).
  for (const p of s.players) {
    while (p.pendingPenalty.length > 0) {
      p.layout.append(p.pendingPenalty.shift()!);
      evs.push(pub("penalty_placed", { player: p.id, position: p.layout.len() - 1, auto: true }));
    }
  }
  s.activeRank = RankNone;
  s.matchExtensions = 0;
  evs.push(pub("matching_window_closed"));
  return [...evs, ...resolvePower(s)];
}

// ---------- powers ----------

function resolvePower(s: State): GameEvent[] {
  if (s.pendingPower === RankNone) return endTurn(s, true);
  const actor = s.player(s.powerActor);
  if (!actor || !actor.active()) {
    s.pendingPower = RankNone;
    return endTurn(s, true);
  }
  const reason = powerFizzles(s, actor, s.pendingPower);
  if (reason !== null) {
    const rank = s.pendingPower;
    s.pendingPower = RankNone;
    return [powerSkipped(actor.id, rank, reason), ...endTurn(s, true)];
  }
  s.phase = Phase.Power;
  return [priv(actor.id, "power_available", { rank: s.pendingPower })];
}

function powerSkipped(player: PlayerID, rank: Rank, reason: string): GameEvent {
  return pub("power_skipped", { player, rank, reason });
}

/** The zero-card cases in §8. Returns the reason the power fizzles, or null. */
function powerFizzles(s: State, actor: PlayerState, rank: Rank): string | null {
  const hasTarget = s.activePlayers().some((q) => q.id !== actor.id && q.targetable());
  switch (rank) {
    case Ten:
      if (actor.layout.len() === 0) return "no own card to peek";
      break;
    case Jack:
    case King:
      if (!hasTarget) return "no opponent card to inspect";
      break;
    case Queen:
      if (actor.layout.len() === 0) return "no own card to swap";
      if (!hasTarget) return "no opponent card to swap";
      break;
  }
  return null;
}

function requirePower(s: State, id: PlayerID, want: Rank, phase: Phase): PlayerState {
  if (s.phase !== phase) throw ErrWrongPhase;
  if (s.powerActor !== id) throw ErrNotYourTurn;
  if (s.pendingPower !== want) throw ErrWrongPhase;
  return s.player(id)!;
}

function targetCard(
  s: State,
  actor: PlayerID,
  target: PlayerID,
  index: number,
): { t: PlayerState; id: CardID } {
  if (target === actor) throw ErrSelfTarget;
  const t = s.player(target);
  if (!t) throw ErrUnknownPlayer;
  if (!t.active()) throw ErrStale;
  if (!t.targetable()) throw ErrTargetEmpty;
  const id = t.layout.at(index);
  if (id === undefined) throw ErrBadIndex;
  return { t, id };
}

function reveal(to: PlayerID, owner: PlayerID, position: number, s: State, id: CardID): GameEvent {
  return priv(to, "private_reveal", { owner, position, card: s.mustCard(id) });
}

function peek10(s: State, player: PlayerID, index: number): GameEvent[] {
  const p = requirePower(s, player, Ten, Phase.Power);
  const id = p.layout.at(index);
  if (id === undefined) throw ErrBadIndex;
  const evs = [reveal(p.id, p.id, index, s, id)];
  s.pendingPower = RankNone;
  return [...evs, ...endTurn(s, true)];
}

function jackPeek(s: State, player: PlayerID, target: PlayerID, index: number): GameEvent[] {
  const p = requirePower(s, player, Jack, Phase.Power);
  const { t, id } = targetCard(s, p.id, target, index);
  const evs = [reveal(p.id, t.id, index, s, id)];
  s.pendingPower = RankNone;
  return [...evs, ...endTurn(s, true)];
}

function queenSwap(
  s: State,
  player: PlayerID,
  ownIndex: number,
  target: PlayerID,
  targetIndex: number,
): GameEvent[] {
  const p = requirePower(s, player, Queen, Phase.Power);
  const ownID = p.layout.at(ownIndex);
  if (ownID === undefined) throw ErrBadIndex;
  const { t, id: targetID } = targetCard(s, p.id, target, targetIndex);
  // §3: each card takes the exact position vacated by the other. Blind: no
  // reveal event is emitted to anyone, including the swapper.
  must(() => p.layout.swap(ownIndex, targetID), ErrBadIndex);
  must(() => t.layout.swap(targetIndex, ownID), ErrBadIndex);
  const evs = [swapped(p.id, ownIndex, t.id, targetIndex, Queen)];
  s.pendingPower = RankNone;
  return [...evs, ...endTurn(s, true)];
}

/** Names positions only; `power` is the power that did it, not a card. */
function swapped(
  player: PlayerID,
  ownIndex: number,
  target: PlayerID,
  targetIndex: number,
  power: Rank,
): GameEvent {
  return pub("cards_swapped", {
    player,
    own_index: ownIndex,
    target,
    target_index: targetIndex,
    power,
  });
}

function kingPeek(s: State, player: PlayerID, target: PlayerID, index: number): GameEvent[] {
  const p = requirePower(s, player, King, Phase.Power);
  const { t, id } = targetCard(s, p.id, target, index);
  const evs = [reveal(p.id, t.id, index, s, id)];

  // §8: a zero-card King user gets the peek but cannot swap.
  if (p.layout.len() === 0) {
    s.pendingPower = RankNone;
    evs.push(powerSkipped(p.id, King, "no own card to swap"));
    return [...evs, ...endTurn(s, true)];
  }
  s.kingPeek = { target: t.id, targetCard: id };
  s.phase = Phase.KingDecision;
  return evs;
}

function kingSwap(s: State, player: PlayerID, ownIndex: number): GameEvent[] {
  const p = requirePower(s, player, King, Phase.KingDecision);
  const kp = s.kingPeek;
  if (!kp) throw ErrWrongPhase;
  const t = s.player(kp.target);
  if (!t || !t.active() || !t.layout.contains(kp.targetCard)) throw ErrStale;
  const ownID = p.layout.at(ownIndex);
  if (ownID === undefined) throw ErrBadIndex;
  const tPos = t.layout.indexOf(kp.targetCard);
  must(() => p.layout.swap(ownIndex, kp.targetCard), ErrBadIndex);
  must(() => t.layout.swap(tPos, ownID), ErrBadIndex);
  const evs = [swapped(p.id, ownIndex, t.id, tPos, King)];
  s.pendingPower = RankNone;
  s.kingPeek = null;
  return [...evs, ...endTurn(s, true)];
}

function cancelKing(s: State, player: PlayerID): GameEvent[] {
  const p = requirePower(s, player, King, Phase.KingDecision);
  s.pendingPower = RankNone;
  s.kingPeek = null;
  return [powerSkipped(p.id, King, "cancelled"), ...endTurn(s, true)];
}

function skipPower(s: State, player: PlayerID): GameEvent[] {
  if (s.phase !== Phase.Power) throw ErrWrongPhase;
  if (s.powerActor !== player) throw ErrNotYourTurn;
  // §8: only the Queen is optional. 10, Jack and the King peek are mandatory
  // and can only lapse through the power timer.
  if (s.pendingPower !== Queen) throw ErrPowerMandatory;
  const rank = s.pendingPower;
  s.pendingPower = RankNone;
  return [powerSkipped(player, rank, "declined"), ...endTurn(s, true)];
}

// ---------- turn end ----------

/**
 * endTurn closes the current turn. counted is false when the turn was forfeited
 * rather than played (the player was removed mid-turn): a forfeit must never
 * advance the round counter, or a disconnect could unlock COMBIO early.
 */
function endTurn(s: State, counted: boolean): GameEvent[] {
  const cur = s.current();
  if (cur && counted) {
    cur.turnsCompleted++;
    if (!s.declared) s.recordTurn(cur.id);
  }
  s.drawn = NoCard;
  s.drawnBy = "";
  s.pendingPower = RankNone;
  s.powerActor = "";
  s.kingPeek = null;
  s.activeRank = RankNone;

  const evs: GameEvent[] = [];
  if (!s.declareUnlockedFlag && s.declareUnlocked()) {
    s.declareUnlockedFlag = true;
    evs.push(pub("declare_unlocked"));
  }
  if (s.activeCount() <= 1) return [...evs, ...finish(s)];
  const n = s.players.length;
  for (let i = 1; i <= n; i++) {
    const idx = (s.turnIdx + i) % n;
    if (s.players[idx].active()) {
      s.turnIdx = idx;
      break;
    }
  }
  s.phase = Phase.TurnStart;
  evs.push(pub("turn_started", { player: s.currentID() }));
  return evs;
}

// ---------- timers ----------

function timerExpired(s: State, kind: string): GameEvent[] {
  switch (kind) {
    case "memory":
      if (s.phase !== Phase.Memory) throw ErrWrongPhase;
      return endMemoryPhase(s);

    case "match_window":
      if (s.phase !== Phase.Matching) throw ErrWrongPhase;
      return closeWindow(s);

    case "power": {
      if (s.phase !== Phase.Power && s.phase !== Phase.KingDecision) throw ErrWrongPhase;
      const rank = s.pendingPower;
      s.pendingPower = RankNone;
      s.kingPeek = null;
      return [powerSkipped(s.powerActor, rank, "timeout"), ...endTurn(s, true)];
    }

    case "turn":
      // §13: a forced legal move, not bot play. Draw and discard, and skip any
      // power the discard would have armed.
      if (s.phase === Phase.TurnStart) {
        const cur = s.currentID();
        return [pub("turn_timed_out", { player: cur }), ...draw(s, cur), ...forcedDiscard(s, cur)];
      }
      if (s.phase === Phase.DrawDecision) {
        const cur = s.drawnBy;
        return [pub("turn_timed_out", { player: cur }), ...forcedDiscard(s, cur)];
      }
      throw ErrWrongPhase;
  }
  throw ErrWrongPhase;
}

function forcedDiscard(s: State, p: PlayerID): GameEvent[] {
  const evs = discardDrawn(s, p);
  if (s.pendingPower !== RankNone) {
    const rank = s.pendingPower;
    s.pendingPower = RankNone;
    s.powerActor = "";
    evs.push(powerSkipped(p, rank, "turn timeout"));
  }
  return evs;
}

// ---------- connection ----------

function disconnected(s: State, player: PlayerID): GameEvent[] {
  const p = requireActive(s, player);
  if (!p.connected) return [];
  p.connected = false;
  s.disconnected.add(p.id);
  const evs = [pub("player_disconnected", { player: p.id })];
  if (!s.paused) {
    s.paused = true;
    evs.push(pub("game_paused"));
  }
  return evs;
}

function reconnected(s: State, player: PlayerID): GameEvent[] {
  const p = requireActive(s, player);
  p.connected = true;
  s.disconnected.delete(p.id);
  const evs = [pub("player_reconnected", { player: p.id })];
  if (s.disconnected.size === 0 && s.paused) {
    s.paused = false;
    evs.push(pub("game_resumed"));
  }
  return evs;
}

/**
 * removePlayer is the reconnect-window lapse (§12). The player leaves the
 * rotation and every card they hold sinks FACE-DOWN TO THE BOTTOM of the
 * discard pile: never revealed, no window, no power, but still in the game.
 */
function removePlayer(s: State, player: PlayerID): GameEvent[] {
  const p = requireActive(s, player);
  const wasCurrent = s.currentID() === p.id;
  const wasDrawing = s.drawnBy === p.id;
  const wasPowerActor = s.powerActor === p.id;
  const powerTargetedThem = s.kingPeek !== null && s.kingPeek.target === p.id;

  // Deterministic sink order, bottom-first: layout cards in layout order ->
  // the privately drawn card, if any -> pending tray cards in tray order.
  const sunk = p.layout.list();
  if (wasDrawing && s.drawn !== NoCard) {
    sunk.push(s.drawn);
    s.drawn = NoCard;
    s.drawnBy = "";
  }
  sunk.push(...p.pendingPenalty);
  while (p.layout.len() > 0) p.layout.removeAt(0);
  p.pendingPenalty = [];
  s.sinkToBottom(sunk);

  const owed = s.owesTurn(p.id);
  p.removed = true;
  p.connected = false;
  s.disconnected.delete(p.id);

  // §10: a player who leaves owing a turn breaks the round in progress. It is
  // discarded rather than completed, and a fresh round starts with whoever is
  // left, so removals can never retroactively finish a round.
  s.roundPending = s.roundPending.filter((id) => id !== p.id);
  if (!s.declared && owed) s.startRound();

  const evs = [pub("player_removed", { player: p.id, cards_to_bottom: sunk.length })];
  if (s.disconnected.size === 0 && s.paused) {
    s.paused = false;
    evs.push(pub("game_resumed"));
  }

  if (s.activeCount() <= 1) {
    s.activeRank = RankNone;
    s.pendingPower = RankNone;
    s.kingPeek = null;
    return [...evs, ...finish(s)];
  }

  // An unresolved action that involved them is cancelled without penalty. The
  // acting player keeps credit for a turn they actually played; a player
  // removed mid-turn forfeits theirs.
  const counted = s.currentID() !== p.id;
  if (
    powerTargetedThem ||
    (wasPowerActor && (s.phase === Phase.Power || s.phase === Phase.KingDecision))
  ) {
    const rank = s.pendingPower;
    s.pendingPower = RankNone;
    s.kingPeek = null;
    evs.push(powerSkipped(s.powerActor, rank, "target removed"));
    return [...evs, ...endTurn(s, counted)];
  }
  if (wasCurrent && (s.phase === Phase.TurnStart || s.phase === Phase.DrawDecision)) {
    s.phase = Phase.TurnStart;
    return [...evs, ...endTurn(s, false)];
  }
  return evs;
}
