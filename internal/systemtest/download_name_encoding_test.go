package systemtest

import (
	"context"
	"path/filepath"
	"sort"
	"strconv"
	"sync/atomic"
	"testing"

	"github.com/csmart/aeronautes/internal/app"
)

// Names containing a rune the pinned engine reserves as a substitute or a
// quote are routed natively on both routes the engine serves, so every
// such name arrives with exact bytes and the engine is only ever asked for
// names it can carry. The archived before probe under docs/experiments/
// probes/2026-09-11-download-name-encoding* recorded the quote rune, the
// C0 control picture and the full-width full stop component as silently
// dropped on the engine route on both providers.

type nameEncodingCase struct {
	name   string
	object string
	// beside adds a second object with the real character, so a substitute
	// beside its original is observed as a pair.
	beside string
	// reserved says the object is carried natively.
	reserved bool
}

func nameEncodingCases() []nameEncodingCase {
	return []nameEncodingCase{
		{name: "quote_rune", object: "p‛q", reserved: true},
		{name: "null_picture", object: "s␀t", reserved: true},
		{name: "control_picture", object: "s␁t", reserved: true},
		{name: "del_picture", object: "s␡t", reserved: true},
		{name: "fullwidth_slash", object: "a／b", reserved: true},
		{name: "fullwidth_slash_beside_real", object: "a／b", beside: "a/b", reserved: true},
		{name: "fullwidth_colon", object: "a：b", reserved: true},
		{name: "fullwidth_backslash", object: "a＼b", reserved: true},
		{name: "fullwidth_stop_component", object: "d/．/e", reserved: true},
		{name: "fullwidth_stop_trailing", object: "f．", reserved: true},
		{name: "space_picture_trailing", object: "t␠", reserved: true},
		{name: "fullwidth_letter_control", object: "Ａbc"},
		{name: "control", object: "plain.txt"},
	}
}

func TestDownloadNameEncodingMock(t *testing.T) {
	t.Parallel()
	for _, c := range nameEncodingCases() {
		t.Run("download/"+c.name, func(t *testing.T) { downloadNameEncoding(t, findMockHarness(t), c) })
		t.Run("copy/"+c.name, func(t *testing.T) { copyNameEncoding(t, findMockHarness(t), c) })
	}
}

func TestDownloadNameEncodingRealSwift(t *testing.T) {
	t.Parallel()
	for _, c := range nameEncodingCases() {
		t.Run("download/"+c.name, func(t *testing.T) { downloadNameEncoding(t, New(t), c) })
		t.Run("copy/"+c.name, func(t *testing.T) { copyNameEncoding(t, New(t), c) })
	}
}

func nameEncodingObjects(c nameEncodingCase) []string {
	objects := []string{"safe.txt", c.object}
	if c.beside != "" {
		objects = append(objects, c.beside)
	}
	return objects
}

func nameEncodingBody(name string) []byte { return []byte("bytes for " + name) }

func nameEncodingNativeCount(c nameEncodingCase) int64 {
	if c.reserved {
		return 1
	}
	return 0
}

func downloadNameEncoding(t *testing.T, h *Harness, c nameEncodingCase) {
	t.Helper()
	source := h.Container(h.Primary, "name-encoding")
	owned := t.TempDir()
	selected := filepath.Join(owned, "selected")
	seedListingLocal(t, owned, "selected/keep", []byte("selected sentinel"))
	seedListingLocal(t, owned, "outside/keep", []byte("outside sentinel"))
	objects := nameEncodingObjects(c)
	for _, name := range objects {
		h.Seed(h.Primary, source, "scope/"+name, nameEncodingBody(name), nil)
	}
	h.Seed(h.Primary, source, "outside-selection", []byte("unselected remote sentinel"), nil)
	beforeA := h.SnapshotNamespace(h.Primary)
	trace := copyCompletionProxy(t, h, h.Primary, "")
	stageCalls := &atomic.Int32{}
	app.WireDownloadStageBoundary(h.Transfer, func(context.Context, string) error { stageCalls.Add(1); return nil })
	app.WireFilePicker(h.Transfer, listingFilePicker{dir: selected})
	picked, err := h.Transfer.PickDownloadDir(t.Context())
	if err != nil || picked == nil {
		t.Fatalf("pick: %+v %v", picked, err)
	}
	request := app.DownloadRequestDTO{BookmarkID: h.Primary.BookmarkID, Container: source, Prefix: "scope/", DestDirToken: picked.Token}
	preflight, err := h.Transfer.PreflightDownload(t.Context(), request)
	if err != nil || preflight == nil || preflight.Rejected() || len(preflight.Items) != len(objects) {
		t.Fatalf("selection was not admitted: %+v %v", preflight, err)
	}
	// The reserved name and only the reserved name leaves the engine route.
	for _, item := range preflight.Items {
		want := "rclone"
		if c.reserved && item.Name == "scope/"+c.object {
			want = "native"
		}
		if item.Route != want {
			t.Errorf("route for %q is %q, want %q", item.Name, item.Route, want)
		}
	}
	before := snapshotListingLocal(t, owned)
	id, err := h.Transfer.StartDownload(t.Context(), request)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	job := h.AwaitJob(id)
	copyCompletionAwaitPins(t, h, h.Primary)
	saved := uploadRecoveryRead(t, h.database, id)
	after := snapshotListingLocal(t, owned)
	if job.State != "complete" || job.Error != "" || job.Counters.ObjectsCopied != int64(len(objects)) || job.Counters.ObjectsVerified != int64(len(objects)) || job.Counters.NativeRouted != nameEncodingNativeCount(c) {
		t.Errorf("job outcome differs: state=%s error=%q counters=%+v totals=%+v", job.State, job.Error, job.Counters, saved.ItemTotals)
	}
	if saved.ItemTotals.Failed != 0 || saved.ItemTotals.Unresolved != 0 || saved.ItemTotals.Unverified != 0 {
		t.Errorf("item totals record a failure: %+v", saved.ItemTotals)
	}
	// Every object landed under its exact spelling with exact bytes, and
	// nothing else was created.
	files := map[string][]byte{}
	var dirs []string
	for _, name := range objects {
		files["selected/"+name] = nameEncodingBody(name)
		for dir := filepath.ToSlash(filepath.Dir(name)); dir != "."; dir = filepath.ToSlash(filepath.Dir(dir)) {
			dirs = append(dirs, "selected/"+dir)
		}
	}
	assertListingLocalDelta(t, before, after, files, dirs...)
	// The engine ran once, for the safe control, and never for a name
	// it cannot carry.
	if stageCalls.Load() != 1 {
		t.Errorf("engine boundary observed %d times", stageCalls.Load())
	}
	if len(trace.writeSnapshot()) != 0 {
		t.Errorf("download changed remote data: %v", trace.writeSnapshot())
	}
	h.AssertNoCollateral(beforeA, h.SnapshotNamespace(h.Primary))
}

func copyNameEncoding(t *testing.T, h *Harness, c nameEncodingCase) {
	t.Helper()
	source := h.Container(h.Primary, "name-encoding-src")
	target := h.Container(h.Secondary, "name-encoding-dst")
	objects := nameEncodingObjects(c)
	for _, name := range objects {
		h.Seed(h.Primary, source, "scope/"+name, nameEncodingBody(name), nil)
	}
	h.Seed(h.Primary, source, "outside-selection", []byte("unselected remote sentinel"), nil)
	h.Seed(h.Secondary, target, "keep", []byte("destination sentinel"), nil)
	srcBefore := h.SnapshotNamespace(h.Primary)
	dstBefore := h.SnapshotNamespace(h.Secondary)
	rec := h.StartCopy(t.Context(), app.CopyRequestDTO{
		SourceBookmarkID: h.Primary.BookmarkID, SourceContainer: source, SourcePrefix: "scope/",
		DestBookmarkID: h.Secondary.BookmarkID, DestContainer: target, DestPrefix: "landed/",
	})
	if rec.Err() != nil {
		t.Fatalf("start copy: %v", rec.Err())
	}
	job := h.AwaitJob(rec.JobID())
	copyCompletionAwaitPins(t, h, h.Primary)
	copyCompletionAwaitPins(t, h, h.Secondary)
	saved := uploadRecoveryRead(t, h.database, rec.JobID())
	dstAfter := h.SnapshotNamespace(h.Secondary)
	if job.State != "complete" || job.Error != "" || job.Counters.ObjectsCopied != int64(len(objects)) || job.Counters.NativeRouted != nameEncodingNativeCount(c) {
		t.Errorf("copy outcome differs: state=%s error=%q counters=%+v totals=%+v", job.State, job.Error, job.Counters, saved.ItemTotals)
	}
	if saved.ItemTotals.Failed != 0 || saved.ItemTotals.Unresolved != 0 || saved.ItemTotals.Unverified != 0 {
		t.Errorf("item totals record a failure: %+v", saved.ItemTotals)
	}
	landed := dstAfter.Containers[target].Objects
	var extra []string
	for name := range landed {
		if name == "keep" {
			continue
		}
		found := false
		for _, object := range objects {
			found = found || name == "landed/"+object
		}
		if !found {
			extra = append(extra, name+":"+strconv.FormatInt(landed[name].Size, 10))
		}
	}
	sort.Strings(extra)
	if len(extra) != 0 {
		t.Errorf("copy landed unexpected names: %v", extra)
	}
	// Destination content matches the source exactly, kind for kind.
	for _, name := range objects {
		object, found := landed["landed/"+name]
		want := srcBefore.Containers[source].Objects["scope/"+name]
		if !found || object.Size != want.Size || object.Digest != want.Digest || object.Size != int64(len(nameEncodingBody(name))) {
			t.Errorf("copied object %q differs at the destination: found=%v got=%+v want=%+v", name, found, object, want)
		}
	}
	if d := diffOf(srcBefore, h.SnapshotNamespace(h.Primary)); !d.Empty() {
		t.Errorf("source account changed during a copy:\n%s", d)
	}
	allowed := make([]string, 0, len(objects))
	for _, name := range objects {
		allowed = append(allowed, target+"/landed/"+name)
	}
	h.AssertNoCollateral(dstBefore, dstAfter, allowed...)
}
