package app

// The connection registry must give different guidance for a credential
// store it cannot reach and a credential that is gone, and the bridge count
// must carry the same distinction. Neither message may claim the key was
// erased when that has not been established.

import (
	"context"
	"errors"
	"fmt"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/csmart/aeronautes/internal/bookmarks"
	"github.com/csmart/aeronautes/internal/db"
	"github.com/csmart/aeronautes/internal/jobs"
	"github.com/csmart/aeronautes/internal/secrets"
	"github.com/csmart/aeronautes/internal/swifttest"
)

// authorityFixture is one saved bookmark whose recording session store the
// test keeps, so it can act both as that authority and as another one.
type authorityFixture struct {
	d        *db.DB
	recorded *authorityBackend
	service  *BookmarkService
	id       string
	ref      string
}

func newAuthorityFixture(t *testing.T) *authorityFixture {
	t.Helper()
	ctx := t.Context()
	d, err := db.Open(ctx, filepath.Join(t.TempDir(), "state.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	mock := swifttest.New("test:tester", "testing")
	srv := httptest.NewServer(mock)
	t.Cleanup(srv.Close)

	recorded := &authorityBackend{SecretStore: secrets.NewSessionStore()}
	store := bookmarks.NewStore(d, recorded)
	service := NewBookmarkService(store, NewConnections(store), jobs.NewStore(d))
	saved, err := service.Save(ctx, BookmarkDTO{
		DisplayName: "primary", AuthURL: srv.URL + "/auth/v1.0", Username: "test:tester",
	}, "testing")
	if err != nil {
		t.Fatal(err)
	}
	var ref string
	if err := d.Reader().QueryRowContext(ctx,
		`SELECT secret_ref FROM bookmarks WHERE id=?`, saved.ID).Scan(&ref); err != nil {
		t.Fatal(err)
	}
	return &authorityFixture{d: d, recorded: recorded, service: service, id: saved.ID, ref: ref}
}

// otherAuthority is a store that recorded none of this work.
func (f *authorityFixture) otherAuthority() *bookmarks.Store {
	return bookmarks.NewStore(f.d, secrets.NewSessionStore())
}

func TestConnectionGuidanceSeparatesUnreachableFromErasure(t *testing.T) {
	f := newAuthorityFixture(t)
	ctx := t.Context()

	// The recording authority reports the value gone: proven erasure.
	if err := f.recorded.Delete(ctx, f.ref); err != nil {
		t.Fatal(err)
	}
	_, erasedErr := NewConnections(bookmarks.NewStore(f.d, f.recorded)).open(ctx, f.id)
	// A different authority leaves the key's fate unknown.
	_, unreachableErr := NewConnections(f.otherAuthority()).open(ctx, f.id)

	if !errors.Is(erasedErr, secrets.ErrNotFound) || errors.Is(erasedErr, secrets.ErrAuthorityUnreachable) {
		t.Fatalf("erasure was not classified as an absent key: %v", erasedErr)
	}
	if !errors.Is(unreachableErr, secrets.ErrAuthorityUnreachable) || errors.Is(unreachableErr, secrets.ErrNotFound) {
		t.Fatalf("an unconfirmed store was not classified as unreachable: %v", unreachableErr)
	}
	if erasedErr.Error() == unreachableErr.Error() {
		t.Fatalf("both refusals give the same guidance: %v", erasedErr)
	}

	unreachableText := strings.ToLower(unreachableErr.Error())
	for _, claim := range []string{"erased", "removed", "deleted"} {
		if strings.Contains(unreachableText, claim) {
			t.Fatalf("the unreachable message claims the key was %s: %v", claim, unreachableErr)
		}
	}
	if !strings.Contains(unreachableText, "unlock") || !strings.Contains(unreachableText, "reconnect") {
		t.Fatalf("the unreachable message does not offer to unlock or reconnect: %v", unreachableErr)
	}
	// Re-entry stays available when the original store cannot be recovered.
	if !strings.Contains(unreachableText, "enter the key again") {
		t.Fatalf("the unreachable message does not preserve re-entry: %v", unreachableErr)
	}
	if !strings.Contains(strings.ToLower(erasedErr.Error()), "enter it again") {
		t.Fatalf("the erasure message does not ask for the key: %v", erasedErr)
	}
	for _, text := range []string{erasedErr.Error(), unreachableErr.Error()} {
		if strings.Contains(text, f.ref) {
			t.Fatalf("a refusal carried the credential reference: %s", text)
		}
	}
}

func TestCredentialStatusDTOReportsUnverifiedWork(t *testing.T) {
	f := newAuthorityFixture(t)
	ctx := t.Context()
	// The pass after the delete must leave the row for this test to have
	// work to report: the fixture answers the removal as unresolved.
	f.recorded.deleteErr = fmt.Errorf("%w: the fixture held the key", secrets.ErrUnresolved)
	if err := f.service.Delete(ctx, f.id); err != nil {
		t.Fatal(err)
	}

	own, err := f.service.CredentialStatus(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if own.Retired != 1 || own.Unverified != 0 {
		t.Fatalf("the recording authority reported its own work as unconfirmed: %+v", own)
	}
	if !strings.Contains(own.CleanupReason, "the fixture held the key") {
		t.Fatalf("the status does not say why cleanup left the row: %+v", own)
	}

	other := NewBookmarkService(f.otherAuthority(), nil, nil)
	changed, err := other.CredentialStatus(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if changed.Retired != 1 || changed.Unverified != 1 {
		t.Fatalf("a different authority did not report unconfirmed work: %+v", changed)
	}
	if changed.Unverified > changed.Retired+changed.Unfinished {
		t.Fatalf("the facet is not a subset of retired and unfinished work: %+v", changed)
	}
}

// authorityBackend records like the session store and answers a delete
// as the test directs, so a status test can keep a retired row that the
// ADR 0076 pass would otherwise remove at once.
type authorityBackend struct {
	secrets.SecretStore
	deleteErr error
}

func (b *authorityBackend) Delete(ctx context.Context, name string) error {
	if b.deleteErr != nil {
		return b.deleteErr
	}
	return b.SecretStore.Delete(ctx, name)
}
