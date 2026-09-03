package engine

import "errors"

// layoutHistoryDepth is how many past orderings we keep per player so that a
// client action carrying an older layoutVersion can still be resolved back to
// the physical card the player was actually looking at (§5). Older than this
// and the action is treated as stale, which is always the safe answer.
const layoutHistoryDepth = 32

// Layout is a player's ordered row of face-down cards (§3).
//
//   - order is meaningful and is the player's only memory scaffold
//   - gaps ALWAYS close: there are never empty slots
//   - every mutation bumps version, and the pre-mutation ordering is retained
//     so concurrent client actions can be resolved by physical card identity
type Layout struct {
	cards   []CardID
	version uint64
	history map[uint64][]CardID
	ring    []uint64
}

func NewLayout(cards []CardID) Layout {
	l := Layout{
		cards:   append([]CardID(nil), cards...),
		version: 1,
		history: map[uint64][]CardID{},
	}
	return l
}

func (l *Layout) Len() int        { return len(l.cards) }
func (l *Layout) Version() uint64 { return l.version }
func (l *Layout) Empty() bool     { return len(l.cards) == 0 }

// Cards returns a copy; the caller can never mutate the layout by accident.
func (l *Layout) Cards() []CardID { return append([]CardID(nil), l.cards...) }

func (l *Layout) At(pos int) (CardID, bool) {
	if pos < 0 || pos >= len(l.cards) {
		return NoCard, false
	}
	return l.cards[pos], true
}

// IndexOf returns the current position of a physical card, or -1.
func (l *Layout) IndexOf(id CardID) int {
	for i, c := range l.cards {
		if c == id {
			return i
		}
	}
	return -1
}

func (l *Layout) Contains(id CardID) bool { return l.IndexOf(id) >= 0 }

// Resolve maps a (version, index) pair from a client back to a physical card.
//
// This is the heart of the stale-vs-wrong distinction in §5: the client tells
// us which version it was rendering, we look up what sat at that index then,
// and we get the CardID the human actually clicked. Whether that card is still
// here is a separate question the caller asks with Contains.
func (l *Layout) Resolve(version uint64, pos int) (CardID, bool) {
	if version == 0 || version == l.version {
		return l.At(pos)
	}
	snap, ok := l.history[version]
	if !ok {
		return NoCard, false // too old to resolve -> caller treats as stale
	}
	if pos < 0 || pos >= len(snap) {
		return NoCard, false
	}
	return snap[pos], true
}

// bump records the current ordering under the current version, then advances
// the version. Called before every mutation.
func (l *Layout) bump() {
	if l.history == nil {
		l.history = map[uint64][]CardID{}
	}
	l.history[l.version] = append([]CardID(nil), l.cards...)
	l.ring = append(l.ring, l.version)
	for len(l.ring) > layoutHistoryDepth {
		delete(l.history, l.ring[0])
		l.ring = l.ring[1:]
	}
	l.version++
}

var errBadPosition = errors.New("position out of range")

// Insert places a card at pos. Valid pos is 0..Len (inclusive), so a card can
// go before everything, between any two cards, or at the end (§3).
func (l *Layout) Insert(pos int, id CardID) error {
	if pos < 0 || pos > len(l.cards) {
		return errBadPosition
	}
	l.bump()
	l.cards = append(l.cards, NoCard)
	copy(l.cards[pos+1:], l.cards[pos:])
	l.cards[pos] = id
	return nil
}

// Append adds to the end (used for auto-placed penalty cards at window close).
func (l *Layout) Append(id CardID) { _ = l.Insert(len(l.cards), id) }

// RemoveAt removes the card at pos and CLOSES THE GAP (§3). No empty slots
// ever persist: the player must not be able to use holes as memory markers.
func (l *Layout) RemoveAt(pos int) (CardID, error) {
	if pos < 0 || pos >= len(l.cards) {
		return NoCard, errBadPosition
	}
	id := l.cards[pos]
	l.bump()
	l.cards = append(l.cards[:pos], l.cards[pos+1:]...)
	return id, nil
}

// RemoveCard removes a physical card wherever it currently sits and returns
// the position it vacated.
func (l *Layout) RemoveCard(id CardID) (int, error) {
	pos := l.IndexOf(id)
	if pos < 0 {
		return -1, errBadPosition
	}
	if _, err := l.RemoveAt(pos); err != nil {
		return -1, err
	}
	return pos, nil
}

// Swap replaces the card at pos with in, returning the card that was there.
// Used by Queen and King: swapped cards take the exact positions vacated by
// the cards they replace (§3), with no reordering choice for either player.
func (l *Layout) Swap(pos int, in CardID) (CardID, error) {
	if pos < 0 || pos >= len(l.cards) {
		return NoCard, errBadPosition
	}
	out := l.cards[pos]
	l.bump()
	l.cards[pos] = in
	return out, nil
}

func (l *Layout) clone() Layout {
	n := Layout{
		cards:   append([]CardID(nil), l.cards...),
		version: l.version,
		history: make(map[uint64][]CardID, len(l.history)),
		ring:    append([]uint64(nil), l.ring...),
	}
	for k, v := range l.history {
		n.history[k] = append([]CardID(nil), v...)
	}
	return n
}
