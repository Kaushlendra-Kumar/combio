// Engine errors. Go's sentinel-error pattern is kept: each rule violation is
// one shared GameError, and wrapped errors keep a link to the sentinel they
// came from so errorIs can recognise them (the equivalent of errors.Is).

export class GameError extends Error {
  constructor(
    message: string,
    readonly base?: GameError,
  ) {
    super(message);
  }
}

export function sentinel(message: string): GameError {
  return new GameError(message);
}

/** wrap is fmt.Errorf("%w: detail", base). */
export function wrap(base: GameError, detail: string): GameError {
  return new GameError(`${base.message}: ${detail}`, base);
}

export function errorIs(err: unknown, target: GameError): boolean {
  let e: GameError | undefined = err instanceof GameError ? err : undefined;
  while (e) {
    if (e === target) return true;
    e = e.base;
  }
  return false;
}

export const ErrWrongPhase = sentinel("action not legal in this phase");
export const ErrNotYourTurn = sentinel("not your turn");
export const ErrUnknownPlayer = sentinel("unknown player");
export const ErrPlayerInactive = sentinel("player is not active");
export const ErrGameOver = sentinel("game is finished");
export const ErrPaused = sentinel("game is paused for a reconnect window");

// ErrStale means the physical card the client clicked has already left play
// or can no longer be resolved. Per §5 this is NOT a wrong match: no penalty,
// no state change (Example 5).
export const ErrStale = sentinel("stale action: that physical card is gone");

export const ErrBadIndex = sentinel("index out of range");
export const ErrDeclareLocked = sentinel("COMBIO is locked until two complete global rounds");
export const ErrAlreadyDeclared = sentinel("COMBIO has already been declared");
export const ErrAlreadyDrawn = sentinel("cannot declare after drawing");
export const ErrMemoryCount = sentinel("must inspect exactly half of your cards");
export const ErrAlreadyInspected = sentinel("memory phase already completed for this player");
export const ErrPowerMandatory = sentinel("this power cannot be skipped voluntarily");
export const ErrNoCardToGive = sentinel("you need at least one card to give");
export const ErrTargetEmpty = sentinel("a player with zero cards cannot be targeted");
export const ErrSelfTarget = sentinel("cannot target yourself with this action");
export const ErrNoPendingTray = sentinel("no penalty cards pending");

// ErrImpossibleState is the defensive fallback: a draw was required, the
// reshuffle could not produce a card, and that must never happen through legal
// play. The engine reports it and mutates nothing.
export const ErrImpossibleState = sentinel("impossible state");

export const ErrPlayerCount = sentinel("COMBIO supports 2-8 players");
export const ErrHandSize = sentinel("cards per player must be 4, 6 or 8");
