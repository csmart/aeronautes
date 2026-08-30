package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
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
	caDir        string
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
	var caPEM []byte
	if b.TLSMode == swiftcontrol.TLSModeCustomCA {
		caPEM, err = c.store.CAPem(ctx, b.TLSCAID)
		if err != nil {
			return nil, fmt.Errorf("custom-CA bookmark: %w", err)
		}
	}
	apiKey, err := c.store.Credential(ctx, bookmarkID)
	if err != nil {
		return nil, fmt.Errorf("no credential on record for this bookmark (session store is wiped on restart): %w", err)
	}
	session, err := swiftcontrol.New(b.SwiftConfig(apiKey, caPEM))
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

// Invalidate drops any cached session (and, when it is the active
// browse connection, the cached capabilities) for a bookmark whose
// stored configuration changed or which was deleted — the next use
// re-reads the store instead of operating on the old account (§65.9
// item 15, review H-12).
func (c *Connections) Invalidate(bookmarkID string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.sessions, bookmarkID)
	if c.activeID == bookmarkID {
		// The active browse pointer is the SAME staleness: drop it too,
		// so Session() answers nil and the UI reconnects against the
		// stored configuration instead of the old cluster.
		c.active = nil
		c.activeID = ""
		c.capabilities = nil
	}
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

// SetCADir sets where CA bundles are materialised as files for the
// rclone child (override.ca_cert needs a path). Default: a private dir
// under the OS temp root.
func (c *Connections) SetCADir(dir string) {
	c.mu.Lock()
	c.caDir = dir
	c.mu.Unlock()
}

// TLSRemote resolves a bookmark's trust for the engine: the insecure
// flag, and for custom-CA mode a filesystem path holding the PEM
// (written once per CA id; certificates are public material).
func (c *Connections) TLSRemote(ctx context.Context, bookmarkID string) (insecure bool, caFile string, err error) {
	b, err := c.store.Get(ctx, bookmarkID)
	if err != nil {
		return false, "", err
	}
	switch b.TLSMode {
	case swiftcontrol.TLSModeInsecure:
		return true, "", nil
	case swiftcontrol.TLSModeCustomCA:
		pem, err := c.store.CAPem(ctx, b.TLSCAID)
		if err != nil {
			return false, "", err
		}
		c.mu.Lock()
		dir := c.caDir
		c.mu.Unlock()
		if dir == "" {
			dir = filepath.Join(os.TempDir(), "aeronautes-ca")
		}
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return false, "", err
		}
		path := filepath.Join(dir, b.TLSCAID+".pem")
		if err := os.WriteFile(path, pem, 0o600); err != nil {
			return false, "", err
		}
		return false, path, nil
	default:
		return false, "", nil
	}
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
