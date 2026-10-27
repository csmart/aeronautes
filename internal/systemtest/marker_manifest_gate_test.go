package systemtest

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/csmart/aeronautes/internal/app"
	"github.com/csmart/aeronautes/internal/mutationfacts"
	"github.com/csmart/aeronautes/internal/swiftcontrol"
)

// On the owned Swift, with a writer acting at each boundary the shared
// protocol has (ADR 0094 decisions 2 and 3): a replacement landing after the
// comparison read and before the DELETE is deleted, which is ADR 0085's
// accepted exposure, measured here and not prevented; a manifest of the same
// kind written back after the DELETE is unverified, because the literal read
// cannot tell it from the one shown; a plain object written after the DELETE
// of a manifest is reported as its successor; a marker written back after
// its DELETE is unverified; and a real object written after a marker's DELETE
// is reported as its successor. A changed identity before the comparison is
// the producers' conflict case.
func TestMarkerManifestDeleteExposureOnRealSwift(t *testing.T) {
	if os.Getenv("AERONAUTES_SAIO_AUTH_URL") == "" {
		t.Skip("owned Swift endpoint not supplied")
	}
	h := New(t)
	ctx := context.Background()
	c := h.Container(h.Primary, "mm-gate")
	segs := h.Container(h.Primary, "mm-gate-segs")
	account := h.Primary.storageURL[strings.LastIndex(h.Primary.storageURL, "/v1/"):]
	g := newSwiftGate(t, h.Primary.authURL)
	bookmark := gatedSwift(t, h, g)
	objectPath := func(name string) string { return account + "/" + c + "/" + name }
	dlo := func(prefix string) map[string]string {
		return map[string]string{"X-Object-Manifest": segs + "/" + prefix, "Content-Type": "video/mp4"}
	}

	h.Seed(h.Primary, segs, "a-0001", []byte("segment"), textType)
	h.Seed(h.Primary, segs, "b-0001", []byte("another"), textType)
	h.Seed(h.Primary, c, "keep.txt", []byte("collateral that must survive"), textType)
	for _, name := range []string{"exposed.dlo", "same-kind.dlo", "plain-after.dlo"} {
		h.rawPut(h.Primary, "/"+c+"/"+name, nil, dlo("a-"))
	}
	for _, name := range []string{"exposed/", "same-after/", "object-after/"} {
		h.Seed(h.Primary, c, name, nil, directoryType)
	}

	type gateCase struct {
		name        string
		marker      bool
		before      func(token string)
		after       func(token string)
		wantOutcome string
		wantErr     bool
		check       func(t *testing.T, result app.MutationResultDTO)
	}
	cases := []gateCase{
		{name: "exposed.dlo", before: func(token string) {
			g.put(t, token, objectPath("exposed.dlo"), nil, dlo("b-"))
		}, wantOutcome: "applied", check: func(t *testing.T, result app.MutationResultDTO) {
			if result.ObjectAfter != nil {
				t.Fatalf("successor = %+v, want an observed absence", result.ObjectAfter)
			}
			if _, err := h.Inspector.Object(ctx, bookmark, c, "exposed.dlo"); !swiftcontrol.IsNotFound(err) {
				t.Fatalf("the replacement inside the window survived: %v", err)
			}
		}},
		{name: "same-kind.dlo", after: func(token string) {
			g.put(t, token, objectPath("same-kind.dlo"), nil, dlo("b-"))
		}, wantErr: true},
		{name: "plain-after.dlo", after: func(token string) {
			g.put(t, token, objectPath("plain-after.dlo"), []byte("a plain successor"), textType)
		}, wantOutcome: "applied", check: func(t *testing.T, result app.MutationResultDTO) {
			if result.ObjectAfter == nil || result.ObjectAfter.Class != "plain" || result.ObjectAfter.ContentLength != int64(len("a plain successor")) {
				t.Fatalf("successor = %+v, want the plain object the literal read found", result.ObjectAfter)
			}
		}},
		{name: "exposed/", marker: true, before: func(token string) {
			g.put(t, token, objectPath("exposed/"), []byte("landed inside the window"), textType)
		}, wantOutcome: "applied", check: func(t *testing.T, result app.MutationResultDTO) {
			if result.ObjectAfter != nil {
				t.Fatalf("successor = %+v, want an observed absence", result.ObjectAfter)
			}
		}},
		{name: "same-after/", marker: true, after: func(token string) {
			g.put(t, token, objectPath("same-after/"), nil, directoryType)
		}, wantErr: true},
		{name: "object-after/", marker: true, after: func(token string) {
			g.put(t, token, objectPath("object-after/"), []byte("a real object now"), textType)
		}, wantOutcome: "applied", check: func(t *testing.T, result app.MutationResultDTO) {
			if result.ObjectAfter == nil || result.ObjectAfter.ContentLength != int64(len("a real object now")) {
				t.Fatalf("successor = %+v, want the object the literal read found", result.ObjectAfter)
			}
		}},
	}
	keepBefore, keepBody := literalHeadStatus(t, h, h.Primary, c, "keep.txt"), rawBody(t, h, h.Primary, c, "keep.txt")
	for _, tc := range cases {
		var identity, operation string
		if tc.marker {
			shown, err := h.Inspector.Prefix(ctx, bookmark, c, tc.name)
			if err != nil || shown.SelfObject == nil {
				t.Fatalf("%s: %+v, %v", tc.name, shown, err)
			}
			identity, operation = shown.SelfObject.ContentFingerprint, "marker.delete"
		} else {
			shown, err := h.Inspector.Object(ctx, bookmark, c, tc.name)
			if err != nil {
				t.Fatalf("%s: %v", tc.name, err)
			}
			identity, operation = shown.ContentFingerprint, "manifest.delete"
		}
		g.mu.Lock()
		g.name, g.beforeDelete, g.afterDelete, g.fired = tc.name, tc.before, tc.after, map[string]bool{}
		g.mu.Unlock()
		var result app.MutationResultDTO
		var err error
		if tc.marker {
			result, err = h.Mutate.DeleteMarker(ctx, h.ActiveScope(ctx), c, tc.name, identity)
		} else {
			result, err = h.Mutate.DeleteManifest(ctx, h.ActiveScope(ctx), c, tc.name, identity)
		}
		if !(g.fired["before"] || g.fired["after"]) {
			t.Fatalf("%s: the writer never acted", tc.name)
		}
		facts := factsFor(t, h, operation, c+"/"+tc.name)
		if tc.wantErr {
			if !errors.Is(err, swiftcontrol.ErrUnverifiedDelete) {
				t.Fatalf("%s: err = %v, want unverified", tc.name, err)
			}
			if len(facts) != 1 || facts[0].Outcome != mutationfacts.OutcomeUnverified {
				t.Fatalf("%s: facts = %+v", tc.name, facts)
			}
		} else {
			if err != nil || result.Outcome != tc.wantOutcome {
				t.Fatalf("%s: result = %+v, err = %v", tc.name, result, err)
			}
			if len(facts) != 1 || facts[0].Outcome != mutationfacts.OutcomeApplied || distinctIDs(facts[0].TransIDs) < 2 {
				t.Fatalf("%s: facts = %+v", tc.name, facts)
			}
			tc.check(t, result)
		}
		if status, body := literalHeadStatus(t, h, h.Primary, c, "keep.txt"), rawBody(t, h, h.Primary, c, "keep.txt"); status != keepBefore || body != keepBody {
			t.Fatalf("%s: the neighbour changed: %d %q", tc.name, status, body)
		}
	}
	// The segments were never touched by any case.
	for _, name := range []string{"a-0001", "b-0001"} {
		if status := literalHeadStatus(t, h, h.Primary, segs, name); status != 200 {
			t.Fatalf("segment %s: literal HEAD %d", name, status)
		}
	}
}
