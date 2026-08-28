package secrets

import (
	"context"
	"sync"
)

// SessionStore keeps secrets in process memory only — the last-resort
// backend when no OS secret service is available and the operator declines
// the vault (§12.1). Everything in it is gone when Aeronautes exits; the
// UI must present credentials held here as session-only.
type SessionStore struct {
	mu     sync.RWMutex
	values map[string][]byte
}

// NewSessionStore returns an empty session-only store.
func NewSessionStore() *SessionStore {
	return &SessionStore{values: map[string][]byte{}}
}

func (s *SessionStore) Get(_ context.Context, name string) ([]byte, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	v, ok := s.values[name]
	if !ok {
		return nil, ErrNotFound
	}
	// Copy so a caller cannot mutate the stored value through the slice.
	out := make([]byte, len(v))
	copy(out, v)
	return out, nil
}

func (s *SessionStore) Put(_ context.Context, name string, value []byte) error {
	stored := make([]byte, len(value))
	copy(stored, value)
	s.mu.Lock()
	defer s.mu.Unlock()
	if old, ok := s.values[name]; ok {
		zero(old)
	}
	s.values[name] = stored
	return nil
}

func (s *SessionStore) Delete(_ context.Context, name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.values[name]
	if !ok {
		return ErrNotFound
	}
	zero(v)
	delete(s.values, name)
	return nil
}

// Wipe zeroes and drops every stored secret. Call on shutdown; best-effort
// hygiene only — Go gives no guarantee about copies the GC has moved.
func (s *SessionStore) Wipe() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for name, v := range s.values {
		zero(v)
		delete(s.values, name)
	}
}

func zero(b []byte) {
	for i := range b {
		b[i] = 0
	}
}
