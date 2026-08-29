package secrets

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"

	"github.com/zalando/go-keyring"
)

// service is the keychain namespace all Aeronautes entries live under.
const service = "aeronautes"

// KeychainStore is the OS-keychain SecretStore (§12.1, ADR 0012): the
// Secret Service on Linux, Credential Manager on Windows, Keychain on
// macOS, via zalando/go-keyring. Values are base64-wrapped so arbitrary
// bytes survive the string-shaped keyring API.
type KeychainStore struct{}

// NewKeychainStore probes the OS keychain with a write/read/delete
// round-trip and returns the store only when the keychain actually
// works — a present-but-locked or absent backend fails here so the
// caller can fall back visibly (never silently).
func NewKeychainStore() (*KeychainStore, error) {
	var nonce [8]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return nil, err
	}
	probe := "probe-" + hex.EncodeToString(nonce[:])
	if err := keyring.Set(service, probe, "probe"); err != nil {
		return nil, fmt.Errorf("keychain unavailable: %w", err)
	}
	if _, err := keyring.Get(service, probe); err != nil {
		_ = keyring.Delete(service, probe)
		return nil, fmt.Errorf("keychain read-back failed: %w", err)
	}
	if err := keyring.Delete(service, probe); err != nil {
		return nil, fmt.Errorf("keychain delete failed: %w", err)
	}
	return &KeychainStore{}, nil
}

// Get returns the named secret.
func (*KeychainStore) Get(_ context.Context, name string) ([]byte, error) {
	encoded, err := keyring.Get(service, name)
	if errors.Is(err, keyring.ErrNotFound) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	value, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return nil, fmt.Errorf("keychain entry %q is not aeronautes-encoded: %w", name, err)
	}
	return value, nil
}

// Put stores the named secret, replacing any existing value.
func (*KeychainStore) Put(_ context.Context, name string, value []byte) error {
	return keyring.Set(service, name, base64.StdEncoding.EncodeToString(value))
}

// Delete removes the named secret.
func (*KeychainStore) Delete(_ context.Context, name string) error {
	err := keyring.Delete(service, name)
	if errors.Is(err, keyring.ErrNotFound) {
		return ErrNotFound
	}
	return err
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
	if store, err := NewKeychainStore(); err == nil {
		return store, BackendKeychain
	}
	return NewSessionStore(), BackendSession
}
