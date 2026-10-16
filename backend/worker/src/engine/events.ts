import type { PlayerID } from "./state";

/**
 * Event is something that happened. `to` is absent for public events and names
 * the single recipient of a private one. The transport layer must respect it:
 * this is the only mechanism by which private reveals stay private (§14).
 *
 * `data` is exactly what goes on the wire, with keys in wire order.
 */
export interface GameEvent {
  readonly name: string;
  readonly to?: PlayerID;
  readonly data: Record<string, unknown>;
}

export function pub(name: string, data: Record<string, unknown> = {}): GameEvent {
  return { name, data };
}

export function priv(to: PlayerID, name: string, data: Record<string, unknown>): GameEvent {
  return { name, to, data };
}

/**
 * filterEvents drops events this player is not entitled to see. Transport code
 * must call this rather than broadcasting the raw event list.
 */
export function filterEvents(evs: readonly GameEvent[], me: PlayerID): GameEvent[] {
  return evs.filter((e) => e.to === undefined || e.to === me);
}
