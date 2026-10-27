package app

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/csmart/aeronautes/internal/mutationfacts"
	"github.com/csmart/aeronautes/internal/swiftcontrol"
	"github.com/csmart/aeronautes/internal/swifttest"
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
	marker, err := h.inspector.Prefix(ctx, h.bookmarkID(), "fixtures", "folder-marker/")
	if err != nil || marker.SelfObject == nil {
		t.Fatalf("marker detail = %+v, %v", marker, err)
	}
	manifest := shownObject(t, h, "external-dlo")
	current := h.portrait()
	requests := h.mock.Requests()
	factsBefore := len(objectDeleteFacts(t, h))

	for name, scope := range map[string]ListingScopeDTO{
		"empty":            {},
		"moved generation": {BookmarkID: current.BookmarkID, Generation: current.Generation + 7},
		"another bookmark": {BookmarkID: "not-the-active-one", Generation: current.Generation},
	} {
		if _, err := h.mutate.DeleteMarker(ctx, scope, "fixtures", "folder-marker/", marker.SelfObject.ContentFingerprint); !errors.Is(err, ErrMutationScopeChanged) {
			t.Fatalf("marker, %s: err = %v, want the scope refusal", name, err)
		}
		if _, err := h.mutate.DeleteManifest(ctx, scope, "fixtures", "external-dlo", manifest.ContentFingerprint); !errors.Is(err, ErrMutationScopeChanged) {
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

// ADR 0094 decision 3: the comparison described the assembled content and
// the literal read describes the stored manifest, so a manifest of the same
// kind written back after the DELETE is unverified, while a plain object
// written back is reported as the successor.
func TestManifestDeleteSameKindWrittenBackIsUnverified(t *testing.T) {
	h := newHarness(t)
	h.connect(t)
	ctx := context.Background()
	shown := shownObject(t, h, "external-dlo")
	placed := false
	h.mock.AfterRequest = func(method, path string) {
		if method == "DELETE" && strings.HasSuffix(path, "/fixtures/external-dlo") && !placed {
			placed = true
			h.mock.PutObject("fixtures", "external-dlo", swifttest.Object{Bytes: 0, ContentType: "video/mp4", Manifest: "external-dlo-segments/wedding-video/"})
		}
	}
	_, err := h.mutate.DeleteManifest(ctx, h.portrait(), "fixtures", "external-dlo", shown.ContentFingerprint)
	if !errors.Is(err, swiftcontrol.ErrUnverifiedDelete) || !placed {
		t.Fatalf("err = %v placed = %v", err, placed)
	}
	facts := factsOfOperation(t, h, "manifest.delete")
	if len(facts) != 1 || facts[0].Outcome != mutationfacts.OutcomeUnverified || !strings.Contains(facts[0].Summary, "a manifest of the kind that was shown") {
		t.Fatalf("facts = %+v", facts)
	}
}

func TestManifestDeletePlainSuccessorIsReported(t *testing.T) {
	h := newHarness(t)
	h.connect(t)
	ctx := context.Background()
	shown := shownObject(t, h, "external-dlo")
	placed := false
	h.mock.AfterRequest = func(method, path string) {
		if method == "DELETE" && strings.HasSuffix(path, "/fixtures/external-dlo") && !placed {
			placed = true
			h.mock.PutObject("fixtures", "external-dlo", swifttest.Object{Bytes: 17, ContentType: "text/plain", ETag: "00000000000000000000000000000017"})
		}
	}
	result, err := h.mutate.DeleteManifest(ctx, h.portrait(), "fixtures", "external-dlo", shown.ContentFingerprint)
	if err != nil || !placed || result.Outcome != "applied" || result.ObjectAfter == nil || result.ObjectAfter.Class != "plain" || result.ObjectAfter.ContentLength != 17 {
		t.Fatalf("result = %+v, err = %v, placed = %v", result, err, placed)
	}
	facts := factsOfOperation(t, h, "manifest.delete")
	if len(facts) != 1 || facts[0].Outcome != mutationfacts.OutcomeApplied || !strings.Contains(facts[0].Summary, "the name now holds a plain object of 17 bytes") {
		t.Fatalf("facts = %+v", facts)
	}
}

// ADR 0094 decision 2: a marker written back after its DELETE has the
// identity that was shown, so the delete is unverified rather than claimed.
func TestMarkerDeleteWrittenBackIsUnverified(t *testing.T) {
	h := newHarness(t)
	h.connect(t)
	ctx := context.Background()
	marker, err := h.inspector.Prefix(ctx, h.bookmarkID(), "fixtures", "folder-marker/")
	if err != nil || marker.SelfObject == nil {
		t.Fatalf("marker detail = %+v, %v", marker, err)
	}
	placed := false
	h.mock.AfterRequest = func(method, path string) {
		if method == "DELETE" && strings.HasSuffix(path, "/fixtures/folder-marker/") && !placed {
			placed = true
			h.mock.PutObject("fixtures", "folder-marker/", swifttest.Object{Bytes: 0, ContentType: "application/directory"})
		}
	}
	_, err = h.mutate.DeleteMarker(ctx, h.portrait(), "fixtures", "folder-marker/", marker.SelfObject.ContentFingerprint)
	if !errors.Is(err, swiftcontrol.ErrUnverifiedDelete) || !placed {
		t.Fatalf("err = %v placed = %v", err, placed)
	}
	facts := factsOfOperation(t, h, "marker.delete")
	if len(facts) != 1 || facts[0].Outcome != mutationfacts.OutcomeUnverified || !strings.Contains(facts[0].Summary, "the identity that was shown") {
		t.Fatalf("facts = %+v", facts)
	}
}

// ADR 0094 decisions 1 and 2: a refusal at the comparison keeps what was
// found, so the durable record explains it: the class, length, entity tag
// and content type beside both identities, with the comparison's
// transaction identifier, and no DELETE sent.
func TestComparisonRefusalKeepsItsEvidence(t *testing.T) {
	ctx := context.Background()
	t.Run("a manifest replaced by an empty plain object", func(t *testing.T) {
		h := newHarness(t)
		h.connect(t)
		shown := shownObject(t, h, "external-dlo")
		h.mock.PutObject("fixtures", "external-dlo", swifttest.Object{Bytes: 0, ContentType: "application/octet-stream"})
		requests := h.mock.Requests()
		_, err := h.mutate.DeleteManifest(ctx, h.portrait(), "fixtures", "external-dlo", shown.ContentFingerprint)
		var refusal *swiftcontrol.ShownRefusalError
		if !errors.As(err, &refusal) || refusal.FoundClass != "plain" || refusal.Expected != shown.ContentFingerprint {
			t.Fatalf("err = %v, want a typed refusal carrying the observation", err)
		}
		facts := factsOfOperation(t, h, "manifest.delete")
		if len(facts) != 1 || facts[0].Outcome != mutationfacts.OutcomeFailed || len(facts[0].TransIDs) == 0 ||
			!strings.Contains(facts[0].Summary, "a plain of 0 bytes") || !strings.Contains(facts[0].Summary, "application/octet-stream") ||
			!strings.Contains(facts[0].Summary, "expected identity "+shown.ContentFingerprint) || strings.Contains(facts[0].Summary, "external-dlo") {
			t.Fatalf("facts = %+v", facts)
		}
		if got := h.mock.Requests() - requests; got != 1 {
			t.Fatalf("the refusal sent %d requests, want the comparison HEAD only", got)
		}
	})
	t.Run("a marker replaced by an empty object of another type", func(t *testing.T) {
		h := newHarness(t)
		h.connect(t)
		marker, err := h.inspector.Prefix(ctx, h.bookmarkID(), "fixtures", "folder-marker/")
		if err != nil || marker.SelfObject == nil {
			t.Fatalf("marker detail = %+v, %v", marker, err)
		}
		h.mock.PutObject("fixtures", "folder-marker/", swifttest.Object{Bytes: 0, ContentType: "text/plain"})
		_, err = h.mutate.DeleteMarker(ctx, h.portrait(), "fixtures", "folder-marker/", marker.SelfObject.ContentFingerprint)
		var refusal *swiftcontrol.ShownRefusalError
		if !errors.As(err, &refusal) || refusal.FoundType != "text/plain" || refusal.Found != marker.SelfObject.ContentFingerprint {
			t.Fatalf("err = %v, want a typed refusal naming the content type with the identical fingerprint", err)
		}
		facts := factsOfOperation(t, h, "marker.delete")
		if len(facts) != 1 || facts[0].Outcome != mutationfacts.OutcomeFailed || len(facts[0].TransIDs) == 0 ||
			!strings.Contains(facts[0].Summary, "content type text/plain") || strings.Contains(facts[0].Summary, "folder-marker") {
			t.Fatalf("facts = %+v", facts)
		}
	})
}
