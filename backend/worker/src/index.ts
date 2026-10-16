// Cloudflare entry point.
//
// Every request goes to ONE Durable Object instance. That instance plays the
// part the Go process used to: it holds every room in memory, runs every timer,
// and is single-threaded, so a game is never touched by two requests at once.
// Only dev-auth tokens are written to storage, so a player's saved session
// survives the object being evicted or redeployed.

import { DurableObject } from "cloudflare:workers";
import { DevTokens, type Identity, type TokenStore } from "./auth";
import { Manager } from "./manager";
import { defaultTimers, type Timers } from "./room";
import { Server, type Session, type Upgrader } from "./server";

export interface Env {
  GAME: DurableObjectNamespace<GameServer>;
  /** Comma-separated browser origins allowed to call the HTTP API (empty = any). */
  ALLOWED_ORIGINS?: string;
  /** Fixed deal seed for every room (empty or 0 = random per room). */
  SEED?: string;
  // Timer overrides as Go-style durations, e.g. "60s", "1m30s", "500ms".
  TURN_TIMER?: string;
  MATCH_TIMER?: string;
  MATCH_EXTENSION?: string;
  POWER_TIMER?: string;
  MEMORY_TIMER?: string;
  RECONNECT_TIMER?: string;
}

const units: Record<string, number> = { ns: 1e-6, us: 1e-3, µs: 1e-3, ms: 1, s: 1000, m: 60_000, h: 3_600_000 };

/** Parses a Go duration string into milliseconds, or returns undefined. */
export function parseDuration(s: string | undefined): number | undefined {
  if (!s || !s.trim()) return undefined;
  const re = /(\d+(?:\.\d+)?)(ns|us|µs|ms|s|m|h)/gy;
  const text = s.trim();
  let total = 0;
  let consumed = 0;
  for (let m = re.exec(text); m; m = re.exec(text)) {
    total += parseFloat(m[1]) * units[m[2]];
    consumed = re.lastIndex;
  }
  return consumed === text.length && consumed > 0 ? total : undefined;
}

function timersFrom(env: Env): Timers {
  const d = defaultTimers();
  return {
    turn: parseDuration(env.TURN_TIMER) ?? d.turn,
    match: parseDuration(env.MATCH_TIMER) ?? d.match,
    matchExtension: parseDuration(env.MATCH_EXTENSION) ?? d.matchExtension,
    power: parseDuration(env.POWER_TIMER) ?? d.power,
    memory: parseDuration(env.MEMORY_TIMER) ?? d.memory,
    reconnect: parseDuration(env.RECONNECT_TIMER) ?? d.reconnect,
  };
}

/** Dev-auth tokens in the object's SQLite storage. Every call is synchronous. */
class SqlTokenStore implements TokenStore {
  constructor(private readonly sql: SqlStorage) {
    sql.exec(
      "CREATE TABLE IF NOT EXISTS tokens (token TEXT PRIMARY KEY, player_id TEXT NOT NULL, name TEXT NOT NULL)",
    );
    sql.exec("CREATE TABLE IF NOT EXISTS meta (k TEXT PRIMARY KEY, v INTEGER NOT NULL)");
  }

  get(token: string): Identity | undefined {
    const rows = this.sql
      .exec<{ player_id: string; name: string }>(
        "SELECT player_id, name FROM tokens WHERE token = ?",
        token,
      )
      .toArray();
    return rows.length > 0 ? { player_id: rows[0].player_id, name: rows[0].name } : undefined;
  }

  put(token: string, id: Identity): void {
    this.sql.exec(
      "INSERT INTO tokens (token, player_id, name) VALUES (?, ?, ?)",
      token,
      id.player_id,
      id.name,
    );
  }

  nextId(): number {
    return this.sql
      .exec<{ v: number }>(
        "INSERT INTO meta (k, v) VALUES ('next_player', 1) ON CONFLICT(k) DO UPDATE SET v = v + 1 RETURNING v",
      )
      .one().v;
  }
}

export class GameServer extends DurableObject<Env> {
  private readonly server: Server;
  private readonly rooms: Manager;

  constructor(ctx: DurableObjectState, env: Env) {
    super(ctx, env);
    const seed = Number(env.SEED ?? 0);
    this.rooms = new Manager({
      timers: timersFrom(env),
      seed: Number.isSafeInteger(seed) ? seed : 0,
    });
    const allowedOrigins = (env.ALLOWED_ORIGINS ?? "")
      .split(",")
      .map((o) => o.trim())
      .filter((o) => o !== "");
    this.server = new Server({
      auth: new DevTokens(new SqlTokenStore(ctx.storage.sql)),
      rooms: this.rooms,
      allowedOrigins,
      log: (line) => console.log(line),
    });
    // Periodically close rooms nobody is connected to.
    setInterval(() => {
      const n = this.rooms.reap();
      if (n > 0) console.log(`reaped ${n} empty room(s)`);
    }, 60_000);
  }

  async fetch(req: Request): Promise<Response> {
    return this.server.fetch(req, upgrade);
  }
}

/** Accepts the socket inside this object and wires its events to a session. */
const upgrade: Upgrader = (_req, open) => {
  const pair = new WebSocketPair();
  const [client, server] = Object.values(pair);
  server.accept();
  const session: Session = open({
    send: (data) => server.send(data),
    close: () => server.close(1000, "closed"),
  });
  server.addEventListener("message", (ev) => session.onMessage(ev.data));
  server.addEventListener("close", () => session.onClose());
  server.addEventListener("error", () => session.onClose());
  return new Response(null, { status: 101, webSocket: client });
};

export default {
  async fetch(req: Request, env: Env): Promise<Response> {
    const stub = env.GAME.get(env.GAME.idFromName("global"));
    return stub.fetch(req);
  },
} satisfies ExportedHandler<Env>;
