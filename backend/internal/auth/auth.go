// Package auth resolves a connection to a player identity.
//
// Phase 5 will swap the dev provider for Supabase JWT validation. Everything
// upstream depends only on the Authenticator interface, so that swap touches
// this package and nothing else.
package auth

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/kaushlendra/combio/backend/internal/engine"
)

var (
	ErrNoToken      = errors.New("no auth token supplied")
	ErrBadToken     = errors.New("unknown or expired auth token")
	ErrInvalidName  = errors.New("display name must be 1-24 printable characters")
	errTokenGenFail = errors.New("could not generate a token")
)

// Identity is who the server believes is on the other end of a connection.
// PlayerID is stable for a given identity, which is what makes reconnecting to
// an in-progress game possible.
type Identity struct {
	PlayerID engine.PlayerID `json:"player_id"`
	Name     string          `json:"name"`
}

// Authenticator turns an opaque token into an identity.
type Authenticator interface {
	Authenticate(token string) (Identity, error)
}

// DevTokens is the MVP implementation: an in-memory token table. It issues an
// opaque random token per player and never accepts a client-supplied player id.
type DevTokens struct {
	mu     sync.RWMutex
	byTok  map[string]Identity
	nextID int
}

func NewDevTokens() *DevTokens {
	return &DevTokens{byTok: map[string]Identity{}}
}

// Issue mints a token for a display name and returns the identity bound to it.
// The player id is generated here; a client can never choose its own.
func (d *DevTokens) Issue(name string) (string, Identity, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		name = "Player"
	}
	if len(name) > 24 || !printable(name) {
		return "", Identity{}, ErrInvalidName
	}
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "", Identity{}, errTokenGenFail
	}
	tok := hex.EncodeToString(buf)

	d.mu.Lock()
	defer d.mu.Unlock()
	d.nextID++
	id := Identity{
		PlayerID: engine.PlayerID(fmt.Sprintf("u%d", d.nextID)),
		Name:     name,
	}
	d.byTok[tok] = id
	return tok, id, nil
}

// IssueFixed binds a known token to a known player id. Test support only.
func (d *DevTokens) IssueFixed(token string, id engine.PlayerID, name string) Identity {
	d.mu.Lock()
	defer d.mu.Unlock()
	ident := Identity{PlayerID: id, Name: name}
	d.byTok[token] = ident
	return ident
}

func (d *DevTokens) Authenticate(token string) (Identity, error) {
	if strings.TrimSpace(token) == "" {
		return Identity{}, ErrNoToken
	}
	d.mu.RLock()
	defer d.mu.RUnlock()
	id, ok := d.byTok[token]
	if !ok {
		return Identity{}, ErrBadToken
	}
	return id, nil
}

func printable(s string) bool {
	for _, r := range s {
		if r < 0x20 || r == 0x7f {
			return false
		}
	}
	return true
}
