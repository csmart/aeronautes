package systemtest

import (
	"context"
	"crypto/sha256"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/csmart/aeronautes/internal/app"
	"github.com/csmart/aeronautes/internal/swifttest"
	"github.com/csmart/aeronautes/internal/transfer/rclone"
)

// The picker token names the directory picked, held by handle from the
// pick through every effect of every job it starts. These cases replace
// the selected root or a parent under it at one deterministic boundary
// the service exposes: the held Swift response before a native write,
// the engine boundary between staging and placement, or the gap between
// the operator's preflight and start calls. The assertions are exact:
// where every byte landed, which items failed and why, and what the job
// reported. The archived service probe under docs/experiments/probes/
// failed eight of these cases on both providers.

type rootAuthorityServiceCase struct {
	name    string
	route   string // engine, native or marker
	seam    string // stage, get, bridge, or empty for a control
	replace string // root or parent
}

const (
	rootAuthorityRefusal      = "is not an inspectable ordinary directory"
	rootAuthorityEngineRefuse = "the engine can only be given a pathname; engine items were not attempted"
	rootAuthorityEngineGate   = "engine downloads are refused on this platform"
	rootAuthorityStageReplace = "was replaced under the selected directory while the engine ran"
)

func downloadRootAuthorityCases(t *testing.T, makeHarness func(*testing.T) *Harness) {
	for _, c := range []rootAuthorityServiceCase{
		{"engine_parent_after_stage", "engine", "stage", "parent"},
		{"engine_root_after_stage", "engine", "stage", "root"},
		{"engine_stage_relocated_during_get", "engine", "get", "stage"},
		{"native_parent_during_get", "native", "get", "parent"},
		{"native_root_during_get", "native", "get", "root"},
		{"marker_parent_during_get", "marker", "get", "parent"},
		{"marker_root_during_get", "marker", "get", "root"},
		{"engine_root_before_start", "engine", "bridge", "root"},
		{"native_root_before_start", "native", "bridge", "root"},
		{"engine_control", "engine", "", ""},
		{"native_control", "native", "", ""},
		{"marker_control", "marker", "", ""},
	} {
		t.Run(c.name, func(t *testing.T) { downloadRootAuthorityService(t, makeHarness(t), c) })
	}
}

func TestDownloadRootAuthorityMock(t *testing.T) {
	t.Parallel()
	downloadRootAuthorityCases(t, findMockHarness)
}

func TestDownloadRootAuthorityRealSwift(t *testing.T) {
	t.Parallel()
	downloadRootAuthorityCases(t, New)
}

type rootAuthorityObject struct {
	rel  string
	body []byte
	dir  bool
}

func downloadRootAuthorityService(t *testing.T, h *Harness, c rootAuthorityServiceCase) {
	t.Helper()
	source := h.Container(h.Primary, "download-root-authority")
	other := h.Container(h.Secondary, "download-root-authority-other")
	owned := t.TempDir()
	selected := filepath.Join(owned, "selected")
	outside := filepath.Join(owned, "outside")
	moved := filepath.Join(owned, "moved")
	movedStage := filepath.Join(owned, "moved-stage")
	seedListingLocal(t, owned, "selected/keep", []byte("selected sentinel\x00\xff"))
	seedListingLocal(t, owned, "outside/keep", []byte("outside sentinel"))
	// A newline in the prefix keeps the child engine away, so the native
	// route carries every non-engine case.
	prefix := "scope/"
	if c.route != "engine" {
		prefix = "scope\n/"
	}
	parent := ""
	if c.replace == "parent" {
		parent = "redirect/"
	}
	var objects []rootAuthorityObject
	held := ""
	switch c.route {
	case "engine":
		objects = []rootAuthorityObject{{rel: parent + "payload.bin", body: []byte("engine bytes " + c.name)}}
		held = objects[0].rel
	case "native":
		objects = []rootAuthorityObject{{rel: parent + "b.bin", body: []byte("native bytes " + c.name)}}
		held = objects[0].rel
	case "marker":
		objects = []rootAuthorityObject{{rel: "a.bin", body: []byte("native bytes " + c.name)}, {rel: parent + "m/", dir: true}}
		held = "a.bin"
	default:
		t.Fatalf("unknown route %q", c.route)
	}
	for _, object := range objects {
		h.Seed(h.Primary, source, prefix+object.rel, object.body, nil)
	}
	h.Seed(h.Primary, source, "outside-selection", []byte("unselected remote sentinel"), nil)
	h.Seed(h.Secondary, other, "keep", []byte("other remote sentinel"), nil)
	beforeA, beforeB := h.SnapshotNamespace(h.Primary), h.SnapshotNamespace(h.Secondary)

	stageDir := ""
	act := func() {
		if c.replace == "root" {
			if err := os.Rename(selected, moved); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(outside, selected); err != nil {
				t.Fatal(err)
			}
			return
		}
		if c.replace == "stage" {
			// The stage this executor created is moved out of the selected
			// directory while the child's source read is held, and a link
			// to the outside directory is left at its name.
			if err := os.Rename(stageDir, movedStage); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(outside, stageDir); err != nil {
				t.Fatal(err)
			}
			return
		}
		// Absent at admission, so the complete inspection admits it.
		if err := os.Symlink(outside, filepath.Join(selected, "redirect")); err != nil {
			t.Fatal(err)
		}
	}

	var trace *uploadDeleteTrace
	var barrier *swifttest.HTTPResponseBarrier
	var hits *atomic.Int32
	if c.seam == "get" {
		barrier, _, hits, trace = interruptionProxy(t, h, source, http.MethodGet, prefix+held, nil)
	} else {
		trace = copyCompletionProxy(t, h, h.Primary, "")
	}
	stageCalls := &atomic.Int32{}
	app.WireDownloadStageBoundary(h.Transfer, func(context.Context, string) error {
		stageCalls.Add(1)
		if c.seam == "stage" {
			act()
		}
		return nil
	})
	app.WireFilePicker(h.Transfer, listingFilePicker{dir: selected})
	picked, err := h.Transfer.PickDownloadDir(t.Context(), app.SelectionScopeDTO{BookmarkID: h.Primary.BookmarkID, Container: source, Prefix: prefix})
	if err != nil || picked == nil || picked.Dir != selected {
		t.Fatalf("picker changed the selected spelling: %+v %v", picked, err)
	}
	request := app.DownloadRequestDTO{BookmarkID: h.Primary.BookmarkID, Container: source, Prefix: prefix, DestDirToken: picked.Token}
	beforeLocal := snapshotListingLocal(t, owned)
	preflight, err := h.Transfer.PreflightDownload(t.Context(), request)
	if err != nil || preflight == nil || preflight.Rejected() || len(preflight.Items) != len(objects) {
		t.Fatalf("selection was not admitted before any replacement: %+v %v", preflight, err)
	}
	if c.seam == "bridge" {
		act()
	}
	id, err := h.Transfer.StartDownload(t.Context(), request)
	if err != nil {
		t.Fatalf("start refused an admitted selection: %v", err)
	}
	stageDir = filepath.Join(selected, ".aeronautes-stage-"+id)
	if c.seam == "get" {
		event, err := barrier.Wait(t.Context())
		if err != nil || event.StatusCode != http.StatusOK {
			t.Fatalf("held source read: %+v %v", event, err)
		}
		act()
		if err := barrier.Release(); err != nil {
			t.Fatal(err)
		}
	}
	job := h.AwaitJob(id)
	copyCompletionAwaitPins(t, h, h.Primary)
	saved := uploadRecoveryRead(t, h.database, id)
	afterLocal := snapshotListingLocal(t, owned)

	var added, changed []string
	for key, state := range afterLocal {
		before, existed := beforeLocal[key]
		switch {
		case !existed:
			added = append(added, key)
		case before != state:
			changed = append(changed, key)
		}
	}
	sort.Strings(added)
	sort.Strings(changed)
	t.Logf("root authority service outcome: state=%s error=%q counters=%+v totals=%+v stageCalls=%d added=%v changed=%v",
		job.State, job.Error, job.Counters, saved.ItemTotals, stageCalls.Load(), added, changed)

	// Nothing outside the selected directory changes.
	for _, key := range append(append([]string(nil), added...), changed...) {
		if key == "outside" || strings.HasPrefix(key, "outside/") {
			t.Errorf("outside entry %q was created or changed", key)
		}
	}
	for key := range beforeLocal {
		if (key == "outside" || strings.HasPrefix(key, "outside/")) && afterLocal[key] == (listingLocalState{}) {
			t.Errorf("outside entry %q disappeared", key)
		}
	}

	// Expected outcome per case: a replaced root keeps every effect inside
	// the directory picked and reports the displacement as a partial
	// outcome; a planted parent fails exactly the items beneath it with
	// the refusal reason; controls complete.
	realRoot := "selected"
	if c.replace == "root" && c.seam != "" {
		realRoot = "moved"
	}
	var wantCopied int64
	var wantFailedRel []string
	// Without a descriptor anchor the engine route is refused on this
	// platform, controls included. With the anchor, a stage relocated
	// while the child runs fails its items with the stage diagnosis and
	// the child's bytes stay in the relocated stage this executor created.
	engineRefused := c.route == "engine" && !rclone.AnchorSupported
	stageRelocated := c.replace == "stage" && !engineRefused
	for _, object := range objects {
		if c.replace == "parent" && c.seam != "" && strings.HasPrefix(object.rel, "redirect/") || engineRefused || stageRelocated {
			wantFailedRel = append(wantFailedRel, object.rel)
			continue
		}
		wantCopied++
	}
	wantState := "complete"
	if c.seam != "" || engineRefused {
		wantState = "partial"
	}
	if job.State != wantState || job.Counters.ObjectsCopied != wantCopied || job.Counters.ObjectsVerified != wantCopied {
		t.Errorf("job outcome differs: state=%s counters=%+v error=%q; want %s with %d copied", job.State, job.Counters, job.Error, wantState, wantCopied)
	}
	switch {
	case engineRefused:
		if !strings.Contains(job.Error, rootAuthorityEngineGate) {
			t.Errorf("engine route without an anchor was not refused by reason: %q", job.Error)
		}
	case c.seam == "":
		if job.Error != "" {
			t.Errorf("control reported an error: %q", job.Error)
		}
	case stageRelocated:
		if !strings.Contains(job.Error, rootAuthorityStageReplace) || !strings.Contains(job.Error, "stage cleanup") {
			t.Errorf("relocated stage was not reported with its diagnosis and retained cleanup: %q", job.Error)
		}
		key := "moved-stage/" + objects[0].rel
		state, found := afterLocal[key]
		if !found || !state.mode.IsRegular() || state.size != int64(len(objects[0].body)) || state.digest != sha256.Sum256(objects[0].body) {
			t.Errorf("the child's bytes are not in the relocated stage this executor created at %q: %+v %v", key, state, found)
		}
	case c.replace == "root":
		if !strings.Contains(job.Error, "the selected directory was relocated or replaced at its pathname") {
			t.Errorf("replaced root was not reported: %q", job.Error)
		}
		if strings.Contains(job.Error, "transfer failures") {
			t.Errorf("replaced root failed items instead of continuing into the directory picked: %q", job.Error)
		}
	default:
		if !strings.Contains(job.Error, rootAuthorityRefusal) {
			t.Errorf("planted parent was not refused by reason: %q", job.Error)
		}
	}
	// A refused item is recorded as failed with its effect unresolved,
	// because the walk may have created a parent before the refusal.
	if int64(len(wantFailedRel)) != saved.ItemTotals.Failed || saved.ItemTotals.Conflicts != 0 || saved.ItemTotals.Refused != 0 {
		t.Errorf("item totals do not match the expected failures %v: %+v", wantFailedRel, saved.ItemTotals)
	}

	// Every expected success exists inside the directory picked, reached
	// without any link, with exact bytes; a failed item left nothing.
	for _, object := range objects {
		key := realRoot + "/" + strings.TrimSuffix(object.rel, "/")
		state, found := afterLocal[key]
		failed := false
		for _, rel := range wantFailedRel {
			failed = failed || rel == object.rel
		}
		switch {
		case failed:
			if found {
				t.Errorf("failed item %q left an entry at %q: %+v", object.rel, key, state)
			}
		case !found:
			t.Errorf("expected success %q is absent from the directory picked at %q", object.rel, key)
		case object.dir && !state.mode.IsDir():
			t.Errorf("marker %q is not a directory at %q: %+v", object.rel, key, state)
		case !object.dir && (!state.mode.IsRegular() || state.size != int64(len(object.body)) || state.digest != sha256.Sum256(object.body)):
			t.Errorf("file %q differs at %q: %+v", object.rel, key, state)
		}
	}

	// Stage or temporary residue is reported as a cleanup problem, and a
	// refused engine placement retains its staged bytes under the stage.
	residue := 0
	for _, key := range added {
		if strings.Contains(key, ".aeronautes-stage-") || strings.Contains(key, ".aeronautes-part-") {
			residue++
		}
	}
	if residue > 0 && !strings.Contains(job.Error, "stage cleanup") {
		t.Errorf("%d stage or temporary entries remain while the job reports %q with error %q", residue, job.State, job.Error)
	}
	if c.route == "engine" && (c.replace == "parent" || c.replace == "stage") && !engineRefused && residue == 0 {
		t.Errorf("refused engine placement left no retained stage: added=%v", added)
	}
	if c.route == "engine" && !engineRefused && stageCalls.Load() != 1 {
		t.Errorf("engine boundary observed %d times", stageCalls.Load())
	}
	if engineRefused && stageCalls.Load() != 0 {
		t.Errorf("refused engine still ran %d times", stageCalls.Load())
	}

	if len(trace.writeSnapshot()) != 0 {
		t.Errorf("download changed remote data: %v", trace.writeSnapshot())
	}
	h.AssertNoCollateral(beforeA, h.SnapshotNamespace(h.Primary))
	h.AssertNoCollateral(beforeB, h.SnapshotNamespace(h.Secondary))
	if c.seam == "get" && (hits.Load() != 1 || barrier.Err() != nil) {
		t.Errorf("held source read retried or failed: %d %v", hits.Load(), barrier.Err())
	}

	if c.seam == "" && !engineRefused {
		files := map[string][]byte{}
		var dirs []string
		for _, object := range objects {
			key := "selected/" + strings.TrimSuffix(object.rel, "/")
			if object.dir {
				dirs = append(dirs, key)
				continue
			}
			files[key] = object.body
			if strings.Contains(object.rel, "/") {
				dirs = append(dirs, "selected/"+filepath.ToSlash(filepath.Dir(object.rel)))
			}
		}
		assertListingLocalDelta(t, beforeLocal, afterLocal, files, dirs...)
	}
}
