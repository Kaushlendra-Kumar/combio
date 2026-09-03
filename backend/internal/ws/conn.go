// Package ws is pure transport. It knows about sockets and JSON frames and
// nothing about COMBIO: no gameplay logic lives here.
package ws

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"github.com/kaushlendra/combio/backend/internal/protocol"
)

const (
	writeWait  = 10 * time.Second
	pongWait   = 60 * time.Second
	pingPeriod = 25 * time.Second
	sendBuffer = 64

	// MaxMessageSize caps a single inbound frame. Gorilla closes the
	// connection when a client exceeds it, which bounds how much memory one
	// client can make the server allocate for a message.
	MaxMessageSize = 16 << 10
)

var (
	// ErrClosed is returned once the connection is finished.
	ErrClosed = errors.New("connection closed")

	// ErrBadMessage means the frame arrived intact but was not valid protocol
	// JSON. The connection is still healthy: the caller should reply with an
	// error rather than hanging up.
	ErrBadMessage = errors.New("malformed message")
)

// Conn is one client socket.
type Conn struct {
	c    *websocket.Conn
	send chan []byte
	done chan struct{}
	once sync.Once
}

// Upgrader accepts any origin: the MVP has no cookie auth to protect, the token
// is supplied explicitly, and the deployment origin is not known yet.
var upgrader = websocket.Upgrader{
	ReadBufferSize:  4096,
	WriteBufferSize: 4096,
	CheckOrigin:     func(*http.Request) bool { return true },
}

// Upgrade turns an HTTP request into a Conn and starts its write pump.
func Upgrade(w http.ResponseWriter, r *http.Request) (*Conn, error) {
	raw, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return nil, err
	}
	c := &Conn{
		c:    raw,
		send: make(chan []byte, sendBuffer),
		done: make(chan struct{}),
	}
	raw.SetReadLimit(MaxMessageSize)
	_ = raw.SetReadDeadline(time.Now().Add(pongWait))
	raw.SetPongHandler(func(string) error {
		return raw.SetReadDeadline(time.Now().Add(pongWait))
	})
	go c.writePump()
	return c, nil
}

// Send queues a message. It never blocks: a client that cannot keep up is
// disconnected rather than allowed to stall the room loop.
func (c *Conn) Send(m protocol.ServerMessage) {
	data, err := json.Marshal(m)
	if err != nil {
		return
	}
	select {
	case c.send <- data:
	case <-c.done:
	default:
		c.Close()
	}
}

// Read blocks for the next client message.
//
// A transport error (closed socket, oversized frame) is fatal and the caller
// should stop. A decode failure comes back wrapped in ErrBadMessage and is not:
// garbage from one frame should not cost a player their game.
func (c *Conn) Read() (protocol.ClientMessage, error) {
	var m protocol.ClientMessage
	_, data, err := c.c.ReadMessage()
	if err != nil {
		return m, err
	}
	if err := json.Unmarshal(data, &m); err != nil {
		return m, fmt.Errorf("%w: %v", ErrBadMessage, err)
	}
	return m, nil
}

func (c *Conn) Close() {
	c.once.Do(func() {
		close(c.done)
		_ = c.c.Close()
	})
}

func (c *Conn) Done() <-chan struct{} { return c.done }

// RemoteAddr is used for logging only.
func (c *Conn) RemoteAddr() string { return c.c.RemoteAddr().String() }

func (c *Conn) writePump() {
	ticker := time.NewTicker(pingPeriod)
	defer func() {
		ticker.Stop()
		c.Close()
	}()
	for {
		select {
		case data := <-c.send:
			_ = c.c.SetWriteDeadline(time.Now().Add(writeWait))
			if err := c.c.WriteMessage(websocket.TextMessage, data); err != nil {
				return
			}
		case <-ticker.C:
			_ = c.c.SetWriteDeadline(time.Now().Add(writeWait))
			if err := c.c.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		case <-c.done:
			return
		}
	}
}
