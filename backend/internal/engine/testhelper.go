package engine

import (
	"fmt"
	"strings"
)

// This file is deliberately NOT _test.go: Phase 2's simulation harness needs the
// same deterministic scenario builder that the unit tests use.

// ParseCardSpec reads a short card string: "8S", "AH", "10D", "TD", "KC:B".
// The optional ":R" / ":B" suffix picks the deck colour (default red).
func ParseCardSpec(spec string) (Rank, Suit, DeckColor, error) {
	s := strings.ToUpper(strings.TrimSpace(spec))
	color := DeckRed
	if i := strings.IndexByte(s, ':'); i >= 0 {
		switch s[i+1:] {
		case "B", "BLUE":
			color = DeckBlue
		case "R", "RED":
			color = DeckRed
		default:
			return 0, 0, 0, fmt.Errorf("bad deck colour in %q", spec)
		}
		s = s[:i]
	}
	if len(s) < 2 {
		return 0, 0, 0, fmt.Errorf("bad card spec %q", spec)
	}
	suitCh := s[len(s)-1]
	rankStr := s[:len(s)-1]

	var suit Suit
	switch suitCh {
	case 'C':
		suit = Clubs
	case 'D':
		suit = Diamonds
	case 'H':
		suit = Hearts
	case 'S':
		suit = Spades
	default:
		return 0, 0, 0, fmt.Errorf("bad suit in %q", spec)
	}

	var rank Rank
	switch rankStr {
	case "A":
		rank = Ace
	case "T", "10":
		rank = Ten
	case "J":
		rank = Jack
	case "Q":
		rank = Queen
	case "K":
		rank = King
	default:
		n := 0
		if _, err := fmt.Sscanf(rankStr, "%d", &n); err != nil || n < 2 || n > 9 {
			return 0, 0, 0, fmt.Errorf("bad rank in %q", spec)
		}
		rank = Rank(n)
	}
	return rank, suit, color, nil
}

// Scenario pins an exact table so a test can assert precise behaviour.
type Scenario struct {
	Players []PlayerID
	// Hands maps a player to their layout, left to right.
	Hands map[PlayerID][]string
	// Deck is the draw deck; the LAST element is the top card, drawn first.
	Deck []string
	// DiscardTop is the card sitting face-up in the middle.
	DiscardTop string
	// Start is whose turn it is (defaults to the first player).
	Start PlayerID
	// TurnsCompleted seeds the two-round COMBIO gate for every player.
	TurnsCompleted int
	// Unlocked is shorthand for "two global rounds already completed".
	Unlocked bool
	// RoundsCompleted seeds the global round counter directly.
	RoundsCompleted int
}

// BuildScenario produces a State parked at PhaseTurnStart with the memory phase
// already done, containing exactly the cards named and nothing else. Card
// conservation is checked against this smaller universe, so tests stay fast and
// completely deterministic.
func BuildScenario(sc Scenario) (State, error) {
	if len(sc.Players) < 2 {
		return State{}, ErrPlayerCount
	}
	s := State{
		Config:       Config{CardsPerPlayer: 4, NumDecks: 1},
		Cards:        map[CardID]Card{},
		Disconnected: map[PlayerID]bool{},
		Phase:        PhaseTurnStart,
		shuffler:     NoShuffle{},
	}
	next := CardID(1)
	mk := func(spec string) (CardID, error) {
		r, su, col, err := ParseCardSpec(spec)
		if err != nil {
			return NoCard, err
		}
		id := next
		next++
		s.Cards[id] = Card{ID: id, Rank: r, Suit: su, Deck: col}
		return id, nil
	}

	turns := sc.TurnsCompleted
	if sc.Unlocked && turns < 2 {
		turns = 2
	}

	for seat, pid := range sc.Players {
		hand := make([]CardID, 0)
		for _, spec := range sc.Hands[pid] {
			id, err := mk(spec)
			if err != nil {
				return State{}, err
			}
			hand = append(hand, id)
		}
		s.Players = append(s.Players, PlayerState{
			ID:             pid,
			Seat:           seat,
			Layout:         NewLayout(hand),
			Connected:      true,
			MemoryDone:     true,
			TurnsCompleted: turns,
		})
	}
	s.startRound()
	if sc.RoundsCompleted > 0 {
		s.RoundsCompleted = sc.RoundsCompleted
	} else if turns >= 2 {
		s.RoundsCompleted = 2
	}
	if s.RoundsCompleted >= 2 {
		s.DeclareUnlockedFlag = true
	}

	for _, spec := range sc.Deck {
		id, err := mk(spec)
		if err != nil {
			return State{}, err
		}
		s.Deck = append(s.Deck, id)
	}

	top := sc.DiscardTop
	if top == "" {
		top = "2C"
	}
	id, err := mk(top)
	if err != nil {
		return State{}, err
	}
	s.Discard = []CardID{id}

	if sc.Start != "" {
		p := s.Player(sc.Start)
		if p == nil {
			return State{}, ErrUnknownPlayer
		}
		s.TurnIdx = p.Seat
	}
	if err := CheckInvariants(&s); err != nil {
		return State{}, err
	}
	return s, nil
}

// Hand renders a player's layout as readable specs. Test-only convenience.
func (s *State) Hand(id PlayerID) []string {
	p := s.Player(id)
	if p == nil {
		return nil
	}
	out := make([]string, 0, p.Layout.Len())
	for _, cid := range p.Layout.Cards() {
		c := s.MustCard(cid)
		out = append(out, fmt.Sprintf("%s%s", c.Rank, c.Suit))
	}
	return out
}

// FindEvent returns the first event of the requested name, or nil.
func FindEvent(evs []Event, name string) Event {
	for _, e := range evs {
		if e.EventName() == name {
			return e
		}
	}
	return nil
}

// HasEvent reports whether an event of that name was emitted.
func HasEvent(evs []Event, name string) bool { return FindEvent(evs, name) != nil }
