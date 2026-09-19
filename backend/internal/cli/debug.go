package cli

import (
	"fmt"
	"strings"

	"github.com/kaushlendra/combio/backend/internal/engine"
)

// This file is the single exception to the "render from PlayerView only" rule.
// It reads engine.State directly and is reachable only via --debug.

// DebugDump prints the complete authoritative state, hidden cards included.
func DebugDump(s *engine.State) string {
	var b strings.Builder
	b.WriteString("\n*** DEBUG MODE — HIDDEN CARDS VISIBLE ***\n")
	fmt.Fprintf(&b, "phase=%s  turn=%s  activeRank=%s  extensions=%d  paused=%v  declared=%v(%s)\n",
		s.Phase, s.CurrentID(), s.ActiveRank, s.MatchExtensions, s.Paused, s.Declared, s.Declarer)
	fmt.Fprintf(&b, "rounds completed=%d  pending this round=%v  COMBIO unlocked=%v\n",
		s.RoundsCompleted, s.RoundPending, s.DeclareUnlocked())
	if s.PendingPower != engine.RankNone {
		fmt.Fprintf(&b, "pending power=%s actor=%s\n", s.PendingPower, s.PowerActor)
	}
	if s.Drawn != engine.NoCard {
		fmt.Fprintf(&b, "drawn=%s by %s\n", card(s, s.Drawn), s.DrawnBy)
	}

	fmt.Fprintf(&b, "deck (%d, top last): %s\n", len(s.Deck), cards(s, s.Deck))
	fmt.Fprintf(&b, "discard (%d, bottom first): %s\n", len(s.Discard), cards(s, s.Discard))

	for i := range s.Players {
		p := &s.Players[i]
		fmt.Fprintf(&b, "  %-10s seat=%d v=%d turns=%d active=%v conn=%v score=%d\n",
			p.ID, p.Seat, p.Layout.Version(), p.TurnsCompleted, p.Active(), p.Connected, s.Score(p.ID))
		fmt.Fprintf(&b, "      layout: %s\n", cards(s, p.Layout.Cards()))
		if len(p.PendingPenalty) > 0 {
			fmt.Fprintf(&b, "      tray:   %s\n", cards(s, p.PendingPenalty))
		}
	}
	if err := engine.CheckInvariants(s); err != nil {
		fmt.Fprintf(&b, "INVARIANTS: FAILED — %v\n", err)
	} else {
		fmt.Fprintf(&b, "invariants: ok (%d cards conserved)\n", len(s.Cards))
	}
	b.WriteString("*****************************************\n")
	return b.String()
}

func card(s *engine.State, id engine.CardID) string {
	c, ok := s.Card(id)
	if !ok {
		return fmt.Sprintf("?%d", id)
	}
	return fmt.Sprintf("%s%s(%s)#%d", c.Rank, c.Suit, c.Deck, c.ID)
}

func cards(s *engine.State, ids []engine.CardID) string {
	if len(ids) == 0 {
		return "(none)"
	}
	parts := make([]string, 0, len(ids))
	for _, id := range ids {
		parts = append(parts, card(s, id))
	}
	return strings.Join(parts, " ")
}
