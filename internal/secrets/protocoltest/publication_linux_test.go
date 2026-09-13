package protocoltest

import (
	"bytes"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/csmart/aeronautes/internal/bookmarks"
	"github.com/csmart/aeronautes/internal/db"
	"github.com/csmart/aeronautes/internal/secrets"
	"github.com/zalando/go-keyring"
)

var writeFailures = []string{
	"dismiss-set", "set-error", "set-error-after", "read-missing",
	"read-error", "read-different", "read-noncanonical", "dismiss-read-unlock",
}

func assertPrivateError(t *testing.T, err error, reference string, value []byte) {
	t.Helper()
	if err == nil {
		t.Error("unverified keychain write reported success")
		return
	}
	for _, private := range []string{reference, string(value), base64.StdEncoding.EncodeToString(value)} {
		if private != "" && strings.Contains(err.Error(), private) {
			t.Error("returned error exposed a credential reference or value")
		}
	}
}

func TestKeychainPutVerifiesReadBack(t *testing.T) {
	for _, mode := range writeFailures {
		t.Run(mode, func(t *testing.T) {
			f := fixture(t)
			f.failWith("")
			t.Cleanup(func() { f.failWith("") })
			store, err := secrets.NewKeychainStore()
			if err != nil {
				t.Fatal(err)
			}
			name, value := "private-reference/"+rand.Text(), []byte("synthetic replacement key")
			f.failWith(mode)
			err = store.Put(t.Context(), name, value)
			faults, lastWrite, deletes := f.observations()
			assertPrivateError(t, err, name, value)
			if faults == 0 || lastWrite != name {
				t.Error("write/read-back fault was not exercised")
			}
			if len(deletes) != 0 {
				t.Error("failed Put attempted to compensate outside the credential journal")
			}
		})
	}
}

func TestFailedKeychainPublicationPreservesAuthority(t *testing.T) {
	for _, replace := range []bool{false, true} {
		kind := "creation"
		if replace {
			kind = "replacement"
		}
		t.Run(kind, func(t *testing.T) {
			for _, mode := range writeFailures {
				t.Run(mode, func(t *testing.T) {
					f := fixture(t)
					f.failWith("")
					t.Cleanup(func() { f.failWith("") })
					backend, err := secrets.NewKeychainStore()
					if err != nil {
						t.Fatal(err)
					}
					d, err := db.Open(t.Context(), filepath.Join(t.TempDir(), "state.sqlite"))
					if err != nil {
						t.Fatal(err)
					}
					t.Cleanup(func() { d.Close() })
					store := bookmarks.NewStore(d, backend)
					before := bookmarks.Bookmark{DisplayName: "fixture", AuthURL: "https://original.example/auth/v1.0", Username: "account:user"}
					oldValue, newValue := []byte("synthetic original key"), []byte("synthetic replacement key")
					var oldRef string
					var oldGeneration int64
					if replace {
						before, err = store.Save(t.Context(), before, oldValue)
						if err != nil {
							t.Fatal(err)
						}
						if err := d.Reader().QueryRowContext(t.Context(), "SELECT secret_ref, generation FROM bookmarks WHERE id=?", before.ID).Scan(&oldRef, &oldGeneration); err != nil {
							t.Fatal(err)
						}
					}
					proposed := before
					proposed.AuthURL = "https://replacement.example/auth/v1.0"
					f.failWith(mode)
					_, saveErr := store.Save(t.Context(), proposed, newValue)
					faults, candidate, deletes := f.observations()
					assertPrivateError(t, saveErr, candidate, newValue)
					if faults == 0 || candidate == "" || candidate == oldRef {
						t.Error("fault did not target a fresh candidate reference")
					}
					if len(deletes) != 0 {
						t.Error("failed publication deleted external credential material")
					}
					f.failWith("")
					if replace {
						got, err := store.Get(t.Context(), before.ID)
						if err != nil || got != before {
							t.Error("failed replacement changed the published bookmark")
						}
						config, generation, err := store.ConnectionConfig(t.Context(), before.ID)
						if err != nil || config.AuthURL != before.AuthURL || config.APIKey != string(oldValue) || generation != oldGeneration {
							t.Error("failed replacement lost the original credential binding or generation")
						}
					} else {
						var count int
						if err := d.Reader().QueryRowContext(t.Context(), "SELECT COUNT(*) FROM bookmarks").Scan(&count); err != nil || count != 0 {
							t.Error("failed creation published an unreadable bookmark")
						}
					}
					status, err := store.CredentialStatus(t.Context())
					// The candidate was recorded by the keychain adapter, whose
					// authority this session cannot confirm.
					if err != nil || status != (bookmarks.CredentialStatus{Unfinished: 1, Unverified: 1}) {
						t.Errorf("failed publication lost its unfinished work: %+v, %v", status, err)
					}
					var ref, owner, state, originKind, originInstance string
					err = d.Reader().QueryRowContext(t.Context(), "SELECT secret_ref, bookmark_id, state, origin_kind, origin_instance FROM credential_work").Scan(&ref, &owner, &state, &originKind, &originInstance)
					if err != nil || ref != candidate || owner == "" || (replace && owner != before.ID) || state != "prepared" || originKind != string(backend.Origin().Kind) || originInstance != backend.Origin().Instance {
						t.Error("failed publication lost the candidate's prepared provenance")
					}
					stored, err := keyring.Get("aeronautes", candidate)
					if mode == "dismiss-set" || mode == "set-error" {
						if !errors.Is(err, keyring.ErrNotFound) {
							t.Error("pre-effect failure unexpectedly stored a candidate")
						}
					} else if err != nil || stored != base64.StdEncoding.EncodeToString(newValue) {
						t.Error("partially successful write was removed or corrupted")
					}
					// Failure must release the active-write state without erasing its
					// journal entry, so a later explicitly retried save can succeed.
					retried, err := store.Save(t.Context(), proposed, newValue)
					if err != nil {
						t.Fatal("healthy retry was refused")
					}
					value, err := store.Credential(t.Context(), retried.ID)
					if err != nil || !bytes.Equal(value, newValue) {
						t.Error("healthy retry did not publish its own key")
					}
				})
			}
		})
	}
}

func TestKeychainProbeVerifiesItsEffects(t *testing.T) {
	for _, mode := range append(append([]string(nil), writeFailures...), "dismiss-delete", "read-after-delete-error") {
		t.Run(mode, func(t *testing.T) {
			f := fixture(t)
			f.failWith(mode)
			t.Cleanup(func() { f.failWith("") })
			store, err := secrets.NewKeychainStore()
			if err == nil || store != nil {
				t.Error("incomplete or unverified probe was accepted as healthy")
			}
			faults, reference, _ := f.observations()
			if faults == 0 || !strings.HasPrefix(reference, "probe-") {
				t.Error("startup probe fault did not fire")
			}
			f.failWith(mode)
			fallback, kind := secrets.OpenDefault()
			if kind != secrets.BackendSession {
				t.Error("failed native probe did not select visible session fallback")
				return
			}
			if err := fallback.Put(t.Context(), "synthetic-session", []byte("value")); err != nil {
				t.Fatal(err)
			}
			value, err := fallback.Get(t.Context(), "synthetic-session")
			if err != nil || string(value) != "value" {
				t.Fatal("session fallback was unusable")
			}
		})
	}
}

func TestReadBackDoesNotBlockBookmarkAccess(t *testing.T) {
	f := fixture(t)
	f.failWith("")
	t.Cleanup(func() { f.failWith("") })
	backend, err := secrets.NewKeychainStore()
	if err != nil {
		t.Fatal(err)
	}
	d, err := db.Open(t.Context(), filepath.Join(t.TempDir(), "state.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	store := bookmarks.NewStore(d, backend)
	before, err := store.Save(t.Context(), bookmarks.Bookmark{DisplayName: "fixture", AuthURL: "https://original.example/auth/v1.0", Username: "account:user"}, []byte("original"))
	if err != nil {
		t.Fatal(err)
	}
	gate := f.blockReadBack()
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(gate.release) }) }
	saved := make(chan error, 1)
	settled := false
	var access chan error
	accessSettled := false
	t.Cleanup(func() {
		release()
		if !settled {
			select {
			case <-saved:
			case <-time.After(5 * time.Second):
				t.Error("save did not settle after releasing read-back")
			}
		}
		if access != nil && !accessSettled {
			select {
			case <-access:
			case <-time.After(5 * time.Second):
				t.Error("bookmark access did not settle after releasing read-back")
			}
		}
	})
	go func() {
		_, err := store.Save(t.Context(), before, []byte("replacement"))
		saved <- err
	}()
	select {
	case <-gate.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("save never reached acknowledged read-back")
	}
	access = make(chan error, 1)
	go func() {
		config, _, err := store.ConnectionConfig(t.Context(), before.ID)
		if err != nil || config.APIKey != "original" {
			access <- errors.New("original credential unavailable during read-back")
			return
		}
		status, err := store.CredentialStatus(t.Context())
		if err != nil || status != (bookmarks.CredentialStatus{Saving: 1}) {
			access <- errors.New("blocked verification lost its active-save status")
			return
		}
		access <- d.WriteTx(t.Context(), func(tx *sql.Tx) error {
			var value int
			return tx.QueryRowContext(t.Context(), "SELECT 1").Scan(&value)
		})
	}()
	select {
	case err := <-access:
		accessSettled = true
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("read-back held bookmark coordination or the SQLite writer")
	}
	release()
	select {
	case err := <-saved:
		settled = true
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("save did not finish after verified read-back")
	}
	value, err := store.Credential(t.Context(), before.ID)
	if err != nil || string(value) != "replacement" {
		t.Fatal("verified replacement was not published")
	}
}
