import { type Card, type CardID, NoCard, type Rank, RankNone, rankString } from "./card";
import { buildDeck, decksFor, NoShuffle, type Shuffler } from "./deck";
import { ErrHandSize, ErrPlayerCount, GameError } from "./errors";
import { type GameEvent, pub } from "./events";
import { Layout } from "./layout";

export type PlayerID = string;

/** Phase is the engine's state-machine node (§16). */
export enum Phase {
  Memory, // players are inspecting half their cards
  TurnStart, // current player may declare or draw
  DrawDecision, // current player holds a private drawn card
  Matching, // matching window open to everyone
  Power, // discarded drawn power card is resolving
  KingDecision, // King peeked; actor chooses swap or cancel
  Finished,
}

export function phaseString(p: Phase): string {
  switch (p) {
    case Phase.Memory:
      return "memory";
    case Phase.TurnStart:
      return "turn_start";
    case Phase.DrawDecision:
      return "draw_decision";
    case Phase.Matching:
      return "matching";
    case Phase.Power:
      return "power";
    case Phase.KingDecision:
      return "king_decision";
    default:
      return "finished";
  }
}

/** Config is fixed at table creation. */
export interface Config {
  cardsPerPlayer: number; // 4, 6 or 8
  numDecks: number; // derived from player count unless overridden
}

/** PlayerState is everything the server knows about one seat. */
export class PlayerState {
  pendingPenalty: CardID[] = []; // §6 tray: held, not yet placed, untargetable
  connected = true;
  removed = false;
  turnsCompleted = 0;
  memoryDone = false;

  constructor(
    readonly id: PlayerID,
    readonly seat: number,
    public layout: Layout,
  ) {}

  active(): boolean {
    return !this.removed;
  }

  /**
   * §7: a player holding zero cards cannot be the target of Jack, Queen, King
   * or opponent-card matching. Tray cards do not count.
   */
  targetable(): boolean {
    return this.active() && this.layout.len() > 0;
  }

  clone(): PlayerState {
    const n = new PlayerState(this.id, this.seat, this.layout.clone());
    n.pendingPenalty = [...this.pendingPenalty];
    n.connected = this.connected;
    n.removed = this.removed;
    n.turnsCompleted = this.turnsCompleted;
    n.memoryDone = this.memoryDone;
    return n;
  }
}

/** What the King user privately saw, so the follow-up swap refers to the same physical card. */
export interface KingPeekState {
  target: PlayerID;
  targetCard: CardID;
}

/**
 * State is the complete authoritative game. It is treated as a value: apply
 * clones it, mutates the clone, and returns the clone, so a rejected action can
 * never corrupt the caller's state.
 */
export class State {
  config: Config = { cardsPerPlayer: 0, numDecks: 0 };

  /** Immutable master table of every physical card, shared across clones. */
  cards = new Map<CardID, Card>();

  players: PlayerState[] = []; // index == seat, clockwise
  turnIdx = 0;

  phase: Phase = Phase.Memory;

  deck: CardID[] = []; // draw deck; last element is the top
  discard: CardID[] = []; // index 0 is the bottom, last element is the active top

  activeRank: Rank = RankNone; // non-zero only while Phase.Matching
  matchExtensions = 0; // accepted matches this window (+2s each, §5)

  drawn: CardID = NoCard; // private drawn card; non-zero only in Phase.DrawDecision
  drawnBy: PlayerID = "";

  pendingPower: Rank = RankNone; // power awaiting resolution after the window closes
  powerActor: PlayerID = "";
  kingPeek: KingPeekState | null = null;

  declared = false;
  declarer: PlayerID = "";

  paused = false;
  disconnected = new Set<PlayerID>();

  /**
   * Round accounting for the COMBIO gate (§10). roundsCompleted counts rounds
   * ACTUALLY completed by real turns. roundPending is who still owes a real
   * turn in the round currently in progress.
   */
  roundsCompleted = 0;
  roundPending: PlayerID[] = [];

  declareUnlockedFlag = false; // latched once the two-round gate opens

  scores: Map<PlayerID, number> | null = null;
  winners: PlayerID[] = [];

  shuffler: Shuffler = new NoShuffle();

  clone(): State {
    const n = Object.assign(Object.create(State.prototype) as State, this);
    n.config = { ...this.config };
    n.players = this.players.map((p) => p.clone());
    n.deck = [...this.deck];
    n.discard = [...this.discard];
    n.roundPending = [...this.roundPending];
    n.winners = [...this.winners];
    n.disconnected = new Set(this.disconnected);
    n.scores = this.scores ? new Map(this.scores) : null;
    n.kingPeek = this.kingPeek ? { ...this.kingPeek } : null;
    return n;
  }

  // ---------- lookups ----------

  player(id: PlayerID): PlayerState | undefined {
    return this.players.find((p) => p.id === id);
  }

  mustCard(id: CardID): Card {
    return this.cards.get(id)!;
  }

  current(): PlayerState | undefined {
    if (this.turnIdx < 0 || this.turnIdx >= this.players.length) return undefined;
    return this.players[this.turnIdx];
  }

  currentID(): PlayerID {
    return this.current()?.id ?? "";
  }

  activePlayers(): PlayerState[] {
    return this.players.filter((p) => p.active());
  }

  activeCount(): number {
    return this.activePlayers().length;
  }

  /** The active/visible card in the middle. Never empty after setup. */
  discardTop(): CardID {
    return this.discard.length === 0 ? NoCard : this.discard[this.discard.length - 1];
  }

  /**
   * §10: two COMPLETE global rounds must actually have been played. A removal
   * never completes a round, and roundsCompleted only ever increases, so the
   * gate never opens early and never closes again.
   */
  declareUnlocked(): boolean {
    return this.roundsCompleted >= 2;
  }

  /** Seeds the pending set for a new round from the live roster. */
  startRound(): void {
    this.roundPending = this.activePlayers().map((p) => p.id);
  }

  owesTurn(id: PlayerID): boolean {
    return this.roundPending.includes(id);
  }

  /** Marks a REAL completed turn, completing the round when nobody owes one. */
  recordTurn(id: PlayerID): void {
    this.roundPending = this.roundPending.filter((p) => p !== id);
    if (this.roundPending.length === 0) {
      this.roundsCompleted++;
      this.startRound();
    }
  }

  debug(): string {
    let b =
      `phase=${phaseString(this.phase)} turn=${this.currentID()} deck=${this.deck.length} ` +
      `discard=${this.discard.length} rank=${rankString(this.activeRank)} declared=${this.declared} ` +
      `paused=${this.paused} rounds=${this.roundsCompleted} pending=[${this.roundPending.join(" ")}]`;
    for (const p of this.players) {
      b +=
        ` | ${p.id}(seat${p.seat} active=${p.active()} cards=${p.layout.len()} ` +
        `tray=${p.pendingPenalty.length} turns=${p.turnsCompleted})`;
    }
    return b;
  }

  // ---------- pile helpers ----------

  /** Puts a card on top of the pile, making it the visible card. */
  pushDiscardTop(id: CardID): void {
    this.discard.push(id);
  }

  /**
   * Slides a card UNDER the current top (§5). Matched cards and wrong attempts
   * go here: they never become the active rank and never open a new window.
   */
  buryDiscard(id: CardID): void {
    if (this.discard.length === 0) {
      this.discard.push(id);
      return;
    }
    this.discard.splice(this.discard.length - 1, 0, id);
  }

  /**
   * Places cards face-down at the very BOTTOM of the pile (§12): never
   * revealed, never a matching window, but returned by the next reshuffle.
   */
  sinkToBottom(ids: CardID[]): void {
    if (ids.length === 0) return;
    this.discard = [...ids, ...this.discard];
  }

  /** §9: keep the active top card, shuffle everything else back into the deck. */
  reshuffle(): boolean {
    if (this.discard.length <= 1) return false;
    const top = this.discard[this.discard.length - 1];
    const rest = this.discard.slice(0, -1);
    this.shuffler.shuffle(rest);
    this.deck.push(...rest);
    this.discard = [top];
    return true;
  }

  /**
   * Pops the top of the draw deck, reshuffling first if needed. Returns NoCard
   * when no card can physically be produced; callers decide whether that is a
   * legal short-draw (penalties, §6) or an impossible state (a turn draw, §9).
   */
  drawOne(): { id: CardID; reshuffled: boolean } {
    let reshuffled = false;
    if (this.deck.length === 0) reshuffled = this.reshuffle();
    if (this.deck.length === 0) return { id: NoCard, reshuffled };
    return { id: this.deck.pop()!, reshuffled };
  }
}

/** newGame deals a fresh table and stops in Phase.Memory. */
export function newGame(
  cfg: Config,
  ids: PlayerID[],
  sh: Shuffler,
): { state: State; events: GameEvent[] } {
  if (ids.length < 2 || ids.length > 8) throw ErrPlayerCount;
  if (cfg.cardsPerPlayer !== 4 && cfg.cardsPerPlayer !== 6 && cfg.cardsPerPlayer !== 8) {
    throw ErrHandSize;
  }
  const config = { ...cfg };
  if (config.numDecks === 0) config.numDecks = decksFor(ids.length);
  let cards: Card[];
  try {
    cards = buildDeck(config.numDecks);
  } catch (e) {
    throw new GameError((e as Error).message);
  }
  if (cards.length < ids.length * config.cardsPerPlayer + 1) {
    throw new GameError("not enough cards for that table");
  }

  const s = new State();
  s.config = config;
  s.shuffler = sh;
  s.phase = Phase.Memory;
  const deck: CardID[] = [];
  for (const c of cards) {
    s.cards.set(c.id, c);
    deck.push(c.id);
  }
  sh.shuffle(deck);
  s.deck = deck;

  ids.forEach((id, seat) => {
    const hand: CardID[] = [];
    for (let i = 0; i < config.cardsPerPlayer; i++) hand.push(s.drawOne().id);
    s.players.push(new PlayerState(id, seat, new Layout(hand)));
  });

  s.startRound();

  // §2: the initial face-up discard sets no rank and triggers no power.
  s.pushDiscardTop(s.drawOne().id);
  s.activeRank = RankNone;

  return {
    state: s,
    events: [
      pub("game_started", {
        players: [...ids],
        cards_per_player: config.cardsPerPlayer,
        num_decks: config.numDecks,
      }),
      pub("memory_phase_started", { inspect: config.cardsPerPlayer / 2 }),
    ],
  };
}
