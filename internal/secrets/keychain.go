package secrets

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/zalando/go-keyring"
)

// service is the keychain namespace all Aeronautes entries live under.
const service = "aeronautes"

// openDefaultTimeout is how long OpenDefault waits for the keychain
// probe before selecting the session store (ADR 0055 decision 5).
const openDefaultTimeout = 10 * time.Second

// KeychainStore is the OS-keychain SecretStore (§12.1, ADR 0012): the
// Secret Service on Linux, Credential Manager on Windows, Keychain on
// macOS, via zalando/go-keyring. Values are base64-wrapped so arbitrary
// bytes survive the string-shaped keyring API.
type KeychainStore struct{ origin Origin }

// NewKeychainStore probes the OS keychain with a write/read/delete
// round-trip and returns the store only when the keychain actually
// works — a present-but-locked or absent backend fails here so the
// caller can fall back visibly (never silently).
func NewKeychainStore() (*KeychainStore, error) {
	return newKeychainStore(context.Background())
}

func newKeychainStore(ctx context.Context) (*KeychainStore, error) {
	var nonce [8]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return nil, err
	}
	probe := "probe-" + hex.EncodeToString(nonce[:])
	if err := setKeychainValue(ctx, probe, "probe"); err != nil {
		_ = awaitKeychain(context.Background(), "delete", func() error {
			return keyring.Delete(service, probe)
		})
		return nil, fmt.Errorf("keychain unavailable: %w", err)
	}
	if err := awaitKeychain(ctx, "delete", func() error {
		return keyring.Delete(service, probe)
	}); err != nil {
		return nil, fmt.Errorf("keychain delete failed: %w", err)
	}
	err := awaitKeychain(ctx, "read", func() error {
		_, err := keyring.Get(service, probe)
		return err
	})
	if !errors.Is(err, keyring.ErrNotFound) {
		if err == nil {
			return nil, errors.New("keychain probe deletion could not be verified")
		}
		return nil, fmt.Errorf("keychain probe verify: %w", err)
	}
	return &KeychainStore{origin: newOrigin(BackendKeychain)}, nil
}

func (s *KeychainStore) Origin() Origin { return s.origin }

// Get returns the named secret.
func (*KeychainStore) Get(ctx context.Context, name string) ([]byte, error) {
	var encoded string
	err := awaitKeychain(ctx, "read", func() error {
		value, err := keyring.Get(service, name)
		encoded = value
		return err
	})
	if errors.Is(err, keyring.ErrNotFound) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	value, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return nil, fmt.Errorf("keychain entry is not aeronautes-encoded: %w", err)
	}
	return value, nil
}

// Put stores the named secret, replacing any existing value.
func (*KeychainStore) Put(ctx context.Context, name string, value []byte) error {
	return setKeychainValue(ctx, name, base64.StdEncoding.EncodeToString(value))
}

func setKeychainValue(ctx context.Context, name, value string) error {
	// Native error text may carry the key or reference being verified.
	if err := awaitKeychain(ctx, "write", func() error {
		if err := keyring.Set(service, name, value); err != nil {
			return errors.New("keychain write failed")
		}
		return nil
	}); err != nil {
		return err
	}
	// The pinned client can return nil after a dismissed creation prompt.
	// Publication must not select that reference without reading its value.
	var stored string
	err := awaitKeychain(ctx, "read", func() error {
		value, err := keyring.Get(service, name)
		stored = value
		return err
	})
	if err != nil {
		if errors.Is(err, ErrUnresolved) || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return fmt.Errorf("keychain write read-back unresolved: %w", errors.Join(ErrUnresolved, err))
		}
		return errors.New("keychain write read-back failed")
	}
	if stored != value {
		return errors.New("keychain write read-back did not match")
	}
	return nil
}

// Delete removes the named secret.
func (*KeychainStore) Delete(ctx context.Context, name string) error {
	err := awaitKeychain(ctx, "delete", func() error {
		return keyring.Delete(service, name)
	})
	if errors.Is(err, keyring.ErrNotFound) {
		return ErrNotFound
	}
	return err
}

func awaitKeychain(ctx context.Context, op string, run func() error) error {
	if err := ctx.Err(); err != nil {
		return boundKeychainError(op, err)
	}
	done := make(chan error, 1)
	go func() { done <- run() }()
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		return boundKeychainError(op, ctx.Err())
	}
}

func boundKeychainError(op string, err error) error {
	if op == "read" {
		return fmt.Errorf("keychain read: %w", err)
	}
	return fmt.Errorf("keychain %s unresolved: %w", op, errors.Join(ErrUnresolved, err))
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
