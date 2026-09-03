// Package room owns live games. One room is one goroutine: every state
// transition for a given game happens in that loop, so engine.State is never
// touched by two goroutines at once. The engine itself stays pure.
package room

import (
	"errors"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/kaushlendra/combio/backend/internal/engine"
	"github.com/kaushlendra/combio/backend/internal/protocol"
)

// Status is the room lifecycle.
type Status string

const (
	StatusWaiting    Status = "WAITING"
	StatusReady      Status = "READY"
	StatusStarting   Status = "STARTING"
	StatusPlaying    Status = "PLAYING"
	StatusFinalTurns Status = "FINAL_TURNS"
	StatusFinished   Status = "FINISHED"
)

var (
	ErrRoomFull       = errors.New("room is full")
	ErrDisconnected   = errors.New("your connection is not attached to this seat")
	ErrInProgress     = errors.New("game already in progress")
	ErrNotHost        = errors.New("only the host can start the game")
	ErrNotReady       = errors.New("every player must be ready, and there must be at least two")
	ErrAlreadyStarted = errors.New("the game has already started")
	ErrNotInRoom      = errors.New("you are not in this room")
	ErrRoomClosed     = errors.New("room is closed")
)

// Client is one connected player. The ws layer supplies the real thing; tests
// supply a fake. Send must never block the room loop.
type Client interface {
	ID() engine.PlayerID
	Name() string
	Send(protocol.ServerMessage)
	Close()
}

// Timers are the server-owned durations. The engine holds no clock; when one of
// these fires the room feeds engine.TimerExpired through Apply.
type Timers struct {
	Memory         time.Duration
	Turn           time.Duration
	Match          time.Duration
	MatchExtension time.Duration
	Power          time.Duration
	Reconnect      time.Duration
}

func DefaultTimers() Timers {
	return Timers{
		Memory:         60 * time.Second,
		Turn:           30 * time.Second,
		Match:          5 * time.Second,
		MatchExtension: 2 * time.Second,
		Power:          15 * time.Second,
		Reconnect:      30 * time.Second,
	}
}

// Config is fixed for the room's lifetime.
type Config struct {
	Code           string
	Public         bool
	CardsPerPlayer int
	MaxPlayers     int
	Seed           int64
	Timers         Timers
	Clock          Clock
	// OnFinished is called (asynchronously) once the game is over.
	OnFinished func(code string)
}

type seat struct {
	id      engine.PlayerID
	name    string
	ready   bool
	client  Client
	removed bool
}

// scope identifies which timed phase a timer belongs to. A timer only fires if
// its scope and generation still match, which is what stops a window's timer
// from closing the window after it.
type scope struct {
	kind engine.TimerKind
	key  string
	none bool
}

type scheduled struct {
	sc        scope
	gen       uint64
	deadline  time.Time
	remaining time.Duration
	stopper   Stopper
	frozen    bool
}

// Room is the actor.
type Room struct {
	cfg   Config
	cmds  chan func()
	done  chan struct{}
	clock Clock

	// everything below is owned by the loop goroutine
	status  Status
	host    engine.PlayerID
	seats   []*seat
	st      engine.State
	started bool

	phaseTimer       *scheduled
	timerGen         uint64
	extensionPending bool

	reconnect    map[engine.PlayerID]Stopper
	reconnectGen map[engine.PlayerID]uint64

	guard serialGuard
}

// serialGuard is a cheap always-on assertion that engine.Apply is only ever
// called from one goroutine at a time for a given room. Two atomic operations
// per action is nothing, and it turns "the loop should serialize this" from a
// claim into something a test can read back.
type serialGuard struct {
	inside    atomic.Int32
	applies   atomic.Int64
	conflicts atomic.Int64
}

func (g *serialGuard) enter() {
	if g.inside.Add(1) != 1 {
		g.conflicts.Add(1)
	}
	g.applies.Add(1)
}

func (g *serialGuard) leave() { g.inside.Add(-1) }

// Stats reports how many engine transitions this room has run and how many of
// them overlapped. Conflicts must always be zero.
type Stats struct {
	Applies   int64
	Conflicts int64
}

func (r *Room) Stats() Stats {
	return Stats{Applies: r.guard.applies.Load(), Conflicts: r.guard.conflicts.Load()}
}

// Done is closed when the room loop has been told to stop.
func (r *Room) Done() <-chan struct{} { return r.done }

// New starts a room and its loop.
func New(cfg Config) *Room {
	if cfg.Clock == nil {
		cfg.Clock = RealClock()
	}
	if cfg.Timers == (Timers{}) {
		cfg.Timers = DefaultTimers()
	}
	if cfg.MaxPlayers < 2 || cfg.MaxPlayers > 8 {
		cfg.MaxPlayers = 8
	}
	if cfg.CardsPerPlayer != 4 && cfg.CardsPerPlayer != 6 && cfg.CardsPerPlayer != 8 {
		cfg.CardsPerPlayer = 4
	}
	r := &Room{
		cfg:          cfg,
		cmds:         make(chan func(), 256),
		done:         make(chan struct{}),
		clock:        cfg.Clock,
		status:       StatusWaiting,
		reconnect:    map[engine.PlayerID]Stopper{},
		reconnectGen: map[engine.PlayerID]uint64{},
	}
	go r.loop()
	return r
}

func (r *Room) loop() {
	for {
		select {
		case fn := <-r.cmds:
			fn()
		case <-r.done:
			return
		}
	}
}

// do queues work on the loop. Never call this from inside the loop.
func (r *Room) do(fn func()) {
	select {
	case r.cmds <- fn:
	case <-r.done:
	}
}

// sync queues work and waits for it, so callers can read loop-owned state
// safely.
func (r *Room) sync(fn func()) {
	ready := make(chan struct{})
	select {
	// close(ready) is deferred so a callback that panics still releases the
	// caller instead of wedging it.
	case r.cmds <- func() { defer close(ready); fn() }:
	case <-r.done:
		return
	}
	select {
	case <-ready:
	case <-r.done:
	}
}

// Close stops the room.
func (r *Room) Close() {
	select {
	case <-r.done:
	default:
		close(r.done)
	}
}

func (r *Room) Code() string { return r.cfg.Code }

// ---------- public API (all asynchronous, all serialized) ----------

// Join attaches a client. It doubles as reconnect: if the player already has a
// seat in a running game, their session is restored instead.
func (r *Room) Join(c Client, cid string) {
	r.do(func() { r.join(c, cid) })
}

func (r *Room) Leave(id engine.PlayerID) { r.do(func() { r.leave(id) }) }

func (r *Room) SetReady(id engine.PlayerID, ready bool, cid string) {
	r.do(func() { r.setReady(id, ready, cid) })
}

func (r *Room) Start(id engine.PlayerID, cid string) { r.do(func() { r.start(id, cid) }) }

// Act is the only way a gameplay action enters the room. The actor is supplied
// by the caller from the authenticated connection; nothing in data can change
// it.
func (r *Room) Act(actor engine.PlayerID, data protocol.ActionData, cid string) {
	r.do(func() { r.act(actor, data, cid) })
}

// Disconnected reports that a player's socket dropped.
func (r *Room) Disconnected(id engine.PlayerID) { r.do(func() { r.disconnected(id) }) }

// ---------- loop-side handlers ----------

func (r *Room) seatOf(id engine.PlayerID) *seat {
	for _, s := range r.seats {
		if s.id == id {
			return s
		}
	}
	return nil
}

func (r *Room) sendTo(id engine.PlayerID, m protocol.ServerMessage) {
	if s := r.seatOf(id); s != nil && s.client != nil {
		s.client.Send(m)
	}
}

func (r *Room) broadcastMsg(m protocol.ServerMessage) {
	for _, s := range r.seats {
		if s.client != nil {
			s.client.Send(m)
		}
	}
}

func (r *Room) join(c Client, cid string) {
	existing := r.seatOf(c.ID())

	if existing == nil {
		if r.started {
			// Only a player who already has a seat may enter a running game.
			c.Send(protocol.Error(cid, ErrInProgress))
			return
		}
		if len(r.seats) >= r.cfg.MaxPlayers {
			c.Send(protocol.Error(cid, ErrRoomFull))
			return
		}
		s := &seat{id: c.ID(), name: c.Name(), client: c}
		r.seats = append(r.seats, s)
		if r.host == "" {
			r.host = s.id
		}
		r.refreshStatus()
		r.broadcastMsg(protocol.Msg(protocol.SPlayerJoined,
			protocol.PlayerChangeData{Player: s.id, Name: s.name}))
		r.broadcastRoomState()
		return
	}

	// Reconnect, or a second connection replacing the first. Re-attaching a
	// socket that is already attached is a no-op replay, not a reconnect.
	wasDetached := existing.client == nil
	if existing.client != nil && existing.client != c {
		existing.client.Send(protocol.Msg(protocol.SReconnectRequired,
			protocol.ReconnectRequiredData{Code: r.cfg.Code, Reason: "replaced by a newer connection"}))
		existing.client.Close()
	}
	existing.client = c
	existing.name = c.Name()

	if r.started && !existing.removed && wasDetached {
		r.cancelReconnect(existing.id)
		r.applySystem(engine.PlayerReconnected{Base: engine.For(existing.id)})
	}
	r.broadcastRoomState()
	if r.started {
		// Give the returning player their private view immediately.
		r.sendState(existing)
		if r.st.Phase == engine.PhaseFinished {
			c.Send(protocol.Msg(protocol.SGameFinished,
				protocol.GameFinishedData{View: r.st.ViewFor(existing.id)}))
		}
	}
}

func (r *Room) leave(id engine.PlayerID) {
	s := r.seatOf(id)
	if s == nil {
		return
	}
	if r.started {
		// Leaving a live game is a disconnect: the rules decide what happens.
		s.client = nil
		r.disconnected(id)
		return
	}
	out := r.seats[:0]
	for _, x := range r.seats {
		if x.id != id {
			out = append(out, x)
		}
	}
	r.seats = out
	if r.host == id && len(r.seats) > 0 {
		r.host = r.seats[0].id
	}
	r.refreshStatus()
	r.broadcastMsg(protocol.Msg(protocol.SPlayerLeft, protocol.PlayerChangeData{Player: id, Name: s.name}))
	r.broadcastRoomState()
}

func (r *Room) setReady(id engine.PlayerID, ready bool, cid string) {
	s := r.seatOf(id)
	if s == nil {
		return
	}
	if r.started {
		r.sendTo(id, protocol.Error(cid, ErrAlreadyStarted))
		return
	}
	s.ready = ready
	r.refreshStatus()
	r.broadcastRoomState()
}

func (r *Room) start(id engine.PlayerID, cid string) {
	if r.started {
		r.sendTo(id, protocol.Error(cid, ErrAlreadyStarted))
		return
	}
	if id != r.host {
		r.sendTo(id, protocol.Error(cid, ErrNotHost))
		return
	}
	if r.status != StatusReady {
		r.sendTo(id, protocol.Error(cid, ErrNotReady))
		return
	}
	ids := make([]engine.PlayerID, 0, len(r.seats))
	for _, s := range r.seats {
		ids = append(ids, s.id)
	}
	st, evs, err := engine.NewGame(
		engine.Config{CardsPerPlayer: r.cfg.CardsPerPlayer},
		ids,
		engine.NewSeededShuffler(r.cfg.Seed),
	)
	if err != nil {
		r.sendTo(id, protocol.Error(cid, err))
		return
	}
	r.st = st
	r.started = true
	r.status = StatusStarting
	r.broadcastMsg(protocol.Msg(protocol.SGameStarted, protocol.GameStartedData{
		CardsPerPlayer: r.cfg.CardsPerPlayer,
		Inspect:        r.cfg.CardsPerPlayer / 2,
	}))
	r.broadcastRoomState()
	r.noteEvents(evs)
	r.broadcast(evs)
	r.syncTimers()
}

func (r *Room) act(actor engine.PlayerID, data protocol.ActionData, cid string) {
	if !r.started {
		r.sendTo(actor, protocol.Error(cid, ErrNotReady))
		return
	}
	seat := r.seatOf(actor)
	if seat == nil {
		r.sendTo(actor, protocol.Error(cid, ErrNotInRoom))
		return
	}
	// A seat with no live connection cannot act. The engine would reject most
	// of these anyway (the game is paused), but this keeps a stale goroutine
	// from acting on behalf of someone who has gone.
	if seat.client == nil {
		return
	}
	if seat.removed {
		r.sendTo(actor, protocol.Error(cid, ErrDisconnected))
		return
	}
	// The authenticated actor is stamped here and nowhere else.
	a, err := protocol.BuildAction(data.Kind, data.Args, actor)
	if err != nil {
		r.sendTo(actor, protocol.Error(cid, err))
		return
	}
	r.guard.enter()
	ns, evs, err := engine.Apply(r.st, a)
	r.guard.leave()
	if err != nil {
		// A lost click race arrives here as ErrStale: rejected, free, private.
		r.sendTo(actor, protocol.Error(cid, err))
		return
	}
	r.st = ns
	r.noteEvents(evs)
	r.broadcast(evs)
	r.refreshStatus()
	r.syncTimers()
}

// applySystem runs a server-generated action (timers, connection changes).
// There is no client to report a rejection to, so failures are dropped: they
// mean the state moved on, which the generation guard already tolerates.
func (r *Room) applySystem(a engine.Action) bool {
	r.guard.enter()
	ns, evs, err := engine.Apply(r.st, a)
	r.guard.leave()
	if err != nil {
		// The action was rejected, but a timer that fired is now spent. Re-arm
		// before returning, or the room would be left with no timer at all and
		// nothing would ever wake it again.
		r.syncTimers()
		return false
	}
	r.st = ns
	r.noteEvents(evs)
	r.broadcast(evs)
	r.refreshStatus()
	r.syncTimers()
	return true
}

// noteEvents picks up the few events the server itself must react to. Right now
// that is only the matching-window extension: the engine says "+2s", the server
// owns the clock, so the server is the one that stretches the timer.
func (r *Room) noteEvents(evs []engine.Event) {
	for _, e := range evs {
		if _, ok := e.(engine.EvMatchingWindowExtended); ok {
			r.extensionPending = true
		}
	}
}

func (r *Room) disconnected(id engine.PlayerID) {
	s := r.seatOf(id)
	if s == nil {
		return
	}
	s.client = nil
	if !r.started {
		r.leave(id)
		return
	}
	if s.removed || r.st.Phase == engine.PhaseFinished {
		return
	}
	r.applySystem(engine.PlayerDisconnected{Base: engine.For(id)})
	r.startReconnectTimer(id)
	r.broadcastRoomState()
}

// ---------- status ----------

func (r *Room) refreshStatus() {
	if !r.started {
		ready := len(r.seats) >= 2
		for _, s := range r.seats {
			if !s.ready {
				ready = false
			}
		}
		if ready {
			r.status = StatusReady
		} else {
			r.status = StatusWaiting
		}
		return
	}
	prev := r.status
	switch {
	case r.st.Phase == engine.PhaseFinished:
		r.status = StatusFinished
	case r.st.Declared:
		r.status = StatusFinalTurns
	case r.st.Phase == engine.PhaseMemory:
		r.status = StatusStarting
	default:
		r.status = StatusPlaying
	}
	// Mirror engine removals onto the seats.
	for _, s := range r.seats {
		if p := r.st.Player(s.id); p != nil && p.Removed {
			s.removed = true
		}
	}
	if r.status == StatusFinished && prev != StatusFinished {
		r.stopPhaseTimer()
		for id := range r.reconnect {
			r.cancelReconnect(id)
		}
		for _, s := range r.seats {
			if s.client != nil {
				s.client.Send(protocol.Msg(protocol.SGameFinished,
					protocol.GameFinishedData{View: r.st.ViewFor(s.id)}))
			}
		}
		r.broadcastRoomState()
		if r.cfg.OnFinished != nil {
			code := r.cfg.Code
			cb := r.cfg.OnFinished
			go cb(code)
		}
	}
}

// ---------- broadcasting ----------

// broadcast is the ONLY path from engine output to the wire. Every player gets
// their own ViewFor and their own FilterEvents; raw State never leaves here.
func (r *Room) broadcast(evs []engine.Event) {
	for _, s := range r.seats {
		if s.client == nil {
			continue
		}
		s.client.Send(protocol.Msg(protocol.SStateUpdate,
			protocol.StateUpdateData{View: r.st.ViewFor(s.id)}))
		mine := engine.FilterEvents(evs, s.id)
		if len(mine) > 0 {
			s.client.Send(protocol.Msg(protocol.SEvents,
				protocol.EventsData{Events: protocol.EncodeEvents(mine)}))
		}
	}
}

func (r *Room) sendState(s *seat) {
	if s.client == nil {
		return
	}
	s.client.Send(protocol.Msg(protocol.SStateUpdate,
		protocol.StateUpdateData{View: r.st.ViewFor(s.id)}))
}

func (r *Room) roomState() protocol.RoomStateData {
	ps := make([]protocol.RoomPlayer, 0, len(r.seats))
	for _, s := range r.seats {
		ps = append(ps, protocol.RoomPlayer{
			ID: s.id, Name: s.name, Ready: s.ready,
			Connected: s.client != nil, Removed: s.removed, Host: s.id == r.host,
		})
	}
	return protocol.RoomStateData{
		Code: r.cfg.Code, Public: r.cfg.Public, Status: string(r.status),
		CardsPerPlayer: r.cfg.CardsPerPlayer, MaxPlayers: r.cfg.MaxPlayers, Players: ps,
	}
}

func (r *Room) broadcastRoomState() {
	r.broadcastMsg(protocol.Msg(protocol.SRoomState, r.roomState()))
}

// ---------- timers ----------

func (r *Room) duration(k engine.TimerKind) time.Duration {
	switch k {
	case engine.TimerMemory:
		return r.cfg.Timers.Memory
	case engine.TimerTurn:
		return r.cfg.Timers.Turn
	case engine.TimerMatchWindow:
		return r.cfg.Timers.Match
	default:
		return r.cfg.Timers.Power
	}
}

func scopeFor(st *engine.State) scope {
	switch st.Phase {
	case engine.PhaseMemory:
		return scope{kind: engine.TimerMemory}
	case engine.PhaseTurnStart, engine.PhaseDrawDecision:
		return scope{kind: engine.TimerTurn, key: string(st.CurrentID())}
	case engine.PhaseMatching:
		return scope{kind: engine.TimerMatchWindow, key: string(st.CurrentID())}
	case engine.PhasePower, engine.PhaseKingDecision:
		return scope{kind: engine.TimerPower, key: string(st.PowerActor)}
	default:
		return scope{none: true}
	}
}

// syncTimers reconciles the running timer with the phase after every apply.
func (r *Room) syncTimers() {
	if !r.started || r.st.Phase == engine.PhaseFinished {
		r.stopPhaseTimer()
		return
	}
	if r.st.Paused {
		r.freezePhaseTimer()
		return
	}
	want := scopeFor(&r.st)
	if want.none {
		r.stopPhaseTimer()
		return
	}
	if r.phaseTimer != nil && r.phaseTimer.sc == want {
		if r.phaseTimer.frozen {
			r.resumePhaseTimer()
		} else if r.extensionPending {
			r.extensionPending = false
			r.extendPhaseTimer(r.cfg.Timers.MatchExtension)
		}
		return
	}
	r.extensionPending = false
	r.startPhaseTimer(want)
}

func (r *Room) startPhaseTimer(sc scope) {
	r.stopPhaseTimer()
	d := r.duration(sc.kind)
	r.timerGen++
	gen := r.timerGen
	t := &scheduled{sc: sc, gen: gen, deadline: r.clock.Now().Add(d)}
	t.stopper = r.clock.AfterFunc(d, func() {
		r.do(func() { r.firePhaseTimer(gen) })
	})
	r.phaseTimer = t
}

func (r *Room) stopPhaseTimer() {
	if r.phaseTimer == nil {
		return
	}
	if r.phaseTimer.stopper != nil {
		r.phaseTimer.stopper.Stop()
	}
	// Bumping the generation makes any callback already in flight stale.
	r.timerGen++
	r.phaseTimer = nil
}

func (r *Room) freezePhaseTimer() {
	t := r.phaseTimer
	if t == nil || t.frozen {
		return
	}
	t.remaining = t.deadline.Sub(r.clock.Now())
	if t.remaining < 0 {
		t.remaining = 0
	}
	if t.stopper != nil {
		t.stopper.Stop()
	}
	r.timerGen++
	t.gen = r.timerGen
	t.frozen = true
}

func (r *Room) resumePhaseTimer() {
	t := r.phaseTimer
	if t == nil || !t.frozen {
		return
	}
	r.timerGen++
	gen := r.timerGen
	t.gen = gen
	t.frozen = false
	t.deadline = r.clock.Now().Add(t.remaining)
	t.stopper = r.clock.AfterFunc(t.remaining, func() {
		r.do(func() { r.firePhaseTimer(gen) })
	})
}

func (r *Room) extendPhaseTimer(by time.Duration) {
	t := r.phaseTimer
	if t == nil || t.frozen {
		return
	}
	remaining := t.deadline.Sub(r.clock.Now()) + by
	if t.stopper != nil {
		t.stopper.Stop()
	}
	r.timerGen++
	gen := r.timerGen
	t.gen = gen
	t.deadline = r.clock.Now().Add(remaining)
	t.stopper = r.clock.AfterFunc(remaining, func() {
		r.do(func() { r.firePhaseTimer(gen) })
	})
}

// firePhaseTimer is the generation guard: a timer from a phase that has already
// ended cannot close the phase that replaced it.
func (r *Room) firePhaseTimer(gen uint64) {
	t := r.phaseTimer
	if t == nil || t.gen != gen || t.frozen {
		return
	}
	if r.st.Paused {
		return
	}
	kind := t.sc.kind
	r.phaseTimer = nil
	r.applySystem(engine.TimerExpired{Kind: kind})
}

func (r *Room) startReconnectTimer(id engine.PlayerID) {
	r.cancelReconnect(id)
	r.reconnectGen[id]++
	gen := r.reconnectGen[id]
	r.reconnect[id] = r.clock.AfterFunc(r.cfg.Timers.Reconnect, func() {
		r.do(func() { r.fireReconnect(id, gen) })
	})
}

func (r *Room) cancelReconnect(id engine.PlayerID) {
	if s, ok := r.reconnect[id]; ok {
		s.Stop()
		delete(r.reconnect, id)
	}
	r.reconnectGen[id]++
}

func (r *Room) fireReconnect(id engine.PlayerID, gen uint64) {
	if r.reconnectGen[id] != gen {
		return
	}
	delete(r.reconnect, id)
	if s := r.seatOf(id); s == nil || s.client != nil || s.removed {
		return
	}
	// The engine decides what removal means: cards sink to the bottom of the
	// discard pile, the rotation closes up, the round accounting stays honest.
	r.applySystem(engine.RemovePlayer{Base: engine.For(id)})
	r.broadcastRoomState()
}

// ---------- inspection ----------

// Info is a snapshot for the manager and for tests.
type Info struct {
	Code           string
	Public         bool
	Status         Status
	Host           engine.PlayerID
	CardsPerPlayer int
	MaxPlayers     int
	Players        []protocol.RoomPlayer
	Started        bool
	Phase          string
	Paused         bool
	ActiveRank     engine.Rank
	Empty          bool
}

func (r *Room) Snapshot() Info {
	var out Info
	r.sync(func() {
		rs := r.roomState()
		out = Info{
			Code: r.cfg.Code, Public: r.cfg.Public, Status: r.status, Host: r.host,
			CardsPerPlayer: r.cfg.CardsPerPlayer, MaxPlayers: r.cfg.MaxPlayers,
			Players: rs.Players, Started: r.started,
		}
		connected := 0
		for _, s := range r.seats {
			if s.client != nil {
				connected++
			}
		}
		out.Empty = connected == 0
		if r.started {
			out.Phase = r.st.Phase.String()
			out.Paused = r.st.Paused
			out.ActiveRank = r.st.ActiveRank
		}
	})
	return out
}

// ReplaceStateForTesting swaps in a scripted game state and re-reconciles the
// room around it. The name is deliberately ugly: it exists so tests and
// scenarios can pin an exact deal, and nothing on the wire path may call it.
func (r *Room) ReplaceStateForTesting(st engine.State) {
	r.sync(func() {
		r.st = st
		r.started = true
		r.refreshStatus()
		r.syncTimers()
	})
}

// WithState runs fn against the authoritative state inside the loop. It exists
// for tests and diagnostics; nothing on the wire path may use it.
//
// fn runs on the room's own goroutine, so it must only read. Anything that can
// abort the calling goroutine — a test assertion, for instance — would take the
// room loop down with it. Use CloneState for that.
func (r *Room) WithState(fn func(*engine.State)) {
	r.sync(func() { fn(&r.st) })
}

// CloneState returns a snapshot of the authoritative state, safe to inspect on
// any goroutine.
func (r *Room) CloneState() engine.State {
	var out engine.State
	r.sync(func() { out = r.st.Clone() })
	return out
}

func (r *Room) String() string {
	return fmt.Sprintf("room(%s)", r.cfg.Code)
}
