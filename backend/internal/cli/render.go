package cli

import (
	"fmt"
	"sort"
	"strings"

	"github.com/kaushlendra/combio/backend/internal/engine"
)

// Everything in this file renders from engine.PlayerView and engine.Event only.
// Raw engine.State is never read here: that is confined to debug.go.

type nameBook map[engine.PlayerID]string

func (n nameBook) of(id engine.PlayerID) string {
	if s, ok := n[id]; ok {
		return s
	}
	if id == "" {
		return "-"
	}
	return string(id)
}

// CardText renders a fully known card: rank, suit and deck colour.
func CardText(c engine.Card) string {
	return fmt.Sprintf("%s%s(%s)", c.Rank, c.Suit, c.Deck)
}

// handles renders face-down cards as position + deck colour, never identity.
func handles(hs []engine.CardHandle) string {
	if len(hs) == 0 {
		return "(no cards)"
	}
	parts := make([]string, 0, len(hs))
	for _, h := range hs {
		parts = append(parts, fmt.Sprintf("%d:[%s]", h.Position, h.Deck))
	}
	return strings.Join(parts, " ")
}

const rule = "──────────────────────────────────────────────────────────────"

// Board renders the table as this player is allowed to see it. Set private when
// the screen belongs to that player alone (it adds their own positions and any
// private state such as a drawn card).
func Board(v engine.PlayerView, names nameBook, private bool) string {
	var b strings.Builder

	top := "-"
	if v.DiscardTop != nil {
		top = CardText(*v.DiscardTop)
	}
	fmt.Fprintf(&b, "%s\n", rule)
	fmt.Fprintf(&b, " COMBIO   phase: %-13s draw pile: %-3d discard: %d\n", v.Phase, v.DeckCount, v.DiscardCount)
	fmt.Fprintf(&b, " middle card: %-12s", top)
	if v.ActiveRank != engine.RankNone {
		fmt.Fprintf(&b, "  ACTIVE MATCHING RANK: %s", v.ActiveRank)
	}
	b.WriteString("\n")
	if v.Declared {
		fmt.Fprintf(&b, " COMBIO declared by %s\n", names.of(v.Declarer))
	}
	if v.Paused {
		b.WriteString(" ** PAUSED — waiting for a reconnect **\n")
	}
	fmt.Fprintf(&b, "%s\n", rule)

	type row struct {
		seat int
		line string
	}
	rows := []row{}
	for _, o := range v.Opponents {
		marker := "  "
		if o.ID == v.TurnPlayer {
			marker = "> "
		}
		status := ""
		if o.Removed {
			status = "  [REMOVED]"
		} else if !o.Connected {
			status = "  [DISCONNECTED]"
		}
		if o.PendingPenalty > 0 {
			status += fmt.Sprintf("  [%d penalty card(s) pending]", o.PendingPenalty)
		}
		rows = append(rows, row{o.Seat, fmt.Sprintf("%s%-12s %2d cards  %s%s",
			marker, names.of(o.ID), o.Count, handles(o.Cards), status)})
	}
	marker := "  "
	if v.MyTurn {
		marker = "> "
	}
	me := fmt.Sprintf("%s%-12s %2d cards  %s", marker, names.of(v.Me)+" (you)", len(v.MyCards), handles(v.MyCards))
	if v.MyPendingCount > 0 {
		me += fmt.Sprintf("  [%d penalty card(s) pending]", v.MyPendingCount)
	}
	rows = append(rows, row{seatOf(v), me})
	sort.Slice(rows, func(i, j int) bool { return rows[i].seat < rows[j].seat })
	for _, r := range rows {
		b.WriteString(r.line + "\n")
	}
	fmt.Fprintf(&b, "%s\n", rule)

	if private {
		if v.MyDrawnCard != nil {
			fmt.Fprintf(&b, " YOU DREW: %s   (only you can see this)\n", CardText(*v.MyDrawnCard))
		}
		if v.MyPendingPower != engine.RankNone {
			fmt.Fprintf(&b, " YOUR POWER TO RESOLVE: %s\n", powerName(v.MyPendingPower))
		}
		if v.DeclareAvailable {
			b.WriteString(" COMBIO is available to you this turn.\n")
		} else if v.MyTurn && !v.Declared {
			b.WriteString(" COMBIO is not unlocked yet (two complete rounds needed).\n")
		}
	}
	return b.String()
}

// seatOf works out my own seat from the seats I can see.
func seatOf(v engine.PlayerView) int {
	taken := map[int]bool{}
	for _, o := range v.Opponents {
		taken[o.Seat] = true
	}
	for i := 0; i <= len(v.Opponents); i++ {
		if !taken[i] {
			return i
		}
	}
	return len(v.Opponents)
}

func powerName(r engine.Rank) string {
	switch r {
	case engine.Ten:
		return "10 — peek at one of your own cards"
	case engine.Jack:
		return "Jack — look at one opponent card"
	case engine.Queen:
		return "Queen — blind swap (optional)"
	case engine.King:
		return "King — look at an opponent card, then swap or cancel"
	}
	return r.String()
}

// Describe turns one event into a feed line, or "" if it should not be shown.
// Private events are rendered too: the caller is responsible for only ever
// passing events already filtered for the recipient.
func Describe(e engine.Event, names nameBook) string {
	switch ev := e.(type) {
	case engine.EvGameStarted:
		return fmt.Sprintf("game started: %d players, %d cards each, %d deck(s)",
			len(ev.Players), ev.CardsPerPlayer, ev.NumDecks)
	case engine.EvMemoryPhaseStarted:
		return fmt.Sprintf("memory phase: each player inspects %d of their own cards", ev.Inspect)
	case engine.EvMemoryPhaseEnded:
		return "memory phase over — remember them"
	case engine.EvTurnStarted:
		return fmt.Sprintf("turn: %s", names.of(ev.Player))
	case engine.EvDeclareUnlocked:
		return "two complete rounds played — COMBIO is now unlocked"
	case engine.EvCardDrawn:
		return fmt.Sprintf("%s drew a card", names.of(ev.Player))
	case engine.EvCardKept:
		if ev.Removed == nil {
			return fmt.Sprintf("%s kept the drawn card (no card removed)", names.of(ev.Player))
		}
		return fmt.Sprintf("%s kept the drawn card, discarding %s and inserting at position %d",
			names.of(ev.Player), CardText(*ev.Removed), ev.InsertIndex)
	case engine.EvCardDiscarded:
		return fmt.Sprintf("%s discarded %s — active rank is now %s",
			names.of(ev.Player), CardText(ev.Card), ev.ActiveRank)
	case engine.EvMatchingWindowOpened:
		return fmt.Sprintf("matching window open on rank %s", ev.ActiveRank)
	case engine.EvMatchingWindowExtended:
		return fmt.Sprintf("window extended by %ds", ev.Seconds)
	case engine.EvMatchingWindowClosed:
		return "matching window closed"
	case engine.EvMatchAccepted:
		if ev.Player == ev.Owner {
			return fmt.Sprintf("%s matched %s from their own hand", names.of(ev.Player), CardText(ev.Card))
		}
		return fmt.Sprintf("%s knocked %s out of %s's hand",
			names.of(ev.Player), CardText(ev.Card), names.of(ev.Owner))
	case engine.EvMatchFailed:
		if ev.Thrown != nil {
			return fmt.Sprintf("%s threw %s — wrong rank", names.of(ev.Player), CardText(*ev.Thrown))
		}
		return fmt.Sprintf("%s guessed wrong about %s's card", names.of(ev.Player), names.of(ev.Target))
	case engine.EvPenaltyApplied:
		return fmt.Sprintf("%s takes %d penalty card(s), face down", names.of(ev.Player), ev.Count)
	case engine.EvPenaltyPlaced:
		if ev.Auto {
			return fmt.Sprintf("%s's unplaced penalty card went to position %d", names.of(ev.Player), ev.Position)
		}
		return fmt.Sprintf("%s slid a penalty card into position %d", names.of(ev.Player), ev.Position)
	case engine.EvCardTransferred:
		return fmt.Sprintf("%s handed a card to %s at position %d",
			names.of(ev.From), names.of(ev.To), ev.Position)
	case engine.EvCardsSwapped:
		return fmt.Sprintf("%s used the %s to swap their position %d with %s's position %d",
			names.of(ev.Player), ev.Rank, ev.OwnIndex, names.of(ev.Target), ev.TargetIndex)
	case engine.EvPowerSkipped:
		return fmt.Sprintf("%s's %s power was not used (%s)", names.of(ev.Player), ev.Rank, ev.Reason)
	case engine.EvCombioDeclared:
		return fmt.Sprintf("*** %s DECLARES COMBIO ***", names.of(ev.Player))
	case engine.EvDeckReshuffled:
		return fmt.Sprintf("draw deck exhausted — discard pile reshuffled (%d cards)", ev.DeckSize)
	case engine.EvTurnTimedOut:
		return fmt.Sprintf("%s ran out of time — forced draw and discard", names.of(ev.Player))
	case engine.EvPlayerDisconnected:
		return fmt.Sprintf("%s disconnected — game paused", names.of(ev.Player))
	case engine.EvPlayerReconnected:
		return fmt.Sprintf("%s reconnected", names.of(ev.Player))
	case engine.EvGamePaused:
		return "game paused"
	case engine.EvGameResumed:
		return "game resumed"
	case engine.EvPlayerRemoved:
		return fmt.Sprintf("%s was removed — %d card(s) sank to the bottom of the discard pile",
			names.of(ev.Player), ev.CardsToBottom)
	case engine.EvGameFinished:
		return "game over"

	// private events (already filtered for the recipient by the caller)
	case engine.EvPrivateDraw:
		return fmt.Sprintf("you drew %s", CardText(ev.Card))
	case engine.EvMemoryRevealed:
		parts := make([]string, 0, len(ev.Cards))
		for i, c := range ev.Cards {
			parts = append(parts, fmt.Sprintf("position %d = %s", ev.Positions[i], CardText(c)))
		}
		return "you memorise: " + strings.Join(parts, ", ")
	case engine.EvPrivateReveal:
		return fmt.Sprintf("you see %s's position %d: %s", names.of(ev.Owner), ev.Position, CardText(ev.Card))
	case engine.EvPowerAvailable:
		return fmt.Sprintf("your %s power is ready", ev.Rank)
	}
	return ""
}

// Results renders the final screen from the finished view.
func Results(v engine.PlayerView, names nameBook) string {
	var b strings.Builder
	b.WriteString("\n" + rule + "\n GAME OVER\n" + rule + "\n")

	type line struct {
		id    engine.PlayerID
		score int
	}
	rows := make([]line, 0, len(v.Scores))
	for id, sc := range v.Scores {
		rows = append(rows, line{id, sc})
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].score != rows[j].score {
			return rows[i].score < rows[j].score
		}
		return rows[i].id < rows[j].id
	})
	winner := map[engine.PlayerID]bool{}
	for _, w := range v.Winners {
		winner[w] = true
	}
	for _, r := range rows {
		mark := "  "
		if winner[r.id] {
			mark = "* "
		}
		cards := make([]string, 0)
		for _, c := range v.Reveal[r.id] {
			cards = append(cards, CardText(c))
		}
		note := ""
		if r.id == v.Declarer {
			note = "  (declared)"
		}
		fmt.Fprintf(&b, "%s%-12s %4d   %s%s\n", mark, names.of(r.id), r.score, strings.Join(cards, " "), note)
	}
	b.WriteString(rule + "\n")
	switch {
	case len(v.Winners) == 0:
		b.WriteString(" no winner\n")
	case len(v.Winners) == 1:
		fmt.Fprintf(&b, " WINNER: %s\n", names.of(v.Winners[0]))
		if v.Declared && v.Winners[0] != v.Declarer {
			fmt.Fprintf(&b, " %s's declaration failed.\n", names.of(v.Declarer))
		}
	default:
		ws := make([]string, 0, len(v.Winners))
		for _, w := range v.Winners {
			ws = append(ws, names.of(w))
		}
		fmt.Fprintf(&b, " CO-WINNERS: %s\n", strings.Join(ws, ", "))
		if v.Declared {
			fmt.Fprintf(&b, " %s's declaration failed.\n", names.of(v.Declarer))
		}
	}
	b.WriteString(rule + "\n")
	return b.String()
}
