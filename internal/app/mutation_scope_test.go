package app

import (
	"context"
	"errors"
	"fmt"
	"testing"
)

// ADR 0079: each of the four kept mutations is bound to the scope the
// caller names. Under the active connection's portrait it applies and
// records as before; under another bookmark's scope, an earlier
// generation of the same bookmark, or no scope at all it is refused
// before any intent is recorded or any request is sent.
func TestMutationsAreBoundToTheCallerScope(t *testing.T) {
	h := newHarness(t)
	ctx := t.Context()
	h.connect(t)
	bookmark := h.transfer.connections.ActiveBookmarkID()
	other, err := h.bookmarks.Save(ctx, BookmarkDTO{DisplayName: "Other", AuthURL: h.authURL, Username: "test:tester"}, "testing")
	if err != nil {
		t.Fatal(err)
	}
	current := h.portrait()
	if current.BookmarkID != bookmark || current.Generation <= 0 {
		t.Fatalf("portrait = %+v", current)
	}
	stale := current
	facts := func() int {
		t.Helper()
		var n int
		if err := h.db.Reader().QueryRowContext(ctx, "SELECT COUNT(*) FROM mutation_facts").Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	audits := func() int {
		t.Helper()
		events, err := h.auditSvc.RecentFor(ctx, bookmark, 100)
		if err != nil {
			t.Fatal(err)
		}
		return len(events)
	}
	// prepare reads what the edit needs (a fingerprint) before the request
	// count is taken, so only the mutation's own traffic is measured.
	type action struct {
		name    string
		prepare func() string
		run     func(scope ListingScopeDTO, fingerprint, suffix string) error
	}
	none := func() string { return "" }
	actions := []action{
		{"UpdateObjectMetadata", func() string {
			detail, err := h.inspector.Object(ctx, bookmark, "fixtures", "normal/hello.txt")
			if err != nil {
				t.Fatal(err)
			}
			return detail.EditableFingerprint
		}, func(scope ListingScopeDTO, fingerprint, suffix string) error {
			_, err := h.mutate.UpdateObjectMetadata(ctx, scope, "fixtures", "normal/hello.txt", fingerprint, ObjectMetadataEditDTO{Meta: map[string]string{"scope-case": suffix}})
			return err
		}},
		{"UpdateContainer", func() string {
			detail, err := h.inspector.Container(ctx, bookmark, "fixtures")
			if err != nil {
				t.Fatal(err)
			}
			return detail.EditableFingerprint
		}, func(scope ListingScopeDTO, fingerprint, suffix string) error {
			_, err := h.mutate.UpdateContainer(ctx, scope, "fixtures", fingerprint, ContainerEditDTO{Meta: map[string]string{"scope-case": suffix}})
			return err
		}},
		{"CreateContainer", none, func(scope ListingScopeDTO, _, suffix string) error {
			_, err := h.mutate.CreateContainer(ctx, scope, ContainerCreateDTO{Name: "scoped-" + suffix})
			return err
		}},
		{"CreateMarker", none, func(scope ListingScopeDTO, _, suffix string) error {
			_, err := h.mutate.CreateMarker(ctx, scope, "fixtures", "scoped-"+suffix+"/")
			return err
		}},
	}
	refusals := []struct {
		name  string
		scope func() ListingScopeDTO
	}{
		{"other_bookmark", func() ListingScopeDTO { return ListingScopeDTO{BookmarkID: other.ID, Generation: current.Generation} }},
		{"empty_scope", func() ListingScopeDTO { return ListingScopeDTO{} }},
		{"stale_generation", func() ListingScopeDTO { return stale }},
	}
	for i, a := range actions {
		t.Run(a.name, func(t *testing.T) {
			for _, r := range refusals {
				if r.name == "stale_generation" {
					// A key change moves the generation; the view built under
					// the old one no longer names the connection.
					if _, err := h.bookmarks.Save(ctx, BookmarkDTO{ID: bookmark, DisplayName: "Mock", AuthURL: h.authURL, Username: "test:tester"}, "testing"); err != nil {
						t.Fatal(err)
					}
					if _, err := h.browse.Connect(ctx, bookmark); err != nil {
						t.Fatal(err)
					}
					current = h.portrait()
					if current.Generation <= stale.Generation {
						t.Fatalf("generation did not move: %+v after %+v", current, stale)
					}
				}
				fingerprint := a.prepare()
				before, beforeAudits, beforeRequests := facts(), audits(), h.mock.Requests()
				err := a.run(r.scope(), fingerprint, fmt.Sprintf("%d-%s", i, r.name))
				if !errors.Is(err, ErrMutationScopeChanged) {
					t.Fatalf("%s under %s was not refused: %v", a.name, r.name, err)
				}
				if facts() != before || audits() != beforeAudits || h.mock.Requests() != beforeRequests {
					t.Fatalf("%s under %s recorded or sent something: facts %d->%d audits %d->%d requests %d->%d",
						a.name, r.name, before, facts(), beforeAudits, audits(), beforeRequests, h.mock.Requests())
				}
			}
			fingerprint := a.prepare()
			before, beforeAudits := facts(), audits()
			if err := a.run(current, fingerprint, fmt.Sprintf("%d-current", i)); err != nil {
				t.Fatalf("%s under the current portrait: %v", a.name, err)
			}
			if facts() != before+1 || audits() != beforeAudits+1 {
				t.Fatalf("%s under the current portrait recorded facts %d->%d audits %d->%d", a.name, before, facts(), beforeAudits, audits())
			}
			stale = current
		})
	}
}

var _ = context.Background
