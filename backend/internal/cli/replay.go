package cli

import (
	"bytes"
	"fmt"
	"math/rand"

	"github.com/kaushlendra/combio/backend/internal/engine"
)

// Replay runs a complete bot-only game with no input at all and returns the
// transcript. Same seed, same game, every time — this is the deterministic
// simulation mode.
func Replay(seed int64, players, cards int, debug bool) (string, error) {
	s, buf, err := newReplaySession(seed, players, cards, debug)
	if err != nil {
		return "", err
	}
	if err := s.runHeadless(); err != nil {
		return buf.String(), err
	}
	v := s.anyView()
	for _, l := range s.feed {
		fmt.Fprintf(buf, "  · %s\n", l)
	}
	buf.WriteString(Results(v, s.names))
	if debug {
		buf.WriteString(DebugDump(&s.st))
	}
	return buf.String(), nil
}

func newReplaySession(seed int64, players, cards int, debug bool) (*Session, *bytes.Buffer, error) {
	if players < 2 || players > 8 {
		return nil, nil, fmt.Errorf("players must be 2-8")
	}
	if cards != 4 && cards != 6 && cards != 8 {
		return nil, nil, fmt.Errorf("cards must be 4, 6 or 8")
	}
	buf := &bytes.Buffer{}

	ids := make([]engine.PlayerID, 0, players)
	ps := make([]Player, 0, players)
	names := nameBook{}
	for i := 1; i <= players; i++ {
		id := engine.PlayerID(fmt.Sprintf("p%d", i))
		name := fmt.Sprintf("Bot-%d", i)
		ids = append(ids, id)
		ps = append(ps, Player{ID: id, Name: name, Bot: true})
		names[id] = name
	}

	st, evs, err := engine.NewGame(engine.Config{CardsPerPlayer: cards}, ids, engine.NewSeededShuffler(seed))
	if err != nil {
		return nil, nil, err
	}
	t := &IO{in: nil, out: buf, clear: false, echo: false}
	s := &Session{
		st:      st,
		players: ps,
		names:   names,
		io:      t,
		debug:   debug,
		rng:     rand.New(rand.NewSource(seed)),
	}
	s.record(evs)
	fmt.Fprintf(buf, "COMBIO replay — seed %d, %d players, %d cards each\n\n", seed, players, cards)
	return s, buf, nil
}

// runHeadless plays the whole game with no input at all.
func (s *Session) runHeadless() error {
	for turns := 0; turns < 20000; turns++ {
		v := s.anyView()
		if v.Finished {
			break
		}
		switch v.Phase {
		case "memory":
			for _, p := range s.players {
				pv := s.view(p.ID)
				s.apply(engine.InspectMemory{
					Base: engine.For(p.ID), Positions: s.brainFor(p.ID).BotMemoryPositions(pv),
				})
			}
		case "turn_start":
			s.botTurnStart(s.player(v.TurnPlayer))
		case "draw_decision":
			s.botDrawDecision(s.player(v.TurnPlayer))
		case "matching":
			for _, p := range s.orderFrom(v.TurnPlayer) {
				if s.phase() != "matching" || s.isRemoved(p.ID) {
					continue
				}
				s.botMatch(p, v.ActiveRank)
			}
			if s.phase() == "matching" {
				s.apply(engine.TimerExpired{Kind: engine.TimerMatchWindow})
			}
		case "power", "king_decision":
			s.resolvePower()
		default:
			return fmt.Errorf("unexpected phase %s", v.Phase)
		}
		if err := engine.CheckInvariants(&s.st); err != nil {
			return fmt.Errorf("invariant broken: %w", err)
		}
	}
	if !s.anyView().Finished {
		return fmt.Errorf("game did not finish")
	}
	return nil
}
