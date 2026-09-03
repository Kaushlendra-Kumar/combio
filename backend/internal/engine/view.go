package engine

// CardHandle is how EVERY face-down card is described to a client: position,
// physical identity, and deck colour. There is deliberately no rank or suit
// field on this type, so a hidden card cannot leak even by accident.
//
// Deck colour is public by design (§1): two-toned decks are what let players
// track physical cards across swaps.
type CardHandle struct {
	Position int       `json:"position"`
	ID       CardID    `json:"id"`
	Deck     DeckColor `json:"deck"`
}

type OpponentView struct {
	ID             PlayerID     `json:"id"`
	Seat           int          `json:"seat"`
	Cards          []CardHandle `json:"cards"`
	Count          int          `json:"count"`
	PendingPenalty int          `json:"pending_penalty"`
	Connected      bool         `json:"connected"`
	Removed        bool         `json:"removed"`
	TurnsCompleted int          `json:"turns_completed"`
}

// PlayerView is the only thing that should ever be serialised to a client.
type PlayerView struct {
	Me    PlayerID `json:"me"`
	Phase string   `json:"phase"`

	MyCards        []CardHandle `json:"my_cards"`
	MyPendingCount int          `json:"my_pending_count"`

	Opponents []OpponentView `json:"opponents"`

	TurnPlayer PlayerID `json:"turn_player"`
	MyTurn     bool     `json:"my_turn"`
	FinalTurns bool     `json:"final_turns"`

	DeckCount    int   `json:"deck_count"`
	DiscardCount int   `json:"discard_count"`
	DiscardTop   *Card `json:"discard_top"` // the only publicly identified card
	ActiveRank   Rank  `json:"active_rank"`

	// MyDrawnCard is populated ONLY for the player who drew it.
	MyDrawnCard *Card `json:"my_drawn_card"`

	// MyPendingPower is populated ONLY for the player resolving a power.
	MyPendingPower Rank `json:"my_pending_power"`

	DeclareAvailable bool     `json:"declare_available"`
	Declared         bool     `json:"declared"`
	Declarer         PlayerID `json:"declarer"`

	Paused   bool `json:"paused"`
	Finished bool `json:"finished"`

	// Populated only once the game is over.
	Scores  map[PlayerID]int    `json:"scores,omitempty"`
	Winners []PlayerID          `json:"winners,omitempty"`
	Reveal  map[PlayerID][]Card `json:"reveal,omitempty"`
}

func (s *State) handles(p *PlayerState) []CardHandle {
	ids := p.Layout.Cards()
	out := make([]CardHandle, 0, len(ids))
	for i, id := range ids {
		out = append(out, CardHandle{Position: i, ID: id, Deck: s.MustCard(id).Deck})
	}
	return out
}

// ViewFor builds the filtered, player-specific state (§14). Everything the
// server knows that this player must not know is simply absent from the result.
func (s *State) ViewFor(me PlayerID) PlayerView {
	v := PlayerView{
		Me:           me,
		Phase:        s.Phase.String(),
		TurnPlayer:   s.CurrentID(),
		MyTurn:       s.CurrentID() == me,
		FinalTurns:   s.Declared,
		DeckCount:    len(s.Deck),
		DiscardCount: len(s.Discard),
		ActiveRank:   s.ActiveRank,
		Declared:     s.Declared,
		Declarer:     s.Declarer,
		Paused:       s.Paused,
		Finished:     s.Phase == PhaseFinished,
	}
	if top := s.DiscardTop(); top != NoCard {
		c := s.MustCard(top)
		v.DiscardTop = &c
	}
	for i := range s.Players {
		p := &s.Players[i]
		if p.ID == me {
			v.MyCards = s.handles(p)
			v.MyPendingCount = len(p.PendingPenalty)
			continue
		}
		v.Opponents = append(v.Opponents, OpponentView{
			ID:             p.ID,
			Seat:           p.Seat,
			Cards:          s.handles(p),
			Count:          p.Layout.Len(),
			PendingPenalty: len(p.PendingPenalty),
			Connected:      p.Connected,
			Removed:        p.Removed,
			TurnsCompleted: p.TurnsCompleted,
		})
	}
	if s.Phase == PhaseDrawDecision && s.DrawnBy == me && s.Drawn != NoCard {
		c := s.MustCard(s.Drawn)
		v.MyDrawnCard = &c
	}
	if (s.Phase == PhasePower || s.Phase == PhaseKingDecision) && s.PowerActor == me {
		v.MyPendingPower = s.PendingPower
	}
	// §10: timing only. The player's real score is never consulted.
	v.DeclareAvailable = s.Phase == PhaseTurnStart &&
		s.CurrentID() == me &&
		!s.Declared &&
		!s.Paused &&
		s.DeclareUnlocked()

	if s.Phase == PhaseFinished {
		v.Scores = map[PlayerID]int{}
		for k, val := range s.Scores {
			v.Scores[k] = val
		}
		v.Winners = append([]PlayerID(nil), s.Winners...)
		v.Reveal = map[PlayerID][]Card{}
		for i := range s.Players {
			p := &s.Players[i]
			if !p.Active() {
				continue
			}
			cards := make([]Card, 0, p.Layout.Len())
			for _, id := range p.Layout.Cards() {
				cards = append(cards, s.MustCard(id))
			}
			v.Reveal[p.ID] = cards
		}
	}
	return v
}

// FilterEvents drops events this player is not entitled to see. Transport code
// must call this rather than broadcasting the raw event slice.
func FilterEvents(evs []Event, me PlayerID) []Event {
	out := make([]Event, 0, len(evs))
	for _, e := range evs {
		aud := e.Audience()
		if aud == nil {
			out = append(out, e)
			continue
		}
		for _, id := range aud {
			if id == me {
				out = append(out, e)
				break
			}
		}
	}
	return out
}
