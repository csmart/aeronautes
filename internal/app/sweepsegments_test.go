package app

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/csmart/aeronautes/internal/jobs"
	"github.com/csmart/aeronautes/internal/swifttest"
)

// putSLO stores a static large object over whole segments whose recorded
// hashes match their bytes, in the order given, without resetting either
// container. Each segment is "name=content" and is stored; a bare "name" is
// named by the manifest and not stored, for one a test stored already with
// the content "0123456789".
func putSLO(h *harness, container, name, segContainer string, segments ...string) {
	entries := make([]string, 0, len(segments))
	for _, segment := range segments {
		seg, content, stored := strings.Cut(segment, "=")
		if stored {
			seedObject(h.mock, segContainer, seg, content, nil)
		} else {
			content = "0123456789"
		}
		entries = append(entries, fmt.Sprintf(`{"name":"/%s/%s","hash":"%s","bytes":%d}`, segContainer, seg, md5hex(content), len(content)))
	}
	putManifest(h, container, name, "["+strings.Join(entries, ",")+"]")
}

func putManifest(h *harness, container, name, manifestJSON string) {
	h.mock.PutObject(container, name, swifttest.Object{
		Bytes: 20, SLO: true, SLOEtag: "0f0f0f0f0f0f0f0f0f0f0f0f0f0f0f0f",
		SLOManifestJSON: manifestJSON, ContentType: "application/octet-stream",
	})
}

func withSegments(bookmark, container, prefix string, mode DeleteMode) DeleteRequestDTO {
	req := DeleteRequestDTO{BookmarkID: bookmark, Container: container, Prefix: prefix, Mode: mode, Segments: true}
	req.Confirm = deleteConfirmation(req)
	return req
}

// deletesOf counts the DELETEs the store was sent for one object.
func deletesOf(h *harness, container, name string) int {
	count := 0
	for _, line := range h.mock.RequestLog() {
		if line == "DELETE /v1/AUTH_test/"+container+"/"+name {
			count++
		}
	}
	return count
}

func leftEvents(t *testing.T, h *harness, bookmark string) map[string]string {
	t.Helper()
	events, err := h.auditSvc.RecentFor(context.Background(), bookmark, 200)
	if err != nil {
		t.Fatal(err)
	}
	left := map[string]string{}
	for _, e := range events {
		if e.Operation == "delete.segments.left" {
			left[e.Resource] = e.Summary
		}
	}
	return left
}

// ADR 0119 decisions 4 to 6: with the option, a prefix deletion deletes each
// large object's manifest and then its segments, static and dynamic, each
// once and as members of the job, and touches nothing a manifest outside the
// scope names.
func TestSweepWithSegmentsDeletesEachLargeObjectsSegments(t *testing.T) {
	h, service, bookmark := deleteHarness(t)
	ctx := context.Background()
	seedObject(h.mock, "media", "scope/plain.txt", "plain", nil)
	putSLO(h, "media", "scope/big.slo", "media_segments", "scope/big.slo/0001=0123456789", "scope/big.slo/0002=abcdefghij", "scope/big.slo/empty=")
	for i := range 3 {
		seedObject(h.mock, "media_segments", fmt.Sprintf("dyn/pfx/00000%d", i), "seg", nil)
	}
	h.mock.PutObject("media", "scope/dyn.bin", swifttest.Object{Manifest: "media_segments/dyn/pfx"})
	// A neighbour outside the scope, and an object in the segments container
	// that no manifest in the scope names.
	putSLO(h, "media", "keep/other.slo", "media_segments", "keep/other.slo/0001=klmnopqrst")
	seedObject(h.mock, "media_segments", "unrelated", "nobody's segment", nil)

	jobID, err := service.StartDelete(ctx, withSegments(bookmark, "media", "scope/", ModePrefix))
	if err != nil {
		t.Fatal(err)
	}
	job := awaitTerminal(t, h, jobID)
	wire := h.mock.RequestLog()
	// Three rows of the scope, three static segments and three dynamic ones.
	if job.State != "complete" || job.Counters.ObjectsPlanned != 9 || job.Counters.ObjectsDeleted != 9 {
		t.Fatalf("job = %+v", job)
	}
	if !strings.Contains(job.Settings, `"segments":true`) {
		t.Fatalf("effective configuration = %q", job.Settings)
	}
	gone := map[string][]string{
		"media":          {"scope/plain.txt", "scope/big.slo", "scope/dyn.bin"},
		"media_segments": {"scope/big.slo/0001", "scope/big.slo/0002", "scope/big.slo/empty", "dyn/pfx/000000", "dyn/pfx/000001", "dyn/pfx/000002"},
	}
	// A segment is deleted by one DELETE, an empty one included: nothing is
	// read to find out what an empty row holds.
	if requests := requestsFor(wire, "media_segments", "scope/big.slo/empty"); len(requests) != 1 || requests[0] != "DELETE" {
		t.Fatalf("the empty segment's requests = %v, want its one DELETE", requests)
	}
	items := itemsOf(t, h, jobID)
	for container, names := range gone {
		for _, name := range names {
			if stillReadable(t, h, container, name) {
				t.Fatalf("%s/%s is still readable", container, name)
			}
			if deletes := deletesOf(h, container, name); deletes != 1 {
				t.Fatalf("%s/%s: %d DELETEs, want one", container, name, deletes)
			}
			item := items[container+"/"+name]
			if item.Execution == nil || item.Execution.Result != jobs.ItemDeleted || item.IssuedAt.IsZero() {
				t.Fatalf("%s/%s: item = %+v", container, name, item)
			}
		}
	}
	for _, kept := range [][2]string{{"media", "keep/other.slo"}, {"media_segments", "keep/other.slo/0001"}, {"media_segments", "unrelated"}} {
		if !stillReadable(t, h, kept[0], kept[1]) {
			t.Fatalf("%s/%s was touched", kept[0], kept[1])
		}
	}
	for _, line := range h.mock.RequestLog() {
		if strings.Contains(line, "multipart-manifest=delete") || strings.Contains(line, "bulk-delete") {
			t.Fatalf("a segment-expanding or bulk request was sent: %s", line)
		}
	}
	if left := leftEvents(t, h, bookmark); len(left) != 0 {
		t.Fatalf("segments reported left: %v", left)
	}
}

// ADR 0119 decision 2: a deletion that takes segments is armed by other
// words than one that does not, checked before any job exists.
func TestSweepWithSegmentsIsArmedByItsOwnWords(t *testing.T) {
	h, service, bookmark := deleteHarness(t)
	ctx := context.Background()
	putSLO(h, "media", "scope/big.slo", "media_segments", "scope/big.slo/0001=0123456789")
	for name, req := range map[string]DeleteRequestDTO{
		"the plain words with the option": {BookmarkID: bookmark, Container: "media", Prefix: "scope/", Mode: ModePrefix, Segments: true, Confirm: "media/scope/"},
		"the option's words without it":   {BookmarkID: bookmark, Container: "media", Prefix: "scope/", Mode: ModePrefix, Confirm: "media/scope/ and segments"},
		"a container with the option":     {BookmarkID: bookmark, Container: "media", Mode: ModeDeleteContainer, Segments: true, Confirm: "delete media"},
		// ADR 0121 decision 3 holds with the option too: the bare name starts
		// neither container job, the words that empty a container never delete
		// it, nor the deletion's words empty it.
		"the name for a deletion":            {BookmarkID: bookmark, Container: "media", Mode: ModeDeleteContainer, Segments: true, Confirm: "media and segments"},
		"the name for an emptying":           {BookmarkID: bookmark, Container: "media", Mode: ModeEmpty, Segments: true, Confirm: "media and segments"},
		"an emptying's words for a deletion": {BookmarkID: bookmark, Container: "media", Mode: ModeDeleteContainer, Segments: true, Confirm: "empty media and segments"},
		"a deletion's words for an emptying": {BookmarkID: bookmark, Container: "media", Mode: ModeEmpty, Segments: true, Confirm: "delete media and segments"},
		"an emptying with the option":        {BookmarkID: bookmark, Container: "media", Mode: ModeEmpty, Segments: true, Confirm: "empty media"},
	} {
		if jobID, err := service.StartDelete(ctx, req); err == nil || !strings.Contains(err.Error(), "does not match") {
			t.Fatalf("%s: job %q, err %v; want the confirmation refused", name, jobID, err)
		}
	}
	if rows := jobCount(t, h); rows != 0 {
		t.Fatalf("the refusals left %d job rows", rows)
	}
	if !stillReadable(t, h, "media", "scope/big.slo") || !stillReadable(t, h, "media_segments", "scope/big.slo/0001") {
		t.Fatal("a refused start deleted something")
	}
	if jobID, err := service.StartDelete(ctx, DeleteRequestDTO{BookmarkID: bookmark, Container: "media", Mode: ModeDeleteContainer, Segments: true, Confirm: "delete media and segments"}); err != nil {
		t.Fatalf("a container armed with its own words: %v", err)
	} else if job := awaitTerminal(t, h, jobID); job.State != "complete" {
		t.Fatalf("job = %+v", job)
	}
}

// ADR 0119 decision 3: where the container keeps versions, a kept version
// still names the segments, so the option is refused before any job, in
// every versioning mode, and nothing is deleted.
func TestSweepWithSegmentsIsRefusedWhereVersionsAreKept(t *testing.T) {
	for name, enable := range map[string]func(*swifttest.Server){
		"versions location": func(m *swifttest.Server) { m.EnableVersionArchive("media", "archive", false) },
		"history location":  func(m *swifttest.Server) { m.EnableVersionArchive("media", "archive", true) },
		"object versioning": func(m *swifttest.Server) { m.EnableModernVersioning("media") },
	} {
		t.Run(name, func(t *testing.T) {
			h, service, bookmark := deleteHarness(t)
			putSLO(h, "media", "scope/big.slo", "media_segments", "scope/big.slo/0001=0123456789")
			enable(h.mock)
			jobID, err := service.StartDelete(context.Background(), withSegments(bookmark, "media", "scope/", ModePrefix))
			if err == nil || !strings.Contains(err.Error(), "keeps versions") {
				t.Fatalf("job %q, err %v; want the option refused", jobID, err)
			}
			if !stillReadable(t, h, "media", "scope/big.slo") || !stillReadable(t, h, "media_segments", "scope/big.slo/0001") {
				t.Fatal("something was deleted")
			}
		})
	}
}

// ADR 0119 decision 4: a large object whose segments the rules cannot bound
// loses its manifest only, as without the option; every segment stays, the
// reason is recorded and the job is partial.
func TestSweepWithSegmentsLeavesWhatItCannotBound(t *testing.T) {
	part := fmt.Sprintf(`{"name":"/media_segments/big/0001","hash":"%s","bytes":10`, md5hex("0123456789"))
	for name, tc := range map[string]struct {
		seed   func(t *testing.T, h *harness)
		reason string
	}{
		"a ranged entry": {func(t *testing.T, h *harness) {
			putManifest(h, "media", "scope/in/big", "["+part+`,"range":"0-4"}]`)
		}, "nested, ranged or inline"},
		"a nested manifest": {func(t *testing.T, h *harness) {
			putManifest(h, "media", "scope/in/big", "["+part+`,"sub_slo":true}]`)
		}, "nested, ranged or inline"},
		"an entry with no recorded entity tag": {func(t *testing.T, h *harness) {
			seedObject(h.mock, "media_segments", "big/0002", "abcdefghij", nil)
			putManifest(h, "media", "scope/in/big", "["+part+`},{"name":"/media_segments/big/0002","bytes":10}]`)
		}, "no recorded entity tag"},
		"an entry with an empty entity tag": {func(t *testing.T, h *harness) {
			putManifest(h, "media", "scope/in/big", `[{"name":"/media_segments/big/0001","hash":"","bytes":10}]`)
		}, "no recorded entity tag"},
		"a dynamic manifest that reads a name outside its namespace": {func(t *testing.T, h *harness) {
			seedObject(h.mock, "media_segments", "big0/other", "another object's", nil)
			h.mock.PutObject("media", "scope/in/big", swifttest.Object{Manifest: "media_segments/big"})
		}, "outside its own namespace"},
		"a dynamic manifest whose namespace is wider than the scope": {func(t *testing.T, h *harness) {
			h.mock.PutObject("media", "scope/in/big", swifttest.Object{Manifest: "media/scope"})
		}, "wider than the scope"},
		"a dynamic manifest whose segments' container another job is staging into": {func(t *testing.T, h *harness) {
			h.mock.PutObject("media", "scope/in/big", swifttest.Object{Manifest: "media_segments/big"})
			owner, _, err := h.transfer.runner.admit(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(owner.finishStart)
			// A transfer into another folder of the same container: it does not
			// overlap the scope, and it stages in the segments container too.
			if err := owner.claim(stageClaims("auth_test", "media", "elsewhere/")...); err != nil {
				t.Fatal(err)
			}
		}, "another job is writing into the container"},
		"a segment in a live job's stage": {func(t *testing.T, h *harness) {
			const other = "0f1e2d3c4b5a6978"
			staged := ".aeronautes/stage/" + other + "/big/0001"
			putSLO(h, "media", "scope/in/big", "media_segments", staged+"=0123456789")
			runner := h.transfer.runner
			owner, _, err := runner.admit(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(owner.finishStart)
			if err := owner.claim(stageClaims("auth_test", "media", "elsewhere/")...); err != nil {
				t.Fatal(err)
			}
			runner.mu.Lock()
			owner.jobID = other
			owner.claims[1].path = ".aeronautes/stage/"
			runner.mu.Unlock()
		}, "is writing"},
	} {
		t.Run(name, func(t *testing.T) {
			h, service, bookmark := deleteHarness(t)
			seedObject(h.mock, "media_segments", "big/0001", "0123456789", nil)
			// Inside the widest namespace a case names, outside the scope.
			seedObject(h.mock, "media", "scope/neighbour", "not in the scope", nil)
			tc.seed(t, h)
			jobID, err := service.StartDelete(context.Background(), withSegments(bookmark, "media", "scope/in/", ModePrefix))
			if err != nil {
				t.Fatal(err)
			}
			job := awaitTerminal(t, h, jobID)
			if job.State != "partial" || !strings.Contains(job.Error, "the segments of 1 large object(s) were left") || !strings.Contains(job.Error, tc.reason) {
				t.Fatalf("job = %+v", job)
			}
			if stillReadable(t, h, "media", "scope/in/big") {
				t.Fatal("the manifest survived; it is deleted manifest-only as without the option")
			}
			if deletes := deletesUnder(h, "media_segments"); len(deletes) != 0 {
				t.Fatalf("segment DELETEs = %v, want none", deletes)
			}
			if !stillReadable(t, h, "media", "scope/neighbour") {
				t.Fatal("an object outside the scope was deleted")
			}
			if summary := leftEvents(t, h, bookmark)["media/scope/in/big"]; !strings.Contains(summary, tc.reason) {
				t.Fatalf("the audit trail does not say why the segments were left: %q", summary)
			}
		})
	}
}

// ADR 0119 decision 5: segments are deleted only after their manifest is
// recorded deleted. A manifest the store refuses to delete is failed, and
// one whose DELETE reply is lost is unverified; either way every segment is
// left and settled as not attempted.
func TestSweepWithSegmentsLeavesSegmentsWhenTheManifestIsNotDeleted(t *testing.T) {
	for name, tc := range map[string]struct {
		arm          func(h *harness)
		state        string
		manifest     jobs.ItemResult
		manifestGone bool
	}{
		"the store refuses the manifest's DELETE": {func(h *harness) {
			h.mock.RefuseObjectDELETE("media", "scope/big.slo", http.StatusForbidden)
		}, "failed", jobs.ItemFailed, false},
		"the manifest's DELETE reply is lost": {func(h *harness) { h.mock.DropNextObjectDELETE() }, "partial", jobs.ItemUnverified, true},
	} {
		t.Run(name, func(t *testing.T) {
			h, service, bookmark := deleteHarness(t)
			putSLO(h, "media", "scope/big.slo", "media_segments", "scope/big.slo/0001=0123456789", "scope/big.slo/0002=abcdefghij")
			tc.arm(h)
			jobID, err := service.StartDelete(context.Background(), withSegments(bookmark, "media", "scope/", ModePrefix))
			if err != nil {
				t.Fatal(err)
			}
			job := awaitTerminal(t, h, jobID)
			h.mock.BeforeRequest = nil
			if job.State != tc.state {
				t.Fatalf("job = %+v, want %s", job, tc.state)
			}
			items := itemsOf(t, h, jobID)
			if manifest := items["media/scope/big.slo"]; manifest.Execution == nil || manifest.Execution.Result != tc.manifest {
				t.Fatalf("manifest item = %+v, want %s", manifest, tc.manifest)
			}
			if stillReadable(t, h, "media", "scope/big.slo") == tc.manifestGone {
				t.Fatalf("manifest gone = %v, want %v", !tc.manifestGone, tc.manifestGone)
			}
			for _, segment := range []string{"scope/big.slo/0001", "scope/big.slo/0002"} {
				if !stillReadable(t, h, "media_segments", segment) {
					t.Fatalf("segment %s was deleted though its manifest was not recorded deleted", segment)
				}
				if item := items["media_segments/"+segment]; item.Execution == nil || item.Execution.Result != jobs.ItemNotAttempted {
					t.Fatalf("segment %s: item = %+v, want not attempted", segment, item)
				}
			}
			if deletes := deletesUnder(h, "media_segments"); len(deletes) != 0 {
				t.Fatalf("segment DELETEs = %v, want none", deletes)
			}
		})
	}
}

// ADR 0119 decision 6: a segment is a member of the job once. One that two
// manifests of the scope name, and one that is itself a row of the scope,
// static or dynamic, is requested once, and the job is complete.
func TestSweepWithSegmentsRequestsASharedSegmentOnce(t *testing.T) {
	h, service, bookmark := deleteHarness(t)
	ctx := context.Background()
	seedObject(h.mock, "mixed", "parts/shared", "0123456789", nil)
	putSLO(h, "mixed", "a.slo", "mixed", "parts/shared", "parts/a=abcdefghij")
	putSLO(h, "mixed", "b.slo", "mixed", "parts/shared", "elsewhere/b=klmnopqrst")
	seedObject(h.mock, "mixed", "dyn/0001", "seg", nil)
	h.mock.PutObject("mixed", "c.dlo", swifttest.Object{Manifest: "mixed/dyn/"})
	// One segment lies outside the scope's container and is named twice.
	seedObject(h.mock, "mixed_segments", "twice", "0123456789", nil)
	putSLO(h, "mixed", "d.slo", "mixed_segments", "twice")
	putSLO(h, "mixed", "e.slo", "mixed_segments", "twice")

	jobID, err := service.StartDelete(ctx, withSegments(bookmark, "mixed", "", ModeEmpty))
	if err != nil {
		t.Fatal(err)
	}
	job := awaitTerminal(t, h, jobID)
	if job.State != "complete" {
		t.Fatalf("job = %+v", job)
	}
	for _, object := range [][2]string{
		{"mixed", "a.slo"}, {"mixed", "b.slo"}, {"mixed", "c.dlo"}, {"mixed", "d.slo"}, {"mixed", "e.slo"},
		{"mixed", "parts/shared"}, {"mixed", "parts/a"}, {"mixed", "elsewhere/b"}, {"mixed", "dyn/0001"}, {"mixed_segments", "twice"},
	} {
		if deletes := deletesOf(h, object[0], object[1]); deletes != 1 {
			t.Fatalf("%s/%s: %d DELETEs, want one", object[0], object[1], deletes)
		}
		if stillReadable(t, h, object[0], object[1]) {
			t.Fatalf("%s/%s is still readable", object[0], object[1])
		}
	}
	totals, err := h.jobsSvc.ItemTotals(ctx, bookmark, jobID)
	if err != nil || totals.Admitted != 10 || totals.Deleted != 10 {
		t.Fatalf("totals = %+v, %v; want each of the ten objects admitted and deleted once", totals, err)
	}
}

// ADR 0119 decision 4: a dynamic large object whose namespace is the scope
// itself has its segments deleted as the scope's rows, each once, with no
// second sweep and nothing recorded as left.
func TestSweepWithSegmentsDeletesAManifestWhoseNamespaceIsTheScope(t *testing.T) {
	h, service, bookmark := deleteHarness(t)
	ctx := context.Background()
	segments := []string{"dyn/0001", "dyn/0002", "dyn/0003"}
	for _, segment := range segments {
		seedObject(h.mock, "same", segment, "seg", nil)
	}
	h.mock.PutObject("same", "dyn/c.dlo", swifttest.Object{Manifest: "same/dyn"})
	jobID, err := service.StartDelete(ctx, withSegments(bookmark, "same", "dyn/", ModePrefix))
	if err != nil {
		t.Fatal(err)
	}
	job := awaitTerminal(t, h, jobID)
	if job.State != "complete" || job.Counters.ObjectsDeleted != 4 {
		t.Fatalf("job = %+v", job)
	}
	for _, name := range append(segments, "dyn/c.dlo") {
		if deletes := deletesOf(h, "same", name); deletes != 1 {
			t.Fatalf("same/%s: %d DELETEs, want one", name, deletes)
		}
	}
	if left := leftEvents(t, h, bookmark); len(left) != 0 {
		t.Fatalf("segments reported left: %v", left)
	}
}

// ADR 0120 decision 6: a static and a dynamic manifest can name the same
// segments, as the published and the staged manifest of one upload do in a
// history archive. Both manifests are deleted, each segment is requested
// once, and nothing is recorded as left.
func TestSweepWithSegmentsDeletesSegmentsAStaticAndADynamicManifestShare(t *testing.T) {
	h, service, bookmark := deleteHarness(t)
	ctx := context.Background()
	putSLO(h, "archive", "a.slo", "parts", "up/0001=0123456789", "up/0002=abcdefghij", "up/0003=klmnopqrst")
	h.mock.PutObject("archive", "b.dlo", swifttest.Object{Manifest: "parts/up/"})
	jobID, err := service.StartDelete(ctx, withSegments(bookmark, "archive", "", ModeEmpty))
	if err != nil {
		t.Fatal(err)
	}
	job := awaitTerminal(t, h, jobID)
	if job.State != "complete" || job.Counters.ObjectsDeleted != 5 {
		t.Fatalf("job = %+v", job)
	}
	for _, object := range [][2]string{{"archive", "a.slo"}, {"archive", "b.dlo"}, {"parts", "up/0001"}, {"parts", "up/0002"}, {"parts", "up/0003"}} {
		if deletes := deletesOf(h, object[0], object[1]); deletes != 1 {
			t.Fatalf("%s/%s: %d DELETEs, want one", object[0], object[1], deletes)
		}
	}
	if left := leftEvents(t, h, bookmark); len(left) != 0 {
		t.Fatalf("segments reported left: %v", left)
	}
}

// ADR 0119 decision 7: a segment the store refuses to delete is failed and
// left; the job is partial, and a container deletion that did not delete
// everything it was asked leaves the container standing.
func TestSweepWithSegmentsKeepsTheContainerWhenASegmentWasNotDeleted(t *testing.T) {
	h, service, bookmark := deleteHarness(t)
	ctx := context.Background()
	putSLO(h, "media", "big.slo", "media_segments", "big.slo/0001=0123456789", "big.slo/0002=abcdefghij")
	h.mock.RefuseObjectDELETE("media_segments", "big.slo/0002", http.StatusForbidden)

	jobID, err := service.StartDelete(ctx, withSegments(bookmark, "media", "", ModeDeleteContainer))
	if err != nil {
		t.Fatal(err)
	}
	job := awaitTerminal(t, h, jobID)
	if job.State != "partial" {
		t.Fatalf("job = %+v", job)
	}
	items := itemsOf(t, h, jobID)
	if refused := items["media_segments/big.slo/0002"]; refused.Execution == nil || refused.Execution.Result != jobs.ItemFailed {
		t.Fatalf("refused segment = %+v, want failed", refused)
	}
	if !stillReadable(t, h, "media_segments", "big.slo/0002") || stillReadable(t, h, "media_segments", "big.slo/0001") {
		t.Fatal("the refused segment must stay and the other one go")
	}
	if containerDeleted(h.mock.RequestLog(), "media") {
		t.Fatal("the container was deleted by a job that did not delete everything it was asked")
	}

	// The same deletion with nothing in the way deletes the container.
	putSLO(h, "films", "big.slo", "films_segments", "big.slo/0001=0123456789")
	jobID, err = service.StartDelete(ctx, withSegments(bookmark, "films", "", ModeDeleteContainer))
	if err != nil {
		t.Fatal(err)
	}
	if job := awaitTerminal(t, h, jobID); job.State != "complete" || !containerDeleted(h.mock.RequestLog(), "films") {
		t.Fatalf("job = %+v; want the container deleted", job)
	}
	if containerDeleted(h.mock.RequestLog(), "films_segments") {
		t.Fatal("the segments container was deleted")
	}
}

// ADR 0119 decision 6 with ADR 0095 decision 3: a cancellation lands between
// members. The job is cancelled while the store holds the manifest's DELETE,
// so the manifest, already issued, finishes whole, and its segments are
// settled as not attempted; the job is partial.
func TestSweepWithSegmentsCancelledAfterTheManifest(t *testing.T) {
	h, service, bookmark := deleteHarness(t)
	ctx := context.Background()
	putSLO(h, "media", "scope/big.slo", "media_segments", "scope/big.slo/0001=0123456789", "scope/big.slo/0002=abcdefghij")
	ready := make(chan string, 1)
	var cancelled atomic.Bool
	h.mock.BeforeRequest = func(method, path string) {
		if method == "DELETE" && strings.HasSuffix(path, "/media/scope/big.slo") && cancelled.CompareAndSwap(false, true) {
			if err := h.transfer.CancelJob(ctx, <-ready); err != nil {
				t.Error(err)
			}
		}
	}
	jobID, err := service.StartDelete(ctx, withSegments(bookmark, "media", "scope/", ModePrefix))
	if err != nil {
		t.Fatal(err)
	}
	ready <- jobID
	job := awaitTerminal(t, h, jobID)
	h.mock.BeforeRequest = nil
	if job.State != "partial" {
		t.Fatalf("job = %+v", job)
	}
	items := itemsOf(t, h, jobID)
	if manifest := items["media/scope/big.slo"]; manifest.Execution == nil || manifest.Execution.Result != jobs.ItemDeleted {
		t.Fatalf("manifest item = %+v, want it finished whole and deleted", manifest)
	}
	for _, segment := range []string{"scope/big.slo/0001", "scope/big.slo/0002"} {
		if item := items["media_segments/"+segment]; item.Execution == nil || item.Execution.Result != jobs.ItemNotAttempted {
			t.Fatalf("segment %s: item = %+v, want not attempted", segment, item)
		}
		if !stillReadable(t, h, "media_segments", segment) {
			t.Fatalf("segment %s was deleted after the cancellation", segment)
		}
	}
}

// ADR 0119 decision 6 with ADR 0095 decision 4: a segment admission the
// ledger cannot record stops the job before the manifest is requested, and a
// manifest outcome it cannot record leaves every segment untouched; either
// way the job stays open with its status unavailable.
func TestSweepWithSegmentsStopsWhenARecordIsLost(t *testing.T) {
	for name, trigger := range map[string]string{
		"a segment's admission": `CREATE TRIGGER refuse_record BEFORE INSERT ON job_items WHEN NEW.item_key LIKE 'media_segments/%'
			BEGIN SELECT RAISE(ABORT,'fixture required refusal'); END`,
		"the manifest's outcome": `CREATE TRIGGER refuse_record BEFORE INSERT ON job_item_observations
			BEGIN SELECT RAISE(ABORT,'fixture required refusal'); END`,
	} {
		t.Run(name, func(t *testing.T) {
			h, service, bookmark := deleteHarness(t)
			ctx := context.Background()
			putSLO(h, "media", "scope/big.slo", "media_segments", "scope/big.slo/0001=0123456789")
			refuseSQL(t, h, trigger)
			if _, err := service.StartDelete(ctx, withSegments(bookmark, "media", "scope/", ModePrefix)); err != nil {
				t.Fatal(err)
			}
			for tick := 0; ; tick++ {
				events, err := h.auditSvc.RecentFor(ctx, bookmark, 20)
				if err != nil {
					t.Fatal(err)
				}
				noted := false
				for _, e := range events {
					noted = noted || e.Operation == "delete.prefix.unavailable"
				}
				if noted {
					break
				}
				if tick > 200 {
					t.Fatal("the job never reported its records unavailable")
				}
				awaitTick()
			}
			if deletes := deletesUnder(h, "media_segments"); len(deletes) != 0 {
				t.Fatalf("a segment was deleted after the lost record: %v", deletes)
			}
			if name == "a segment's admission" && deletesOf(h, "media", "scope/big.slo") != 0 {
				t.Fatal("the manifest was requested though its segments could not be admitted")
			}
		})
	}
}
