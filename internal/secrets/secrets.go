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

// ErrAuthorityUnreachable reports that the authority recorded for a
// reference cannot be confirmed by the active backend. It never proves
// that the value was removed, so it must not satisfy ErrNotFound.
var ErrAuthorityUnreachable = errors.New("credential authority is unreachable")

// ErrUnresolved reports that a native credential write or delete was
// abandoned while the OS keychain may still complete it (ADR 0055).
// It must not be reported as "did not happen".
var ErrUnresolved = errors.New("credential operation unresolved")

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

// Origin is internal routing metadata. Instance is an adapter lifetime.
// Persistent is a resolved native collection path on Linux; empty means
// unverified (session, legacy, non-Linux, or unresolved alias).
type Origin struct {
	Kind       Backend
	Instance   string
	Persistent string
}

func newOrigin(kind Backend) Origin {
	return Origin{Kind: kind, Instance: rand.Text()}
}

// VerifiedBy reports whether active is the exact authority that recorded
// this origin. A live session store matches its own instance. A keychain
// origin matches only a non-empty Persistent equal to the active store's.
// Empty Persistent, including legacy and unresolved alias, is never verified.
// Absence reported by an unverified authority is not erasure evidence.
func (o Origin) VerifiedBy(active Origin) bool {
	if o.Kind == BackendSession {
		return o.Instance != "" && o == active
	}
	return o.Kind == BackendKeychain && o.Persistent != "" &&
		active.Kind == BackendKeychain && o.Persistent == active.Persistent
}

// ReadableBy is permission to attempt a read, not a reachability claim.
// A keychain origin with empty Persistent still admits any keychain
// adapter. A recorded Persistent admits only the same collection path.
func (o Origin) ReadableBy(active Origin) bool {
	switch o.Kind {
	case "":
		return o.Instance == "" && o.Persistent == ""
	case BackendSession:
		return o.Instance != "" && o == active
	case BackendKeychain:
		if active.Kind != BackendKeychain {
			return false
		}
		if o.Persistent != "" {
			return o.Persistent == active.Persistent
		}
		return o.Instance != "" && active.Instance != ""
	default:
		return false
	}
}
