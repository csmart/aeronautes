package systemtest

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/csmart/aeronautes/internal/app"
	"github.com/csmart/aeronautes/internal/jobs"
)

func assertDownloadRecords(t *testing.T, h *Harness, id, source, prefix, root string, problems map[string]bool, originals map[string][]byte, native bool, saved uploadRecoveryState) {
	t.Helper()
	count := int64(len(problems))
	want := jobs.ItemTotals{Items: int64(len(originals)), Planned: int64(len(originals)), Admitted: int64(len(originals)), Problems: count}
	if native {
		want.Failed = count
	} else {
		want.Conflicts = count
	}
	if !saved.ItemsCaptured || saved.ItemTotals != want || len(saved.Items) != len(originals) || len(saved.Rows) != 0 {
		t.Fatalf("download lost exact identities or invented recovery authority: %+v want=%+v", saved, want)
	}
	byKey := map[string]jobs.ItemRecord{}
	for _, item := range saved.Items {
		name, selected := strings.CutPrefix(item.Key, prefix)
		body, exists := originals[name]
		if !selected || !exists || byKey[item.Key].ID != 0 || item.JobID != id || !item.Planned || !item.Admitted || item.ListedBytes != int64(len(body)) ||
			item.Source != (jobs.ItemLocation{Kind: jobs.ItemSwift, BookmarkID: h.Primary.BookmarkID, Container: source, Object: prefix + name}) ||
			item.Destination != (jobs.ItemLocation{Kind: jobs.ItemLocal, LocalPath: filepath.Join(root, name)}) ||
			item.ID <= 0 || item.CreatedAt.IsZero() || item.Execution == nil {
			t.Fatalf("download identity differs from source selection and captured local root: %+v", item)
		}
		if problems[name] {
			// The engine left the operator's file alone; the native route's
			// read was refused by the store.
			expected := jobs.ItemObservation{Result: jobs.ItemConflict, Effect: jobs.EffectNotAttempted, Diagnostic: jobs.ItemDiagnostic{Stage: jobs.StageCommit, Code: jobs.CodeConflict}}
			if native {
				expected = jobs.ItemObservation{Result: jobs.ItemFailed, Effect: jobs.EffectNotAttempted, Diagnostic: jobs.ItemDiagnostic{Stage: jobs.StageSourceRead, Code: jobs.CodeForbidden, HTTPStatus: 403}}
			}
			if item.Execution.ItemObservation != expected || item.Verification != nil {
				t.Fatalf("download refusal lost actual effects or invented verification: %+v expected=%+v", item, expected)
			}
		} else if item.Execution.ItemObservation != (jobs.ItemObservation{Result: jobs.ItemCopied, Effect: jobs.EffectReportedApplied, CopiedBytes: int64(len(body))}) ||
			item.Verification == nil || item.Verification.ItemObservation != (jobs.ItemObservation{Result: jobs.ItemVerified, Effect: jobs.EffectNotAttempted}) {
			t.Fatalf("download lost copied and verified content: %+v", item)
		}
		byKey[item.Key] = item
	}
	assertDownloadItemPages(t, h, id, byKey, want)
	summary := fmt.Sprintf(" · item outcomes: %d failed names, %d conflicts, 0 refusals, %d unresolved, 0 unverified, %d with problems", want.Failed, want.Conflicts, want.Unresolved, want.Problems)
	if len(saved.Audit) != 1 || saved.Audit[0].Summary != summary {
		t.Fatalf("download terminal audit is not item-derived: %+v want=%q", saved.Audit, summary)
	}
	if strings.Contains(saved.Job.Error, "200 failed") || count > 0 && !strings.Contains(saved.Job.Error, "transfer failures (first:") {
		t.Fatalf("download error presents a bounded sample as exact: %q", saved.Job.Error)
	}
	if repeat := uploadRecoveryRead(t, h.database, id); !reflect.DeepEqual(repeat, saved) {
		t.Fatal("reading download history changed retained facts")
	}
}

func assertDownloadItemPages(t *testing.T, h *Harness, id string, stored map[string]jobs.ItemRecord, want jobs.ItemTotals) {
	t.Helper()
	seen := map[string]bool{}
	lastID := int64(0)
	for cursor := ""; ; {
		page, err := h.Jobs.Items(t.Context(), h.Primary.BookmarkID, id, cursor, 100)
		if err != nil || page.Status != "available" || len(page.Items) > 100 {
			t.Fatalf("public download items unavailable: %+v %v", page, err)
		}
		for _, row := range page.Items {
			item, exists := stored[row.Key]
			rowID, err := strconv.ParseInt(row.ID, 10, 64)
			if err != nil || rowID <= lastID || rowID != item.ID || !exists || seen[row.Key] || row.KeyDisplay != strconv.Quote(item.Key) ||
				row.Source != (app.JobItemLocationDTO{Kind: "swift", BookmarkID: item.Source.BookmarkID, Container: item.Source.Container, Object: item.Source.Object,
					Display: strconv.Quote(item.Source.BookmarkID) + " / " + strconv.Quote(item.Source.Container) + " / " + strconv.Quote(item.Source.Object)}) ||
				row.Destination != (app.JobItemLocationDTO{Kind: "local", LocalPath: item.Destination.LocalPath, Display: strconv.Quote(item.Destination.LocalPath)}) ||
				row.Planned != item.Planned || row.Admitted != item.Admitted || row.ListedBytes != item.ListedBytes {
				t.Fatalf("public download identity differs: %+v", row)
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
			t.Fatalf("download page cursor skipped or repeated: %q", page.NextCursor)
		}
		cursor = page.NextCursor
	}
	if len(seen) != len(stored) {
		t.Fatalf("public download pages lost identities: got=%d want=%d", len(seen), len(stored))
	}
	public, err := h.Jobs.ItemTotals(t.Context(), h.Primary.BookmarkID, id)
	expected := app.JobItemTotalsDTO{Status: "available", Items: want.Items, Planned: want.Planned, Admitted: want.Admitted,
		Failed: want.Failed, Incomplete: want.Incomplete, Conflicts: want.Conflicts, Refused: want.Refused, Unresolved: want.Unresolved, Unverified: want.Unverified, Problems: want.Problems}
	if err != nil || public != expected {
		t.Fatalf("public download facets differ: %+v %v want=%+v", public, err, expected)
	}
	encoded, err := json.Marshal(stored)
	if err != nil {
		t.Fatal(err)
	}
	assertCopyItemDiagnosticText(t, encoded)
}
