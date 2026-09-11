package app

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"sync"

	"github.com/csmart/aeronautes/internal/transfer/local"
)

// selectionStore holds picker-issued filesystem selections behind
// opaque tokens (ADR 0011: paths never originate in — or round-trip
// through — the webview). Session-lifetime, bounded. A directory
// selection is held as an open handle from the pick, so the token names
// the directory picked rather than whatever its pathname names later.
type selectionStore struct {
	mu    sync.Mutex
	files map[string][]string
	dirs  map[string]dirSelection
	order []string
}

// dirSelection keeps the exact picker spelling beside the handle. A pick
// that cannot be held keeps its spelling with no handle, so the complete
// preflight report can say why it is refused, exactly as before.
type dirSelection struct {
	path string
	root *local.DestinationRoot
}

const maxSelections = 64

func (s *selectionStore) token() string {
	var raw [16]byte
	_, _ = rand.Read(raw[:])
	return hex.EncodeToString(raw[:])
}

// evict drops the oldest selections. An evicted directory handle closes
// here; handles derived from it for jobs are owned by those jobs and
// stay open. A close failure has no consumer at eviction.
func (s *selectionStore) evict() {
	for len(s.order) > maxSelections {
		old := s.order[0]
		s.order = s.order[1:]
		delete(s.files, old)
		if dir, ok := s.dirs[old]; ok {
			if dir.root != nil {
				_ = dir.root.Close()
			}
			delete(s.dirs, old)
		}
	}
}

func (s *selectionStore) putFiles(paths []string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.files == nil {
		s.files = map[string][]string{}
	}
	t := s.token()
	s.files[t] = paths
	s.order = append(s.order, t)
	s.evict()
	return t
}

// putDir captures the picked directory behind a handle at the pick. The
// spelling is kept unchanged either way; a directory that cannot be held
// is refused with its reason by the preflight report.
func (s *selectionStore) putDir(dir string) string {
	selection := dirSelection{path: dir}
	if root, err := local.OpenDestinationRoot(dir); err == nil {
		selection.root = root
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.dirs == nil {
		s.dirs = map[string]dirSelection{}
	}
	t := s.token()
	s.dirs[t] = selection
	s.order = append(s.order, t)
	s.evict()
	return t
}

func (s *selectionStore) getFiles(token string) ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	paths, ok := s.files[token]
	if !ok {
		return nil, errors.New("unknown or expired file selection — pick the files again")
	}
	return paths, nil
}

// getDir returns the exact picker spelling for display and records.
func (s *selectionStore) getDir(token string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	dir, ok := s.dirs[token]
	if !ok {
		return "", errors.New("unknown or expired directory selection — pick the directory again")
	}
	return dir.path, nil
}

// leaseDir derives a handle on the selected directory that the caller
// owns until it closes it, beside the exact spelling. One pick can start
// any number of jobs, each with its own handle, and evicting the token
// never touches them. A pick that could not be held yields no handle.
func (s *selectionStore) leaseDir(token string) (*local.DestinationRoot, string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	dir, ok := s.dirs[token]
	if !ok {
		return nil, "", errors.New("unknown or expired directory selection — pick the directory again")
	}
	if dir.root == nil {
		return nil, dir.path, nil
	}
	derived, err := dir.root.Derive()
	if err != nil {
		return nil, dir.path, err
	}
	return derived, dir.path, nil
}
