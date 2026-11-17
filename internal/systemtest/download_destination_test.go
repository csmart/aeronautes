package systemtest

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/csmart/aeronautes/internal/app"
	"github.com/csmart/aeronautes/internal/jobs"
)

func downloadDestinationCases(t *testing.T, makeHarness func(*testing.T) *Harness) {
	for _, route := range []string{"engine", "native", "marker"} {
		for _, fault := range []string{
			"descendant_symlink", "dangling_final_symlink", "parent_file", "missing_root",
			"safe_absent_subtree", "initial_root_symlink", "contained_symlink", "deep_symlink",
			"root_file", "existing_parent", "mixed",
		} {
			t.Run(route+"/"+fault, func(t *testing.T) { downloadDestinationService(t, makeHarness(t), route, fault) })
		}
	}
	for _, fault := range []string{"marker_file_collision", "existing_marker", "root_marker"} {
		t.Run("marker/"+fault, func(t *testing.T) { downloadDestinationService(t, makeHarness(t), "marker", fault) })
	}
	for _, fault := range []string{"missing_root", "root_file", "existing_parent"} {
		t.Run("empty/"+fault, func(t *testing.T) { downloadDestinationService(t, makeHarness(t), "empty", fault) })
	}
	for _, route := range []string{"engine", "native", "marker", "empty"} {
		for _, fault := range []string{"parent_file", "missing_root"} {
			if route == "empty" && fault == "parent_file" {
				continue
			}
			t.Run("queued/"+route+"/"+fault, func(t *testing.T) { downloadDestinationQueued(t, makeHarness(t), route, fault) })
		}
	}
}

func TestDownloadDestinationMock(t *testing.T) {
	t.Parallel()
	downloadDestinationCases(t, findMockHarness)
}

func TestDownloadDestinationRealSwift(t *testing.T) {
	t.Parallel()
	downloadDestinationCases(t, New)
}

// A file that appeared while the download was queued is the one way a
// download job ends in a conflict, so these top-level tests, which may
// credit the scenario contract, carry that case (ADR 0122 decision 13).
func TestDownloadOfAFileThatAppearedMock(t *testing.T) {
	t.Parallel()
	downloadDestinationQueued(t, findMockHarness(t), "engine", "file_appeared")
}

func TestDownloadOfAFileThatAppearedRealSwift(t *testing.T) {
	t.Parallel()
	downloadDestinationQueued(t, New(t), "engine", "file_appeared")
}

func downloadDestinationService(t *testing.T, h *Harness, route, fault string) {
	t.Helper()
	source := h.Container(h.Primary, "download-destination")
	other := h.Container(h.Secondary, "download-destination-other")
	owned := t.TempDir()
	selected := filepath.Join(owned, "selected")
	seedListingLocal(t, owned, "selected/keep", []byte("selected sentinel\x00\xff"))
	seedListingLocal(t, owned, "outside/keep", []byte("outside sentinel"))
	seedListingLocal(t, owned, "unrelated/keep", []byte("unrelated sentinel"))
	prefix := "scope/"
	if route == "native" {
		prefix = "scope\n/"
	}
	captured, relative := selected, "proof.bin"
	if route == "marker" {
		relative = "created/"
	}
	refused := true
	var newDirectories []string
	switch fault {
	case "descendant_symlink", "contained_symlink", "deep_symlink", "mixed":
		link, target := "redirect", filepath.Join(owned, "outside")
		if fault == "contained_symlink" {
			target = "."
		} else if fault == "deep_symlink" {
			seedListingLocal(t, owned, "selected/ordinary/keep", []byte("ordinary parent"))
			link = "ordinary/redirect"
		}
		if err := os.Symlink(target, filepath.Join(selected, filepath.FromSlash(link))); err != nil {
			t.Fatal(err)
		}
		relative = link + "/payload.bin"
		if route == "marker" {
			relative = link + "/created/"
		}
		if fault == "mixed" {
			seedListingLocal(t, owned, "selected/blocked", []byte("second invalid parent"))
		}
	case "dangling_final_symlink":
		relative = "dangling"
		if err := os.Symlink(filepath.Join(owned, "outside", "absent-target"), filepath.Join(selected, relative)); err != nil {
			t.Fatal(err)
		}
		if route == "marker" {
			relative += "/"
		}
	case "parent_file":
		seedListingLocal(t, owned, "selected/blocked", []byte("existing parent file"))
		relative = "blocked/payload.bin"
		if route == "marker" {
			relative = "blocked/created/"
		}
	case "marker_file_collision":
		seedListingLocal(t, owned, "selected/created", []byte("existing marker-name file"))
	case "missing_root", "root_file":
		if route == "marker" {
			relative = ""
		}
	case "safe_absent_subtree":
		refused, relative = false, "new/nested/payload.bin"
		newDirectories = []string{"selected/new", "selected/new/nested"}
		if route == "marker" {
			relative = "new/nested/"
		}
	case "initial_root_symlink":
		captured, refused = filepath.Join(owned, "picked-link"), false
		if err := os.Symlink(selected, captured); err != nil {
			t.Fatal(err)
		}
		if route == "marker" {
			relative = ""
		}
	case "existing_parent":
		refused, relative = false, "existing/payload.bin"
		seedListingLocal(t, owned, "selected/existing/keep", []byte("existing directory contents"))
		if route == "marker" {
			relative = "existing/created/"
			newDirectories = []string{"selected/existing/created"}
		}
	case "existing_marker":
		refused = false
		seedListingLocal(t, owned, "selected/created/keep", []byte("existing marker contents"))
	case "root_marker":
		refused, relative = false, ""
	default:
		t.Fatalf("unknown destination fixture %q", fault)
	}
	body := []byte("complete source bytes\x00\xff " + route + "/" + fault)
	if route == "marker" {
		body = nil
	}
	name := prefix + relative
	if route != "empty" {
		h.Seed(h.Primary, source, name, body, nil)
	}
	if fault == "mixed" {
		first, second := "aaa-safe.bin", "blocked/second.bin"
		if route == "marker" {
			first, second = "aaa-safe/", "blocked/second/"
		}
		h.Seed(h.Primary, source, prefix+first, body, nil)
		h.Seed(h.Primary, source, prefix+second, body, nil)
	}
	h.Seed(h.Primary, source, "outside-selection", []byte("unselected remote sentinel"), nil)
	h.Seed(h.Secondary, other, "keep", []byte("other remote sentinel"), nil)
	beforeA, beforeB := h.SnapshotNamespace(h.Primary), h.SnapshotNamespace(h.Secondary)
	trace := downloadRecordsProxy(t, h, source, prefix, nil)
	app.WireFilePicker(h.Transfer, listingFilePicker{dir: captured})
	picked, err := h.Transfer.PickDownloadDir(t.Context(), app.SelectionScopeDTO{BookmarkID: h.Primary.BookmarkID, Container: source, Prefix: prefix})
	if err != nil || picked == nil || picked.Dir != captured || picked.Token == "" {
		t.Fatalf("owned picker changed the selected spelling: %+v %v", picked, err)
	}
	if fault == "missing_root" || fault == "root_file" {
		if err := os.Remove(filepath.Join(selected, "keep")); err != nil {
			t.Fatal(err)
		}
		if err := os.Remove(selected); err != nil {
			t.Fatal(err)
		}
		if fault == "root_file" {
			seedListingLocal(t, owned, "selected", []byte("replacement root file"))
		}
	}
	request := app.DownloadRequestDTO{BookmarkID: h.Primary.BookmarkID, Container: source, Prefix: prefix, DestDirToken: picked.Token}
	beforeLocal, beforeCounts := snapshotListingLocal(t, owned), downloadRootGuardCounts(t, h)
	preflight, preflightErr := h.Transfer.PreflightDownload(t.Context(), request)
	if !reflect.DeepEqual(beforeLocal, snapshotListingLocal(t, owned)) || !reflect.DeepEqual(beforeCounts, downloadRootGuardCounts(t, h)) {
		t.Error("preflight changed owned filesystem or transfer records")
	}
	id, startErr := h.Transfer.StartDownload(t.Context(), request)
	var saved uploadRecoveryState
	if id != "" {
		h.AwaitJob(id)
		copyCompletionAwaitPins(t, h, h.Primary)
		saved = uploadRecoveryRead(t, h.database, id)
		public, err := h.Jobs.Get(t.Context(), h.Primary.BookmarkID, id)
		if err != nil || !reflect.DeepEqual(public, saved.Job) {
			t.Errorf("public job differs from independently read records: %+v %v", public, err)
		}
		byKey := map[string]jobs.ItemRecord{}
		for _, item := range saved.Items {
			byKey[item.Key] = item
		}
		assertDownloadItemPages(t, h, id, byKey, saved.ItemTotals)
	}
	h.AssertNoCollateral(beforeA, h.SnapshotNamespace(h.Primary))
	h.AssertNoCollateral(beforeB, h.SnapshotNamespace(h.Secondary))
	if len(trace.traffic.writeSnapshot()) != 0 {
		t.Errorf("download changed remote data: %v", trace.traffic.writeSnapshot())
	}
	if refused {
		if preflightErr != nil || preflight == nil || !preflight.Rejected {
			t.Errorf("existing %s hazard lacked a preflight refusal: %+v error=%v", fault, preflight, preflightErr)
		} else if fault == "mixed" {
			rejections := 0
			for _, problem := range preflight.ProblemSamples {
				if problem.Reject {
					rejections++
				}
			}
			if rejections != 2 || int(preflight.Objects) != 1 || !strings.HasPrefix(preflight.ItemSamples[0].Name, prefix+"aaa-safe") {
				t.Errorf("mixed preflight did not collect both late hazards and the safe first member: %+v", preflight)
			}
		}
		if id != "" || startErr == nil || !strings.Contains(startErr.Error(), "preflight found problems") {
			t.Errorf("Start admitted an existing destination hazard: id=%q error=%v", id, startErr)
		}
		if !reflect.DeepEqual(beforeCounts, downloadRootGuardCounts(t, h)) || len(trace.selectedGets()) != 0 {
			t.Errorf("destination refusal admitted records or content work: counts=%v -> %v gets=%v", beforeCounts, downloadRootGuardCounts(t, h), trace.selectedGets())
		}
		assertListingLocalDelta(t, beforeLocal, snapshotListingLocal(t, owned), nil)
		return
	}
	count := int64(1)
	if route == "empty" {
		count = 0
	}
	if preflightErr != nil || preflight == nil || preflight.Rejected || preflight.Objects != count || startErr != nil || id == "" {
		t.Fatalf("safe control was not admitted: preflight=%+v error=%v start=%q error=%v", preflight, preflightErr, id, startErr)
	}
	want := jobs.Counters{ObjectsPlanned: count, BytesPlanned: count * int64(len(body)), ObjectsCopied: count, BytesCopied: count * int64(len(body)), ObjectsVerified: count}
	if route == "native" {
		want.NativeRouted = count
	}
	if saved.Job.State != "complete" || saved.Job.Error != "" || saved.Job.Counters != want || !saved.ItemsCaptured || int64(len(saved.Items)) != count ||
		saved.ItemTotals != (jobs.ItemTotals{Items: count, Planned: count, Admitted: count}) || len(saved.Rows) != 0 || len(saved.Audit) != 1 ||
		saved.Audit[0].Operation != "transfer.local.complete" || saved.Audit[0].Resource != "job "+id || saved.Audit[0].Result != "applied" ||
		saved.Audit[0].Summary != " · item outcomes: 0 failed names, 0 conflicts, 0 refusals, 0 unresolved, 0 unverified, 0 with problems" {
		t.Fatalf("safe control lost exact job, item or audit outcome: %+v", saved)
	}
	if count == 1 {
		planned, item := preflight.ItemSamples[0], saved.Items[0]
		rel := filepath.FromSlash(strings.TrimSuffix(relative, "/"))
		wantRoute := "rclone"
		if route == "native" {
			wantRoute = "native"
		}
		if planned.Name != name || planned.RelPath != rel || planned.Dir != (route == "marker") || route != "marker" && planned.Route != wantRoute ||
			item.Key != name || !item.Planned || !item.Admitted || item.ListedBytes != int64(len(body)) ||
			item.Source != (jobs.ItemLocation{Kind: jobs.ItemSwift, BookmarkID: h.Primary.BookmarkID, Container: source, Object: name}) ||
			item.Destination != (jobs.ItemLocation{Kind: jobs.ItemLocal, LocalPath: filepath.Join(captured, rel)}) ||
			item.Execution == nil || item.Execution.ItemObservation != (jobs.ItemObservation{Result: jobs.ItemCopied, Effect: jobs.EffectReportedApplied, CopiedBytes: int64(len(body))}) ||
			item.Verification == nil || item.Verification.ItemObservation != (jobs.ItemObservation{Result: jobs.ItemVerified, Effect: jobs.EffectNotAttempted}) {
			t.Fatalf("safe control changed selected identity, route or recorded observations: plan=%+v item=%+v", planned, item)
		}
	}
	files := map[string][]byte{}
	if route == "engine" || route == "native" {
		files["selected/"+relative] = body
		if data, err := os.ReadFile(filepath.Join(selected, filepath.FromSlash(relative))); err != nil || !bytes.Equal(data, body) {
			t.Errorf("safe control full file bytes differ: %q error=%v", data, err)
		}
	}
	assertListingLocalDelta(t, beforeLocal, snapshotListingLocal(t, owned), files, newDirectories...)
	gets := trace.selectedGets()
	if route == "engine" && gets[relative] == 0 || route == "native" && gets[relative] != 1 || (route == "empty" || route == "marker") && len(gets) != 0 {
		t.Errorf("safe control content route differs: gets=%v", gets)
	}
}
