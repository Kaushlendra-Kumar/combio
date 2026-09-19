package engine

// Apply is the entire engine API.
//
// It is pure: the incoming State is cloned, the clone is mutated, and the clone
// is returned. If anything fails — a rule violation, a stale click, or the
// defensive impossible-state check — the ORIGINAL state comes back untouched.
// The engine never panics and never half-applies an action.
//
// Time is not a dependency: the driver owns the clock and feeds discrete
// TimerExpired actions, which is what makes every test exact.
func Apply(s State, a Action) (State, []Event, error) {
	ns := s.clone()
	evs, err := ns.dispatch(a)
	if err != nil {
		return s, nil, err
	}
	if err := CheckInvariants(&ns); err != nil {
		return s, nil, err
	}
	return ns, evs, nil
}

func (s *State) dispatch(a Action) ([]Event, error) {
	if s.Phase == PhaseFinished {
		return nil, ErrGameOver
	}
	// §12: a disconnect freezes the whole game. Only connection actions pass.
	switch a.(type) {
	case PlayerDisconnected, PlayerReconnected, RemovePlayer:
	default:
		if s.Paused {
			return nil, ErrPaused
		}
	}

	switch act := a.(type) {
	case InspectMemory:
		return s.inspectMemory(act)
	case DeclareCombio:
		return s.declare(act)
	case DrawCard:
		return s.draw(act)
	case KeepDrawn:
		return s.keepDrawn(act)
	case DiscardDrawn:
		return s.discardDrawn(act)
	case AttemptOwnMatch:
		return s.ownMatch(act)
	case AttemptOpponentMatch:
		return s.opponentMatch(act)
	case InsertPenalty:
		return s.insertPenalty(act)
	case Peek10:
		return s.peek10(act)
	case JackPeek:
		return s.jackPeek(act)
	case QueenSwap:
		return s.queenSwap(act)
	case KingPeek:
		return s.kingPeek(act)
	case KingSwap:
		return s.kingSwap(act)
	case CancelKingSwap:
		return s.cancelKing(act)
	case SkipPower:
		return s.skipPower(act)
	case TimerExpired:
		return s.timerExpired(act)
	case PlayerDisconnected:
		return s.disconnected(act)
	case PlayerReconnected:
		return s.reconnected(act)
	case RemovePlayer:
		return s.removePlayer(act)
	default:
		return nil, ErrWrongPhase
	}
}

// ---------- guards ----------

func (s *State) requireActive(id PlayerID) (*PlayerState, error) {
	p := s.Player(id)
	if p == nil {
		return nil, ErrUnknownPlayer
	}
	if !p.Active() {
		return nil, ErrPlayerInactive
	}
	return p, nil
}

func (s *State) requireCurrent(id PlayerID) (*PlayerState, error) {
	p, err := s.requireActive(id)
	if err != nil {
		return nil, err
	}
	if s.CurrentID() != id {
		return nil, ErrNotYourTurn
	}
	return p, nil
}

// ---------- memory phase ----------

func (s *State) inspectMemory(a InspectMemory) ([]Event, error) {
	if s.Phase != PhaseMemory {
		return nil, ErrWrongPhase
	}
	p, err := s.requireActive(a.Player)
	if err != nil {
		return nil, err
	}
	if p.MemoryDone {
		return nil, ErrAlreadyInspected
	}
	need := p.Layout.Len() / 2
	if len(a.Positions) != need {
		return nil, ErrMemoryCount
	}
	seen := map[int]bool{}
	cards := make([]Card, 0, need)
	for _, pos := range a.Positions {
		id, ok := p.Layout.At(pos)
		if !ok {
			return nil, ErrBadIndex
		}
		if seen[pos] {
			return nil, ErrMemoryCount
		}
		seen[pos] = true
		cards = append(cards, s.MustCard(id))
	}
	p.MemoryDone = true
	evs := []Event{EvMemoryRevealed{priv: priv{To: p.ID}, Cards: cards, Positions: append([]int(nil), a.Positions...)}}

	done := true
	for _, q := range s.ActivePlayers() {
		if !q.MemoryDone {
			done = false
			break
		}
	}
	if done {
		evs = append(evs, s.endMemoryPhase()...)
	}
	return evs, nil
}

func (s *State) endMemoryPhase() []Event {
	s.Phase = PhaseTurnStart
	for i := range s.Players {
		if s.Players[i].Active() {
			s.TurnIdx = i
			break
		}
	}
	return []Event{EvMemoryPhaseEnded{}, EvTurnStarted{Player: s.CurrentID()}}
}

// ---------- turn ----------

func (s *State) declare(a DeclareCombio) ([]Event, error) {
	if s.Phase != PhaseTurnStart {
		if s.Phase == PhaseDrawDecision {
			return nil, ErrAlreadyDrawn
		}
		return nil, ErrWrongPhase
	}
	p, err := s.requireCurrent(a.Player)
	if err != nil {
		return nil, err
	}
	if s.Declared {
		return nil, ErrAlreadyDeclared
	}
	// §10: availability is timing-only. The real score is never consulted here.
	if !s.DeclareUnlocked() {
		return nil, ErrDeclareLocked
	}

	s.Declared = true
	s.Declarer = p.ID
	p.TurnsCompleted++

	// Declaring ends the hand there and then: every layout is revealed, scored
	// and the winner decided (§10). Nobody gets a turn after a declaration, so
	// a declaration is a bet on the table exactly as it stands.
	evs := []Event{EvCombioDeclared{Player: p.ID}}
	return append(evs, s.finish()...), nil
}

func (s *State) draw(a DrawCard) ([]Event, error) {
	if s.Phase != PhaseTurnStart {
		return nil, ErrWrongPhase
	}
	p, err := s.requireCurrent(a.Player)
	if err != nil {
		return nil, err
	}
	id, reshuffled := s.drawOne()
	if id == NoCard {
		// Defensive only. Legal play always leaves something to reshuffle,
		// because every turn deposits at least one card into the pile.
		return nil, impossible(s, "turn draw required but no card obtainable after reshuffle")
	}
	s.Drawn = id
	s.DrawnBy = p.ID
	s.Phase = PhaseDrawDecision

	var evs []Event
	if reshuffled {
		evs = append(evs, EvDeckReshuffled{DeckSize: len(s.Deck) + 1})
	}
	evs = append(evs,
		EvCardDrawn{Player: p.ID},
		EvPrivateDraw{priv: priv{To: p.ID}, Card: s.MustCard(id)},
	)
	return evs, nil
}

func (s *State) keepDrawn(a KeepDrawn) ([]Event, error) {
	if s.Phase != PhaseDrawDecision {
		return nil, ErrWrongPhase
	}
	if a.Player != s.DrawnBy {
		return nil, ErrNotYourTurn
	}
	p := s.Player(a.Player)
	drawn := s.Drawn

	// §29: a zero-card player keeping the drawn card simply gains their first
	// card. Nothing is removed, so no discard and no matching window.
	if p.Layout.Len() == 0 {
		if err := p.Layout.Insert(0, drawn); err != nil {
			return nil, ErrBadIndex
		}
		s.Drawn = NoCard
		s.DrawnBy = ""
		evs := []Event{EvCardKept{Player: p.ID, InsertIndex: 0}}
		more, err := s.endTurn(true)
		return append(evs, more...), err
	}

	removed, err := p.Layout.RemoveAt(a.RemoveIndex)
	if err != nil {
		return nil, ErrBadIndex
	}
	if err := p.Layout.Insert(a.InsertIndex, drawn); err != nil {
		// Apply discards this clone on error, so the half-done removal above
		// never reaches the caller.
		return nil, ErrBadIndex
	}
	s.Drawn = NoCard
	s.DrawnBy = ""

	card := s.MustCard(removed)
	s.pushDiscardTop(removed)
	s.ActiveRank = card.Rank
	// §4: an Option A removal NEVER activates a power, even for 10/J/Q/K.
	s.PendingPower = RankNone
	s.PowerActor = ""

	evs := []Event{
		EvCardKept{Player: p.ID, Removed: &card, InsertIndex: a.InsertIndex},
		EvCardDiscarded{Player: p.ID, Card: card, ActiveRank: card.Rank, FromDrawn: false},
	}
	return append(evs, s.openWindow()...), nil
}

func (s *State) discardDrawn(a DiscardDrawn) ([]Event, error) {
	if s.Phase != PhaseDrawDecision {
		return nil, ErrWrongPhase
	}
	if a.Player != s.DrawnBy {
		return nil, ErrNotYourTurn
	}
	drawn := s.Drawn
	card := s.MustCard(drawn)
	s.pushDiscardTop(drawn)
	s.ActiveRank = card.Rank
	s.Drawn = NoCard
	s.DrawnBy = ""

	// §8: only a directly discarded DRAWN card arms a power, and it resolves
	// after the matching window, never racing it.
	if card.Rank.IsPower() {
		s.PendingPower = card.Rank
		s.PowerActor = a.Player
	}

	evs := []Event{EvCardDiscarded{Player: a.Player, Card: card, ActiveRank: card.Rank, FromDrawn: true}}
	return append(evs, s.openWindow()...), nil
}

func (s *State) openWindow() []Event {
	s.Phase = PhaseMatching
	s.MatchExtensions = 0
	return []Event{EvMatchingWindowOpened{ActiveRank: s.ActiveRank}}
}

// ---------- matching ----------

func (s *State) ownMatch(a AttemptOwnMatch) ([]Event, error) {
	if s.Phase != PhaseMatching {
		return nil, ErrWrongPhase
	}
	p, err := s.requireActive(a.Player)
	if err != nil {
		return nil, err
	}
	id, ok := p.Layout.Resolve(a.Version, a.Index)
	if !ok || !p.Layout.Contains(id) {
		return nil, ErrStale
	}
	card := s.MustCard(id)
	pos := p.Layout.IndexOf(id)
	if _, err := p.Layout.RemoveAt(pos); err != nil {
		return nil, ErrBadIndex
	}
	// Right or wrong, the thrown card is discarded. It is buried under the
	// active card: matching never chains (§5).
	s.buryDiscard(id)

	if card.Rank == s.ActiveRank {
		s.MatchExtensions++
		return []Event{
			EvMatchAccepted{Player: p.ID, Owner: p.ID, Card: card},
			EvMatchingWindowExtended{Seconds: 2},
		}, nil
	}
	evs := []Event{EvMatchFailed{Player: p.ID, Thrown: &card}}
	return append(evs, s.applyPenalty(p)...), nil
}

func (s *State) opponentMatch(a AttemptOpponentMatch) ([]Event, error) {
	if s.Phase != PhaseMatching {
		return nil, ErrWrongPhase
	}
	if a.Target == a.Player {
		return nil, ErrSelfTarget
	}
	actor, err := s.requireActive(a.Player)
	if err != nil {
		return nil, err
	}
	target := s.Player(a.Target)
	if target == nil {
		return nil, ErrUnknownPlayer
	}
	// A removed player's cards have left play, so a click on one is stale (and
	// therefore free), never a wrong match.
	if !target.Active() {
		return nil, ErrStale
	}
	if !target.Targetable() {
		return nil, ErrTargetEmpty
	}
	// §7: you must have a card to hand over, so a zero-card player cannot use
	// this mechanic at all.
	if actor.Layout.Len() == 0 {
		return nil, ErrNoCardToGive
	}

	targetID, ok := target.Layout.Resolve(a.TargetVersion, a.TargetIndex)
	if !ok || !target.Layout.Contains(targetID) {
		return nil, ErrStale
	}
	giveID, ok := actor.Layout.Resolve(a.GiveVersion, a.GiveIndex)
	if !ok || !actor.Layout.Contains(giveID) {
		return nil, ErrStale
	}

	card := s.MustCard(targetID)
	if card.Rank != s.ActiveRank {
		// §7: their card stays, unrevealed. Only the matcher pays.
		evs := []Event{EvMatchFailed{Player: actor.ID, Target: target.ID}}
		return append(evs, s.applyPenalty(actor)...), nil
	}

	pos, err := target.Layout.RemoveCard(targetID)
	if err != nil {
		return nil, ErrStale
	}
	s.buryDiscard(targetID)
	if _, err := actor.Layout.RemoveCard(giveID); err != nil {
		return nil, ErrStale
	}
	// §3: the transferred card lands in the exact gap the removed card left.
	if err := target.Layout.Insert(pos, giveID); err != nil {
		return nil, ErrBadIndex
	}
	s.MatchExtensions++
	return []Event{
		EvMatchAccepted{Player: actor.ID, Owner: target.ID, Card: card},
		EvCardTransferred{From: actor.ID, To: target.ID, Position: pos},
		EvMatchingWindowExtended{Seconds: 2},
	}, nil
}

// applyPenalty implements §6: +2 face-down cards into the pending tray. They
// are not in the layout yet, so nobody can target them.
func (s *State) applyPenalty(p *PlayerState) []Event {
	got := 0
	var evs []Event
	for i := 0; i < 2; i++ {
		id, reshuffled := s.drawOne()
		if reshuffled {
			evs = append(evs, EvDeckReshuffled{DeckSize: len(s.Deck) + 1})
		}
		if id == NoCard {
			break // legal short-draw: never duplicate a card
		}
		p.PendingPenalty = append(p.PendingPenalty, id)
		got++
	}
	return append(evs, EvPenaltyApplied{Player: p.ID, Count: got})
}

func (s *State) insertPenalty(a InsertPenalty) ([]Event, error) {
	if s.Phase != PhaseMatching {
		return nil, ErrWrongPhase
	}
	p, err := s.requireActive(a.Player)
	if err != nil {
		return nil, err
	}
	if a.TrayIndex < 0 || a.TrayIndex >= len(p.PendingPenalty) {
		return nil, ErrNoPendingTray
	}
	id := p.PendingPenalty[a.TrayIndex]
	rest := append([]CardID(nil), p.PendingPenalty[:a.TrayIndex]...)
	p.PendingPenalty = append(rest, p.PendingPenalty[a.TrayIndex+1:]...)
	if err := p.Layout.Insert(a.Position, id); err != nil {
		return nil, ErrBadIndex
	}
	return []Event{EvPenaltyPlaced{Player: p.ID, Position: a.Position}}, nil
}

func (s *State) closeWindow() ([]Event, error) {
	var evs []Event
	// Anything still in a tray is auto-appended when the window shuts (§6).
	for i := range s.Players {
		p := &s.Players[i]
		for len(p.PendingPenalty) > 0 {
			id := p.PendingPenalty[0]
			p.PendingPenalty = p.PendingPenalty[1:]
			p.Layout.Append(id)
			evs = append(evs, EvPenaltyPlaced{Player: p.ID, Position: p.Layout.Len() - 1, Auto: true})
		}
	}
	s.ActiveRank = RankNone
	s.MatchExtensions = 0
	evs = append(evs, EvMatchingWindowClosed{})

	more, err := s.resolvePower()
	return append(evs, more...), err
}

// ---------- powers ----------

func (s *State) resolvePower() ([]Event, error) {
	if s.PendingPower == RankNone {
		return s.endTurn(true)
	}
	actor := s.Player(s.PowerActor)
	if actor == nil || !actor.Active() {
		s.PendingPower = RankNone
		return s.endTurn(true)
	}
	if reason, fizzles := s.powerFizzles(actor, s.PendingPower); fizzles {
		rank := s.PendingPower
		s.PendingPower = RankNone
		evs := []Event{EvPowerSkipped{Player: actor.ID, Rank: rank, Reason: reason}}
		more, err := s.endTurn(true)
		return append(evs, more...), err
	}
	s.Phase = PhasePower
	return []Event{EvPowerAvailable{priv: priv{To: actor.ID}, Rank: s.PendingPower}}, nil
}

// powerFizzles encodes the zero-card cases in §8.
func (s *State) powerFizzles(actor *PlayerState, rank Rank) (string, bool) {
	hasTarget := false
	for _, q := range s.ActivePlayers() {
		if q.ID != actor.ID && q.Targetable() {
			hasTarget = true
			break
		}
	}
	switch rank {
	case Ten:
		if actor.Layout.Len() == 0 {
			return "no own card to peek", true
		}
	case Jack, King:
		if !hasTarget {
			return "no opponent card to inspect", true
		}
	case Queen:
		if actor.Layout.Len() == 0 {
			return "no own card to swap", true
		}
		if !hasTarget {
			return "no opponent card to swap", true
		}
	}
	return "", false
}

func (s *State) requirePower(id PlayerID, want Rank, phase Phase) (*PlayerState, error) {
	if s.Phase != phase {
		return nil, ErrWrongPhase
	}
	if s.PowerActor != id {
		return nil, ErrNotYourTurn
	}
	if s.PendingPower != want {
		return nil, ErrWrongPhase
	}
	return s.Player(id), nil
}

func (s *State) targetCard(actor PlayerID, target PlayerID, index int) (*PlayerState, CardID, error) {
	if target == actor {
		return nil, NoCard, ErrSelfTarget
	}
	t := s.Player(target)
	if t == nil {
		return nil, NoCard, ErrUnknownPlayer
	}
	if !t.Active() {
		return nil, NoCard, ErrStale
	}
	if !t.Targetable() {
		return nil, NoCard, ErrTargetEmpty
	}
	id, ok := t.Layout.At(index)
	if !ok {
		return nil, NoCard, ErrBadIndex
	}
	return t, id, nil
}

func (s *State) peek10(a Peek10) ([]Event, error) {
	p, err := s.requirePower(a.Player, Ten, PhasePower)
	if err != nil {
		return nil, err
	}
	id, ok := p.Layout.At(a.Index)
	if !ok {
		return nil, ErrBadIndex
	}
	evs := []Event{EvPrivateReveal{priv: priv{To: p.ID}, Owner: p.ID, Position: a.Index, Card: s.MustCard(id)}}
	s.PendingPower = RankNone
	more, err := s.endTurn(true)
	return append(evs, more...), err
}

func (s *State) jackPeek(a JackPeek) ([]Event, error) {
	p, err := s.requirePower(a.Player, Jack, PhasePower)
	if err != nil {
		return nil, err
	}
	t, id, err := s.targetCard(p.ID, a.Target, a.TargetIndex)
	if err != nil {
		return nil, err
	}
	evs := []Event{EvPrivateReveal{priv: priv{To: p.ID}, Owner: t.ID, Position: a.TargetIndex, Card: s.MustCard(id)}}
	s.PendingPower = RankNone
	more, err := s.endTurn(true)
	return append(evs, more...), err
}

func (s *State) queenSwap(a QueenSwap) ([]Event, error) {
	p, err := s.requirePower(a.Player, Queen, PhasePower)
	if err != nil {
		return nil, err
	}
	ownID, ok := p.Layout.At(a.OwnIndex)
	if !ok {
		return nil, ErrBadIndex
	}
	t, targetID, err := s.targetCard(p.ID, a.Target, a.TargetIndex)
	if err != nil {
		return nil, err
	}
	// §3: each card takes the exact position vacated by the other. Blind: no
	// reveal event is emitted to anyone, including the swapper.
	if _, err := p.Layout.Swap(a.OwnIndex, targetID); err != nil {
		return nil, ErrBadIndex
	}
	if _, err := t.Layout.Swap(a.TargetIndex, ownID); err != nil {
		return nil, ErrBadIndex
	}
	evs := []Event{EvCardsSwapped{Player: p.ID, OwnIndex: a.OwnIndex, Target: t.ID, TargetIndex: a.TargetIndex, Rank: Queen}}
	s.PendingPower = RankNone
	more, err := s.endTurn(true)
	return append(evs, more...), err
}

func (s *State) kingPeek(a KingPeek) ([]Event, error) {
	p, err := s.requirePower(a.Player, King, PhasePower)
	if err != nil {
		return nil, err
	}
	t, id, err := s.targetCard(p.ID, a.Target, a.TargetIndex)
	if err != nil {
		return nil, err
	}
	evs := []Event{EvPrivateReveal{priv: priv{To: p.ID}, Owner: t.ID, Position: a.TargetIndex, Card: s.MustCard(id)}}

	// §8: a zero-card King user gets the peek but cannot swap.
	if p.Layout.Len() == 0 {
		s.PendingPower = RankNone
		evs = append(evs, EvPowerSkipped{Player: p.ID, Rank: King, Reason: "no own card to swap"})
		more, err := s.endTurn(true)
		return append(evs, more...), err
	}
	s.KingPeek = &kingPeekState{Target: t.ID, TargetCard: id}
	s.Phase = PhaseKingDecision
	return evs, nil
}

func (s *State) kingSwap(a KingSwap) ([]Event, error) {
	p, err := s.requirePower(a.Player, King, PhaseKingDecision)
	if err != nil {
		return nil, err
	}
	if s.KingPeek == nil {
		return nil, ErrWrongPhase
	}
	t := s.Player(s.KingPeek.Target)
	if t == nil || !t.Active() || !t.Layout.Contains(s.KingPeek.TargetCard) {
		return nil, ErrStale
	}
	ownID, ok := p.Layout.At(a.OwnIndex)
	if !ok {
		return nil, ErrBadIndex
	}
	tPos := t.Layout.IndexOf(s.KingPeek.TargetCard)
	if _, err := p.Layout.Swap(a.OwnIndex, s.KingPeek.TargetCard); err != nil {
		return nil, ErrBadIndex
	}
	if _, err := t.Layout.Swap(tPos, ownID); err != nil {
		return nil, ErrBadIndex
	}
	evs := []Event{EvCardsSwapped{Player: p.ID, OwnIndex: a.OwnIndex, Target: t.ID, TargetIndex: tPos, Rank: King}}
	s.PendingPower = RankNone
	s.KingPeek = nil
	more, err := s.endTurn(true)
	return append(evs, more...), err
}

func (s *State) cancelKing(a CancelKingSwap) ([]Event, error) {
	p, err := s.requirePower(a.Player, King, PhaseKingDecision)
	if err != nil {
		return nil, err
	}
	s.PendingPower = RankNone
	s.KingPeek = nil
	evs := []Event{EvPowerSkipped{Player: p.ID, Rank: King, Reason: "cancelled"}}
	more, err := s.endTurn(true)
	return append(evs, more...), err
}

func (s *State) skipPower(a SkipPower) ([]Event, error) {
	if s.Phase != PhasePower {
		return nil, ErrWrongPhase
	}
	if s.PowerActor != a.Player {
		return nil, ErrNotYourTurn
	}
	// §8: only the Queen is optional. 10, Jack and the King peek are mandatory
	// and can only lapse through the power timer.
	if s.PendingPower != Queen {
		return nil, ErrPowerMandatory
	}
	rank := s.PendingPower
	s.PendingPower = RankNone
	evs := []Event{EvPowerSkipped{Player: a.Player, Rank: rank, Reason: "declined"}}
	more, err := s.endTurn(true)
	return append(evs, more...), err
}

// ---------- turn end ----------

// endTurn closes the current turn. counted is false when the turn was forfeited
// rather than played (the player was removed mid-turn): a forfeit must never
// advance the round counter, or a disconnect could unlock COMBIO early.
func (s *State) endTurn(counted bool) ([]Event, error) {
	if cur := s.Current(); cur != nil && counted {
		cur.TurnsCompleted++
		if !s.Declared {
			s.recordTurn(cur.ID)
		}
	}
	s.Drawn = NoCard
	s.DrawnBy = ""
	s.PendingPower = RankNone
	s.PowerActor = ""
	s.KingPeek = nil
	s.ActiveRank = RankNone

	var evs []Event
	if !s.DeclareUnlockedFlag && s.DeclareUnlocked() {
		s.DeclareUnlockedFlag = true
		evs = append(evs, EvDeclareUnlocked{})
	}
	if s.ActiveCount() <= 1 {
		return append(evs, s.finish()...), nil
	}
	n := len(s.Players)
	for i := 1; i <= n; i++ {
		idx := (s.TurnIdx + i) % n
		if s.Players[idx].Active() {
			s.TurnIdx = idx
			break
		}
	}
	s.Phase = PhaseTurnStart
	return append(evs, EvTurnStarted{Player: s.CurrentID()}), nil
}

// ---------- timers ----------

func (s *State) timerExpired(a TimerExpired) ([]Event, error) {
	switch a.Kind {
	case TimerMemory:
		if s.Phase != PhaseMemory {
			return nil, ErrWrongPhase
		}
		return s.endMemoryPhase(), nil

	case TimerMatchWindow:
		if s.Phase != PhaseMatching {
			return nil, ErrWrongPhase
		}
		return s.closeWindow()

	case TimerPower:
		if s.Phase != PhasePower && s.Phase != PhaseKingDecision {
			return nil, ErrWrongPhase
		}
		rank := s.PendingPower
		s.PendingPower = RankNone
		s.KingPeek = nil
		evs := []Event{EvPowerSkipped{Player: s.PowerActor, Rank: rank, Reason: "timeout"}}
		more, err := s.endTurn(true)
		return append(evs, more...), err

	case TimerTurn:
		// §13: a forced legal move, not bot play. Draw and discard, and skip
		// any power the discard would have armed.
		switch s.Phase {
		case PhaseTurnStart:
			cur := s.CurrentID()
			evs := []Event{EvTurnTimedOut{Player: cur}}
			more, err := s.draw(DrawCard{Base: Base{Player: cur}})
			if err != nil {
				return nil, err
			}
			evs = append(evs, more...)
			more, err = s.forcedDiscard(cur)
			return append(evs, more...), err
		case PhaseDrawDecision:
			cur := s.DrawnBy
			evs := []Event{EvTurnTimedOut{Player: cur}}
			more, err := s.forcedDiscard(cur)
			return append(evs, more...), err
		default:
			return nil, ErrWrongPhase
		}
	}
	return nil, ErrWrongPhase
}

func (s *State) forcedDiscard(p PlayerID) ([]Event, error) {
	evs, err := s.discardDrawn(DiscardDrawn{Base: Base{Player: p}})
	if err != nil {
		return nil, err
	}
	if s.PendingPower != RankNone {
		rank := s.PendingPower
		s.PendingPower = RankNone
		s.PowerActor = ""
		evs = append(evs, EvPowerSkipped{Player: p, Rank: rank, Reason: "turn timeout"})
	}
	return evs, nil
}

// ---------- connection ----------

func (s *State) disconnected(a PlayerDisconnected) ([]Event, error) {
	p, err := s.requireActive(a.Player)
	if err != nil {
		return nil, err
	}
	if !p.Connected {
		return nil, nil
	}
	p.Connected = false
	s.Disconnected[p.ID] = true
	evs := []Event{EvPlayerDisconnected{Player: p.ID}}
	if !s.Paused {
		s.Paused = true
		evs = append(evs, EvGamePaused{})
	}
	return evs, nil
}

func (s *State) reconnected(a PlayerReconnected) ([]Event, error) {
	p, err := s.requireActive(a.Player)
	if err != nil {
		return nil, err
	}
	p.Connected = true
	delete(s.Disconnected, p.ID)
	evs := []Event{EvPlayerReconnected{Player: p.ID}}
	if len(s.Disconnected) == 0 && s.Paused {
		s.Paused = false
		evs = append(evs, EvGameResumed{})
	}
	return evs, nil
}

// removePlayer is the 30-second lapse (§12). The player leaves the rotation and
// every card they hold sinks FACE-DOWN TO THE BOTTOM of the discard pile: never
// revealed, no matching window, no power — but still physically in the game and
// returned by the next reshuffle. No card is ever destroyed.
func (s *State) removePlayer(a RemovePlayer) ([]Event, error) {
	p, err := s.requireActive(a.Player)
	if err != nil {
		return nil, err
	}
	wasCurrent := s.CurrentID() == p.ID
	wasDrawing := s.DrawnBy == p.ID
	wasPowerActor := s.PowerActor == p.ID
	powerTargetedThem := s.KingPeek != nil && s.KingPeek.Target == p.ID

	// Deterministic sink order, bottom-first:
	//   layout cards in current layout order
	//   -> the privately drawn card, if they were holding one
	//   -> pending penalty tray cards in tray order
	// Nothing is revealed and nothing is destroyed; physical identity is kept.
	sunk := append([]CardID(nil), p.Layout.Cards()...)
	if wasDrawing && s.Drawn != NoCard {
		sunk = append(sunk, s.Drawn)
		s.Drawn = NoCard
		s.DrawnBy = ""
	}
	sunk = append(sunk, p.PendingPenalty...)
	for p.Layout.Len() > 0 {
		if _, err := p.Layout.RemoveAt(0); err != nil {
			return nil, impossible(s, "failed to clear removed player's layout")
		}
	}
	p.PendingPenalty = nil
	s.sinkToBottom(sunk)

	owed := s.owesTurn(p.ID)
	p.Removed = true
	p.Connected = false
	delete(s.Disconnected, p.ID)

	// §10: a player who leaves owing a turn breaks the round in progress. It is
	// discarded rather than completed, and a fresh round starts with whoever is
	// left, so removals can never retroactively finish a round.
	// They can never owe a turn again, so drop them from the pending set...
	pending := s.RoundPending[:0]
	for _, id := range s.RoundPending {
		if id != p.ID {
			pending = append(pending, id)
		}
	}
	s.RoundPending = pending
	// ...but if they left still owing one, the round is broken rather than
	// finished: discard it and start a fresh one with the remaining players.
	if !s.Declared && owed {
		s.startRound()
	}

	evs := []Event{EvPlayerRemoved{Player: p.ID, CardsToBottom: len(sunk)}}
	if len(s.Disconnected) == 0 && s.Paused {
		s.Paused = false
		evs = append(evs, EvGameResumed{})
	}

	if s.ActiveCount() <= 1 {
		s.ActiveRank = RankNone
		s.PendingPower = RankNone
		s.KingPeek = nil
		return append(evs, s.finish()...), nil
	}

	// An unresolved action that involved them is cancelled without penalty.
	// The acting player keeps credit for a turn they actually played; a player
	// removed mid-turn forfeits theirs.
	counted := s.CurrentID() != p.ID
	if powerTargetedThem || (wasPowerActor && (s.Phase == PhasePower || s.Phase == PhaseKingDecision)) {
		rank := s.PendingPower
		s.PendingPower = RankNone
		s.KingPeek = nil
		evs = append(evs, EvPowerSkipped{Player: s.PowerActor, Rank: rank, Reason: "target removed"})
		more, err := s.endTurn(counted)
		return append(evs, more...), err
	}
	if wasCurrent && (s.Phase == PhaseTurnStart || s.Phase == PhaseDrawDecision) {
		s.Phase = PhaseTurnStart
		more, err := s.endTurn(false)
		return append(evs, more...), err
	}
	return evs, nil
}
