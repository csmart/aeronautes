package systemtest

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/csmart/aeronautes/internal/app"
	"github.com/csmart/aeronautes/internal/audit"
	"github.com/csmart/aeronautes/internal/jobs"
	"github.com/csmart/aeronautes/internal/transfer/local"
)

// A selection holding two spellings of one name that differ only by
// Unicode normalization form cannot be preserved on a normalizing target.
// The service must refuse the whole selection before any job, item, audit
// record, source read or local effect, on every route and both providers,
// and the refusals the established case identity already produces must
// keep their own diagnostics.

const (
	// Built from explicit escapes: a decomposed sequence typed as a
	// literal is normalized on its way into the file, which would seed one
	// object twice and quietly prove nothing.
	nameFormDotAbove = "\u0307"
	nameFormAcute    = "\u0301"
	nameFormNFC      = "\u00e9tude"
	nameFormNFD      = "e" + nameFormAcute + "tude"
	nameFormDottedI  = "\u0130"
)

func TestDownloadNameFormMock(t *testing.T) {
	t.Parallel()
	downloadNameFormCases(t, findMockHarness)
}

func TestDownloadNameFormRealSwift(t *testing.T) {
	t.Parallel()
	downloadNameFormCases(t, New)
}

func downloadNameFormCases(t *testing.T, makeHarness func(*testing.T) *Harness) {
	for _, route := range []string{"engine", "native", "marker"} {
		for _, shape := range []string{
			"twin_file", "ancestor_precomposed", "ancestor_decomposed",
			"dotted_i_canonical", "dotted_i_case", "dotted_i_plain", "mixed_case_and_form",
			"safe_distinct", "safe_lone_decomposed", "safe_lone_precomposed",
			"safe_ligature", "safe_plain_i_vs_i_dot",
		} {
			t.Run(route+"/"+shape, func(t *testing.T) { downloadNameFormService(t, makeHarness(t), route, shape) })
		}
	}
}

func downloadNameFormService(t *testing.T, h *Harness, route, shape string) {
	t.Helper()
	source := h.Container(h.Primary, "download-name-form")
	other := h.Container(h.Secondary, "download-name-form-other")
	owned := t.TempDir()
	selected := filepath.Join(owned, "selected")
	seedListingLocal(t, owned, "selected/keep", []byte("selected sentinel\x00\xff"))
	seedListingLocal(t, owned, "outside/keep", []byte("outside sentinel\x00\xff"))
	prefix, first := "scope/", "000-safe.bin"
	if route == "native" {
		prefix = "scope\n/"
	}
	if route == "marker" {
		first = "000-safe/"
	}

	// refusal names the two spellings a diagnostic must quote; kind is the
	// expected problem kind; ancestor names the file a file-vs-prefix
	// refusal must identify.
	names := []string{first}
	var kind, ancestor string
	var quoted []string
	switch shape {
	case "twin_file":
		kind = "name-form"
		names = append(names, nameFormNFC+".bin", nameFormNFD+".bin")
		quoted = []string{nameFormNFC + ".bin", nameFormNFD + ".bin"}
	case "ancestor_precomposed":
		ancestor = nameFormNFC
		names = append(names, nameFormNFC, nameFormNFD+"/child.bin")
		if route == "marker" {
			names[2] = nameFormNFD + "/"
		}
	case "ancestor_decomposed":
		ancestor = nameFormNFD
		names = append(names, nameFormNFD, nameFormNFC+"/child.bin")
		if route == "marker" {
			names[2] = nameFormNFC + "/"
		}
	case "dotted_i_canonical":
		kind = "name-form"
		names = append(names, nameFormDottedI+".bin", "I"+nameFormDotAbove+".bin")
		quoted = []string{nameFormDottedI + ".bin", "I" + nameFormDotAbove + ".bin"}
	case "dotted_i_case":
		kind = "case-fold"
		names = append(names, "I"+nameFormDotAbove+".bin", "i"+nameFormDotAbove+".bin")
		quoted = []string{"I" + nameFormDotAbove + ".bin", "i" + nameFormDotAbove + ".bin"}
	case "dotted_i_plain":
		// Carried by the established lowercase relation alone.
		kind = "case-fold"
		names = append(names, nameFormDottedI+".bin", "i.bin")
		quoted = []string{nameFormDottedI + ".bin", "i.bin"}
	case "mixed_case_and_form":
		// Carried by the caseless relation alone.
		kind = "name-form"
		names = append(names, "\u00c9tude.bin", nameFormNFD+".bin")
		quoted = []string{"\u00c9tude.bin", nameFormNFD + ".bin"}
	case "safe_distinct":
		names = append(names, "resume.bin", "r\u00e9sum\u00e9.bin")
		if route == "marker" {
			names[1], names[2] = "resume/", "r\u00e9sum\u00e9/"
		}
	case "safe_lone_decomposed":
		names = append(names, nameFormNFD+".bin")
		if route == "marker" {
			names[1] = nameFormNFD + "/"
		}
	case "safe_lone_precomposed":
		names = append(names, nameFormNFC+".bin")
		if route == "marker" {
			names[1] = nameFormNFC + "/"
		}
	case "safe_ligature":
		names = append(names, "\ufb01le.bin", "file.bin")
		if route == "marker" {
			names[1], names[2] = "\ufb01le/", "file/"
		}
	case "safe_plain_i_vs_i_dot":
		// The admitted consequence of not closing the union.
		names = append(names, "i.bin", "i"+nameFormDotAbove+".bin")
		if route == "marker" {
			names[1], names[2] = "i/", "i"+nameFormDotAbove+"/"
		}
	default:
		t.Fatalf("unknown name-form fixture %q", shape)
	}
	refused := kind != "" || ancestor != ""

	seeded := map[string]bool{}
	bodies := map[string][]byte{}
	for _, name := range names {
		if seeded[name] {
			t.Fatalf("fixture seeds %q twice: the two spellings are not distinct", name)
		}
		seeded[name] = true
		body := []byte("complete source bytes\x00\xff " + route + "/" + shape + "/" + name)
		if strings.HasSuffix(name, "/") {
			body = nil
		}
		bodies[name] = body
		h.Seed(h.Primary, source, prefix+name, body, nil)
	}
	h.Seed(h.Primary, source, "outside-selection", []byte("unselected remote sentinel\x00\xff"), nil)
	h.Seed(h.Secondary, other, "keep", []byte("other remote sentinel\x00\xff"), nil)

	// The provider must return every seeded spelling, or the fixture has
	// not exercised the collision at all.
	listed := h.SnapshotNamespace(h.Primary)
	found := 0
	for _, name := range names {
		if strings.Contains(fmt.Sprint(listed), prefix+name) {
			found++
		}
	}
	beforeA, beforeB := h.SnapshotNamespace(h.Primary), h.SnapshotNamespace(h.Secondary)
	trace := downloadRecordsProxy(t, h, source, prefix, nil)
	app.WireFilePicker(h.Transfer, listingFilePicker{dir: selected})
	picked, err := h.Transfer.PickDownloadDir(t.Context())
	if err != nil || picked == nil || picked.Token == "" || picked.Dir != selected {
		t.Fatalf("owned picker did not preserve the selected spelling: %+v %v", picked, err)
	}
	request := app.DownloadRequestDTO{BookmarkID: h.Primary.BookmarkID, Container: source, Prefix: prefix, DestDirToken: picked.Token}
	beforeLocal, beforeCounts := snapshotListingLocal(t, owned), downloadRootGuardCounts(t, h)
	preflight, preflightErr := h.Transfer.PreflightDownload(t.Context(), request)
	if !reflect.DeepEqual(beforeLocal, snapshotListingLocal(t, owned)) || !reflect.DeepEqual(beforeCounts, downloadRootGuardCounts(t, h)) {
		t.Error("name-form preflight changed owned entries or history")
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
			t.Errorf("public job differs from the durable outcome: %+v error=%v", public, err)
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
		"route": route, "shape": shape, "seeded_names": names, "listed_matches": found,
		"preflight": preflight, "preflight_error": fmt.Sprint(preflightErr), "job_id": id, "start_error": fmt.Sprint(startErr),
		"before_counts": beforeCounts, "after_counts": afterCounts, "transfer_audit": events,
		"selected_gets": trace.selectedGets(), "returned_Copy_calls": returnedCopies.Load(), "remote_writes": trace.traffic.writeSnapshot(),
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("name-form service evidence: %s", evidence)
	t.Logf("owned local before=%+v after=%+v", beforeLocal, afterLocal)
	h.AssertNoCollateral(beforeA, afterA)
	h.AssertNoCollateral(beforeB, afterB)
	if len(trace.traffic.writeSnapshot()) != 0 {
		t.Errorf("download changed remote data: %v", trace.traffic.writeSnapshot())
	}
	if !refused {
		// An accepted control must prove the download happened. An
		// executor that admits everything and then reports success
		// without copying must fail every assertion below.
		if preflightErr != nil || preflight == nil || preflight.Rejected() || len(preflight.Items) != len(names) || id == "" || startErr != nil {
			t.Fatalf("accepted control was not admitted: preflight=%+v error=%v id=%q error=%v", preflight, preflightErr, id, startErr)
		}
		for _, name := range names {
			if !slices.ContainsFunc(preflight.Items, func(item local.DownloadItem) bool { return item.Name == prefix+name }) {
				t.Errorf("accepted control lost the exact selected spelling %q: %+v", name, preflight.Items)
			}
		}
		count := int64(len(names))
		var totalBytes, fileCount int64
		for _, name := range names {
			if !strings.HasSuffix(name, "/") {
				fileCount++
				totalBytes += int64(len(bodies[name]))
			}
		}
		want := jobs.Counters{ObjectsPlanned: count, BytesPlanned: totalBytes, ObjectsCopied: count, BytesCopied: totalBytes, ObjectsVerified: count}
		if route == "native" {
			want.NativeRouted = fileCount
		}
		if saved.Job.State != "complete" || saved.Job.Error != "" || saved.Job.Counters != want || !saved.ItemsCaptured ||
			int64(len(saved.Items)) != count || saved.ItemTotals != (jobs.ItemTotals{Items: count, Planned: count, Admitted: count}) ||
			len(saved.Rows) != 0 || len(saved.Audit) != 1 || len(events) != 2 ||
			saved.Audit[0].Operation != "transfer.local.complete" || saved.Audit[0].Resource != "job "+id || saved.Audit[0].Result != "applied" {
			t.Fatalf("accepted control lost the exact job, item or audit outcome: %+v audit=%+v", saved, events)
		}
		wantCounts := append([]int64(nil), beforeCounts...)
		wantCounts[0]++
		wantCounts[2] += count
		wantCounts[3] += 2 * count
		wantCounts[4] += 2
		if !reflect.DeepEqual(afterCounts, wantCounts) {
			t.Errorf("accepted control left unexpected history: got=%v want=%v", afterCounts, wantCounts)
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
				item.Verification == nil || item.Verification.ItemObservation != (jobs.ItemObservation{Result: jobs.ItemVerified, Effect: jobs.EffectNotAttempted}) ||
				(item.Staging == nil) != directory {
				t.Fatalf("accepted control lost exact identity or item observations: %+v", item)
			}
			if !directory {
				data, err := os.ReadFile(filepath.Join(selected, rel))
				if err != nil || !bytes.Equal(data, body) {
					t.Errorf("accepted control full file bytes differ for %q: %q error=%v", name, data, err)
				}
			}
		}
		wantCopies := int32(0)
		if route != "native" && fileCount > 0 {
			wantCopies = 1
		}
		if returnedCopies.Load() != wantCopies {
			t.Errorf("accepted control route differs: returned Copy calls=%d want=%d", returnedCopies.Load(), wantCopies)
		}
		gets := trace.selectedGets()
		for _, name := range names {
			if strings.HasSuffix(name, "/") && gets[name] != 0 ||
				!strings.HasSuffix(name, "/") && gets[name] == 0 ||
				route == "native" && !strings.HasSuffix(name, "/") && gets[name] != 1 {
				t.Errorf("accepted control source-read route differs for %q: %v", name, gets)
			}
		}
		return
	}
	if preflightErr != nil || preflight == nil || !preflight.Rejected() {
		t.Fatalf("name-form collision lacked preflight refusal: %+v error=%v", preflight, preflightErr)
	}
	if len(preflight.Problems) != 1 {
		t.Fatalf("name-form collision produced %d problems, want one: %+v", len(preflight.Problems), preflight.Problems)
	}
	problem := preflight.Problems[0]
	if ancestor != "" {
		if problem.Kind != "file-vs-prefix" || problem.Name != prefix+ancestor || !strings.Contains(problem.Detail, ancestor) {
			t.Errorf("name-form ancestor diagnostic lost the selected spelling: %+v", problem)
		}
	} else {
		if problem.Kind != kind {
			t.Errorf("name-form twin diagnostic used kind %q, want %q: %+v", problem.Kind, kind, problem)
		}
		for _, spelling := range quoted {
			if !strings.Contains(problem.Detail, spelling) {
				t.Errorf("name-form twin diagnostic lost the exact spelling %q: %+v", spelling, problem)
			}
		}
	}
	if id != "" || startErr == nil || !strings.Contains(startErr.Error(), "preflight found problems") {
		t.Errorf("Start admitted a name-form collision: id=%q error=%v", id, startErr)
	}
	if !reflect.DeepEqual(beforeCounts, afterCounts) || len(events) != 0 || len(trace.selectedGets()) != 0 || returnedCopies.Load() != 0 {
		t.Errorf("name-form refusal admitted history or content work: counts=%v -> %v gets=%v returned Copy calls=%d audit=%+v",
			beforeCounts, afterCounts, trace.selectedGets(), returnedCopies.Load(), events)
	}
	assertListingLocalDelta(t, beforeLocal, afterLocal, nil)
}
