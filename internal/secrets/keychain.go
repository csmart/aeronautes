package secrets

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"time"
)

// service is the keychain namespace all Aeronautes entries live under.
const service = "aeronautes"

// openDefaultTimeout is how long OpenDefault waits for the keychain
// probe before selecting the session store (ADR 0055 decision 5).
const openDefaultTimeout = 10 * time.Second

// KeychainStore is the OS-keychain SecretStore (§12.1, ADR 0012).
// Values are base64-wrapped so arbitrary bytes survive the string-shaped
// keyring API. pin is the Linux collection object path used for later
// operations; empty on other platforms.
type KeychainStore struct {
	origin Origin
	pin    string
}

// NewKeychainStore probes the OS keychain with a write/read/delete
// round-trip and returns the store only when the keychain actually
// works — a present-but-locked or absent backend fails here so the
// caller can fall back visibly (never silently).
func NewKeychainStore() (*KeychainStore, error) {
	return newKeychainStore(context.Background())
}

func (s *KeychainStore) Origin() Origin { return s.origin }

func probeName() (string, error) {
	var nonce [8]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return "", err
	}
	return "probe-" + hex.EncodeToString(nonce[:]), nil
}

func decodeKeychainValue(encoded string) ([]byte, error) {
	value, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return nil, fmt.Errorf("keychain entry is not aeronautes-encoded: %w", err)
	}
	return value, nil
}

type nativeResult struct {
	value string
	err   error
}

func awaitKeychain(ctx context.Context, op string, run func() (string, error)) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", boundKeychainError(op, err)
	}
	done := make(chan nativeResult, 1)
	go func() {
		value, err := run()
		done <- nativeResult{value: value, err: err}
	}()
	select {
	case r := <-done:
		return r.value, r.err
	case <-ctx.Done():
		return "", boundKeychainError(op, ctx.Err())
	}
}

func boundKeychainError(op string, err error) error {
	if op == "read" {
		return fmt.Errorf("keychain read: %w", err)
	}
	return fmt.Errorf("keychain %s unresolved: %w", op, errors.Join(ErrUnresolved, err))
}

func readBackError(err error) error {
	if errors.Is(err, ErrUnresolved) || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return fmt.Errorf("keychain write read-back unresolved: %w", errors.Join(ErrUnresolved, err))
	}
	return errors.New("keychain write read-back failed")
}

// Backend names the active secret backend for display (§12.1: the UI
// must say when credentials are session-only).
type Backend string

const (
	BackendKeychain Backend = "keychain"
	BackendSession  Backend = "session"
)

// OpenDefault returns the best available SecretStore: the OS keychain
// when it probes healthy, otherwise the in-memory session store. The
// returned Backend is for the UI's visible warning, never a silent
// downgrade (§12.1).
func OpenDefault() (SecretStore, Backend) {
	ctx, cancel := context.WithTimeout(context.Background(), openDefaultTimeout)
	defer cancel()
	if store, err := newKeychainStore(ctx); err == nil {
		return store, BackendKeychain
	}
	return NewSessionStore(), BackendSession
}
