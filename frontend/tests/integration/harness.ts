import { spawn, type ChildProcess } from "node:child_process";
import path from "node:path";
import WebSocket from "ws";

import { GameClient, type SocketLike } from "@/lib/websocket";
import { initialState, reduce, type GameState } from "@/lib/game-state";
import { makeActions, type Actions } from "@/lib/store";
import type { ServerMessage } from "@/lib/protocol";

const BACKEND = path.resolve(__dirname, "../../../backend");

/**
 * The real Go server, built and run for the test.
 *
 * Nothing here is a stub: these tests exercise the same protocol layer, the
 * same WebSocket client and the same reducer the browser uses, against the
 * actual Phase 3 binary.
 */
export class Server {
  private proc: ChildProcess | null = null;
  readonly port: number;

  constructor(port: number) {
    this.port = port;
  }

  get httpUrl(): string {
    return `http://127.0.0.1:${this.port}`;
  }
  get wsUrl(): string {
    return `ws://127.0.0.1:${this.port}/ws`;
  }

  async start(): Promise<void> {
    const bin = path.join("/tmp", `combio-server-${this.port}`);
    await run("go", ["build", "-o", bin, "./cmd/server"], BACKEND);
    this.proc = spawn(bin, ["--addr", `127.0.0.1:${this.port}`], {
      stdio: ["ignore", "pipe", "pipe"],
    });
    await this.waitForHealth();
  }

  private async waitForHealth(): Promise<void> {
    const deadline = Date.now() + 20_000;
    while (Date.now() < deadline) {
      try {
        const res = await fetch(`${this.httpUrl}/healthz`);
        if (res.ok) return;
      } catch {
        // not up yet
      }
      await sleep(100);
    }
    throw new Error("the server never became healthy");
  }

  stop(): void {
    this.proc?.kill("SIGKILL");
    this.proc = null;
  }
}

function run(cmd: string, args: string[], cwd: string): Promise<void> {
  return new Promise((resolve, reject) => {
    const p = spawn(cmd, args, { cwd, stdio: ["ignore", "pipe", "pipe"] });
    let err = "";
    p.stderr?.on("data", (d: Buffer) => (err += d.toString()));
    p.on("close", (code) => (code === 0 ? resolve() : reject(new Error(`${cmd} failed: ${err}`))));
  });
}

export function sleep(ms: number): Promise<void> {
  return new Promise((r) => setTimeout(r, ms));
}

/**
 * One browser, headless.
 *
 * It keeps the exact state the real app would keep (same reducer) and records
 * every raw frame it received, so a test can inspect the serialized boundary
 * rather than trusting the rendered output.
 */
export class TestClient {
  state: GameState = initialState;
  token = "";
  readonly frames: string[] = [];
  readonly actions: Actions;
  private client: GameClient;

  private constructor(
    readonly name: string,
    readonly playerId: string,
    token: string,
    wsUrl: string,
  ) {
    this.client = new GameClient({
      url: wsUrl,
      token,
      retryDelays: [50, 100],
      handlers: {
        onMessage: (msg: ServerMessage) => {
          this.state = reduce(this.state, { type: "server", msg });
        },
        onState: (s) => {
          this.state = reduce(this.state, { type: "connection", state: s });
        },
        onProtocolError: (e) => {
          this.state = reduce(this.state, {
            type: "protocol-error",
            kind: e.kind,
            message: e.message,
          });
        },
      },
      socketFactory: (url) => {
        const sock = new WebSocket(url) as unknown as SocketLike & {
          addEventListener?: unknown;
        };
        // Record the wire bytes before the client parses them.
        const ws = sock as unknown as WebSocket;
        ws.on("message", (data: Buffer | string) => this.frames.push(data.toString()));
        return sock;
      },
    });
    this.actions = makeActions(
      () => this.client,
      (a) => {
        this.state = reduce(this.state, a);
      },
    );
  }

  static async connect(server: Server, name: string): Promise<TestClient> {
    const res = await fetch(`${server.httpUrl}/auth/dev?name=${encodeURIComponent(name)}`, {
      method: "POST",
    });
    if (!res.ok) throw new Error(`dev auth failed: ${res.status}`);
    const body = (await res.json()) as { token: string; player_id: string };
    return TestClient.connectWithToken(server, name, body.player_id, body.token);
  }

  /** Reconnect with an existing token, which is what a browser refresh does. */
  static async connectWithToken(
    server: Server,
    name: string,
    playerId: string,
    token: string,
  ): Promise<TestClient> {
    const c = new TestClient(name, playerId, token, server.wsUrl);
    c.token = token;
    c.client.connect();
    await c.until(() => c.state.connection === "open" && c.state.session !== null, "welcome");
    return c;
  }

  /** Send a raw frame, for hostile-payload tests. */
  sendRaw(body: string): void {
    const anyClient = this.client as unknown as { socket: { send(d: string): void } | null };
    anyClient.socket?.send(body);
  }

  close(): void {
    this.client.close();
  }

  /** Waits for a condition on this client's state. */
  async until(pred: () => boolean, what: string, timeoutMs = 8000): Promise<void> {
    const deadline = Date.now() + timeoutMs;
    while (Date.now() < deadline) {
      if (pred()) return;
      await sleep(10);
    }
    throw new Error(
      `${this.name}: timed out waiting for ${what}\nphase=${this.state.view?.phase} room=${this.state.room?.status}`,
    );
  }

  /** Every event name this client has been sent, in order. */
  eventNames(): string[] {
    const names: string[] = [];
    for (const raw of this.frames) {
      const m = JSON.parse(raw) as { type: string; data?: { events?: { name: string }[] } };
      if (m.type === "events") {
        for (const e of m.data?.events ?? []) names.push(e.name);
      }
    }
    return names;
  }

  lastErrorCode(): string | null {
    return this.state.notice?.code ?? null;
  }
}

/** Drives both clients through the lobby into a started game. */
export async function startGame(
  host: TestClient,
  guest: TestClient,
  opts: { cardsPerPlayer?: number } = {},
): Promise<string> {
  host.actions.createRoom({
    public: true,
    cardsPerPlayer: opts.cardsPerPlayer ?? 4,
    maxPlayers: 4,
  });
  await host.until(() => !!host.state.room?.code, "room code");
  const code = host.state.room!.code;

  guest.actions.joinRoom(code);
  await guest.until(() => guest.state.room?.players.length === 2, "guest seated");
  await host.until(() => host.state.room?.players.length === 2, "host sees guest");

  host.actions.setReady(true);
  guest.actions.setReady(true);
  await host.until(() => host.state.room?.status === "READY", "room ready");

  host.actions.startGame();
  await host.until(() => host.state.view?.phase === "memory", "memory phase");
  await guest.until(() => guest.state.view?.phase === "memory", "memory phase");
  return code;
}

/** Both players complete the memory phase with the count the server asked for. */
export async function finishMemory(...clients: TestClient[]): Promise<void> {
  for (const c of clients) {
    const n = c.state.inspectCount;
    c.actions.inspectMemory(Array.from({ length: n }, (_, i) => i));
  }
  for (const c of clients) {
    await c.until(() => c.state.view?.phase === "turn_start", "play to begin");
  }
}

export function whoseTurn(clients: TestClient[]): TestClient {
  const view = clients[0]!.state.view!;
  const c = clients.find((x) => x.playerId === view.turn_player);
  if (!c) throw new Error(`turn player ${view.turn_player} is not one of the clients`);
  return c;
}
