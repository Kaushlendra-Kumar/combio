import type { PlayerID } from "./state";

/**
 * Action is any input the engine accepts. Every action names its actor so the
 * transition layer can authorise it without the caller having to.
 *
 * Only the matching actions carry a layout version: the matching window is the
 * ONLY phase where several players act concurrently, so it is the only place an
 * index can go stale under a client's fingers.
 */
export type Action =
  | { kind: "inspect_memory"; player: PlayerID; positions: number[] }
  | { kind: "declare_combio"; player: PlayerID }
  | { kind: "draw_card"; player: PlayerID }
  | { kind: "keep_drawn"; player: PlayerID; removeIndex: number; insertIndex: number }
  | { kind: "discard_drawn"; player: PlayerID }
  | { kind: "attempt_own_match"; player: PlayerID; index: number; version: number }
  | {
      kind: "attempt_opponent_match";
      player: PlayerID;
      target: PlayerID;
      targetIndex: number;
      targetVersion: number;
      giveIndex: number;
      giveVersion: number;
    }
  | { kind: "insert_penalty"; player: PlayerID; trayIndex: number; position: number }
  | { kind: "peek_10"; player: PlayerID; index: number }
  | { kind: "jack_peek"; player: PlayerID; target: PlayerID; targetIndex: number }
  | { kind: "queen_swap"; player: PlayerID; ownIndex: number; target: PlayerID; targetIndex: number }
  | { kind: "king_peek"; player: PlayerID; target: PlayerID; targetIndex: number }
  | { kind: "king_swap"; player: PlayerID; ownIndex: number }
  | { kind: "cancel_king_swap"; player: PlayerID }
  | { kind: "skip_power"; player: PlayerID }
  // ---- system ----
  // Time enters the pure engine only as a discrete expiry; the driver owns the clock.
  | { kind: "timer_expired"; player: PlayerID; timer: TimerKind }
  // §12: a disconnect pauses the game; the driver removes the player when
  // the reconnect window lapses.
  | { kind: "player_disconnected"; player: PlayerID }
  | { kind: "player_reconnected"; player: PlayerID }
  | { kind: "remove_player"; player: PlayerID };

export type TimerKind = "memory" | "turn" | "match_window" | "power";
