package engine

import (
	"strings"
	"testing"
)

// Phase 2 exported the embedded action Base so packages outside the engine can
// construct actions. These tests pin down what that does and does not allow.
//
// The short version: Base only lets a caller *claim* an actor. Every rule check
// still runs against the real state, so a claim that is not legal right now is
// rejected and nothing changes.

func authState(t *testing.T) State {
	t.Helper()
	s, err := BuildScenario(Scenario{
		Players: []PlayerID{"A", "B", "C"},
		Hands: map[PlayerID][]string{
			"A": {"2C", "3D"}, "B": {"KS", "4H"}, "C": {"9D", "6S"},
		},
		Deck:       []string{"5S", "6C", "7H", "8H"},
		DiscardTop: "TH",
		Unlocked:   true,
	})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// A forged Base cannot act out of turn, out of phase, or as somebody who is not
// in the game.
func TestForgedBaseCannotBypassActorChecks(t *testing.T) {
	s := authState(t)

	// Wrong seat.
	rejects(t, s, DrawCard{Base: For("B")}, ErrNotYourTurn)
	rejects(t, s, DeclareCombio{Base: For("C")}, ErrNotYourTurn)
	// Player who does not exist.
	rejects(t, s, DrawCard{Base: For("ghost")}, ErrUnknownPlayer)
	rejects(t, s, DeclareCombio{Base: For("ghost")}, ErrUnknownPlayer)
	// Wrong phase.
	rejects(t, s, KeepDrawn{Base: For("A"), RemoveIndex: 0, InsertIndex: 0}, ErrWrongPhase)
	rejects(t, s, DiscardDrawn{Base: For("A")}, ErrWrongPhase)
	rejects(t, s, AttemptOwnMatch{Base: For("A"), Index: 0}, ErrWrongPhase)
	rejects(t, s, Peek10{Base: For("A"), Index: 0}, ErrWrongPhase)
	rejects(t, s, InspectMemory{Base: For("A"), Positions: []int{0}}, ErrWrongPhase)

	// A removed player cannot be revived by claiming to be them.
	s, _ = step(t, s, PlayerDisconnected{Base: For("C")})
	s, _ = step(t, s, RemovePlayer{Base: For("C")})
	rejects(t, s, DrawCard{Base: For("C")}, ErrPlayerInactive)

	// Holding a drawn card belongs to the drawer alone.
	s, _ = step(t, s, DrawCard{Base: For("A")})
	rejects(t, s, KeepDrawn{Base: For("B"), RemoveIndex: 0, InsertIndex: 0}, ErrNotYourTurn)
	rejects(t, s, DiscardDrawn{Base: For("B")}, ErrNotYourTurn)
}

// Layout versions are still checked, and a forged one is stale rather than a
// free hit or a penalty.
func TestForgedVersionsAreStaleNotAccepted(t *testing.T) {
	s := authState(t)
	s, _ = step(t, s, DrawCard{Base: For("A")})
	s, _ = step(t, s, DiscardDrawn{Base: For("A")}) // rank 8 window

	// A version that never existed cannot be resolved.
	rejects(t, s, AttemptOwnMatch{Base: For("B"), Index: 0, Version: 999999}, ErrStale)
	rejects(t, s, AttemptOpponentMatch{
		Base: For("B"), Target: "C", TargetIndex: 0, TargetVersion: 424242, GiveIndex: 0,
	}, ErrStale)
	// Out-of-range indices resolve to nothing.
	rejects(t, s, AttemptOwnMatch{Base: For("B"), Index: 99}, ErrStale)
	rejects(t, s, AttemptOwnMatch{Base: For("B"), Index: -1}, ErrStale)

	for _, id := range []PlayerID{"A", "B", "C"} {
		if n := len(s.Player(id).PendingPenalty); n != 0 {
			t.Fatalf("%s was penalised for a stale/forged action (%d cards)", id, n)
		}
	}
}

// Matching permissions: who may be targeted, and what a caller must own.
func TestForgedBaseCannotBypassMatchingRules(t *testing.T) {
	s := authState(t)
	s, _ = step(t, s, DrawCard{Base: For("A")})
	s, _ = step(t, s, DiscardDrawn{Base: For("A")})

	rejects(t, s, AttemptOpponentMatch{Base: For("B"), Target: "B", TargetIndex: 0, GiveIndex: 0}, ErrSelfTarget)
	rejects(t, s, AttemptOpponentMatch{Base: For("B"), Target: "ghost", TargetIndex: 0, GiveIndex: 0}, ErrUnknownPlayer)
	rejects(t, s, InsertPenalty{Base: For("B"), TrayIndex: 0, Position: 0}, ErrNoPendingTray)

	// A zero-card player can neither be targeted nor use opponent targeting.
	s2, err := BuildScenario(Scenario{
		Players:    []PlayerID{"A", "B", "C"},
		Hands:      map[PlayerID][]string{"A": {"2C", "3D"}, "B": {"KS", "4H"}, "C": {}},
		Deck:       []string{"5S", "8H"},
		DiscardTop: "TH",
		Unlocked:   true,
	})
	if err != nil {
		t.Fatal(err)
	}
	s2, _ = step(t, s2, DrawCard{Base: For("A")})
	s2, _ = step(t, s2, DiscardDrawn{Base: For("A")})
	rejects(t, s2, AttemptOpponentMatch{Base: For("B"), Target: "C", TargetIndex: 0, GiveIndex: 0}, ErrTargetEmpty)
	rejects(t, s2, AttemptOpponentMatch{Base: For("C"), Target: "B", TargetIndex: 0, GiveIndex: 0}, ErrNoCardToGive)
}

// Power permissions: only the actor whose discarded drawn card armed the power
// may resolve it, only the right power, and only where the rules allow a skip.
func TestForgedBaseCannotBypassPowerRules(t *testing.T) {
	s, err := BuildScenario(Scenario{
		Players:    []PlayerID{"A", "B", "C"},
		Hands:      map[PlayerID][]string{"A": {"2C", "3D"}, "B": {"KS", "4H"}, "C": {"9D", "6S"}},
		Deck:       []string{"JC"},
		DiscardTop: "2H",
		Unlocked:   true,
	})
	if err != nil {
		t.Fatal(err)
	}
	s, _ = step(t, s, DrawCard{Base: For("A")})
	s, _ = step(t, s, DiscardDrawn{Base: For("A")})
	s, _ = step(t, s, TimerExpired{Kind: TimerMatchWindow})

	// Somebody else claiming the power.
	rejects(t, s, JackPeek{Base: For("B"), Target: "C", TargetIndex: 0}, ErrNotYourTurn)
	rejects(t, s, JackPeek{Base: For("ghost"), Target: "C", TargetIndex: 0}, ErrNotYourTurn)
	// The right actor claiming the wrong power.
	rejects(t, s, Peek10{Base: For("A"), Index: 0}, ErrWrongPhase)
	rejects(t, s, QueenSwap{Base: For("A"), OwnIndex: 0, Target: "B", TargetIndex: 0}, ErrWrongPhase)
	rejects(t, s, KingSwap{Base: For("A"), OwnIndex: 0}, ErrWrongPhase)
	// A mandatory power cannot be waved away.
	rejects(t, s, SkipPower{Base: For("A")}, ErrPowerMandatory)
	// Targets are still validated.
	rejects(t, s, JackPeek{Base: For("A"), Target: "A", TargetIndex: 0}, ErrSelfTarget)
	rejects(t, s, JackPeek{Base: For("A"), Target: "B", TargetIndex: 42}, ErrBadIndex)
}

// A pause and a finished game block every claimed actor equally.
func TestForgedBaseCannotActWhilePausedOrFinished(t *testing.T) {
	s := authState(t)
	paused, _ := step(t, s, PlayerDisconnected{Base: For("B")})
	for _, a := range []Action{
		DrawCard{Base: For("A")},
		DeclareCombio{Base: For("A")},
		TimerExpired{Kind: TimerTurn},
		InspectMemory{Base: For("A"), Positions: []int{0}},
	} {
		rejects(t, paused, a, ErrPaused)
	}

	done, err := BuildScenario(Scenario{
		Players:    []PlayerID{"A", "B"},
		Hands:      map[PlayerID][]string{"A": {"AS"}, "B": {"KC"}},
		Deck:       []string{"2C", "3D"},
		DiscardTop: "9H",
		Unlocked:   true,
	})
	if err != nil {
		t.Fatal(err)
	}
	done, _ = step(t, done, DeclareCombio{Base: For("A")})
	done = plainTurn(t, done, "B")
	if done.Phase != PhaseFinished {
		t.Fatalf("phase = %s", done.Phase)
	}
	rejects(t, done, DrawCard{Base: For("B")}, ErrGameOver)
	rejects(t, done, AttemptOwnMatch{Base: For("A"), Index: 0}, ErrGameOver)
}

// A ghost identity cannot do ANYTHING, in any phase of a real game, and never
// changes the state.
func TestGhostActorIsRejectedInEveryPhase(t *testing.T) {
	ghost := func() []Action {
		return []Action{
			InspectMemory{Base: For("ghost"), Positions: []int{0}},
			DeclareCombio{Base: For("ghost")},
			DrawCard{Base: For("ghost")},
			KeepDrawn{Base: For("ghost"), RemoveIndex: 0, InsertIndex: 0},
			DiscardDrawn{Base: For("ghost")},
			AttemptOwnMatch{Base: For("ghost"), Index: 0},
			AttemptOpponentMatch{Base: For("ghost"), Target: "p1", TargetIndex: 0, GiveIndex: 0},
			InsertPenalty{Base: For("ghost"), TrayIndex: 0, Position: 0},
			Peek10{Base: For("ghost"), Index: 0},
			JackPeek{Base: For("ghost"), Target: "p1", TargetIndex: 0},
			QueenSwap{Base: For("ghost"), OwnIndex: 0, Target: "p1", TargetIndex: 0},
			KingPeek{Base: For("ghost"), Target: "p1", TargetIndex: 0},
			KingSwap{Base: For("ghost"), OwnIndex: 0},
			CancelKingSwap{Base: For("ghost")},
			SkipPower{Base: For("ghost")},
			PlayerDisconnected{Base: For("ghost")},
			PlayerReconnected{Base: For("ghost")},
			RemovePlayer{Base: For("ghost")},
		}
	}

	s, _, err := NewGame(Config{CardsPerPlayer: 4}, ids(3), NewSeededShuffler(5))
	if err != nil {
		t.Fatal(err)
	}
	phasesSeen := map[Phase]bool{}
	probe := func() {
		phasesSeen[s.Phase] = true
		before := s.Debug()
		for _, a := range ghost() {
			ns, evs, err := Apply(s, a)
			if err == nil {
				t.Fatalf("ghost got away with %s in phase %s", a.Name(), s.Phase)
			}
			if evs != nil {
				t.Fatalf("ghost action %s emitted events", a.Name())
			}
			if ns.Debug() != before {
				t.Fatalf("ghost action %s changed the state", a.Name())
			}
		}
	}

	probe()
	for _, p := range ids(3) {
		s, _ = step(t, s, InspectMemory{Base: For(p), Positions: []int{0, 1}})
		probe()
	}
	for turn := 0; turn < 12 && s.Phase != PhaseFinished; turn++ {
		switch s.Phase {
		case PhaseTurnStart:
			s, _ = step(t, s, DrawCard{Base: For(s.CurrentID())})
		case PhaseDrawDecision:
			s, _ = step(t, s, DiscardDrawn{Base: For(s.DrawnBy)})
		case PhaseMatching:
			s, _ = step(t, s, TimerExpired{Kind: TimerMatchWindow})
		case PhasePower, PhaseKingDecision:
			s, _ = step(t, s, TimerExpired{Kind: TimerPower})
		}
		probe()
	}
	for _, want := range []Phase{PhaseMemory, PhaseTurnStart, PhaseDrawDecision, PhaseMatching} {
		if !phasesSeen[want] {
			t.Fatalf("the probe never reached phase %s", want)
		}
	}
}

// This is the boundary the exported Base creates, stated as a test so nobody
// has to rediscover it in Phase 3.
//
// The engine authorises the actor a message CLAIMS to be. It is a pure function
// with no notion of connections, so it cannot tell who actually sent the
// message. Anything in front of it MUST fill Base from the authenticated
// session and never from client-supplied data. On a shared local device that is
// trivially true; over a socket it is the transport's job.
func TestEngineAuthorisesTheClaimedActorSoTransportsMustAuthenticate(t *testing.T) {
	s := authState(t)
	s, _ = step(t, s, DrawCard{Base: For("A")})
	s, _ = step(t, s, DiscardDrawn{Base: For("A")}) // rank-8 window, open to everyone

	// B legitimately throws their own card.
	if _, _, err := Apply(s, AttemptOwnMatch{Base: For("B"), Index: 0}); err != nil {
		t.Fatalf("B should be able to act in the window: %v", err)
	}
	// The same message with C's name on it is equally legal, because during a
	// window C really is allowed to throw a card. The engine cannot detect the
	// forgery; only an authenticated transport can.
	if _, _, err := Apply(s, AttemptOwnMatch{Base: For("C"), Index: 0}); err != nil {
		t.Fatalf("C acting in a window is a legal move: %v", err)
	}
	// What the engine DOES guarantee is that no claimed identity unlocks
	// anything the rules forbid for that identity right now.
	rejects(t, s, DrawCard{Base: For("B")}, ErrWrongPhase)
	rejects(t, s, DeclareCombio{Base: For("C")}, ErrWrongPhase)

	closed, _ := step(t, s, TimerExpired{Kind: TimerMatchWindow})
	rejects(t, closed, DrawCard{Base: For("A")}, ErrNotYourTurn)
	rejects(t, closed, DrawCard{Base: For("C")}, ErrNotYourTurn)
}

// Error text never carries card identities, so an error surfaced on a shared
// screen cannot leak a hidden card.
func TestEngineErrorsCarryNoCardIdentities(t *testing.T) {
	s, err := BuildScenario(Scenario{
		Players:    []PlayerID{"A", "B"},
		Hands:      map[PlayerID][]string{"A": {"KD", "QS"}, "B": {"JH", "AC"}},
		DiscardTop: "9H",
	})
	if err != nil {
		t.Fatal(err)
	}
	s.SetShuffler(NewSeededShuffler(1))
	_, _, drawErr := Apply(s, DrawCard{Base: For("A")}) // impossible-state path
	if drawErr == nil {
		t.Fatal("expected the impossible-state error")
	}
	texts := []string{s.Debug(), drawErr.Error()}
	for _, txt := range texts {
		for _, secret := range []string{"KD", "QS", "JH", "AC"} {
			if strings.Contains(txt, secret) {
				t.Fatalf("diagnostic text leaked %s: %s", secret, txt)
			}
		}
	}
}
