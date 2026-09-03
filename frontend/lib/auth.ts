import { env } from "./env";

/**
 * DEVELOPMENT AUTHENTICATION ONLY.
 *
 * This talks to the Go server's /auth/dev endpoint, which mints an opaque
 * token bound to a server-generated player id. There is no password, no
 * verification and no expiry: anyone who can reach the endpoint can mint an
 * identity. It exists so the game is playable before real auth lands.
 *
 * Everything else in the app depends only on `Session`, so replacing this file
 * is the whole job when production auth arrives.
 */
export interface Session {
  token: string;
  playerId: string;
  name: string;
}

const STORAGE_KEY = "combio.dev-session";

export async function signInAsGuest(name: string): Promise<Session> {
  const res = await fetch(`${env.apiUrl}/auth/dev?name=${encodeURIComponent(name)}`, {
    method: "POST",
  });
  if (!res.ok) {
    throw new Error(`sign-in failed (${res.status})`);
  }
  const body: unknown = await res.json();
  if (
    typeof body !== "object" ||
    body === null ||
    typeof (body as Record<string, unknown>).token !== "string" ||
    typeof (body as Record<string, unknown>).player_id !== "string"
  ) {
    throw new Error("sign-in returned an unexpected response");
  }
  const b = body as { token: string; player_id: string; name?: string };
  // The player id comes from the server. The client never invents one.
  const session: Session = { token: b.token, playerId: b.player_id, name: b.name ?? name };
  saveSession(session);
  return session;
}

export function saveSession(s: Session): void {
  if (typeof window === "undefined") return;
  window.sessionStorage.setItem(STORAGE_KEY, JSON.stringify(s));
}

/**
 * Sessions live in sessionStorage, so each tab is a separate player. That is
 * what makes two-tab local testing work, and it means a refresh keeps your
 * seat while a new tab gets a new identity.
 */
export function loadSession(): Session | null {
  if (typeof window === "undefined") return null;
  const raw = window.sessionStorage.getItem(STORAGE_KEY);
  if (!raw) return null;
  try {
    const parsed: unknown = JSON.parse(raw);
    if (
      typeof parsed === "object" &&
      parsed !== null &&
      typeof (parsed as Session).token === "string" &&
      typeof (parsed as Session).playerId === "string"
    ) {
      return parsed as Session;
    }
  } catch {
    // fall through
  }
  return null;
}

export function clearSession(): void {
  if (typeof window === "undefined") return;
  window.sessionStorage.removeItem(STORAGE_KEY);
}
