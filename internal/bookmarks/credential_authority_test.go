package bookmarks

// Finish-line L01: an authority the active backend cannot confirm must be
// distinguishable from an erased credential, and retained cleanup must be
// reportable against the authority that recorded it. Absence proves erasure
// only from that exact authority.

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/csmart/aeronautes/internal/secrets"
)

// reoriginated reports a different backend kind over a fresh session store,
// standing in for another native lifetime. It holds none of the earlier
// values, which is what a changed backend looks like to a read.
type reoriginated struct {
	secrets.SecretStore
	kind secrets.Backend
}

func (s *reoriginated) Origin() secrets.Origin {
	origin := s.SecretStore.Origin()
	origin.Kind = s.kind
	return origin
}

func changedBackend(kind secrets.Backend) secrets.SecretStore {
	return &reoriginated{SecretStore: secrets.NewSessionStore(), kind: kind}
}

// lockedBackend answers every read with a locked-store failure rather than a
// not-found, standing in for a present but locked or cancelled keychain.
type lockedBackend struct {
	secrets.SecretStore
}

func (s *lockedBackend) Get(context.Context, string) ([]byte, error) {
	return nil, errors.New("credential storage is locked")
}

// recordedAs presents the wrapped store under a fixed origin, so a record can
// be written with keychain or legacy provenance without a native keychain.
type recordedAs struct {
	secrets.SecretStore
	origin secrets.Origin
}

func (s *recordedAs) Origin() secrets.Origin { return s.origin }

func saveOne(t *testing.T, st *Store) (string, record) {
	t.Helper()
	b, err := st.Save(t.Context(), sample(), []byte("original-key"))
	if err != nil {
		t.Fatal(err)
	}
	return b.ID, storedRecord(t, st, b.ID)
}

func TestUnreadableBackendIsDistinctFromErasure(t *testing.T) {
	st, d, _ := newTestStore(t)
	id, _ := saveOne(t, st)

	reopened := NewStore(d, changedBackend(secrets.BackendKeychain))
	_, _, changedErr := reopened.ConnectionConfig(t.Context(), id)

	erased, _, _ := newTestStore(t)
	erasedID, erasedRecord := saveOne(t, erased)
	if err := erased.secrets.Delete(t.Context(), erasedRecord.ref); err != nil {
		t.Fatal(err)
	}
	_, _, erasedErr := erased.ConnectionConfig(t.Context(), erasedID)

	if !errors.Is(changedErr, secrets.ErrAuthorityUnreachable) {
		t.Fatalf("changed backend is not reported as unreachable authority: %v", changedErr)
	}
	if errors.Is(changedErr, secrets.ErrNotFound) {
		t.Fatalf("unreachable authority is reported as erasure: %v", changedErr)
	}
	if !errors.Is(erasedErr, secrets.ErrNotFound) {
		t.Fatalf("the recording authority did not report erasure: %v", erasedErr)
	}
	if errors.Is(erasedErr, secrets.ErrAuthorityUnreachable) {
		t.Fatalf("erasure is reported as unreachable authority: %v", erasedErr)
	}
}

func TestLockedBackendIsClassifiedUnresolved(t *testing.T) {
	st, d, _ := newTestStore(t)
	id, _ := saveOne(t, st)

	locked := NewStore(d, &lockedBackend{SecretStore: st.secrets})
	_, _, lockedErr := locked.ConnectionConfig(t.Context(), id)
	changed := NewStore(d, changedBackend(secrets.BackendKeychain))
	_, _, changedErr := changed.ConnectionConfig(t.Context(), id)

	for name, err := range map[string]error{"locked": lockedErr, "changed": changedErr} {
		if !errors.Is(err, secrets.ErrAuthorityUnreachable) {
			t.Fatalf("%s backend is not reported as unreachable authority: %v", name, err)
		}
		if errors.Is(err, secrets.ErrNotFound) {
			t.Fatalf("%s backend is reported as erasure: %v", name, err)
		}
	}
}

func TestRetainedCleanupReportsReachability(t *testing.T) {
	st, d, _ := newTestStore(t)
	id, _ := saveOne(t, st)
	if err := st.Delete(t.Context(), id); err != nil {
		t.Fatal(err)
	}

	original, err := st.CredentialStatus(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	reopened := NewStore(d, changedBackend(secrets.BackendKeychain))
	changed, err := reopened.CredentialStatus(t.Context())
	if err != nil {
		t.Fatal(err)
	}

	if original.Retired != 1 || changed.Retired != 1 {
		t.Fatalf("retained cleanup was lost: original %+v changed %+v", original, changed)
	}
	if original.Unverified != 0 {
		t.Fatalf("the recording authority reported its own work as unverified: %+v", original)
	}
	if changed.Unverified != 1 {
		t.Fatalf("cleanup does not report an unconfirmed authority: %+v", changed)
	}
}

func TestRestartKeepsUnreachableDistinct(t *testing.T) {
	st, d, _ := newTestStore(t)
	id, _ := saveOne(t, st)

	// A new Store over the same open database, not a process restart.
	restarted := NewStore(d, changedBackend(secrets.BackendKeychain))
	_, _, err := restarted.ConnectionConfig(t.Context(), id)
	if !errors.Is(err, secrets.ErrAuthorityUnreachable) {
		t.Fatalf("a new store over a changed backend did not report unreachable authority: %v", err)
	}
	if errors.Is(err, secrets.ErrNotFound) {
		t.Fatalf("a new store reports an unreachable authority as erasure: %v", err)
	}
}

// A keychain reference read by a second keychain adapter passes ReadableBy,
// so only the verified-authority predicate keeps its absence unproven.
func TestSecondKeychainAdapterCannotProveErasure(t *testing.T) {
	backing := secrets.NewSessionStore()
	first := &recordedAs{SecretStore: backing, origin: secrets.Origin{Kind: secrets.BackendKeychain, Instance: "adapter-1"}}
	st, d, _ := newTestStore(t)
	st.secrets = first
	id, r := saveOne(t, st)

	second := &recordedAs{SecretStore: secrets.NewSessionStore(), origin: secrets.Origin{Kind: secrets.BackendKeychain, Instance: "adapter-2"}}
	if !r.origin.ReadableBy(second.Origin()) {
		t.Fatal("the case no longer exercises a readable but unverified origin")
	}
	reopened := NewStore(d, second)
	_, _, err := reopened.ConnectionConfig(t.Context(), id)
	if !errors.Is(err, secrets.ErrAuthorityUnreachable) {
		t.Fatalf("a second keychain adapter did not report unreachable authority: %v", err)
	}
	if errors.Is(err, secrets.ErrNotFound) {
		t.Fatalf("a second keychain adapter claimed erasure: %v", err)
	}

	status, err := reopened.CredentialStatus(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if status.Unverified != status.Retired+status.Unfinished {
		t.Fatalf("keychain work was counted as confirmed: %+v", status)
	}
}

// A legacy reference has no recorded provenance. It stays readable through
// the active backend, but its absence is not erasure evidence.
func TestLegacyOriginCannotProveErasure(t *testing.T) {
	st, d, _ := newTestStore(t)
	id, r := saveOne(t, st)
	if err := d.WriteTx(t.Context(), func(tx *sql.Tx) error {
		_, err := tx.ExecContext(t.Context(),
			`UPDATE bookmarks SET origin_kind='', origin_instance='' WHERE id=?`, id)
		return err
	}); err != nil {
		t.Fatal(err)
	}

	// Control: a legacy reference the active backend still holds is read.
	config, _, err := st.ConnectionConfig(t.Context(), id)
	if err != nil || config.APIKey != "original-key" {
		t.Fatalf("legacy reference stopped being readable: %v", err)
	}

	if err := st.secrets.Delete(t.Context(), r.ref); err != nil {
		t.Fatal(err)
	}
	_, _, err = st.ConnectionConfig(t.Context(), id)
	if !errors.Is(err, secrets.ErrAuthorityUnreachable) {
		t.Fatalf("a legacy origin did not report unreachable authority: %v", err)
	}
	if errors.Is(err, secrets.ErrNotFound) {
		t.Fatalf("a legacy origin claimed erasure: %v", err)
	}
}

// A new session store is a different authority even though its kind matches.
func TestNewSessionCannotProveErasure(t *testing.T) {
	st, d, _ := newTestStore(t)
	id, _ := saveOne(t, st)

	_, _, err := NewStore(d, secrets.NewSessionStore()).ConnectionConfig(t.Context(), id)
	if !errors.Is(err, secrets.ErrAuthorityUnreachable) {
		t.Fatalf("a new session did not report unreachable authority: %v", err)
	}
	if errors.Is(err, secrets.ErrNotFound) {
		t.Fatalf("a new session claimed erasure: %v", err)
	}
}

// Saves running in this app stay outside the facet, so it remains a subset
// of the retired and unfinished counts.
func TestUnverifiedExcludesActiveSaves(t *testing.T) {
	st, _, _ := newTestStore(t)
	id, _ := saveOne(t, st)
	if err := st.Delete(t.Context(), id); err != nil {
		t.Fatal(err)
	}

	changedActive := &recordedAs{SecretStore: st.secrets, origin: secrets.Origin{Kind: secrets.BackendKeychain, Instance: "adapter-9"}}
	st.secrets = changedActive
	st.mu.Lock()
	st.preparing["bookmark/in-flight"] = true
	st.mu.Unlock()
	err := st.db.WriteTx(t.Context(), func(tx *sql.Tx) error {
		_, err := tx.ExecContext(t.Context(), `INSERT INTO credential_work
			(secret_ref, bookmark_id, origin_kind, origin_instance, state, created_at)
			VALUES ('bookmark/in-flight', 'b', 'keychain', 'adapter-9', 'prepared', '2026-09-13T00:00:00Z')`)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}

	status, err := st.CredentialStatus(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if status.Saving != 1 {
		t.Fatalf("the in-flight save was not counted: %+v", status)
	}
	if status.Unverified != status.Retired+status.Unfinished {
		t.Fatalf("the facet is not a subset of retired and unfinished work: %+v", status)
	}
	if status.Unverified != 1 {
		t.Fatalf("the in-flight save was counted as unverified work: %+v", status)
	}
}

func TestChangedBackendKeepsStoredValue(t *testing.T) {
	st, d, _ := newTestStore(t)
	id, r := saveOne(t, st)

	reopened := NewStore(d, changedBackend(secrets.BackendKeychain))
	if _, _, err := reopened.ConnectionConfig(t.Context(), id); err == nil {
		t.Fatal("changed backend was accepted")
	}
	value, err := st.secrets.Get(t.Context(), r.ref)
	if err != nil || string(value) != "original-key" {
		t.Fatalf("original authority lost its value: %q %v", value, err)
	}
}

func TestChangedBackendKeepsReference(t *testing.T) {
	st, d, _ := newTestStore(t)
	id, before := saveOne(t, st)

	reopened := NewStore(d, changedBackend(secrets.BackendKeychain))
	if _, _, err := reopened.ConnectionConfig(t.Context(), id); err == nil {
		t.Fatal("changed backend was accepted")
	}
	after := storedRecord(t, reopened, id)
	if after.ref != before.ref || after.origin != before.origin {
		t.Fatalf("unreadable read rewrote the binding: %+v then %+v", before, after)
	}
}

func TestRetirementKeepsOriginalAuthority(t *testing.T) {
	st, _, _ := newTestStore(t)
	id, r := saveOne(t, st)
	if err := st.Delete(t.Context(), id); err != nil {
		t.Fatal(err)
	}
	work := pendingWork(t, st)
	if len(work) != 1 || work[0].origin != r.origin || work[0].ref != r.ref || work[0].state != "retired" {
		t.Fatalf("retired work lost its authority: %+v", work)
	}
}

func TestSameAuthorityStillReads(t *testing.T) {
	st, _, _ := newTestStore(t)
	id, _ := saveOne(t, st)
	config, _, err := st.ConnectionConfig(t.Context(), id)
	if err != nil || config.APIKey != "original-key" {
		t.Fatalf("same authority could not read its credential: %v", err)
	}
}

func TestErasedCredentialStillRefuses(t *testing.T) {
	st, _, _ := newTestStore(t)
	id, r := saveOne(t, st)
	if err := st.secrets.Delete(t.Context(), r.ref); err != nil {
		t.Fatal(err)
	}
	if _, _, err := st.ConnectionConfig(t.Context(), id); err == nil {
		t.Fatal("erased credential was accepted")
	}
}
