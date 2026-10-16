// HTTP routes and WebSocket session dispatch.
//
// This is where the actor-authority boundary lives: a session's player id
// comes from the authenticated token at connect time and is the only identity
// ever handed to a room. Nothing a client puts in a message body can change it.
//
// Nothing here is Cloudflare-specific: the Durable Object supplies the socket
// upgrade, and tests drive the same code with fake sockets.

import { type DevTokens, type Identity } from "./auth";
import { GameError, wrap } from "./engine/errors";
import { type Manager, ErrNoSuchRoom } from "./manager";
import {
  CAction,
  CCreateRoom,
  CJoinRoom,
  CLeaveRoom,
  CListRooms,
  CPing,
  CReady,
  CReconnect,
  CRematch,
  CStartGame,
  type ClientMessage,
  decodeAction,
  decodeCreateRoom,
  decodeJoinRoom,
  decodeReady,
  ErrMalformed,
  ErrUnsupportedVersion,
  errorMsg,
  msg,
  parseClientMessage,
  reply,
  SPong,
  SRoomList,
  type ServerMessage,
  SWelcome,
  Version,
} from "./protocol";
import { type Client, ErrNotInRoom, type Room } from "./room";

/** Caps a single inbound frame, bounding what one client can make the server hold. */
export const MaxMessageSize = 16 << 10;

/** A client gets this many malformed frames in a row before we hang up. */
const maxBadFrames = 5;

/** The transport half of a socket: what a session needs to talk back. */
export interface Socket {
  send(data: string): void;
  close(): void;
}

export interface ServerOptions {
  auth: DevTokens;
  rooms: Manager;
  /**
   * Browser origins permitted to call the HTTP endpoints. Empty means "any",
   * which is what local development needs.
   */
  allowedOrigins: string[];
  log?: (line: string) => void;
}

/** Turns an authenticated request into a 101 response plus the server-side socket. */
export type Upgrader = (req: Request, open: (sock: Socket) => Session) => Response;

const textHeaders = {
  "Content-Type": "text/plain; charset=utf-8",
  "X-Content-Type-Options": "nosniff",
};

/** http.Error: a plain-text body with a trailing newline. */
function textError(message: string, status: number): Response {
  return new Response(message + "\n", { status, headers: textHeaders });
}

function json(status: number, body: unknown): Response {
  return new Response(JSON.stringify(body) + "\n", {
    status,
    headers: { "Content-Type": "application/json" },
  });
}

function tokenFrom(req: Request, url: URL): string {
  const t = url.searchParams.get("token");
  if (t) return t;
  const h = req.headers.get("Authorization") ?? "";
  if (h.toLowerCase().startsWith("bearer ")) return h.slice(7).trim();
  return h;
}

export class Server {
  constructor(readonly opts: ServerOptions) {}

  private log(line: string): void {
    this.opts.log?.(line);
  }

  private originAllowed(origin: string): boolean {
    const allowed = this.opts.allowedOrigins;
    if (allowed.length === 0) return true; // development default
    return allowed.some((o) => o === "*" || o.toLowerCase() === origin.toLowerCase());
  }

  /**
   * fetch answers one HTTP request. CORS headers are added to every answer so
   * the browser can read it: without them the page gets a response it is not
   * allowed to read, which shows up as an opaque "Failed to fetch". No
   * credentials are involved — the token travels in the body and then in the
   * WebSocket URL — so nothing here weakens the actor authority model.
   */
  async fetch(req: Request, upgrade: Upgrader): Promise<Response> {
    const res = await this.route(req, upgrade);
    if (res.status === 101) return res; // a socket upgrade carries no CORS headers
    const origin = req.headers.get("Origin");
    if (!origin) return res;
    const headers = new Headers(res.headers);
    headers.append("Vary", "Origin");
    if (this.originAllowed(origin)) {
      headers.set("Access-Control-Allow-Origin", origin);
      headers.set("Access-Control-Allow-Methods", "GET, POST, OPTIONS");
      headers.set("Access-Control-Allow-Headers", "Content-Type, Authorization");
      headers.set("Access-Control-Max-Age", "600");
    }
    return new Response(res.body, { status: res.status, headers });
  }

  private async route(req: Request, upgrade: Upgrader): Promise<Response> {
    // A preflight never reaches a route: answer it here and stop.
    if (req.method === "OPTIONS") return new Response(null, { status: 204 });
    const url = new URL(req.url);
    switch (url.pathname) {
      case "/healthz":
        return json(200, { ok: true, protocol: Version });
      case "/auth/dev":
        return this.handleDevAuth(req, url);
      case "/ws":
        return this.handleWS(req, url, upgrade);
      default:
        return textError("404 page not found", 404);
    }
  }

  /** Mints a token for a display name. The player id is chosen by the server. */
  private async handleDevAuth(req: Request, url: URL): Promise<Response> {
    if (req.method !== "POST") return textError("POST only", 405);
    let name = url.searchParams.get("name") ?? "";
    if (name === "") {
      try {
        const body: unknown = JSON.parse(await req.text());
        if (body && typeof body === "object" && !Array.isArray(body)) {
          const rec = body as Record<string, unknown>;
          const key = "name" in rec ? "name" : Object.keys(rec).find((k) => k.toLowerCase() === "name");
          if (key !== undefined && typeof rec[key] === "string") name = rec[key] as string;
        }
      } catch {
        // An unreadable body just means no name was given.
      }
    }
    try {
      const { token, identity } = this.opts.auth.issue(name);
      // Keys in sorted order, as Go encodes a map.
      return json(200, { name: identity.name, player_id: identity.player_id, token });
    } catch (e) {
      return json(400, { error: (e as Error).message });
    }
  }

  private handleWS(req: Request, url: URL, upgrade: Upgrader): Response {
    // Authenticate BEFORE upgrading: an unauthenticated socket never exists.
    let id: Identity;
    try {
      id = this.opts.auth.authenticate(tokenFrom(req, url));
    } catch (e) {
      return textError((e as Error).message, 401);
    }
    if (req.method !== "GET") {
      return textError("websocket: the client is not using the websocket protocol: request method is not GET", 405);
    }
    if ((req.headers.get("Upgrade") ?? "").toLowerCase() !== "websocket") {
      return textError(
        "websocket: the client is not using the websocket protocol: 'websocket' token not found in 'Upgrade' header",
        400,
      );
    }
    return upgrade(req, (sock) => new Session(this, sock, id));
  }

  get rooms(): Manager {
    return this.opts.rooms;
  }

  logf(line: string): void {
    this.log(line);
  }
}

/** Session is one connected player. It is the room's Client. */
export class Session implements Client {
  private room: Room | null = null;
  private bad = 0;
  private ended = false;

  constructor(
    private readonly srv: Server,
    private readonly sock: Socket,
    private readonly identity: Identity,
  ) {
    this.send(
      msg(SWelcome, { version: Version, player_id: identity.player_id, name: identity.name }),
    );
  }

  get id(): string {
    return this.identity.player_id;
  }

  get name(): string {
    return this.identity.name;
  }

  send(m: ServerMessage): void {
    if (this.ended) return;
    try {
      this.sock.send(JSON.stringify(m));
    } catch {
      // A socket that cannot take a message is dead: hang up rather than
      // leave the seat attached to it.
      this.end();
    }
  }

  /** Called by a room that has handed this seat to a newer connection. */
  close(): void {
    this.end();
  }

  /** The socket delivered a frame. */
  onMessage(data: string | ArrayBuffer): void {
    if (this.ended) return;
    const size = typeof data === "string" ? new TextEncoder().encode(data).length : data.byteLength;
    if (size > MaxMessageSize) {
      // Transport failure: an oversized frame ends the connection.
      this.end();
      return;
    }
    const text = typeof data === "string" ? data : new TextDecoder().decode(data);
    let m: ClientMessage;
    try {
      m = parseClientMessage(text);
    } catch {
      // Garbage from one frame costs an error reply, not a game.
      this.bad++;
      this.send(errorMsg("", ErrMalformed));
      if (this.bad >= maxBadFrames) this.end();
      return;
    }
    if (m.v !== Version) {
      this.send(
        errorMsg(
          m.id,
          wrap(ErrUnsupportedVersion, `this server speaks v${Version}, you sent v${m.v}`),
        ),
      );
      return;
    }
    this.bad = 0;
    this.handle(m);
  }

  /** The socket closed or errored. There is nothing left to close. */
  onClose(): void {
    this.end(false);
  }

  private end(closeSocket = true): void {
    if (this.ended) return;
    this.ended = true;
    const r = this.room;
    this.room = null;
    r?.disconnected(this);
    if (!closeSocket) return;
    try {
      this.sock.close();
    } catch {
      // already closed
    }
  }

  private fail(cid: string, err: unknown): void {
    this.send(errorMsg(cid, err));
  }

  private handle(m: ClientMessage): void {
    const rooms = this.srv.rooms;
    try {
      switch (m.type) {
        case CPing:
          this.send(reply(m.id, SPong));
          return;

        case CListRooms:
          this.send(reply(m.id, SRoomList, { rooms: rooms.list() }));
          return;

        case CCreateRoom: {
          const d = decodeCreateRoom(m.data);
          const r = rooms.create(d);
          this.leaveCurrent();
          this.room = r;
          r.join(this, m.id);
          return;
        }

        case CJoinRoom:
        case CReconnect: {
          const d = decodeJoinRoom(m.data);
          // Reconnect without a code: find the seat this identity holds.
          const r = d.code !== "" ? rooms.get(d.code) : rooms.findPlayerRoom(this.id);
          if (!r) {
            this.fail(m.id, ErrNoSuchRoom);
            return;
          }
          this.leaveCurrent();
          this.room = r;
          r.join(this, m.id);
          return;
        }

        case CLeaveRoom:
          this.leaveCurrent();
          return;

        case CReady: {
          const d = decodeReady(m.data);
          if (this.room) this.room.setReady(this.id, d.ready, m.id);
          else this.fail(m.id, ErrNotInRoom);
          return;
        }

        case CStartGame:
          if (this.room) this.room.start(this.id, m.id);
          else this.fail(m.id, ErrNotInRoom);
          return;

        case CRematch:
          if (this.room) this.room.rematch(this.id, m.id);
          else this.fail(m.id, ErrNotInRoom);
          return;

        case CAction: {
          const d = decodeAction(m.data);
          if (!this.room) {
            this.fail(m.id, ErrNotInRoom);
            return;
          }
          // THE authority boundary: the actor is this.id, which came from the
          // authenticated token. Any "player" field inside d is never consulted.
          this.room.act(this.id, d, m.id);
          return;
        }

        default:
          this.fail(m.id, new GameError("unknown message type: " + m.type));
      }
    } catch (e) {
      // Payload decode failures and room-creation refusals land here.
      this.fail(m.id, e);
    }
  }

  private leaveCurrent(): void {
    if (this.room) {
      this.room.leave(this.id);
      this.room = null;
    }
  }
}
