// CardID is the identity of a single *physical* card. Two 8♠ from two different
// decks have different CardIDs. Knowledge in COMBIO follows the physical card,
// never the position, so nothing in the engine may identify a card by rank.
export type CardID = number;

/** NoCard means "no card here". */
export const NoCard: CardID = 0;

/** Rank is 1..13. RankNone means "no active rank". */
export type Rank = number;

export const RankNone = 0;
export const Ace = 1;
export const Ten = 10;
export const Jack = 11;
export const Queen = 12;
export const King = 13;

/** §1: Ace is -1, everything else is its face value. */
export function rankValue(r: Rank): number {
  return r === Ace ? -1 : r;
}

/** Whether this rank carries a power when it is the directly discarded drawn card (§8). */
export function isPower(r: Rank): boolean {
  return r === Ten || r === Jack || r === Queen || r === King;
}

export function rankString(r: Rank): string {
  switch (r) {
    case RankNone:
      return "-";
    case Ace:
      return "A";
    case Ten:
      return "10";
    case Jack:
      return "J";
    case Queen:
      return "Q";
    case King:
      return "K";
    default:
      return String(r);
  }
}

/** Suit is cosmetic in COMBIO: matching is rank-only. Clubs=0 .. Spades=3. */
export type Suit = number;

/**
 * DeckColor distinguishes the two physical decks used in 5-8 player games
 * (§1). It is PUBLIC for every card, including face-down ones.
 */
export type DeckColor = number;
export const DeckRed = 0;
export const DeckBlue = 1;

/** Card is immutable for the whole game. Field order matches the wire JSON. */
export interface Card {
  readonly id: CardID;
  readonly rank: Rank;
  readonly suit: Suit;
  readonly deck: DeckColor;
}
