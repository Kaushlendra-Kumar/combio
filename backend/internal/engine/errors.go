package engine

import (
	"errors"
	"fmt"
)

var (
	ErrWrongPhase     = errors.New("action not legal in this phase")
	ErrNotYourTurn    = errors.New("not your turn")
	ErrUnknownPlayer  = errors.New("unknown player")
	ErrPlayerInactive = errors.New("player is not active")
	ErrGameOver       = errors.New("game is finished")
	ErrPaused         = errors.New("game is paused for a reconnect window")

	// ErrStale means the physical card the client clicked has already left play
	// or can no longer be resolved. Per §5 this is NOT a wrong match: no penalty,
	// no state change (Example 5).
	ErrStale = errors.New("stale action: that physical card is gone")

	ErrBadIndex         = errors.New("index out of range")
	ErrDeclareLocked    = errors.New("COMBIO is locked until two complete global rounds")
	ErrAlreadyDeclared  = errors.New("COMBIO has already been declared")
	ErrAlreadyDrawn     = errors.New("cannot declare after drawing")
	ErrMemoryCount      = errors.New("must inspect exactly half of your cards")
	ErrAlreadyInspected = errors.New("memory phase already completed for this player")
	ErrPowerMandatory   = errors.New("this power cannot be skipped voluntarily")
	ErrNoCardToGive     = errors.New("you need at least one card to give")
	ErrTargetEmpty      = errors.New("a player with zero cards cannot be targeted")
	ErrSelfTarget       = errors.New("cannot target yourself with this action")
	ErrNoPendingTray    = errors.New("no penalty cards pending")

	// ErrImpossibleState is the defensive fallback demanded by the Phase 0
	// correction: a draw was required, the reshuffle could not produce a card,
	// and that must never happen through legal play. The engine reports it with
	// the full state and mutates nothing; it never panics and is never surfaced
	// to players as a rule.
	ErrImpossibleState = errors.New("impossible state")
)

// impossible wraps ErrImpossibleState with a complete state dump for logging.
func impossible(s *State, reason string) error {
	return fmt.Errorf("%w: %s | %s", ErrImpossibleState, reason, s.Debug())
}
