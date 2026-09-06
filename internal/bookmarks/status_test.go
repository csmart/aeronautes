package bookmarks

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/csmart/aeronautes/internal/db"
	"github.com/csmart/aeronautes/internal/secrets"
)

func TestCredentialStatusSurvivesDeletionAndFallback(t *testing.T) {
	for _, origin := range []string{"session", "keychain", "legacy"} {
		t.Run(origin, func(t *testing.T) {
			st, d, path := newTestStore(t)
			if origin == "keychain" {
				st.secrets = newPersistentFixture(filepath.Join(t.TempDir(), "synthetic-keys"))
			}
			b, err := st.Save(t.Context(), sample(), []byte("first-key"))
			if err != nil {
				t.Fatal(err)
			}
			if origin == "legacy" {
				if err := d.WriteTx(t.Context(), func(tx *sql.Tx) error {
					_, err := tx.Exec(`UPDATE bookmarks SET origin_kind='', origin_instance='' WHERE id=?`, b.ID)
					return err
				}); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := st.Save(t.Context(), b, []byte("second-key")); err != nil {
				t.Fatal(err)
			}
			st.secrets = failingPutStore{st.secrets, true}
			if _, err := st.Save(t.Context(), sample(), []byte("unfinished-key")); err == nil {
				t.Fatal("external failure was not injected")
			}
			if err := st.Delete(t.Context(), b.ID); err != nil {
				t.Fatal(err)
			}
			before := pendingWork(t, st)
			if err := d.Close(); err != nil {
				t.Fatal(err)
			}
			reopened, err := db.Open(t.Context(), path)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { reopened.Close() })
			fallback := NewStore(reopened, secrets.NewSessionStore())
			want := CredentialStatus{Retired: 2, Unfinished: 1}
			if got, err := fallback.CredentialStatus(t.Context()); err != nil || got != want {
				t.Fatalf("fallback status = %+v, %v; want %+v", got, err, want)
			}
			if after := pendingWork(t, fallback); !reflect.DeepEqual(before, after) {
				t.Fatalf("status lost cleanup evidence: before=%+v after=%+v", before, after)
			}
			if _, err := fallback.Get(t.Context(), b.ID); !errors.Is(err, ErrNotFound) {
				t.Fatalf("status restored deleted authority: %v", err)
			}
			// An unavailable backend must not prevent journal-only reporting.
			fallback.secrets = nil
			if got, err := fallback.CredentialStatus(t.Context()); err != nil || got != want {
				t.Fatalf("unavailable backend changed status: %+v, %v", got, err)
			}
		})
	}
}

func TestCredentialStatusDistinguishesActiveSaves(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(map[bool]string{false: "published", true: "failed"}[fail], func(t *testing.T) {
			st, _, _ := newTestStore(t)
			base := st.secrets
			entered := make(chan struct{}, 1)
			gate, release := publicationGate(t)
			st.secrets = hookedSecrets{SecretStore: base, put: func(ctx context.Context, name string, value []byte) error {
				entered <- struct{}{}
				<-gate
				if err := base.Put(ctx, name, value); err != nil {
					return err
				}
				if fail {
					return errors.New("lost write response")
				}
				return nil
			}}
			result := make(chan error, 1)
			go func() {
				_, err := st.Save(t.Context(), sample(), []byte("new-key"))
				result <- err
			}()
			awaitPublication(t, entered)
			status := make(chan CredentialStatus, 1)
			go func() {
				got, err := st.CredentialStatus(t.Context())
				if err != nil {
					t.Error(err)
				}
				status <- got
			}()
			if got := awaitPublication(t, status); got != (CredentialStatus{Saving: 1}) {
				t.Fatalf("active keychain prompt counted as unfinished: %+v", got)
			}
			release()
			if err := awaitPublication(t, result); (err != nil) != fail {
				t.Fatalf("save outcome = %v; want failure %v", err, fail)
			}
			want := CredentialStatus{}
			if fail {
				want.Unfinished = 1
			}
			if got, err := st.CredentialStatus(t.Context()); err != nil || got != want {
				t.Fatalf("finished save status = %+v, %v; want %+v", got, err, want)
			}
		})
	}
}

func TestCredentialStatusReadFailureIsNotAnEmptyJournal(t *testing.T) {
	st, d, _ := newTestStore(t)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if got, err := st.CredentialStatus(ctx); err == nil || got != (CredentialStatus{}) {
		t.Fatalf("cancelled status read = %+v, %v", got, err)
	}
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
	if got, err := st.CredentialStatus(t.Context()); err == nil || got != (CredentialStatus{}) {
		t.Fatalf("closed database status read = %+v, %v", got, err)
	}
}
