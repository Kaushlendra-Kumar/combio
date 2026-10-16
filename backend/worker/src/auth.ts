// Resolves a connection to a player identity.
//
// Dev auth only: /auth/dev mints an opaque random token bound to a
// server-generated player id. A client can never choose its own id.

import { GameError } from "./engine/errors";
import type { PlayerID } from "./engine/state";

export const ErrNoToken = new GameError("no auth token supplied");
export const ErrBadToken = new GameError("unknown or expired auth token");
export const ErrInvalidName = new GameError("display name must be 1-24 printable characters");

/** Who the server believes is on the other end. player_id is stable per identity. */
export interface Identity {
  player_id: PlayerID;
  name: string;
}

/**
 * Where tokens live. The Durable Object keeps them in its SQLite storage so a
 * player's saved session survives the object being evicted or redeployed; the
 * in-memory version is for tests.
 */
export interface TokenStore {
  get(token: string): Identity | undefined;
  put(token: string, id: Identity): void;
  /** Returns the next player number, starting at 1. */
  nextId(): number;
}

export class MemoryTokenStore implements TokenStore {
  private byTok = new Map<string, Identity>();
  private n = 0;

  get(token: string): Identity | undefined {
    return this.byTok.get(token);
  }

  put(token: string, id: Identity): void {
    this.byTok.set(token, id);
  }

  nextId(): number {
    return ++this.n;
  }
}

export class DevTokens {
  constructor(private readonly store: TokenStore) {}

  /** Mints a token for a display name. The player id is generated here. */
  issue(rawName: string): { token: string; identity: Identity } {
    let name = rawName.trim();
    if (name === "") name = "Player";
    // The 24 limit counts UTF-8 bytes, as the Go server did.
    if (new TextEncoder().encode(name).length > 24 || !printable(name)) throw ErrInvalidName;
    const buf = new Uint8Array(16);
    crypto.getRandomValues(buf);
    const token = [...buf].map((b) => b.toString(16).padStart(2, "0")).join("");
    const identity = { player_id: `u${this.store.nextId()}`, name };
    this.store.put(token, identity);
    return { token, identity };
  }

  authenticate(token: string): Identity {
    if (token.trim() === "") throw ErrNoToken;
    const id = this.store.get(token);
    if (!id) throw ErrBadToken;
    return id;
  }
}

function printable(s: string): boolean {
  for (const ch of s) {
    const c = ch.codePointAt(0)!;
    if (c < 0x20 || c === 0x7f) return false;
  }
  return true;
}
