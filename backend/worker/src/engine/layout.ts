import type { CardID } from "./card";

/**
 * How many past orderings are kept per player so that a client action carrying
 * an older layoutVersion can still be resolved back to the physical card the
 * player was looking at (§5). Older than this and the action is stale.
 */
const layoutHistoryDepth = 32;

class BadPosition extends Error {
  constructor() {
    super("position out of range");
  }
}

/**
 * Layout is a player's ordered row of face-down cards (§3).
 *
 *   - order is meaningful and is the player's only memory scaffold
 *   - gaps ALWAYS close: there are never empty slots
 *   - every mutation bumps version, and the pre-mutation ordering is retained
 *     so concurrent client actions can be resolved by physical card identity
 */
export class Layout {
  private cards: CardID[];
  private _version = 1;
  private history = new Map<number, CardID[]>();
  private ring: number[] = [];

  constructor(cards: CardID[] = []) {
    this.cards = [...cards];
  }

  len(): number {
    return this.cards.length;
  }

  version(): number {
    return this._version;
  }

  /** A copy; the caller can never mutate the layout by accident. */
  list(): CardID[] {
    return [...this.cards];
  }

  at(pos: number): CardID | undefined {
    if (!Number.isInteger(pos) || pos < 0 || pos >= this.cards.length) return undefined;
    return this.cards[pos];
  }

  indexOf(id: CardID): number {
    return this.cards.indexOf(id);
  }

  contains(id: CardID): boolean {
    return this.indexOf(id) >= 0;
  }

  /**
   * resolve maps a (version, index) pair from a client back to a physical
   * card. This is the heart of the stale-vs-wrong distinction in §5.
   */
  resolve(version: number, pos: number): CardID | undefined {
    if (version === 0 || version === this._version) return this.at(pos);
    const snap = this.history.get(version);
    if (!snap) return undefined; // too old -> caller treats as stale
    if (pos < 0 || pos >= snap.length) return undefined;
    return snap[pos];
  }

  /** Records the current ordering under the current version, then advances it. */
  private bump(): void {
    this.history.set(this._version, [...this.cards]);
    this.ring.push(this._version);
    while (this.ring.length > layoutHistoryDepth) {
      this.history.delete(this.ring.shift()!);
    }
    this._version++;
  }

  /** Valid pos is 0..len (inclusive). Throws on a bad position. */
  insert(pos: number, id: CardID): void {
    if (!Number.isInteger(pos) || pos < 0 || pos > this.cards.length) throw new BadPosition();
    this.bump();
    this.cards.splice(pos, 0, id);
  }

  append(id: CardID): void {
    this.insert(this.cards.length, id);
  }

  /** Removes the card at pos and CLOSES THE GAP (§3). */
  removeAt(pos: number): CardID {
    if (!Number.isInteger(pos) || pos < 0 || pos >= this.cards.length) throw new BadPosition();
    const id = this.cards[pos];
    this.bump();
    this.cards.splice(pos, 1);
    return id;
  }

  /** Removes a physical card wherever it sits and returns the vacated position. */
  removeCard(id: CardID): number {
    const pos = this.indexOf(id);
    if (pos < 0) throw new BadPosition();
    this.removeAt(pos);
    return pos;
  }

  /** Replaces the card at pos with `incoming`, returning the card that was there. */
  swap(pos: number, incoming: CardID): CardID {
    if (!Number.isInteger(pos) || pos < 0 || pos >= this.cards.length) throw new BadPosition();
    const out = this.cards[pos];
    this.bump();
    this.cards[pos] = incoming;
    return out;
  }

  clone(): Layout {
    const n = new Layout(this.cards);
    n._version = this._version;
    n.ring = [...this.ring];
    for (const [k, v] of this.history) n.history.set(k, [...v]);
    return n;
  }
}
