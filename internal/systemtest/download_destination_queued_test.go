package systemtest

import (
	"bytes"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/csmart/aeronautes/internal/app"
	"github.com/csmart/aeronautes/internal/jobs"
)

func downloadDestinationQueued(t *testing.T, h *Harness, route, fault string) {
	t.Helper()
	source := h.Container(h.Primary, "queued-destination")
	other := h.Container(h.Secondary, "queued-destination-other")
	holdBody, body := []byte("holding bytes\x00\xff"), []byte("queued selected bytes")
	h.Seed(h.Primary, source, "hold/first.bin", holdBody, nil)
	h.Seed(h.Primary, source, "outside", []byte("unselected source"), nil)
	h.Seed(h.Secondary, other, "keep", []byte("other account"), nil)
	prefix, relative := "scope/", "nested/proof.bin"
	if route == "native" {
		prefix = "scope\n/"
	} else if route == "marker" {
		relative, body = "nested/", nil
	}
	count := int64(1)
	if route == "empty" {
		count = 0
	} else {
		h.Seed(h.Primary, source, prefix+relative, body, nil)
	}
	beforeA, beforeB := h.SnapshotNamespace(h.Primary), h.SnapshotNamespace(h.Secondary)
	// The holding job's engine is held at its read of its one file, so the
	// selected job queues behind it and its destination can change first.
	reached, release := make(chan struct{}), make(chan struct{})
	var reachedOnce, releaseOnce sync.Once
	t.Cleanup(func() { releaseOnce.Do(func() { close(release) }) })
	trace := downloadRecordsProxy(t, h, source, "", nil, func(method, object string) {
		if method != http.MethodGet || object != "hold/first.bin" {
			return
		}
		reachedOnce.Do(func() { close(reached) })
		select {
		case <-release:
		case <-time.After(30 * time.Second):
		}
	})
	settings, err := h.Settings.Get(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	settings.MaxActiveJobs = 1
	if _, err := h.Settings.Set(t.Context(), settings); err != nil {
		t.Fatal(err)
	}
	firstRoot, owned := t.TempDir(), t.TempDir()
	seedListingLocal(t, firstRoot, "keep", []byte("holding destination sentinel"))
	seedListingLocal(t, owned, "selected/keep", []byte("queued destination sentinel"))
	seedListingLocal(t, owned, "outside/keep", []byte("outside destination sentinel"))
	selected := filepath.Join(owned, "selected")
	pick := func(path, prefix string) app.DownloadRequestDTO {
		app.WireFilePicker(h.Transfer, listingFilePicker{dir: path})
		picked, err := h.Transfer.PickDownloadDir(t.Context(), app.SelectionScopeDTO{BookmarkID: h.Primary.BookmarkID, Container: source, Prefix: prefix})
		if err != nil || picked == nil || picked.Dir != path {
			t.Fatalf("queued fixture picker: %+v error=%v", picked, err)
		}
		return app.DownloadRequestDTO{BookmarkID: h.Primary.BookmarkID, Container: source, Prefix: prefix, DestDirToken: picked.Token}
	}
	firstRequest, request := pick(firstRoot, "hold/"), pick(selected, prefix)
	preflight, err := h.Transfer.PreflightDownload(t.Context(), request)
	if err != nil || preflight == nil || preflight.Rejected || preflight.Objects != count {
		t.Fatalf("queued fixture was not initially admissible: %+v error=%v", preflight, err)
	}
	firstBefore := snapshotListingLocal(t, firstRoot)
	first, err := h.Transfer.StartDownload(t.Context(), firstRequest)
	if err != nil || first == "" {
		t.Fatalf("holding download: %q error=%v", first, err)
	}
	select {
	case <-reached:
	case <-time.After(30 * time.Second):
		t.Fatal("holding download's engine never read its file")
	}
	started := h.StartDownload(t.Context(), request)
	id, err := started.JobID(), started.Err()
	if err != nil || id == "" {
		t.Fatalf("initially valid queued download: %q error=%v", id, err)
	}
	queued, err := h.Jobs.Get(t.Context(), h.Primary.BookmarkID, id)
	if err != nil || queued.State != "queued" || queued.StartedAt != "" {
		t.Fatalf("fixture changed the destination after execution already began: %+v error=%v", queued, err)
	}
	switch fault {
	case "parent_file":
		seedListingLocal(t, owned, "selected/nested", []byte("late parent file"))
	case "file_appeared":
		seedListingLocal(t, owned, "selected/"+relative, []byte("the operator's own file"))
	default:
		if err := os.Remove(filepath.Join(selected, "keep")); err != nil {
			t.Fatal(err)
		}
		if err := os.Remove(selected); err != nil {
			t.Fatal(err)
		}
	}
	before := snapshotListingLocal(t, owned)
	releaseOnce.Do(func() { close(release) })
	h.AwaitJob(first)
	h.AwaitJob(id)
	copyCompletionAwaitPins(t, h, h.Primary)
	saved, firstSaved := uploadRecoveryRead(t, h.database, id), uploadRecoveryRead(t, h.database, first)
	if fault == "file_appeared" {
		// ADR 0122 decision 5: a file that has appeared since the preview is
		// left alone and reported as not downloaded because it exists; the
		// job is not refused as a whole.
		conflict := jobs.ItemObservation{Result: jobs.ItemConflict, Effect: jobs.EffectNotAttempted, Diagnostic: jobs.ItemDiagnostic{Stage: jobs.StageCommit, Code: jobs.CodeConflict}}
		if saved.Job.State != "partial" || len(saved.Items) != 1 || saved.Items[0].Execution == nil || saved.Items[0].Execution.ItemObservation != conflict {
			t.Errorf("a file that appeared while the job was queued: job=%+v items=%+v, want partial with the file a conflict", saved.Job, saved.Items)
		}
		assertListingLocalDelta(t, before, snapshotListingLocal(t, owned), nil)
		if gets := trace.selectedGets(); len(gets) != 1 || gets["hold/first.bin"] == 0 {
			t.Errorf("the operator's file was read for: %v", gets)
		}
		// The one way a download job ends in a conflict (ADR 0122 decision
		// 13): this case credits that cell of the scenario contract.
		started.CreditJobConflict()
		return
	}
	if fault == "parent_file" {
		// ADR 0122 decision 5: a folder that is no longer a folder fails the
		// item under it, and nothing is written there; the job is not
		// refused as a whole.
		item := jobs.ItemRecord{}
		if len(saved.Items) == 1 {
			item = saved.Items[0]
		}
		if saved.Job.State != "partial" || len(saved.Items) != 1 || item.Execution == nil || item.Execution.Result != jobs.ItemFailed || saved.Job.Counters.ObjectsCopied != 0 {
			t.Errorf("a folder that became a file while the job was queued: job=%+v items=%+v, want partial with the item failed", saved.Job, saved.Items)
		}
		assertListingLocalDelta(t, before, snapshotListingLocal(t, owned), nil)
		return
	}
	visible, err := h.Jobs.Get(t.Context(), h.Primary.BookmarkID, id)
	want := jobs.Counters{ObjectsPlanned: count, BytesPlanned: count * int64(len(body))}
	if err != nil || !reflect.DeepEqual(visible, saved.Job) || saved.Job.State != "failed" || saved.Job.Counters != want || saved.Job.StartedAt == "" || saved.Job.FinishedAt == "" ||
		saved.Job.Error == "" || !saved.ItemsCaptured || len(saved.Items) != 0 || saved.ItemTotals != (jobs.ItemTotals{}) || len(saved.Rows) != 0 {
		t.Errorf("executor-entry refusal lost the admitted job or invented item effects: %+v error=%v", saved, err)
	}
	assertDownloadItemPages(t, h, id, map[string]jobs.ItemRecord{}, jobs.ItemTotals{})
	firstWant := jobs.Counters{ObjectsPlanned: 1, BytesPlanned: int64(len(holdBody)), ObjectsCopied: 1, BytesCopied: int64(len(holdBody)), ObjectsVerified: 1}
	firstTotals := jobs.ItemTotals{Items: 1, Planned: 1, Admitted: 1}
	if firstSaved.Job.State != "complete" || firstSaved.Job.Counters != firstWant || firstSaved.Job.Error != "" || !firstSaved.ItemsCaptured || len(firstSaved.Items) != 1 || firstSaved.ItemTotals != firstTotals {
		t.Fatalf("queue control lost the holding job's outcome: %+v", firstSaved)
	}
	firstItem := firstSaved.Items[0]
	if firstItem.Key != "hold/first.bin" || firstItem.Source != (jobs.ItemLocation{Kind: jobs.ItemSwift, BookmarkID: h.Primary.BookmarkID, Container: source, Object: firstItem.Key}) ||
		firstItem.Destination != (jobs.ItemLocation{Kind: jobs.ItemLocal, LocalPath: filepath.Join(firstRoot, "first.bin")}) ||
		!firstItem.Planned || !firstItem.Admitted || firstItem.ListedBytes != int64(len(holdBody)) || firstItem.Execution == nil ||
		firstItem.Execution.ItemObservation != (jobs.ItemObservation{Result: jobs.ItemCopied, Effect: jobs.EffectReportedApplied, CopiedBytes: int64(len(holdBody))}) ||
		firstItem.Verification == nil || firstItem.Verification.ItemObservation != (jobs.ItemObservation{Result: jobs.ItemVerified, Effect: jobs.EffectNotAttempted}) {
		t.Errorf("holding job lost its exact item observations: %+v", firstItem)
	}
	assertDownloadItemPages(t, h, first, map[string]jobs.ItemRecord{firstItem.Key: firstItem}, firstTotals)
	if len(saved.Audit) != 2 {
		t.Errorf("queued pair lost its two terminal audits: %+v", saved.Audit)
	}
	for _, jobID := range []string{first, id} {
		state, result, matched := "complete", "applied", 0
		if jobID == id {
			state, result = "failed", "failed"
		}
		for _, event := range saved.Audit {
			if event.Resource != "job "+jobID {
				continue
			}
			matched++
			if event.Operation != "transfer.local."+state || event.Result != result || event.Summary != " · item outcomes: 0 failed names, 0 conflicts, 0 refusals, 0 unresolved, 0 unverified, 0 with problems" {
				t.Errorf("queued terminal audit differs: %+v", event)
			}
		}
		if matched != 1 {
			t.Errorf("job %q has %d terminal audits", jobID, matched)
		}
		var starts int
		if err := h.database.Reader().QueryRowContext(t.Context(), "SELECT COUNT(*) FROM audit_events WHERE operation='transfer.download.start' AND summary LIKE ?", "job "+jobID+" · %").Scan(&starts); err != nil || starts != 1 {
			t.Errorf("executor refusal erased job admission: starts=%d error=%v", starts, err)
		}
	}
	gets := trace.selectedGets()
	if len(gets) != 1 || gets["hold/first.bin"] == 0 || len(trace.traffic.writeSnapshot()) != 0 {
		t.Errorf("queued refusal dispatched later content or writes: gets=%v writes=%v", gets, trace.traffic.writeSnapshot())
	}
	assertListingLocalDelta(t, before, snapshotListingLocal(t, owned), nil)
	assertListingLocalDelta(t, firstBefore, snapshotListingLocal(t, firstRoot), map[string][]byte{"first.bin": holdBody})
	if data, err := os.ReadFile(filepath.Join(firstRoot, "first.bin")); err != nil || !bytes.Equal(data, holdBody) {
		t.Errorf("holding control's full bytes differ: %q error=%v", data, err)
	}
	h.AssertNoCollateral(beforeA, h.SnapshotNamespace(h.Primary))
	h.AssertNoCollateral(beforeB, h.SnapshotNamespace(h.Secondary))
	requests := trace.traffic.requestCount()
	if next, err := h.Transfer.RerunJob(t.Context(), id); next != "" || err == nil || !strings.Contains(err.Error(), "own dialog") {
		t.Errorf("refused queued history gained selection authority: %q error=%v", next, err)
	}
	uploadRecoveryReopen(t, h, id, saved)
	if trace.traffic.requestCount() != requests {
		t.Error("refused queued history reopen contacted Swift")
	}
	assertListingLocalDelta(t, before, snapshotListingLocal(t, owned), nil)
}
