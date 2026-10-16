// A room owns one live game. In Go every room was its own goroutine; here the
// whole server runs inside one Durable Object, which is single-threaded, so
// every transition for a game is already serialized and the engine stays pure.

import type { Action, TimerKind } from "./engine/actions";
import type { Rank } from "./engine/card";
import { SeededShuffler } from "./engine/deck";
import { GameError } from "./engine/errors";
import { filterEvents, type GameEvent } from "./engine/events";
import { newGame, Phase, phaseString, type PlayerID, type State } from "./engine/state";
import { apply } from "./engine/transition";
import { viewFor } from "./engine/view";
import {
  type ActionData,
  buildAction,
  encodeEvents,
  errorMsg,
  msg,
  type RoomPlayer,
  type RoomStateData,
  SEvents,
  SGameFinished,
  SGameStarted,
  SPlayerJoined,
  SPlayerLeft,
  SReconnectRequired,
  SRoomState,
  SStateUpdate,
  type ServerMessage,
} from "./protocol";

/** The room lifecycle. */
export type Status = "WAITING" | "READY" | "STARTING" | "PLAYING" | "FINISHED";

export const ErrRoomFull = new GameError("room is full");
export const ErrDisconnected = new GameError("your connection is not attached to this seat");
export const ErrInProgress = new GameError("game already in progress");
export const ErrNotHost = new GameError("only the host can start the game");
export const ErrNotReady = new GameError(
  "every player must be ready, and there must be at least two",
);
export const ErrAlreadyStarted = new GameError("the game has already started");
export const ErrNotInRoom = new GameError("you are not in this room");
export const ErrNotFinished = new GameError("the game is not over yet");

/** Client is one connected player. send must never block. */
export interface Client {
  readonly id: PlayerID;
  readonly name: string;
  send(m: ServerMessage): void;
  close(): void;
}

/** Clock is the room's only access to time, so tests can drive timers by hand. */
export interface Clock {
  now(): number; // milliseconds
  after(ms: number, fn: () => void): () => void; // returns a cancel function
}

export const realClock: Clock = {
  now: () => Date.now(),
  after: (ms, fn) => {
    const t = setTimeout(fn, ms);
    return () => clearTimeout(t);
  },
};

/** Server-owned durations in milliseconds. The engine itself holds no clock. */
export interface Timers {
  memory: number;
  turn: number;
  match: number;
  matchExtension: number;
  power: number;
  reconnect: number;
}

export function defaultTimers(): Timers {
  return {
    memory: 60_000,
    // A minute per turn: in a browser you have to read the board, click, and
    // think about what you remember, so 30s ran out mid-decision.
    turn: 60_000,
    match: 5_000,
    matchExtension: 2_000,
    power: 15_000,
    reconnect: 30_000,
  };
}

export interface RoomConfig {
  code: string;
  public: boolean;
  cardsPerPlayer: number;
  maxPlayers: number;
  seed: number;
  timers: Timers;
  clock: Clock;
}

interface Seat {
  id: PlayerID;
  name: string;
  ready: boolean;
  client: Client | null;
  removed: boolean;
}

/**
 * Which timed phase a timer belongs to. A timer only fires if its scope and
 * generation still match, which stops a window's timer from closing the window
 * after it.
 */
interface Scope {
  kind: TimerKind;
  key: string;
}

interface Scheduled {
  sc: Scope;
  gen: number;
  deadline: number;
  remaining: number;
  cancel: (() => void) | null;
  frozen: boolean;
}

/** A snapshot for the manager. */
export interface Info {
  code: string;
  public: boolean;
  status: Status;
  host: PlayerID;
  cardsPerPlayer: number;
  maxPlayers: number;
  players: RoomPlayer[];
  started: boolean;
  phase: string;
  paused: boolean;
  activeRank: Rank;
  empty: boolean;
}

export class Room {
  private status: Status = "WAITING";
  private host: PlayerID = "";
  private seats: Seat[] = [];
  private st: State | null = null;
  private started = false;
  private closed = false;

  private phaseTimer: Scheduled | null = null;
  private timerGen = 0;
  private extensionPending = false;
  // How many hands this room has dealt, so a rematch gets a different deal.
  private games = 0;

  private reconnect = new Map<PlayerID, () => void>();
  private reconnectGen = new Map<PlayerID, number>();

  constructor(readonly cfg: RoomConfig) {
    if (cfg.maxPlayers < 2 || cfg.maxPlayers > 8) cfg.maxPlayers = 8;
    if (cfg.cardsPerPlayer !== 4 && cfg.cardsPerPlayer !== 6 && cfg.cardsPerPlayer !== 8) {
      cfg.cardsPerPlayer = 4;
    }
  }

  get code(): string {
    return this.cfg.code;
  }

  /** Stops the room: every timer is cancelled and further calls are ignored. */
  close(): void {
    if (this.closed) return;
    this.closed = true;
    this.stopPhaseTimer();
    for (const cancel of this.reconnect.values()) cancel();
    this.reconnect.clear();
  }

  // ---------- public API ----------

  /**
   * join attaches a client. It doubles as reconnect: if the player already has
   * a seat in a running game, their session is restored instead.
   */
  join(c: Client, cid: string): void {
    if (this.closed) return;
    const existing = this.seatOf(c.id);

    if (!existing) {
      if (this.started) {
        // Only a player who already has a seat may enter a running game.
        c.send(errorMsg(cid, ErrInProgress));
        return;
      }
      if (this.seats.length >= this.cfg.maxPlayers) {
        c.send(errorMsg(cid, ErrRoomFull));
        return;
      }
      const s: Seat = { id: c.id, name: c.name, ready: false, client: c, removed: false };
      this.seats.push(s);
      if (this.host === "") this.host = s.id;
      this.refreshStatus();
      this.broadcastMsg(msg(SPlayerJoined, { player: s.id, name: s.name }));
      this.broadcastRoomState();
      return;
    }

    // Reconnect, or a second connection replacing the first. Re-attaching a
    // socket that is already attached is a no-op replay, not a reconnect.
    const wasDetached = existing.client === null;
    const previous = existing.client;
    existing.client = c;
    existing.name = c.name;
    if (previous && previous !== c) {
      previous.send(
        msg(SReconnectRequired, {
          code: this.cfg.code,
          reason: "replaced by a newer connection",
        }),
      );
      previous.close();
    }

    if (this.started && !existing.removed && wasDetached) {
      this.cancelReconnect(existing.id);
      this.applySystem({ kind: "player_reconnected", player: existing.id });
    }
    this.broadcastRoomState();
    if (this.started) {
      // Give the returning player their private view immediately.
      this.sendState(existing);
      if (this.st!.phase === Phase.Finished) {
        c.send(msg(SGameFinished, { view: viewFor(this.st!, existing.id) }));
      }
    }
  }

  leave(id: PlayerID): void {
    if (this.closed) return;
    const s = this.seatOf(id);
    if (!s) return;
    if (this.started) {
      // Leaving a live game is a disconnect: the rules decide what happens.
      s.client = null;
      this.onDisconnected(id);
      return;
    }
    this.seats = this.seats.filter((x) => x.id !== id);
    if (this.host === id && this.seats.length > 0) this.host = this.seats[0].id;
    this.refreshStatus();
    this.broadcastMsg(msg(SPlayerLeft, { player: id, name: s.name }));
    this.broadcastRoomState();
  }

  setReady(id: PlayerID, ready: boolean, cid: string): void {
    if (this.closed) return;
    const s = this.seatOf(id);
    if (!s) return;
    if (this.started) {
      this.sendTo(id, errorMsg(cid, ErrAlreadyStarted));
      return;
    }
    s.ready = ready;
    this.refreshStatus();
    this.broadcastRoomState();
  }

  start(id: PlayerID, cid: string): void {
    if (this.closed) return;
    if (this.started) {
      this.sendTo(id, errorMsg(cid, ErrAlreadyStarted));
      return;
    }
    if (id !== this.host) {
      this.sendTo(id, errorMsg(cid, ErrNotHost));
      return;
    }
    if (this.status !== "READY") {
      this.sendTo(id, errorMsg(cid, ErrNotReady));
      return;
    }
    let dealt: { state: State; events: GameEvent[] };
    try {
      dealt = newGame(
        { cardsPerPlayer: this.cfg.cardsPerPlayer, numDecks: 0 },
        this.seats.map((s) => s.id),
        new SeededShuffler(this.cfg.seed + this.games),
      );
    } catch (e) {
      this.sendTo(id, errorMsg(cid, e));
      return;
    }
    this.st = dealt.state;
    this.started = true;
    this.games++;
    this.status = "STARTING";
    this.broadcastMsg(
      msg(SGameStarted, {
        cards_per_player: this.cfg.cardsPerPlayer,
        inspect: this.cfg.cardsPerPlayer / 2,
      }),
    );
    this.broadcastRoomState();
    this.noteEvents(dealt.events);
    this.broadcast(dealt.events);
    this.syncTimers();
  }

  /**
   * rematch discards the finished game and returns to the lobby. Seats are
   * kept, ready flags cleared, and anyone the last game removed is welcome
   * back. Players whose socket has gone are dropped.
   */
  rematch(id: PlayerID, cid: string): void {
    if (this.closed) return;
    if (!this.seatOf(id)) {
      this.sendTo(id, errorMsg(cid, ErrNotInRoom));
      return;
    }
    if (this.status !== "FINISHED") {
      this.sendTo(id, errorMsg(cid, ErrNotFinished));
      return;
    }

    this.stopPhaseTimer();
    for (const pid of [...this.reconnect.keys()]) this.cancelReconnect(pid);

    this.seats = this.seats.filter((s) => s.client !== null);
    for (const s of this.seats) {
      s.ready = false;
      s.removed = false;
    }
    if (!this.seatOf(this.host) && this.seats.length > 0) this.host = this.seats[0].id;

    this.started = false;
    this.st = null;
    this.extensionPending = false;

    this.refreshStatus();
    this.broadcastRoomState();
  }

  /**
   * act is the only way a gameplay action enters the room. The actor comes
   * from the authenticated connection; nothing in data can change it.
   */
  act(actor: PlayerID, data: ActionData, cid: string): void {
    if (this.closed) return;
    if (!this.started) {
      this.sendTo(actor, errorMsg(cid, ErrNotReady));
      return;
    }
    const seat = this.seatOf(actor);
    if (!seat) {
      this.sendTo(actor, errorMsg(cid, ErrNotInRoom));
      return;
    }
    // A seat with no live connection cannot act.
    if (seat.client === null) return;
    if (seat.removed) {
      this.sendTo(actor, errorMsg(cid, ErrDisconnected));
      return;
    }
    let a: Action;
    try {
      a = buildAction(data.kind, data.args, actor);
    } catch (e) {
      this.sendTo(actor, errorMsg(cid, e));
      return;
    }
    const res = apply(this.st!, a);
    if (!res.ok) {
      // A lost click race arrives here as ErrStale: rejected, free, private.
      this.sendTo(actor, errorMsg(cid, res.error));
      return;
    }
    this.st = res.state;
    this.noteEvents(res.events);
    this.broadcast(res.events);
    this.refreshStatus();
    this.syncTimers();
  }

  /**
   * disconnected reports that a client's socket dropped. Only the client that
   * currently holds the seat can detach it: a socket that was already replaced
   * by a newer connection must not knock the new one off.
   */
  disconnected(c: Client): void {
    if (this.closed) return;
    const s = this.seatOf(c.id);
    if (!s || (s.client !== null && s.client !== c)) return;
    this.onDisconnected(c.id);
  }

  // ---------- internals ----------

  private seatOf(id: PlayerID): Seat | undefined {
    return this.seats.find((s) => s.id === id);
  }

  private sendTo(id: PlayerID, m: ServerMessage): void {
    this.seatOf(id)?.client?.send(m);
  }

  private broadcastMsg(m: ServerMessage): void {
    for (const s of this.seats) s.client?.send(m);
  }

  /**
   * applySystem runs a server-generated action (timers, connection changes).
   * There is no client to report a rejection to, so failures are dropped: they
   * mean the state moved on, which the generation guard already tolerates.
   */
  private applySystem(a: Action): boolean {
    const res = apply(this.st!, a);
    if (!res.ok) {
      // A timer that fired is now spent. Re-arm before returning, or nothing
      // would ever wake the room again.
      this.syncTimers();
      return false;
    }
    this.st = res.state;
    this.noteEvents(res.events);
    this.broadcast(res.events);
    this.refreshStatus();
    this.syncTimers();
    return true;
  }

  /** The engine says "+2s"; the server owns the clock, so it stretches the timer. */
  private noteEvents(evs: GameEvent[]): void {
    if (evs.some((e) => e.name === "matching_window_extended")) this.extensionPending = true;
  }

  private onDisconnected(id: PlayerID): void {
    const s = this.seatOf(id);
    if (!s) return;
    s.client = null;
    if (!this.started) {
      this.leave(id);
      return;
    }
    if (s.removed || this.st!.phase === Phase.Finished) return;
    this.applySystem({ kind: "player_disconnected", player: id });
    this.startReconnectTimer(id);
    this.broadcastRoomState();
  }

  private refreshStatus(): void {
    if (!this.started) {
      const ready = this.seats.length >= 2 && this.seats.every((s) => s.ready);
      this.status = ready ? "READY" : "WAITING";
      return;
    }
    const st = this.st!;
    const prev = this.status;
    if (st.phase === Phase.Finished) this.status = "FINISHED";
    else if (st.phase === Phase.Memory) this.status = "STARTING";
    else this.status = "PLAYING";
    // Mirror engine removals onto the seats.
    for (const s of this.seats) {
      if (st.player(s.id)?.removed) s.removed = true;
    }
    if (this.status === "FINISHED" && prev !== "FINISHED") {
      this.stopPhaseTimer();
      for (const pid of [...this.reconnect.keys()]) this.cancelReconnect(pid);
      for (const s of this.seats) {
        s.client?.send(msg(SGameFinished, { view: viewFor(st, s.id) }));
      }
      this.broadcastRoomState();
    }
  }

  /**
   * broadcast is the ONLY path from engine output to the wire. Every player
   * gets their own view and their own filtered events.
   */
  private broadcast(evs: GameEvent[]): void {
    for (const s of this.seats) {
      if (!s.client) continue;
      s.client.send(msg(SStateUpdate, { view: viewFor(this.st!, s.id) }));
      const mine = filterEvents(evs, s.id);
      if (mine.length > 0) s.client.send(msg(SEvents, { events: encodeEvents(mine) }));
    }
  }

  private sendState(s: Seat): void {
    s.client?.send(msg(SStateUpdate, { view: viewFor(this.st!, s.id) }));
  }

  private roomState(): RoomStateData {
    return {
      code: this.cfg.code,
      public: this.cfg.public,
      status: this.status,
      cards_per_player: this.cfg.cardsPerPlayer,
      max_players: this.cfg.maxPlayers,
      players: this.seats.map((s) => ({
        id: s.id,
        name: s.name,
        ready: s.ready,
        connected: s.client !== null,
        removed: s.removed,
        host: s.id === this.host,
      })),
    };
  }

  private broadcastRoomState(): void {
    this.broadcastMsg(msg(SRoomState, this.roomState()));
  }

  // ---------- timers ----------

  private duration(k: TimerKind): number {
    const t = this.cfg.timers;
    switch (k) {
      case "memory":
        return t.memory;
      case "turn":
        return t.turn;
      case "match_window":
        return t.match;
      default:
        return t.power;
    }
  }

  private scopeFor(st: State): Scope | null {
    switch (st.phase) {
      case Phase.Memory:
        return { kind: "memory", key: "" };
      case Phase.TurnStart:
      case Phase.DrawDecision:
        return { kind: "turn", key: st.currentID() };
      case Phase.Matching:
        return { kind: "match_window", key: st.currentID() };
      case Phase.Power:
      case Phase.KingDecision:
        return { kind: "power", key: st.powerActor };
      default:
        return null;
    }
  }

  /** Reconciles the running timer with the phase after every apply. */
  private syncTimers(): void {
    if (!this.started || this.st!.phase === Phase.Finished) {
      this.stopPhaseTimer();
      return;
    }
    if (this.st!.paused) {
      this.freezePhaseTimer();
      return;
    }
    const want = this.scopeFor(this.st!);
    if (!want) {
      this.stopPhaseTimer();
      return;
    }
    const t = this.phaseTimer;
    if (t && t.sc.kind === want.kind && t.sc.key === want.key) {
      if (t.frozen) {
        this.resumePhaseTimer();
      } else if (this.extensionPending) {
        this.extensionPending = false;
        this.extendPhaseTimer(this.cfg.timers.matchExtension);
      }
      return;
    }
    this.extensionPending = false;
    this.startPhaseTimer(want);
  }

  private schedule(ms: number, gen: number): () => void {
    return this.cfg.clock.after(ms, () => this.firePhaseTimer(gen));
  }

  private startPhaseTimer(sc: Scope): void {
    this.stopPhaseTimer();
    const d = this.duration(sc.kind);
    const gen = ++this.timerGen;
    this.phaseTimer = {
      sc,
      gen,
      deadline: this.cfg.clock.now() + d,
      remaining: 0,
      cancel: this.schedule(d, gen),
      frozen: false,
    };
  }

  private stopPhaseTimer(): void {
    if (!this.phaseTimer) return;
    this.phaseTimer.cancel?.();
    // Bumping the generation makes any callback already in flight stale.
    this.timerGen++;
    this.phaseTimer = null;
  }

  private freezePhaseTimer(): void {
    const t = this.phaseTimer;
    if (!t || t.frozen) return;
    t.remaining = Math.max(0, t.deadline - this.cfg.clock.now());
    t.cancel?.();
    t.gen = ++this.timerGen;
    t.frozen = true;
  }

  private resumePhaseTimer(): void {
    const t = this.phaseTimer;
    if (!t || !t.frozen) return;
    t.gen = ++this.timerGen;
    t.frozen = false;
    t.deadline = this.cfg.clock.now() + t.remaining;
    t.cancel = this.schedule(t.remaining, t.gen);
  }

  private extendPhaseTimer(by: number): void {
    const t = this.phaseTimer;
    if (!t || t.frozen) return;
    const remaining = t.deadline - this.cfg.clock.now() + by;
    t.cancel?.();
    t.gen = ++this.timerGen;
    t.deadline = this.cfg.clock.now() + remaining;
    t.cancel = this.schedule(remaining, t.gen);
  }

  /** The generation guard: a timer from an ended phase cannot close its successor. */
  private firePhaseTimer(gen: number): void {
    if (this.closed) return;
    const t = this.phaseTimer;
    if (!t || t.gen !== gen || t.frozen) return;
    if (this.st!.paused) return;
    this.phaseTimer = null;
    this.applySystem({ kind: "timer_expired", player: "", timer: t.sc.kind });
  }

  private startReconnectTimer(id: PlayerID): void {
    this.cancelReconnect(id);
    const gen = this.bumpReconnectGen(id);
    this.reconnect.set(
      id,
      this.cfg.clock.after(this.cfg.timers.reconnect, () => this.fireReconnect(id, gen)),
    );
  }

  private bumpReconnectGen(id: PlayerID): number {
    const gen = (this.reconnectGen.get(id) ?? 0) + 1;
    this.reconnectGen.set(id, gen);
    return gen;
  }

  private cancelReconnect(id: PlayerID): void {
    const cancel = this.reconnect.get(id);
    if (cancel) {
      cancel();
      this.reconnect.delete(id);
    }
    this.bumpReconnectGen(id);
  }

  private fireReconnect(id: PlayerID, gen: number): void {
    if (this.closed || this.reconnectGen.get(id) !== gen) return;
    this.reconnect.delete(id);
    const s = this.seatOf(id);
    if (!s || s.client !== null || s.removed) return;
    // The engine decides what removal means: cards sink to the bottom of the
    // discard pile, the rotation closes up, the round accounting stays honest.
    this.applySystem({ kind: "remove_player", player: id });
    this.broadcastRoomState();
  }

  // ---------- inspection ----------

  snapshot(): Info {
    return {
      code: this.cfg.code,
      public: this.cfg.public,
      status: this.status,
      host: this.host,
      cardsPerPlayer: this.cfg.cardsPerPlayer,
      maxPlayers: this.cfg.maxPlayers,
      players: this.roomState().players,
      started: this.started,
      phase: this.started ? phaseString(this.st!.phase) : "",
      paused: this.started ? this.st!.paused : false,
      activeRank: this.started ? this.st!.activeRank : 0,
      empty: this.seats.every((s) => s.client === null),
    };
  }

  /** Test support only: the authoritative state. Nothing on the wire path may use it. */
  stateForTesting(): State | null {
    return this.st;
  }

  /**
   * Swaps in a scripted game state and re-reconciles the room around it, so a
   * test can pin an exact deal. Nothing on the wire path may call it.
   */
  replaceStateForTesting(st: State): void {
    this.st = st;
    this.started = true;
    this.refreshStatus();
    this.syncTimers();
  }
}
