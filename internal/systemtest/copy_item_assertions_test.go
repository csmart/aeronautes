package systemtest

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/csmart/aeronautes/internal/jobs"
)

func assertCopyItemRecords(t *testing.T, h *Harness, id, src, dst string, failed, bytes int, selected map[string]bool, saved copyCompletionState) {
	t.Helper()
	want := jobs.ItemTotals{Items: int64(failed + 1), Planned: int64(failed + 1), Admitted: int64(failed + 1), Failed: int64(failed), Problems: int64(failed)}
	if !saved.ItemsCaptured || saved.ItemTotals != want || len(saved.Items) != failed+1 {
		t.Fatalf("actual Copy did not retain each failed identity once: captured=%v totals=%+v rows=%d want=%+v", saved.ItemsCaptured, saved.ItemTotals, len(saved.Items), want)
	}
	byKey := map[string]jobs.ItemRecord{}
	for _, item := range saved.Items {
		if _, exists := byKey[item.Key]; exists {
			t.Fatalf("repeated durable identity %q", item.Key)
		}
		byKey[item.Key] = item
		if item.JobID != id || !item.Admitted || !item.Planned || item.ListedBytes != int64(bytes) ||
			item.Source != (jobs.ItemLocation{Kind: jobs.ItemSwift, BookmarkID: h.Primary.BookmarkID, Container: src, Object: "scope/" + item.Key}) ||
			item.Destination != (jobs.ItemLocation{Kind: jobs.ItemSwift, BookmarkID: h.Secondary.BookmarkID, Container: dst, Object: "scope/" + item.Key}) ||
			item.Staging != nil || item.Execution == nil {
			t.Fatalf("actual Copy lost captured identity or observations, or recorded a stage name: %+v", item)
		}
		// A refused read of the source wrote nothing, and that answer is the
		// whole record: only an object that was written, or was already
		// there, gets a second one (ADR 0122 decision 7).
		if selected[item.Key] {
			if item.Execution.ItemObservation != (jobs.ItemObservation{Result: jobs.ItemFailed, Effect: jobs.EffectNotAttempted,
				Diagnostic: jobs.ItemDiagnostic{Stage: jobs.StageSourceRead, Code: jobs.CodeForbidden, HTTPStatus: 403}}) ||
				item.Verification != nil {
				t.Fatalf("a refused source read was misrecorded or acquired a second record: %+v", item)
			}
		} else if item.Key != "z-engine.bin" ||
			item.Execution.ItemObservation != (jobs.ItemObservation{Result: jobs.ItemCopied, Effect: jobs.EffectReportedApplied, CopiedBytes: int64(bytes)}) ||
			item.Verification == nil || item.Verification.ItemObservation != (jobs.ItemObservation{Result: jobs.ItemVerified, Effect: jobs.EffectNotAttempted}) {
			t.Fatalf("successful engine control was misrecorded: %+v", item)
		}
	}
	for key := range selected {
		if _, exists := byKey[key]; !exists {
			t.Fatalf("failed identity inaccessible: %q", key)
		}
	}
	seen := map[string]bool{}
	for cursor := ""; ; {
		page, err := h.Jobs.Items(t.Context(), h.Primary.BookmarkID, id, cursor, 100)
		if err != nil || page.Status != "available" || len(page.Items) > 100 {
			t.Fatalf("actual Copy public page unavailable: %+v %v", page, err)
		}
		for _, row := range page.Items {
			stored, exists := byKey[row.Key]
			if !exists || seen[row.Key] || row.Source.Object != stored.Source.Object || row.Destination.Object != stored.Destination.Object ||
				row.Execution == nil || row.Execution.Result != string(stored.Execution.Result) ||
				(row.Verification == nil) != (stored.Verification == nil) || (row.Verification != nil && row.Verification.Result != string(stored.Verification.Result)) ||
				row.Execution.Code != string(stored.Execution.Diagnostic.Code) || row.Execution.HTTPStatus != stored.Execution.Diagnostic.HTTPStatus {
				t.Fatalf("actual Copy public identity or observation differs: %+v", row)
			}
			seen[row.Key] = true
		}
		encoded, err := json.Marshal(page)
		if err != nil {
			t.Fatal(err)
		}
		assertCopyItemDiagnosticText(t, encoded)
		if page.NextCursor == "" {
			break
		}
		if page.NextCursor == cursor {
			t.Fatal("public Copy item page repeated")
		}
		cursor = page.NextCursor
	}
	if len(seen) != len(byKey) {
		t.Fatalf("public Copy pages lost identities: got=%d want=%d", len(seen), len(byKey))
	}
	encoded, err := json.Marshal(saved.Items)
	if err != nil {
		t.Fatal(err)
	}
	assertCopyItemDiagnosticText(t, encoded)
	public, err := h.Jobs.ItemTotals(t.Context(), h.Primary.BookmarkID, id)
	if err != nil || public.Status != "available" || public.Failed != want.Failed || public.Unresolved != 0 || public.Unverified != 0 {
		t.Fatalf("public Copy totals differ: %+v %v", public, err)
	}
	if repeat := copyCompletionRead(t, h.database, id); !reflect.DeepEqual(repeat, saved) {
		t.Fatal("reading Copy pages changed retained history")
	}
}

func assertCopyItemDiagnosticText(t *testing.T, encoded []byte) {
	t.Helper()
	for _, text := range []string{"fixture selected source read refusal", "script", "copy-private", "fixture.invalid", "password", "fixture-private-header"} {
		if strings.Contains(string(encoded), text) {
			t.Fatalf("provider body or header entered persisted items or DTO: %s", encoded)
		}
	}
}
