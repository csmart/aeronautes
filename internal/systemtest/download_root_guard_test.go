package systemtest

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/csmart/aeronautes/internal/app"
	"github.com/csmart/aeronautes/internal/jobs"
)

func downloadRootGuardCounts(t *testing.T, h *Harness) []int64 {
	t.Helper()
	var result []int64
	for _, query := range []string{
		"SELECT COUNT(*) FROM jobs",
		"SELECT COUNT(*) FROM job_recovery",
		"SELECT COUNT(*) FROM job_items",
		"SELECT COUNT(*) FROM job_item_observations",
		"SELECT COUNT(*) FROM audit_events WHERE operation LIKE 'transfer.%'",
	} {
		var count int64
		if err := h.database.Reader().QueryRowContext(t.Context(), query).Scan(&count); err != nil {
			t.Fatal(err)
		}
		result = append(result, count)
	}
	return result
}

func downloadRootGuardCases(t *testing.T, makeHarness func(*testing.T) *Harness) {
	for _, native := range []bool{false, true} {
		for _, spelling := range []string{"canonical", "unicode", "symlink", "symlink_parent", "relative", "dot", "parent", "empty_component", "trailing_separator", "invalid_utf8"} {
			for _, empty := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s_native_%v_empty_%v", spelling, native, empty), func(t *testing.T) {
					downloadRootGuardService(t, makeHarness(t), spelling, native, empty)
				})
			}
		}
	}
}

func TestDownloadRootGuardMock(t *testing.T) {
	t.Parallel()

	downloadRootGuardCases(t, findMockHarness)
}

func TestDownloadRootGuardRealSwift(t *testing.T) {
	t.Parallel()
	downloadRootGuardCases(t, New)
}

func downloadRootGuardRefused(t *testing.T, h *Harness, request app.DownloadRequestDTO, trace *uploadDeleteTrace) {
	t.Helper()
	before := downloadRootGuardCounts(t, h)
	requests := trace.requestCount()
	preflight, err := h.Transfer.PreflightDownload(t.Context(), request)
	if err != nil || preflight == nil || !preflight.Rejected || int(preflight.Objects) != 0 || int(preflight.ProblemCount) != 1 || preflight.Objects != 0 || preflight.TotalBytes != 0 {
		t.Errorf("unsafe destination was not refused before planning: %+v %v", preflight, err)
	} else {
		problem := preflight.ProblemSamples[0]
		const detail = "the selected destination path cannot be used unchanged; select it again using an absolute UTF-8 path without dot or empty components"
		if problem.Name != "destination" || problem.Kind != "destination-path" || problem.Detail != detail || !problem.Reject {
			t.Errorf("unsafe destination lost its constant refusal: %+v", problem)
		}
		encoded, err := json.Marshal(preflight)
		if err != nil || strings.ContainsRune(string(encoded), '\ufffd') || strings.Contains(string(encoded), `\ufffd`) {
			t.Errorf("unsafe destination refusal changed path bytes through JSON: %s %v", encoded, err)
		}
	}
	id, err := h.Transfer.StartDownload(t.Context(), request)
	if id != "" {
		h.AwaitJob(id)
		copyCompletionAwaitPins(t, h, h.Primary)
	}
	if id != "" || err == nil || !strings.Contains(err.Error(), "preflight found problems") {
		t.Errorf("unsafe destination admitted work or mislabelled refusal: %q %v", id, err)
	}
	if after := downloadRootGuardCounts(t, h); !reflect.DeepEqual(before, after) || trace.requestCount() != requests {
		t.Errorf("unsafe destination created records or contacted storage: before=%v after=%v requests=%d", before, after, trace.requestCount()-requests)
	}
	if err := h.jobStore.Available(); err != nil {
		t.Errorf("destination refusal made the store unavailable: %v", err)
	}
}

func downloadRootGuardService(t *testing.T, h *Harness, spelling string, native, empty bool) {
	t.Helper()
	source := h.Container(h.Primary, "download-root-source")
	other := h.Container(h.Secondary, "download-root-other")
	prefix := "scope/"
	if native {
		prefix = "scope\n/"
	}
	body := []byte("selected source bytes")
	h.Seed(h.Primary, source, prefix+"proof.bin", body, nil)
	h.Seed(h.Primary, source, "outside", []byte("unselected source"), nil)
	h.Seed(h.Secondary, other, "outside", []byte("other account"), nil)
	if empty {
		prefix += "empty/"
	}
	beforeA, beforeB := h.SnapshotNamespace(h.Primary), h.SnapshotNamespace(h.Secondary)
	root := t.TempDir()
	seedListingLocal(t, root, "actual/selected/keep", []byte("selected directory"))
	seedListingLocal(t, root, "lexical/selected/keep", []byte("different directory"))
	seedListingLocal(t, root, "actual/nested/keep", []byte("symlink target"))
	if err := os.Symlink(filepath.Join(root, "actual", "nested"), filepath.Join(root, "lexical", "link")); err != nil {
		t.Fatal(err)
	}
	sep := string(os.PathSeparator)
	captured := filepath.Join(root, "actual", "selected")
	observed := "actual/selected/proof.bin"
	refused := true
	switch spelling {
	case "canonical":
		refused = false
	case "unicode":
		captured = filepath.Join(root, "valid\ufffd-é-🛫")
		seedListingLocal(t, root, "valid\ufffd-é-🛫/keep", []byte("selected directory"))
		observed, refused = "valid\ufffd-é-🛫/proof.bin", false
	case "symlink":
		link := filepath.Join(root, "direct")
		if err := os.Symlink(captured, link); err != nil {
			t.Fatal(err)
		}
		captured, refused = link, false
	case "symlink_parent":
		captured = filepath.Join(root, "lexical", "link") + sep + ".." + sep + "selected"
	case "dot":
		captured += sep + "."
	case "parent":
		captured = filepath.Join(root, "actual", "nested") + sep + ".." + sep + "selected"
	case "empty_component":
		captured = root + sep + "actual" + sep + sep + "selected"
	case "trailing_separator":
		captured += sep
	case "relative":
		cwd, err := os.Getwd()
		if err != nil {
			t.Fatal(err)
		}
		captured, err = filepath.Rel(cwd, captured)
		if err != nil {
			t.Fatal(err)
		}
	case "invalid_utf8":
		captured = root + sep + string([]byte{'b', 0xff})
		seedListingLocal(t, root, string([]byte{'b', 0xff})+"/keep", []byte("selected directory"))
	default:
		t.Fatalf("unknown spelling %q", spelling)
	}
	marker, err := os.ReadFile(captured + sep + "keep")
	if err != nil || string(marker) != "selected directory" {
		t.Fatalf("fixture did not select the actual directory: %q %v", marker, err)
	}
	beforeLocal := snapshotListingLocal(t, root)
	trace := copyCompletionProxy(t, h, h.Primary, "")
	app.WireFilePicker(h.Transfer, listingFilePicker{dir: captured})
	picked, err := h.Transfer.PickDownloadDir(t.Context(), app.SelectionScopeDTO{BookmarkID: h.Primary.BookmarkID, Container: source, Prefix: prefix})
	if err != nil || picked == nil || picked.Dir != captured {
		t.Fatalf("picker changed captured spelling: %+v %v", picked, err)
	}
	req := app.DownloadRequestDTO{BookmarkID: h.Primary.BookmarkID, Container: source, Prefix: prefix, DestDirToken: picked.Token}
	if refused {
		downloadRootGuardRefused(t, h, req, trace)
		if !reflect.DeepEqual(beforeLocal, snapshotListingLocal(t, root)) {
			t.Error("refused destination changed the complete owned filesystem")
		}
		h.AssertNoCollateral(beforeA, h.SnapshotNamespace(h.Primary))
		h.AssertNoCollateral(beforeB, h.SnapshotNamespace(h.Secondary))
		return
	}
	preflight, err := h.Transfer.PreflightDownload(t.Context(), req)
	want := int64(1)
	if empty {
		want = 0
	}
	if err != nil || preflight == nil || preflight.Rejected || preflight.Objects != want {
		t.Fatalf("valid destination preflight differs: %+v %v", preflight, err)
	}
	if !empty {
		route := "rclone"
		if native {
			route = "native"
		}
		if preflight.ItemSamples[0].Route != route || preflight.ItemSamples[0].Name != prefix+"proof.bin" || preflight.ItemSamples[0].RelPath != "proof.bin" {
			t.Fatalf("accepted destination changed route or identity: %+v", preflight.ItemSamples[0])
		}
	}
	id, err := h.Transfer.StartDownload(t.Context(), req)
	if err != nil {
		t.Fatal(err)
	}
	h.AwaitJob(id)
	copyCompletionAwaitPins(t, h, h.Primary)
	saved := uploadRecoveryRead(t, h.database, id)
	counters := jobs.Counters{ObjectsPlanned: want, BytesPlanned: want * int64(len(body)), ObjectsCopied: want, BytesCopied: want * int64(len(body)), ObjectsVerified: want}
	if native {
		counters.NativeRouted = want
	}
	if saved.Job.State != "complete" || saved.Job.Counters != counters || saved.Job.Error != "" {
		t.Fatalf("accepted destination outcome differs: %+v", saved.Job)
	}
	allowed := map[string][]byte{}
	if !empty {
		selected, err := os.ReadFile(captured + sep + "proof.bin")
		if err != nil || !bytes.Equal(selected, body) {
			t.Errorf("captured destination did not receive full bytes: %q %v", selected, err)
		}
		allowed[observed] = body
	}
	assertListingLocalDelta(t, beforeLocal, snapshotListingLocal(t, root), allowed)
	h.AssertNoCollateral(beforeA, h.SnapshotNamespace(h.Primary))
	h.AssertNoCollateral(beforeB, h.SnapshotNamespace(h.Secondary))
	visible, err := h.Jobs.Get(t.Context(), id)
	if err != nil || !reflect.DeepEqual(visible, saved.Job) || len(saved.Audit) != 1 || saved.Audit[0].Operation != "transfer.local.complete" || saved.Audit[0].Resource != "job "+id || saved.Audit[0].Result != "applied" {
		t.Fatalf("public, durable or audit outcome differs: %+v %v", saved, err)
	}
	if len(trace.writeSnapshot()) != 0 {
		t.Fatalf("download changed remote data: %v", trace.writeSnapshot())
	}
	originals := map[string][]byte{}
	if !empty {
		originals["proof.bin"] = body
	}
	assertDownloadRecords(t, h, id, source, prefix, captured, nil, originals, native, saved)
	if next, err := h.Transfer.RerunJob(t.Context(), id); next != "" || err == nil || !strings.Contains(err.Error(), "own dialog") {
		t.Fatalf("download history gained rerun authority: %q %v", next, err)
	}
	requests := trace.requestCount()
	uploadRecoveryReopen(t, h, id, saved)
	if requests != trace.requestCount() {
		t.Error("download root reopen contacted Swift")
	}
	assertListingLocalDelta(t, beforeLocal, snapshotListingLocal(t, root), allowed)
	h.AssertNoCollateral(beforeA, h.SnapshotNamespace(h.Primary))
	h.AssertNoCollateral(beforeB, h.SnapshotNamespace(h.Secondary))
}
