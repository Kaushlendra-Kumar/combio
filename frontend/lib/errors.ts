/**
 * Server error codes are stable strings (backend/internal/protocol). This maps
 * them to something a player can read. Anything unmapped falls back to the
 * server's own message, which the backend guarantees never contains a card.
 */
const MESSAGES: Record<string, string> = {
  stale: "Too late \u2014 that card was already taken.",
  not_your_turn: "It is not your turn.",
  wrong_phase: "You can't do that right now.",
  paused: "The game is paused while someone reconnects.",
  game_over: "The game has finished.",
  declare_locked: "COMBIO isn't unlocked yet.",
  already_declared: "COMBIO has already been declared.",
  already_drawn: "You've already drawn \u2014 too late to declare.",
  memory_count: "Pick exactly the number of cards asked for.",
  already_inspected: "You've already looked at your cards.",
  power_mandatory: "You have to use this power.",
  no_card_to_give: "You need a card of your own to hand over.",
  target_empty: "That player has no cards to target.",
  self_target: "You can't target yourself with that.",
  no_pending_tray: "You have no penalty cards waiting.",
  bad_index: "That card isn't there.",
  unknown_player: "That player isn't in this game.",
  player_inactive: "That player is out of the game.",
  unknown_action: "This client sent something the server didn't recognise.",
  unsupported_version: "This page is out of date. Reload to get the latest version.",
  malformed: "Something went wrong sending that. Try again.",
  internal: "The server couldn't complete that. Nothing changed.",
  rejected: "That move was rejected.",
};

export function errorMessage(code: string, fallback: string): string {
  return MESSAGES[code] ?? fallback ?? "Something went wrong.";
}

/**
 * Losing a click race is normal play, not a mistake. The UI shows it quietly
 * and it never implies a penalty.
 */
export function isBenign(code: string): boolean {
  return code === "stale";
}
