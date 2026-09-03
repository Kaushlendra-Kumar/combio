import {
  type ClientData,
  type ClientType,
  type ServerMessage,
  ProtocolError,
  clientMessage,
  parseServerMessage,
} from "./protocol";

export type ConnectionState = "idle" | "connecting" | "open" | "closing" | "closed";

/** Minimal surface a socket must have. Lets Node's `ws` stand in for tests. */
export interface SocketLike {
  send(data: string): void;
  close(code?: number, reason?: string): void;
  readyState: number;
  onopen: ((ev: unknown) => void) | null;
  onclose: ((ev: unknown) => void) | null;
  onerror: ((ev: unknown) => void) | null;
  onmessage: ((ev: { data: unknown }) => void) | null;
}

export type SocketFactory = (url: string) => SocketLike;

export interface ClientHandlers {
  onMessage(msg: ServerMessage): void;
  onState(state: ConnectionState): void;
  /** A frame we could not understand. The socket survives it. */
  onProtocolError(err: ProtocolError): void;
}

export interface ClientOptions {
  url: string;
  token: string;
  handlers: ClientHandlers;
  socketFactory?: SocketFactory;
  /** Backoff steps in ms. Reconnects stop after the list is exhausted. */
  retryDelays?: number[];
  setTimeoutImpl?: typeof setTimeout;
  clearTimeoutImpl?: typeof clearTimeout;
}

const DEFAULT_RETRIES = [500, 1000, 2000, 4000, 8000];

function defaultFactory(url: string): SocketLike {
  return new WebSocket(url) as unknown as SocketLike;
}

/**
 * One socket per game session.
 *
 * React mounts, unmounts and re-mounts components freely (twice in Strict
 * Mode), so this class refuses to open a second socket while one is live and
 * is safe to `connect()` repeatedly. It reconnects with bounded backoff and
 * stops entirely once `close()` is called, so a closed tab cannot leave a
 * retry loop spinning.
 */
export class GameClient {
  private socket: SocketLike | null = null;
  private state: ConnectionState = "idle";
  private retry = 0;
  private timer: ReturnType<typeof setTimeout> | null = null;
  private disposed = false;
  private seq = 0;

  private readonly retryDelays: number[];
  private readonly factory: SocketFactory;
  private readonly setT: typeof setTimeout;
  private readonly clearT: typeof clearTimeout;

  constructor(private readonly opts: ClientOptions) {
    this.retryDelays = opts.retryDelays ?? DEFAULT_RETRIES;
    this.factory = opts.socketFactory ?? defaultFactory;
    this.setT = opts.setTimeoutImpl ?? setTimeout;
    this.clearT = opts.clearTimeoutImpl ?? clearTimeout;
  }

  get connectionState(): ConnectionState {
    return this.state;
  }

  get isOpen(): boolean {
    return this.state === "open";
  }

  /** Idempotent: calling it while connecting or open does nothing. */
  connect(): void {
    if (this.disposed) return;
    if (this.state === "connecting" || this.state === "open") return;
    this.open();
  }

  private open(): void {
    this.setState("connecting");
    const url = `${this.opts.url}?token=${encodeURIComponent(this.opts.token)}`;
    let sock: SocketLike;
    try {
      sock = this.factory(url);
    } catch {
      this.scheduleRetry();
      return;
    }
    this.socket = sock;

    sock.onopen = () => {
      this.retry = 0;
      this.setState("open");
    };
    sock.onmessage = (ev) => {
      const raw = typeof ev.data === "string" ? ev.data : String(ev.data);
      try {
        this.opts.handlers.onMessage(parseServerMessage(raw));
      } catch (err) {
        if (err instanceof ProtocolError) {
          // A frame we cannot read is not a reason to drop the session.
          this.opts.handlers.onProtocolError(err);
          return;
        }
        throw err;
      }
    };
    sock.onerror = () => {
      // onclose always follows; retrying here would double up.
    };
    sock.onclose = () => {
      this.socket = null;
      if (this.disposed) {
        this.setState("closed");
        return;
      }
      this.scheduleRetry();
    };
  }

  private scheduleRetry(): void {
    const delay = this.retryDelays[this.retry];
    if (delay === undefined) {
      this.setState("closed");
      return;
    }
    this.retry += 1;
    this.setState("connecting");
    this.timer = this.setT(() => {
      this.timer = null;
      if (!this.disposed) this.open();
    }, delay);
  }

  private setState(next: ConnectionState): void {
    if (this.state === next) return;
    this.state = next;
    this.opts.handlers.onState(next);
  }

  /** Returns the correlation id, or null if the socket was not open. */
  send(type: ClientType, data?: ClientData): string | null {
    if (!this.socket || this.state !== "open") return null;
    this.seq += 1;
    const id = `c${this.seq}`;
    try {
      this.socket.send(JSON.stringify(clientMessage(type, data, id)));
    } catch {
      return null;
    }
    return id;
  }

  /** Stops for good: no further retries, listeners dropped. */
  close(): void {
    this.disposed = true;
    if (this.timer !== null) {
      this.clearT(this.timer);
      this.timer = null;
    }
    const sock = this.socket;
    this.socket = null;
    if (sock) {
      this.setState("closing");
      sock.onopen = null;
      sock.onmessage = null;
      sock.onerror = null;
      sock.onclose = null;
      try {
        sock.close();
      } catch {
        // already gone
      }
    }
    this.setState("closed");
  }
}
