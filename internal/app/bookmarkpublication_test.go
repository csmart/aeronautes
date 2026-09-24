package app

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/csmart/aeronautes/internal/bookmarks"
	"github.com/csmart/aeronautes/internal/secrets"
	"github.com/csmart/aeronautes/internal/swifttest"
)

func TestFailedBookmarkReplacementKeepsWorkingKey(t *testing.T) {
	h := newHarness(t)
	ctx := t.Context()
	h.connect(t)
	before, err := h.bookmarks.List(ctx)
	if err != nil || len(before) != 1 {
		t.Fatalf("bookmark fixture: %v", err)
	}
	live, err := h.transfer.connections.Session()
	if err != nil {
		t.Fatal(err)
	}
	var receiverRequests atomic.Int64
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receiverRequests.Add(1)
		w.WriteHeader(http.StatusUnauthorized)
	}))
	t.Cleanup(receiver.Close)
	if err := h.db.WriteTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.Exec(`CREATE TRIGGER refuse_key_replacement BEFORE UPDATE ON bookmarks
			BEGIN SELECT RAISE(ABORT, 'injected publication failure'); END`)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	proposed := before[0]
	proposed.AuthURL = receiver.URL + "/auth/v1.0"
	if _, err := h.bookmarks.Save(ctx, proposed, "replacement-key"); err == nil || !strings.Contains(err.Error(), "injected publication failure") {
		t.Fatalf("publication fault not reached: %v", err)
	}
	after, err := h.bookmarks.List(ctx)
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatalf("failed save changed metadata: %+v, %v", after, err)
	}
	current, err := h.transfer.connections.Session()
	if err != nil || current != live {
		t.Fatalf("failed save invalidated the live connection: %v", err)
	}
	fresh := NewConnections(h.bookmarks.store)
	if err := fresh.Connect(ctx, before[0].ID); err != nil {
		t.Errorf("failed replacement broke fresh authentication to the original endpoint: %v", err)
	}
	if receiverRequests.Load() != 0 {
		t.Errorf("failed replacement reached its receiver %d times", receiverRequests.Load())
	}
}

type blockedCredentialRead struct {
	*secrets.SessionStore
	entered, release chan struct{}
	once             sync.Once
}

func (s *blockedCredentialRead) Get(ctx context.Context, name string) ([]byte, error) {
	s.once.Do(func() { close(s.entered) })
	select {
	case <-s.release:
		return s.SessionStore.Get(ctx, name)
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func TestConnectCannotMixConfigurationAndReplacementKey(t *testing.T) {
	h := newHarness(t)
	var oldRequests, newRequests atomic.Int64
	oldMock := swifttest.New("old:user", "old-key")
	oldReceiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		oldRequests.Add(1)
		oldMock.ServeHTTP(w, r)
	}))
	t.Cleanup(oldReceiver.Close)
	newReceiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		newRequests.Add(1)
		w.WriteHeader(http.StatusUnauthorized)
	}))
	t.Cleanup(newReceiver.Close)
	backend := &blockedCredentialRead{SessionStore: secrets.NewSessionStore(),
		entered: make(chan struct{}), release: make(chan struct{})}
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(backend.release) }) }
	t.Cleanup(release)
	store := bookmarks.NewStore(h.db, backend)
	connections := NewConnections(store)
	service := NewBookmarkService(store, connections, nil)
	b, err := service.Save(t.Context(), BookmarkDTO{
		DisplayName: "captured", AuthURL: oldReceiver.URL + "/auth/v1.0", Username: "old:user", TLSMode: "system",
	}, "old-key")
	if err != nil {
		t.Fatal(err)
	}
	result := make(chan error, 1)
	go func() { result <- connections.Connect(t.Context(), b.ID) }()
	select {
	case <-backend.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("connection did not reach the key-read barrier")
	}
	b.AuthURL, b.Username = newReceiver.URL+"/auth/v1.0", "new:user"
	if _, err := service.Save(t.Context(), b, "new-key"); err != nil {
		t.Fatal(err)
	}
	release()
	select {
	case err := <-result:
		if err == nil {
			t.Error("Connect accepted a binding changed during acquisition")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("connection did not leave the key-read barrier")
	}
	if oldRequests.Load() != 0 || newRequests.Load() != 0 {
		t.Fatalf("mixed acquisition sent requests: old=%d new=%d", oldRequests.Load(), newRequests.Load())
	}
	if _, err := connections.Session(); !errors.Is(err, ErrNotConnected) {
		t.Fatalf("refused acquisition installed an active session: %v", err)
	}
}

func TestBookmarkDeletionRemovesTheRetiredKey(t *testing.T) {
	h := newHarness(t)
	h.connect(t)
	list, err := h.bookmarks.List(t.Context())
	if err != nil || len(list) != 1 {
		t.Fatalf("bookmark fixture: %v", err)
	}
	if err := h.bookmarks.Delete(t.Context(), list[0].ID); err != nil {
		t.Fatal(err)
	}
	if _, err := h.transfer.connections.Session(); !errors.Is(err, ErrNotConnected) {
		t.Fatalf("logical deletion retained active cache authority: %v", err)
	}
	if _, err := h.transfer.connections.SessionFor(t.Context(), list[0].ID); !errors.Is(err, bookmarks.ErrNotFound) {
		t.Fatalf("logical deletion allowed new session acquisition: %v", err)
	}
	// The session store is the authority that recorded the key, so the
	// pass after the delete removes it and its journal row (ADR 0076).
	var pending int
	if err := h.db.Reader().QueryRow(`SELECT COUNT(*) FROM credential_work WHERE bookmark_id=? AND state='retired'`,
		list[0].ID).Scan(&pending); err != nil || pending != 0 {
		t.Fatalf("physical cleanup did not follow the logical deletion: %d, %v", pending, err)
	}
	status, err := h.bookmarks.CredentialStatus(t.Context())
	if err != nil || status.Retired != 0 || status.CleanupReason != "" {
		t.Fatalf("credential status after deletion = %+v, %v", status, err)
	}
}
