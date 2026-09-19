// Package server wires HTTP and WebSocket transport to the room manager.
//
// This is where the actor-authority boundary lives: a session's PlayerID comes
// from the authenticated token at connect time and is the only identity ever
// handed to the room. Nothing a client puts in a message body can change it.
package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"
	"sync"

	"github.com/kaushlendra/combio/backend/internal/auth"
	"github.com/kaushlendra/combio/backend/internal/engine"
	"github.com/kaushlendra/combio/backend/internal/protocol"
	"github.com/kaushlendra/combio/backend/internal/room"
	"github.com/kaushlendra/combio/backend/internal/ws"
)

// Options configures a server.
type Options struct {
	Auth   auth.Authenticator
	Dev    *auth.DevTokens // when set, exposes POST /auth/dev
	Rooms  *room.Manager
	Logger *log.Logger

	// AllowedOrigins lists the browser origins permitted to call the HTTP
	// endpoints. Empty means "any", which is what local development needs: the
	// frontend runs on :3000 and the server on :8080, so every request from the
	// page is cross-origin and would otherwise be unreadable to it.
	//
	// This is a development default. Locking it to the deployed origin belongs
	// to the hardening phase, alongside the WebSocket upgrader's origin check.
	AllowedOrigins []string
}

type Server struct {
	opts Options
	mux  *http.ServeMux

	mu       sync.Mutex
	sessions map[*session]struct{}
	closed   bool
}

func New(o Options) *Server {
	if o.Rooms == nil {
		o.Rooms = room.NewManager(room.ManagerOptions{})
	}
	if o.Auth == nil && o.Dev != nil {
		o.Auth = o.Dev
	}
	s := &Server{opts: o, mux: http.NewServeMux(), sessions: map[*session]struct{}{}}
	s.routes()
	return s
}

// Handler is the HTTP entry point, wrapped so browsers can actually read the
// responses.
func (s *Server) Handler() http.Handler { return s.cors(s.mux) }

func (s *Server) originAllowed(origin string) bool {
	if len(s.opts.AllowedOrigins) == 0 {
		return true // development default
	}
	for _, o := range s.opts.AllowedOrigins {
		if o == "*" || strings.EqualFold(o, origin) {
			return true
		}
	}
	return false
}

// cors answers the browser's cross-origin rules.
//
// Without this the page gets a successful response it is not allowed to read,
// which shows up in the console as an opaque "Failed to fetch" rather than as
// anything resembling a CORS problem. No credentials are involved — the token
// travels in the body and then in the WebSocket URL — so no
// Access-Control-Allow-Credentials is set, and nothing here weakens the actor
// authority model: the server still decides who a socket is.
func (s *Server) cors(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if origin := r.Header.Get("Origin"); origin != "" {
			w.Header().Add("Vary", "Origin")
			if s.originAllowed(origin) {
				w.Header().Set("Access-Control-Allow-Origin", origin)
				w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
				w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
				w.Header().Set("Access-Control-Max-Age", "600")
			}
		}
		// A preflight never reaches a route: answer it here and stop.
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) track(sess *session) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return false
	}
	s.sessions[sess] = struct{}{}
	return true
}

func (s *Server) untrack(sess *session) {
	s.mu.Lock()
	delete(s.sessions, sess)
	s.mu.Unlock()
}

func (s *Server) isClosed() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.closed
}

// Shutdown stops accepting connections, closes the ones that are open, and
// shuts every room down. It is safe to call twice and never blocks on a room
// loop, so it cannot deadlock against a room that is mid-broadcast.
func (s *Server) Shutdown(ctx context.Context) error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	open := make([]*session, 0, len(s.sessions))
	for sess := range s.sessions {
		open = append(open, sess)
	}
	s.sessions = map[*session]struct{}{}
	s.mu.Unlock()

	// Closing the socket unblocks each session's read loop, which then returns.
	for _, sess := range open {
		sess.Close()
	}
	// Rooms stop creating and every room goroutine exits.
	s.opts.Rooms.Close()

	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
		return nil
	}
}
func (s *Server) Rooms() *room.Manager { return s.opts.Rooms }

func (s *Server) logf(format string, a ...any) {
	if s.opts.Logger != nil {
		s.opts.Logger.Printf(format, a...)
	}
}

func (s *Server) routes() {
	s.mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "protocol": protocol.Version})
	})
	s.mux.HandleFunc("/auth/dev", s.handleDevAuth)
	s.mux.HandleFunc("/ws", s.handleWS)
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

// handleDevAuth mints a token for a display name. The player id is chosen by
// the server; the request cannot ask for one.
func (s *Server) handleDevAuth(w http.ResponseWriter, r *http.Request) {
	if s.opts.Dev == nil {
		http.Error(w, "dev auth disabled", http.StatusNotFound)
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	name := r.URL.Query().Get("name")
	if name == "" {
		var body struct {
			Name string `json:"name"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		name = body.Name
	}
	token, id, err := s.opts.Dev.Issue(name)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"token": token, "player_id": id.PlayerID, "name": id.Name,
	})
}

func tokenFrom(r *http.Request) string {
	if t := r.URL.Query().Get("token"); t != "" {
		return t
	}
	h := r.Header.Get("Authorization")
	if strings.HasPrefix(strings.ToLower(h), "bearer ") {
		return strings.TrimSpace(h[7:])
	}
	return h
}

func (s *Server) handleWS(w http.ResponseWriter, r *http.Request) {
	if s.isClosed() {
		http.Error(w, "server is shutting down", http.StatusServiceUnavailable)
		return
	}
	if s.opts.Auth == nil {
		http.Error(w, "auth not configured", http.StatusInternalServerError)
		return
	}
	// Authenticate BEFORE upgrading: an unauthenticated socket never exists.
	id, err := s.opts.Auth.Authenticate(tokenFrom(r))
	if err != nil {
		http.Error(w, err.Error(), http.StatusUnauthorized)
		return
	}
	conn, err := ws.Upgrade(w, r)
	if err != nil {
		s.logf("upgrade failed: %v", err)
		return
	}
	sess := &session{srv: s, conn: conn, id: id}
	if !s.track(sess) {
		sess.Close()
		return
	}
	defer s.untrack(sess)
	sess.run()
}

// session is one connected player. It implements room.Client.
type session struct {
	srv  *Server
	conn *ws.Conn
	id   auth.Identity

	mu   sync.Mutex
	room *room.Room
}

func (s *session) ID() engine.PlayerID { return s.id.PlayerID }
func (s *session) Name() string        { return s.id.Name }

func (s *session) Send(m protocol.ServerMessage) { s.conn.Send(m) }
func (s *session) Close()                        { s.conn.Close() }

func (s *session) currentRoom() *room.Room {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.room
}

func (s *session) setRoom(r *room.Room) {
	s.mu.Lock()
	s.room = r
	s.mu.Unlock()
}

func (s *session) run() {
	defer func() {
		if r := s.currentRoom(); r != nil {
			r.Disconnected(s.id.PlayerID)
		}
		s.conn.Close()
	}()

	s.Send(protocol.Msg(protocol.SWelcome, protocol.WelcomeData{
		Version: protocol.Version, PlayerID: s.id.PlayerID, Name: s.id.Name,
	}))

	// A client gets a few malformed frames before we hang up on it. That way a
	// single bad message costs an error reply, not a game.
	const maxBadFrames = 5
	bad := 0

	for {
		m, err := s.conn.Read()
		switch {
		case err == nil:
			// fall through
		case errors.Is(err, ws.ErrBadMessage):
			bad++
			s.fail("", protocol.ErrMalformed)
			if bad >= maxBadFrames {
				return
			}
			continue
		default:
			// Transport failure: closed socket, oversized frame, dead peer.
			return
		}
		if m.V != protocol.Version {
			s.fail(m.ID, fmt.Errorf("%w: this server speaks v%d, you sent v%d",
				protocol.ErrUnsupportedVersion, protocol.Version, m.V))
			continue
		}
		bad = 0
		s.handle(m)
	}
}

func (s *session) fail(cid string, err error) {
	s.Send(protocol.Error(cid, err))
}

func (s *session) handle(m protocol.ClientMessage) {
	switch m.Type {
	case protocol.CPing:
		s.Send(protocol.Reply(m.ID, protocol.SPong, nil))

	case protocol.CListRooms:
		s.Send(protocol.Reply(m.ID, protocol.SRoomList,
			protocol.RoomListData{Rooms: s.srv.opts.Rooms.List()}))

	case protocol.CCreateRoom:
		var d protocol.CreateRoomData
		if err := decode(m.Data, &d); err != nil {
			s.fail(m.ID, err)
			return
		}
		r, err := s.srv.opts.Rooms.Create(d)
		if err != nil {
			s.fail(m.ID, err)
			return
		}
		s.leaveCurrent()
		s.setRoom(r)
		r.Join(s, m.ID)

	case protocol.CJoinRoom, protocol.CReconnect:
		var d protocol.JoinRoomData
		if err := decode(m.Data, &d); err != nil {
			s.fail(m.ID, err)
			return
		}
		var (
			r  *room.Room
			ok bool
		)
		if d.Code != "" {
			r, ok = s.srv.opts.Rooms.Get(d.Code)
		} else {
			// Reconnect without a code: find the seat this identity holds.
			r, ok = s.srv.opts.Rooms.FindPlayerRoom(s.id.PlayerID)
		}
		if !ok {
			s.fail(m.ID, room.ErrNoSuchRoom)
			return
		}
		s.leaveCurrent()
		s.setRoom(r)
		r.Join(s, m.ID)

	case protocol.CLeaveRoom:
		s.leaveCurrent()

	case protocol.CReady:
		var d protocol.ReadyData
		if err := decode(m.Data, &d); err != nil {
			s.fail(m.ID, err)
			return
		}
		if r := s.currentRoom(); r != nil {
			r.SetReady(s.id.PlayerID, d.Ready, m.ID)
		} else {
			s.fail(m.ID, room.ErrNotInRoom)
		}

	case protocol.CStartGame:
		if r := s.currentRoom(); r != nil {
			r.Start(s.id.PlayerID, m.ID)
		} else {
			s.fail(m.ID, room.ErrNotInRoom)
		}

	case protocol.CRematch:
		if r := s.currentRoom(); r != nil {
			r.Rematch(s.id.PlayerID, m.ID)
		} else {
			s.fail(m.ID, room.ErrNotInRoom)
		}

	case protocol.CAction:
		var d protocol.ActionData
		if err := decode(m.Data, &d); err != nil {
			s.fail(m.ID, err)
			return
		}
		r := s.currentRoom()
		if r == nil {
			s.fail(m.ID, room.ErrNotInRoom)
			return
		}
		// THE authority boundary: the actor is s.id.PlayerID, which came from
		// the authenticated token. Any "player" field inside d was dropped by
		// the decoder and is never consulted.
		r.Act(s.id.PlayerID, d, m.ID)

	default:
		s.fail(m.ID, errUnknownType(m.Type))
	}
}

func (s *session) leaveCurrent() {
	if r := s.currentRoom(); r != nil {
		r.Leave(s.id.PlayerID)
		s.setRoom(nil)
	}
}

func decode(raw json.RawMessage, v any) error {
	if len(raw) == 0 {
		return nil
	}
	return json.Unmarshal(raw, v)
}

type unknownType string

func (u unknownType) Error() string { return "unknown message type: " + string(u) }

func errUnknownType(t string) error { return unknownType(t) }
