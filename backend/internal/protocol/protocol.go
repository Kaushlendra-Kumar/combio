// Package protocol defines the versioned WebSocket wire format.
//
// It deliberately exposes only the PlayerView / Event model. Raw engine.State
// has no wire representation here and must never acquire one.
package protocol

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/kaushlendra/combio/backend/internal/engine"
)

// Version is bumped whenever the wire format changes incompatibly.
const Version = 1

// ---------- envelopes ----------

// ClientMessage is anything the browser sends.
//
// Note what is NOT here: a player id. The actor is always taken from the
// authenticated connection, never from the payload (see BuildAction).
type ClientMessage struct {
	V    int             `json:"v"`
	Type string          `json:"type"`
	ID   string          `json:"id,omitempty"` // client correlation id, echoed back
	Data json.RawMessage `json:"data,omitempty"`
}

// ServerMessage is anything the server sends.
type ServerMessage struct {
	V    int    `json:"v"`
	Type string `json:"type"`
	ID   string `json:"id,omitempty"`
	Data any    `json:"data,omitempty"`
}

func Msg(typ string, data any) ServerMessage {
	return ServerMessage{V: Version, Type: typ, Data: data}
}

func Reply(id, typ string, data any) ServerMessage {
	return ServerMessage{V: Version, Type: typ, ID: id, Data: data}
}

// Client -> server message types.
const (
	CCreateRoom = "create_room"
	CJoinRoom   = "join_room"
	CLeaveRoom  = "leave_room"
	CReady      = "ready"
	CStartGame  = "start_game"
	CAction     = "action"
	CReconnect  = "reconnect"
	CListRooms  = "list_rooms"
	CPing       = "ping"
)

// Server -> client message types.
const (
	SWelcome           = "welcome"
	SRoomState         = "room_state"
	SPlayerJoined      = "player_joined"
	SPlayerLeft        = "player_left"
	SGameStarted       = "game_started"
	SStateUpdate       = "state_update"
	SEvents            = "events"
	SError             = "error"
	SReconnectRequired = "reconnect_required"
	SGameFinished      = "game_finished"
	SRoomList          = "room_list"
	SPong              = "pong"
)

// ---------- client payloads ----------

type CreateRoomData struct {
	Public         bool `json:"public"`
	CardsPerPlayer int  `json:"cards_per_player"`
	MaxPlayers     int  `json:"max_players"`
}

type JoinRoomData struct {
	Code string `json:"code"`
}

type ReadyData struct {
	Ready bool `json:"ready"`
}

// ActionData is the gameplay envelope. Any "player" field a client puts in here
// is ignored: encoding/json drops unknown fields, and BuildAction only ever
// stamps the authenticated actor.
type ActionData struct {
	Kind string          `json:"kind"`
	Args json.RawMessage `json:"args,omitempty"`
}

// ---------- server payloads ----------

type WelcomeData struct {
	Version  int             `json:"version"`
	PlayerID engine.PlayerID `json:"player_id"`
	Name     string          `json:"name"`
}

type RoomPlayer struct {
	ID        engine.PlayerID `json:"id"`
	Name      string          `json:"name"`
	Ready     bool            `json:"ready"`
	Connected bool            `json:"connected"`
	Removed   bool            `json:"removed"`
	Host      bool            `json:"host"`
}

type RoomStateData struct {
	Code           string       `json:"code"`
	Public         bool         `json:"public"`
	Status         string       `json:"status"`
	CardsPerPlayer int          `json:"cards_per_player"`
	MaxPlayers     int          `json:"max_players"`
	Players        []RoomPlayer `json:"players"`
}

type RoomSummary struct {
	Code           string `json:"code"`
	Players        int    `json:"players"`
	MaxPlayers     int    `json:"max_players"`
	CardsPerPlayer int    `json:"cards_per_player"`
	Status         string `json:"status"`
}

type RoomListData struct {
	Rooms []RoomSummary `json:"rooms"`
}

type PlayerChangeData struct {
	Player engine.PlayerID `json:"player"`
	Name   string          `json:"name"`
}

type GameStartedData struct {
	CardsPerPlayer int `json:"cards_per_player"`
	Inspect        int `json:"inspect"`
}

// StateUpdateData carries the per-player filtered view and nothing else.
type StateUpdateData struct {
	View engine.PlayerView `json:"view"`
}

// WireEvent is one engine event rendered for the wire.
type WireEvent struct {
	Name string          `json:"name"`
	Data json.RawMessage `json:"data,omitempty"`
}

type EventsData struct {
	Events []WireEvent `json:"events"`
}

type GameFinishedData struct {
	View engine.PlayerView `json:"view"`
}

type ErrorData struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type ReconnectRequiredData struct {
	Code   string `json:"code"`
	Reason string `json:"reason"`
}

// EncodeEvents renders already-filtered events for one recipient.
// The caller MUST have run engine.FilterEvents first.
func EncodeEvents(evs []engine.Event) []WireEvent {
	out := make([]WireEvent, 0, len(evs))
	for _, e := range evs {
		raw, err := json.Marshal(e)
		if err != nil {
			raw = json.RawMessage(`{}`)
		}
		out = append(out, WireEvent{Name: e.EventName(), Data: raw})
	}
	return out
}

// ---------- action decoding ----------

var (
	ErrUnknownAction = errors.New("unknown action kind")

	// ErrUnsupportedVersion is returned for a frame whose envelope version this
	// server does not speak.
	ErrUnsupportedVersion = errors.New("unsupported protocol version")

	// ErrMalformed is returned for a frame that is not valid protocol JSON.
	ErrMalformed = errors.New("malformed message")
)

// Action kind strings.
const (
	AInspectMemory        = "inspect_memory"
	ADeclareCombio        = "declare_combio"
	ADrawCard             = "draw_card"
	AKeepDrawn            = "keep_drawn"
	ADiscardDrawn         = "discard_drawn"
	AAttemptOwnMatch      = "attempt_own_match"
	AAttemptOpponentMatch = "attempt_opponent_match"
	AInsertPenalty        = "insert_penalty"
	APeek10               = "peek_10"
	AJackPeek             = "jack_peek"
	AQueenSwap            = "queen_swap"
	AKingPeek             = "king_peek"
	AKingSwap             = "king_swap"
	ACancelKingSwap       = "cancel_king_swap"
	ASkipPower            = "skip_power"
)

// args is the union of every gameplay argument a client may legitimately send.
//
// There is deliberately no player/actor field. A target IS client input — you
// choose whom to peek at — but who is acting never is.
type args struct {
	Positions     []int           `json:"positions"`
	RemoveIndex   int             `json:"remove_index"`
	InsertIndex   int             `json:"insert_index"`
	Index         int             `json:"index"`
	Version       uint64          `json:"version"`
	Target        engine.PlayerID `json:"target"`
	TargetIndex   int             `json:"target_index"`
	TargetVersion uint64          `json:"target_version"`
	GiveIndex     int             `json:"give_index"`
	GiveVersion   uint64          `json:"give_version"`
	OwnIndex      int             `json:"own_index"`
	TrayIndex     int             `json:"tray_index"`
	Position      int             `json:"position"`
}

// BuildAction turns a wire action into an engine action.
//
// actor comes from the authenticated connection and is the ONLY source of
// engine.Base. This function is the single place in the server where an action
// is constructed, so the actor-authority rule is enforced in exactly one spot.
func BuildAction(kind string, raw json.RawMessage, actor engine.PlayerID) (engine.Action, error) {
	var a args
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &a); err != nil {
			return nil, fmt.Errorf("bad action args: %w", err)
		}
	}
	base := engine.For(actor)

	switch kind {
	case AInspectMemory:
		return engine.InspectMemory{Base: base, Positions: a.Positions}, nil
	case ADeclareCombio:
		return engine.DeclareCombio{Base: base}, nil
	case ADrawCard:
		return engine.DrawCard{Base: base}, nil
	case AKeepDrawn:
		return engine.KeepDrawn{Base: base, RemoveIndex: a.RemoveIndex, InsertIndex: a.InsertIndex}, nil
	case ADiscardDrawn:
		return engine.DiscardDrawn{Base: base}, nil
	case AAttemptOwnMatch:
		return engine.AttemptOwnMatch{Base: base, Index: a.Index, Version: a.Version}, nil
	case AAttemptOpponentMatch:
		return engine.AttemptOpponentMatch{
			Base: base, Target: a.Target, TargetIndex: a.TargetIndex,
			TargetVersion: a.TargetVersion, GiveIndex: a.GiveIndex, GiveVersion: a.GiveVersion,
		}, nil
	case AInsertPenalty:
		return engine.InsertPenalty{Base: base, TrayIndex: a.TrayIndex, Position: a.Position}, nil
	case APeek10:
		return engine.Peek10{Base: base, Index: a.Index}, nil
	case AJackPeek:
		return engine.JackPeek{Base: base, Target: a.Target, TargetIndex: a.TargetIndex}, nil
	case AQueenSwap:
		return engine.QueenSwap{Base: base, OwnIndex: a.OwnIndex, Target: a.Target, TargetIndex: a.TargetIndex}, nil
	case AKingPeek:
		return engine.KingPeek{Base: base, Target: a.Target, TargetIndex: a.TargetIndex}, nil
	case AKingSwap:
		return engine.KingSwap{Base: base, OwnIndex: a.OwnIndex}, nil
	case ACancelKingSwap:
		return engine.CancelKingSwap{Base: base}, nil
	case ASkipPower:
		return engine.SkipPower{Base: base}, nil
	}
	return nil, fmt.Errorf("%w: %q", ErrUnknownAction, kind)
}

// ErrorCode maps an engine error to a stable wire code so clients can react
// without string matching. A lost click race is "stale", which the client
// should treat as "nothing happened", not as a penalty.
func ErrorCode(err error) string {
	switch {
	case err == nil:
		return ""
	case errors.Is(err, engine.ErrStale):
		return "stale"
	case errors.Is(err, engine.ErrNotYourTurn):
		return "not_your_turn"
	case errors.Is(err, engine.ErrWrongPhase):
		return "wrong_phase"
	case errors.Is(err, engine.ErrUnknownPlayer):
		return "unknown_player"
	case errors.Is(err, engine.ErrPlayerInactive):
		return "player_inactive"
	case errors.Is(err, engine.ErrGameOver):
		return "game_over"
	case errors.Is(err, engine.ErrPaused):
		return "paused"
	case errors.Is(err, engine.ErrBadIndex):
		return "bad_index"
	case errors.Is(err, engine.ErrDeclareLocked):
		return "declare_locked"
	case errors.Is(err, engine.ErrAlreadyDeclared):
		return "already_declared"
	case errors.Is(err, engine.ErrAlreadyDrawn):
		return "already_drawn"
	case errors.Is(err, engine.ErrMemoryCount):
		return "memory_count"
	case errors.Is(err, engine.ErrAlreadyInspected):
		return "already_inspected"
	case errors.Is(err, engine.ErrPowerMandatory):
		return "power_mandatory"
	case errors.Is(err, engine.ErrNoCardToGive):
		return "no_card_to_give"
	case errors.Is(err, engine.ErrTargetEmpty):
		return "target_empty"
	case errors.Is(err, engine.ErrSelfTarget):
		return "self_target"
	case errors.Is(err, engine.ErrNoPendingTray):
		return "no_pending_tray"
	case errors.Is(err, engine.ErrImpossibleState):
		return "internal"
	case errors.Is(err, ErrUnknownAction):
		return "unknown_action"
	case errors.Is(err, ErrUnsupportedVersion):
		return "unsupported_version"
	case errors.Is(err, ErrMalformed):
		return "malformed"
	}
	return "rejected"
}

// Error builds an error reply. Engine error text never contains card
// identities, so it is safe to pass through to the player who caused it.
func Error(id string, err error) ServerMessage {
	return Reply(id, SError, ErrorData{Code: ErrorCode(err), Message: err.Error()})
}
