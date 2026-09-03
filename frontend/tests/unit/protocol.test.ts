import { describe, expect, it, vi } from "vitest";

import {
  PROTOCOL_VERSION,
  ProtocolError,
  clientMessage,
  parseServerMessage,
  printedValue,
  rankLabel,
} from "@/lib/protocol";
import { GameClient, type SocketLike } from "@/lib/websocket";

// ---------------------------------------------------------------- protocol

describe("protocol parsing", () => {
  it("accepts a well-formed current-version frame", () => {
    const msg = parseServerMessage(
      JSON.stringify({ v: 1, type: "pong", id: "c1" }),
    );
    expect(msg.type).toBe("pong");
    expect(msg.id).toBe("c1");
  });

  it("rejects malformed input without throwing anything unexpected", () => {
    for (const bad of ["{", "not json", "[1,2,3]", '"a string"', "null"]) {
      expect(() => parseServerMessage(bad)).toThrowError(ProtocolError);
      try {
        parseServerMessage(bad);
      } catch (e) {
        expect((e as ProtocolError).kind).toBe("malformed");
      }
    }
  });

  it("rejects an unsupported protocol version distinctly", () => {
    try {
      parseServerMessage(JSON.stringify({ v: 99, type: "pong" }));
      throw new Error("should have thrown");
    } catch (e) {
      expect(e).toBeInstanceOf(ProtocolError);
      expect((e as ProtocolError).kind).toBe("unsupported_version");
      expect((e as ProtocolError).message).toContain("v99");
    }
  });

  it("rejects a frame with no version and unknown types", () => {
    expect(() => parseServerMessage(JSON.stringify({ type: "pong" }))).toThrowError(ProtocolError);
    expect(() => parseServerMessage(JSON.stringify({ v: 1, type: "drop_tables" }))).toThrowError(
      ProtocolError,
    );
  });

  it("rejects a non-object payload", () => {
    expect(() =>
      parseServerMessage(JSON.stringify({ v: 1, type: "room_state", data: "nope" })),
    ).toThrowError(ProtocolError);
  });

  it("builds client frames with no room for an actor", () => {
    const msg = clientMessage("action", { kind: "draw_card" }, "c7");
    expect(msg.v).toBe(PROTOCOL_VERSION);
    const json = JSON.stringify(msg);
    for (const forbidden of ["player", "actor", "Base", "player_id"]) {
      expect(json).not.toContain(forbidden);
    }
  });

  it("labels ranks and prints Ace as -1 without deciding anything", () => {
    expect(rankLabel(1)).toBe("A");
    expect(rankLabel(11)).toBe("J");
    expect(rankLabel(7)).toBe("7");
    expect(printedValue(1)).toBe(-1);
    expect(printedValue(13)).toBe(13);
  });
});

// ------------------------------------------------------------ ws client

class FakeSocket implements SocketLike {
  static made: FakeSocket[] = [];
  readyState = 0;
  onopen: ((ev: unknown) => void) | null = null;
  onclose: ((ev: unknown) => void) | null = null;
  onerror: ((ev: unknown) => void) | null = null;
  onmessage: ((ev: { data: unknown }) => void) | null = null;
  sent: string[] = [];
  closed = false;

  constructor(readonly url: string) {
    FakeSocket.made.push(this);
  }
  send(data: string) {
    this.sent.push(data);
  }
  close() {
    this.closed = true;
    this.onclose?.({});
  }
  open() {
    this.readyState = 1;
    this.onopen?.({});
  }
  deliver(raw: string) {
    this.onmessage?.({ data: raw });
  }
}

function newClient() {
  FakeSocket.made = [];
  const messages: unknown[] = [];
  const states: string[] = [];
  const protocolErrors: ProtocolError[] = [];
  const client = new GameClient({
    url: "ws://test/ws",
    token: "tok tok",
    retryDelays: [10, 20],
    handlers: {
      onMessage: (m) => messages.push(m),
      onState: (s) => states.push(s),
      onProtocolError: (e) => protocolErrors.push(e),
    },
    socketFactory: (url) => new FakeSocket(url),
  });
  return { client, messages, states, protocolErrors };
}

describe("GameClient", () => {
  it("puts the token in the url, encoded", () => {
    const { client } = newClient();
    client.connect();
    expect(FakeSocket.made[0]?.url).toBe("ws://test/ws?token=tok%20tok");
    client.close();
  });

  it("never opens a second socket for repeated connect calls", () => {
    const { client } = newClient();
    client.connect();
    client.connect();
    FakeSocket.made[0]?.open();
    client.connect();
    client.connect();
    expect(FakeSocket.made).toHaveLength(1);
    client.close();
  });

  it("reports connection state transitions", () => {
    const { client, states } = newClient();
    client.connect();
    FakeSocket.made[0]?.open();
    expect(states).toEqual(["connecting", "open"]);
    client.close();
    expect(states.at(-1)).toBe("closed");
  });

  it("refuses to send while not open, and sends once open", () => {
    const { client } = newClient();
    expect(client.send("ping")).toBeNull();
    client.connect();
    expect(client.send("ping")).toBeNull();
    FakeSocket.made[0]?.open();
    const id = client.send("ping");
    expect(id).toBe("c1");
    expect(FakeSocket.made[0]?.sent).toHaveLength(1);
    client.close();
  });

  it("survives a malformed frame instead of dropping the session", () => {
    const { client, messages, protocolErrors } = newClient();
    client.connect();
    const sock = FakeSocket.made[0]!;
    sock.open();
    sock.deliver("{{{ not json");
    expect(protocolErrors).toHaveLength(1);
    expect(client.isOpen).toBe(true);
    sock.deliver(JSON.stringify({ v: 1, type: "pong" }));
    expect(messages).toHaveLength(1);
    client.close();
  });

  it("reconnects with backoff after an unexpected close", () => {
    vi.useFakeTimers();
    const { client, states } = newClient();
    client.connect();
    FakeSocket.made[0]?.open();
    FakeSocket.made[0]?.onclose?.({});
    expect(states.at(-1)).toBe("connecting");
    vi.advanceTimersByTime(10);
    expect(FakeSocket.made).toHaveLength(2);
    client.close();
    vi.useRealTimers();
  });

  it("stops retrying for good once closed, so a dead tab cannot spin", () => {
    vi.useFakeTimers();
    const { client } = newClient();
    client.connect();
    const sock = FakeSocket.made[0]!;
    sock.open();
    client.close();
    sock.onclose?.({});
    vi.advanceTimersByTime(10_000);
    expect(FakeSocket.made).toHaveLength(1);
    vi.useRealTimers();
  });

  it("gives up after the backoff list is exhausted", () => {
    vi.useFakeTimers();
    const { client, states } = newClient();
    client.connect();
    FakeSocket.made[0]?.onclose?.({});
    vi.advanceTimersByTime(10);
    FakeSocket.made[1]?.onclose?.({});
    vi.advanceTimersByTime(20);
    FakeSocket.made[2]?.onclose?.({});
    vi.advanceTimersByTime(1000);
    expect(states.at(-1)).toBe("closed");
    expect(FakeSocket.made).toHaveLength(3);
    client.close();
    vi.useRealTimers();
  });

  it("drops its listeners on close so nothing leaks", () => {
    const { client } = newClient();
    client.connect();
    const sock = FakeSocket.made[0]!;
    sock.open();
    client.close();
    expect(sock.onmessage).toBeNull();
    expect(sock.onclose).toBeNull();
    expect(sock.onopen).toBeNull();
    expect(sock.closed).toBe(true);
  });
});
