package engine

import (
	"errors"
	"math/rand"
)

// Shuffler is the engine's only source of randomness. Injecting it is what
// makes every test deterministic: production passes a seeded RNG, tests pass
// StackedShuffler and get an exact, known deck order.
type Shuffler interface {
	Shuffle(ids []CardID)
}

// SeededShuffler is the production implementation.
type SeededShuffler struct{ rng *rand.Rand }

func NewSeededShuffler(seed int64) *SeededShuffler {
	return &SeededShuffler{rng: rand.New(rand.NewSource(seed))}
}

func (s *SeededShuffler) Shuffle(ids []CardID) {
	s.rng.Shuffle(len(ids), func(i, j int) { ids[i], ids[j] = ids[j], ids[i] })
}

// NoShuffle leaves order untouched. Useful for "deal in build order" tests.
type NoShuffle struct{}

func (NoShuffle) Shuffle([]CardID) {}

// StackedShuffler forces an exact ordering. The order given is the order the
// slice ends up in, so tests can pin who is dealt what and what is drawn next.
// Any card not named in Order keeps its relative position after the named ones.
type StackedShuffler struct{ Order []CardID }

func (s *StackedShuffler) Shuffle(ids []CardID) {
	want := make(map[CardID]int, len(s.Order))
	for i, id := range s.Order {
		want[id] = i
	}
	head := make([]CardID, 0, len(s.Order))
	tail := make([]CardID, 0, len(ids))
	seen := make([]CardID, len(s.Order))
	for _, id := range ids {
		if pos, ok := want[id]; ok {
			seen[pos] = id
			continue
		}
		tail = append(tail, id)
	}
	for _, id := range seen {
		if id != NoCard {
			head = append(head, id)
		}
	}
	copy(ids, append(head, tail...))
}

var errBadDeckCount = errors.New("deck count must be 1 or 2")

// BuildDeck creates 52*n unique physical cards with sequential CardIDs.
// With two decks the second is DeckBlue so players can visually track physical
// cards even when both copies of 8♠ are in play (§1).
func BuildDeck(numDecks int) ([]Card, error) {
	if numDecks != 1 && numDecks != 2 {
		return nil, errBadDeckCount
	}
	out := make([]Card, 0, 52*numDecks)
	id := CardID(1)
	for d := 0; d < numDecks; d++ {
		color := DeckRed
		if d == 1 {
			color = DeckBlue
		}
		for _, suit := range []Suit{Clubs, Diamonds, Hearts, Spades} {
			for r := Ace; r <= King; r++ {
				out = append(out, Card{ID: id, Rank: r, Suit: suit, Deck: color})
				id++
			}
		}
	}
	return out, nil
}

// DecksFor implements §1: 2-4 players use one deck, 5-8 use two.
func DecksFor(players int) int {
	if players >= 5 {
		return 2
	}
	return 1
}
