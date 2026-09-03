package engine

// ScoreEntry is one player's final total. A slice of these is used instead of a
// map so winner determination is deterministic regardless of map iteration.
type ScoreEntry struct {
	Player PlayerID
	Score  int
}

// DetermineWinners implements §11.
//
//	declarer strictly lower than every other player -> declarer alone wins
//	otherwise                                       -> declaration fails, the
//	                                                   declarer is out of
//	                                                   contention entirely, and
//	                                                   the lowest score among
//	                                                   the rest wins, with
//	                                                   co-winners on a tie
//
// declarerEligible is false when the declarer was removed by a disconnect: the
// declaration still stands and still ends the game, but they cannot win (§12).
func DetermineWinners(order []ScoreEntry, declared bool, declarer PlayerID, declarerEligible bool) (winners []PlayerID, failed bool) {
	if len(order) == 0 {
		return nil, false
	}
	if len(order) == 1 {
		return []PlayerID{order[0].Player}, false
	}

	lowestOf := func(entries []ScoreEntry) []PlayerID {
		best := entries[0].Score
		for _, e := range entries[1:] {
			if e.Score < best {
				best = e.Score
			}
		}
		var out []PlayerID
		for _, e := range entries {
			if e.Score == best {
				out = append(out, e.Player)
			}
		}
		return out
	}

	if !declared {
		return lowestOf(order), false
	}

	others := make([]ScoreEntry, 0, len(order))
	var declarerScore int
	foundDeclarer := false
	for _, e := range order {
		if e.Player == declarer {
			declarerScore = e.Score
			foundDeclarer = true
			continue
		}
		others = append(others, e)
	}
	if len(others) == 0 {
		return []PlayerID{declarer}, false
	}
	if !foundDeclarer || !declarerEligible {
		// Declarer is gone: declaration stands, they cannot win.
		return lowestOf(others), true
	}

	othersMin := others[0].Score
	for _, e := range others[1:] {
		if e.Score < othersMin {
			othersMin = e.Score
		}
	}
	if declarerScore < othersMin {
		return []PlayerID{declarer}, false
	}
	return lowestOf(others), true
}

// finish reveals every remaining layout, scores it and decides the winner.
func (s *State) finish() []Event {
	s.Phase = PhaseFinished
	s.ActiveRank = RankNone
	s.Drawn = NoCard
	s.PendingPower = RankNone
	s.KingPeek = nil

	// Any card still sitting in a pending tray belongs to that player and
	// counts toward their score (§6), so place it before revealing.
	for i := range s.Players {
		p := &s.Players[i]
		for len(p.PendingPenalty) > 0 {
			id := p.PendingPenalty[0]
			p.PendingPenalty = p.PendingPenalty[1:]
			p.Layout.Append(id)
		}
	}

	order := make([]ScoreEntry, 0, len(s.Players))
	scores := make(map[PlayerID]int, len(s.Players))
	reveal := make(map[PlayerID][]Card, len(s.Players))
	for i := range s.Players {
		p := &s.Players[i]
		if !p.Active() {
			continue
		}
		sum := 0
		cards := make([]Card, 0, p.Layout.Len())
		for _, id := range p.Layout.Cards() {
			c := s.MustCard(id)
			sum += c.Value()
			cards = append(cards, c)
		}
		order = append(order, ScoreEntry{Player: p.ID, Score: sum})
		scores[p.ID] = sum
		reveal[p.ID] = cards
	}

	declarerEligible := false
	if s.Declared {
		if d := s.Player(s.Declarer); d != nil && d.Active() {
			declarerEligible = true
		}
	}
	winners, failed := DetermineWinners(order, s.Declared, s.Declarer, declarerEligible)
	s.Scores = scores
	s.Winners = winners

	return []Event{EvGameFinished{
		Scores:            scores,
		Winners:           append([]PlayerID(nil), winners...),
		Declarer:          s.Declarer,
		DeclarationFailed: failed,
		Reveal:            reveal,
	}}
}

// Score returns a player's current total. Cards in the pending penalty tray are
// not in the layout yet and are not scored until placed.
func (s *State) Score(id PlayerID) int {
	p := s.Player(id)
	if p == nil {
		return 0
	}
	sum := 0
	for _, cid := range p.Layout.Cards() {
		sum += s.MustCard(cid).Value()
	}
	return sum
}
