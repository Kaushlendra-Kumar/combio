import { type Card, type CardID, DeckBlue, DeckRed, NoCard } from "./card";

/**
 * Shuffler is the engine's only source of randomness. Injecting it is what
 * makes tests deterministic: production passes a seeded RNG, tests pass
 * StackedShuffler and get an exact, known deck order.
 */
export interface Shuffler {
  shuffle(ids: CardID[]): void;
}

/**
 * SeededShuffler is the production implementation: a Fisher-Yates shuffle
 * driven by splitmix64, so a given seed always deals the same game.
 */
export class SeededShuffler implements Shuffler {
  private state: bigint;

  constructor(seed: number) {
    this.state = BigInt.asUintN(64, BigInt(Math.trunc(seed)));
  }

  private next(): bigint {
    this.state = BigInt.asUintN(64, this.state + 0x9e3779b97f4a7c15n);
    let z = this.state;
    z = BigInt.asUintN(64, (z ^ (z >> 30n)) * 0xbf58476d1ce4e5b9n);
    z = BigInt.asUintN(64, (z ^ (z >> 27n)) * 0x94d049bb133111ebn);
    return z ^ (z >> 31n);
  }

  /** intn returns a uniform integer in [0, n). */
  private intn(n: number): number {
    const bound = BigInt(n);
    const limit = (1n << 64n) - ((1n << 64n) % bound);
    for (;;) {
      const v = this.next();
      if (v < limit) return Number(v % bound);
    }
  }

  shuffle(ids: CardID[]): void {
    for (let i = ids.length - 1; i > 0; i--) {
      const j = this.intn(i + 1);
      [ids[i], ids[j]] = [ids[j], ids[i]];
    }
  }
}

/** NoShuffle leaves order untouched. */
export class NoShuffle implements Shuffler {
  shuffle(): void {}
}

/**
 * StackedShuffler forces an exact ordering. The order given is the order the
 * slice ends up in. Any card not named keeps its relative position after the
 * named ones.
 */
export class StackedShuffler implements Shuffler {
  constructor(readonly order: CardID[]) {}

  shuffle(ids: CardID[]): void {
    const want = new Map<CardID, number>();
    this.order.forEach((id, i) => want.set(id, i));
    const seen: CardID[] = new Array(this.order.length).fill(NoCard);
    const tail: CardID[] = [];
    for (const id of ids) {
      const pos = want.get(id);
      if (pos !== undefined) {
        seen[pos] = id;
        continue;
      }
      tail.push(id);
    }
    const out = [...seen.filter((id) => id !== NoCard), ...tail];
    for (let i = 0; i < out.length; i++) ids[i] = out[i];
  }
}

/**
 * buildDeck creates 52*n unique physical cards with sequential CardIDs. With
 * two decks the second is DeckBlue so players can track physical cards (§1).
 */
export function buildDeck(numDecks: number): Card[] {
  if (numDecks !== 1 && numDecks !== 2) throw new Error("deck count must be 1 or 2");
  const out: Card[] = [];
  let id = 1;
  for (let d = 0; d < numDecks; d++) {
    const deck = d === 1 ? DeckBlue : DeckRed;
    for (let suit = 0; suit < 4; suit++) {
      for (let rank = 1; rank <= 13; rank++) {
        out.push({ id: id++, rank, suit, deck });
      }
    }
  }
  return out;
}

/** §1: 2-4 players use one deck, 5-8 use two. */
export function decksFor(players: number): number {
  return players >= 5 ? 2 : 1;
}
