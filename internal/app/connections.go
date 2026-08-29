package app

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/csmart/aeronautes/internal/bookmarks"
	"github.com/csmart/aeronautes/internal/swiftcontrol"
)

// ErrNotConnected is returned by browse/inspect calls before Connect.
var ErrNotConnected = errors.New("not connected — choose a bookmark first")

// Connections owns the live Swift sessions: the single ACTIVE session
// the browse/inspect/mutate surfaces follow, plus a per-bookmark
// registry for cross-account transfers (a Copy holds two sessions at
// once, §65.4).
type Connections struct {
	store *bookmarks.Store

	mu           sync.Mutex
	active       *swiftcontrol.Session
	activeID     string
	capabilities *swiftcontrol.Capabilities
	sessions     map[string]*swiftcontrol.Session
}

// NewConnections builds the registry over the bookmark store.
func NewConnections(store *bookmarks.Store) *Connections {
	return &Connections{store: store, sessions: map[string]*swiftcontrol.Session{}}
}

// open authenticates one bookmark into a fresh session.
func (c *Connections) open(ctx context.Context, bookmarkID string) (*swiftcontrol.Session, error) {
	b, err := c.store.Get(ctx, bookmarkID)
	if err != nil {
		return nil, err
	}
	if b.TLSMode == swiftcontrol.TLSModeCustomCA {
		// The CA store arrives with the settings surface; refuse clearly
		// rather than silently downgrading trust (§12.3).
		return nil, errors.New("custom-CA bookmarks are not connectable yet: the CA store is not wired")
	}
	apiKey, err := c.store.Credential(ctx, bookmarkID)
	if err != nil {
		return nil, fmt.Errorf("no credential on record for this bookmark (session store is wiped on restart): %w", err)
	}
	session, err := swiftcontrol.New(b.SwiftConfig(apiKey, nil))
	if err != nil {
		return nil, err
	}
	if err := session.Authenticate(ctx); err != nil {
		return nil, err
	}
	return session, nil
}

// Connect authenticates the bookmark and makes it the active session.
func (c *Connections) Connect(ctx context.Context, bookmarkID string) error {
	session, err := c.open(ctx, bookmarkID)
	if err != nil {
		return err
	}
	// Capability discovery at connect (§10): what this cluster supports
	// gates what the UI offers. An unreadable /info yields the Unknown
	// mode, never an error — Unknown is not Unsupported.
	capabilities, err := session.FetchCapabilities(ctx)
	if err != nil {
		return err
	}

	c.mu.Lock()
	c.active = session
	c.activeID = bookmarkID
	c.capabilities = capabilities
	c.sessions[bookmarkID] = session
	c.mu.Unlock()
	return nil
}

// SessionFor returns a live session for the bookmark, authenticating on
// first use and caching it (transfers hold sessions for BOTH ends —
// neither needs to be the active browse connection).
func (c *Connections) SessionFor(ctx context.Context, bookmarkID string) (*swiftcontrol.Session, error) {
	c.mu.Lock()
	if session, ok := c.sessions[bookmarkID]; ok {
		c.mu.Unlock()
		return session, nil
	}
	c.mu.Unlock()

	session, err := c.open(ctx, bookmarkID)
	if err != nil {
		return nil, err
	}
	c.mu.Lock()
	c.sessions[bookmarkID] = session
	c.mu.Unlock()
	return session, nil
}

// TLSInsecure reports whether the bookmark opts into unverified TLS —
// the transfer service maps it onto the rclone remote (§12.3: identical
// trust across one child, or the runner refuses).
func (c *Connections) TLSInsecure(ctx context.Context, bookmarkID string) (bool, error) {
	b, err := c.store.Get(ctx, bookmarkID)
	if err != nil {
		return false, err
	}
	return b.TLSMode == swiftcontrol.TLSModeInsecure, nil
}

// Capabilities returns the active connection's capability snapshot, nil
// when not connected.
func (c *Connections) Capabilities() *swiftcontrol.Capabilities {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.capabilities
}

// Session returns the active session.
func (c *Connections) Session() (*swiftcontrol.Session, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.active == nil {
		return nil, ErrNotConnected
	}
	return c.active, nil
}

// ActiveBookmarkID returns the connected bookmark's id, "" when none —
// the scope key for evidence and other per-endpoint state.
func (c *Connections) ActiveBookmarkID() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.activeID
}

// AccountName derives the display account name from the active session's
// storage URL suffix (…/v1/AUTH_demo → AUTH_demo).
func (c *Connections) AccountName() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.active == nil {
		return ""
	}
	url := c.active.StorageURL()
	if i := strings.LastIndex(url, "/"); i >= 0 {
		return url[i+1:]
	}
	return url
}
