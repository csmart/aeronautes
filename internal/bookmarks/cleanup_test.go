package bookmarks

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/csmart/aeronautes/internal/db"
	"github.com/csmart/aeronautes/internal/secrets"
)

// cleanupBackend is a verified keychain-like store whose Delete answers
// as the test directs, recording every removal it was asked for.
type cleanupBackend struct {
	mu      sync.Mutex
	origin  secrets.Origin
	values  map[string][]byte
	deletes []string
	mode    string
	hold    chan struct{}
}

func verifiedBackend(collection string) *cleanupBackend {
	return &cleanupBackend{
		origin: secrets.Origin{Kind: secrets.BackendKeychain, Instance: "fixture-" + collection, Persistent: "/org/freedesktop/secrets/collection/" + collection},
		values: map[string][]byte{},
	}
}

func (b *cleanupBackend) Origin() secrets.Origin { return b.origin }

func (b *cleanupBackend) Get(ctx context.Context, name string) ([]byte, error) {
	if b.hold != nil {
		select {
		case <-b.hold:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	value, ok := b.values[name]
	if !ok {
		return nil, secrets.ErrNotFound
	}
	return append([]byte(nil), value...), nil
}

func (b *cleanupBackend) Put(_ context.Context, name string, value []byte) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.values[name] = append([]byte(nil), value...)
	return nil
}

func (b *cleanupBackend) Delete(_ context.Context, name string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.deletes = append(b.deletes, name)
	switch b.mode {
	case "unresolved":
		return fmt.Errorf("%w: the prompt was dismissed", secrets.ErrUnresolved)
	case "unreachable":
		return secrets.ErrAuthorityUnreachable
	case "missing":
		delete(b.values, name)
		return secrets.ErrNotFound
	}
	if _, ok := b.values[name]; !ok {
		return secrets.ErrNotFound
	}
	delete(b.values, name)
	return nil
}

func (b *cleanupBackend) has(name string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	_, ok := b.values[name]
	return ok
}

func newCleanupStore(t *testing.T, backend secrets.SecretStore) (*Store, *db.DB, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "state.sqlite")
	d, err := db.Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	return NewStore(d, backend), d, path
}

func retiredCount(t *testing.T, d *db.DB) int {
	t.Helper()
	var n int
	if err := d.Reader().QueryRow(`SELECT COUNT(*) FROM credential_work WHERE state='retired'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestCleanupRemovesTheKeyOfADeletedBookmark(t *testing.T) {
	backend := verifiedBackend("one")
	st, d, _ := newCleanupStore(t, backend)
	id, r := saveOne(t, st)
	if !backend.has(r.ref) {
		t.Fatal("the fixture did not receive the key")
	}
	if err := st.Delete(t.Context(), id); err != nil {
		t.Fatal(err)
	}
	if retiredCount(t, d) != 1 {
		t.Fatal("logical deletion did not journal the retired key")
	}
	result, err := st.Cleanup(t.Context(), "")
	if err != nil || result.Removed != 1 || result.Pending != 0 || result.Reason != "" {
		t.Fatalf("cleanup = %+v, %v", result, err)
	}
	if backend.has(r.ref) || len(backend.deletes) != 1 || backend.deletes[0] != r.ref {
		t.Fatalf("the key was not removed exactly once: present=%v deletes=%v", backend.has(r.ref), backend.deletes)
	}
	if retiredCount(t, d) != 0 {
		t.Fatal("a confirmed removal left its journal row")
	}
	status, err := st.CredentialStatus(t.Context())
	if err != nil || status.Retired != 0 || status.CleanupReason != "" {
		t.Fatalf("status after cleanup = %+v, %v", status, err)
	}
}

func TestCleanupRemovesTheReplacedKeyAndKeepsTheCurrentOne(t *testing.T) {
	backend := verifiedBackend("one")
	st, d, _ := newCleanupStore(t, backend)
	id, before := saveOne(t, st)
	b, err := st.Get(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.Save(t.Context(), b, []byte("replacement-key")); err != nil {
		t.Fatal(err)
	}
	after := storedRecord(t, st, id)
	if after.ref == before.ref {
		t.Fatal("a re-entered key kept the reference")
	}
	result, err := st.Cleanup(t.Context(), id)
	if err != nil || result.Removed != 1 || result.Pending != 0 {
		t.Fatalf("cleanup = %+v, %v", result, err)
	}
	if backend.has(before.ref) || !backend.has(after.ref) {
		t.Fatalf("cleanup removed the wrong key: old present=%v current present=%v", backend.has(before.ref), backend.has(after.ref))
	}
	if retiredCount(t, d) != 0 {
		t.Fatal("the retired row survived a confirmed removal")
	}
}

func TestCleanupKeepsTheRowWhenRemovalIsUnresolved(t *testing.T) {
	backend := verifiedBackend("one")
	st, d, _ := newCleanupStore(t, backend)
	id, r := saveOne(t, st)
	if err := st.Delete(t.Context(), id); err != nil {
		t.Fatal(err)
	}
	backend.mode = "unresolved"
	result, err := st.Cleanup(t.Context(), "")
	if err != nil || result.Removed != 0 || result.Pending != 1 || !strings.Contains(result.Reason, "the prompt was dismissed") {
		t.Fatalf("unresolved cleanup = %+v, %v", result, err)
	}
	if retiredCount(t, d) != 1 {
		t.Fatal("an unresolved removal lost its journal row")
	}
	status, err := st.CredentialStatus(t.Context())
	if err != nil || status.Retired != 1 || status.CleanupReason != result.Reason {
		t.Fatalf("status after an unresolved pass = %+v, %v", status, err)
	}
	// The next pass, once the store answers, removes it.
	backend.mode = ""
	result, err = st.Cleanup(t.Context(), "")
	if err != nil || result.Removed != 1 || retiredCount(t, d) != 0 || backend.has(r.ref) {
		t.Fatalf("retry = %+v, %v, retired=%d present=%v", result, err, retiredCount(t, d), backend.has(r.ref))
	}
}

func TestCleanupTreatsAbsenceFromTheVerifiedStoreAsRemoved(t *testing.T) {
	backend := verifiedBackend("one")
	st, d, _ := newCleanupStore(t, backend)
	id, _ := saveOne(t, st)
	if err := st.Delete(t.Context(), id); err != nil {
		t.Fatal(err)
	}
	backend.mode = "missing"
	result, err := st.Cleanup(t.Context(), "")
	if err != nil || result.Removed != 1 || retiredCount(t, d) != 0 {
		t.Fatalf("absence from the recording store = %+v, %v, retired=%d", result, err, retiredCount(t, d))
	}
}

func TestCleanupLeavesRowsAnotherStoreRecorded(t *testing.T) {
	recording := verifiedBackend("one")
	st, d, path := newCleanupStore(t, recording)
	id, r := saveOne(t, st)
	if err := st.Delete(t.Context(), id); err != nil {
		t.Fatal(err)
	}
	// The same journal, opened over a different collection, must not
	// treat that collection's answer as erasure of the recorded key.
	other := verifiedBackend("two")
	reopened, err := db.Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { reopened.Close() })
	second := NewStore(reopened, other)
	result, err := second.Cleanup(t.Context(), "")
	if err != nil || result.Removed != 0 || result.Pending != 1 || result.Reason != "the store that recorded the key is not the active one" {
		t.Fatalf("cleanup under another store = %+v, %v", result, err)
	}
	if len(other.deletes) != 0 || !recording.has(r.ref) || retiredCount(t, d) != 1 {
		t.Fatalf("another store touched the recorded key: deletes=%v present=%v retired=%d", other.deletes, recording.has(r.ref), retiredCount(t, d))
	}
	// An unverified keychain origin (empty Persistent) is never cleaned.
	unverified := &recordedAs{SecretStore: recording, origin: secrets.Origin{Kind: secrets.BackendKeychain, Instance: "x"}}
	third := NewStore(reopened, unverified)
	if result, err := third.Cleanup(t.Context(), ""); err != nil || result.Removed != 0 || result.Pending != 1 {
		t.Fatalf("cleanup under an unverified store = %+v, %v", result, err)
	}
}

func TestCleanupWaitsForALeaseAndRefusesNewReaders(t *testing.T) {
	backend := verifiedBackend("one")
	backend.hold = make(chan struct{})
	st, d, _ := newCleanupStore(t, backend)
	id, before := saveOne(t, st)
	// A reader holds the current key's lease while its Get is in flight.
	acquired := make(chan error, 1)
	go func() {
		_, _, err := st.ConnectionConfig(t.Context(), id)
		acquired <- err
	}()
	for {
		st.mu.Lock()
		held := st.leases[before.ref] > 0
		st.mu.Unlock()
		if held {
			break
		}
	}
	b, err := st.Get(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.Save(t.Context(), b, []byte("replacement-key")); err != nil {
		t.Fatal(err)
	}
	result, err := st.Cleanup(t.Context(), id)
	if err != nil || result.Removed != 0 || result.Pending != 1 || result.Reason != "a read still uses the key" {
		t.Fatalf("cleanup during a lease = %+v, %v", result, err)
	}
	if len(backend.deletes) != 0 || !backend.has(before.ref) {
		t.Fatal("cleanup removed a leased key")
	}
	close(backend.hold)
	if err := <-acquired; !errors.Is(err, ErrChanged) {
		t.Fatalf("the held read finished as %v, want ErrChanged after the re-key", err)
	}
	result, err = st.Cleanup(t.Context(), id)
	if err != nil || result.Removed != 1 || backend.has(before.ref) || retiredCount(t, d) != 0 {
		t.Fatalf("cleanup after the lease = %+v, %v, present=%v retired=%d", result, err, backend.has(before.ref), retiredCount(t, d))
	}
	// A reference a pass has claimed cannot be acquired by a new reader.
	current := storedRecord(t, st, id)
	st.mu.Lock()
	st.claimed[current.ref] = true
	st.mu.Unlock()
	if _, _, err := st.ConnectionConfig(t.Context(), id); !errors.Is(err, ErrChanged) {
		t.Fatalf("a claimed key was acquired: %v", err)
	}
}

func TestCleanupWaitsForAPinnedJob(t *testing.T) {
	backend := verifiedBackend("one")
	st, d, _ := newCleanupStore(t, backend)
	id, before := saveOne(t, st)
	// A re-key whose removal stayed unresolved leaves a retired row; a job
	// then pins the bookmark. Save and Delete both refuse under a pin, so
	// this is the order in which a pinned bookmark can own a retired row.
	b, err := st.Get(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.Save(t.Context(), b, []byte("replacement-key")); err != nil {
		t.Fatal(err)
	}
	backend.mode = "unresolved"
	if result, err := st.Cleanup(t.Context(), id); err != nil || result.Pending != 1 {
		t.Fatalf("unresolved first pass = %+v, %v", result, err)
	}
	backend.mode = ""
	current := storedRecord(t, st, id)
	unpin, err := st.PinGeneration(t.Context(), id, current.generation)
	if err != nil {
		t.Fatal(err)
	}
	result, err := st.Cleanup(t.Context(), id)
	if err != nil || result.Removed != 0 || result.Pending != 1 || result.Reason != "a job still uses the bookmark" {
		t.Fatalf("cleanup under a pin = %+v, %v", result, err)
	}
	unpin()
	result, err = st.Cleanup(t.Context(), id)
	if err != nil || result.Removed != 1 || retiredCount(t, d) != 0 || backend.has(before.ref) {
		t.Fatalf("cleanup after the pin = %+v, %v", result, err)
	}
}
