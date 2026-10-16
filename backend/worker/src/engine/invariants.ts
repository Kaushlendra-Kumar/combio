import { type CardID, NoCard, RankNone, rankString } from "./card";
import { GameError } from "./errors";
import { Phase, phaseString, type State } from "./state";

/**
 * checkInvariants runs after every accepted transition. If any of these fail,
 * apply rejects the action and returns the untouched original state, so a bug
 * can never corrupt a live game.
 *
 * Card conservation is the important one: no card ever leaves the game, so the
 * total is constant and a single equality check catches almost any
 * card-handling bug.
 */
export function checkInvariants(s: State): GameError | null {
  const seen = new Set<CardID>();
  let count = 0;

  const add = (ids: CardID[], where: string): GameError | null => {
    for (const id of ids) {
      if (id === NoCard) return new GameError(`invariant: NoCard found in ${where}`);
      if (!s.cards.has(id)) return new GameError(`invariant: unknown card ${id} in ${where}`);
      if (seen.has(id)) return new GameError(`invariant: card ${id} appears twice (${where})`);
      seen.add(id);
      count++;
    }
    return null;
  };

  let err = add(s.deck, "deck") ?? add(s.discard, "discard");
  if (err) return err;
  for (const p of s.players) {
    err = add(p.layout.list(), "layout:" + p.id) ?? add(p.pendingPenalty, "tray:" + p.id);
    if (err) return err;
    if (p.removed && (p.layout.len() > 0 || p.pendingPenalty.length > 0)) {
      return new GameError(`invariant: removed player ${p.id} still holds cards`);
    }
  }
  if (s.drawn !== NoCard) {
    err = add([s.drawn], "drawn");
    if (err) return err;
  }

  if (count !== s.cards.size) {
    return new GameError(
      `invariant: card conservation broken, accounted ${count} of ${s.cards.size} | ${s.debug()}`,
    );
  }

  // Phase consistency.
  if ((s.activeRank !== RankNone) !== (s.phase === Phase.Matching)) {
    return new GameError(
      `invariant: activeRank=${rankString(s.activeRank)} but phase=${phaseString(s.phase)}`,
    );
  }
  if ((s.drawn !== NoCard) !== (s.phase === Phase.DrawDecision)) {
    return new GameError(`invariant: drawn card present but phase=${phaseString(s.phase)}`);
  }
  if (s.phase !== Phase.KingDecision && s.kingPeek !== null) {
    return new GameError("invariant: king peek state outside king decision phase");
  }
  if (s.discard.length === 0) {
    return new GameError("invariant: discard pile must never be empty after setup");
  }
  // The pending round set may only ever name active players.
  for (const id of s.roundPending) {
    const q = s.player(id);
    if (!q || !q.active()) {
      return new GameError(`invariant: round pending set names inactive player ${id}`);
    }
  }

  // Trays only exist while a matching window is open.
  if (s.phase !== Phase.Matching) {
    for (const p of s.players) {
      if (p.pendingPenalty.length > 0) {
        return new GameError(
          `invariant: ${p.id} has an unplaced penalty tray in phase ${phaseString(s.phase)}`,
        );
      }
    }
  }
  return null;
}
