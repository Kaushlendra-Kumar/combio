// Room, timer and session behaviour, driven through the real Server with fake
// sockets and a hand-cranked clock, so every timing question is exact.

import { beforeEach, describe, expect, it } from "vitest";
import { DevTokens, MemoryTokenStore } from "../src/auth";
import type { CardID } from "../src/engine/card";
import { StackedShuffler } from "../src/engine/deck";
import { newGame } from "../src/engine/state";
import { Manager } from "../src/manager";
import { type Clock, defaultTimers } from "../src/room";
import { MaxMessageSize, Server, type Session, type Socket } from "../src/server";

class FakeClock implements Clock {
  t = 0;
  private queue: { at: number; id: number; fn: () => void }[] = [];
  private n = 0;

  now(): number {
    return this.t;
  }

  after(ms: number, fn: () => void): () => void {
    const e = { at: this.t + ms, id: ++this.n, fn };
    this.queue.push(e);
    return () => {
      this.queue = this.queue.filter((x) => x !== e);
    };
  }

  /** Moves time forward, firing everything that falls due on the way, in order. */
  advance(ms: number): void {
    const end = this.t + ms;
    for (;;) {
      this.queue.sort((a, b) => a.at - b.at || a.id - b.id);
      const e = this.queue[0];
      if (!e || e.at > end) break;
      this.queue.shift();
      this.t = e.at;
      e.fn();
    }
    this.t = end;
  }
}

// eslint-disable-next-line @typescript-eslint/no-explicit-any
type Msg = { v: number; type: string; id?: string; data?: any };

class FakeSocket implements Socket {
  msgs: Msg[] = [];
  closed = false;
  send(data: string): void {
    this.msgs.push(JSON.parse(data));
  }
  close(): void {
    this.closed = true;
  }
}

interface Conn {
  sock: FakeSocket;
  session: Session;
  send(type: string, data?: unknown, id?: string): void;
  last(type: string): Msg | undefined;
  events(): string[];
  clear(): void;
}

let clock: FakeClock;
let server: Server;
let auth: DevTokens;
let rooms: Manager;

beforeEach(() => {
  clock = new FakeClock();
  auth = new DevTokens(new MemoryTokenStore());
  rooms = new Manager({ clock, timers: defaultTimers(), seed: 42 });
  server = new Server({ auth, rooms, allowedOrigins: ["https://combio.example"] });
});

async function connect(token: string): Promise<Conn> {
  const sock = new FakeSocket();
  let session: Session | undefined;
  const req = new Request(`http://worker.test/ws?token=${token}`, {
    headers: { Upgrade: "websocket" },
  });
  const res = await server.fetch(req, (_req, open) => {
    session = open(sock);
    return new Response(null, { status: 200 }); // Node cannot build a 101
  });
  expect(res.status).toBe(200);
  const s = session!;
  return {
    sock,
    session: s,
    send: (type, data, id) => s.onMessage(JSON.stringify({ v: 1, type, id, data })),
    last: (type) => sock.msgs.filter((m) => m.type === type).at(-1),
    events: () =>
      sock.msgs.filter((m) => m.type === "events").flatMap((m) => m.data.events.map((e: { name: string }) => e.name)),
    clear: () => {
      sock.msgs = [];
    },
  };
}

/** Two players, seated, ready and dealt in. Returns them in seat order. */
async function startedGame(): Promise<{ a: Conn; b: Conn; ta: string; tb: string; code: string }> {
  const ta = auth.issue("Asha").token;
  const tb = auth.issue("Bilal").token;
  const a = await connect(ta);
  const b = await connect(tb);
  a.send("create_room", { cards_per_player: 4, max_players: 4 }, "c1");
  const code = a.last("room_state")!.data.code as string;
  b.send("join_room", { code: code.toLowerCase() }, "j1");
  a.send("ready", { ready: true });
  b.send("ready", { ready: true });
  expect(a.last("room_state")!.data.status).toBe("READY");
  a.send("start_game", undefined, "s1");
  expect(a.last("state_update")!.data.view.phase).toBe("memory");
  return { a, b, ta, tb, code };
}

function phase(c: Conn): string {
  return c.last("state_update")!.data.view.phase;
}

describe("lobby", () => {
  it("seats players, tracks the host and lists public rooms", async () => {
    const a = await connect(auth.issue("Asha").token);
    expect(a.sock.msgs[0]).toEqual({ v: 1, type: "welcome", data: { version: 1, player_id: "u1", name: "Asha" } });
    a.send("create_room", { public: true }, "c1");
    const rs = a.last("room_state")!.data;
    expect(rs).toMatchObject({ public: true, status: "WAITING", cards_per_player: 4, max_players: 8 });
    expect(rs.players).toEqual([{ id: "u1", name: "Asha", ready: false, connected: true, removed: false, host: true }]);

    const b = await connect(auth.issue("Bilal").token);
    b.send("list_rooms", undefined, "l1");
    expect(b.last("room_list")).toEqual({
      v: 1, type: "room_list", id: "l1",
      data: { rooms: [{ code: rs.code, players: 1, max_players: 8, cards_per_player: 4, status: "WAITING" }] },
    });

    // The host leaving hands the room to whoever is left.
    b.send("join_room", { code: rs.code });
    a.send("leave_room");
    expect(b.last("player_left")!.data).toEqual({ player: "u1", name: "Asha" });
    expect(b.last("room_state")!.data.players[0]).toMatchObject({ id: "u2", host: true });
  });

  it("refuses bad settings, unknown rooms and a start by anyone but the host", async () => {
    const a = await connect(auth.issue("Asha").token);
    a.send("create_room", { cards_per_player: 5 }, "c1");
    expect(a.last("error")).toEqual({
      v: 1, type: "error", id: "c1",
      data: { code: "rejected", message: "cards per player must be 4, 6 or 8 and max players 2-8" },
    });
    a.send("join_room", { code: "ZZZZ" }, "j1");
    expect(a.last("error")!.data.message).toBe("no room with that code");
    a.send("ready", { ready: true }, "r1");
    expect(a.last("error")!.data.message).toBe("you are not in this room");

    a.send("create_room", {}, "c2");
    const b = await connect(auth.issue("Bilal").token);
    b.send("join_room", { code: a.last("room_state")!.data.code });
    b.send("start_game", undefined, "s1");
    expect(b.last("error")!.data.message).toBe("only the host can start the game");
    a.send("start_game", undefined, "s2");
    expect(a.last("error")!.data.message).toBe("every player must be ready, and there must be at least two");
  });
});

describe("timers", () => {
  it("ends the memory phase after 60s and forces a move when a turn runs out", async () => {
    const { a, b } = await startedGame();
    clock.advance(59_999);
    expect(phase(a)).toBe("memory");
    clock.advance(1);
    expect(phase(a)).toBe("turn_start");
    expect(phase(b)).toBe("turn_start");

    a.clear();
    clock.advance(59_999);
    expect(a.events()).toEqual([]);
    clock.advance(1);
    expect(a.events().slice(0, 2)).toEqual(["turn_timed_out", "card_drawn"]);
    expect(phase(a)).toBe("matching");

    clock.advance(4_999);
    expect(phase(a)).toBe("matching");
    clock.advance(1);
    expect(a.events()).toContain("matching_window_closed");
    expect(phase(a)).toBe("turn_start");
    expect(a.last("state_update")!.data.view.turn_player).toBe("u2");
  });

  it("stretches the matching window by 2s for every accepted match", async () => {
    const { a, b } = await startedGame();
    // Pin the deal: u1's first card is A♣ and the first card drawn is A♦,
    // so u1 can throw a matching Ace into the window.
    const order: CardID[] = Array.from({ length: 52 }, (_, i) => i + 1);
    const put = (index: number, id: CardID) => {
      const from = order.indexOf(id);
      [order[index], order[from]] = [order[from], order[index]];
    };
    put(51, 1); // dealt first: u1 position 0
    put(42, 14); // the first card drawn after the deal and the face-up discard
    const room = rooms.get(a.last("room_state")!.data.code)!;
    room.replaceStateForTesting(
      newGame({ cardsPerPlayer: 4, numDecks: 0 }, ["u1", "u2"], new StackedShuffler(order)).state,
    );
    a.send("action", { kind: "inspect_memory", args: { positions: [0, 1] } });
    b.send("action", { kind: "inspect_memory", args: { positions: [0, 1] } });
    expect(phase(a)).toBe("turn_start");

    a.send("action", { kind: "draw_card" });
    expect(a.last("state_update")!.data.view.my_drawn_card).toMatchObject({ id: 14, rank: 1 });
    a.send("action", { kind: "discard_drawn" });
    expect(phase(a)).toBe("matching");

    clock.advance(4_000);
    const version = a.last("state_update")!.data.view.my_version;
    a.send("action", { kind: "attempt_own_match", args: { index: 0, version } }, "m1");
    expect(a.events()).toContain("match_accepted");

    // The window would have shut at 5s; the match moved it to 7s.
    clock.advance(2_999);
    expect(phase(a)).toBe("matching");
    clock.advance(1);
    expect(phase(a)).toBe("turn_start");
  });
});

describe("disconnects", () => {
  it("freezes the clock while a player is away and resumes it on their return", async () => {
    const { a, b, tb, code } = await startedGame();
    clock.advance(60_000); // memory over, u1 to move
    clock.advance(20_000); // 40s left on u1's turn

    b.session.onClose();
    expect(a.last("state_update")!.data.view.paused).toBe(true);
    expect(a.last("room_state")!.data.players[1].connected).toBe(false);

    a.clear();
    clock.advance(29_000); // nothing moves while paused
    expect(a.events()).toEqual([]);

    const b2 = await connect(tb);
    b2.send("join_room", { code }, "rj");
    expect(a.events()).toEqual(["player_reconnected", "game_resumed"]);
    expect(b2.last("state_update")!.data.view.me).toBe("u2");

    a.clear();
    clock.advance(39_999);
    expect(a.events()).toEqual([]);
    clock.advance(1);
    expect(a.events()[0]).toBe("turn_timed_out");
  });

  it("removes a player who stays away for 30s, then lets the table rematch", async () => {
    const { a, b } = await startedGame();
    b.session.onClose();
    clock.advance(29_999);
    expect(a.last("game_finished")).toBeUndefined();
    clock.advance(1);
    expect(a.events()).toContain("player_removed");
    const done = a.last("game_finished")!.data.view;
    expect(done).toMatchObject({ finished: true, winners: ["u1"] });
    expect(a.last("room_state")!.data).toMatchObject({ status: "FINISHED" });

    a.send("rematch", undefined, "rm");
    const rs = a.last("room_state")!.data;
    expect(rs.status).toBe("WAITING");
    // Bilal's socket is gone, so his seat is not kept for the next game.
    expect(rs.players).toEqual([{ id: "u1", name: "Asha", ready: false, connected: true, removed: false, host: true }]);
  });

  it("hands the seat to a newer connection without pausing the game", async () => {
    const { a, b, ta, code } = await startedGame();
    const a2 = await connect(ta);
    a2.send("join_room", { code });
    expect(a.last("reconnect_required")!.data).toEqual({ code, reason: "replaced by a newer connection" });
    expect(a.sock.closed).toBe(true);

    // The old socket's close event arrives afterwards. It must not knock the
    // new connection off the seat.
    a.session.onClose();
    expect(b.last("state_update")!.data.view.paused).toBe(false);
    expect(b.last("room_state")!.data.players[0].connected).toBe(true);
    a2.send("action", { kind: "inspect_memory", args: { positions: [0, 1] } }, "i1");
    expect(a2.events()).toContain("memory_revealed");
  });
});

describe("frames", () => {
  it("answers bad frames with errors and hangs up after five in a row", async () => {
    const a = await connect(auth.issue("Asha").token);
    a.session.onMessage(JSON.stringify({ v: 2, type: "ping", id: "x" }));
    expect(a.last("error")).toEqual({
      v: 1, type: "error", id: "x",
      data: { code: "unsupported_version", message: "unsupported protocol version: this server speaks v1, you sent v2" },
    });
    a.send("teleport", undefined, "t");
    expect(a.last("error")!.data).toEqual({ code: "rejected", message: "unknown message type: teleport" });
    a.send("ping", undefined, "p");
    expect(a.last("pong")).toEqual({ v: 1, type: "pong", id: "p" });

    for (let i = 0; i < 4; i++) a.session.onMessage("{not json");
    expect(a.last("error")!.data).toEqual({ code: "malformed", message: "malformed message" });
    expect(a.sock.closed).toBe(false);
    a.session.onMessage(JSON.stringify({ v: "1" }));
    expect(a.sock.closed).toBe(true);
  });

  it("drops a connection that sends an oversized frame", async () => {
    const a = await connect(auth.issue("Asha").token);
    a.session.onMessage("x".repeat(MaxMessageSize + 1));
    expect(a.sock.closed).toBe(true);
  });

  it("never lets a client choose who is acting", async () => {
    const { a } = await startedGame();
    a.send("action", { kind: "inspect_memory", args: { positions: [0, 1], player: "u2" }, player: "u2" });
    const view = a.last("state_update")!.data.view;
    expect(view.me).toBe("u1");
    expect(a.events()).toContain("memory_revealed");
  });
});

describe("http", () => {
  it("mints tokens and checks names", async () => {
    const ok = await server.fetch(new Request("http://w/auth/dev?name=Asha", { method: "POST" }), () => {
      throw new Error("no upgrade");
    });
    expect(ok.status).toBe(200);
    const body = (await ok.json()) as Record<string, unknown>;
    expect(Object.keys(body)).toEqual(["name", "player_id", "token"]);
    expect(body).toMatchObject({ name: "Asha", player_id: "u1" });

    const fromBody = await server.fetch(
      new Request("http://w/auth/dev", { method: "POST", body: JSON.stringify({ name: "Bilal" }) }),
      () => new Response(),
    );
    expect(((await fromBody.json()) as { name: string }).name).toBe("Bilal");

    const long = await server.fetch(
      new Request(`http://w/auth/dev?name=${"x".repeat(25)}`, { method: "POST" }),
      () => new Response(),
    );
    expect(long.status).toBe(400);
    expect(await long.json()).toEqual({ error: "display name must be 1-24 printable characters" });

    const get = await server.fetch(new Request("http://w/auth/dev"), () => new Response());
    expect(get.status).toBe(405);
    expect(await get.text()).toBe("POST only\n");
  });

  it("refuses an unauthenticated socket before upgrading", async () => {
    const res = await server.fetch(
      new Request("http://w/ws?token=nope", { headers: { Upgrade: "websocket" } }),
      () => {
        throw new Error("must not upgrade");
      },
    );
    expect(res.status).toBe(401);
    expect(await res.text()).toBe("unknown or expired auth token\n");
  });

  it("only grants CORS to allowed origins", async () => {
    const pre = await server.fetch(
      new Request("http://w/auth/dev", { method: "OPTIONS", headers: { Origin: "https://combio.example" } }),
      () => new Response(),
    );
    expect(pre.status).toBe(204);
    expect(pre.headers.get("Access-Control-Allow-Origin")).toBe("https://combio.example");

    const other = await server.fetch(
      new Request("http://w/healthz", { headers: { Origin: "https://evil.example" } }),
      () => new Response(),
    );
    expect(await other.json()).toEqual({ ok: true, protocol: 1 });
    expect(other.headers.get("Access-Control-Allow-Origin")).toBeNull();
    expect(other.headers.get("Vary")).toBe("Origin");
  });
});
