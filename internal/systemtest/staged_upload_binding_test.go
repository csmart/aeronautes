package systemtest

import (
	"bytes"
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/csmart/aeronautes/internal/app"
	"github.com/csmart/aeronautes/internal/jobs"
	"github.com/csmart/aeronautes/internal/swifttest"
)

// Maintained regressions for ADR 0080's staged route through the public
// service, on both fixtures. The native route's cases live beside these
// in upload_content_binding_test.go; the staged publication is a separate
// decision with a separate binding.
//
// The fault is a real store-side change rather than a rewritten request
// body, because the engine verifies what it wrote and would refuse a body
// altered in flight before the comparison this record is about was ever
// reached. Every change is made at the app's OWN first HEAD of the staged
// object, told apart from the engine's requests by the user agent, so the
// change is complete before the commit begins and no engine check can see
// it.
//
// One case is the stale-row condition ADR 0042 rejected listing evidence
// for: the segment's container row still reports the bytes it replaced,
// so an implementation that trusted the row would publish. It runs on the
// mock fixture only, and deliberately so. A deployed Swift recomputes a
// row from the bytes it stored, and it refuses a PUT whose declared
// entity tag its body contradicts, so the condition cannot be created
// over the protocol at all; it is a cluster's own inconsistency, not
// something a client can ask for. The mock's store is written directly
// for that one case, and every other case runs on both fixtures.

// appAgent is the user agent the application's own session sends
// (internal/swiftcontrol/session.go). The pinned engine sends its own, so
// this is what separates the commit's requests from the staging run's.
const appAgent = "aeronautes"

// stagedFault changes what the store holds once, at the app's first HEAD
// of the staged object, and then serves the segment listing it captured
// beforehand so the container's rows stay stale.
type stagedFault struct {
	mu sync.Mutex
	// segments are the segment object paths the engine wrote, in the
	// order it wrote them, without the storage prefix.
	segments []string
	// base is the file's own name, and staged is the staging path once the
	// job has an id. The trigger uses the staging path's SHAPE rather than
	// its exact name, because the job's id is not known until after the
	// job can already have started.
	base     string
	staged   string
	fired    bool
	fault    error
	appReads int
	livePuts int
	change   func(f *stagedFault) error
	account  *Account
	// mock is the fixture's own store when the harness is the mock one and
	// nil against the owned Swift. Only the stale-row case needs it.
	mock *swifttest.Server
}

func (f *stagedFault) observe(req *http.Request, path string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	fromApp := strings.Contains(req.Header.Get("User-Agent"), appAgent)
	onStaging := strings.Contains(path, "/.aeronautes/stage/") && strings.HasSuffix(path, "/"+f.base)
	switch {
	case req.Method == http.MethodPut && strings.Contains(path, "_segments/"):
		f.segments = append(f.segments, path)
		f.staged = ""
	case req.Method == http.MethodPut && onStaging:
		// The engine's own staging write, which is also how the plain
		// route's staged object is learned.
		f.staged = path
	case req.Method == http.MethodGet && fromApp && onStaging:
		f.appReads++
	case req.Method == http.MethodPut && fromApp && strings.HasSuffix(path, "/live/"+f.base):
		f.livePuts++
	}
	// The commit's first HEAD of the staged object: the application's own
	// request, after the engine has finished writing, so any change made
	// here is complete before the commit examines anything.
	if f.fired || !fromApp || req.Method != http.MethodHead || !onStaging {
		return
	}
	f.fired = true
	f.staged = path
	if f.change == nil {
		return
	}
	f.fault = f.change(f)
}

func TestStagedUploadContentIsBoundToTheSourceMock(t *testing.T) {
	stagedUploadContentBinding(t, findMockHarness)
}

func TestStagedUploadContentIsBoundToTheSourceRealSwift(t *testing.T) {
	stagedUploadContentBinding(t, New)
}

func stagedUploadContentBinding(t *testing.T, makeHarness func(*testing.T) *Harness) {
	t.Helper()
	// large says whether the upload is chunked into a manifest; change is
	// what the fixture does to the staged bytes, and nil is the control.
	for _, tc := range []struct {
		name string
		// mockOnly marks a condition a deployed store cannot be asked to
		// produce; the case is skipped rather than weakened there.
		mockOnly bool
		large    bool
		// stage is the boundary the refusal must name; empty means the
		// case must publish.
		stage  jobs.ItemStage
		code   jobs.ItemCode
		change func(f *stagedFault) error
	}{
		{name: "a segment replaced at its own length, with its listing row unchanged", large: true,
			stage: jobs.StageStaging, code: jobs.CodeUnverified, change: stagedSegmentReplaced},
		{name: "a segment whose container row still describes the bytes it replaced", large: true,
			mockOnly: true, stage: jobs.StageStaging, code: jobs.CodeUnverified, change: stagedStaleRow},
		{name: "a segment missing", large: true,
			stage: jobs.StageStaging, code: jobs.CodeIncomplete, change: stagedSegmentMissing},
		{name: "a segment duplicated", large: true,
			stage: jobs.StageStaging, code: jobs.CodeUnverified, change: stagedSegmentDuplicated},
		{name: "segments reordered", large: true,
			stage: jobs.StageStaging, code: jobs.CodeUnverified, change: stagedSegmentsReordered},
		{name: "a run of another job at the same total length", large: true,
			stage: jobs.StageStaging, code: jobs.CodeUnverified, change: stagedForeignRun},
		{name: "a staged plain object altered to the same length before the commit", large: false,
			stage: jobs.StageStaging, code: jobs.CodeUnverified, change: stagedPlainAltered},
		{name: "an unaltered staged large upload", large: true},
		{name: "an unaltered staged plain upload", large: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := makeHarness(t)
			if tc.mockOnly && h.MockPrimary == nil {
				t.Skip("a deployed store recomputes a listing row from the bytes it stored, so this condition cannot be created over the protocol")
			}
			container := h.Container(h.Primary, "staged-upload-binding")
			segments := container + "_segments"
			h.rawPut(h.Primary, "/"+segments, nil, nil)
			t.Cleanup(func() { h.purge(h.Primary, segments) })
			other := h.Container(h.Secondary, "staged-upload-untouched")
			h.Seed(h.Primary, container, "keep", []byte("destination sentinel"), nil)
			h.Seed(h.Secondary, other, "keep", []byte("other account sentinel"), nil)
			beforeA, beforeB := h.SnapshotNamespace(h.Primary), h.SnapshotNamespace(h.Secondary)

			const base = "subject.bin"
			body := []byte("the bytes this job planned to send, of a known length")
			if tc.large {
				body = []byte(strings.Repeat("abcdefg", 160000))
			}
			dir := t.TempDir()
			path := filepath.Join(dir, base)
			if err := os.WriteFile(path, body, 0o600); err != nil {
				t.Fatal(err)
			}

			fault := &stagedFault{base: base, change: tc.change, account: h.Primary, mock: h.MockPrimary}
			installUploadProxyHooked(t, h, h.Primary, "", nil, fault.observe)
			if _, err := h.Browse.Connect(t.Context(), h.Primary.BookmarkID); err != nil {
				t.Fatal(err)
			}
			app.WireFilePicker(h.Transfer, listingFilePicker{files: []string{path}, dir: dir})
			picked, err := h.Transfer.PickUploadFiles(t.Context(), app.SelectionScopeDTO{
				BookmarkID: h.Primary.BookmarkID, Container: container, Prefix: "live/"})
			if err != nil || picked == nil {
				t.Fatalf("pick: %+v %v", picked, err)
			}
			id, err := h.Transfer.StartUpload(t.Context(), app.UploadRequestDTO{
				BookmarkID: h.Primary.BookmarkID, Container: container, Prefix: "live/",
				SelectionToken: picked.Token, Settings: app.TransferSettingsDTO{ChunkSize: "1Mi"}})
			if err != nil {
				t.Fatal(err)
			}
			h.AwaitJob(id)
			job, err := h.Jobs.Get(t.Context(), h.Primary.BookmarkID, id)
			if err != nil {
				t.Fatal(err)
			}
			page, err := jobs.NewStore(h.database).ReadItems(t.Context(), id, 0, 100)
			if err != nil || len(page.Items) != 1 || page.Items[0].Execution == nil {
				t.Fatalf("persisted item rows = %d %v", len(page.Items), err)
			}
			item := page.Items[0]
			staged := container + "/.aeronautes/stage/" + id + "/batch/" + base

			if tc.change == nil {
				if job.State != "complete" || job.Counters.ObjectsCopied != 1 || job.Counters.ObjectsVerified != 1 ||
					job.Counters.ObjectsFailed != 0 || job.Counters.BytesCopied != int64(len(body)) {
					t.Fatalf("matching staged content was not published: state=%s counters=%+v", job.State, job.Counters)
				}
				if item.Execution.Result != jobs.ItemCopied || item.Execution.Effect != jobs.EffectReportedApplied ||
					item.Verification == nil || item.Verification.Result != jobs.ItemVerified ||
					item.Verification.Effect != jobs.EffectNotAttempted {
					t.Fatalf("a published staged upload was misrecorded: %+v %+v", item.Execution, item.Verification)
				}
				// The staged large control reads the staged content exactly
				// once: the comparison is one read per publication, not one
				// per segment and not one per retry (ADR 0080 decision 3).
				if tc.large {
					fault.mu.Lock()
					reads := fault.appReads
					fault.mu.Unlock()
					if reads != 1 {
						t.Fatalf("the staged large control read the staged content %d times, not once", reads)
					}
				}
				detail, err := h.Inspector.Object(t.Context(), h.Primary.BookmarkID, container, "live/"+base)
				if err != nil || detail.ContentLength != int64(len(body)) {
					t.Fatalf("published object = %+v %v", detail, err)
				}
				h.AssertNoCollateral(beforeB, h.SnapshotNamespace(h.Secondary))
				return
			}

			fault.mu.Lock()
			fired, faultErr, livePuts := fault.fired, fault.fault, fault.livePuts
			fault.mu.Unlock()
			if !fired {
				t.Fatal("the fixture never changed anything; the case proves nothing")
			}
			if faultErr != nil {
				t.Fatalf("the fixture's change failed: %v", faultErr)
			}
			if livePuts != 0 {
				t.Fatalf("a staged object that is not the source was published: %d live writes", livePuts)
			}
			if job.State == "complete" || job.Counters.ObjectsCopied != 0 {
				t.Fatalf("a staged object that is not the source was published: state=%s counters=%+v", job.State, job.Counters)
			}
			// The refusal is a typed failure with an unresolved effect,
			// because the engine's remote writes already happened, and its
			// reason matters as much as its boundary: a refusal of content
			// that ended early proves less than one of content that is the
			// right length and the wrong bytes (ADR 0080 decision 7).
			if item.Execution.Result != jobs.ItemFailed || item.Execution.Effect != jobs.EffectUnresolved ||
				item.Execution.Diagnostic.Stage != tc.stage || item.Execution.Diagnostic.Code != tc.code {
				t.Fatalf("a refused staged commit was misrecorded: %+v", item.Execution.ItemObservation)
			}
			if _, err := h.Inspector.Object(t.Context(), h.Primary.BookmarkID, container, "live/"+base); err == nil {
				t.Errorf("a refused staged commit published at %s/live/%s", container, base)
			}
			// The staged bytes are retained under ADR 0020, and so is every
			// segment the manifest still names: this refusal publishes
			// nothing and reclaims nothing.
			if _, err := h.Inspector.Object(t.Context(), h.Primary.BookmarkID, container, ".aeronautes/stage/"+id+"/batch/"+base); err != nil {
				t.Errorf("a refused staged commit did not retain its staging at %s: %v", staged, err)
			}
			held, err := h.Primary.Oracle.ListObjects(segments)
			if err != nil {
				t.Fatal(err)
			}
			fault.mu.Lock()
			wrote := append([]string(nil), fault.segments...)
			fault.mu.Unlock()
			want := map[string]bool{}
			for _, p := range wrote {
				want[p[strings.Index(p, segments+"/")+len(segments)+1:]] = true
			}
			if tc.change != nil && strings.Contains(tc.name, "missing") {
				// The one case that deliberately removes a segment.
				if len(held) >= len(want) {
					t.Errorf("the missing-segment fixture removed nothing: held %d of %d", len(held), len(want))
				}
			} else {
				for _, name := range held {
					delete(want, name)
				}
				if len(want) != 0 {
					t.Errorf("a refused staged commit did not retain every segment it staged: missing %v", want)
				}
			}
			h.AssertNoCollateral(beforeB, h.SnapshotNamespace(h.Secondary))
			allowed := []string{staged, segments}
			for _, name := range held {
				allowed = append(allowed, segments+"/"+name)
			}
			h.AssertNoCollateral(beforeA, h.SnapshotNamespace(h.Primary), allowed...)
		})
	}
}

// The changes. Each keeps the manifest's own total length wherever the
// record requires a same-length difference, so nothing but reading the
// staged content can see it.

func stagedSegmentReplaced(f *stagedFault) error {
	if len(f.segments) == 0 {
		return fmt.Errorf("the engine wrote no segment to replace")
	}
	return rawInvert(f.account, f.segments[0])
}

func stagedSegmentMissing(f *stagedFault) error {
	if len(f.segments) == 0 {
		return fmt.Errorf("the engine wrote no segment to remove")
	}
	return rawDelete(f.account, f.segments[0])
}

func stagedSegmentDuplicated(f *stagedFault) error {
	if len(f.segments) < 2 {
		return fmt.Errorf("the engine wrote %d segments, the case needs two", len(f.segments))
	}
	first, err := rawGet(f.account, f.segments[0], "")
	if err != nil {
		return err
	}
	second, err := rawGet(f.account, f.segments[1], "")
	if err != nil {
		return err
	}
	// The first segment's bytes, trimmed to the second's length, so the
	// total is unchanged and the content is the first run twice over.
	if len(first) < len(second) {
		return fmt.Errorf("segment lengths %d and %d cannot be duplicated at one length", len(first), len(second))
	}
	return rawReplace(f.account, f.segments[1], first[:len(second)])
}

func stagedSegmentsReordered(f *stagedFault) error {
	if len(f.segments) < 2 {
		return fmt.Errorf("the engine wrote %d segments, the case needs two", len(f.segments))
	}
	first, err := rawGet(f.account, f.segments[0], "")
	if err != nil {
		return err
	}
	second, err := rawGet(f.account, f.segments[1], "")
	if err != nil {
		return err
	}
	if len(first) == len(second) {
		if err := rawReplace(f.account, f.segments[0], second); err != nil {
			return err
		}
		return rawReplace(f.account, f.segments[1], first)
	}
	// Unequal runs: swap the leading bytes they share, which reorders the
	// stream without changing any object's length.
	n := min(len(first), len(second))
	head := append([]byte(nil), first[:n]...)
	if err := rawReplace(f.account, f.segments[0], append(append([]byte(nil), second[:n]...), first[n:]...)); err != nil {
		return err
	}
	return rawReplace(f.account, f.segments[1], append(head, second[n:]...))
}

func stagedForeignRun(f *stagedFault) error {
	if len(f.segments) == 0 {
		return fmt.Errorf("the engine wrote no segment to replace")
	}
	stored, err := rawGet(f.account, f.segments[0], "")
	if err != nil {
		return err
	}
	// Another job's bytes, at exactly this segment's length.
	foreign := bytes.Repeat([]byte("another job wrote this"), len(stored)/22+1)[:len(stored)]
	return rawReplace(f.account, f.segments[0], foreign)
}

func stagedPlainAltered(f *stagedFault) error {
	if f.staged == "" {
		return fmt.Errorf("the staged name was never learned")
	}
	return rawInvert(f.account, f.staged)
}

// stagedStaleRow replaces a segment's bytes while leaving its container
// row reporting the length and entity tag of the bytes it replaced. Only
// the fixture's own store can be asked for that, which is the point: the
// row is evidence a client cannot verify, and ADR 0042 rejected trusting
// it with this exact counterexample.
func stagedStaleRow(f *stagedFault) error {
	if len(f.segments) == 0 {
		return fmt.Errorf("the engine wrote no segment to replace")
	}
	if f.mock == nil {
		return fmt.Errorf("the stale-row case needs the fixture's own store")
	}
	stored, err := rawGet(f.account, f.segments[0], "")
	if err != nil {
		return err
	}
	trimmed := strings.TrimPrefix(f.segments[0], "/")
	slash := strings.Index(trimmed, "/")
	if slash <= 0 {
		return fmt.Errorf("segment path %q names no container", f.segments[0])
	}
	sum := md5.Sum(stored)
	f.mock.PutObject(trimmed[:slash], trimmed[slash+1:], swifttest.Object{
		Bytes: int64(len(stored)), ETag: hex.EncodeToString(sum[:]),
		ContentType: "application/octet-stream", Data: inverted(stored),
	})
	return nil
}

// Raw helpers. Each goes through the account's own storage URL rather
// than the proxied one, so a fixture's change is never mistaken for the
// route's own write.

func rawRequest(a *Account, method, path, query string, body []byte) (int, []byte, error) {
	url := a.storageURL + path
	if query != "" {
		url += "?" + query
	}
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	req, err := http.NewRequest(method, url, reader)
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("X-Auth-Token", a.token)
	if body != nil {
		req.Header.Set("Content-Type", "application/octet-stream")
	}
	resp, err := a.rawClient.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	answered, err := io.ReadAll(resp.Body)
	return resp.StatusCode, answered, err
}

func rawGet(a *Account, path, query string) ([]byte, error) {
	status, body, err := rawRequest(a, http.MethodGet, path, query, nil)
	if err != nil {
		return nil, err
	}
	if status/100 != 2 {
		return nil, fmt.Errorf("raw read %s: HTTP %d", path, status)
	}
	return body, nil
}

func rawReplace(a *Account, path string, body []byte) error {
	status, _, err := rawRequest(a, http.MethodPut, path, "", body)
	if err != nil {
		return err
	}
	if status/100 != 2 {
		return fmt.Errorf("raw replace %s: HTTP %d", path, status)
	}
	return nil
}

func rawDelete(a *Account, path string) error {
	status, _, err := rawRequest(a, http.MethodDelete, path, "", nil)
	if err != nil {
		return err
	}
	if status/100 != 2 {
		return fmt.Errorf("raw delete %s: HTTP %d", path, status)
	}
	return nil
}

// rawInvert reads stored bytes and writes them back inverted, so the
// replacement is exactly as long as what it replaced.
func rawInvert(a *Account, path string) error {
	stored, err := rawGet(a, path, "")
	if err != nil {
		return err
	}
	if len(stored) == 0 {
		return fmt.Errorf("raw read %s returned nothing to invert", path)
	}
	return rawReplace(a, path, inverted(stored))
}

// inverted returns bytes of the same length that are not the original.
func inverted(body []byte) []byte {
	out := append([]byte(nil), body...)
	for i := range out {
		out[i] ^= 0xff
	}
	return out
}

func md5Hex(body []byte) string {
	sum := md5.Sum(body)
	return hex.EncodeToString(sum[:])
}
