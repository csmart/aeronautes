package bookmarks

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/csmart/aeronautes/internal/db"
	"github.com/csmart/aeronautes/internal/secrets"
)

type failingPutStore struct {
	secrets.SecretStore
	afterWrite bool
}

func (s failingPutStore) Put(ctx context.Context, name string, value []byte) error {
	if s.afterWrite {
		if err := s.SecretStore.Put(ctx, name, value); err != nil {
			return err
		}
	}
	return errors.New("injected external write failure")
}

func TestFailedReplacementPreservesPublishedKey(t *testing.T) {
	for _, boundary := range []string{"before external write", "after external write", "sql publication"} {
		t.Run(boundary, func(t *testing.T) {
			st, d, _ := newTestStore(t)
			ctx := context.Background()
			before, err := st.Save(ctx, sample(), []byte("original-key"))
			if err != nil {
				t.Fatal(err)
			}
			if boundary == "sql publication" {
				err = d.WriteTx(ctx, func(tx *sql.Tx) error {
					_, err := tx.ExecContext(ctx, `CREATE TRIGGER refuse_replacement BEFORE UPDATE ON bookmarks
						BEGIN SELECT RAISE(ABORT, 'injected publication failure'); END`)
					return err
				})
				if err != nil {
					t.Fatal(err)
				}
			} else {
				st.secrets = failingPutStore{st.secrets, boundary == "after external write"}
			}
			proposed := before
			proposed.AuthURL = "https://replacement.example/auth/v1.0"
			_, err = st.Save(ctx, proposed, []byte("replacement-key"))
			if err == nil {
				t.Fatal("injected failure did not refuse replacement")
			}
			if boundary == "sql publication" && !strings.Contains(err.Error(), "injected publication failure") {
				t.Fatalf("publication trigger was not reached: %v", err)
			}
			got, err := st.Get(ctx, before.ID)
			if err != nil || got != before {
				t.Errorf("failed replacement changed the bookmark: %+v, %v", got, err)
			}
			key, err := st.Credential(ctx, before.ID)
			if err != nil || string(key) != "original-key" {
				t.Errorf("failed replacement changed the published key: %q, %v", key, err)
			}
			work := pendingWork(t, st)
			if len(work) != 1 || work[0].state != "prepared" || work[0].owner != before.ID || work[0].origin != st.secrets.Origin() {
				t.Fatalf("failed write lost prepared provenance: %+v", work)
			}
			current := storedRecord(t, st, before.ID)
			if work[0].ref == current.ref {
				t.Fatal("failed replacement reused the published reference")
			}
		})
	}
}

func TestRecordedSessionCannotChangeLifetime(t *testing.T) {
	st, d, _ := newTestStore(t)
	b, err := st.Save(t.Context(), sample(), []byte("original"))
	if err != nil {
		t.Fatal(err)
	}
	r := storedRecord(t, st, b.ID)
	other := secrets.NewSessionStore()
	if err := other.Put(t.Context(), r.ref, []byte("wrong-lifetime")); err != nil {
		t.Fatal(err)
	}
	reopened := NewStore(d, other)
	if config, _, err := reopened.ConnectionConfig(t.Context(), b.ID); !errors.Is(err, secrets.ErrNotFound) || config.APIKey != "" {
		t.Fatalf("new memory store impersonated original lifetime: %+v, %v", config, err)
	}
	if err := reopened.Delete(t.Context(), b.ID); err != nil {
		t.Fatal(err)
	}
	work := pendingWork(t, reopened)
	if len(work) != 1 || work[0].origin != r.origin || work[0].ref != r.ref {
		t.Fatalf("fallback replaced cleanup provenance: %+v", work)
	}
	if value, err := st.secrets.Get(t.Context(), r.ref); err != nil || string(value) != "original" {
		t.Fatalf("fallback removed original lifetime key: %q, %v", value, err)
	}
}

func TestLegacyReferenceRemainsReadableUntilRetired(t *testing.T) {
	st, d, _ := newTestStore(t)
	b, err := st.Save(t.Context(), sample(), nil)
	if err != nil {
		t.Fatal(err)
	}
	legacy := secretName(b.ID)
	if err := st.secrets.Put(t.Context(), legacy, []byte("legacy-key")); err != nil {
		t.Fatal(err)
	}
	if err := d.WriteTx(t.Context(), func(tx *sql.Tx) error {
		_, err := tx.Exec(`UPDATE bookmarks SET secret_ref=? WHERE id=?`, legacy, b.ID)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if config, _, err := st.ConnectionConfig(t.Context(), b.ID); err != nil || config.APIKey != "legacy-key" {
		t.Fatalf("upgrade forced legacy reentry: %+v, %v", config, err)
	}
	b.DisplayName = "renamed legacy"
	if _, err := st.Save(t.Context(), b, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Save(t.Context(), b, []byte("replacement")); err != nil {
		t.Fatal(err)
	}
	work := pendingWork(t, st)
	if len(work) != 1 || work[0].ref != legacy || work[0].origin != (secrets.Origin{}) || work[0].state != "retired" {
		t.Fatalf("legacy retirement invented provenance or lost reference: %+v", work)
	}
	if old, err := st.secrets.Get(t.Context(), legacy); err != nil || string(old) != "legacy-key" {
		t.Fatalf("rotation overwrote legacy reference: %q, %v", old, err)
	}
}

// This deliberately plaintext fixture holds synthetic values outside SQLite.
// Reopening it proves retention across database and adapter reopen, not native
// keychain selection, physical erasure or a power cut.
type persistentFixture struct {
	path   string
	origin secrets.Origin
	mu     sync.Mutex
}

func newPersistentFixture(path string) *persistentFixture {
	origin := secrets.NewSessionStore().Origin()
	origin.Kind = secrets.BackendKeychain
	return &persistentFixture{path: path, origin: origin}
}
func (s *persistentFixture) Origin() secrets.Origin { return s.origin }
func (s *persistentFixture) values() (map[string][]byte, error) {
	raw, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return map[string][]byte{}, nil
	}
	if err != nil {
		return nil, err
	}
	var values map[string][]byte
	err = json.Unmarshal(raw, &values)
	return values, err
}
func (s *persistentFixture) Get(_ context.Context, name string) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	values, err := s.values()
	if err != nil {
		return nil, err
	}
	value, ok := values[name]
	if !ok {
		return nil, secrets.ErrNotFound
	}
	return value, nil
}
func (s *persistentFixture) Put(_ context.Context, name string, value []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	values, err := s.values()
	if err != nil {
		return err
	}
	values[name] = value
	raw, err := json.Marshal(values)
	if err != nil {
		return err
	}
	return os.WriteFile(s.path, raw, 0o600)
}
func (s *persistentFixture) Delete(context.Context, string) error {
	return errors.New("physical cleanup must not run in the publication slice")
}

func TestRestartRetainsPersistentWorkAcrossFallback(t *testing.T) {
	ctx := t.Context()
	dir := t.TempDir()
	path := filepath.Join(dir, "state.sqlite")
	backendPath := filepath.Join(dir, "synthetic-keys.json")
	d, err := db.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	backend := newPersistentFixture(backendPath)
	st := NewStore(d, backend)
	a, err := st.Save(ctx, sample(), []byte("original"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.Save(ctx, a, []byte("replacement")); err != nil {
		t.Fatal(err)
	}
	st.secrets = failingPutStore{backend, true}
	if _, err := st.Save(ctx, a, []byte("unused-candidate")); err == nil {
		t.Fatal("external write fault did not fire")
	}
	st.secrets = backend
	b, err := st.Save(ctx, sample(), []byte("deleted-key"))
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Delete(ctx, b.ID); err != nil {
		t.Fatal(err)
	}
	before := pendingWork(t, st)
	selected := storedRecord(t, st, a.ID)
	if len(before) != 3 {
		t.Fatalf("fixture lost work before restart: %+v", before)
	}
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}

	reopenedDB, err := db.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { reopenedDB.Close() })
	fallbackMemory := secrets.NewSessionStore()
	if err := fallbackMemory.Put(ctx, selected.ref, []byte("wrong-fallback-key")); err != nil {
		t.Fatal(err)
	}
	fallback := NewStore(reopenedDB, fallbackMemory)
	if _, _, err := fallback.ConnectionConfig(ctx, a.ID); !errors.Is(err, secrets.ErrNotFound) {
		t.Fatalf("session fallback read a persistent reference: %v", err)
	}
	if got := pendingWork(t, fallback); !slices.Equal(got, before) {
		t.Fatalf("fallback erased or rerouted durable work: %+v", got)
	}
	reopenedBackend := newPersistentFixture(backendPath)
	recovered := NewStore(reopenedDB, reopenedBackend)
	if recovered.secrets.Origin().Instance == backend.Origin().Instance {
		t.Fatal("fixture reused adapter lifetime")
	}
	config, _, err := recovered.ConnectionConfig(ctx, a.ID)
	if err != nil || config.APIKey != "replacement" {
		t.Fatalf("published reference failed after restart: %+v, %v", config, err)
	}
	if _, err := recovered.Get(ctx, b.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("recovery restored deleted authority: %v", err)
	}
	for _, work := range pendingWork(t, recovered) {
		if value, err := reopenedBackend.Get(ctx, work.ref); err != nil || len(value) == 0 {
			t.Fatalf("retained work cannot locate its synthetic key: %+v, %v", work, err)
		}
		if work.origin != backend.Origin() {
			t.Fatalf("restart substituted provenance: %+v", work)
		}
	}
}

func TestReplacementRefusesConcurrentCosmeticEdit(t *testing.T) {
	st, _, _ := newTestStore(t)
	before, err := st.Save(t.Context(), sample(), []byte("original"))
	if err != nil {
		t.Fatal(err)
	}
	base := st.secrets
	entered := make(chan string, 1)
	gate, release := publicationGate(t)
	st.secrets = hookedSecrets{SecretStore: base, put: func(ctx context.Context, name string, value []byte) error {
		entered <- name
		select {
		case <-gate:
			return base.Put(ctx, name, value)
		case <-ctx.Done():
			return ctx.Err()
		}
	}}
	result := make(chan error, 1)
	go func() { _, err := st.Save(t.Context(), before, []byte("replacement")); result <- err }()
	awaitPublication(t, entered)
	cosmetic := before
	cosmetic.DisplayName = "concurrent rename"
	if _, err := st.Save(t.Context(), cosmetic, nil); err != nil {
		t.Fatal(err)
	}
	release()
	if err := awaitPublication(t, result); !errors.Is(err, ErrChanged) {
		t.Fatalf("replacement silently reverted concurrent metadata: %v", err)
	}
	current := storedRecord(t, st, before.ID)
	if current.DisplayName != cosmetic.DisplayName || current.generation != 1 || current.revision != 2 {
		t.Fatalf("cosmetic publication changed the key generation or was overwritten: %+v", current.Bookmark)
	}
	config, _, err := st.ConnectionConfig(t.Context(), before.ID)
	if err != nil || config.APIKey != "original" {
		t.Fatalf("losing candidate replaced the key: %+v, %v", config, err)
	}
}

func TestCancelledKeyReadReleasesLease(t *testing.T) {
	st, _, _ := newTestStore(t)
	before, err := st.Save(t.Context(), sample(), []byte("original"))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	entered := make(chan struct{})
	st.secrets = hookedSecrets{SecretStore: st.secrets, get: func(ctx context.Context, _ string) ([]byte, error) {
		close(entered)
		<-ctx.Done()
		return nil, ctx.Err()
	}}
	result := make(chan error, 1)
	go func() {
		config, generation, err := st.ConnectionConfig(ctx, before.ID)
		if config.APIKey != "" || generation != 0 {
			err = errors.New("cancelled snapshot returned material")
		}
		result <- err
	}()
	awaitPublication(t, entered)
	cancel()
	if err := awaitPublication(t, result); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled acquisition = %v", err)
	}
	st.mu.Lock()
	defer st.mu.Unlock()
	if len(st.leases) != 0 {
		t.Fatal("cancelled external read leaked its lease")
	}
}

func TestFailedCreationKeepsUnpublishedKeyTracked(t *testing.T) {
	st, _, _ := newTestStore(t)
	backend := st.secrets
	st.secrets = failingPutStore{backend, true}
	if _, err := st.Save(t.Context(), sample(), []byte("unpublished-key")); err == nil {
		t.Fatal("failed creation was reported as saved")
	}
	list, err := st.List(t.Context())
	if err != nil || len(list) != 0 {
		t.Fatalf("failed creation published a row: %+v, %v", list, err)
	}
	work := pendingWork(t, st)
	if len(work) != 1 || work[0].state != "prepared" || work[0].owner == "" {
		t.Fatalf("unpublished key has no independent journal owner: %+v", work)
	}
	if value, err := backend.Get(t.Context(), work[0].ref); err != nil || string(value) != "unpublished-key" {
		t.Fatalf("prepared reference cannot locate the accepted key: %q, %v", value, err)
	}
}

func TestLostPreparationResponseNeverStartsExternalWrite(t *testing.T) {
	st, d, _ := newTestStore(t)
	before, err := st.Save(t.Context(), sample(), []byte("original"))
	if err != nil {
		t.Fatal(err)
	}
	st.db = &lostCommitReply{DB: d, at: 1}
	called := false
	st.secrets = hookedSecrets{SecretStore: st.secrets, put: func(context.Context, string, []byte) error {
		called = true
		return nil
	}}
	if _, err := st.Save(t.Context(), before, []byte("replacement")); err == nil {
		t.Fatal("unconfirmed preparation reported success")
	}
	if called {
		t.Fatal("external write started after lost preparation acknowledgement")
	}
	if current := storedRecord(t, st, before.ID); current.Bookmark != before {
		t.Fatal("lost preparation reply changed published metadata")
	}
	if work := pendingWork(t, st); len(work) != 1 || work[0].state != "prepared" {
		t.Fatalf("lost preparation reply erased committed evidence: %+v", work)
	}
}

type workRecord struct {
	ref, owner, state string
	origin            secrets.Origin
}

func pendingWork(t *testing.T, st *Store) []workRecord {
	t.Helper()
	rows, err := st.db.Reader().Query(`SELECT secret_ref, bookmark_id, state, origin_kind, origin_instance
		FROM credential_work ORDER BY secret_ref`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var result []workRecord
	for rows.Next() {
		var item workRecord
		if err := rows.Scan(&item.ref, &item.owner, &item.state, &item.origin.Kind, &item.origin.Instance); err != nil {
			t.Fatal(err)
		}
		result = append(result, item)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return result
}

func storedRecord(t *testing.T, st *Store, id string) record {
	t.Helper()
	r, err := readRecord(t.Context(), st.db.Reader(), id)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

type hookedSecrets struct {
	secrets.SecretStore
	put func(context.Context, string, []byte) error
	get func(context.Context, string) ([]byte, error)
}

func (s hookedSecrets) Put(ctx context.Context, name string, value []byte) error {
	if s.put != nil {
		return s.put(ctx, name, value)
	}
	return s.SecretStore.Put(ctx, name, value)
}

func (s hookedSecrets) Get(ctx context.Context, name string) ([]byte, error) {
	if s.get != nil {
		return s.get(ctx, name)
	}
	return s.SecretStore.Get(ctx, name)
}

func publicationGate(t *testing.T) (chan struct{}, func()) {
	t.Helper()
	ch := make(chan struct{})
	var once sync.Once
	release := func() { once.Do(func() { close(ch) }) }
	t.Cleanup(release)
	return ch, release
}

func awaitPublication[T any](t *testing.T, ch <-chan T) T {
	t.Helper()
	select {
	case result := <-ch:
		return result
	case <-time.After(5 * time.Second):
		t.Fatal("publication barrier timed out")
		var zero T
		return zero
	}
}

func TestPreparedWritePrecedesExternalIO(t *testing.T) {
	st, d, path := newTestStore(t)
	base := st.secrets
	entered := make(chan string, 1)
	gate, release := publicationGate(t)
	st.secrets = hookedSecrets{SecretStore: base, put: func(ctx context.Context, name string, value []byte) error {
		entered <- name
		select {
		case <-gate:
			return base.Put(ctx, name, value)
		case <-ctx.Done():
			return ctx.Err()
		}
	}}
	result := make(chan error, 1)
	go func() { _, err := st.Save(t.Context(), sample(), []byte("new-key")); result <- err }()
	ref := awaitPublication(t, entered)
	observer, err := db.Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { observer.Close() })
	work := pendingWork(t, NewStore(observer, base))
	if len(work) != 1 || work[0].ref != ref || work[0].state != "prepared" || work[0].origin != base.Origin() {
		t.Fatalf("external write started without durable provenance: %+v", work)
	}
	// The independent handle saw the committed preparation. A concurrent
	// write also proves no global writer lock is held through the OS call.
	written := make(chan error, 1)
	go func() {
		written <- d.WriteTx(t.Context(), func(tx *sql.Tx) error {
			_, err := tx.Exec(`INSERT INTO ui_preferences (name,value) VALUES ('during-put','ok')`)
			return err
		})
	}()
	if err := awaitPublication(t, written); err != nil {
		t.Fatal(err)
	}
	st.mu.Lock()
	active := st.preparing[ref]
	st.mu.Unlock()
	if !active {
		t.Fatal("active prepared write was not protected")
	}
	release()
	if err := awaitPublication(t, result); err != nil {
		t.Fatal(err)
	}
	if got := pendingWork(t, st); len(got) != 0 {
		t.Fatalf("published preparation remained: %+v", got)
	}
}

func TestFailedPreparationNeverCallsExternalStore(t *testing.T) {
	st, d, _ := newTestStore(t)
	if err := d.WriteTx(t.Context(), func(tx *sql.Tx) error {
		_, err := tx.Exec(`CREATE TRIGGER refuse_preparation BEFORE INSERT ON credential_work
			BEGIN SELECT RAISE(ABORT, 'injected prepare failure'); END`)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	called := false
	st.secrets = hookedSecrets{SecretStore: st.secrets, put: func(context.Context, string, []byte) error {
		called = true
		return nil
	}}
	if _, err := st.Save(t.Context(), sample(), []byte("new-key")); err == nil || !strings.Contains(err.Error(), "injected prepare failure") {
		t.Fatalf("prepare failure was not reached: %v", err)
	}
	if called {
		t.Fatal("external Put ran without its durable preparation")
	}
	list, err := st.List(t.Context())
	if err != nil || len(list) != 0 {
		t.Fatalf("failed creation published a bookmark: %+v, %v", list, err)
	}
}

func TestConcurrentReplacementsHaveOneWinner(t *testing.T) {
	st, _, _ := newTestStore(t)
	before, err := st.Save(t.Context(), sample(), []byte("original"))
	if err != nil {
		t.Fatal(err)
	}
	base := st.secrets
	entered := make(chan string, 2)
	firstGate, releaseFirst := publicationGate(t)
	secondGate, releaseSecond := publicationGate(t)
	st.secrets = hookedSecrets{SecretStore: base, put: func(ctx context.Context, name string, value []byte) error {
		entered <- string(value)
		gate := firstGate
		if string(value) == "second" {
			gate = secondGate
		}
		select {
		case <-gate:
			return base.Put(ctx, name, value)
		case <-ctx.Done():
			return ctx.Err()
		}
	}}
	first, second := make(chan error, 1), make(chan error, 1)
	for _, job := range []struct {
		key    string
		result chan error
	}{{"first", first}, {"second", second}} {
		go func() {
			proposed := before
			proposed.AuthURL = "https://" + job.key + ".example/auth"
			_, err := st.Save(t.Context(), proposed, []byte(job.key))
			job.result <- err
		}()
	}
	awaitPublication(t, entered)
	awaitPublication(t, entered)
	releaseFirst()
	if err := awaitPublication(t, first); err != nil {
		t.Fatal(err)
	}
	releaseSecond()
	if err := awaitPublication(t, second); !errors.Is(err, ErrChanged) {
		t.Fatalf("losing Save = %v", err)
	}
	config, generation, err := st.ConnectionConfig(t.Context(), before.ID)
	if err != nil || config.APIKey != "first" || config.AuthURL != "https://first.example/auth" || generation != 2 {
		t.Fatalf("winner was replaced or mismatched: %+v, %d, %v", config, generation, err)
	}
	work := pendingWork(t, st)
	states := map[string]int{}
	for _, w := range work {
		states[w.state]++
	}
	if states["retired"] != 1 || states["prepared"] != 1 || len(work) != 2 {
		t.Fatalf("winner/loser cleanup was not retained: %+v", work)
	}
}

func TestReplacementCannotResurrectDeletedBookmark(t *testing.T) {
	st, _, _ := newTestStore(t)
	before, err := st.Save(t.Context(), sample(), []byte("original"))
	if err != nil {
		t.Fatal(err)
	}
	base := st.secrets
	entered := make(chan string, 1)
	gate, release := publicationGate(t)
	st.secrets = hookedSecrets{SecretStore: base, put: func(ctx context.Context, name string, value []byte) error {
		entered <- name
		select {
		case <-gate:
			return base.Put(ctx, name, value)
		case <-ctx.Done():
			return ctx.Err()
		}
	}}
	result := make(chan error, 1)
	go func() { _, err := st.Save(t.Context(), before, []byte("replacement")); result <- err }()
	awaitPublication(t, entered)
	if err := st.Delete(t.Context(), before.ID); err != nil {
		t.Fatal(err)
	}
	release()
	if err := awaitPublication(t, result); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Save after deletion = %v", err)
	}
	if _, err := st.Get(t.Context(), before.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deleted row returned: %v", err)
	}
	if got := pendingWork(t, st); len(got) != 2 {
		t.Fatalf("deleted key or losing candidate lost: %+v", got)
	}
}

func TestSnapshotRefusesReplacementDuringKeyRead(t *testing.T) {
	st, _, _ := newTestStore(t)
	before, err := st.Save(t.Context(), sample(), []byte("original"))
	if err != nil {
		t.Fatal(err)
	}
	original := storedRecord(t, st, before.ID)
	base := st.secrets
	entered := make(chan string, 1)
	gate, release := publicationGate(t)
	st.secrets = hookedSecrets{SecretStore: base, get: func(ctx context.Context, name string) ([]byte, error) {
		if name == original.ref {
			entered <- name
			select {
			case <-gate:
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
		return base.Get(ctx, name)
	}}
	result := make(chan error, 1)
	go func() {
		config, generation, err := st.ConnectionConfig(t.Context(), before.ID)
		if err != nil && (config.APIKey != "" || generation != 0) {
			err = fmt.Errorf("failed snapshot returned credential material")
		}
		result <- err
	}()
	ref := awaitPublication(t, entered)
	st.mu.Lock()
	leased := st.leases[ref] == 1
	st.mu.Unlock()
	if !leased {
		t.Fatal("blocking key read has no acquisition lease")
	}
	proposed := before
	proposed.AuthURL = "https://new.example/auth"
	if _, err := st.Save(t.Context(), proposed, []byte("replacement")); err != nil {
		t.Fatal(err)
	}
	if old, err := base.Get(t.Context(), original.ref); err != nil || string(old) != "original" {
		t.Fatalf("leased key was removed or rewritten: %q, %v", old, err)
	}
	release()
	if err := awaitPublication(t, result); !errors.Is(err, ErrChanged) {
		t.Fatalf("mixed snapshot accepted: %v", err)
	}
	st.mu.Lock()
	remaining := len(st.leases)
	st.mu.Unlock()
	if remaining != 0 {
		t.Fatal("completed snapshot leaked an acquisition lease")
	}
	config, _, err := st.ConnectionConfig(t.Context(), before.ID)
	if err != nil || config.APIKey != "replacement" || config.AuthURL != proposed.AuthURL {
		t.Fatalf("new snapshot is incoherent: %+v, %v", config, err)
	}
}

// This wraps a real committed transaction and changes only its returned
// outcome, modelling lost acknowledgement separately from a rollback trigger.
type lostCommitReply struct {
	*db.DB
	at, calls int
	failRead  bool
	closed    *sql.DB
}

func (d *lostCommitReply) WriteTx(ctx context.Context, fn func(*sql.Tx) error) error {
	d.calls++
	if err := d.DB.WriteTx(ctx, fn); err != nil {
		return err
	}
	if d.calls == d.at {
		return errors.New("injected lost commit response")
	}
	return nil
}

func (d *lostCommitReply) Reader() *sql.DB {
	if d.failRead && d.calls >= d.at {
		return d.closed
	}
	return d.DB.Reader()
}

func TestLostPublicationResponseRevalidatesSelection(t *testing.T) {
	for _, withKey := range []bool{false, true} {
		t.Run(fmt.Sprint("replacement-key=", withKey), func(t *testing.T) {
			st, d, _ := newTestStore(t)
			before, err := st.Save(t.Context(), sample(), []byte("original"))
			if err != nil {
				t.Fatal(err)
			}
			at := 1
			var key []byte
			if withKey {
				at, key = 2, []byte("replacement")
			}
			st.db = &lostCommitReply{DB: d, at: at}
			proposed := before
			proposed.DisplayName = "committed"
			saved, err := st.Save(t.Context(), proposed, key)
			if err != nil || saved.DisplayName != "committed" {
				t.Fatalf("committed Save reported failure: %+v, %v", saved, err)
			}
			config, _, err := st.ConnectionConfig(t.Context(), before.ID)
			want := "original"
			if withKey {
				want = "replacement"
			}
			if err != nil || config.APIKey != want {
				t.Fatalf("selected key was lost: %+v, %v", config, err)
			}
		})
	}
}

func TestUnresolvedPublicationRetainsKeysAndPauses(t *testing.T) {
	st, d, _ := newTestStore(t)
	before, err := st.Save(t.Context(), sample(), []byte("original"))
	if err != nil {
		t.Fatal(err)
	}
	previous := storedRecord(t, st, before.ID)
	closed, err := db.Open(t.Context(), t.TempDir()+"/closed.sqlite")
	if err != nil {
		t.Fatal(err)
	}
	unavailable := closed.Reader()
	if err := closed.Close(); err != nil {
		t.Fatal(err)
	}
	fault := &lostCommitReply{DB: d, at: 2, failRead: true, closed: unavailable}
	st.db = fault
	if _, err := st.Save(t.Context(), before, []byte("replacement")); !errors.Is(err, ErrPublicationUnresolved) {
		t.Fatalf("unresolved Save = %v", err)
	}
	if err := st.Available(t.Context(), before.ID); !errors.Is(err, ErrPublicationUnresolved) {
		t.Fatalf("unresolved bookmark usable: %v", err)
	}
	if value, err := st.secrets.Get(t.Context(), previous.ref); err != nil || string(value) != "original" {
		t.Fatalf("unresolved publication removed old key: %q, %v", value, err)
	}
	var selected string
	if err := d.Reader().QueryRow(`SELECT secret_ref FROM bookmarks WHERE id=?`, before.ID).Scan(&selected); err != nil {
		t.Fatal(err)
	}
	if value, err := st.secrets.Get(t.Context(), selected); err != nil || string(value) != "replacement" {
		t.Fatalf("unresolved publication removed candidate: %q, %v", value, err)
	}
	fault.failRead = false
	if err := st.Available(t.Context(), before.ID); err != nil {
		t.Fatalf("restored state did not resolve pause: %v", err)
	}
	if got := pendingWork(t, st); len(got) != 1 || got[0].ref != previous.ref || got[0].state != "retired" {
		t.Fatalf("committed retirement lost after unresolved response: %+v", got)
	}
}

func TestLostDeletionResponseRetainsCleanup(t *testing.T) {
	st, d, _ := newTestStore(t)
	before, err := st.Save(t.Context(), sample(), []byte("original"))
	if err != nil {
		t.Fatal(err)
	}
	previous := storedRecord(t, st, before.ID)
	st.db = &lostCommitReply{DB: d, at: 1}
	if err := st.Delete(t.Context(), before.ID); err != nil {
		t.Fatalf("committed deletion reported failure: %v", err)
	}
	if _, err := st.Get(t.Context(), before.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deleted row present: %v", err)
	}
	if got := pendingWork(t, st); len(got) != 1 || got[0].ref != previous.ref || got[0].state != "retired" {
		t.Fatalf("deletion lost its cleanup reference: %+v", got)
	}
}

func TestCredentialBackendErrorsDoNotExposeMaterial(t *testing.T) {
	st, _, _ := newTestStore(t)
	b, err := st.Save(t.Context(), sample(), []byte("synthetic-private-value"))
	if err != nil {
		t.Fatal(err)
	}
	ref := storedRecord(t, st, b.ID).ref
	base := st.secrets
	st.secrets = hookedSecrets{SecretStore: base,
		get: func(context.Context, string) ([]byte, error) {
			return nil, fmt.Errorf("%s synthetic-private-value", ref)
		},
		put: func(_ context.Context, name string, value []byte) error { return fmt.Errorf("%s %s", name, value) },
	}
	_, _, readErr := st.ConnectionConfig(t.Context(), b.ID)
	_, writeErr := st.Save(t.Context(), b, []byte("synthetic-private-value"))
	for _, err := range []error{readErr, writeErr} {
		if err == nil {
			t.Fatal("backend error was swallowed")
		}
		if strings.Contains(err.Error(), "bookmark/") || strings.Contains(err.Error(), "synthetic-private-value") {
			t.Fatalf("backend error leaked material: %v", err)
		}
	}
}
