// Package bookmarks stores saved Swift endpoints (DESIGN.md §11): the
// non-secret half in SQLite, the credential in the SecretStore under the
// name recorded as secret_ref. Whether that credential survives a restart
// is the backend's property (OS keychain vs session-only) — this package
// never sees the difference and never sees a secret column (K-SEC-003).
package bookmarks

import (
	"context"
	"crypto/rand"
	"crypto/x509"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/csmart/aeronautes/internal/db"
	"github.com/csmart/aeronautes/internal/secrets"
	"github.com/csmart/aeronautes/internal/swiftcontrol"
)

// ErrNotFound is returned when no bookmark has the id.
var ErrNotFound = errors.New("bookmark not found")

// SegmentPlacement is the destination large-object placement (§11, §26.4):
// rclone honours container-vs-sidecar, never an arbitrary per-upload prefix.
type SegmentPlacement string

const (
	PlacementRcloneDefault SegmentPlacement = "rclone_default"
	PlacementSidecar       SegmentPlacement = "sidecar"
	PlacementInContainer   SegmentPlacement = "in_container"
)

// Bookmark is one saved endpoint. Secret values never live here.
type Bookmark struct {
	ID                 string
	DisplayName        string
	AuthURL            string
	Username           string
	StorageURLOverride string
	TLSMode            swiftcontrol.TLSMode
	TLSCAID            string
	PerformanceProfile string
	SegmentPlacement   SegmentPlacement
	// ChunkSizeBytes 0 means the default: 5 GiB capped at the cluster's
	// max_file_size (§25).
	ChunkSizeBytes int64
	// HasCredential reports whether a credential is on record for this
	// bookmark (secret_ref set).
	HasCredential bool
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

// SwiftConfig assembles the connection config for this bookmark. The API
// key comes from Credential; caPEM is the CA bundle for TLSModeCustomCA
// (the CA store resolves TLSCAID to PEM).
func (b Bookmark) SwiftConfig(apiKey, caPEM []byte) swiftcontrol.Config {
	return swiftcontrol.Config{
		AuthURL:            b.AuthURL,
		Username:           b.Username,
		APIKey:             string(apiKey),
		StorageURLOverride: b.StorageURLOverride,
		TLS:                swiftcontrol.TLSConfig{Mode: b.TLSMode, CACertPEM: caPEM},
	}
}

// Store persists bookmarks.
type Store struct {
	db      *db.DB
	secrets secrets.SecretStore
}

// NewStore builds a Store over the state database and the active secret
// backend.
func NewStore(d *db.DB, s secrets.SecretStore) *Store {
	return &Store{db: d, secrets: s}
}

// Save inserts (empty ID) or updates a bookmark. A non-nil secret is
// stored in the SecretStore under bookmark/<id>; nil keeps any existing
// credential. The saved bookmark is returned with ID and timestamps set.
func (st *Store) Save(ctx context.Context, b Bookmark, secret []byte) (Bookmark, error) {
	if err := validate(b); err != nil {
		return Bookmark{}, err
	}

	now := time.Now().UTC().Truncate(time.Second)
	isNew := b.ID == ""
	if isNew {
		b.ID = newID()
		b.CreatedAt = now
	}
	b.UpdatedAt = now
	if b.SegmentPlacement == "" {
		b.SegmentPlacement = PlacementRcloneDefault
	}
	if b.PerformanceProfile == "" {
		b.PerformanceProfile = "inherit"
	}
	if b.TLSMode == "" {
		b.TLSMode = swiftcontrol.TLSModeSystem
	}

	if secret != nil {
		if err := st.secrets.Put(ctx, secretName(b.ID), secret); err != nil {
			return Bookmark{}, fmt.Errorf("store credential: %w", err)
		}
		b.HasCredential = true
	}

	err := st.db.WriteTx(ctx, func(tx *sql.Tx) error {
		if isNew {
			var ref *string
			if b.HasCredential {
				r := secretName(b.ID)
				ref = &r
			}
			_, err := tx.ExecContext(ctx, `
				INSERT INTO bookmarks (id, display_name, auth_url, username,
					storage_url_override, secret_ref, tls_mode, tls_ca_id,
					performance_profile, segment_placement, chunk_size_bytes,
					created_at, updated_at)
				VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
				b.ID, b.DisplayName, b.AuthURL, b.Username,
				nullable(b.StorageURLOverride), ref, string(b.TLSMode), nullable(b.TLSCAID),
				b.PerformanceProfile, string(b.SegmentPlacement), nullableInt(b.ChunkSizeBytes),
				b.CreatedAt.Format(time.RFC3339), b.UpdatedAt.Format(time.RFC3339))
			return err
		}

		res, err := tx.ExecContext(ctx, `
			UPDATE bookmarks SET display_name=?, auth_url=?, username=?,
				storage_url_override=?, tls_mode=?, tls_ca_id=?,
				performance_profile=?, segment_placement=?, chunk_size_bytes=?,
				secret_ref=CASE WHEN ? THEN ? ELSE secret_ref END,
				updated_at=?
			WHERE id=?`,
			b.DisplayName, b.AuthURL, b.Username,
			nullable(b.StorageURLOverride), string(b.TLSMode), nullable(b.TLSCAID),
			b.PerformanceProfile, string(b.SegmentPlacement), nullableInt(b.ChunkSizeBytes),
			secret != nil, secretName(b.ID),
			b.UpdatedAt.Format(time.RFC3339),
			b.ID)
		if err != nil {
			return err
		}
		n, err := res.RowsAffected()
		if err != nil {
			return err
		}
		if n == 0 {
			return ErrNotFound
		}
		return nil
	})
	if err != nil {
		return Bookmark{}, err
	}
	return st.Get(ctx, b.ID)
}

// Get returns one bookmark.
func (st *Store) Get(ctx context.Context, id string) (Bookmark, error) {
	row := st.db.Reader().QueryRowContext(ctx, selectColumns+` WHERE id=?`, id)
	b, err := scanBookmark(row.Scan)
	if errors.Is(err, sql.ErrNoRows) {
		return Bookmark{}, ErrNotFound
	}
	return b, err
}

// List returns every bookmark ordered by display name.
func (st *Store) List(ctx context.Context) ([]Bookmark, error) {
	rows, err := st.db.Reader().QueryContext(ctx, selectColumns+` ORDER BY display_name, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Bookmark
	for rows.Next() {
		b, err := scanBookmark(rows.Scan)
		if err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// Delete removes the bookmark and its stored credential.
func (st *Store) Delete(ctx context.Context, id string) error {
	err := st.db.WriteTx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `DELETE FROM bookmarks WHERE id=?`, id)
		if err != nil {
			return err
		}
		n, err := res.RowsAffected()
		if err != nil {
			return err
		}
		if n == 0 {
			return ErrNotFound
		}
		return nil
	})
	if err != nil {
		return err
	}
	if err := st.secrets.Delete(ctx, secretName(id)); err != nil && !errors.Is(err, secrets.ErrNotFound) {
		return fmt.Errorf("bookmark deleted but credential removal failed: %w", err)
	}
	return nil
}

// Credential fetches the bookmark's API key from the SecretStore.
func (st *Store) Credential(ctx context.Context, id string) ([]byte, error) {
	b, err := st.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if !b.HasCredential {
		return nil, secrets.ErrNotFound
	}
	return st.secrets.Get(ctx, secretName(id))
}

func secretName(id string) string {
	return "bookmark/" + id
}

func newID() string {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		// crypto/rand failing means the platform is broken; an id from a
		// zeroed buffer must never be used silently.
		panic(fmt.Sprintf("bookmarks: crypto/rand unavailable: %v", err))
	}
	return hex.EncodeToString(raw[:])
}

func validate(b Bookmark) error {
	switch {
	case b.DisplayName == "":
		return errors.New("bookmark needs a display name")
	case b.AuthURL == "":
		return errors.New("bookmark needs an auth URL")
	case b.Username == "":
		return errors.New("bookmark needs a username")
	}
	if err := swiftcontrol.ValidateEndpointURL(b.AuthURL, b.TLSMode); err != nil {
		return fmt.Errorf("auth URL: %w", err)
	}
	if b.StorageURLOverride != "" {
		if err := swiftcontrol.ValidateStorageURL(b.AuthURL, b.StorageURLOverride, b.TLSMode); err != nil {
			return err
		}
	}
	switch b.TLSMode {
	case "", swiftcontrol.TLSModeSystem, swiftcontrol.TLSModeInsecure, swiftcontrol.TLSModeCustomCA:
	default:
		return fmt.Errorf("unknown TLS mode %q", b.TLSMode)
	}
	switch b.SegmentPlacement {
	case "", PlacementRcloneDefault, PlacementSidecar, PlacementInContainer:
	default:
		return fmt.Errorf("unknown segment placement %q", b.SegmentPlacement)
	}
	return nil
}

const selectColumns = `
	SELECT id, display_name, auth_url, username,
		COALESCE(storage_url_override, ''), secret_ref IS NOT NULL,
		tls_mode, COALESCE(tls_ca_id, ''), performance_profile,
		segment_placement, COALESCE(chunk_size_bytes, 0),
		created_at, updated_at
	FROM bookmarks`

func scanBookmark(scan func(...any) error) (Bookmark, error) {
	var b Bookmark
	var tlsMode, placement, created, updated string
	err := scan(&b.ID, &b.DisplayName, &b.AuthURL, &b.Username,
		&b.StorageURLOverride, &b.HasCredential,
		&tlsMode, &b.TLSCAID, &b.PerformanceProfile,
		&placement, &b.ChunkSizeBytes,
		&created, &updated)
	if err != nil {
		return Bookmark{}, err
	}
	b.TLSMode = swiftcontrol.TLSMode(tlsMode)
	b.SegmentPlacement = SegmentPlacement(placement)
	if b.CreatedAt, err = time.Parse(time.RFC3339, created); err != nil {
		return Bookmark{}, fmt.Errorf("bookmark %s: bad created_at: %w", b.ID, err)
	}
	if b.UpdatedAt, err = time.Parse(time.RFC3339, updated); err != nil {
		return Bookmark{}, fmt.Errorf("bookmark %s: bad updated_at: %w", b.ID, err)
	}
	return b, nil
}

func nullable(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func nullableInt(n int64) *int64 {
	if n == 0 {
		return nil
	}
	return &n
}

// SaveCA stores a CA bundle after checking the PEM actually parses to
// at least one certificate, returning its id for tls_ca_id.
func (s *Store) SaveCA(ctx context.Context, pem []byte) (string, error) {
	if !x509.NewCertPool().AppendCertsFromPEM(pem) {
		return "", fmt.Errorf("custom CA: no certificates found in PEM data")
	}
	var raw [8]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	id := hex.EncodeToString(raw[:])
	err := s.db.WriteTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO ca_certs (id, pem, added_at) VALUES (?, ?, ?)`,
			id, string(pem), time.Now().UTC().Format(time.RFC3339))
		return err
	})
	if err != nil {
		return "", err
	}
	return id, nil
}

// CAPem resolves a stored bundle.
func (s *Store) CAPem(ctx context.Context, id string) ([]byte, error) {
	var pem string
	err := s.db.Reader().QueryRowContext(ctx, `SELECT pem FROM ca_certs WHERE id=?`, id).Scan(&pem)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("CA bundle %s not found", id)
	}
	if err != nil {
		return nil, err
	}
	return []byte(pem), nil
}
