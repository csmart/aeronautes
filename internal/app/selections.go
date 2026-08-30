package app

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"sync"
)

// selectionStore holds picker-issued filesystem selections behind
// opaque tokens (ADR 0011: paths never originate in — or round-trip
// through — the webview). Session-lifetime, bounded.
type selectionStore struct {
	mu    sync.Mutex
	files map[string][]string
	dirs  map[string]string
	order []string
}

const maxSelections = 64

func (s *selectionStore) token() string {
	var raw [16]byte
	_, _ = rand.Read(raw[:])
	return hex.EncodeToString(raw[:])
}

func (s *selectionStore) evict() {
	for len(s.order) > maxSelections {
		old := s.order[0]
		s.order = s.order[1:]
		delete(s.files, old)
		delete(s.dirs, old)
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

func (s *selectionStore) putDir(dir string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.dirs == nil {
		s.dirs = map[string]string{}
	}
	t := s.token()
	s.dirs[t] = dir
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

func (s *selectionStore) getDir(token string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	dir, ok := s.dirs[token]
	if !ok {
		return "", errors.New("unknown or expired directory selection — pick the directory again")
	}
	return dir, nil
}
