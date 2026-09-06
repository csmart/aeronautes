// Package secrets is the only place credentials live (K-SEC-003): Swift
// auth keys and passwords are stored behind SecretStore and referenced
// everywhere else by name (bookmarks.secret_ref). Secrets never reach
// SQLite or the frontend (K-SEC-002), and the store never logs values.
//
// Backends, in the DESIGN.md §12.1 priority order: the OS secret service
// (Secret Service / Keychain / Credential Manager), the opt-in encrypted
// vault (ADR 0006, unresolved), and the session-only in-memory store in
// this package. Each backend is its own commit; this package establishes
// the contract and the fallback of last resort.
package secrets

import (
	"context"
	"crypto/rand"
	"errors"
)

// ErrNotFound is returned by Get and Delete when no secret has the name.
var ErrNotFound = errors.New("secret not found")

// SecretStore stores credential material by name. Names are opaque,
// stable identifiers such as "bookmark/<id>"; values are the raw secret
// bytes. Implementations must be safe for concurrent use and must never
// write values to logs, SQLite, or the frontend.
type SecretStore interface {
	// Origin identifies this adapter lifetime, not a verified native store.
	Origin() Origin
	// Get returns the secret's value, or ErrNotFound.
	Get(ctx context.Context, name string) ([]byte, error)
	// Put stores or replaces the secret.
	Put(ctx context.Context, name string, value []byte) error
	// Delete removes the secret; ErrNotFound if it does not exist.
	Delete(ctx context.Context, name string) error
}

// Origin is internal routing metadata. A keychain adapter can follow a
// changed OS default, so its instance does not prove persistent authority.
type Origin struct {
	Kind     Backend
	Instance string
}

func newOrigin(kind Backend) Origin {
	return Origin{Kind: kind, Instance: rand.Text()}
}

// ReadableBy preserves legacy reads but prevents a fresh session fallback
// from impersonating the memory store that accepted a recorded value.
func (o Origin) ReadableBy(active Origin) bool {
	switch o.Kind {
	case "":
		return o.Instance == ""
	case BackendSession:
		return o.Instance != "" && o == active
	case BackendKeychain:
		return o.Instance != "" && active.Kind == BackendKeychain && active.Instance != ""
	default:
		return false
	}
}
