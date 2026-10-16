import { type Card, type CardID, type DeckColor, NoCard, type Rank } from "./card";
import { sortedRecord } from "./scoring";
import { Phase, phaseString, type PlayerID, type PlayerState, type State } from "./state";

/**
 * CardHandle is how EVERY face-down card is described to a client: position,
 * physical identity, and deck colour. There is deliberately no rank or suit
 * field, so a hidden card cannot leak even by accident.
 */
export interface CardHandle {
  position: number;
  id: CardID;
  deck: DeckColor;
}

export interface OpponentView {
  id: PlayerID;
  seat: number;
  cards: CardHandle[];
  /**
   * This layout's revision. A client sends it back with a match attempt so the
   * server can tell "the card you clicked has already gone" (stale, free) apart
   * from "you were wrong about that card" (a penalty).
   */
  version: number;
  count: number;
  pending_penalty: number;
  connected: boolean;
  removed: boolean;
  turns_completed: number;
}

/** PlayerView is the only thing that should ever be serialised to a client. */
export interface PlayerView {
  me: PlayerID;
  phase: string;
  my_cards: CardHandle[];
  my_pending_count: number;
  my_version: number;
  opponents: OpponentView[] | null;
  turn_player: PlayerID;
  my_turn: boolean;
  deck_count: number;
  discard_count: number;
  discard_top: Card | null; // the only publicly identified card
  active_rank: Rank;
  my_drawn_card: Card | null; // ONLY for the player who drew it
  my_pending_power: Rank; // ONLY for the player resolving a power
  declare_available: boolean;
  declared: boolean;
  declarer: PlayerID;
  paused: boolean;
  finished: boolean;
  // Present only once the game is over.
  scores?: Record<PlayerID, number>;
  winners?: PlayerID[];
  reveal?: Record<PlayerID, Card[]>;
}

function handles(s: State, p: PlayerState): CardHandle[] {
  return p.layout.list().map((id, i) => ({ position: i, id, deck: s.mustCard(id).deck }));
}

/**
 * viewFor builds the filtered, player-specific state (§14). Everything the
 * server knows that this player must not know is simply absent.
 */
export function viewFor(s: State, me: PlayerID): PlayerView {
  const top = s.discardTop();
  const v: PlayerView = {
    me,
    phase: phaseString(s.phase),
    my_cards: null as unknown as CardHandle[],
    my_pending_count: 0,
    my_version: 0,
    opponents: null,
    turn_player: s.currentID(),
    my_turn: s.currentID() === me,
    deck_count: s.deck.length,
    discard_count: s.discard.length,
    discard_top: top !== NoCard ? s.mustCard(top) : null,
    active_rank: s.activeRank,
    my_drawn_card: null,
    my_pending_power: 0,
    declare_available: false,
    declared: s.declared,
    declarer: s.declarer,
    paused: s.paused,
    finished: s.phase === Phase.Finished,
  };
  for (const p of s.players) {
    if (p.id === me) {
      v.my_cards = handles(s, p);
      v.my_pending_count = p.pendingPenalty.length;
      v.my_version = p.layout.version();
      continue;
    }
    (v.opponents ??= []).push({
      id: p.id,
      seat: p.seat,
      cards: handles(s, p),
      version: p.layout.version(),
      count: p.layout.len(),
      pending_penalty: p.pendingPenalty.length,
      connected: p.connected,
      removed: p.removed,
      turns_completed: p.turnsCompleted,
    });
  }
  if (s.phase === Phase.DrawDecision && s.drawnBy === me && s.drawn !== NoCard) {
    v.my_drawn_card = s.mustCard(s.drawn);
  }
  if ((s.phase === Phase.Power || s.phase === Phase.KingDecision) && s.powerActor === me) {
    v.my_pending_power = s.pendingPower;
  }
  // §10: timing only. The player's real score is never consulted.
  v.declare_available =
    s.phase === Phase.TurnStart &&
    s.currentID() === me &&
    !s.declared &&
    !s.paused &&
    s.declareUnlocked();

  if (s.phase === Phase.Finished) {
    const scores = new Map(s.scores ?? []);
    if (scores.size > 0) v.scores = sortedRecord(scores);
    if (s.winners.length > 0) v.winners = [...s.winners];
    const reveal = new Map<PlayerID, Card[]>();
    for (const p of s.players) {
      if (!p.active()) continue;
      reveal.set(
        p.id,
        p.layout.list().map((id) => s.mustCard(id)),
      );
    }
    if (reveal.size > 0) v.reveal = sortedRecord(reveal);
  }
  return v;
}
