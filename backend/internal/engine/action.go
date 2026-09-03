package engine

// Action is any input the engine accepts. Every action names its actor so the
// transition layer can authorise it without the caller having to.
//
// Note on layout versions: only the matching actions carry one. The matching
// window is the ONLY phase where several players act concurrently, so it is the
// only place an index can go stale under a client's fingers. Everywhere else
// the actor is the sole mover and plain indices are unambiguous.
type Action interface {
	Actor() PlayerID
	Name() string
}

// Base carries the actor. It is embedded in every action and is exported so
// that packages outside the engine (the CLI, and later the WebSocket layer) can
// construct actions.
type Base struct{ Player PlayerID }

func (b Base) Actor() PlayerID { return b.Player }

// ---------- memory phase ----------

// InspectMemory selects exactly half of your own positions to see (§2).
type InspectMemory struct {
	Base
	Positions []int
}

func (InspectMemory) Name() string { return "inspect_memory" }

// ---------- turn ----------

// DeclareCombio is only legal at the start of your own turn, before drawing,
// and only after the two-round global gate (§10). It never consults your score.
type DeclareCombio struct{ Base }

func (DeclareCombio) Name() string { return "declare_combio" }

type DrawCard struct{ Base }

func (DrawCard) Name() string { return "draw_card" }

// KeepDrawn is Option A (§4). RemoveIndex is resolved against the current
// layout; InsertIndex is the position in the layout AFTER the removal, so it
// ranges 0..Len-1 and may be the vacated gap or anywhere else.
type KeepDrawn struct {
	Base
	RemoveIndex int
	InsertIndex int
}

func (KeepDrawn) Name() string { return "keep_drawn" }

// DiscardDrawn is Option B (§4): the only route to a special power.
type DiscardDrawn struct{ Base }

func (DiscardDrawn) Name() string { return "discard_drawn" }

// ---------- matching window ----------

// AttemptOwnMatch throws one of your own cards at the active rank (§5).
type AttemptOwnMatch struct {
	Base
	Index   int
	Version uint64
}

func (AttemptOwnMatch) Name() string { return "attempt_own_match" }

// AttemptOpponentMatch targets a physical card you believe you know in another
// player's layout, and names which of your own cards you will hand over if you
// are right (§7).
type AttemptOpponentMatch struct {
	Base
	Target        PlayerID
	TargetIndex   int
	TargetVersion uint64
	GiveIndex     int
	GiveVersion   uint64
}

func (AttemptOpponentMatch) Name() string { return "attempt_opponent_match" }

// InsertPenalty places one card from your pending tray into your layout, sight
// unseen (§6). Non-blocking: you may keep attempting matches with cards still
// in the tray, and anything left is auto-appended when the window closes.
type InsertPenalty struct {
	Base
	TrayIndex int
	Position  int
}

func (InsertPenalty) Name() string { return "insert_penalty" }

// ---------- powers ----------

type Peek10 struct {
	Base
	Index int
}

func (Peek10) Name() string { return "peek_10" }

type JackPeek struct {
	Base
	Target      PlayerID
	TargetIndex int
}

func (JackPeek) Name() string { return "jack_peek" }

type QueenSwap struct {
	Base
	OwnIndex    int
	Target      PlayerID
	TargetIndex int
}

func (QueenSwap) Name() string { return "queen_swap" }

type KingPeek struct {
	Base
	Target      PlayerID
	TargetIndex int
}

func (KingPeek) Name() string { return "king_peek" }

type KingSwap struct {
	Base
	OwnIndex int
}

func (KingSwap) Name() string { return "king_swap" }

type CancelKingSwap struct{ Base }

func (CancelKingSwap) Name() string { return "cancel_king_swap" }

// SkipPower is only legal for the Queen, which §8 marks optional. The 10, Jack
// and King peek are mandatory and can only lapse via the power timer.
type SkipPower struct{ Base }

func (SkipPower) Name() string { return "skip_power" }

// ---------- system ----------

type TimerKind uint8

const (
	TimerMemory TimerKind = iota
	TimerTurn
	TimerMatchWindow
	TimerPower
)

func (t TimerKind) String() string {
	switch t {
	case TimerMemory:
		return "memory"
	case TimerTurn:
		return "turn"
	case TimerMatchWindow:
		return "match_window"
	default:
		return "power"
	}
}

// TimerExpired is how time enters a pure engine. The driver owns the clock; the
// engine only ever sees a discrete expiry action, which keeps tests exact.
type TimerExpired struct {
	Base
	Kind TimerKind
}

func (TimerExpired) Name() string { return "timer_expired" }

// PlayerDisconnected pauses the entire game and freezes every timer (§12).
type PlayerDisconnected struct{ Base }

func (PlayerDisconnected) Name() string { return "player_disconnected" }

type PlayerReconnected struct{ Base }

func (PlayerReconnected) Name() string { return "player_reconnected" }

// RemovePlayer is issued by the driver when the 30s reconnect window lapses.
// The player leaves the rotation and their cards sink to the bottom of the
// discard pile — they are never destroyed (§12).
type RemovePlayer struct{ Base }

func (RemovePlayer) Name() string { return "remove_player" }

// For is shorthand for the embedded Base: engine.DrawCard{Base: engine.For(id)}.
func For(p PlayerID) Base { return Base{Player: p} }
