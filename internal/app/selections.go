package app

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"sync"
	"time"

	"github.com/csmart/aeronautes/internal/transfer/local"
)

// selectionStore holds picker-issued filesystem selections behind opaque
// tokens (ADR 0011: paths never originate in — or round-trip through —
// the webview). Under ADR 0041 a token is a capability rather than a
// bare handle: it names the one operation and the one scope it may be
// used in, it expires, and starting the transfer it authorises consumes
// it. A directory selection is held as an open handle from the pick, so
// the token names the directory picked rather than whatever its
// pathname names later.
type selectionStore struct {
	mu      sync.Mutex
	entries map[string]*selectionEntry
	order   []string
	// now is the clock, injected by tests to reach the expiry boundary
	// without sleeping. Nil means time.Now.
	now func() time.Time
}

// selectionOp distinguishes the two pickers. It is recorded so a
// selection cannot be presented at the boundary of the other operation.
type selectionOp string

const (
	selectionUpload   selectionOp = "upload"
	selectionDownload selectionOp = "download"
)

// selectionScope is the single scope a selection may be used in. The
// account half is read from the connection registry rather than taken
// from the caller; ADR 0041 records why that is partial protection and
// not proof of what the user approved.
type selectionScope struct {
	bookmarkID string
	generation int64
	container  string
	prefix     string
}

// selectionEntry keeps the exact picker spelling beside the handle. A
// pick that cannot be held keeps its spelling with no handle, so the
// complete preflight report can say why it is refused, exactly as
// before.
type selectionEntry struct {
	op     selectionOp
	scope  selectionScope
	issued time.Time
	files  []string
	dir    string
	root   *local.DestinationRoot
}

const (
	maxSelections = 64
	// selectionLifetime bounds a selection that is never started. It is
	// sized for a person reading a preflight report and deciding.
	selectionLifetime = 10 * time.Minute
)

// Refusals are distinct so a caller, a test and the dialog can tell an
// expiry from a scope violation, and so a deliberately broken control
// fails only its own case.
var (
	errSelectionUnknown   = errors.New("unknown selection — pick again")
	errSelectionExpired   = errors.New("the selection expired — pick again")
	errSelectionOperation = errors.New("that selection was made for a different operation — pick again")
	errSelectionAccount   = errors.New("that selection was made for a different account — pick again")
	errSelectionChanged   = errors.New("the account's credentials changed since the selection — pick again")
	errSelectionScope     = errors.New("that selection was made for a different container or prefix — pick again")
)

func (s *selectionStore) clock() time.Time {
	if s.now != nil {
		return s.now()
	}
	return time.Now()
}

func (s *selectionStore) token() string {
	var raw [16]byte
	_, _ = rand.Read(raw[:])
	return hex.EncodeToString(raw[:])
}

// drop removes one entry and closes any handle it still owns. Handles
// derived from it for jobs are owned by those jobs and stay open. A
// close failure has no consumer here.
func (s *selectionStore) drop(token string) {
	entry, ok := s.entries[token]
	if !ok {
		return
	}
	if entry.root != nil {
		_ = entry.root.Close()
	}
	delete(s.entries, token)
	for i, t := range s.order {
		if t == token {
			s.order = append(s.order[:i], s.order[i+1:]...)
			break
		}
	}
}

// sweep drops expired selections, then the oldest while over the bound.
func (s *selectionStore) sweep() {
	deadline := s.clock().Add(-selectionLifetime)
	for _, token := range append([]string(nil), s.order...) {
		if entry, ok := s.entries[token]; ok && entry.issued.Before(deadline) {
			s.drop(token)
		}
	}
	for len(s.order) > maxSelections {
		s.drop(s.order[0])
	}
}

func (s *selectionStore) put(entry *selectionEntry) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.entries == nil {
		s.entries = map[string]*selectionEntry{}
	}
	entry.issued = s.clock()
	t := s.token()
	s.entries[t] = entry
	s.order = append(s.order, t)
	s.sweep()
	return t
}

func (s *selectionStore) putFiles(scope selectionScope, paths []string) string {
	return s.put(&selectionEntry{op: selectionUpload, scope: scope, files: paths})
}

// putDir captures the picked directory behind a handle at the pick. The
// spelling is kept unchanged either way; a directory that cannot be held
// is refused with its reason by the preflight report.
func (s *selectionStore) putDir(scope selectionScope, dir string) string {
	entry := &selectionEntry{op: selectionDownload, scope: scope, dir: dir}
	if root, err := local.OpenDestinationRoot(dir); err == nil {
		entry.root = root
	}
	return s.put(entry)
}

// resolve checks the whole binding, in the order that gives the most
// specific reason. The caller holds the lock.
func (s *selectionStore) resolve(token string, op selectionOp, want selectionScope) (*selectionEntry, error) {
	entry, ok := s.entries[token]
	if !ok {
		return nil, errSelectionUnknown
	}
	if !entry.issued.After(s.clock().Add(-selectionLifetime)) {
		s.drop(token)
		return nil, errSelectionExpired
	}
	if entry.op != op {
		return nil, errSelectionOperation
	}
	if entry.scope.bookmarkID != want.bookmarkID {
		return nil, errSelectionAccount
	}
	if entry.scope.generation != want.generation {
		return nil, errSelectionChanged
	}
	if entry.scope.container != want.container || entry.scope.prefix != want.prefix {
		return nil, errSelectionScope
	}
	return entry, nil
}

// getFiles validates a file selection for a read-only report. Preflight
// may repeat; only the start consumes.
func (s *selectionStore) getFiles(token string, want selectionScope) ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	entry, err := s.resolve(token, selectionUpload, want)
	if err != nil {
		return nil, err
	}
	return entry.files, nil
}

// takeFiles validates and consumes a file selection for a start. It is
// atomic under the lock, so two concurrent starts cannot both win.
func (s *selectionStore) takeFiles(token string, want selectionScope) ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	entry, err := s.resolve(token, selectionUpload, want)
	if err != nil {
		return nil, err
	}
	paths := entry.files
	s.drop(token)
	return paths, nil
}

// getDir returns the exact picker spelling for display and records.
func (s *selectionStore) getDir(token string, want selectionScope) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	entry, err := s.resolve(token, selectionDownload, want)
	if err != nil {
		return "", err
	}
	return entry.dir, nil
}

// leaseDir derives a handle for a read-only report, beside the exact
// spelling. The caller owns the derived handle until it closes it, and
// the selection stays available for the start that follows. A pick that
// could not be held yields no handle.
func (s *selectionStore) leaseDir(token string, want selectionScope) (*local.DestinationRoot, string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.deriveLocked(token, want, false)
}

// takeDir derives the job's handle and consumes the selection, so a
// confirmed pick cannot be replayed into further jobs. The derived
// handle outlives the entry; the entry's own handle closes with it.
func (s *selectionStore) takeDir(token string, want selectionScope) (*local.DestinationRoot, string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.deriveLocked(token, want, true)
}

func (s *selectionStore) deriveLocked(token string, want selectionScope, consume bool) (*local.DestinationRoot, string, error) {
	entry, err := s.resolve(token, selectionDownload, want)
	if err != nil {
		return nil, "", err
	}
	dir := entry.dir
	if entry.root == nil {
		if consume {
			s.drop(token)
		}
		return nil, dir, nil
	}
	derived, deriveErr := entry.root.Derive()
	if consume {
		// drop closes the entry's own handle; the derived one is the
		// job's and stays open.
		s.drop(token)
	}
	if deriveErr != nil {
		return nil, dir, deriveErr
	}
	return derived, dir, nil
}
