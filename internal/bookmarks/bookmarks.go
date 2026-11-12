// Package bookmarks stores saved Swift endpoints (DESIGN.md §11): the
// non-secret half in SQLite, the credential in the SecretStore under the
// name recorded as secret_ref. Whether that credential survives a restart
// is recorded as backend provenance, never as a secret column (K-SEC-003).
package bookmarks

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/x509"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"sync"
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
	// AuthVersion selects the auth protocol: 1 is TempAuth v1, 3 is Keystone
	// v3. A bookmark saved without one is stored as 1 (NormalizeAuth).
	AuthVersion int
	// UserDomain, ProjectName, ProjectDomain and Region are the Keystone v3
	// identity for the password method with project scope; empty for v1.
	// The domains default to "Default" and Region is required for v3 and
	// binds the token to one catalog region (ADR 0100).
	UserDomain    string
	ProjectName   string
	ProjectDomain string
	// UserID and ProjectID name the v3 user and project by ID instead of
	// by name and domain (ADR 0113); empty for v1 and for a side named by
	// name.
	UserID             string
	ProjectID          string
	Region             string
	TLSMode            swiftcontrol.TLSMode
	TLSCAID            string
	PerformanceProfile string
	SegmentPlacement   SegmentPlacement
	// ChunkSizeBytes 0 means the default: 5 GiB capped at the cluster's
	// max_file_size (§25).
	ChunkSizeBytes int64
	// LargeObjects is what a segmented upload to this bookmark publishes,
	// "slo" or "dlo" (ADR 0111). Empty reads as static, the default, so a
	// bookmark saved before the setting existed needs no value.
	LargeObjects string
	// VerifyFailure is why the bookmark did not verify after import, or
	// empty (ADR 0114). It is not part of the binding: SetVerifyFailure
	// writes it, and a save neither carries nor clears it.
	VerifyFailure string
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
		AuthVersion:        b.AuthVersion,
		UserDomain:         b.UserDomain,
		ProjectName:        b.ProjectName,
		ProjectDomain:      b.ProjectDomain,
		UserID:             b.UserID,
		ProjectID:          b.ProjectID,
		Region:             b.Region,
		TLS:                swiftcontrol.TLSConfig{Mode: b.TLSMode, CACertPEM: caPEM},
	}
}

// NormalizeAuth fills the auth-field defaults a bookmark takes when it is
// saved or verified without them: TempAuth v1 when no version is named, and
// Keystone's conventional "Default" domain for a v3 bookmark that leaves the
// user or project domain blank (the wire library requires a user domain, and
// an explicit project domain keeps the scoped token deterministic). A side
// named by ID has no domain, so none is filled there (ADR 0113). The save
// and verify paths both apply it so a bookmark's stored form and the form a
// pre-save check authenticates with cannot drift (ADR 0100).
func NormalizeAuth(b *Bookmark) {
	if b.AuthVersion == 0 {
		b.AuthVersion = 1
	}
	if b.AuthVersion == 3 {
		if b.UserDomain == "" && b.UserID == "" {
			b.UserDomain = "Default"
		}
		if b.ProjectDomain == "" && b.ProjectID == "" {
			b.ProjectDomain = "Default"
		}
	}
}

// Store persists bookmarks.
type Store struct {
	db      stateDB
	secrets secrets.SecretStore
	// Cleanup must eventually share this coordination so it cannot claim
	// a key while an external write or snapshot read still uses it.
	mu         sync.Mutex
	preparing  map[string]bool
	leases     map[string]int
	jobPins    map[string]int
	unresolved map[string]bool
	// claimed holds references a cleanup pass is removing (ADR 0076); no
	// new reader may acquire one.
	claimed     map[string]bool
	lastCleanup CleanupResult
}

type stateDB interface {
	Reader() *sql.DB
	WriteTx(context.Context, func(*sql.Tx) error) error
}

// NewStore builds a Store over the state database and the active secret
// backend.
func NewStore(d *db.DB, s secrets.SecretStore) *Store {
	return &Store{db: d, secrets: s, preparing: map[string]bool{}, leases: map[string]int{}, jobPins: map[string]int{}, unresolved: map[string]bool{}, claimed: map[string]bool{}}
}

// Save inserts (empty ID) or updates a bookmark. Every replacement key
// gets an immutable reference; nil retains a key only for the same binding.
func (st *Store) Save(ctx context.Context, b Bookmark, secret []byte) (Bookmark, error) {
	return st.SaveWithCA(ctx, b, secret, nil)
}

// SaveWithCA publishes trust, configuration and the selected key together.
// External writes use a durably prepared, unpublished reference.
func (st *Store) SaveWithCA(ctx context.Context, b Bookmark, secret, caPEM []byte) (Bookmark, error) {
	// Normalised first, so validate judges the auth version a bookmark is
	// stored with, and so the retention check below compares the stored and
	// proposed bookmarks like for like: editing a v1 bookmark saved as
	// version 1 from an older frontend that sends 0 must not look like a
	// binding change and demand the key.
	NormalizeAuth(&b)
	if err := validate(b); err != nil {
		return Bookmark{}, err
	}
	if caPEM != nil {
		caPEM = bytes.Clone(caPEM)
		if !x509.NewCertPool().AppendCertsFromPEM(caPEM) {
			return Bookmark{}, errors.New("custom CA: no certificates found in PEM data")
		}
		if b.TLSMode == swiftcontrol.TLSModeCustomCA {
			b.TLSCAID = newID()
		} else {
			caPEM = nil
		}
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
	var previous, proposed record
	proposed.Bookmark = b
	proposed.generation, proposed.revision = 1, 1
	if secret != nil {
		proposed.ref = secretName(b.ID) + "/" + newID()
		proposed.origin = st.secrets.Origin()
		if proposed.origin.Instance == "" ||
			(proposed.origin.Kind != secrets.BackendSession && proposed.origin.Kind != secrets.BackendKeychain) {
			return Bookmark{}, errors.New("credential backend has no usable provenance")
		}
	}

	st.mu.Lock()
	commitPossible, err := st.writePublication(ctx, func(tx *sql.Tx) error {
		if !isNew {
			var err error
			previous, err = readRecord(ctx, tx, b.ID)
			if err != nil {
				return err
			}
			delete(st.unresolved, b.ID)
			proposed.CreatedAt = previous.CreatedAt
			proposed.revision = previous.revision + 1
			proposed.generation = previous.generation
			bindingErr := retainCredentialBinding(ctx, tx, previous.Bookmark, b, caPEM)
			if bindingErr != nil && !errors.Is(bindingErr, errKeyReentry) {
				return bindingErr
			}
			if secret == nil && previous.HasCredential && bindingErr != nil {
				return bindingErr
			}
			if secret != nil || bindingErr != nil {
				if st.jobPins[b.ID] != 0 {
					return ErrInUse
				}
				proposed.generation++
			}
			if secret == nil {
				proposed.ref, proposed.origin = previous.ref, previous.origin
			}
		}
		proposed.HasCredential = proposed.ref != ""
		if secret != nil {
			return recordWork(ctx, tx, proposed, "prepared")
		}
		return publishRecord(ctx, tx, previous, proposed, caPEM, isNew)
	})
	if err != nil {
		if secret == nil && commitPossible {
			err = st.publicationResult(ctx, previous, proposed, err, isNew)
		}
		st.mu.Unlock()
		if err != nil {
			return Bookmark{}, err
		}
		return proposed.Bookmark, nil
	}
	if secret == nil {
		st.mu.Unlock()
		return proposed.Bookmark, nil
	}
	st.preparing[proposed.ref] = true
	st.mu.Unlock()

	// The writer and coordinator remain free while an OS prompt is open.
	putErr := st.secrets.Put(ctx, proposed.ref, secret)

	st.mu.Lock()
	defer st.mu.Unlock()
	defer delete(st.preparing, proposed.ref)
	if putErr != nil {
		// The backend may have accepted the write before reporting failure.
		// Keep its journal row and do not expose a backend's key-bearing error.
		return Bookmark{}, errors.New("could not save the key; an unused key may remain in credential storage")
	}
	commitPossible, err = st.writePublication(ctx, func(tx *sql.Tx) error {
		if !isNew {
			current, err := readRecord(ctx, tx, b.ID)
			if err != nil {
				return err
			}
			if current.generation != previous.generation || current.revision != previous.revision {
				return ErrChanged
			}
			// A job can be admitted while the external Put is waiting.
			// Its old binding must survive the resumed publication too.
			if st.jobPins[b.ID] != 0 {
				return ErrInUse
			}
		}
		return publishRecord(ctx, tx, previous, proposed, caPEM, isNew)
	})
	if err != nil {
		if commitPossible {
			err = st.publicationResult(ctx, previous, proposed, err, isNew)
		}
		if err != nil {
			return Bookmark{}, err
		}
	}
	return proposed.Bookmark, nil
}

var errKeyReentry = errors.New("re-enter the key when changing the auth URL, username, storage override, auth version, Keystone domain, project, IDs or region, or TLS trust")

// SameBinding reports whether two connection configurations name the same
// credential binding (ADR 0015): the endpoint, the user, the storage
// override, the auth version with its Keystone identity, and the TLS trust
// mode, where an unset mode is system trust. A custom CA bundle is part of
// the binding too, but its bytes live where each caller holds them, so the
// caller compares those.
func SameBinding(current, proposed swiftcontrol.Config) bool {
	mode := func(m swiftcontrol.TLSMode) swiftcontrol.TLSMode {
		if m == "" {
			return swiftcontrol.TLSModeSystem
		}
		return m
	}
	return current.AuthURL == proposed.AuthURL && current.Username == proposed.Username &&
		current.StorageURLOverride == proposed.StorageURLOverride &&
		current.AuthVersion == proposed.AuthVersion && current.UserDomain == proposed.UserDomain &&
		current.ProjectName == proposed.ProjectName && current.ProjectDomain == proposed.ProjectDomain &&
		current.UserID == proposed.UserID && current.ProjectID == proposed.ProjectID &&
		current.Region == proposed.Region && mode(current.TLS.Mode) == mode(proposed.TLS.Mode)
}

// Retention is checked against the row inside the update transaction;
// a prior service read could become stale before this edit is published.
func retainCredentialBinding(ctx context.Context, tx *sql.Tx, current, proposed Bookmark, caPEM []byte) error {
	if !SameBinding(current.SwiftConfig(nil, nil), proposed.SwiftConfig(nil, nil)) {
		return errKeyReentry
	}
	if proposed.TLSMode != swiftcontrol.TLSModeCustomCA {
		return nil
	}
	var currentCA string
	if err := tx.QueryRowContext(ctx, `SELECT pem FROM ca_certs WHERE id=?`, current.TLSCAID).Scan(&currentCA); err != nil {
		return fmt.Errorf("read stored CA for credential binding: %w", err)
	}
	if caPEM == nil {
		var proposedCA string
		if err := tx.QueryRowContext(ctx, `SELECT pem FROM ca_certs WHERE id=?`, proposed.TLSCAID).Scan(&proposedCA); err != nil {
			return fmt.Errorf("read proposed CA for credential binding: %w", err)
		}
		caPEM = []byte(proposedCA)
	}
	if !bytes.Equal([]byte(currentCA), caPEM) {
		return errKeyReentry
	}
	return nil
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

// Delete removes logical authority and durably tracks the selected key.
// Physical removal waits for verified backend and live-reference cleanup.
func (st *Store) Delete(ctx context.Context, id string) error {
	st.mu.Lock()
	defer st.mu.Unlock()
	var previous record
	commitPossible, err := st.writePublication(ctx, func(tx *sql.Tx) error {
		var err error
		previous, err = readRecord(ctx, tx, id)
		if err != nil {
			return err
		}
		if st.jobPins[id] != 0 {
			return ErrInUse
		}
		if previous.ref != "" {
			if err := recordWork(ctx, tx, previous, "retired"); err != nil {
				return err
			}
		}
		_, err = tx.ExecContext(ctx, `DELETE FROM bookmarks WHERE id=?`, id)
		return err
	})
	if err == nil {
		delete(st.unresolved, id)
		return nil
	}
	if !commitPossible {
		return err
	}
	check, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	_, readErr := readRecord(check, st.db.Reader(), id)
	if errors.Is(readErr, ErrNotFound) {
		if previous.ref != "" {
			var count int
			if queryErr := st.db.Reader().QueryRowContext(check,
				`SELECT COUNT(*) FROM credential_work WHERE secret_ref=? AND state='retired'`, previous.ref).Scan(&count); queryErr != nil || count != 1 {
				st.unresolved[id] = true
				return ErrPublicationUnresolved
			}
		}
		delete(st.unresolved, id)
		return nil
	}
	if readErr != nil {
		st.unresolved[id] = true
		return ErrPublicationUnresolved
	}
	return err
}

// Credential is an internal convenience; connection users must acquire the
// complete ConnectionConfig rather than separately fetching bookmark fields.
func (st *Store) Credential(ctx context.Context, id string) ([]byte, error) {
	config, _, err := st.ConnectionConfig(ctx, id)
	if err != nil {
		return nil, err
	}
	return []byte(config.APIKey), nil
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
	case b.Username == "" && b.AuthVersion != 3:
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
	switch swiftcontrol.ObjectClass(b.LargeObjects) {
	case "", swiftcontrol.ClassSLO, swiftcontrol.ClassDLO:
	default:
		return fmt.Errorf("unknown large object kind %q (static is slo, dynamic is dlo)", b.LargeObjects)
	}
	// The version is normalised before this runs, so an unset one is 1 here;
	// the v3 requirements that New enforces at connect time are checked at
	// this save boundary too, so an unusable v3 bookmark cannot be stored.
	switch b.AuthVersion {
	case 1:
		if b.UserID != "" || b.ProjectID != "" {
			return errors.New("a TempAuth bookmark has no Keystone user or project ID")
		}
	case 3:
		if b.Region == "" {
			return errors.New("Keystone v3 bookmark needs a region")
		}
		if b.Username == "" && b.UserID == "" {
			return errors.New("Keystone v3 bookmark needs a username or a user ID")
		}
		if b.ProjectName == "" && b.ProjectID == "" {
			return errors.New("Keystone v3 bookmark needs a project name or a project ID")
		}
		if b.StorageURLOverride != "" {
			return errors.New("Keystone v3 does not use a storage-URL override; the identity catalog selects the endpoint")
		}
	default:
		return fmt.Errorf("unsupported auth version %d", b.AuthVersion)
	}
	return nil
}

const selectColumns = `
	SELECT id, display_name, auth_url, username,
		COALESCE(storage_url_override, ''),
		auth_version, COALESCE(user_domain, ''), COALESCE(project_name, ''),
		COALESCE(project_domain, ''), COALESCE(user_id, ''), COALESCE(project_id, ''),
		COALESCE(region, ''),
		secret_ref IS NOT NULL,
		tls_mode, COALESCE(tls_ca_id, ''), performance_profile,
		segment_placement, COALESCE(chunk_size_bytes, 0), COALESCE(large_objects, ''),
		COALESCE(verify_failure, ''),
		created_at, updated_at, COALESCE(secret_ref, ''), origin_kind,
		origin_instance, origin_persistent, generation, revision,
		(SELECT pem FROM ca_certs WHERE id=bookmarks.tls_ca_id)
	FROM bookmarks`

func scanBookmark(scan func(...any) error) (Bookmark, error) {
	r, err := scanRecord(scan)
	return r.Bookmark, err
}

func scanRecord(scan func(...any) error) (record, error) {
	var r record
	b := &r.Bookmark
	var tlsMode, placement, created, updated string
	err := scan(&b.ID, &b.DisplayName, &b.AuthURL, &b.Username,
		&b.StorageURLOverride,
		&b.AuthVersion, &b.UserDomain, &b.ProjectName, &b.ProjectDomain, &b.UserID, &b.ProjectID,
		&b.Region,
		&b.HasCredential,
		&tlsMode, &b.TLSCAID, &b.PerformanceProfile,
		&placement, &b.ChunkSizeBytes, &b.LargeObjects,
		&b.VerifyFailure,
		&created, &updated, &r.ref, &r.origin.Kind, &r.origin.Instance, &r.origin.Persistent,
		&r.generation, &r.revision, &r.ca)
	if err != nil {
		return record{}, err
	}
	b.TLSMode = swiftcontrol.TLSMode(tlsMode)
	b.SegmentPlacement = SegmentPlacement(placement)
	if b.CreatedAt, err = time.Parse(time.RFC3339, created); err != nil {
		return record{}, fmt.Errorf("bookmark %s: bad created_at: %w", b.ID, err)
	}
	if b.UpdatedAt, err = time.Parse(time.RFC3339, updated); err != nil {
		return record{}, fmt.Errorf("bookmark %s: bad updated_at: %w", b.ID, err)
	}
	return r, nil
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

// SetVerifyFailure records why a bookmark did not verify after import, or
// clears the record when reason is empty (ADR 0114 decision 9). The flag is
// not part of the binding, so the bookmark's generation and revision stay
// as they are and no cached session or edit in flight is invalidated.
func (st *Store) SetVerifyFailure(ctx context.Context, id, reason string) error {
	return st.db.WriteTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE bookmarks SET verify_failure=? WHERE id=?`, nullable(reason), id)
		return err
	})
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
