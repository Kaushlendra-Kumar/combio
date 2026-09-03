package protocol

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/kaushlendra/combio/backend/internal/engine"
)

// The wire schema has no actor field, and BuildAction stamps the one it is
// given. This is the single choke point for the authority rule.
func TestBuildActionIgnoresAnythingActorShapedInThePayload(t *testing.T) {
	hostile := json.RawMessage(`{
		"player":"villain","actor":"villain","base":{"Player":"villain"},
		"Base":{"Player":"villain"},"target":"b1","target_index":1,
		"index":2,"own_index":1,"positions":[0,1],"tray_index":0
	}`)
	kinds := []string{
		AInspectMemory, ADeclareCombio, ADrawCard, AKeepDrawn, ADiscardDrawn,
		AAttemptOwnMatch, AAttemptOpponentMatch, AInsertPenalty, APeek10,
		AJackPeek, AQueenSwap, AKingPeek, AKingSwap, ACancelKingSwap, ASkipPower,
	}
	for _, k := range kinds {
		a, err := BuildAction(k, hostile, "hero")
		if err != nil {
			t.Fatalf("%s: %v", k, err)
		}
		if a.Actor() != "hero" {
			t.Fatalf("%s: actor became %q", k, a.Actor())
		}
		if a.Name() == "" {
			t.Fatalf("%s: action has no name", k)
		}
	}
	if len(kinds) != 15 {
		t.Fatalf("the action table has %d kinds; keep this list in step", len(kinds))
	}
	if _, err := BuildAction("nope", nil, "hero"); err == nil {
		t.Fatal("an unknown kind should be refused")
	}
	if _, err := BuildAction(ADrawCard, json.RawMessage(`{bad`), "hero"); err == nil {
		t.Fatal("malformed args should be refused")
	}
	// A target IS legitimate client input; only the actor is not.
	a, _ := BuildAction(AJackPeek, hostile, "hero")
	if a.(engine.JackPeek).Target != "b1" {
		t.Fatal("the client's chosen target should survive")
	}
}

func TestErrorCodesAreStable(t *testing.T) {
	cases := map[error]string{
		engine.ErrStale:           "stale",
		engine.ErrNotYourTurn:     "not_your_turn",
		engine.ErrWrongPhase:      "wrong_phase",
		engine.ErrPaused:          "paused",
		engine.ErrGameOver:        "game_over",
		engine.ErrDeclareLocked:   "declare_locked",
		engine.ErrPowerMandatory:  "power_mandatory",
		engine.ErrTargetEmpty:     "target_empty",
		engine.ErrSelfTarget:      "self_target",
		engine.ErrNoCardToGive:    "no_card_to_give",
		engine.ErrNoPendingTray:   "no_pending_tray",
		engine.ErrBadIndex:        "bad_index",
		engine.ErrUnknownPlayer:   "unknown_player",
		engine.ErrPlayerInactive:  "player_inactive",
		engine.ErrAlreadyDrawn:    "already_drawn",
		engine.ErrAlreadyDeclared: "already_declared",
		engine.ErrMemoryCount:     "memory_count",
		ErrUnknownAction:          "unknown_action",
	}
	for err, want := range cases {
		if got := ErrorCode(err); got != want {
			t.Errorf("%v mapped to %q, want %q", err, got, want)
		}
	}
	if ErrorCode(nil) != "" {
		t.Error("nil should map to no code")
	}
	if got := ErrorCode(errOther{}); got != "rejected" {
		t.Errorf("an unrecognised error mapped to %q", got)
	}
}

type errOther struct{}

func (errOther) Error() string { return "something else" }

// Events go out with lowercase field names and no audience plumbing.
func TestEncodeEventsHidesEngineInternals(t *testing.T) {
	evs := []engine.Event{
		engine.EvCardDiscarded{Player: "a1", Card: engine.Card{ID: 3, Rank: engine.Eight}, ActiveRank: engine.Eight},
		engine.EvPrivateReveal{Owner: "b1", Position: 1, Card: engine.Card{ID: 9, Rank: engine.King}},
		engine.EvMatchAccepted{Player: "a1", Owner: "b1", Card: engine.Card{ID: 3}},
	}
	out := EncodeEvents(evs)
	if len(out) != 3 {
		t.Fatalf("encoded %d events", len(out))
	}
	blob, err := json.Marshal(out)
	if err != nil {
		t.Fatal(err)
	}
	body := string(blob)
	// The unexported pub/priv embeds must not surface.
	for _, leak := range []string{`"To"`, `"Audience"`, `"pub"`, `"priv"`} {
		if strings.Contains(body, leak) {
			t.Fatalf("wire events expose engine plumbing (%s): %s", leak, body)
		}
	}
	// Go field names must not surface either.
	for _, leak := range []string{`"Player"`, `"Card"`, `"ActiveRank"`, `"Owner"`} {
		if strings.Contains(body, leak) {
			t.Fatalf("wire events use Go field names (%s): %s", leak, body)
		}
	}
	for _, want := range []string{`"name":"card_discarded"`, `"player":"a1"`, `"active_rank":8`} {
		if !strings.Contains(body, want) {
			t.Fatalf("wire events missing %s: %s", want, body)
		}
	}
}

func TestMessageHelpers(t *testing.T) {
	m := Msg(SPong, nil)
	if m.V != Version || m.Type != SPong || m.ID != "" {
		t.Fatalf("bad message %+v", m)
	}
	r := Reply("c7", SRoomList, RoomListData{})
	if r.ID != "c7" {
		t.Fatal("replies must echo the correlation id")
	}
	e := Error("c7", engine.ErrStale)
	d, ok := e.Data.(ErrorData)
	if !ok || d.Code != "stale" || e.ID != "c7" {
		t.Fatalf("bad error message %+v", e)
	}
	if strings.Contains(d.Message, "card") && strings.Contains(d.Message, "#") {
		t.Fatal("error text should never carry a card identity")
	}
}

// Every exported field on every event must carry a json tag, or Go field names
// end up on the wire. This catches the next one somebody forgets.
func TestEveryEventFieldIsTagged(t *testing.T) {
	events := []engine.Event{
		engine.EvGameStarted{}, engine.EvMemoryPhaseStarted{}, engine.EvMemoryRevealed{},
		engine.EvMemoryPhaseEnded{}, engine.EvTurnStarted{}, engine.EvDeclareUnlocked{},
		engine.EvCardDrawn{}, engine.EvPrivateDraw{}, engine.EvCardKept{},
		engine.EvCardDiscarded{}, engine.EvMatchingWindowOpened{},
		engine.EvMatchingWindowExtended{}, engine.EvMatchingWindowClosed{},
		engine.EvMatchAccepted{}, engine.EvMatchFailed{}, engine.EvPenaltyApplied{},
		engine.EvPenaltyPlaced{}, engine.EvCardTransferred{}, engine.EvPowerAvailable{},
		engine.EvPrivateReveal{}, engine.EvPowerSkipped{}, engine.EvCardsSwapped{},
		engine.EvCombioDeclared{}, engine.EvGameFinished{}, engine.EvPlayerDisconnected{},
		engine.EvPlayerReconnected{}, engine.EvGamePaused{}, engine.EvGameResumed{},
		engine.EvPlayerRemoved{}, engine.EvDeckReshuffled{}, engine.EvTurnTimedOut{},
	}
	for _, e := range events {
		tp := reflect.TypeOf(e)
		for i := 0; i < tp.NumField(); i++ {
			f := tp.Field(i)
			tag, ok := f.Tag.Lookup("json")
			if !ok {
				t.Errorf("%s.%s has no json tag; its Go name would go on the wire",
					tp.Name(), f.Name)
				continue
			}
			if f.IsExported() && tag == "-" {
				t.Errorf("%s.%s is exported but tagged out", tp.Name(), f.Name)
			}
			if !f.IsExported() && tag != "-" {
				t.Errorf("%s embeds %s without json:\"-\"; audience plumbing would leak",
					tp.Name(), f.Name)
			}
		}
		// And the encoded form must never contain a capitalised key.
		raw, err := json.Marshal(e)
		if err != nil {
			t.Fatal(err)
		}
		for _, k := range []string{`"Player"`, `"Card"`, `"To"`, `"Rank"`, `"Owner"`, `"Count"`} {
			if strings.Contains(string(raw), k) {
				t.Errorf("%s encodes a Go field name %s: %s", tp.Name(), k, raw)
			}
		}
	}
}
