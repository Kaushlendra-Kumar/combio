package room

import (
	"errors"
	"math/rand"
	"strings"
	"sync"
	"time"

	"github.com/kaushlendra/combio/backend/internal/engine"
	"github.com/kaushlendra/combio/backend/internal/protocol"
)

var (
	ErrNoSuchRoom   = errors.New("no room with that code")
	ErrBadSettings  = errors.New("cards per player must be 4, 6 or 8 and max players 2-8")
	ErrTooManyRooms = errors.New("the server is at its room limit, try again shortly")
	ErrShuttingDown = errors.New("the server is shutting down")
)

// DefaultMaxRooms bounds how many live games one process will hold. It exists
// so a script cannot open rooms until the process runs out of memory.
const DefaultMaxRooms = 500

// codeAlphabet leaves out characters people misread when reading a code aloud.
const codeAlphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"

// Manager is the room registry. Its lock protects only the map: it never holds
// the lock while talking to a room loop, which keeps the two lock domains from
// ever meeting.
type Manager struct {
	mu     sync.Mutex
	rooms  map[string]*Room
	rng    *rand.Rand
	clock  Clock
	timers Timers
	// seed lets tests pin the deal; zero means "derive one per room".
	seed     int64
	maxRooms int
	closed   bool
}

type ManagerOptions struct {
	Clock    Clock
	Timers   Timers
	Seed     int64
	Rand     *rand.Rand
	MaxRooms int
}

func NewManager(o ManagerOptions) *Manager {
	if o.Clock == nil {
		o.Clock = RealClock()
	}
	if o.Timers == (Timers{}) {
		o.Timers = DefaultTimers()
	}
	if o.Rand == nil {
		o.Rand = rand.New(rand.NewSource(time.Now().UnixNano()))
	}
	if o.MaxRooms <= 0 {
		o.MaxRooms = DefaultMaxRooms
	}
	return &Manager{
		rooms:    map[string]*Room{},
		rng:      o.Rand,
		clock:    o.Clock,
		timers:   o.Timers,
		seed:     o.Seed,
		maxRooms: o.MaxRooms,
	}
}

func (m *Manager) newCode() string {
	for attempt := 0; ; attempt++ {
		var b strings.Builder
		for i := 0; i < 4; i++ {
			b.WriteByte(codeAlphabet[m.rng.Intn(len(codeAlphabet))])
		}
		code := b.String()
		if _, taken := m.rooms[code]; !taken {
			return code
		}
		if attempt > 1000 {
			return code + "X"
		}
	}
}

// Create opens a room. The creator becomes the host when they join it.
func (m *Manager) Create(d protocol.CreateRoomData) (*Room, error) {
	cards := d.CardsPerPlayer
	if cards == 0 {
		cards = 4
	}
	max := d.MaxPlayers
	if max == 0 {
		max = 8
	}
	if (cards != 4 && cards != 6 && cards != 8) || max < 2 || max > 8 {
		return nil, ErrBadSettings
	}

	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return nil, ErrShuttingDown
	}
	if len(m.rooms) >= m.maxRooms {
		m.mu.Unlock()
		return nil, ErrTooManyRooms
	}
	code := m.newCode()
	seed := m.seed
	if seed == 0 {
		seed = m.rng.Int63()
	}
	r := New(Config{
		Code:           code,
		Public:         d.Public,
		CardsPerPlayer: cards,
		MaxPlayers:     max,
		Seed:           seed,
		Timers:         m.timers,
		Clock:          m.clock,
		OnFinished:     m.onFinished,
	})
	m.rooms[code] = r
	m.mu.Unlock()
	return r, nil
}

func (m *Manager) Get(code string) (*Room, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.rooms[strings.ToUpper(strings.TrimSpace(code))]
	return r, ok
}

// all copies the room pointers out from under the lock so the caller can talk
// to room loops without holding it.
func (m *Manager) all() []*Room {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]*Room, 0, len(m.rooms))
	for _, r := range m.rooms {
		out = append(out, r)
	}
	return out
}

func (m *Manager) Count() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.rooms)
}

// List returns joinable public rooms.
func (m *Manager) List() []protocol.RoomSummary {
	out := []protocol.RoomSummary{}
	for _, r := range m.all() {
		info := r.Snapshot()
		if !info.Public || info.Started {
			continue
		}
		out = append(out, protocol.RoomSummary{
			Code: info.Code, Players: len(info.Players), MaxPlayers: info.MaxPlayers,
			CardsPerPlayer: info.CardsPerPlayer, Status: string(info.Status),
		})
	}
	return out
}

// FindPlayerRoom locates the live room a player already has a seat in, so a
// returning client can reconnect without remembering the code.
func (m *Manager) FindPlayerRoom(id engine.PlayerID) (*Room, bool) {
	for _, r := range m.all() {
		info := r.Snapshot()
		if info.Status == StatusFinished {
			continue
		}
		for _, p := range info.Players {
			if p.ID == id {
				return r, true
			}
		}
	}
	return nil, false
}

// Remove closes and forgets a room.
func (m *Manager) Remove(code string) {
	m.mu.Lock()
	r, ok := m.rooms[code]
	if ok {
		delete(m.rooms, code)
	}
	m.mu.Unlock()
	if ok {
		r.Close()
	}
}

// onFinished is invoked from a goroutine by the room, never from its loop, so
// it can safely take the manager lock. Finished rooms are left in place so
// players can read the result screen; Reap collects them once nobody is left.
func (m *Manager) onFinished(code string) { _ = code }

// Reap closes rooms nobody is connected to.
func (m *Manager) Reap() int {
	n := 0
	for _, r := range m.all() {
		if info := r.Snapshot(); info.Empty {
			m.Remove(info.Code)
			n++
		}
	}
	return n
}

// CloseAll shuts every room down but leaves the manager usable.
func (m *Manager) CloseAll() {
	for _, r := range m.all() {
		m.Remove(r.Code())
	}
}

// Close shuts every room down and refuses further room creation. Used by
// graceful shutdown.
func (m *Manager) Close() {
	m.mu.Lock()
	m.closed = true
	m.mu.Unlock()
	m.CloseAll()
}
