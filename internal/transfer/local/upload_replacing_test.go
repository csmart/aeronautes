package local

import (
	"errors"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/csmart/aeronautes/internal/swifttest"
	"github.com/csmart/aeronautes/internal/systemtest/oracle"
	transfercopy "github.com/csmart/aeronautes/internal/transfer/copy"
)

// ADR 0081 decision 1: an overwrite happens only when the caller asked for
// one, and asking means naming a different entry point rather than passing
// a value. The two reports are taken over the same selection against the
// same destination, so the only difference between them is which function
// was called.
func TestPreflightUploadReplacingAdmitsALiveNameOnlyWhenNamed(t *testing.T) {
	s, mock := session(t)
	seed(mock, "media", "live/taken", "the bytes already there")
	seed(mock, "outside", "keep", "outside sentinel")
	witness := uploadWitness(t, mock)
	before := uploadSnapshot(t, witness)
	dir := t.TempDir()
	taken := writeFile(t, dir, "taken", "the bytes this job would send")
	free := writeFile(t, dir, "free", "nothing is at this name")
	paths := []string{taken, free}

	refusing, err := PreflightUpload(t.Context(), s, "media", "live/", paths)
	if err != nil {
		t.Fatal(err)
	}
	if !refusing.Rejected() || len(refusing.Items) != 1 || refusing.Items[0].Name != "live/free" ||
		refusing.Items[0].Replaces {
		t.Fatalf("the ordinary preflight admitted a live name: rejected=%v items=%+v", refusing.Rejected(), refusing.Items)
	}
	if len(refusing.Problems) != 1 || refusing.Problems[0].Kind != "exists" || !refusing.Problems[0].Reject {
		t.Fatalf("the ordinary preflight did not refuse the collision: %+v", refusing.Problems)
	}

	replacing, err := PreflightUploadReplacing(t.Context(), s, "media", "live/", paths)
	if err != nil {
		t.Fatal(err)
	}
	if replacing.Rejected() {
		t.Fatalf("the replacing preflight still refuses: %+v", replacing.Problems)
	}
	// The collision is admitted AND still reported: it is the thing the
	// operator acknowledged, not a reason to stop and not a silence.
	if len(replacing.Problems) != 1 || replacing.Problems[0].Kind != "exists" || replacing.Problems[0].Reject ||
		replacing.Problems[0].Name != "live/taken" {
		t.Fatalf("the replacing preflight lost or refused the collision report: %+v", replacing.Problems)
	}
	byName := map[string]UploadItem{}
	for _, item := range replacing.Items {
		byName[item.Name] = item
	}
	if len(byName) != 2 {
		t.Fatalf("the replacing preflight planned %d items: %+v", len(byName), replacing.Items)
	}
	if !byName["live/taken"].Replaces {
		t.Errorf("the live name is not marked as replacing: %+v", byName["live/taken"])
	}
	if byName["live/free"].Replaces {
		t.Errorf("a name that was free is marked as replacing: %+v", byName["live/free"])
	}
	if replacing.TotalBytes != byName["live/taken"].Size+byName["live/free"].Size {
		t.Errorf("the replacing preflight's byte total omits the replaced name: %+v", replacing)
	}

	// Neither report writes anything: a preflight is a read.
	if diff := oracle.Diff(before, uploadSnapshot(t, witness)); !diff.Empty() {
		t.Fatalf("a preflight changed the account: %s", diff)
	}
}

// ADR 0081 decision 6: a replacement drops the conditional that makes a
// create race-free, because the name existing is the point, and keeps the
// declared entity tag, so bytes that did not survive the wire are refused
// by the store with the prior object intact. That is the whole of what an
// acknowledged overwrite changes about the write.
func TestNativeReplacementKeepsItsContentBinding(t *testing.T) {
	const prior = "the bytes already there"
	for _, tc := range []struct {
		name    string
		altered bool
	}{
		{"refuses an altered replacement and leaves the prior object", true},
		{"publishes an unaltered replacement", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mock := swifttest.New("test:tester", "testing")
			mock.AddContainer("media", "standard")
			seed(mock, "media", "docs/object", prior)
			// The local package's proxy always alters, so the unaltered
			// control speaks to the store directly.
			var server *httptest.Server
			if tc.altered {
				server = alteringProxy(t, mock, "/v1/AUTH_test/media/docs/object")
			} else {
				server = httptest.NewServer(mock)
				t.Cleanup(server.Close)
			}
			session := bindingSession(t, server)
			path := writeFile(t, t.TempDir(), "object", "the bytes this job planned to send")
			// The replacing entry point is what admits a live name, and
			// what marks the item, since this test is the route rather
			// than the service.
			preflight, err := PreflightUploadReplacing(t.Context(), session, "media", "docs/", []string{path})
			if err != nil || preflight.Rejected() || len(preflight.Items) != 1 {
				t.Fatalf("the replacing preflight did not admit the live name: %+v %v", preflight, err)
			}
			item := preflight.Items[0]
			item.Route = "native"
			if !item.Replaces {
				t.Fatal("the replacing preflight did not mark the item")
			}

			file, err := os.Open(path)
			if err != nil {
				t.Fatal(err)
			}
			result := nativeUpload(t.Context(), session, "media", item, file)

			live, err := session.ObjectDetail(t.Context(), "media", "docs/object")
			if err != nil {
				t.Fatalf("the name is gone after a replacement attempt: %v", err)
			}
			if tc.altered {
				if result.Result != transfercopy.ResultFailed || result.Effect != transfercopy.EffectUnresolved ||
					result.Diagnostic.Stage != transfercopy.StageCommit || result.Diagnostic.Code != transfercopy.CodeUnverified {
					t.Fatalf("an altered replacement was not refused as an unverified commit: %+v", result.Observation)
				}
				if !strings.EqualFold(strings.Trim(live.ETag, `"`), md5hex(prior)) {
					t.Fatalf("the prior object did not survive a refused replacement: %+v", live)
				}
				return
			}
			if result.Result != transfercopy.ResultCopied {
				t.Fatalf("an unaltered replacement was refused: %+v", result.Observation)
			}
			if !strings.EqualFold(strings.Trim(live.ETag, `"`), md5hex("the bytes this job planned to send")) {
				t.Fatalf("the replacement did not take: %+v", live)
			}
		})
	}
}

// The other half of decision 6, kept honest: an item the admission did NOT
// mark still carries the conditional, so a create onto a name that became
// live is a conflict rather than a silent replacement.
func TestNativeCreateStillRefusesALiveName(t *testing.T) {
	mock := swifttest.New("test:tester", "testing")
	mock.AddContainer("media", "standard")
	seed(mock, "media", "docs/object", "the bytes already there")
	server := httptest.NewServer(mock)
	t.Cleanup(server.Close)
	session := bindingSession(t, server)
	path := writeFile(t, t.TempDir(), "object", "the bytes this job planned to send")
	// An item as the ordinary preflight would never produce it for a live
	// name: unmarked, so the publication keeps its conditional.
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	item := UploadItem{Path: path, Name: "docs/object", Size: info.Size(), Route: "native"}

	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	result := nativeUpload(t.Context(), session, "media", item, file)
	if result.Result != transfercopy.ResultConflict {
		t.Fatalf("a create onto a live name was not a conflict: %+v", result.Observation)
	}
	live, err := session.ObjectDetail(t.Context(), "media", "docs/object")
	if err != nil || !strings.EqualFold(strings.Trim(live.ETag, `"`), md5hex("the bytes already there")) {
		t.Fatalf("a refused create changed the live object: %+v %v", live, err)
	}
}

// A replacement is counted where its publication is witnessed, not where
// it was planned: a marked item whose publication failed has replaced
// nothing, and a job reporting it as a replacement would be claiming an
// effect that did not happen.
func TestReplacementsAreCountedOnlyWhenWitnessed(t *testing.T) {
	marked := UploadItem{Path: "/local/object", Name: "docs/object", Size: 5, Route: "native", Replaces: true}
	free := UploadItem{Path: "/local/other", Name: "docs/other", Size: 5, Route: "native"}
	copied := uploadResult{Observation: transfercopy.Observation{
		Result: transfercopy.ResultCopied, Effect: transfercopy.EffectReportedApplied, CopiedBytes: 5}}
	for _, tc := range []struct {
		name     string
		item     UploadItem
		result   uploadResult
		replaced int64
		done     int64
	}{
		{"a marked item that published", marked, copied, 1, 1},
		{"a marked item whose publication failed", marked,
			uploadFailed(transfercopy.EffectUnresolved, transfercopy.StageCommit, transfercopy.CodeUnverified, "native-put",
				errors.New("the store refused it")), 0, 0},
		{"a marked item refused as a conflict", marked,
			uploadConflict("native-put", errors.New("the name appeared")), 0, 0},
		{"an unmarked item that published", free, copied, 0, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			run := &uploadRun{report: &Report{}}
			if err := run.observe(tc.item, tc.result); err != nil {
				t.Fatalf("observing: %v", err)
			}
			if run.report.Replaced != tc.replaced || run.report.Done != tc.done {
				t.Fatalf("replaced=%d done=%d, wanted %d and %d",
					run.report.Replaced, run.report.Done, tc.replaced, tc.done)
			}
		})
	}
}
