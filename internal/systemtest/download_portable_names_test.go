package systemtest

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/csmart/aeronautes/internal/app"
	"github.com/csmart/aeronautes/internal/audit"
	"github.com/csmart/aeronautes/internal/jobs"
)

func TestDownloadPortableNamesMock(t *testing.T) {
	t.Parallel()
	downloadPortableNameCases(t, findMockHarness)
}

func TestDownloadPortableNamesRealSwift(t *testing.T) {
	t.Parallel()
	downloadPortableNameCases(t, New)
}

func downloadPortableNameCases(t *testing.T, makeHarness func(*testing.T) *Harness) {
	for _, route := range []string{"engine", "native", "marker"} {
		for _, shape := range []string{"device_leaf", "device_extension", "device_ancestor", "trailing_dot_ancestor", "restricted_colon", "near_miss", "shared_parent"} {
			t.Run(route+"/"+shape, func(t *testing.T) { downloadPortableNameService(t, makeHarness(t), route, shape) })
		}
	}
}

func downloadPortableNameService(t *testing.T, h *Harness, route, shape string) {
	t.Helper()
	source := h.Container(h.Primary, "download-portable-names")
	other := h.Container(h.Secondary, "download-portable-names-other")
	owned := t.TempDir()
	selected := filepath.Join(owned, "selected")
	seedListingLocal(t, owned, "selected/keep", []byte("selected sentinel\x00\xff"))
	seedListingLocal(t, owned, "outside/keep", []byte("outside sentinel\x00\xff"))
	seedListingLocal(t, owned, "unrelated/keep", []byte("unrelated sentinel\x00\xff"))
	prefix, first := "scope/", "000-safe.bin"
	if route == "native" {
		prefix = "scope\n/"
	}
	if route == "marker" {
		first = "000-safe/"
	}
	// refused names the portable policy must reject; component is the exact
	// spelling the diagnostic must quote.
	names, refused, component := []string{first}, "", ""
	switch shape {
	case "device_leaf":
		refused, component = "CON", "CON"
	case "device_extension":
		refused, component = "NUL.txt", "NUL.txt"
	case "device_ancestor":
		refused, component = "parent/CON/child.bin", "CON"
	case "trailing_dot_ancestor":
		refused, component = "parent./child.bin", "parent."
	case "restricted_colon":
		refused, component = "bad:name", "bad:name"
	case "near_miss":
		names = append(names, "NULx", "x.NUL")
	case "shared_parent":
		names = append(names, "foo/one", "foo/two")
	default:
		t.Fatalf("unknown portable-name fixture %q", shape)
	}
	if refused != "" {
		if route == "marker" {
			refused = strings.TrimSuffix(refused, ".bin") + "/"
		}
		names = append(names, refused)
	} else if route == "marker" {
		for index := 1; index < len(names); index++ {
			names[index] += "/"
		}
	}
	bodies := map[string][]byte{}
	var totalBytes, fileCount int64
	for _, name := range names {
		body := []byte("complete source bytes\x00\xff " + route + "/" + shape + "/" + name)
		if strings.HasSuffix(name, "/") {
			body = nil
		} else {
			fileCount++
		}
		bodies[name] = body
		totalBytes += int64(len(body))
		h.Seed(h.Primary, source, prefix+name, body, nil)
	}
	h.Seed(h.Primary, source, "outside-selection", []byte("unselected remote sentinel\x00\xff"), nil)
	h.Seed(h.Secondary, other, "keep", []byte("other remote sentinel\x00\xff"), nil)
	beforeA, beforeB := h.SnapshotNamespace(h.Primary), h.SnapshotNamespace(h.Secondary)
	trace := downloadRecordsProxy(t, h, source, prefix, nil)
	app.WireFilePicker(h.Transfer, listingFilePicker{dir: selected})
	picked, err := h.Transfer.PickDownloadDir(t.Context(), app.SelectionScopeDTO{BookmarkID: h.Primary.BookmarkID, Container: source, Prefix: prefix})
	if err != nil || picked == nil || picked.Token == "" || picked.Dir != selected {
		t.Fatalf("owned picker did not preserve the selected spelling: %+v %v", picked, err)
	}
	request := app.DownloadRequestDTO{BookmarkID: h.Primary.BookmarkID, Container: source, Prefix: prefix, DestDirToken: picked.Token}
	beforeLocal, beforeCounts := snapshotListingLocal(t, owned), downloadRootGuardCounts(t, h)
	preflight, preflightErr := h.Transfer.PreflightDownload(t.Context(), request)
	if !reflect.DeepEqual(beforeLocal, snapshotListingLocal(t, owned)) || !reflect.DeepEqual(beforeCounts, downloadRootGuardCounts(t, h)) {
		t.Error("portable-name preflight changed owned entries or history")
	}
	var returnedCopies atomic.Int32
	app.WireDownloadStageBoundary(h.Transfer, func(context.Context, string) error { returnedCopies.Add(1); return nil })
	id, startErr := h.Transfer.StartDownload(t.Context(), request)
	var saved uploadRecoveryState
	if id != "" {
		h.AwaitJob(id)
		copyCompletionAwaitPins(t, h, h.Primary)
		saved = uploadRecoveryRead(t, h.database, id)
		public, err := h.Jobs.Get(t.Context(), id)
		if err != nil || !reflect.DeepEqual(public, saved.Job) {
			t.Errorf("public job differs from durable portable-name outcome: %+v error=%v", public, err)
		}
		byKey := map[string]jobs.ItemRecord{}
		for _, item := range saved.Items {
			byKey[item.Key] = item
		}
		assertDownloadItemPages(t, h, id, byKey, saved.ItemTotals)
	}
	events, err := app.NewAuditService(audit.NewStore(h.database)).Search(t.Context(), app.AuditQueryDTO{OperationPrefix: "transfer.", Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	afterLocal, afterCounts := snapshotListingLocal(t, owned), downloadRootGuardCounts(t, h)
	afterA, afterB := h.SnapshotNamespace(h.Primary), h.SnapshotNamespace(h.Secondary)
	evidence, err := json.Marshal(map[string]any{
		"preflight": preflight, "preflight_error": fmt.Sprint(preflightErr), "job_id": id, "start_error": fmt.Sprint(startErr),
		"before_counts": beforeCounts, "after_counts": afterCounts, "saved": saved, "transfer_audit": events,
		"selected_gets": trace.selectedGets(), "returned_Copy_calls": returnedCopies.Load(), "remote_writes": trace.traffic.writeSnapshot(),
		"before_primary": beforeA, "after_primary": afterA, "before_secondary": beforeB, "after_secondary": afterB,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("portable-name service evidence: %s", evidence)
	t.Logf("owned local before=%+v after=%+v", beforeLocal, afterLocal)
	h.AssertNoCollateral(beforeA, afterA)
	h.AssertNoCollateral(beforeB, afterB)
	if len(trace.traffic.writeSnapshot()) != 0 {
		t.Errorf("download changed remote data: %v", trace.traffic.writeSnapshot())
	}
	if refused != "" {
		if preflightErr != nil || preflight == nil || !preflight.Rejected {
			t.Errorf("unportable component lacked preflight refusal: %+v error=%v", preflight, preflightErr)
		} else if int(preflight.ProblemCount) != 1 || preflight.ProblemSamples[0].Kind != "unportable-name" || preflight.ProblemSamples[0].Name != prefix+refused ||
			!strings.Contains(preflight.ProblemSamples[0].Detail, fmt.Sprintf("%q", component)) {
			t.Errorf("portable diagnostic lost the selected spelling: %+v", preflight.ProblemSamples)
		}
		if id != "" || startErr == nil || !strings.Contains(startErr.Error(), "preflight found problems") {
			t.Errorf("Start admitted an unportable component: id=%q error=%v", id, startErr)
		}
		if !reflect.DeepEqual(beforeCounts, afterCounts) || len(events) != 0 || len(trace.selectedGets()) != 0 || returnedCopies.Load() != 0 {
			t.Errorf("unportable refusal admitted history or content work: counts=%v -> %v gets=%v returned Copy calls=%d audit=%+v", beforeCounts, afterCounts, trace.selectedGets(), returnedCopies.Load(), events)
		}
		assertListingLocalDelta(t, beforeLocal, afterLocal, nil)
		return
	}
	count := int64(len(names))
	if preflightErr != nil || preflight == nil || preflight.Rejected || int(preflight.Objects) != len(names) || preflight.ItemSamples[0].Name != prefix+first || id == "" || startErr != nil {
		t.Fatalf("portable near-miss control was not admitted: preflight=%+v error=%v id=%q error=%v", preflight, preflightErr, id, startErr)
	}
	want := jobs.Counters{ObjectsPlanned: count, BytesPlanned: totalBytes, ObjectsCopied: count, BytesCopied: totalBytes, ObjectsVerified: count}
	if route == "native" {
		want.NativeRouted = fileCount
	}
	if saved.Job.State != "complete" || saved.Job.Error != "" || saved.Job.Counters != want || !saved.ItemsCaptured || int64(len(saved.Items)) != count ||
		saved.ItemTotals != (jobs.ItemTotals{Items: count, Planned: count, Admitted: count}) || len(saved.Rows) != 0 || len(saved.Audit) != 1 || len(events) != 2 ||
		saved.Audit[0].Operation != "transfer.local.complete" || saved.Audit[0].Resource != "job "+id || saved.Audit[0].Result != "applied" ||
		saved.Audit[0].Summary != " · item outcomes: 0 failed names, 0 conflicts, 0 refusals, 0 unresolved, 0 unverified, 0 with problems" {
		t.Fatalf("near-miss control lost exact job/item/audit outcome: %+v audit=%+v", saved, events)
	}
	wantCounts := append([]int64(nil), beforeCounts...)
	wantCounts[0]++
	wantCounts[2] += count
	wantCounts[3] += 2 * count
	wantCounts[4] += 2
	if !reflect.DeepEqual(afterCounts, wantCounts) {
		t.Errorf("near-miss control left unexpected history: got=%v want=%v", afterCounts, wantCounts)
	}
	starts := 0
	for _, event := range events {
		if event.Operation == "transfer.download.start" {
			starts++
			if event.Result != "applied" || event.Resource != source+"/"+prefix+" → local directory (picker-selected)" || !strings.HasPrefix(event.Summary, "job "+id+" · ") {
				t.Errorf("near-miss control lost exact start audit identity: %+v", event)
			}
		}
	}
	if starts != 1 {
		t.Errorf("near-miss control start audit count=%d want=1", starts)
	}
	files, directories := map[string][]byte{}, map[string]bool{}
	for _, name := range names {
		rel := filepath.FromSlash(strings.TrimSuffix(name, "/"))
		path := filepath.Join(selected, rel)
		if !strings.HasSuffix(name, "/") {
			files["selected/"+name] = bodies[name]
			path = filepath.Dir(path)
		}
		for path != selected {
			relative, err := filepath.Rel(owned, path)
			if err != nil {
				t.Fatal(err)
			}
			directories[filepath.ToSlash(relative)] = true
			path = filepath.Dir(path)
		}
	}
	var newDirectories []string
	for directory := range directories {
		newDirectories = append(newDirectories, directory)
	}
	assertListingLocalDelta(t, beforeLocal, afterLocal, files, newDirectories...)
	for _, item := range saved.Items {
		name := strings.TrimPrefix(item.Key, prefix)
		body, exists := bodies[name]
		rel := filepath.FromSlash(strings.TrimSuffix(name, "/"))
		directory := strings.HasSuffix(name, "/")
		if !exists || item.Key != prefix+name || !item.Planned || !item.Admitted || item.ListedBytes != int64(len(body)) ||
			item.Source != (jobs.ItemLocation{Kind: jobs.ItemSwift, BookmarkID: h.Primary.BookmarkID, Container: source, Object: prefix + name}) ||
			item.Destination != (jobs.ItemLocation{Kind: jobs.ItemLocal, LocalPath: filepath.Join(selected, rel)}) ||
			item.Execution == nil || item.Execution.ItemObservation != (jobs.ItemObservation{Result: jobs.ItemCopied, Effect: jobs.EffectReportedApplied, CopiedBytes: int64(len(body))}) ||
			item.Verification == nil || item.Verification.ItemObservation != (jobs.ItemObservation{Result: jobs.ItemVerified, Effect: jobs.EffectNotAttempted}) || (item.Staging == nil) != directory {
			t.Fatalf("near-miss control lost exact identity or item observations: %+v", item)
		}
		if !directory {
			data, err := os.ReadFile(filepath.Join(selected, rel))
			if err != nil || !bytes.Equal(data, body) {
				t.Errorf("near-miss control full file bytes differ for %q: %q error=%v", name, data, err)
			}
		}
	}
	wantCopies := int32(0)
	if route != "native" && fileCount > 0 {
		wantCopies = 1
	}
	if returnedCopies.Load() != wantCopies {
		t.Errorf("near-miss control route differs: returned Copy calls=%d want=%d", returnedCopies.Load(), wantCopies)
	}
	gets := trace.selectedGets()
	for _, name := range names {
		if strings.HasSuffix(name, "/") && gets[name] != 0 || !strings.HasSuffix(name, "/") && gets[name] == 0 || route == "native" && !strings.HasSuffix(name, "/") && gets[name] != 1 {
			t.Errorf("near-miss control source-read route differs for %q: %v", name, gets)
		}
	}
}
