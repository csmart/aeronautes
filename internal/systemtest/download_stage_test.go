package systemtest

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/csmart/aeronautes/internal/app"
	"github.com/csmart/aeronautes/internal/jobs"
	"github.com/csmart/aeronautes/internal/systemtest/oracle"
)

type downloadStageFixture struct {
	h                *Harness
	root, source     string
	body             []byte
	beforeLocal      map[string]listingLocalState
	beforeA, beforeB *oracle.AccountSnapshot
	request          app.DownloadRequestDTO
}

func newDownloadStageFixture(t *testing.T, h *Harness) *downloadStageFixture {
	t.Helper()
	f := &downloadStageFixture{h: h, root: t.TempDir(), body: []byte("selected source bytes")}
	f.source = h.Container(h.Primary, "download-stage-source")
	other := h.Container(h.Secondary, "download-stage-other")
	h.Seed(h.Primary, f.source, "scope/nested/proof.bin", f.body, nil)
	h.Seed(h.Primary, f.source, "hold/first.bin", []byte("hold bytes"), nil)
	h.Seed(h.Primary, f.source, "outside", []byte("unselected source"), nil)
	h.Seed(h.Secondary, other, "outside", []byte("other account"), nil)
	f.beforeA, f.beforeB = h.SnapshotNamespace(h.Primary), h.SnapshotNamespace(h.Secondary)
	seedListingLocal(t, f.root, "selected/keep", []byte("local sentinel"))
	seedListingLocal(t, f.root, "other-job/keep", []byte("other local job"))
	f.beforeLocal = snapshotListingLocal(t, f.root)
	return f
}

// choose picks the destination and takes the report. It is separate from
// seeding because a selection is bound to the account's current
// generation (ADR 0041), and a fixture proxy that republishes the
// bookmark must be installed before the user would have chosen.
func (f *downloadStageFixture) choose(t *testing.T) {
	t.Helper()
	h := f.h
	f.request = f.selectionFor(t, "scope/")
	preflight, err := h.Transfer.PreflightDownload(t.Context(), f.request)
	if err != nil || preflight == nil || preflight.Rejected || int(preflight.Objects) != 1 || preflight.ItemSamples[0].Route != "rclone" || preflight.ItemSamples[0].RelPath != "nested/proof.bin" {
		t.Fatalf("stage fixture engine preflight: %+v %v", preflight, err)
	}
}

// selectionFor picks the same destination for one prefix. Each prefix
// needs its own selection, because a selection authorises the one scope
// it was made for (ADR 0041).
func (f *downloadStageFixture) selectionFor(t *testing.T, prefix string) app.DownloadRequestDTO {
	return f.selectionInto(t, "selected", prefix)
}

// selectionInto picks a directory under the fixture root for one prefix.
// A second active download needs its own directory (ADR 0078).
func (f *downloadStageFixture) selectionInto(t *testing.T, dir, prefix string) app.DownloadRequestDTO {
	t.Helper()
	h := f.h
	app.WireFilePicker(h.Transfer, listingFilePicker{dir: filepath.Join(f.root, dir)})
	picked, err := h.Transfer.PickDownloadDir(t.Context(), app.SelectionScopeDTO{
		BookmarkID: h.Primary.BookmarkID, Container: f.source, Prefix: prefix,
	})
	if err != nil {
		t.Fatal(err)
	}
	return app.DownloadRequestDTO{
		BookmarkID: h.Primary.BookmarkID, Container: f.source, Prefix: prefix, DestDirToken: picked.Token,
	}
}

func (f *downloadStageFixture) check(t *testing.T, id, state, reason string, issues int, counters jobs.Counters, trace *uploadDeleteTrace, checkLocal func()) {
	t.Helper()
	h := f.h
	h.AwaitJob(id)
	copyCompletionAwaitPins(t, h, h.Primary)
	saved := uploadRecoveryRead(t, h.database, id)
	visible, err := h.Jobs.Get(t.Context(), h.Primary.BookmarkID, id)
	if err != nil || !reflect.DeepEqual(visible, saved.Job) || saved.Job.State != state || saved.Job.Counters != counters {
		t.Fatalf("stage public/durable outcome differs: %+v %v", saved.Job, err)
	}
	if reason == "" && saved.Job.Error != "" || reason != "" && !strings.Contains(saved.Job.Error, reason) {
		t.Errorf("stage lost the expected reason %q: %q", reason, saved.Job.Error)
	}
	summary, result := "", "applied"
	if state == "partial" {
		result = "verify_mismatch"
	}
	if state == "failed" {
		result = "failed"
	}
	if issues > 0 {
		// One of the three outcomes ADR 0048 requires, chosen from what is
		// actually on disk rather than from the code path that reached it.
		summary = fmt.Sprintf("download stage cleanup: %d issues", issues)
		if _, err := os.Lstat(filepath.Join(f.root, "selected", ".aeronautes-stage-"+id)); err == nil {
			summary = fmt.Sprintf("download stage retained after cleanup: %d issues", issues)
		}
		if !strings.Contains(saved.Job.Error, summary) {
			t.Errorf("stage group result missing: %+v", saved.Job)
		}
	}
	totals := jobs.ItemTotals{Items: 1, Planned: 1, Admitted: 1}
	execution := &jobs.ItemObservation{Result: jobs.ItemCopied, Effect: jobs.EffectReportedApplied, CopiedBytes: int64(len(f.body))}
	verification := &jobs.ItemObservation{Result: jobs.ItemVerified, Effect: jobs.EffectNotAttempted}
	if state == "failed" {
		totals.Unresolved = 1
		execution, verification = nil, nil
	} else if counters.ObjectsFailed == 1 {
		totals.Failed, totals.Unresolved, totals.Problems = 1, 1, 1
		execution = &jobs.ItemObservation{Result: jobs.ItemFailed, Effect: jobs.EffectUnresolved, Diagnostic: jobs.ItemDiagnostic{Stage: jobs.StageEngine, Code: jobs.CodeUnknown}}
		verification = nil
	} else if reason == "1 post-commit issues" {
		totals.Unresolved, totals.Problems = 1, 1
		execution.Effect = jobs.EffectUnresolved
		execution.Diagnostic = jobs.ItemDiagnostic{Stage: jobs.StageCleanup, Code: jobs.CodeIO}
	}
	summary += fmt.Sprintf(" · item outcomes: %d failed names, 0 conflicts, 0 refusals, %d unresolved, 0 unverified, %d with problems", totals.Failed, totals.Unresolved, totals.Problems)
	var terminal []app.AuditEventDTO
	for _, event := range saved.Audit {
		if event.Resource == "job "+id {
			terminal = append(terminal, event)
		}
	}
	if len(terminal) != 1 || terminal[0].Operation != "transfer.local."+state || terminal[0].Result != result || terminal[0].Summary != summary {
		t.Errorf("stage terminal audit differs: %+v", terminal)
	}
	if len(terminal) == 1 {
		var bookmarkID string
		if err := h.database.Reader().QueryRowContext(t.Context(), "SELECT bookmark_id FROM audit_events WHERE id=?", terminal[0].ID).Scan(&bookmarkID); err != nil || bookmarkID != h.Primary.BookmarkID {
			t.Errorf("stage audit binding differs: %q %v", bookmarkID, err)
		}
	}
	if len(trace.writeSnapshot()) != 0 || !saved.ItemsCaptured || len(saved.Items) != 1 || len(saved.Rows) != 0 {
		t.Fatalf("stage invented writes or authority: %+v", saved)
	}
	item := saved.Items[0]
	if saved.ItemTotals != totals || item.Key != "scope/nested/proof.bin" || !item.Planned || !item.Admitted || item.ListedBytes != int64(len(f.body)) ||
		item.Source != (jobs.ItemLocation{Kind: jobs.ItemSwift, BookmarkID: h.Primary.BookmarkID, Container: f.source, Object: item.Key}) ||
		item.Destination != (jobs.ItemLocation{Kind: jobs.ItemLocal, LocalPath: filepath.Join(f.root, "selected", "nested", "proof.bin")}) ||
		item.Staging == nil || *item.Staging != (jobs.ItemLocation{Kind: jobs.ItemLocal, LocalPath: filepath.Join(f.root, "selected", ".aeronautes-stage-"+id, "nested", "proof.bin")}) ||
		(item.Execution == nil) != (execution == nil) || execution != nil && item.Execution.ItemObservation != *execution ||
		(item.Verification == nil) != (verification == nil) || verification != nil && item.Verification.ItemObservation != *verification {
		t.Fatalf("stage lost exact recorded identities or observed boundaries: %+v", saved)
	}
	assertDownloadItemPages(t, h, id, map[string]jobs.ItemRecord{item.Key: item}, totals)
	checkLocal()
	h.AssertNoCollateral(f.beforeA, h.SnapshotNamespace(h.Primary))
	h.AssertNoCollateral(f.beforeB, h.SnapshotNamespace(h.Secondary))
	requests := trace.requestCount()
	if next, err := h.Transfer.RerunJob(t.Context(), id); next != "" || err == nil || !strings.Contains(err.Error(), "own dialog") {
		t.Fatalf("stage history gained rerun authority: %q %v", next, err)
	}
	uploadRecoveryReopen(t, h, id, saved)
	if trace.requestCount() != requests {
		t.Error("stage rerun/reopen contacted Swift")
	}
	checkLocal()
	h.AssertNoCollateral(f.beforeA, h.SnapshotNamespace(h.Primary))
	h.AssertNoCollateral(f.beforeB, h.SnapshotNamespace(h.Secondary))
}

func downloadStagePlacement(t *testing.T, h *Harness, fault string) {
	t.Helper()
	f := newDownloadStageFixture(t, h)
	trace := copyCompletionProxy(t, h, h.Primary, "")
	f.choose(t)
	entered, release := make(chan string, 1), make(chan error, 1)
	var calls atomic.Int32
	var once sync.Once
	resume := func(err error) { once.Do(func() { release <- err }) }
	t.Cleanup(func() { resume(errors.New("fixture cleanup released stage")) })
	app.WireDownloadStageBoundary(h.Transfer, func(ctx context.Context, stage string) error {
		calls.Add(1)
		select {
		case entered <- stage:
		case <-ctx.Done():
			return ctx.Err()
		}
		select {
		case err := <-release:
			return err
		case <-ctx.Done():
			return ctx.Err()
		}
	})
	id, err := h.Transfer.StartDownload(t.Context(), f.request)
	if err != nil {
		t.Fatal(err)
	}
	stage := filepath.Join(f.root, "selected", ".aeronautes-stage-"+id)
	select {
	case observed := <-entered:
		if observed != stage {
			t.Fatalf("boundary selected another stage: %q", observed)
		}
	case <-time.After(2 * time.Minute):
		t.Fatal("actual child did not reach the placement boundary")
	}
	staged := filepath.Join(stage, "nested", "proof.bin")
	if body, err := os.ReadFile(staged); err != nil || !bytes.Equal(body, f.body) {
		t.Fatalf("actual child did not write full staged bytes: %q %v", body, err)
	}
	target := filepath.Join(f.root, "selected", "nested", "proof.bin")
	if _, err := os.Lstat(target); !os.IsNotExist(err) {
		t.Fatalf("placement happened before acknowledgement: %v", err)
	}
	counters := jobs.Counters{ObjectsPlanned: 1, BytesPlanned: int64(len(f.body)), ObjectsCopied: 1, ObjectsVerified: 1, BytesCopied: int64(len(f.body))}
	state, reason, issues := "complete", "", 0
	files := map[string][]byte{"selected/nested/proof.bin": f.body}
	dirs := []string{"selected/nested"}
	if fault != "control" {
		state, issues = "partial", 2
		files["selected/.aeronautes-stage-"+id+"/nested/proof.bin"] = f.body
		dirs = append(dirs, "selected/.aeronautes-stage-"+id, "selected/.aeronautes-stage-"+id+"/nested")
	}
	switch fault {
	case "control":
		resume(nil)
	case "unlink":
		if os.Geteuid() == 0 {
			t.Fatal("unlink fixture requires an unprivileged owner")
		}
		dir := filepath.Dir(staged)
		t.Cleanup(func() {
			if err := os.Chmod(dir, 0o755); err != nil && !os.IsNotExist(err) {
				t.Error(err)
			}
		})
		if err := os.Chmod(dir, 0o555); err != nil {
			t.Fatal(err)
		}
		reason = "1 post-commit issues"
		// The refused unlink is now reported, and it keeps both directories
		// above it non-empty, so three refusals cascade (ADR 0048).
		issues = 3
		resume(nil)
	case "engine_refusal":
		reason = "fixture post-engine refusal"
		counters.ObjectsCopied, counters.ObjectsVerified, counters.BytesCopied, counters.ObjectsFailed = 0, 0, 0, 1
		delete(files, "selected/nested/proof.bin")
		dirs = dirs[1:]
		// The item never placed, so its staged file is still this job's own
		// scratch and goes with the stage when the job ends (ADR 0048).
		delete(files, "selected/.aeronautes-stage-"+id+"/nested/proof.bin")
		dirs = dirs[:len(dirs)-2]
		issues = 0
		resume(errors.New(reason))
	default:
		t.Fatalf("unknown stage fault %q", fault)
	}
	f.check(t, id, state, reason, issues, counters, trace, func() {
		assertListingLocalDelta(t, f.beforeLocal, snapshotListingLocal(t, f.root), files, dirs...)
		if calls.Load() != 1 {
			t.Errorf("placement boundary calls: %d", calls.Load())
		}
		if fault == "unlink" {
			stagedInfo, err := os.Stat(staged)
			if err != nil {
				t.Fatal(err)
			}
			targetInfo, err := os.Stat(target)
			if err != nil || !os.SameFile(stagedInfo, targetInfo) {
				t.Fatalf("successful placement link not witnessed: %v", err)
			}
			if err := os.Remove(staged); !errors.Is(err, os.ErrPermission) {
				t.Fatalf("actual staged unlink was not denied: %v", err)
			}
		}
	})
}

func downloadStageForeign(t *testing.T, h *Harness, kind string) {
	t.Helper()
	f := newDownloadStageFixture(t, h)
	barrier, ended, hits, trace := interruptionProxy(t, h, f.source, http.MethodGet, "scope/nested/proof.bin", nil)
	f.choose(t)
	id, err := h.Transfer.StartDownload(t.Context(), f.request)
	if err != nil {
		t.Fatal(err)
	}
	if event, err := barrier.Wait(t.Context()); err != nil || event.StatusCode != http.StatusOK {
		t.Fatalf("stage GET barrier: %+v %v", event, err)
	}
	stageRel := "selected/.aeronautes-stage-" + id
	stage := filepath.Join(f.root, filepath.FromSlash(stageRel))
	if info, err := os.Stat(stage); err != nil || !info.IsDir() {
		t.Fatalf("actual child stage absent: %v", err)
	}
	foreign := filepath.Join(stage, "other-writer")
	switch kind {
	case "file":
		if err := os.WriteFile(foreign, []byte("unrelated writer bytes"), 0o600); err != nil {
			t.Fatal(err)
		}
	case "directory":
		seedListingLocal(t, foreign, "keep", []byte("unrelated directory bytes"))
	case "empty_directory":
		if err := os.Mkdir(foreign, 0o700); err != nil {
			t.Fatal(err)
		}
	case "symlink":
		if err := os.Symlink(filepath.Join(f.root, "other-job"), foreign); err != nil {
			t.Fatal(err)
		}
	default:
		t.Fatalf("unknown foreign entry %q", kind)
	}
	// The acknowledged entry alone is permitted residue; do not accept
	// arbitrary files produced while the selected GET is in flight.
	for name, entry := range snapshotListingLocal(t, foreign) {
		key := stageRel + "/other-writer"
		if name != "." {
			key += "/" + name
		}
		f.beforeLocal[key] = entry
	}
	if err := barrier.Release(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-ended:
	case <-time.After(10 * time.Second):
		t.Fatal("released stage response did not end")
	}
	if hits.Load() != 1 || barrier.Err() != nil {
		t.Fatalf("stage GET retried or failed: %d %v", hits.Load(), barrier.Err())
	}
	counters := jobs.Counters{ObjectsPlanned: 1, BytesPlanned: int64(len(f.body)), ObjectsCopied: 1, ObjectsVerified: 1, BytesCopied: int64(len(f.body))}
	f.check(t, id, "partial", "issues", 1, counters, trace, func() {
		assertListingLocalDelta(t, f.beforeLocal, snapshotListingLocal(t, f.root), map[string][]byte{"selected/nested/proof.bin": f.body}, "selected/nested", stageRel)
	})
}

func downloadStageExisting(t *testing.T, h *Harness, kind string) {
	t.Helper()
	f := newDownloadStageFixture(t, h)
	settings, err := h.Settings.Get(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	settings.MaxActiveJobs = 1
	if _, err := h.Settings.Set(t.Context(), settings); err != nil {
		t.Fatal(err)
	}
	barrier, ended, hits, trace := interruptionProxy(t, h, f.source, http.MethodGet, "hold/first.bin", nil)
	f.choose(t)
	var calls atomic.Int32
	app.WireDownloadStageBoundary(h.Transfer, func(context.Context, string) error { calls.Add(1); return nil })
	// The holding job has its own directory: the selected one is the
	// queued job's, and one directory serves one active download (ADR 0078).
	seedListingLocal(t, f.root, "held/keep", []byte("held local sentinel"))
	f.beforeLocal = snapshotListingLocal(t, f.root)
	firstRequest := f.selectionInto(t, "held", "hold/")
	first, err := h.Transfer.StartDownload(t.Context(), firstRequest)
	if err != nil {
		t.Fatal(err)
	}
	if event, err := barrier.Wait(t.Context()); err != nil || event.StatusCode != http.StatusOK {
		t.Fatalf("holding download barrier: %+v %v", event, err)
	}
	id, err := h.Transfer.StartDownload(t.Context(), f.request)
	if err != nil {
		t.Fatal(err)
	}
	queued, err := h.Jobs.Get(t.Context(), h.Primary.BookmarkID, id)
	if err != nil || queued.State != "queued" || queued.StartedAt != "" {
		t.Fatalf("stage job was not queued: %+v %v", queued, err)
	}
	stageRel := "selected/.aeronautes-stage-" + id
	stage := filepath.Join(f.root, filepath.FromSlash(stageRel))
	switch kind {
	case "file":
		if err := os.WriteFile(stage, []byte("existing stage file"), 0o600); err != nil {
			t.Fatal(err)
		}
	case "directory":
		seedListingLocal(t, stage, "nested/keep", []byte("existing stage directory"))
	case "symlink":
		if err := os.Symlink(filepath.Join(f.root, "other-job"), stage); err != nil {
			t.Fatal(err)
		}
	default:
		t.Fatalf("unknown existing entry %q", kind)
	}
	for name, entry := range snapshotListingLocal(t, stage) {
		key := stageRel
		if name != "." {
			key += "/" + name
		}
		f.beforeLocal[key] = entry
	}
	if err := barrier.Release(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-ended:
	case <-time.After(10 * time.Second):
		t.Fatal("holding response did not end")
	}
	if hits.Load() != 1 || barrier.Err() != nil {
		t.Fatalf("holding response retried or failed: %d %v", hits.Load(), barrier.Err())
	}
	h.AwaitJob(first)
	h.AwaitJob(id)
	copyCompletionAwaitPins(t, h, h.Primary)
	firstJob, err := h.Jobs.Get(t.Context(), h.Primary.BookmarkID, first)
	firstCounters := jobs.Counters{ObjectsPlanned: 1, BytesPlanned: 10, ObjectsCopied: 1, ObjectsVerified: 1, BytesCopied: 10}
	if err != nil || firstJob.State != "complete" || firstJob.Counters != firstCounters || firstJob.Error != "" {
		t.Fatalf("holding download changed: %+v %v", firstJob, err)
	}
	counters := jobs.Counters{ObjectsPlanned: 1, BytesPlanned: int64(len(f.body))}
	f.check(t, id, "failed", "file exists", 0, counters, trace, func() {
		assertListingLocalDelta(t, f.beforeLocal, snapshotListingLocal(t, f.root), map[string][]byte{"held/first.bin": []byte("hold bytes")})
		if calls.Load() != 1 {
			t.Errorf("existing stage dispatched another child: %d completed calls", calls.Load())
		}
	})
}

func downloadStageCases(t *testing.T, makeHarness func(*testing.T) *Harness) {
	for _, fault := range []string{"control", "unlink", "engine_refusal"} {
		t.Run(fault, func(t *testing.T) { downloadStagePlacement(t, makeHarness(t), fault) })
	}
	for _, kind := range []string{"file", "directory", "symlink"} {
		t.Run("foreign_"+kind, func(t *testing.T) { downloadStageForeign(t, makeHarness(t), kind) })
		t.Run("existing_"+kind, func(t *testing.T) { downloadStageExisting(t, makeHarness(t), kind) })
	}
	t.Run("foreign_empty_directory", func(t *testing.T) { downloadStageForeign(t, makeHarness(t), "empty_directory") })
}

func TestDownloadStageMock(t *testing.T) {
	t.Parallel()
	downloadStageCases(t, findMockHarness)
}
func TestDownloadStageRealSwift(t *testing.T) {
	t.Parallel()
	downloadStageCases(t, New)
}
