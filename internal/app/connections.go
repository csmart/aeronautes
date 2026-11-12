package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/csmart/aeronautes/internal/bookmarks"
	"github.com/csmart/aeronautes/internal/secrets"
	"github.com/csmart/aeronautes/internal/swiftcontrol"
	"github.com/csmart/aeronautes/internal/transfer/rclone"
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
	active       cachedSession
	activeID     string
	capabilities *swiftcontrol.Capabilities
	sessions     map[string]cachedSession
	// discovered holds the /info read for each cached session, so a
	// reconnect to a session still cached reuses it (ADR 0110 decision 1).
	// It is keyed by the session rather than kept in cachedSession, which
	// is compared by value to tell whether a cached entry is still current.
	discovered map[*swiftcontrol.Session]*swiftcontrol.Capabilities
	caDir      string
	caMu       sync.Mutex
	// settings holds the container settings facts observed under each
	// bookmark's current connection generation (ADR 0071).
	settings settingsFacts
	// connectionCap supplies the per-host connection cap a session is
	// opened with (ADR 0074 decision 4); nil means the session default.
	connectionCap func(context.Context) int
}

type cachedSession struct {
	session    *swiftcontrol.Session
	generation int64
}

// NewConnections builds the registry over the bookmark store.
func NewConnections(store *bookmarks.Store) *Connections {
	return &Connections{store: store, sessions: map[string]cachedSession{}, discovered: map[*swiftcontrol.Session]*swiftcontrol.Capabilities{}}
}

// open authenticates one bookmark into a fresh session.
func (c *Connections) open(ctx context.Context, bookmarkID string) (cachedSession, error) {
	config, generation, err := c.store.ConnectionConfig(ctx, bookmarkID)
	if err != nil {
		// The two are different facts: one store cannot be confirmed, the
		// other has no key. Neither message may claim the key was erased.
		if errors.Is(err, secrets.ErrAuthorityUnreachable) {
			return cachedSession{}, fmt.Errorf("the credential store holding this key cannot be reached; unlock or reconnect it, or enter the key again: %w", err)
		}
		if errors.Is(err, secrets.ErrNotFound) {
			return cachedSession{}, fmt.Errorf("no saved key is available; enter it again: %w", err)
		}
		return cachedSession{}, err
	}
	config.AdmitRequest = func(ctx context.Context) error {
		return c.store.WithCurrentGeneration(ctx, bookmarkID, generation, nil)
	}
	if c.connectionCap != nil {
		config.MaxConnsPerHost = c.connectionCap(ctx)
	}
	session, err := swiftcontrol.New(config)
	if err != nil {
		return cachedSession{}, err
	}
	if err := session.Authenticate(ctx); err != nil {
		return cachedSession{}, err
	}
	if err := c.store.WithCurrentGeneration(ctx, bookmarkID, generation, nil); err != nil {
		return cachedSession{}, err
	}
	return cachedSession{session: session, generation: generation}, nil
}

// Connect makes the bookmark the active session. A session already cached
// under the bookmark's current generation is reused, with the /info read
// when it was first connected, so switching back signs in again only after
// the bookmark changed (ADR 0110 decision 1). A new session is cached only
// once its capabilities are read and its generation is checked, so a
// connect that fails part way leaves nothing behind.
func (c *Connections) Connect(ctx context.Context, bookmarkID string) error {
	_, err := c.connect(ctx, bookmarkID)
	return err
}

// connect is Connect returning how to undo it: undo puts back the session
// that was active before, if this connect's is still the active one and the
// earlier one is still cached, so a caller whose first read on the new
// session fails can leave the active session as it found it (ADR 0110
// decision 1).
func (c *Connections) connect(ctx context.Context, bookmarkID string) (undo func(), err error) {
	c.mu.Lock()
	entry, cached := c.sessions[bookmarkID]
	capabilities := c.discovered[entry.session]
	c.mu.Unlock()
	if cached {
		err := c.guardCache(ctx, bookmarkID, entry.generation, func() error {
			if c.sessions[bookmarkID] != entry {
				return bookmarks.ErrChanged
			}
			return nil
		})
		switch {
		case errors.Is(err, bookmarks.ErrChanged):
			// Changed since it was cached: connect afresh, as the first
			// connect would.
			c.forgetObsolete(bookmarkID, entry, err)
			cached, capabilities = false, nil
		case err != nil:
			return nil, err
		}
	}
	if !cached {
		opened, err := c.open(ctx, bookmarkID)
		if err != nil {
			return nil, err
		}
		entry = opened
	}
	if capabilities == nil {
		// Capability discovery at connect (§10): what this cluster supports
		// gates what the UI offers. An unreadable /info yields the Unknown
		// mode, never an error — Unknown is not Unsupported.
		if capabilities, err = entry.session.FetchCapabilities(ctx); err != nil {
			return nil, err
		}
	}

	err = c.guardCache(ctx, bookmarkID, entry.generation, func() error {
		previous, previousID, previousCapabilities := c.active, c.activeID, c.capabilities
		c.active = entry
		c.activeID = bookmarkID
		c.capabilities = capabilities
		c.sessions[bookmarkID] = entry
		c.discovered[entry.session] = capabilities
		undo = func() {
			c.mu.Lock()
			defer c.mu.Unlock()
			if c.active != entry || c.activeID != bookmarkID {
				return
			}
			if previousID != "" && c.sessions[previousID] == previous {
				c.active, c.activeID, c.capabilities = previous, previousID, previousCapabilities
				return
			}
			c.active, c.activeID, c.capabilities = cachedSession{}, "", nil
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return undo, nil
}

// SessionFor returns a live session for the bookmark, authenticating on
// first use and caching it (transfers hold sessions for BOTH ends —
// neither needs to be the active browse connection).
func (c *Connections) SessionFor(ctx context.Context, bookmarkID string) (*swiftcontrol.Session, error) {
	entry, err := c.sessionEntryFor(ctx, bookmarkID)
	return entry.session, err
}

func (c *Connections) sessionEntryFor(ctx context.Context, bookmarkID string) (cachedSession, error) {
	c.mu.Lock()
	entry, found := c.sessions[bookmarkID]
	c.mu.Unlock()
	if found {
		err := c.guardCache(ctx, bookmarkID, entry.generation, func() error {
			if c.sessions[bookmarkID] != entry {
				return bookmarks.ErrChanged
			}
			return nil
		})
		if err != nil {
			c.forgetObsolete(bookmarkID, entry, err)
			return cachedSession{}, err
		}
		return entry, nil
	}

	entry, err := c.open(ctx, bookmarkID)
	if err != nil {
		return cachedSession{}, err
	}
	if err := c.guardCache(ctx, bookmarkID, entry.generation, func() error {
		c.sessions[bookmarkID] = entry
		return nil
	}); err != nil {
		return cachedSession{}, err
	}
	return entry, nil
}

// Pin before resolving a job's sessions or doing its preflight, so every
// later acquisition must still use those bindings through queueing/cleanup.
func (c *Connections) pinForJob(ctx context.Context, bookmarkIDs ...string) (func(), error) {
	var pins []func()
	release := func() {
		for _, unpin := range pins {
			unpin()
		}
	}
	seen := make(map[string]bool)
	for _, id := range bookmarkIDs {
		if seen[id] {
			continue
		}
		seen[id] = true
		entry, err := c.sessionEntryFor(ctx, id)
		if err != nil {
			release()
			return nil, err
		}
		unpin, err := c.store.PinGeneration(ctx, id, entry.generation)
		if err != nil {
			release()
			return nil, err
		}
		pins = append(pins, unpin)
	}
	return release, nil
}

// The store coordinator stays held across the cache action, so an edit
// cannot publish and invalidate between the recheck and installation.
func (c *Connections) guardCache(ctx context.Context, id string, generation int64, use func() error) error {
	return c.store.WithCurrentGeneration(ctx, id, generation, func() error {
		c.mu.Lock()
		defer c.mu.Unlock()
		return use()
	})
}

func (c *Connections) forgetObsolete(id string, entry cachedSession, cause error) {
	if !errors.Is(cause, bookmarks.ErrChanged) && !errors.Is(cause, bookmarks.ErrNotFound) {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	// A failed lookup must not evict a newer connection installed while
	// this caller was waiting to validate its older snapshot.
	if c.sessions[id] == entry {
		delete(c.discovered, entry.session)
		delete(c.sessions, id)
	}
	if c.activeID == id && c.active == entry {
		c.active = cachedSession{}
		c.activeID = ""
		c.capabilities = nil
	}
}

// activeEndpoint reports the connection endpoint as it stands, without
// validating it. activeSnapshot reports ErrNotConnected both for nothing
// being connected and for a snapshot that changed under it, and those
// mean opposite things to a pick: the second can leave another account
// connected. Reading the fields under the lock keeps absence honest
// (ADR 0041).
func (c *Connections) activeEndpoint() (string, int64, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.active.session == nil || c.activeID == "" {
		return "", 0, false
	}
	return c.activeID, c.active.generation, true
}

func (c *Connections) activeSnapshot() (cachedSession, string, *swiftcontrol.Capabilities, error) {
	c.mu.Lock()
	entry, id := c.active, c.activeID
	c.mu.Unlock()
	if entry.session == nil {
		return cachedSession{}, "", nil, ErrNotConnected
	}
	var capabilities *swiftcontrol.Capabilities
	err := c.guardCache(context.Background(), id, entry.generation, func() error {
		if c.active != entry || c.activeID != id {
			return ErrNotConnected
		}
		capabilities = c.capabilities
		return nil
	})
	if err != nil {
		c.forgetObsolete(id, entry, err)
		return cachedSession{}, "", nil, err
	}
	return entry, id, capabilities, nil
}

// Invalidate drops any cached session (and, when it is the active
// browse connection, the cached capabilities) for a bookmark whose
// stored configuration changed or which was deleted — the next use
// re-reads the store instead of operating on the old account (§65.9
// item 15, review H-12).
func (c *Connections) Invalidate(bookmarkID string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.discovered, c.sessions[bookmarkID].session)
	delete(c.sessions, bookmarkID)
	if c.activeID == bookmarkID {
		// The active browse pointer is the SAME staleness: drop it too,
		// so Session() answers nil and the UI reconnects against the
		// stored configuration instead of the old cluster.
		c.active = cachedSession{}
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

// EngineRemote carries the captured credentials, trust and admission check
// through CA materialisation and the gateway's later upstream requests.
func (c *Connections) EngineRemote(ctx context.Context, session *swiftcontrol.Session, name string) (rclone.Remote, error) {
	credentials, err := session.Credentials(ctx, tokenRefreshMargin)
	if err != nil {
		return rclone.Remote{}, err
	}
	insecure, caFile, err := c.TLSRemote(credentials.TLS)
	if err != nil {
		return rclone.Remote{}, err
	}
	return rclone.Remote{Name: name, StorageURL: credentials.StorageURL, Token: credentials.Token,
		InsecureTLS: insecure, CACertFile: caFile, AdmitRequest: session.AdmitRequest}, nil
}

// TLSRemote materialises the captured trust without consulting bookmarks.
// Publishing another bundle must not replace a running child's CA file.
func (c *Connections) TLSRemote(tls swiftcontrol.TLSConfig) (insecure bool, caFile string, err error) {
	switch tls.Mode {
	case swiftcontrol.TLSModeInsecure:
		return true, "", nil
	case swiftcontrol.TLSModeCustomCA:
		c.mu.Lock()
		dir := c.caDir
		c.mu.Unlock()
		// Keep concurrent materialisation ordered without holding the
		// cache mutex across file I/O and blocking bookmark publication.
		c.caMu.Lock()
		defer c.caMu.Unlock()
		if dir == "" {
			dir = filepath.Join(os.TempDir(), "aeronautes-ca")
		}
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return false, "", err
		}
		path := filepath.Join(dir, fmt.Sprintf("%x.pem", sha256.Sum256(tls.CACertPEM)))
		if stored, err := os.ReadFile(path); err == nil {
			if !bytes.Equal(stored, tls.CACertPEM) {
				return false, "", errors.New("materialised CA differs from the session trust")
			}
			return false, path, nil
		} else if !os.IsNotExist(err) {
			return false, "", err
		}
		file, err := os.CreateTemp(dir, ".ca-*.pem")
		if err != nil {
			return false, "", err
		}
		defer os.Remove(file.Name())
		_, writeErr := file.Write(tls.CACertPEM)
		closeErr := file.Close()
		if err := errors.Join(writeErr, closeErr); err != nil {
			return false, "", err
		}
		if err := os.Rename(file.Name(), path); err != nil {
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
	_, _, capabilities, _ := c.activeSnapshot()
	return capabilities
}

// Session returns the active session.
func (c *Connections) Session() (*swiftcontrol.Session, error) {
	entry, _, _, err := c.activeSnapshot()
	return entry.session, err
}

// scopedSession binds a live Swift read to the bookmark the caller named.
// The generation is resolved from the store rather than taken from the
// connected endpoint, matching ADR 0049: a bookmark re-saved to another
// account without reconnecting is exactly what a cached endpoint cannot
// see. A mismatch refuses rather than answering from whichever connection
// happens to be active (ADR 0049, ADR 0059).
func (c *Connections) scopedSession(ctx context.Context, bookmarkID string) (*swiftcontrol.Session, ListingScopeDTO, error) {
	if bookmarkID == "" {
		return nil, ListingScopeDTO{}, fmt.Errorf("%w: name the bookmark the read is for", ErrNotConnected)
	}
	activeID, connectedGeneration, connected := c.activeEndpoint()
	if !connected {
		return nil, ListingScopeDTO{}, ErrNotConnected
	}
	if activeID != bookmarkID {
		return nil, ListingScopeDTO{}, ErrAccountScopeChanged
	}
	entry, err := c.sessionEntryFor(ctx, bookmarkID)
	if err != nil {
		return nil, ListingScopeDTO{}, err
	}
	if entry.generation != connectedGeneration {
		return nil, ListingScopeDTO{}, ErrAccountScopeChanged
	}
	// The validated entry is this read's session (ADR 0066). Resolving the
	// active session again here could return a connection that replaced
	// this one after the checks above, under this reply's earlier scope.
	return entry.session, ListingScopeDTO{BookmarkID: bookmarkID, Generation: entry.generation}, nil
}

// ActiveBookmarkID returns the connected bookmark's id, "" when none —
// the scope key for evidence and other per-endpoint state.
func (c *Connections) ActiveBookmarkID() string {
	_, id, _, _ := c.activeSnapshot()
	return id
}

// displayName is the stored display name of a bookmark, or empty when the
// bookmark cannot be read; it is display text for a native surface, never
// authority.
func (c *Connections) displayName(ctx context.Context, bookmarkID string) string {
	b, err := c.store.Get(ctx, bookmarkID)
	if err != nil {
		return ""
	}
	return b.DisplayName
}

// AccountName derives the display account name from the active session's
// storage URL suffix (…/v1/AUTH_demo → AUTH_demo).
func (c *Connections) AccountName() string {
	session, err := c.Session()
	if err != nil {
		return ""
	}
	url := session.StorageURL()
	if i := strings.LastIndex(url, "/"); i >= 0 {
		return url[i+1:]
	}
	return url
}
