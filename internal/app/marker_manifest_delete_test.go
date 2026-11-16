package app

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/csmart/aeronautes/internal/mutationfacts"
)

func factsOfOperation(t *testing.T, h *harness, operation string) []mutationfacts.Fact {
	t.Helper()
	var out []mutationfacts.Fact
	for _, f := range objectDeleteFacts(t, h) {
		if f.Operation == operation {
			out = append(out, f)
		}
	}
	return out
}

// ADR 0079 through ADR 0094 decision 1: the marker and manifest deletions
// are refused before any intent and any request when the portrait the view
// was built under is empty, moved or another bookmark's.
func TestMarkerAndManifestDeleteRefuseAChangedScope(t *testing.T) {
	h := newHarness(t)
	h.connect(t)
	ctx := context.Background()
	current := h.portrait()
	requests := h.mock.Requests()
	factsBefore := len(objectDeleteFacts(t, h))

	for name, scope := range map[string]ListingScopeDTO{
		"empty":            {},
		"moved generation": {BookmarkID: current.BookmarkID, Generation: current.Generation + 7},
		"another bookmark": {BookmarkID: "not-the-active-one", Generation: current.Generation},
	} {
		if _, err := h.mutate.DeleteMarker(ctx, scope, "fixtures", "folder-marker/"); !errors.Is(err, ErrMutationScopeChanged) {
			t.Fatalf("marker, %s: err = %v, want the scope refusal", name, err)
		}
		if _, err := h.mutate.DeleteManifest(ctx, scope, "fixtures", "external-dlo"); !errors.Is(err, ErrMutationScopeChanged) {
			t.Fatalf("manifest, %s: err = %v, want the scope refusal", name, err)
		}
	}
	if got := h.mock.Requests(); got != requests {
		t.Fatalf("refused deletes sent %d requests to the store", got-requests)
	}
	if got := len(objectDeleteFacts(t, h)); got != factsBefore {
		t.Fatalf("refused deletes recorded %d facts", got-factsBefore)
	}
}

// ADR 0122 decision 6: a marker and a manifest are deleted by the same one
// DELETE as any object, with no read of the name before or after. What each
// leaves is in its record: a marker's children are untouched, and a
// manifest's segments stay, because the DELETE is the plain one.
func TestMarkerAndManifestDeleteSendOneDeleteAndSayWhatStays(t *testing.T) {
	h := newHarness(t)
	h.connect(t)
	ctx := context.Background()
	segments, err := h.browse.Objects(ctx, h.bookmarkID(), "external-dlo-segments", "", "", 100)
	if err != nil {
		t.Fatal(err)
	}

	for _, c := range []struct {
		operation, name, says string
		run                   func() (MutationResultDTO, error)
	}{
		{"marker.delete", "folder-marker/", "deleted directory marker (children untouched)", func() (MutationResultDTO, error) {
			return h.mutate.DeleteMarker(ctx, h.portrait(), "fixtures", "folder-marker/")
		}},
		{"manifest.delete", "external-dlo", "deleted manifest; segments remain", func() (MutationResultDTO, error) {
			return h.mutate.DeleteManifest(ctx, h.portrait(), "fixtures", "external-dlo")
		}},
	} {
		before := len(h.mock.RequestLog())
		result, err := c.run()
		if err != nil || result.Outcome != "applied" {
			t.Fatalf("%s: result = %+v, err = %v", c.operation, result, err)
		}
		if got := requestsNaming(h, before, c.name); len(got) != 1 || !strings.HasPrefix(got[0], "DELETE ") || strings.Contains(got[0], "?") {
			t.Fatalf("%s: the store received %q for the name, want one plain DELETE and no read", c.operation, got)
		}
		facts := factsOfOperation(t, h, c.operation)
		if len(facts) != 1 || facts[0].Outcome != mutationfacts.OutcomeApplied || !strings.Contains(facts[0].Summary, c.says) {
			t.Fatalf("%s: facts = %+v", c.operation, facts)
		}
	}
	after, err := h.browse.Objects(ctx, h.bookmarkID(), "external-dlo-segments", "", "", 100)
	if err != nil || len(after.Rows) != len(segments.Rows) {
		t.Fatalf("the segments container lists %d rows, want the %d it held: %v", len(after.Rows), len(segments.Rows), err)
	}
}
