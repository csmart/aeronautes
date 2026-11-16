package systemtest

import (
	"encoding/json"
	"fmt"
	"net/url"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/csmart/aeronautes/internal/transfer/rclone"

	"github.com/csmart/aeronautes/internal/app"
	"github.com/csmart/aeronautes/internal/jobs"
)

func assertUploadItemWrites(t *testing.T, trace *copyItemRecordsTrace, storageURL, container string, native bool, paths []string) {
	t.Helper()
	storage, err := url.Parse(storageURL)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]int{}
	for _, write := range trace.traffic.writeSnapshot() {
		got[write]++
	}
	if problems := uploadWriteProblems(got, storage.Path+"/"+container+"/", native, paths); len(problems) > 0 {
		t.Fatalf("upload mutations differ:\n  %s", strings.Join(problems, "\n  "))
	}
}

// maxEngineWritesPerIdentity is how many times ONE name can be written by
// the engine across a whole upload job, which is the two retry layers of
// its lifecycle multiplied, not either of them alone.
//
// The engine's own budget bounds total attempts at one request, so it is
// used as it stands rather than with one added; the measurement is in
// rclone.DefaultLowLevelRetries' own comment. On top of that,
// uploadRun.copy in internal/transfer/local/upload.go runs the engine a
// second time when the first run fails an authentication-shaped failure,
// once and only once, so one name can be written by two separate child runs.
func maxEngineWritesPerIdentity() int {
	const maxChildRuns = 2 // uploadRun.copy's single authentication restart
	return rclone.DefaultLowLevelRetries * maxChildRuns
}

// uploadWriteProblems is the pure core of assertUploadItemWrites, testable
// without a fixture: it returns every way the observed mutations depart
// from what an upload of these paths is allowed to make (ADR 0122 decision
// 3). An upload writes each file at its own name and nothing else, so a
// stage name, a delete or any other name is a problem.
//
// This application's own PUT, of a name the engine cannot carry, is exactly
// once, because it issues it. The ENGINE's writes are not, and asserting
// that they were claimed something the engine's own flags contradict.
// pacingArgs hands it a low-level retry budget, and a low-level retry
// re-sends one request, so a transient refusal of one upload out of
// hundreds is re-sent by design. PR 182's first attempt failed on exactly
// that, one PUT of 402 issued twice. The names stay exact in both
// directions and the engine's repeats stay bounded by the budget it was
// given, so nothing here is unchecked.
func uploadWriteProblems(got map[string]int, prefix string, native bool, paths []string) []string {
	maxEngineAttempts := maxEngineWritesPerIdentity()
	ours := map[string]int{}
	engine := map[string]bool{}
	for _, path := range paths {
		name := filepath.Base(path)
		write := "PUT " + prefix + "scope/" + name
		if native && name != "z-engine.bin" {
			ours[write] = 1
		} else {
			engine[write] = true
		}
	}

	var problems []string
	mine := map[string]int{}
	for write, attempts := range got {
		if engine[write] {
			continue
		}
		mine[write] = attempts
	}
	if !reflect.DeepEqual(mine, ours) {
		problems = append(problems, fmt.Sprintf("writes other than the engine's own differ: got=%v want=%v", mine, ours))
	}
	for write := range engine {
		attempts := got[write]
		if attempts < 1 {
			problems = append(problems, "the engine never wrote a name it was given: "+write)
			continue
		}
		if attempts > maxEngineAttempts {
			problems = append(problems, fmt.Sprintf(
				"a name was written %d times, outside the engine's budget of 1 to %d: %s",
				attempts, maxEngineAttempts, write))
		}
	}
	return problems
}

func assertUploadItemRecords(t *testing.T, h *Harness, id, dst string, failed, bytes int, native bool, paths []string, saved uploadRecoveryState) {
	t.Helper()
	// The store's refusal of this application's own PUT wrote nothing. The
	// engine's error for a file does not say whether anything was written,
	// so that outcome is unresolved (ADR 0122 decision 7).
	refusal := jobs.ItemObservation{Result: jobs.ItemFailed, Effect: jobs.EffectUnresolved,
		Diagnostic: jobs.ItemDiagnostic{Stage: jobs.StageEngine, Code: jobs.CodeIO}}
	unresolved := failed
	if native {
		refusal = jobs.ItemObservation{Result: jobs.ItemFailed, Effect: jobs.EffectNotAttempted,
			Diagnostic: jobs.ItemDiagnostic{Stage: jobs.StageCommit, Code: jobs.CodeForbidden, HTTPStatus: 403}}
		unresolved = 0
	}
	want := jobs.ItemTotals{Items: int64(failed + 1), Planned: int64(failed + 1), Admitted: int64(failed + 1),
		Failed: int64(failed), Unresolved: int64(unresolved), Problems: int64(failed)}
	if !saved.ItemsCaptured || saved.ItemTotals != want || len(saved.Items) != len(paths) {
		t.Fatalf("upload lost distinct identities: captured=%t totals=%+v rows=%d want=%+v", saved.ItemsCaptured, saved.ItemTotals, len(saved.Items), want)
	}
	byKey := map[string]jobs.ItemRecord{}
	for i, path := range paths {
		item := saved.Items[i]
		name := filepath.Base(path)
		if item.Key != "scope/"+name || byKey[item.Key].ID != 0 || item.JobID != id || !item.Planned || !item.Admitted || item.ListedBytes != int64(bytes) ||
			item.Source != (jobs.ItemLocation{Kind: jobs.ItemLocal, LocalPath: path}) ||
			item.Destination != (jobs.ItemLocation{Kind: jobs.ItemSwift, BookmarkID: h.Primary.BookmarkID, Container: dst, Object: "scope/" + name}) ||
			item.ID <= 0 || item.CreatedAt.IsZero() || item.Execution == nil {
			t.Fatalf("upload identity differs from backend selection: %+v path=%q", item, path)
		}
		byKey[item.Key] = item
		if item.Staging != nil {
			t.Fatalf("an upload recorded a stage name: %+v", item)
		}
		if i < failed {
			if item.Execution.ItemObservation != refusal || item.Verification != nil {
				t.Fatalf("a refused PUT was misrecorded or invented a second record: %+v want=%+v", item, refusal)
			}
		} else if item.Execution.ItemObservation != (jobs.ItemObservation{Result: jobs.ItemCopied, Effect: jobs.EffectReportedApplied, CopiedBytes: int64(bytes)}) ||
			item.Verification == nil || item.Verification.ItemObservation != (jobs.ItemObservation{Result: jobs.ItemVerified, Effect: jobs.EffectNotAttempted}) {
			t.Fatalf("successful upload lost copied or verified bytes: %+v", item)
		}
	}
	assertUploadItemPages(t, h, id, byKey, want)
	summary := fmt.Sprintf(" · item outcomes: %d failed names, 0 conflicts, 0 refusals, %d unresolved, 0 unverified, %d with problems", failed, unresolved, failed)
	if len(saved.Audit) != 1 || saved.Audit[0].Summary != summary {
		t.Fatalf("upload terminal audit is not item-derived: %+v want=%q", saved.Audit, summary)
	}
	if strings.Contains(saved.Job.Error, "200 failed") || failed > 0 && !strings.Contains(saved.Job.Error, "transfer failures (first:") {
		t.Fatalf("upload error presents sample length as exact: %q", saved.Job.Error)
	}
	if repeat := uploadRecoveryRead(t, h.database, id); !reflect.DeepEqual(repeat, saved) {
		t.Fatal("reading upload history changed retained facts")
	}
}

func assertUploadItemPages(t *testing.T, h *Harness, id string, stored map[string]jobs.ItemRecord, want jobs.ItemTotals) {
	t.Helper()
	seen := map[string]bool{}
	lastID := int64(0)
	for cursor := ""; ; {
		page, err := h.Jobs.Items(t.Context(), h.Primary.BookmarkID, id, cursor, 100)
		if err != nil || page.Status != "available" || len(page.Items) > 100 {
			t.Fatalf("public upload items unavailable: %+v %v", page, err)
		}
		for _, row := range page.Items {
			item, exists := stored[row.Key]
			rowID, err := strconv.ParseInt(row.ID, 10, 64)
			if err != nil || rowID <= lastID || rowID != item.ID || !exists || seen[row.Key] || row.KeyDisplay != strconv.Quote(item.Key) ||
				row.Source.LocalPath != item.Source.LocalPath || row.Source.Display != strconv.Quote(item.Source.LocalPath) || row.Source.Kind != "local" ||
				row.Destination.Kind != "swift" || row.Destination.BookmarkID != item.Destination.BookmarkID || row.Destination.Container != item.Destination.Container || row.Destination.Object != item.Destination.Object ||
				row.Planned != item.Planned || row.Admitted != item.Admitted || row.ListedBytes != item.ListedBytes {
				t.Fatalf("public upload identity differs: %+v", row)
			}
			if (row.Staging == nil) != (item.Staging == nil) || row.Staging != nil && (row.Staging.Object != item.Staging.Object || row.Staging.BookmarkID != item.Staging.BookmarkID || row.Staging.Container != item.Staging.Container) {
				t.Fatalf("public upload stage differs: %+v", row)
			}
			assertUploadPublicObservation(t, row.Execution, item.Execution)
			assertUploadPublicObservation(t, row.Verification, item.Verification)
			seen[row.Key], lastID = true, rowID
		}
		encoded, err := json.Marshal(page)
		if err != nil {
			t.Fatal(err)
		}
		assertCopyItemDiagnosticText(t, encoded)
		if page.NextCursor == "" {
			break
		}
		if page.NextCursor == cursor || page.NextCursor != strconv.FormatInt(lastID, 10) {
			t.Fatalf("upload page cursor skipped or repeated: %q", page.NextCursor)
		}
		cursor = page.NextCursor
	}
	if len(seen) != len(stored) {
		t.Fatalf("public upload pages lost identities: got=%d want=%d", len(seen), len(stored))
	}
	public, err := h.Jobs.ItemTotals(t.Context(), h.Primary.BookmarkID, id)
	wantPublic := app.JobItemTotalsDTO{Status: "available", Items: want.Items, Planned: want.Planned, Admitted: want.Admitted,
		Failed: want.Failed, Conflicts: want.Conflicts, Refused: want.Refused, Unresolved: want.Unresolved, Unverified: want.Unverified, Problems: want.Problems}
	if err != nil || public != wantPublic {
		t.Fatalf("public upload facets differ: %+v %v want=%+v", public, err, wantPublic)
	}
	encoded, err := json.Marshal(stored)
	if err != nil {
		t.Fatal(err)
	}
	assertCopyItemDiagnosticText(t, encoded)
}

func assertUploadPublicObservation(t *testing.T, got *app.JobItemObservationDTO, want *jobs.RecordedObservation) {
	t.Helper()
	if (got == nil) != (want == nil) {
		t.Fatalf("public upload invented or lost an observation: %+v want=%+v", got, want)
	}
	if want == nil {
		return
	}
	if got.Result != string(want.Result) || got.Effect != string(want.Effect) || got.Stage != string(want.Diagnostic.Stage) ||
		got.Code != string(want.Diagnostic.Code) || got.HTTPStatus != want.Diagnostic.HTTPStatus || got.CopiedBytes != want.CopiedBytes || got.CreatedAt == "" {
		t.Fatalf("public upload observation differs: %+v want=%+v", got, want)
	}
}
