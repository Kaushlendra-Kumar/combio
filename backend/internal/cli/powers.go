package cli

import (
	"github.com/kaushlendra/combio/backend/internal/engine"
)

// resolvePower prompts the player whose discarded drawn card armed a power.
// It runs after the matching window has closed, never alongside it.
func (s *Session) resolvePower() {
	v := s.anyView()
	actor := s.player(v.TurnPlayer)

	// The view only exposes MyPendingPower to its owner, so find the actor by
	// asking each seat whether the power is theirs.
	for _, p := range s.players {
		if s.view(p.ID).MyPendingPower != engine.RankNone {
			actor = p
			break
		}
	}
	pv := s.view(actor.ID)
	if pv.MyPendingPower == engine.RankNone {
		// Nothing to resolve (already fizzled); let the timer move things on.
		s.apply(engine.TimerExpired{Kind: engine.TimerPower})
		return
	}

	if actor.Bot {
		s.botPower(actor)
		return
	}

	s.handOver(actor)
	pv = s.privateBoard(actor)
	s.io.Printf("\n POWER: %s\n", powerName(pv.MyPendingPower))

	switch pv.MyPendingPower {
	case engine.Ten:
		if s.phaseIs("power") {
			idx := s.io.Int(" peek at which of your own positions", 0, len(pv.MyCards)-1)
			evs, ok := s.apply(engine.Peek10{Base: engine.For(actor.ID), Index: idx})
			if ok {
				s.showPrivate(actor, evs)
			}
		}
	case engine.Jack:
		tgt := s.pickOpponent(pv)
		if tgt == "" {
			s.apply(engine.TimerExpired{Kind: engine.TimerPower})
			break
		}
		o := s.opponent(pv, tgt)
		idx := s.io.Int(" look at which of their positions", 0, o.Count-1)
		evs, ok := s.apply(engine.JackPeek{Base: engine.For(actor.ID), Target: tgt, TargetIndex: idx})
		if ok {
			s.showPrivate(actor, evs)
		}
	case engine.Queen:
		if !s.io.YesNo(" Use the Queen? (a blind swap — neither card is shown)") {
			s.apply(engine.SkipPower{Base: engine.For(actor.ID)})
			break
		}
		tgt := s.pickOpponent(pv)
		if tgt == "" {
			s.apply(engine.SkipPower{Base: engine.For(actor.ID)})
			break
		}
		o := s.opponent(pv, tgt)
		own := s.io.Int(" swap which of your positions", 0, len(pv.MyCards)-1)
		theirs := s.io.Int(" with which of their positions", 0, o.Count-1)
		s.apply(engine.QueenSwap{
			Base: engine.For(actor.ID), OwnIndex: own, Target: tgt, TargetIndex: theirs,
		})
	case engine.King:
		tgt := s.pickOpponent(pv)
		if tgt == "" {
			s.apply(engine.TimerExpired{Kind: engine.TimerPower})
			break
		}
		o := s.opponent(pv, tgt)
		idx := s.io.Int(" look at which of their positions", 0, o.Count-1)
		evs, ok := s.apply(engine.KingPeek{Base: engine.For(actor.ID), Target: tgt, TargetIndex: idx})
		if !ok {
			break
		}
		s.showPrivate(actor, evs)
		if s.phaseIs("king_decision") {
			if s.io.YesNo(" Swap that card into your hand?") {
				own := s.io.Int(" give away which of your positions", 0, len(s.view(actor.ID).MyCards)-1)
				s.apply(engine.KingSwap{Base: engine.For(actor.ID), OwnIndex: own})
			} else {
				s.apply(engine.CancelKingSwap{Base: engine.For(actor.ID)})
			}
		}
	}
	s.handBack(actor)
}

func (s *Session) phaseIs(p string) bool { return s.phase() == p }
