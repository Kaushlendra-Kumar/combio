package cli

import (
	"fmt"

	"github.com/kaushlendra/combio/backend/internal/engine"
)

// matchingWindow runs the local stand-in for a real-time race.
//
// Online this is a few seconds of everyone clicking at once. On one shared
// device that is impossible, so the window is walked in a fixed clockwise
// order starting with the player after whoever discarded, and each player may
// keep attempting for as long as they want before passing on. The engine rules
// and actions are exactly the same either way; only the turn-taking is local.
func (s *Session) matchingWindow() {
	v := s.anyView()
	rank := v.ActiveRank
	discarder := v.TurnPlayer

	s.publicBoard()
	s.io.Printf("\n=== MATCHING WINDOW — active rank is %s ===\n", rank)
	s.io.Println(" Everyone may throw cards of that rank, including the player who discarded.")
	s.io.Println(" You may also target a card you believe you know in someone else's hand.")
	s.io.Println(" Guess wrong and you take two face-down penalty cards.")
	s.io.Pause("\n (press ENTER to start the window) ")

	for _, p := range s.orderFrom(discarder) {
		if s.io.EOF() || s.phase() != "matching" {
			break
		}
		if s.isRemoved(p.ID) {
			continue
		}
		s.matchAttemptsFor(p, rank)
	}

	if s.phase() == "matching" {
		s.apply(engine.TimerExpired{Kind: engine.TimerMatchWindow})
		s.io.Println("\n Window closed.")
		s.feedTail(8)
		if !s.anyView().Finished {
			s.io.Pause(" (press ENTER) ")
		}
	}
}

// orderFrom returns every seat clockwise starting AFTER the given player, with
// that player last. Others get first crack; the discarder can still act.
func (s *Session) orderFrom(after engine.PlayerID) []Player {
	start := 0
	for i, p := range s.players {
		if p.ID == after {
			start = i
			break
		}
	}
	out := make([]Player, 0, len(s.players))
	for i := 1; i <= len(s.players); i++ {
		out = append(out, s.players[(start+i)%len(s.players)])
	}
	return out
}

func (s *Session) isRemoved(id engine.PlayerID) bool {
	v := s.anyView()
	if v.Me == id {
		return false
	}
	for _, o := range v.Opponents {
		if o.ID == id {
			return o.Removed
		}
	}
	return false
}

func (s *Session) matchAttemptsFor(p Player, rank engine.Rank) {
	if p.Bot {
		s.botMatch(p, rank)
		return
	}
	s.handOver(p)
	for !s.io.EOF() && s.phase() == "matching" {
		v := s.privateBoard(p)
		s.io.Printf(" Active rank is %s. You may act as often as you like.\n", rank)

		opts := []Option{{"n", "Pass — I'm done with this window"}}
		if len(v.MyCards) > 0 {
			opts = append([]Option{{"o", "Throw one of MY cards at the pile"}}, opts...)
			if s.hasTargetableOpponent(v) {
				opts = append([]Option{{"t", "Target a card in someone ELSE's hand"}}, opts...)
			}
		}
		if v.MyPendingCount > 0 {
			opts = append([]Option{{"p", fmt.Sprintf("Place one of my %d pending penalty cards", v.MyPendingCount)}}, opts...)
		}

		switch s.io.Choose(fmt.Sprintf(" %s — attempt a match?", p.Name), opts) {
		case "o":
			idx := s.io.Int(" which of your positions", 0, len(v.MyCards)-1)
			s.apply(engine.AttemptOwnMatch{
				Base: engine.For(p.ID), Index: idx, Version: 0,
			})
			s.feedTail(3)
		case "t":
			tgt := s.pickOpponent(v)
			if tgt == "" {
				continue
			}
			tv := s.opponent(v, tgt)
			s.io.Printf(" %s has %d cards: %s\n", s.names.of(tgt), tv.Count, handles(tv.Cards))
			ti := s.io.Int(" which of their positions", 0, tv.Count-1)
			s.io.Println(" if you are right, you must hand them one of your own cards.")
			gi := s.io.Int(" which of your positions do you give away", 0, len(v.MyCards)-1)
			s.apply(engine.AttemptOpponentMatch{
				Base: engine.For(p.ID), Target: tgt, TargetIndex: ti, GiveIndex: gi,
			})
			s.feedTail(3)
		case "p":
			s.placePenalty(p)
		default:
			s.handBack(p)
			return
		}
	}
	s.handBack(p)
}

func (s *Session) placePenalty(p Player) {
	v := s.view(p.ID)
	if v.MyPendingCount == 0 {
		return
	}
	s.io.Printf(" You hold %d penalty card(s). You do NOT get to see them.\n", v.MyPendingCount)
	tray := 0
	if v.MyPendingCount > 1 {
		tray = s.io.Int(" which pending card (order they arrived)", 0, v.MyPendingCount-1)
	}
	pos := s.io.Int(" insert it at which position in your hand", 0, len(v.MyCards))
	s.apply(engine.InsertPenalty{Base: engine.For(p.ID), TrayIndex: tray, Position: pos})
}

func (s *Session) hasTargetableOpponent(v engine.PlayerView) bool {
	for _, o := range v.Opponents {
		if !o.Removed && o.Count > 0 {
			return true
		}
	}
	return false
}

func (s *Session) pickOpponent(v engine.PlayerView) engine.PlayerID {
	choices := []Option{}
	byKey := map[string]engine.PlayerID{}
	for i, o := range v.Opponents {
		if o.Removed || o.Count == 0 {
			continue
		}
		key := fmt.Sprintf("%d", i)
		byKey[key] = o.ID
		choices = append(choices, Option{key, fmt.Sprintf("%s (%d cards)", s.names.of(o.ID), o.Count)})
	}
	if len(choices) == 0 {
		return ""
	}
	choices = append(choices, Option{"n", "never mind"})
	k := s.io.Choose(" target which player", choices)
	return byKey[k]
}

func (s *Session) opponent(v engine.PlayerView, id engine.PlayerID) engine.OpponentView {
	for _, o := range v.Opponents {
		if o.ID == id {
			return o
		}
	}
	return engine.OpponentView{}
}
