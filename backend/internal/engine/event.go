package engine

// Event is something that happened. Audience returns nil for public events and
// an explicit recipient list for private ones. The transport layer must respect
// it: this is the only mechanism by which private reveals stay private (§14).
type Event interface {
	EventName() string
	Audience() []PlayerID
}

type pub struct{}

func (pub) Audience() []PlayerID { return nil }

type priv struct{ To PlayerID }

func (p priv) Audience() []PlayerID { return []PlayerID{p.To} }

// ---------- lifecycle ----------

type EvGameStarted struct {
	pub            `json:"-"`
	Players        []PlayerID `json:"players"`
	CardsPerPlayer int        `json:"cards_per_player"`
	NumDecks       int        `json:"num_decks"`
}

func (EvGameStarted) EventName() string { return "game_started" }

type EvMemoryPhaseStarted struct {
	pub     `json:"-"`
	Inspect int `json:"inspect"`
}

func (EvMemoryPhaseStarted) EventName() string { return "memory_phase_started" }

// EvMemoryRevealed is private: the selected cards, shown once, then gone.
// There is no persistent memory assistance anywhere (§2).
type EvMemoryRevealed struct {
	priv      `json:"-"`
	Cards     []Card `json:"cards"`
	Positions []int  `json:"positions"`
}

func (EvMemoryRevealed) EventName() string { return "memory_revealed" }

type EvMemoryPhaseEnded struct {
	pub `json:"-"`
}

func (EvMemoryPhaseEnded) EventName() string { return "memory_phase_ended" }

type EvTurnStarted struct {
	pub    `json:"-"`
	Player PlayerID `json:"player"`
}

func (EvTurnStarted) EventName() string { return "turn_started" }

type EvDeclareUnlocked struct {
	pub `json:"-"`
}

func (EvDeclareUnlocked) EventName() string { return "declare_unlocked" }

// ---------- draw / discard ----------

// EvCardDrawn is public: everyone sees THAT you drew, never WHAT.
type EvCardDrawn struct {
	pub    `json:"-"`
	Player PlayerID `json:"player"`
}

func (EvCardDrawn) EventName() string { return "card_drawn" }

type EvPrivateDraw struct {
	priv `json:"-"`
	Card Card `json:"card"`
}

func (EvPrivateDraw) EventName() string { return "private_draw" }

// EvCardKept is Option A. The removed card is revealed because it is discarded;
// the kept card's identity is not, only where it landed.
type EvCardKept struct {
	pub    `json:"-"`
	Player PlayerID `json:"player"`
	// Removed is nil when a zero-card player simply gained a card.
	Removed     *Card `json:"removed"`
	InsertIndex int   `json:"insert_index"`
}

func (EvCardKept) EventName() string { return "card_kept" }

type EvCardDiscarded struct {
	pub        `json:"-"`
	Player     PlayerID `json:"player"`
	Card       Card     `json:"card"`
	ActiveRank Rank     `json:"active_rank"`
	FromDrawn  bool     `json:"from_drawn"`
}

func (EvCardDiscarded) EventName() string { return "card_discarded" }

// ---------- matching ----------

type EvMatchingWindowOpened struct {
	pub        `json:"-"`
	ActiveRank Rank `json:"active_rank"`
}

func (EvMatchingWindowOpened) EventName() string { return "matching_window_opened" }

type EvMatchingWindowExtended struct {
	pub     `json:"-"`
	Seconds int `json:"seconds"`
}

func (EvMatchingWindowExtended) EventName() string { return "matching_window_extended" }

type EvMatchingWindowClosed struct {
	pub `json:"-"`
}

func (EvMatchingWindowClosed) EventName() string { return "matching_window_closed" }

type EvMatchAccepted struct {
	pub    `json:"-"`
	Player PlayerID `json:"player"`
	// Owner is whose card left play; it differs for opponent targeting.
	Owner PlayerID `json:"owner"`
	Card  Card     `json:"card"`
}

func (EvMatchAccepted) EventName() string { return "match_accepted" }

type EvMatchFailed struct {
	pub    `json:"-"`
	Player PlayerID `json:"player"`
	// Thrown is set for a wrong own-card throw; that card is discarded, so it is public.
	Thrown *Card    `json:"thrown"`
	Target PlayerID `json:"target"`
}

func (EvMatchFailed) EventName() string { return "match_failed" }

type EvPenaltyApplied struct {
	pub    `json:"-"`
	Player PlayerID `json:"player"`
	Count  int      `json:"count"`
}

func (EvPenaltyApplied) EventName() string { return "penalty_applied" }

type EvPenaltyPlaced struct {
	pub      `json:"-"`
	Player   PlayerID `json:"player"`
	Position int      `json:"position"`
	Auto     bool     `json:"auto"`
}

func (EvPenaltyPlaced) EventName() string { return "penalty_placed" }

// EvCardTransferred is the §7 hand-over after a successful opponent match. The
// identity is deliberately absent: only the giver knows what they gave.
type EvCardTransferred struct {
	pub      `json:"-"`
	From     PlayerID `json:"from"`
	To       PlayerID `json:"to"`
	Position int      `json:"position"`
}

func (EvCardTransferred) EventName() string { return "card_transferred" }

// ---------- powers ----------

type EvPowerAvailable struct {
	priv `json:"-"`
	Rank Rank `json:"rank"`
}

func (EvPowerAvailable) EventName() string { return "power_available" }

type EvPrivateReveal struct {
	priv     `json:"-"`
	Owner    PlayerID `json:"owner"`
	Position int      `json:"position"`
	Card     Card     `json:"card"`
}

func (EvPrivateReveal) EventName() string { return "private_reveal" }

type EvPowerSkipped struct {
	pub    `json:"-"`
	Player PlayerID `json:"player"`
	Rank   Rank     `json:"rank"`
	Reason string   `json:"reason"`
}

func (EvPowerSkipped) EventName() string { return "power_skipped" }

// EvCardsSwapped names positions only. A Queen swap is blind: nobody, including
// the swapper, learns either identity from the event.
type EvCardsSwapped struct {
	pub         `json:"-"`
	Player      PlayerID `json:"player"`
	OwnIndex    int      `json:"own_index"`
	Target      PlayerID `json:"target"`
	TargetIndex int      `json:"target_index"`
	// Rank names the power that did it, not a card.
	Rank Rank `json:"power"`
}

func (EvCardsSwapped) EventName() string { return "cards_swapped" }

// ---------- endgame ----------

type EvCombioDeclared struct {
	pub    `json:"-"`
	Player PlayerID `json:"player"`
}

func (EvCombioDeclared) EventName() string { return "combio_declared" }

type EvGameFinished struct {
	pub               `json:"-"`
	Scores            map[PlayerID]int    `json:"scores"`
	Winners           []PlayerID          `json:"winners"`
	Declarer          PlayerID            `json:"declarer"`
	DeclarationFailed bool                `json:"declaration_failed"`
	Reveal            map[PlayerID][]Card `json:"reveal"`
}

func (EvGameFinished) EventName() string { return "game_finished" }

// ---------- connection ----------

type EvPlayerDisconnected struct {
	pub    `json:"-"`
	Player PlayerID `json:"player"`
}

func (EvPlayerDisconnected) EventName() string { return "player_disconnected" }

type EvPlayerReconnected struct {
	pub    `json:"-"`
	Player PlayerID `json:"player"`
}

func (EvPlayerReconnected) EventName() string { return "player_reconnected" }

type EvGamePaused struct {
	pub `json:"-"`
}

func (EvGamePaused) EventName() string { return "game_paused" }

type EvGameResumed struct {
	pub `json:"-"`
}

func (EvGameResumed) EventName() string { return "game_resumed" }

type EvPlayerRemoved struct {
	pub           `json:"-"`
	Player        PlayerID `json:"player"`
	CardsToBottom int      `json:"cards_to_bottom"`
}

func (EvPlayerRemoved) EventName() string { return "player_removed" }

type EvDeckReshuffled struct {
	pub      `json:"-"`
	DeckSize int `json:"deck_size"`
}

func (EvDeckReshuffled) EventName() string { return "deck_reshuffled" }

type EvTurnTimedOut struct {
	pub    `json:"-"`
	Player PlayerID `json:"player"`
}

func (EvTurnTimedOut) EventName() string { return "turn_timed_out" }
