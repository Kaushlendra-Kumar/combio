// The versioned WebSocket wire format.
//
// It deliberately exposes only the PlayerView / Event model. Raw engine State
// has no wire representation here and must never acquire one.

import type { Action } from "./engine/actions";
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
  errorIs,
  GameError,
  sentinel,
  wrap,
} from "./engine/errors";
import type { GameEvent } from "./engine/events";
import type { PlayerID } from "./engine/state";
import type { PlayerView } from "./engine/view";

/** Bumped whenever the wire format changes incompatibly. */
export const Version = 1;

// ---------- envelopes ----------

/**
 * ClientMessage is anything the browser sends. Note what is NOT here: a player
 * id. The actor is always taken from the authenticated connection.
 */
export interface ClientMessage {
  v: number;
  type: string;
  id: string;
  data: unknown; // raw JSON value; undefined when absent
}

/** ServerMessage is anything the server sends. `id` and `data` are omitted when empty. */
export interface ServerMessage {
  v: number;
  type: string;
  id?: string;
  data?: unknown;
}

export function msg(type: string, data?: unknown): ServerMessage {
  return reply("", type, data);
}

export function reply(id: string, type: string, data?: unknown): ServerMessage {
  const m: ServerMessage = { v: Version, type };
  if (id) m.id = id;
  if (data !== undefined && data !== null) m.data = data;
  return m;
}

// Client -> server message types.
export const CCreateRoom = "create_room";
export const CJoinRoom = "join_room";
export const CLeaveRoom = "leave_room";
export const CReady = "ready";
export const CStartGame = "start_game";
export const CAction = "action";
export const CReconnect = "reconnect";
export const CRematch = "rematch";
export const CListRooms = "list_rooms";
export const CPing = "ping";

// Server -> client message types.
export const SWelcome = "welcome";
export const SRoomState = "room_state";
export const SPlayerJoined = "player_joined";
export const SPlayerLeft = "player_left";
export const SGameStarted = "game_started";
export const SStateUpdate = "state_update";
export const SEvents = "events";
export const SError = "error";
export const SReconnectRequired = "reconnect_required";
export const SGameFinished = "game_finished";
export const SRoomList = "room_list";
export const SPong = "pong";

// ---------- payloads ----------

export interface CreateRoomData {
  public: boolean;
  cards_per_player: number;
  max_players: number;
}

export interface JoinRoomData {
  code: string;
}

export interface ReadyData {
  ready: boolean;
}

/** The gameplay envelope. Any "player" field a client puts in here is ignored. */
export interface ActionData {
  kind: string;
  args: unknown;
}

export interface RoomPlayer {
  id: PlayerID;
  name: string;
  ready: boolean;
  connected: boolean;
  removed: boolean;
  host: boolean;
}

export interface RoomStateData {
  code: string;
  public: boolean;
  status: string;
  cards_per_player: number;
  max_players: number;
  players: RoomPlayer[];
}

export interface RoomSummary {
  code: string;
  players: number;
  max_players: number;
  cards_per_player: number;
  status: string;
}

export interface WireEvent {
  name: string;
  data: Record<string, unknown>;
}

export interface StateUpdateData {
  view: PlayerView;
}

/** Renders already-filtered events for one recipient. The caller MUST filter first. */
export function encodeEvents(evs: GameEvent[]): WireEvent[] {
  return evs.map((e) => ({ name: e.name, data: e.data }));
}

// ---------- JSON decoding with Go's encoding/json rules ----------
//
// The Go server decoded into typed structs, so these rules are what clients
// already rely on: unknown fields are ignored, null leaves a field at its zero
// value, field names match case-insensitively, and a value of the wrong type is
// an error.

type FieldType = "int" | "uint" | "string" | "player" | "bool" | "[]int" | "raw";

type Schema = Record<string, FieldType>;

function jsonKind(v: unknown): string {
  if (Array.isArray(v)) return "array";
  if (typeof v === "number") return "number";
  if (typeof v === "string") return "string";
  if (typeof v === "boolean") return "bool";
  return "object";
}

const goTypes: Record<FieldType, string> = {
  int: "int",
  uint: "uint64",
  string: "string",
  player: "engine.PlayerID",
  bool: "bool",
  "[]int": "[]int",
  raw: "json.RawMessage",
};

function zero(t: FieldType): unknown {
  switch (t) {
    case "int":
    case "uint":
      return 0;
    case "string":
    case "player":
      return "";
    case "bool":
      return false;
    case "[]int":
      return [];
    case "raw":
      return undefined;
  }
}

function checkValue(t: FieldType, v: unknown): boolean {
  switch (t) {
    case "int":
      return typeof v === "number" && Number.isSafeInteger(v);
    case "uint":
      return typeof v === "number" && Number.isSafeInteger(v) && v >= 0;
    case "string":
    case "player":
      return typeof v === "string";
    case "bool":
      return typeof v === "boolean";
    case "[]int":
      return Array.isArray(v) && v.every((x) => typeof x === "number" && Number.isSafeInteger(x));
    case "raw":
      return true;
  }
}

/**
 * decodeStruct decodes a raw JSON value into an object with the given schema,
 * the way json.Unmarshal would into a Go struct named `goName`.
 */
function decodeStruct<T>(raw: unknown, schema: Schema, goName: string): T {
  const out: Record<string, unknown> = {};
  for (const [k, t] of Object.entries(schema)) out[k] = zero(t);
  if (raw === undefined || raw === null) return out as T;
  if (typeof raw !== "object" || Array.isArray(raw)) {
    throw new GameError(`json: cannot unmarshal ${jsonKind(raw)} into Go value of type ${goName}`);
  }
  const obj = raw as Record<string, unknown>;
  const keys = Object.keys(obj);
  for (const [field, t] of Object.entries(schema)) {
    const key = field in obj ? field : keys.find((k) => k.toLowerCase() === field);
    if (key === undefined) continue;
    const v = obj[key];
    if (v === null) continue;
    if (!checkValue(t, v)) {
      const shortName = goName.slice(goName.lastIndexOf(".") + 1);
      // A bad element inside []int is reported against the element type.
      const [bad, type] =
        t === "[]int" && Array.isArray(v)
          ? [v.find((x) => !checkValue("int", x)), "int"]
          : [v, goTypes[t]];
      // Go names the offending literal only when a number fails to fit a
      // numeric field ("number -1"); otherwise it gives just the JSON kind.
      const numeric = type === "int" || type === "uint64";
      const what = typeof bad === "number" && numeric ? `number ${bad}` : jsonKind(bad);
      throw new GameError(
        `json: cannot unmarshal ${what} into Go struct field ${shortName}.${field} of type ${type}`,
      );
    }
    out[field] = t === "[]int" ? [...(v as number[])] : v;
  }
  return out as T;
}

export const ErrUnknownAction = sentinel("unknown action kind");
/** A frame whose envelope version this server does not speak. */
export const ErrUnsupportedVersion = sentinel("unsupported protocol version");
/** A frame that is not valid protocol JSON. */
export const ErrMalformed = sentinel("malformed message");

/** Parses one text frame. Throws (any error) when the frame is not protocol JSON. */
export function parseClientMessage(text: string): ClientMessage {
  const raw: unknown = JSON.parse(text);
  return decodeStruct<ClientMessage>(
    raw,
    { v: "int", type: "string", id: "string", data: "raw" },
    "protocol.ClientMessage",
  );
}

export function decodeCreateRoom(raw: unknown): CreateRoomData {
  return decodeStruct(
    raw,
    { public: "bool", cards_per_player: "int", max_players: "int" },
    "protocol.CreateRoomData",
  );
}

export function decodeJoinRoom(raw: unknown): JoinRoomData {
  return decodeStruct(raw, { code: "string" }, "protocol.JoinRoomData");
}

export function decodeReady(raw: unknown): ReadyData {
  return decodeStruct(raw, { ready: "bool" }, "protocol.ReadyData");
}

export function decodeAction(raw: unknown): ActionData {
  return decodeStruct(raw, { kind: "string", args: "raw" }, "protocol.ActionData");
}

// ---------- action building ----------

interface Args {
  positions: number[];
  remove_index: number;
  insert_index: number;
  index: number;
  version: number;
  target: string;
  target_index: number;
  target_version: number;
  give_index: number;
  give_version: number;
  own_index: number;
  tray_index: number;
  position: number;
}

/**
 * The union of every gameplay argument a client may send. There is
 * deliberately no player/actor field: a target IS client input, but who is
 * acting never is.
 */
const argsSchema: Schema = {
  positions: "[]int",
  remove_index: "int",
  insert_index: "int",
  index: "int",
  version: "uint",
  target: "player",
  target_index: "int",
  target_version: "uint",
  give_index: "int",
  give_version: "uint",
  own_index: "int",
  tray_index: "int",
  position: "int",
};

/**
 * buildAction turns a wire action into an engine action. `actor` comes from
 * the authenticated connection and is the ONLY source of the acting player:
 * this is the single place an action is constructed, so the actor-authority
 * rule is enforced in exactly one spot.
 */
export function buildAction(kind: string, raw: unknown, actor: PlayerID): Action {
  let a: Args;
  try {
    // A null args value decodes to all zeroes, just like an absent one.
    a = decodeStruct<Args>(raw, argsSchema, "protocol.args");
  } catch (e) {
    throw new GameError(`bad action args: ${(e as Error).message}`);
  }
  const player = actor;
  switch (kind) {
    case "inspect_memory":
      return { kind, player, positions: a.positions };
    case "declare_combio":
    case "draw_card":
    case "discard_drawn":
    case "cancel_king_swap":
    case "skip_power":
      return { kind, player };
    case "keep_drawn":
      return { kind, player, removeIndex: a.remove_index, insertIndex: a.insert_index };
    case "attempt_own_match":
      return { kind, player, index: a.index, version: a.version };
    case "attempt_opponent_match":
      return {
        kind,
        player,
        target: a.target,
        targetIndex: a.target_index,
        targetVersion: a.target_version,
        giveIndex: a.give_index,
        giveVersion: a.give_version,
      };
    case "insert_penalty":
      return { kind, player, trayIndex: a.tray_index, position: a.position };
    case "peek_10":
      return { kind, player, index: a.index };
    case "jack_peek":
    case "king_peek":
      return { kind, player, target: a.target, targetIndex: a.target_index };
    case "queen_swap":
      return {
        kind,
        player,
        ownIndex: a.own_index,
        target: a.target,
        targetIndex: a.target_index,
      };
    case "king_swap":
      return { kind, player, ownIndex: a.own_index };
  }
  throw wrap(ErrUnknownAction, JSON.stringify(kind));
}

const codes: [GameError, string][] = [
  [ErrStale, "stale"],
  [ErrNotYourTurn, "not_your_turn"],
  [ErrWrongPhase, "wrong_phase"],
  [ErrUnknownPlayer, "unknown_player"],
  [ErrPlayerInactive, "player_inactive"],
  [ErrGameOver, "game_over"],
  [ErrPaused, "paused"],
  [ErrBadIndex, "bad_index"],
  [ErrDeclareLocked, "declare_locked"],
  [ErrAlreadyDeclared, "already_declared"],
  [ErrAlreadyDrawn, "already_drawn"],
  [ErrMemoryCount, "memory_count"],
  [ErrAlreadyInspected, "already_inspected"],
  [ErrPowerMandatory, "power_mandatory"],
  [ErrNoCardToGive, "no_card_to_give"],
  [ErrTargetEmpty, "target_empty"],
  [ErrSelfTarget, "self_target"],
  [ErrNoPendingTray, "no_pending_tray"],
  [ErrImpossibleState, "internal"],
  [ErrUnknownAction, "unknown_action"],
  [ErrUnsupportedVersion, "unsupported_version"],
  [ErrMalformed, "malformed"],
];

/**
 * errorCode maps an error to a stable wire code so clients can react without
 * string matching. A lost click race is "stale": nothing happened.
 */
export function errorCode(err: unknown): string {
  for (const [e, code] of codes) if (errorIs(err, e)) return code;
  return "rejected";
}

/**
 * Builds an error reply. Engine error text never contains card identities, so
 * it is safe to pass through to the player who caused it.
 */
export function errorMsg(id: string, err: unknown): ServerMessage {
  const message = err instanceof Error ? err.message : String(err);
  return reply(id, SError, { code: errorCode(err), message });
}
