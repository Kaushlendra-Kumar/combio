/**
 * The COMBIO wire protocol, typed.
 *
 * Every shape here mirrors a Go type in backend/internal/protocol and
 * backend/internal/engine. If the two ever drift, the integration test in
 * tests/integration/play.test.ts fails, because it round-trips these types
 * against a real server.
 *
 * Two rules this file exists to enforce:
 *   1. No client message carries a player id. The actor is the authenticated
 *      socket, decided server-side. See `ClientMessage` and `ActionArgs`.
 *   2. Nothing here can represent raw engine state. A hidden card is a
 *      `CardHandle`, which has no rank field at all.
 */

export const PROTOCOL_VERSION = 1;

// ---------------------------------------------------------------- primitives

export type PlayerId = string;
export type CardId = number;

/** Rank is the raw number the server sends. 0 means "no active rank". */
export type Rank = number;
export const RANK_NONE = 0;
export const RANK_ACE = 1;
export const RANK_TEN = 10;
export const RANK_JACK = 11;
export const RANK_QUEEN = 12;
export const RANK_KING = 13;

/** Suit: 0 clubs, 1 diamonds, 2 hearts, 3 spades. Never affects matching. */
export type Suit = 0 | 1 | 2 | 3;

/** Deck colour: 0 red, 1 blue. Public for every card, including face-down. */
export type DeckColor = 0 | 1;

/** A fully known card. Only ever sent for cards you are entitled to see. */
export interface Card {
  id: CardId;
  rank: Rank;
  suit: Suit;
  deck: DeckColor;
}

/**
 * A face-down card. Deliberately has no rank or suit: this is the shape the
 * server uses for every hidden card, yours included.
 */
export interface CardHandle {
  position: number;
  id: CardId;
  deck: DeckColor;
}

export type Phase =
  | "memory"
  | "turn_start"
  | "draw_decision"
  | "matching"
  | "power"
  | "king_decision"
  | "finished";

export interface OpponentView {
  id: PlayerId;
  seat: number;
  cards: CardHandle[];
  /** Layout revision, sent back with a match so a lost race reads as stale. */
  version: number;
  count: number;
  pending_penalty: number;
  connected: boolean;
  removed: boolean;
  turns_completed: number;
}

/** Exactly what this browser is allowed to know. Nothing more exists here. */
export interface PlayerView {
  me: PlayerId;
  phase: Phase;

  my_cards: CardHandle[];
  my_pending_count: number;
  my_version: number;

  opponents: OpponentView[];

  turn_player: PlayerId;
  my_turn: boolean;

  deck_count: number;
  discard_count: number;
  /** The one publicly identified card on the table. */
  discard_top: Card | null;
  active_rank: Rank;

  /** Only ever populated for the player who drew it. */
  my_drawn_card: Card | null;
  /** Only ever populated for the player resolving a power. */
  my_pending_power: Rank;

  declare_available: boolean;
  declared: boolean;
  declarer: PlayerId;

  paused: boolean;
  finished: boolean;

  scores?: Record<PlayerId, number>;
  winners?: PlayerId[];
  reveal?: Record<PlayerId, Card[]>;
}

// ------------------------------------------------------------- room payloads

export type RoomStatus =
  | "WAITING"
  | "READY"
  | "STARTING"
  | "PLAYING"
  | "FINAL_TURNS"
  | "FINISHED";

export interface RoomPlayer {
  id: PlayerId;
  name: string;
  ready: boolean;
  connected: boolean;
  removed: boolean;
  host: boolean;
}

export interface RoomStateData {
  code: string;
  public: boolean;
  status: RoomStatus;
  cards_per_player: number;
  max_players: number;
  players: RoomPlayer[];
}

export interface RoomSummary {
  code: string;
  players: number;
  max_players: number;
  cards_per_player: number;
  status: RoomStatus;
}

// ------------------------------------------------------- client -> server

export const C = {
  CreateRoom: "create_room",
  JoinRoom: "join_room",
  LeaveRoom: "leave_room",
  Ready: "ready",
  StartGame: "start_game",
  Action: "action",
  Reconnect: "reconnect",
  Rematch: "rematch",
  ListRooms: "list_rooms",
  Ping: "ping",
} as const;

export type ClientType = (typeof C)[keyof typeof C];

/** Action kinds, one for one with the engine's actions. */
export const A = {
  InspectMemory: "inspect_memory",
  DeclareCombio: "declare_combio",
  DrawCard: "draw_card",
  KeepDrawn: "keep_drawn",
  DiscardDrawn: "discard_drawn",
  AttemptOwnMatch: "attempt_own_match",
  AttemptOpponentMatch: "attempt_opponent_match",
  InsertPenalty: "insert_penalty",
  Peek10: "peek_10",
  JackPeek: "jack_peek",
  QueenSwap: "queen_swap",
  KingPeek: "king_peek",
  KingSwap: "king_swap",
  CancelKingSwap: "cancel_king_swap",
  SkipPower: "skip_power",
} as const;

export type ActionKind = (typeof A)[keyof typeof A];

/**
 * Everything a client may send with an action.
 *
 * `target` is legitimate input: you choose whom to peek at. There is no actor
 * field, and adding one would achieve nothing — the server ignores the body
 * when deciding who is acting.
 */
export interface ActionArgs {
  positions?: number[];
  remove_index?: number;
  insert_index?: number;
  index?: number;
  version?: number;
  target?: PlayerId;
  target_index?: number;
  target_version?: number;
  give_index?: number;
  give_version?: number;
  own_index?: number;
  tray_index?: number;
  position?: number;
}

export interface ActionData {
  kind: ActionKind;
  args?: ActionArgs;
}

export type ClientData =
  | { public: boolean; cards_per_player: number; max_players: number }
  | { code: string }
  | { ready: boolean }
  | ActionData
  | undefined;

export interface ClientMessage {
  v: number;
  type: ClientType;
  id?: string;
  data?: ClientData;
}

// ------------------------------------------------------- server -> client

export const S = {
  Welcome: "welcome",
  RoomState: "room_state",
  PlayerJoined: "player_joined",
  PlayerLeft: "player_left",
  GameStarted: "game_started",
  StateUpdate: "state_update",
  Events: "events",
  Error: "error",
  ReconnectRequired: "reconnect_required",
  GameFinished: "game_finished",
  RoomList: "room_list",
  Pong: "pong",
} as const;

export type ServerType = (typeof S)[keyof typeof S];

export interface WireEvent {
  name: string;
  data?: unknown;
}

export type ServerMessage =
  | { v: number; type: "welcome"; id?: string; data: { version: number; player_id: PlayerId; name: string } }
  | { v: number; type: "room_state"; id?: string; data: RoomStateData }
  | { v: number; type: "player_joined"; id?: string; data: { player: PlayerId; name: string } }
  | { v: number; type: "player_left"; id?: string; data: { player: PlayerId; name: string } }
  | { v: number; type: "game_started"; id?: string; data: { cards_per_player: number; inspect: number } }
  | { v: number; type: "state_update"; id?: string; data: { view: PlayerView } }
  | { v: number; type: "events"; id?: string; data: { events: WireEvent[] } }
  | { v: number; type: "game_finished"; id?: string; data: { view: PlayerView } }
  | { v: number; type: "reconnect_required"; id?: string; data: { code: string; reason: string } }
  | { v: number; type: "room_list"; id?: string; data: { rooms: RoomSummary[] } }
  | { v: number; type: "error"; id?: string; data: { code: string; message: string } }
  | { v: number; type: "pong"; id?: string; data?: undefined };

// ------------------------------------------------------------------ parsing

export class ProtocolError extends Error {
  constructor(
    message: string,
    readonly kind: "malformed" | "unsupported_version",
  ) {
    super(message);
    this.name = "ProtocolError";
  }
}

function isObject(v: unknown): v is Record<string, unknown> {
  return typeof v === "object" && v !== null && !Array.isArray(v);
}

/**
 * Parse a frame from the socket.
 *
 * Anything that is not a well-formed, current-version message of a type we
 * know is rejected here rather than being allowed to reach the reducer as a
 * half-typed object. Unknown message *types* are surfaced as malformed so a
 * future server can add one without this client silently mis-handling it.
 */
export function parseServerMessage(raw: string): ServerMessage {
  let value: unknown;
  try {
    value = JSON.parse(raw);
  } catch {
    throw new ProtocolError("server sent something that is not JSON", "malformed");
  }
  if (!isObject(value)) {
    throw new ProtocolError("server frame was not an object", "malformed");
  }
  if (typeof value.v !== "number") {
    throw new ProtocolError("server frame has no protocol version", "malformed");
  }
  if (value.v !== PROTOCOL_VERSION) {
    throw new ProtocolError(
      `this client speaks v${PROTOCOL_VERSION}, the server sent v${value.v}`,
      "unsupported_version",
    );
  }
  if (typeof value.type !== "string") {
    throw new ProtocolError("server frame has no type", "malformed");
  }
  const known: string[] = Object.values(S);
  if (!known.includes(value.type)) {
    throw new ProtocolError(`unknown server message type: ${value.type}`, "malformed");
  }
  if (value.data !== undefined && !isObject(value.data)) {
    throw new ProtocolError(`${value.type} carried a non-object payload`, "malformed");
  }
  return value as unknown as ServerMessage;
}

/** Build a client frame. There is no place to put a player id, by design. */
export function clientMessage(type: ClientType, data?: ClientData, id?: string): ClientMessage {
  const msg: ClientMessage = { v: PROTOCOL_VERSION, type };
  if (id !== undefined) msg.id = id;
  if (data !== undefined) msg.data = data;
  return msg;
}

// ------------------------------------------------------- presentation only

const RANK_LABEL: Record<number, string> = {
  1: "A",
  10: "10",
  11: "J",
  12: "Q",
  13: "K",
};

/** Presentational: how a rank reads on a card face. */
export function rankLabel(rank: Rank): string {
  return RANK_LABEL[rank] ?? String(rank);
}

const SUIT_LABEL = ["\u2663", "\u2666", "\u2665", "\u2660"] as const;

export function suitLabel(suit: Suit): string {
  return SUIT_LABEL[suit] ?? "?";
}

export function suitIsRed(suit: Suit): boolean {
  return suit === 1 || suit === 2;
}

/**
 * Presentational only: the printed value of a card face the server has already
 * shown us. This is not scoring — the server sends final scores, and this
 * client never adds anything up to decide an outcome.
 */
export function printedValue(rank: Rank): number {
  return rank === RANK_ACE ? -1 : rank;
}

export function powerLabel(rank: Rank): string {
  switch (rank) {
    case RANK_TEN:
      return "10 — peek at one of your own cards";
    case RANK_JACK:
      return "Jack — look at one opponent card";
    case RANK_QUEEN:
      return "Queen — blind swap (optional)";
    case RANK_KING:
      return "King — look, then swap or cancel";
    default:
      return "";
  }
}

export function phaseLabel(phase: Phase): string {
  switch (phase) {
    case "memory":
      return "Memory phase";
    case "turn_start":
      return "Turn";
    case "draw_decision":
      return "Deciding";
    case "matching":
      return "Matching window";
    case "power":
      return "Power";
    case "king_decision":
      return "King decision";
    case "finished":
      return "Finished";
  }
}
