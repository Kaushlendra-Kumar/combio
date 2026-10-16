import { GameError } from "./engine/errors";
import type { PlayerID } from "./engine/state";
import type { CreateRoomData, RoomSummary } from "./protocol";
import { type Clock, defaultTimers, realClock, Room, type Timers } from "./room";

export const ErrNoSuchRoom = new GameError("no room with that code");
export const ErrBadSettings = new GameError(
  "cards per player must be 4, 6 or 8 and max players 2-8",
);
export const ErrTooManyRooms = new GameError(
  "the server is at its room limit, try again shortly",
);

/** Bounds how many live games one server holds, so a script cannot exhaust memory. */
export const DefaultMaxRooms = 500;

/** Leaves out characters people misread when reading a code aloud. */
const codeAlphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789";

export interface ManagerOptions {
  clock?: Clock;
  timers?: Timers;
  /** Pins the deal for every room; zero means a fresh random seed per room. */
  seed?: number;
  maxRooms?: number;
  /** Source of randomness for room codes and seeds: returns [0, 1). */
  random?: () => number;
}

function cryptoRandom(): number {
  const buf = new Uint32Array(2);
  crypto.getRandomValues(buf);
  // 53 random bits, the full precision of a double.
  return (buf[0] * 2 ** 21 + (buf[1] >>> 11)) / 2 ** 53;
}

/** Manager is the room registry. */
export class Manager {
  private rooms = new Map<string, Room>();
  private readonly clock: Clock;
  private readonly timers: Timers;
  private readonly seed: number;
  private readonly maxRooms: number;
  private readonly random: () => number;

  constructor(o: ManagerOptions = {}) {
    this.clock = o.clock ?? realClock;
    this.timers = o.timers ?? defaultTimers();
    this.seed = o.seed ?? 0;
    this.maxRooms = o.maxRooms && o.maxRooms > 0 ? o.maxRooms : DefaultMaxRooms;
    this.random = o.random ?? cryptoRandom;
  }

  private newCode(): string {
    for (let attempt = 0; ; attempt++) {
      let code = "";
      for (let i = 0; i < 4; i++) {
        code += codeAlphabet[Math.floor(this.random() * codeAlphabet.length)];
      }
      if (!this.rooms.has(code)) return code;
      if (attempt > 1000) return code + "X";
    }
  }

  /** Opens a room. The creator becomes the host when they join it. */
  create(d: CreateRoomData): Room {
    const cards = d.cards_per_player || 4;
    const max = d.max_players || 8;
    if ((cards !== 4 && cards !== 6 && cards !== 8) || max < 2 || max > 8) throw ErrBadSettings;
    if (this.rooms.size >= this.maxRooms) throw ErrTooManyRooms;
    const code = this.newCode();
    // Kept well inside 2^52 so seed + games stays an exact integer.
    const seed = this.seed !== 0 ? this.seed : Math.floor(this.random() * 2 ** 48) + 1;
    const r = new Room({
      code,
      public: d.public,
      cardsPerPlayer: cards,
      maxPlayers: max,
      seed,
      timers: this.timers,
      clock: this.clock,
    });
    this.rooms.set(code, r);
    return r;
  }

  get(code: string): Room | undefined {
    return this.rooms.get(code.trim().toUpperCase());
  }

  count(): number {
    return this.rooms.size;
  }

  /** Joinable public rooms. */
  list(): RoomSummary[] {
    const out: RoomSummary[] = [];
    for (const r of this.rooms.values()) {
      const info = r.snapshot();
      if (!info.public || info.started) continue;
      out.push({
        code: info.code,
        players: info.players.length,
        max_players: info.maxPlayers,
        cards_per_player: info.cardsPerPlayer,
        status: info.status,
      });
    }
    return out;
  }

  /** Locates the live room a player already has a seat in, for reconnects without a code. */
  findPlayerRoom(id: PlayerID): Room | undefined {
    for (const r of this.rooms.values()) {
      const info = r.snapshot();
      if (info.status === "FINISHED") continue;
      if (info.players.some((p) => p.id === id)) return r;
    }
    return undefined;
  }

  remove(code: string): void {
    const r = this.rooms.get(code);
    if (!r) return;
    this.rooms.delete(code);
    r.close();
  }

  /** Closes rooms nobody is connected to. Returns how many were reaped. */
  reap(): number {
    let n = 0;
    for (const r of [...this.rooms.values()]) {
      if (r.snapshot().empty) {
        this.remove(r.code);
        n++;
      }
    }
    return n;
  }
}
