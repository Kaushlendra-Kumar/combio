import { type Card, NoCard, RankNone, rankValue } from "./card";
import { type GameEvent, pub } from "./events";
import { Phase, type PlayerID, type State } from "./state";

/** One player's final total. A list keeps winner determination deterministic. */
export interface ScoreEntry {
  player: PlayerID;
  score: number;
}

/**
 * determineWinners implements §11.
 *
 *   declarer strictly lower than every other player -> declarer alone wins
 *   otherwise -> declaration fails, the declarer is out of contention, and the
 *                lowest score among the rest wins, with co-winners on a tie
 *
 * declarerEligible is false when the declarer was removed by a disconnect: the
 * declaration still stands and still ends the game, but they cannot win (§12).
 */
export function determineWinners(
  order: ScoreEntry[],
  declared: boolean,
  declarer: PlayerID,
  declarerEligible: boolean,
): { winners: PlayerID[] | null; failed: boolean } {
  if (order.length === 0) return { winners: null, failed: false };
  if (order.length === 1) return { winners: [order[0].player], failed: false };

  const lowestOf = (entries: ScoreEntry[]): PlayerID[] => {
    const best = Math.min(...entries.map((e) => e.score));
    return entries.filter((e) => e.score === best).map((e) => e.player);
  };

  if (!declared) return { winners: lowestOf(order), failed: false };

  const others: ScoreEntry[] = [];
  let declarerScore = 0;
  let foundDeclarer = false;
  for (const e of order) {
    if (e.player === declarer) {
      declarerScore = e.score;
      foundDeclarer = true;
      continue;
    }
    others.push(e);
  }
  if (others.length === 0) return { winners: [declarer], failed: false };
  if (!foundDeclarer || !declarerEligible) {
    // Declarer is gone: declaration stands, they cannot win.
    return { winners: lowestOf(others), failed: true };
  }
  const othersMin = Math.min(...others.map((e) => e.score));
  if (declarerScore < othersMin) return { winners: [declarer], failed: false };
  return { winners: lowestOf(others), failed: true };
}

/** sortedRecord renders a map the way Go's encoding/json does: keys sorted. */
export function sortedRecord<V>(m: Map<string, V>): Record<string, V> {
  const out: Record<string, V> = {};
  for (const k of [...m.keys()].sort()) out[k] = m.get(k)!;
  return out;
}

/** finish reveals every remaining layout, scores it and decides the winner. */
export function finish(s: State): GameEvent[] {
  s.phase = Phase.Finished;
  s.activeRank = RankNone;
  s.drawn = NoCard;
  s.pendingPower = RankNone;
  s.kingPeek = null;

  // Any card still in a pending tray belongs to that player and counts toward
  // their score (§6), so place it before revealing.
  for (const p of s.players) {
    while (p.pendingPenalty.length > 0) p.layout.append(p.pendingPenalty.shift()!);
  }

  const order: ScoreEntry[] = [];
  const scores = new Map<PlayerID, number>();
  const reveal = new Map<PlayerID, Card[]>();
  for (const p of s.players) {
    if (!p.active()) continue;
    const cards = p.layout.list().map((id) => s.mustCard(id));
    const sum = cards.reduce((acc, c) => acc + rankValue(c.rank), 0);
    order.push({ player: p.id, score: sum });
    scores.set(p.id, sum);
    reveal.set(p.id, cards);
  }

  let declarerEligible = false;
  if (s.declared) {
    const d = s.player(s.declarer);
    if (d && d.active()) declarerEligible = true;
  }
  const { winners, failed } = determineWinners(order, s.declared, s.declarer, declarerEligible);
  s.scores = scores;
  s.winners = winners ? [...winners] : [];

  return [
    pub("game_finished", {
      scores: sortedRecord(scores),
      winners: winners && winners.length > 0 ? [...winners] : null,
      declarer: s.declarer,
      declaration_failed: failed,
      reveal: sortedRecord(reveal),
    }),
  ];
}
