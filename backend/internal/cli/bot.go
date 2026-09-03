package cli

import (
	"math/rand"

	"github.com/kaushlendra/combio/backend/internal/engine"
)

// The bot is deliberately dumb. Its only real constraint is honesty: every
// function here takes an engine.PlayerView (and, where a rule allows it, that
// player's own private events). None of them can see engine.State, so the bot
// physically cannot cheat.

// BotBrain holds one bot's randomness and its memory.
//
// The memory is filled ONLY from private events that were addressed to this
// bot: its own memory-phase reveals, its own draws, and the peeks its own
// powers earned. Each bot has its own brain, and events reach it through
// engine.FilterEvents, so one bot can never learn another's cards.
type BotBrain struct {
	rng  *rand.Rand
	seen map[engine.CardID]engine.Rank
}

func NewBotBrain(seed int64) *BotBrain {
	return &BotBrain{rng: rand.New(rand.NewSource(seed)), seen: map[engine.CardID]engine.Rank{}}
}

// Remember files away cards this bot was legitimately shown. The caller must
// pass events already filtered for this bot.
func (b *BotBrain) Remember(mine []engine.Event) {
	for _, e := range mine {
		switch ev := e.(type) {
		case engine.EvMemoryRevealed:
			for _, c := range ev.Cards {
				b.seen[c.ID] = c.Rank
			}
		case engine.EvPrivateDraw:
			b.seen[ev.Card.ID] = ev.Card.Rank
		case engine.EvPrivateReveal:
			b.seen[ev.Card.ID] = ev.Card.Rank
		}
	}
}

// FindOwn returns a position in this bot's hand holding a card it remembers as
// the active rank, or -1.
func (b *BotBrain) FindOwn(v engine.PlayerView, rank engine.Rank) int {
	for _, h := range v.MyCards {
		if b.seen[h.ID] == rank {
			return h.Position
		}
	}
	return -1
}

// FindOpponent returns a remembered opponent card of the active rank.
func (b *BotBrain) FindOpponent(v engine.PlayerView, rank engine.Rank) (engine.PlayerID, int, bool) {
	for _, o := range v.Opponents {
		if o.Removed {
			continue
		}
		for _, h := range o.Cards {
			if b.seen[h.ID] == rank {
				return o.ID, h.Position, true
			}
		}
	}
	return "", 0, false
}

// BotMemoryPositions picks which of its own cards to inspect.
func (b *BotBrain) BotMemoryPositions(v engine.PlayerView) []int {
	n := len(v.MyCards) / 2
	out := make([]int, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, i)
	}
	return out
}

// BotDeclares decides whether to call COMBIO. It cannot see its own score, so
// this is a pure gamble, exactly as it is for a forgetful human.
func (b *BotBrain) BotDeclares(v engine.PlayerView) bool {
	if !v.DeclareAvailable {
		return false
	}
	// Fewer cards is a better bet, so declare more eagerly with a small hand.
	odds := 8
	if len(v.MyCards) <= 2 {
		odds = 3
	}
	return b.rng.Intn(odds) == 0
}

// BotKeepsDrawn looks at the card it just drew, which it is entitled to see.
func (b *BotBrain) BotKeepsDrawn(v engine.PlayerView, drawn engine.Card) bool {
	val := drawn.Value()
	return val <= 4 || (val <= 7 && b.rng.Intn(3) == 0)
}

// BotGambles is the blind punt it takes when it remembers nothing useful.
func (b *BotBrain) BotGambles(v engine.PlayerView) bool {
	return len(v.MyCards) > 0 && b.rng.Intn(14) == 0
}

func (b *BotBrain) Pick(n int) int {
	if n <= 0 {
		return 0
	}
	return b.rng.Intn(n)
}

// ---------- session wiring ----------

// brainFor hands out one private brain per bot.
func (s *Session) brainFor(id engine.PlayerID) *BotBrain {
	if s.bots == nil {
		s.bots = map[engine.PlayerID]*BotBrain{}
	}
	if b, ok := s.bots[id]; ok {
		return b
	}
	b := NewBotBrain(s.rng.Int63())
	s.bots[id] = b
	return b
}

func (s *Session) botTurnStart(p Player) {
	v := s.view(p.ID)
	if s.brainFor(p.ID).BotDeclares(v) {
		if _, ok := s.apply(engine.DeclareCombio{Base: engine.For(p.ID)}); ok {
			s.io.Printf(" %s declares COMBIO.\n", p.Name)
			return
		}
	}
	if _, ok := s.apply(engine.DrawCard{Base: engine.For(p.ID)}); !ok {
		return
	}
	s.io.Printf(" %s draws.\n", p.Name)
	s.botDrawDecision(p)
}

func (s *Session) botDrawDecision(p Player) {
	v := s.view(p.ID)
	if v.MyDrawnCard == nil {
		return
	}
	b := s.brainFor(p.ID)
	keep := b.BotKeepsDrawn(v, *v.MyDrawnCard)
	switch {
	case keep && len(v.MyCards) == 0:
		s.apply(engine.KeepDrawn{Base: engine.For(p.ID)})
		s.io.Printf(" %s keeps it as their first card.\n", p.Name)
	case keep:
		rm := b.Pick(len(v.MyCards))
		ins := b.Pick(len(v.MyCards))
		s.apply(engine.KeepDrawn{Base: engine.For(p.ID), RemoveIndex: rm, InsertIndex: ins})
		s.io.Printf(" %s keeps it, dropping position %d.\n", p.Name, rm)
	default:
		s.apply(engine.DiscardDrawn{Base: engine.For(p.ID)})
		s.io.Printf(" %s discards it.\n", p.Name)
	}
}

func (s *Session) botMatch(p Player, rank engine.Rank) {
	b := s.brainFor(p.ID)

	// Throw every card it actually remembers holding at this rank.
	for s.phase() == "matching" {
		v := s.view(p.ID)
		idx := b.FindOwn(v, rank)
		if idx < 0 {
			break
		}
		if _, ok := s.apply(engine.AttemptOwnMatch{Base: engine.For(p.ID), Index: idx}); !ok {
			break
		}
		s.io.Printf(" %s throws a remembered %s.\n", p.Name, rank)
	}

	// Then strip a remembered opponent card, if it has one to give away.
	if v := s.view(p.ID); s.phase() == "matching" && len(v.MyCards) > 0 {
		if tgt, ti, ok := b.FindOpponent(v, rank); ok {
			if _, ok := s.apply(engine.AttemptOpponentMatch{
				Base: engine.For(p.ID), Target: tgt, TargetIndex: ti,
				GiveIndex: b.Pick(len(v.MyCards)),
			}); ok {
				s.io.Printf(" %s knocks a remembered %s out of %s.\n", p.Name, rank, s.names.of(tgt))
			}
		}
	}

	// Otherwise, very occasionally, a blind punt.
	if v := s.view(p.ID); s.phase() == "matching" && b.BotGambles(v) {
		s.apply(engine.AttemptOwnMatch{Base: engine.For(p.ID), Index: b.Pick(len(v.MyCards))})
		s.io.Printf(" %s takes a punt at rank %s.\n", p.Name, rank)
	}

	// Place anything pending; whatever is left is auto-appended at window close.
	for s.view(p.ID).MyPendingCount > 0 && s.phase() == "matching" {
		pos := b.Pick(len(s.view(p.ID).MyCards) + 1)
		if _, ok := s.apply(engine.InsertPenalty{Base: engine.For(p.ID), TrayIndex: 0, Position: pos}); !ok {
			break
		}
	}
}

func (s *Session) botPower(p Player) {
	b := s.brainFor(p.ID)
	v := s.view(p.ID)
	target := engine.PlayerID("")
	count := 0
	for _, o := range v.Opponents {
		if !o.Removed && o.Count > 0 {
			target = o.ID
			count = o.Count
			break
		}
	}
	switch v.MyPendingPower {
	case engine.Ten:
		s.apply(engine.Peek10{Base: engine.For(p.ID), Index: b.Pick(len(v.MyCards))})
	case engine.Jack:
		if target == "" {
			s.apply(engine.TimerExpired{Kind: engine.TimerPower})
			return
		}
		s.apply(engine.JackPeek{Base: engine.For(p.ID), Target: target, TargetIndex: b.Pick(count)})
	case engine.Queen:
		if target == "" || len(v.MyCards) == 0 || b.Pick(2) == 0 {
			s.apply(engine.SkipPower{Base: engine.For(p.ID)})
			return
		}
		s.apply(engine.QueenSwap{
			Base: engine.For(p.ID), OwnIndex: b.Pick(len(v.MyCards)),
			Target: target, TargetIndex: b.Pick(count),
		})
	case engine.King:
		if target == "" {
			s.apply(engine.TimerExpired{Kind: engine.TimerPower})
			return
		}
		s.apply(engine.KingPeek{Base: engine.For(p.ID), Target: target, TargetIndex: b.Pick(count)})
		if s.phaseIs("king_decision") {
			cur := s.view(p.ID)
			if len(cur.MyCards) > 0 && b.Pick(2) == 0 {
				s.apply(engine.KingSwap{Base: engine.For(p.ID), OwnIndex: b.Pick(len(cur.MyCards))})
			} else {
				s.apply(engine.CancelKingSwap{Base: engine.For(p.ID)})
			}
		}
	}
	s.io.Printf(" %s resolves their power.\n", p.Name)
}
