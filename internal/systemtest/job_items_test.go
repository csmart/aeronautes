package systemtest

import (
	"database/sql"
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

func retainedIntent(key string) jobs.ItemIntent {
	return jobs.ItemIntent{ItemIdentity: jobs.ItemIdentity{Key: key,
		Source:      jobs.ItemLocation{Kind: jobs.ItemSwift, BookmarkID: "source-binding", Container: "source", Object: "scope/" + key},
		Destination: jobs.ItemLocation{Kind: jobs.ItemSwift, BookmarkID: "dest-binding", Container: "dest", Object: "target/" + key},
	}, Planned: true, Admitted: true, ListedBytes: 10}
}

// These fixtures exercise the actual read service; transfer callers have not opted in yet.
func TestJobItemAccessBeyondRecentJobsAndLargeCursors(t *testing.T) {
	h, store, database := recoveryStatusHarness(t)
	ctx := t.Context()
	job, err := store.Create(ctx, jobs.Job{Type: jobs.TypeCopy, ItemRecordsVersion: 1, SourceBookmarkID: h.Primary.BookmarkID})
	if err != nil {
		t.Fatal(err)
	}
	names := []string{"", "line\nbreak", "line\\nbreak", "\x00", "\u202ename", "\u200b", "<script>alert(1)</script>", "é", "e\u0301", "\ufffd", " / ", "\t\r"}
	for len(names) < 402 {
		names = append(names, fmt.Sprintf("item-%04d", len(names)))
	}
	intents := make([]jobs.ItemIntent, len(names))
	for i, key := range names {
		intents[i] = retainedIntent(key)
		if i%2 == 0 {
			intents[i].Staging = &jobs.ItemLocation{Kind: jobs.ItemSwift, BookmarkID: "dest-binding", Container: "dest", Object: ".staging/job/batch/" + key}
		}
	}
	if err := store.AdmitItems(ctx, job.ID, intents); err != nil {
		t.Fatal(err)
	}
	const offset int64 = 9007199254740992
	if err := database.WriteTx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `UPDATE jobs SET created_at='2000-01-01T00:00:00Z' WHERE id=?`, job.ID); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `UPDATE job_items SET id=id+? WHERE job_id=?`, offset, job.ID)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	for i, intent := range intents {
		execution := jobs.ItemObservation{Result: jobs.ItemFailed, Effect: jobs.EffectNotAttempted,
			Diagnostic: jobs.ItemDiagnostic{Stage: jobs.StageSourceRead, Code: jobs.CodeForbidden, HTTPStatus: 403}}
		verification := jobs.ItemObservation{Result: jobs.ItemFailed, Effect: jobs.EffectNotAttempted,
			Diagnostic: jobs.ItemDiagnostic{Stage: jobs.StageVerification, Code: jobs.CodeMissing, HTTPStatus: 404}}
		if i == len(intents)-1 {
			execution = jobs.ItemObservation{Result: jobs.ItemCopied, Effect: jobs.EffectReportedApplied, CopiedBytes: 10}
			verification = jobs.ItemObservation{Result: jobs.ItemVerified, Effect: jobs.EffectNotAttempted}
		}
		if err := store.RecordExecution(ctx, job.ID, intent.Key, execution); err != nil {
			t.Fatal(err)
		}
		if err := store.RecordComparison(ctx, job.ID, intent.ItemIdentity, verification); err != nil {
			t.Fatal(err)
		}
	}
	for range 65 {
		if _, err := store.Create(ctx, jobs.Job{Type: jobs.TypeDownload, SourceBookmarkID: h.Primary.BookmarkID}); err != nil {
			t.Fatal(err)
		}
	}
	recent, err := h.Jobs.List(ctx, "", false, 50)
	if err != nil || len(recent) != 50 {
		t.Fatalf("recent fixture = %d, %v", len(recent), err)
	}
	for _, row := range recent {
		if row.ID == job.ID {
			t.Fatal("retained job must be outside the old fifty-job window")
		}
	}
	first, history := h.JobHistory(ctx, "", "", 0)
	if history.Err() != nil || len(first.Jobs) != 50 || first.NextCursor == "" {
		t.Fatalf("first history page = %+v, %v", first, history.Err())
	}
	older, next := h.JobHistory(ctx, "", first.NextCursor, 0)
	if next.Err() != nil || len(older.Jobs) != 16 || older.NextCursor != "" || older.Jobs[15].ID != job.ID {
		t.Fatalf("older retained job was undiscoverable: %+v, %v", older, next.Err())
	}
	for _, limit := range []int{0, 37, 200} {
		cursor, count := "", 0
		for {
			page, read := h.JobItems(ctx, older.Jobs[15].ID, cursor, limit)
			bound := limit
			if bound == 0 {
				bound = 100
			}
			if read.Err() != nil || page.Status != "available" || len(page.Items) > bound || len(page.Items) == 0 {
				t.Fatalf("item page = %+v, %v", page, read.Err())
			}
			for _, item := range page.Items {
				if count >= len(intents) {
					t.Fatal("item pagination repeated rows")
				}
				intent := intents[count]
				if item.ID != strconv.FormatInt(offset+int64(count)+1, 10) || item.Key != intent.Key || item.KeyDisplay != strconv.Quote(intent.Key) ||
					item.Source.Object != intent.Source.Object || item.Destination.Object != intent.Destination.Object ||
					item.Source.BookmarkID != intent.Source.BookmarkID || item.Destination.BookmarkID != intent.Destination.BookmarkID ||
					!item.Planned || !item.Admitted || item.ListedBytes != 10 || item.CreatedAt == "" {
					t.Fatalf("identity changed at %d: %+v", count, item)
				}
				if item.Source.Display != `"source-binding" / "source" / `+strconv.Quote(intent.Source.Object) ||
					item.Destination.Display != `"dest-binding" / "dest" / `+strconv.Quote(intent.Destination.Object) {
					t.Fatalf("location boundaries hidden: %+v", item)
				}
				if (item.Staging == nil) != (intent.Staging == nil) || (item.Staging != nil && item.Staging.Object != intent.Staging.Object) {
					t.Fatalf("staging identity changed: %+v", item)
				}
				if item.Execution == nil || item.Verification == nil || item.Execution.CreatedAt == "" || item.Verification.CreatedAt == "" {
					t.Fatalf("missing observation: %+v", item)
				}
				if count < 401 {
					if item.Execution.Result != "failed" || item.Execution.Stage != "source_read" || item.Execution.Code != "forbidden" || item.Execution.HTTPStatus != 403 ||
						item.Verification.Result != "failed" || item.Verification.Stage != "verification" || item.Verification.Code != "missing" || item.Verification.HTTPStatus != 404 {
						t.Fatalf("failed phases lost or conflated: %+v", item)
					}
				} else if item.Execution.Result != "copied" || item.Execution.CopiedBytes != 10 || item.Execution.Effect != "reported_applied" || item.Verification.Result != "verified" {
					t.Fatalf("copy facts changed: %+v", item)
				}
				encoded, err := json.Marshal(item)
				var decoded app.JobItemDTO
				if err != nil || json.Unmarshal(encoded, &decoded) != nil || !reflect.DeepEqual(decoded, item) {
					t.Fatal("JSON round trip changed raw identities, string cursors or observations")
				}
				count++
			}
			if page.NextCursor == "" {
				if limit == 0 {
					read.CreditRead()
				}
				break
			}
			if len(page.Items) != bound || page.NextCursor != page.Items[len(page.Items)-1].ID || page.NextCursor == cursor {
				t.Fatalf("invalid item continuation: %+v", page)
			}
			cursor = page.NextCursor
		}
		if count != len(intents) {
			t.Fatalf("only %d of %d items reachable", count, len(intents))
		}
	}
	totals, read := h.JobItemTotals(ctx, job.ID)
	want := app.JobItemTotalsDTO{Status: "available", Items: 402, Planned: 402, Admitted: 402, Failed: 401, Problems: 401}
	if read.Err() != nil || totals != want {
		t.Fatalf("distinct totals = %+v, %v; want %+v", totals, read.Err(), want)
	}
	history.CreditRead()
	read.CreditRead()
}

func TestJobItemAccessDistinguishesMissingFactsAndFacets(t *testing.T) {
	h, store, _ := recoveryStatusHarness(t)
	ctx := t.Context()
	for _, version := range []int{0, 1} {
		job, err := store.Create(ctx, jobs.Job{Type: jobs.TypeCopy, ItemRecordsVersion: version, SourceBookmarkID: h.Primary.BookmarkID})
		if err != nil {
			t.Fatal(err)
		}
		want := "not_captured"
		if version == 1 {
			want = "available"
		}
		page, read := h.JobItems(ctx, job.ID, "", 100)
		totals, totalRead := h.JobItemTotals(ctx, job.ID)
		if read.Err() != nil || totalRead.Err() != nil || page.Status != want || page.Items == nil || len(page.Items) != 0 || page.NextCursor != "" || totals != (app.JobItemTotalsDTO{Status: want}) {
			t.Fatalf("uncaptured/empty conflated: %+v %+v, %v %v", page, totals, read.Err(), totalRead.Err())
		}
	}
	job, err := store.Create(ctx, jobs.Job{Type: jobs.TypeUpload, ItemRecordsVersion: 1, SourceBookmarkID: h.Primary.BookmarkID})
	if err != nil {
		t.Fatal(err)
	}
	intents := []jobs.ItemIntent{retainedIntent("missing"), retainedIntent("copied"), retainedIntent("refused"), retainedIntent("conflict"), retainedIntent("same")}
	intents[0].Source = jobs.ItemLocation{Kind: jobs.ItemLocal, LocalPath: filepath.Join(t.TempDir(), "local\nname")}
	intents[2].Admitted = false
	intents[4].Planned, intents[4].Admitted = false, false
	if err := store.AdmitItems(ctx, job.ID, intents); err != nil {
		t.Fatal(err)
	}
	for _, row := range []struct {
		key string
		obs jobs.ItemObservation
	}{
		{"copied", jobs.ItemObservation{Result: jobs.ItemCopied, Effect: jobs.EffectUnresolved, CopiedBytes: 9, Diagnostic: jobs.ItemDiagnostic{Stage: jobs.StageCleanup, Code: jobs.CodeIO}}},
		{"refused", jobs.ItemObservation{Result: jobs.ItemRefused, Effect: jobs.EffectNotAttempted, Diagnostic: jobs.ItemDiagnostic{Stage: jobs.StagePlanning, Code: jobs.CodeConflict}}},
		{"conflict", jobs.ItemObservation{Result: jobs.ItemConflict, Effect: jobs.EffectUnresolved, Diagnostic: jobs.ItemDiagnostic{Stage: jobs.StageCommit, Code: jobs.CodeConflict, HTTPStatus: 412}}},
		{"same", jobs.ItemObservation{Result: jobs.ItemSame, Effect: jobs.EffectNotAttempted}},
	} {
		if err := store.RecordExecution(ctx, job.ID, row.key, row.obs); err != nil {
			t.Fatal(err)
		}
	}
	comparison := retainedIntent("comparison-only").ItemIdentity
	if err := store.RecordComparison(ctx, job.ID, comparison, jobs.ItemObservation{Result: jobs.ItemFailed, Effect: jobs.EffectNotAttempted,
		Diagnostic: jobs.ItemDiagnostic{Stage: jobs.StageVerification, Code: jobs.CodeUnverified}}); err != nil {
		t.Fatal(err)
	}
	page, read := h.JobItems(ctx, job.ID, "", 100)
	if read.Err() != nil || len(page.Items) != 6 {
		t.Fatalf("facet rows = %+v, %v", page, read.Err())
	}
	if page.Items[0].Execution != nil || page.Items[0].Verification != nil || page.Items[0].Source.LocalPath != intents[0].Source.LocalPath || page.Items[0].Source.Display != strconv.Quote(intents[0].Source.LocalPath) ||
		page.Items[1].Execution.CopiedBytes != 9 || page.Items[1].Verification != nil || page.Items[2].Execution.Result != "refused" || page.Items[3].Execution.Result != "conflict" || page.Items[4].Execution.Result != "same" ||
		page.Items[5].Execution != nil || page.Items[5].Verification.Result != "failed" || page.Items[5].Planned || page.Items[5].Admitted {
		t.Fatalf("missing, comparison-only, local or execution facts changed: %+v", page)
	}
	totals, totalRead := h.JobItemTotals(ctx, job.ID)
	want := app.JobItemTotalsDTO{Status: "available", Items: 6, Planned: 4, Admitted: 3, Failed: 1, Conflicts: 1, Refused: 1, Unresolved: 3, Unverified: 1, Problems: 4}
	if totalRead.Err() != nil || totals != want {
		t.Fatalf("overlapping facets = %+v, %v; want %+v", totals, totalRead.Err(), want)
	}
}

func TestJobItemAccessRefusesInvalidPages(t *testing.T) {
	h, store, _ := recoveryStatusHarness(t)
	ctx := t.Context()
	job, err := store.Create(ctx, jobs.Job{Type: jobs.TypeCopy, ItemRecordsVersion: 1, SourceBookmarkID: h.Primary.BookmarkID})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.AdmitItems(ctx, job.ID, []jobs.ItemIntent{retainedIntent("a"), retainedIntent("b")}); err != nil {
		t.Fatal(err)
	}
	other, err := store.Create(ctx, jobs.Job{Type: jobs.TypeCopy, ItemRecordsVersion: 1, SourceBookmarkID: h.Primary.BookmarkID})
	if err != nil {
		t.Fatal(err)
	}
	first, read := h.JobItems(ctx, job.ID, "", 1)
	if read.Err() != nil || first.NextCursor == "" {
		t.Fatalf("cursor fixture = %+v, %v", first, read.Err())
	}
	for _, cursor := range []string{"0", "-1", "01", "+1", "1.0", "1e2", " 1", "9223372036854775808", "99999", "' OR 1=1 --"} {
		page, read := h.JobItems(ctx, job.ID, cursor, 1)
		if read.Err() == nil || read.Err().Error() != jobs.ErrInvalidItemPage.Error() || page.Items != nil || page.Status != "" || page.NextCursor != "" {
			t.Fatalf("invalid cursor %q returned data: %+v, %v", cursor, page, read.Err())
		}
	}
	for _, limit := range []int{-1, 201} {
		if page, read := h.JobItems(ctx, job.ID, "", limit); read.Err() == nil || page.Items != nil {
			t.Fatalf("invalid limit returned data: %+v, %v", page, read.Err())
		}
	}
	if page, read := h.JobItems(ctx, other.ID, first.NextCursor, 1); read.Err() == nil || page.Items != nil {
		t.Fatalf("cursor crossed jobs: %+v, %v", page, read.Err())
	}
	if page, read := h.JobItems(ctx, "missing", "", 1); read.Err() == nil || read.Err().Error() != jobs.ErrItemJobNotFound.Error() || page.Items != nil {
		t.Fatalf("missing job returned data: %+v, %v", page, read.Err())
	}
	if totals, read := h.JobItemTotals(ctx, "missing"); read.Err() == nil || read.Err().Error() != jobs.ErrItemJobNotFound.Error() || totals != (app.JobItemTotalsDTO{}) {
		t.Fatalf("missing job returned totals: %+v, %v", totals, read.Err())
	}
	if err := store.Available(); err != nil {
		t.Fatalf("invalid read requests latched required writes: %v", err)
	}
}

func TestJobItemAccessRefusesClosedDatabase(t *testing.T) {
	jobItemAccessRefusesUnavailable(t, "closed")
}

func TestJobItemAccessRefusesRecordingLatch(t *testing.T) {
	jobItemAccessRefusesUnavailable(t, "latch")
}

func TestJobItemAccessRefusesObservationQueryFailure(t *testing.T) {
	jobItemAccessRefusesUnavailable(t, "observation-query")
}

func jobItemAccessRefusesUnavailable(t *testing.T, fault string) {
	t.Helper()
	h, store, database := recoveryStatusHarness(t)
	ctx := t.Context()
	job, err := store.Create(ctx, jobs.Job{Type: jobs.TypeCopy, ItemRecordsVersion: 1, SourceBookmarkID: h.Primary.BookmarkID})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.AdmitItems(ctx, job.ID, []jobs.ItemIntent{retainedIntent("a"), retainedIntent("b")}); err != nil {
		t.Fatal(err)
	}
	switch fault {
	case "closed":
		if err := database.Close(); err != nil {
			t.Fatal(err)
		}
	case "latch":
		if err := store.AdmitItems(ctx, job.ID, nil); err == nil {
			t.Fatal("required write refusal was not injected")
		}
	case "observation-query":
		if err := database.WriteTx(ctx, func(tx *sql.Tx) error {
			_, err := tx.ExecContext(ctx, `ALTER TABLE job_item_observations RENAME TO private_provider_payload`)
			return err
		}); err != nil {
			t.Fatal(err)
		}
	}
	page, read := h.JobItems(ctx, job.ID, "", 1)
	totals, totalRead := h.JobItemTotals(ctx, job.ID)
	want := "item records are unavailable; retained outcomes cannot be confirmed"
	if read.Err() == nil || totalRead.Err() == nil || read.Err().Error() != want || totalRead.Err().Error() != want ||
		page.Status != "" || page.Items != nil || page.NextCursor != "" || totals != (app.JobItemTotalsDTO{}) {
		t.Fatalf("unavailable records fabricated a page/count or leaked errors: %+v %+v, %v %v", page, totals, read.Err(), totalRead.Err())
	}
	read.CreditRefusal()
	totalRead.CreditRefusal()
	if fault != "observation-query" {
		history, result := h.JobHistory(ctx, "", "", 50)
		if result.Err() == nil || result.Err().Error() != jobs.ErrHistoryUnavailable.Error() || history.Jobs != nil || history.NextCursor != "" {
			t.Fatalf("unavailable history returned data: %+v, %v", history, result.Err())
		}
		result.CreditRefusal()
	}
}

func TestJobItemAccessDropsPageOnInvalidRetainedIdentity(t *testing.T) {
	h, store, database := recoveryStatusHarness(t)
	ctx := t.Context()
	job, err := store.Create(ctx, jobs.Job{Type: jobs.TypeCopy, ItemRecordsVersion: 1, SourceBookmarkID: h.Primary.BookmarkID})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.AdmitItems(ctx, job.ID, []jobs.ItemIntent{retainedIntent("good"), retainedIntent("bad")}); err != nil {
		t.Fatal(err)
	}
	if err := database.WriteTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE job_items SET source_json=? WHERE item_key='bad'`, `{"kind":"swift","provider":"secret-payload"}`)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	page, read := h.JobItems(ctx, job.ID, "", 100)
	if read.Err() == nil || page.Items != nil || page.Status != "" || strings.Contains(read.Err().Error(), "secret-payload") {
		t.Fatalf("bad later identity leaked partial data: %+v, %v", page, read.Err())
	}
}
