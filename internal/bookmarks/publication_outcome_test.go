package bookmarks

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"

	"github.com/csmart/aeronautes/internal/db"
)

func unavailablePublicationReader(t *testing.T) *sql.DB {
	t.Helper()
	d, err := db.Open(t.Context(), t.TempDir()+"/unavailable.sqlite")
	if err != nil {
		t.Fatal(err)
	}
	reader := d.Reader()
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
	return reader
}

func TestRefusedSaveDoesNotBecomeUnresolved(t *testing.T) {
	for _, boundary := range []string{"binding refusal", "missing row", "rollback", "begin failure"} {
		t.Run(boundary, func(t *testing.T) {
			st, d, _ := newTestStore(t)
			before, err := st.Save(t.Context(), sample(), []byte("original"))
			if err != nil {
				t.Fatal(err)
			}
			proposed := before
			proposed.DisplayName = "refused edit"
			ctx := t.Context()
			var want error
			switch boundary {
			case "binding refusal":
				proposed.AuthURL = "https://other.example/auth"
				want = errKeyReentry
			case "missing row":
				proposed.ID = "missing"
				want = ErrNotFound
			case "rollback":
				if err := d.WriteTx(ctx, func(tx *sql.Tx) error {
					_, err := tx.Exec(`CREATE TRIGGER refuse_update BEFORE UPDATE ON bookmarks
						BEGIN SELECT RAISE(ABORT, 'injected rollback'); END`)
					return err
				}); err != nil {
					t.Fatal(err)
				}
			case "begin failure":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
				want = context.Canceled
			}
			st.db = &lostCommitReply{DB: d, at: 1, failRead: true, closed: unavailablePublicationReader(t)}
			_, err = st.Save(ctx, proposed, nil)
			if want != nil {
				if !errors.Is(err, want) {
					t.Errorf("known refusal = %v, want %v", err, want)
				}
			} else if err == nil || !strings.Contains(err.Error(), "injected rollback") {
				t.Errorf("rollback result changed: %v", err)
			}
			if len(st.unresolved) != 0 {
				t.Error("a transaction that could not commit paused the bookmark")
			}
			observer := NewStore(d, st.secrets)
			current := storedRecord(t, observer, before.ID)
			if current.Bookmark != before {
				t.Error("known refusal changed the published bookmark")
			}
			if got := pendingWork(t, observer); len(got) != 0 {
				t.Fatalf("rolled back refusal retained work: %+v", got)
			}
		})
	}
}

func TestRefusedDeleteDoesNotBecomeUnresolved(t *testing.T) {
	st, d, _ := newTestStore(t)
	before, err := st.Save(t.Context(), sample(), []byte("original"))
	if err != nil {
		t.Fatal(err)
	}
	if err := d.WriteTx(t.Context(), func(tx *sql.Tx) error {
		_, err := tx.Exec(`CREATE TRIGGER refuse_delete BEFORE DELETE ON bookmarks
			BEGIN SELECT RAISE(ABORT, 'injected deletion rollback'); END`)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	st.db = &lostCommitReply{DB: d, at: 1, failRead: true, closed: unavailablePublicationReader(t)}
	if err := st.Delete(t.Context(), before.ID); err == nil || !strings.Contains(err.Error(), "injected deletion rollback") {
		t.Errorf("known deletion refusal = %v", err)
	}
	if len(st.unresolved) != 0 {
		t.Error("rolled back deletion paused the bookmark")
	}
	observer := NewStore(d, st.secrets)
	if current := storedRecord(t, observer, before.ID); current.Bookmark != before {
		t.Error("refused deletion removed its row")
	}
	if got := pendingWork(t, observer); len(got) != 0 {
		t.Fatalf("retirement record escaped deletion rollback: %+v", got)
	}
}

func TestLosingReplacementDoesNotPauseWinner(t *testing.T) {
	st, d, _ := newTestStore(t)
	before, err := st.Save(t.Context(), sample(), []byte("original"))
	if err != nil {
		t.Fatal(err)
	}
	base := st.secrets
	entered := make(chan string, 1)
	gate, release := publicationGate(t)
	st.db = &lostCommitReply{DB: d, at: 3, failRead: true, closed: unavailablePublicationReader(t)}
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
	go func() { _, err := st.Save(t.Context(), before, []byte("losing candidate")); result <- err }()
	awaitPublication(t, entered)
	winner := before
	winner.DisplayName = "winning rename"
	if _, err := st.Save(t.Context(), winner, nil); err != nil {
		t.Fatal(err)
	}
	release()
	if err := awaitPublication(t, result); !errors.Is(err, ErrChanged) {
		t.Errorf("losing Save = %v", err)
	}
	if len(st.unresolved) != 0 {
		t.Error("a refused replacement paused the winner")
	}
	observer := NewStore(d, base)
	current := storedRecord(t, observer, before.ID)
	if current.DisplayName != winner.DisplayName {
		t.Error("loser replaced the winner")
	}
	if config, _, err := observer.ConnectionConfig(t.Context(), before.ID); err != nil || config.APIKey != "original" {
		t.Fatalf("loser changed the winning key: %v", err)
	}
	if work := pendingWork(t, observer); len(work) != 1 || work[0].state != "prepared" {
		t.Fatalf("loser lost its unused candidate: %+v", work)
	}
}
