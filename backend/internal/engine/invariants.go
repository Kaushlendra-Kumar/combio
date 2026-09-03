package engine

import "fmt"

// CheckInvariants runs after every accepted transition. If any of these ever
// fail, Apply rejects the action and returns the untouched original state, so a
// bug can never corrupt a live game.
//
// Invariant 1 is the important one. Since the Phase 0 correction, no card ever
// leaves the game — a disconnect MOVES cards to the bottom of the discard pile
// rather than deleting them — so the total is constant for the whole game and a
// single equality check catches almost any card-handling bug.
func CheckInvariants(s *State) error {
	seen := make(map[CardID]bool, len(s.Cards))
	count := 0

	add := func(ids []CardID, where string) error {
		for _, id := range ids {
			if id == NoCard {
				return fmt.Errorf("invariant: NoCard found in %s", where)
			}
			if _, ok := s.Cards[id]; !ok {
				return fmt.Errorf("invariant: unknown card %d in %s", id, where)
			}
			if seen[id] {
				return fmt.Errorf("invariant: card %d appears twice (%s)", id, where)
			}
			seen[id] = true
			count++
		}
		return nil
	}

	if err := add(s.Deck, "deck"); err != nil {
		return err
	}
	if err := add(s.Discard, "discard"); err != nil {
		return err
	}
	for i := range s.Players {
		p := &s.Players[i]
		if err := add(p.Layout.Cards(), "layout:"+string(p.ID)); err != nil {
			return err
		}
		if err := add(p.PendingPenalty, "tray:"+string(p.ID)); err != nil {
			return err
		}
		if p.Removed && (p.Layout.Len() > 0 || len(p.PendingPenalty) > 0) {
			return fmt.Errorf("invariant: removed player %s still holds cards", p.ID)
		}
	}
	if s.Drawn != NoCard {
		if err := add([]CardID{s.Drawn}, "drawn"); err != nil {
			return err
		}
	}

	if count != len(s.Cards) {
		return fmt.Errorf("invariant: card conservation broken, accounted %d of %d | %s",
			count, len(s.Cards), s.Debug())
	}

	// Phase consistency.
	if (s.ActiveRank != RankNone) != (s.Phase == PhaseMatching) {
		return fmt.Errorf("invariant: activeRank=%s but phase=%s", s.ActiveRank, s.Phase)
	}
	if (s.Drawn != NoCard) != (s.Phase == PhaseDrawDecision) {
		return fmt.Errorf("invariant: drawn card present but phase=%s", s.Phase)
	}
	if s.Phase != PhaseKingDecision && s.KingPeek != nil {
		return fmt.Errorf("invariant: king peek state outside king decision phase")
	}
	if len(s.Discard) == 0 {
		return fmt.Errorf("invariant: discard pile must never be empty after setup")
	}
	// The pending round set may only ever name active players.
	for _, id := range s.RoundPending {
		q := s.Player(id)
		if q == nil || !q.Active() {
			return fmt.Errorf("invariant: round pending set names inactive player %s", id)
		}
	}

	// Trays only exist while a matching window is open.
	if s.Phase != PhaseMatching {
		for i := range s.Players {
			if len(s.Players[i].PendingPenalty) > 0 {
				return fmt.Errorf("invariant: %s has an unplaced penalty tray in phase %s",
					s.Players[i].ID, s.Phase)
			}
		}
	}
	return nil
}
