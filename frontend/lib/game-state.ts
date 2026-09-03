import type { ConnectionState } from "./websocket";
import {
  type Card,
  type PlayerView,
  type RoomStateData,
  type ServerMessage,
  type WireEvent,
  S,
} from "./protocol";
import { errorMessage, isBenign } from "./errors";

/**
 * The client's whole picture of the game.
 *
 * Every gameplay field here is copied straight from a server message. Nothing
 * is computed, inferred or remembered across updates: when a `state_update`
 * arrives it REPLACES the view wholesale, which is what stops a card the
 * server has stopped showing us from lingering in React state.
 *
 * The only locally-owned fields are `connection`, `feed`, `notice` and
 * `pendingReveals` — connection status and transient UI chrome.
 */
export interface GameState {
  connection: ConnectionState;
  session: { playerId: string; name: string } | null;

  room: RoomStateData | null;
  /** The authoritative per-player view. Never merged, only replaced. */
  view: PlayerView | null;
  started: boolean;
  inspectCount: number;

  /** Transient: private reveals, shown once then dropped. */
  pendingReveals: Reveal[];
  /** Public event feed, newest last. Presentation only. */
  feed: FeedLine[];
  /** The current banner, if any. */
  notice: Notice | null;
  /** Set when the server tells us to reconnect. */
  reconnectRequired: string | null;
}

export interface Reveal {
  key: string;
  owner: string;
  position: number;
  card: Card;
  label: string;
}

export interface FeedLine {
  key: string;
  text: string;
}

export interface Notice {
  key: string;
  code: string;
  text: string;
  tone: "error" | "info";
}

export const initialState: GameState = {
  connection: "idle",
  session: null,
  room: null,
  view: null,
  started: false,
  inspectCount: 0,
  pendingReveals: [],
  feed: [],
  notice: null,
  reconnectRequired: null,
};

export type Action =
  | { type: "connection"; state: ConnectionState }
  | { type: "server"; msg: ServerMessage }
  | { type: "protocol-error"; kind: string; message: string }
  | { type: "dismiss-notice" }
  | { type: "consume-reveal"; key: string }
  | { type: "leave" };

let counter = 0;
function key(): string {
  counter += 1;
  return `k${counter}`;
}

/** Reset for deterministic test output. */
export function resetKeys(): void {
  counter = 0;
}

const FEED_LIMIT = 40;

function pushFeed(feed: FeedLine[], text: string): FeedLine[] {
  const next = [...feed, { key: key(), text }];
  return next.length > FEED_LIMIT ? next.slice(next.length - FEED_LIMIT) : next;
}

/**
 * Turn a server event into a feed line.
 *
 * This is presentation, not rules: it reads fields the server already decided
 * and never draws a conclusion of its own.
 */
function describe(ev: WireEvent, nameOf: (id: string) => string): string | null {
  const d = (ev.data ?? {}) as Record<string, unknown>;
  const who = (k: string): string => nameOf(String(d[k] ?? ""));
  const cardOf = (k: string): string => {
    const c = d[k] as Card | undefined;
    return c ? `${c.rank}` : "a card";
  };
  switch (ev.name) {
    case "game_started":
      return "Game started.";
    case "memory_phase_started":
      return `Memory phase: look at ${String(d.inspect ?? "")} of your cards.`;
    case "memory_phase_ended":
      return "Memory phase over \u2014 remember them.";
    case "turn_started":
      return d.final_turn ? `Final turn: ${who("player")}` : `${who("player")}'s turn`;
    case "declare_unlocked":
      return "Two rounds played \u2014 COMBIO is now available.";
    case "card_drawn":
      return `${who("player")} drew a card.`;
    case "card_kept":
      return `${who("player")} kept the drawn card.`;
    case "card_discarded":
      return `${who("player")} discarded a ${cardOf("card")}.`;
    case "matching_window_opened":
      return `Matching window: rank ${String(d.active_rank ?? "")}!`;
    case "matching_window_closed":
      return "Matching window closed.";
    case "match_accepted":
      return d.player === d.owner
        ? `${who("player")} matched from their own hand.`
        : `${who("player")} knocked a card out of ${who("owner")}'s hand.`;
    case "match_failed":
      return `${who("player")} guessed wrong.`;
    case "penalty_applied":
      return `${who("player")} takes ${String(d.count ?? "")} penalty card(s).`;
    case "penalty_placed":
      return `${who("player")} placed a penalty card.`;
    case "card_transferred":
      return `${who("from")} handed a card to ${who("to")}.`;
    case "cards_swapped":
      return `${who("player")} swapped a card with ${who("target")}.`;
    case "power_skipped":
      return `${who("player")}'s power was not used.`;
    case "combio_declared":
      return `\u2605 ${who("player")} declares COMBIO!`;
    case "deck_reshuffled":
      return "Draw deck ran out \u2014 discard pile reshuffled.";
    case "turn_timed_out":
      return `${who("player")} ran out of time.`;
    case "player_disconnected":
      return `${who("player")} disconnected \u2014 game paused.`;
    case "player_reconnected":
      return `${who("player")} reconnected.`;
    case "player_removed":
      return `${who("player")} was removed from the game.`;
    case "game_finished":
      return "Game over.";
    default:
      return null;
  }
}

/** Private events carry a card the server decided we may see, once. */
function toReveal(ev: WireEvent): Reveal[] {
  const d = (ev.data ?? {}) as Record<string, unknown>;
  switch (ev.name) {
    case "memory_revealed": {
      const cards = (d.cards ?? []) as Card[];
      const positions = (d.positions ?? []) as number[];
      return cards.map((card, i) => ({
        key: key(),
        owner: "me",
        position: positions[i] ?? i,
        card,
        label: "Memorise this",
      }));
    }
    case "private_draw": {
      const card = d.card as Card | undefined;
      return card ? [{ key: key(), owner: "me", position: -1, card, label: "You drew" }] : [];
    }
    case "private_reveal": {
      const card = d.card as Card | undefined;
      if (!card) return [];
      return [
        {
          key: key(),
          owner: String(d.owner ?? ""),
          position: Number(d.position ?? -1),
          card,
          label: "You see",
        },
      ];
    }
    default:
      return [];
  }
}

export function reduce(state: GameState, action: Action): GameState {
  switch (action.type) {
    case "connection":
      return { ...state, connection: action.state };

    case "dismiss-notice":
      return { ...state, notice: null };

    case "consume-reveal":
      return {
        ...state,
        pendingReveals: state.pendingReveals.filter((r) => r.key !== action.key),
      };

    case "leave":
      return {
        ...initialState,
        connection: state.connection,
        session: state.session,
      };

    case "protocol-error":
      return {
        ...state,
        notice: {
          key: key(),
          code: action.kind,
          text: errorMessage(action.kind, action.message),
          tone: "error",
        },
      };

    case "server":
      return applyServer(state, action.msg);
  }
}

function nameLookup(state: GameState): (id: string) => string {
  return (id: string) => {
    if (!id) return "someone";
    if (state.session && id === state.session.playerId) return "you";
    const p = state.room?.players.find((x) => x.id === id);
    return p ? p.name : id;
  };
}

function applyServer(state: GameState, msg: ServerMessage): GameState {
  switch (msg.type) {
    case S.Welcome:
      return {
        ...state,
        session: { playerId: msg.data.player_id, name: msg.data.name },
      };

    case S.RoomState:
      // Replaces the roster wholesale: the server's list is the list.
      return { ...state, room: msg.data, reconnectRequired: null };

    case S.PlayerJoined:
      return { ...state, feed: pushFeed(state.feed, `${msg.data.name} joined.`) };

    case S.PlayerLeft:
      return { ...state, feed: pushFeed(state.feed, `${msg.data.name} left.`) };

    case S.GameStarted:
      return {
        ...state,
        started: true,
        inspectCount: msg.data.inspect,
        feed: pushFeed(state.feed, "Game started."),
      };

    case S.StateUpdate:
      // Wholesale replacement. Anything the server no longer shows us is gone
      // from this client the moment this lands.
      return { ...state, view: msg.data.view, started: true };

    case S.GameFinished:
      return { ...state, view: msg.data.view, started: true };

    case S.Events: {
      const nameOf = nameLookup(state);
      let feed = state.feed;
      let reveals = state.pendingReveals;
      for (const ev of msg.data.events) {
        const line = describe(ev, nameOf);
        if (line) feed = pushFeed(feed, line);
        const r = toReveal(ev);
        if (r.length) reveals = [...reveals, ...r];
      }
      return { ...state, feed, pendingReveals: reveals };
    }

    case S.ReconnectRequired:
      return { ...state, reconnectRequired: msg.data.reason };

    case S.Error: {
      const { code, message } = msg.data;
      const text = errorMessage(code, message);
      // Don't stack identical banners when a player mashes a button.
      if (state.notice && state.notice.code === code) return state;
      return {
        ...state,
        notice: { key: key(), code, text, tone: isBenign(code) ? "info" : "error" },
      };
    }

    case S.RoomList:
    case S.Pong:
      return state;
  }
}

// ---------------------------------------------------------- selectors (UI)

/**
 * These read facts the server already sent. None of them decides whether a
 * move is legal: they decide whether a button is worth rendering. The server
 * remains the authority, and a rejected action shows its error.
 */

export function canAct(state: GameState): boolean {
  const v = state.view;
  return !!v && !v.paused && !v.finished && state.connection === "open";
}

export function isMatchingOpen(state: GameState): boolean {
  const v = state.view;
  return !!v && v.phase === "matching" && v.active_rank !== 0;
}

export function myTurnPhase(state: GameState): "draw" | "decide" | "power" | "king" | null {
  const v = state.view;
  if (!v || !canAct(state)) return null;
  if (v.phase === "turn_start" && v.my_turn) return "draw";
  if (v.phase === "draw_decision" && v.my_drawn_card) return "decide";
  if (v.phase === "power" && v.my_pending_power !== 0) return "power";
  if (v.phase === "king_decision" && v.my_pending_power !== 0) return "king";
  return null;
}

export function playerName(state: GameState, id: string): string {
  return nameLookup(state)(id);
}
