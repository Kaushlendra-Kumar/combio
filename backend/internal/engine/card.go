package engine

import "fmt"

// CardID is the identity of a single *physical* card. Two 8♠ from two different
// decks have different CardIDs. Knowledge in COMBIO follows the physical card,
// never the position, so nothing in the engine may identify a card by rank.
type CardID uint16

// NoCard is the zero value, meaning "no card here".
const NoCard CardID = 0

// Rank is 1..13. RankNone means "no active rank".
type Rank uint8

const (
	RankNone Rank = 0
	Ace      Rank = 1
	Two      Rank = 2
	Three    Rank = 3
	Four     Rank = 4
	Five     Rank = 5
	Six      Rank = 6
	Seven    Rank = 7
	Eight    Rank = 8
	Nine     Rank = 9
	Ten      Rank = 10
	Jack     Rank = 11
	Queen    Rank = 12
	King     Rank = 13
)

// Value implements §1: Ace is -1, everything else is its face value.
func (r Rank) Value() int {
	if r == Ace {
		return -1
	}
	return int(r)
}

// IsPower reports whether this rank carries a special power when it is the
// directly discarded drawn card (§8).
func (r Rank) IsPower() bool {
	return r == Ten || r == Jack || r == Queen || r == King
}

func (r Rank) String() string {
	switch r {
	case RankNone:
		return "-"
	case Ace:
		return "A"
	case Ten:
		return "10"
	case Jack:
		return "J"
	case Queen:
		return "Q"
	case King:
		return "K"
	default:
		return fmt.Sprintf("%d", int(r))
	}
}

// Suit is cosmetic in COMBIO: matching is rank-only.
type Suit uint8

const (
	Clubs Suit = iota
	Diamonds
	Hearts
	Spades
)

func (s Suit) String() string {
	switch s {
	case Clubs:
		return "C"
	case Diamonds:
		return "D"
	case Hearts:
		return "H"
	default:
		return "S"
	}
}

// DeckColor distinguishes the two physical decks used in 5-8 player games (§1).
// Deck colour is PUBLIC information for every card, including face-down ones:
// that is exactly what lets players track physical cards.
type DeckColor uint8

const (
	DeckRed DeckColor = iota
	DeckBlue
)

func (d DeckColor) String() string {
	if d == DeckBlue {
		return "B"
	}
	return "R"
}

// Card is immutable for the whole game. The State holds one master table of
// them; everything else refers to cards by CardID only.
type Card struct {
	ID   CardID    `json:"id"`
	Rank Rank      `json:"rank"`
	Suit Suit      `json:"suit"`
	Deck DeckColor `json:"deck"`
}

func (c Card) Value() int { return c.Rank.Value() }

func (c Card) String() string {
	return fmt.Sprintf("%s%s:%s#%d", c.Rank, c.Suit, c.Deck, c.ID)
}
