package jobs

import (
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/csmart/aeronautes/internal/audit"
)

func recordedJob(t *testing.T, s *Store, kind Type) Job {
	t.Helper()
	j, err := s.Create(t.Context(), Job{Type: kind, ItemRecordsVersion: 1})
	if err != nil {
		t.Fatal(err)
	}
	for _, state := range []State{StatePlanning, StateRunning, StateVerifying} {
		if err := s.Transition(t.Context(), j.ID, state); err != nil {
			t.Fatal(err)
		}
	}
	return j
}

func transferIntent(bookmark, key string) ItemIntent {
	return ItemIntent{ItemIdentity: ItemIdentity{
		Key:         key,
		Source:      ItemLocation{Kind: ItemSwift, BookmarkID: bookmark, Container: "source", Object: "scope/" + key},
		Destination: ItemLocation{Kind: ItemSwift, BookmarkID: bookmark + "-other", Container: "destination", Object: "scope/" + key},
	}, Planned: true, Admitted: true, ListedBytes: 10}
}

func itemProblem(result ItemResult, stage ItemStage, code ItemCode) ItemObservation {
	return ItemObservation{Result: result, Effect: EffectNotAttempted, Diagnostic: ItemDiagnostic{Stage: stage, Code: code}}
}

func itemCompletion(job Job, state State) Completion {
	result := audit.ResultVerifyMismatch
	if state == StateComplete {
		result = audit.ResultApplied
	} else if state == StateFailed || state == StateCancelled {
		result = audit.ResultFailed
	}
	return Completion{State: state, Counters: Counters{ObjectsCopied: 1, ObjectsVerified: 1, ObjectsFailed: 9999, BytesCopied: 10},
		Error: "retained item outcomes", Audit: audit.Event{Operation: "transfer.copy." + string(state), Resource: "job " + job.ID, Result: result}}
}

func readAllItems(t *testing.T, s *Store, id string, limit int) []ItemRecord {
	t.Helper()
	var out []ItemRecord
	var cursor int64
	for {
		page, err := s.ReadItems(t.Context(), id, cursor, limit)
		if err != nil {
			t.Fatal(err)
		}
		bound := limit
		if bound == 0 {
			bound = 100
		}
		if len(page.Items) > bound {
			t.Fatalf("page exceeded bound: %d > %d", len(page.Items), bound)
		}
		for _, item := range page.Items {
			if item.ID <= cursor || item.JobID != id {
				t.Fatalf("page repeated, reordered or leaked an item: %+v cursor=%d", item, cursor)
			}
			cursor = item.ID
			out = append(out, item)
		}
		if page.NextCursor == 0 {
			return out
		}
		if len(page.Items) != bound || page.NextCursor != cursor {
			t.Fatalf("nonprogressing or incorrect continuation: %+v", page)
		}
	}
}

func TestItemRecordsExactFailuresAndPages(t *testing.T) {
	for _, failures := range []int{0, 1, 201, 401, 1203} {
		t.Run(fmt.Sprint(failures), func(t *testing.T) {
			s, bookmark := newStore(t)
			job := recordedJob(t, s, TypeCopy)
			intents := make([]ItemIntent, failures+1)
			for i := range intents {
				intents[i] = transferIntent(bookmark, fmt.Sprintf("f-%04d\nname", i))
			}
			for offset := 0; offset < len(intents); offset += 500 {
				if err := s.AdmitItems(t.Context(), job.ID, intents[offset:min(offset+500, len(intents))]); err != nil {
					t.Fatal(err)
				}
			}
			execution := itemProblem(ItemFailed, StageSourceRead, CodeForbidden)
			execution.Diagnostic.HTTPStatus = 403
			verification := itemProblem(ItemFailed, StageVerification, CodeMissing)
			verification.Diagnostic.HTTPStatus = 404
			for _, intent := range intents[:failures] {
				if err := s.RecordExecution(t.Context(), job.ID, intent.Key, execution); err != nil {
					t.Fatal(err)
				}
				if err := s.RecordComparison(t.Context(), job.ID, intent.ItemIdentity, verification); err != nil {
					t.Fatal(err)
				}
			}
			success := intents[failures]
			copied := ItemObservation{Result: ItemCopied, Effect: EffectReportedApplied, CopiedBytes: 10}
			verified := ItemObservation{Result: ItemVerified, Effect: EffectNotAttempted}
			if err := s.RecordExecution(t.Context(), job.ID, success.Key, copied); err != nil {
				t.Fatal(err)
			}
			if err := s.RecordComparison(t.Context(), job.ID, success.ItemIdentity, verified); err != nil {
				t.Fatal(err)
			}
			wantTotals := ItemTotals{Items: int64(failures + 1), Planned: int64(failures + 1), Admitted: int64(failures + 1), Failed: int64(failures), Problems: int64(failures)}
			if got, err := s.ReadItemTotals(t.Context(), job.ID); err != nil || got != wantTotals {
				t.Fatalf("logical failures must count once, beyond samples: got=%+v want=%+v err=%v", got, wantTotals, err)
			}
			for _, limit := range []int{0, 37, 200} {
				items := readAllItems(t, s, job.ID, limit)
				if len(items) != len(intents) {
					t.Fatalf("incomplete failure access: %d of %d", len(items), len(intents))
				}
				for i, item := range items {
					if !reflect.DeepEqual(item.ItemIntent, intents[i]) || item.CreatedAt.IsZero() || item.Execution == nil || item.Verification == nil {
						t.Fatalf("identity or observations changed: %+v", item)
					}
					wantExec, wantVerify := execution, verification
					if i == failures {
						wantExec, wantVerify = copied, verified
					}
					if item.Execution.ItemObservation != wantExec || item.Verification.ItemObservation != wantVerify || item.Execution.CreatedAt.IsZero() || item.Verification.CreatedAt.IsZero() {
						t.Fatalf("observation facts changed: %+v", item)
					}
				}
			}
			state := StatePartial
			if failures == 0 {
				state = StateComplete
			}
			completion := itemCompletion(job, state)
			trail := audit.NewStore(s.db)
			if err := s.FinishFromItems(t.Context(), job.ID, completion, trail); err != nil {
				t.Fatal(err)
			}
			completion.Counters.ObjectsFailed = int64(failures)
			got, err := s.Get(t.Context(), job.ID)
			if err != nil || got.Counters != completion.Counters || got.State != state || got.FinishedAt.IsZero() {
				t.Fatalf("terminal total came from a sample or caller guess: %+v %v", got, err)
			}
			events, err := trail.List(t.Context(), audit.Query{Limit: 10})
			if err != nil || len(events) != 1 || events[0].Resource != completion.Audit.Resource {
				t.Fatalf("missing terminal audit: %+v %v", events, err)
			}
		})
	}
}

func TestItemRecordVersionAndEmpty(t *testing.T) {
	s, _ := newStore(t)
	legacy, err := s.Create(t.Context(), Job{Type: TypeCopy})
	if err != nil {
		t.Fatal(err)
	}
	if page, err := s.ReadItems(t.Context(), legacy.ID, 0, 0); !errors.Is(err, ErrItemRecordsNotCaptured) || page.Items != nil {
		t.Fatalf("unrecorded job presented as empty: %+v %v", page, err)
	}
	if totals, err := s.ReadItemTotals(t.Context(), legacy.ID); !errors.Is(err, ErrItemRecordsNotCaptured) || totals != (ItemTotals{}) {
		t.Fatalf("unrecorded totals presented as exact: %+v %v", totals, err)
	}
	for _, kind := range []Type{TypeCopy, TypeUpload, TypeDownload} {
		job := recordedJob(t, s, kind)
		if page, err := s.ReadItems(t.Context(), job.ID, 0, 0); err != nil || page.Items == nil || len(page.Items) != 0 || page.NextCursor != 0 {
			t.Fatalf("recorded empty job unavailable: %+v %v", page, err)
		}
		if totals, err := s.ReadItemTotals(t.Context(), job.ID); err != nil || totals != (ItemTotals{}) {
			t.Fatalf("empty totals: %+v %v", totals, err)
		}
		got, err := s.Get(t.Context(), job.ID)
		if err != nil || got.ItemRecordsVersion != 1 {
			t.Fatalf("version not retained: %+v %v", got, err)
		}
		result := itemCompletion(job, StateComplete)
		result.Counters = Counters{}
		if err := s.FinishFromItems(t.Context(), job.ID, result, audit.NewStore(s.db)); err != nil {
			t.Fatal(err)
		}
		if err := s.db.WriteTx(t.Context(), func(tx *sql.Tx) error {
			_, err := tx.ExecContext(t.Context(), `UPDATE jobs SET item_records_version=0 WHERE id=?`, job.ID)
			return err
		}); err == nil {
			t.Fatal("recording mode disabled after creation")
		}
	}
	for _, input := range []Job{{Type: TypeCopy, ItemRecordsVersion: -1}, {Type: TypeCopy, ItemRecordsVersion: 2}, {Type: TypeDeletePrefix, ItemRecordsVersion: 1}, {Type: TypeMirror, ItemRecordsVersion: 1}, {Type: TypeMirrorPreview, ItemRecordsVersion: 1}} {
		if _, err := s.Create(t.Context(), input); !errors.Is(err, ErrInvalidItemRecord) {
			t.Fatalf("invalid opt-in accepted: %+v %v", input, err)
		}
	}
	if err := s.db.WriteTx(t.Context(), func(tx *sql.Tx) error {
		_, err := tx.ExecContext(t.Context(), `UPDATE jobs SET item_records_version=1 WHERE id=?`, legacy.ID)
		return err
	}); err == nil {
		t.Fatal("historical job relabelled as recorded")
	}
	if s.Available() != nil {
		t.Fatal("non-recording read/creation validation latched storage failure")
	}
}

func TestItemIdentityVerbatimAndScope(t *testing.T) {
	s, bookmark := newStore(t)
	keys := []string{"", "line\nbreak", "nul\x00tail", "é", "e\u0301", "a//b", "a/b", " space ", "Case", "case", `back\slash`, "🐦"}
	for _, kind := range []Type{TypeCopy, TypeUpload, TypeDownload} {
		job := recordedJob(t, s, kind)
		var intents []ItemIntent
		for i, key := range keys {
			item := transferIntent(bookmark+string(kind), key)
			if kind == TypeUpload {
				item.Source = ItemLocation{Kind: ItemLocal, LocalPath: filepath.Join(t.TempDir(), fmt.Sprint(i))}
			}
			if kind == TypeDownload {
				item.Destination = ItemLocation{Kind: ItemLocal, LocalPath: filepath.Join(t.TempDir(), fmt.Sprint(i))}
			}
			stage := ItemLocation{Kind: ItemSwift, BookmarkID: bookmark, Container: "staging", Object: job.ID + "/" + key}
			item.Staging = &stage
			intents = append(intents, item)
		}
		if err := s.AdmitItems(t.Context(), job.ID, intents); err != nil {
			t.Fatal(err)
		}
		items := readAllItems(t, s, job.ID, 1)
		if len(items) != len(intents) {
			t.Fatalf("names collapsed: %d of %d", len(items), len(intents))
		}
		for i, got := range items {
			if !reflect.DeepEqual(got.ItemIntent, intents[i]) || got.Execution != nil || got.Verification != nil {
				t.Fatalf("identity changed: got=%+v want=%+v", got, intents[i])
			}
		}
	}
}

func TestItemAdmissionAtomicAndImmutable(t *testing.T) {
	for _, fault := range []string{"duplicate_in_batch", "existing_same", "existing_changed", "sql_second", "invalid_second", "empty", "too_many", "legacy", "missing"} {
		t.Run(fault, func(t *testing.T) {
			s, bookmark := newStore(t)
			job := recordedJob(t, s, TypeCopy)
			prior := transferIntent(bookmark, "prior")
			if err := s.AdmitItems(t.Context(), job.ID, []ItemIntent{prior}); err != nil {
				t.Fatal(err)
			}
			before := readAllItems(t, s, job.ID, 0)
			batch := []ItemIntent{transferIntent(bookmark, "one"), transferIntent(bookmark, "two")}
			id := job.ID
			switch fault {
			case "duplicate_in_batch":
				batch[1] = batch[0]
			case "existing_same":
				batch[1] = prior
			case "existing_changed":
				batch[1] = prior
				batch[1].Destination.Container = "different"
			case "sql_second":
				itemSQL(t, s, `CREATE TRIGGER refuse_item BEFORE INSERT ON job_items WHEN NEW.item_key='two' BEGIN SELECT RAISE(ABORT,'private provider token=fixture'); END`)
			case "invalid_second":
				batch[1].Key = string([]byte{0xff})
			case "empty":
				batch = nil
			case "too_many":
				batch = make([]ItemIntent, 501)
				for i := range batch {
					batch[i] = transferIntent(bookmark, fmt.Sprint(i))
				}
			case "legacy":
				legacy, err := s.Create(t.Context(), Job{Type: TypeCopy})
				if err != nil {
					t.Fatal(err)
				}
				id = legacy.ID
			case "missing":
				id = "missing"
			}
			err := s.AdmitItems(t.Context(), id, batch)
			assertItemUnavailable(t, s, job.ID, err)
			observer := NewStore(s.db)
			if got := readAllItems(t, observer, job.ID, 0); !reflect.DeepEqual(got, before) {
				t.Fatalf("rejected admission changed earlier records: got=%+v before=%+v", got, before)
			}
			var count int
			if err := s.db.Reader().QueryRow(`SELECT COUNT(*) FROM job_items`).Scan(&count); err != nil || count != 1 {
				t.Fatalf("partial batch survived: %d %v", count, err)
			}
		})
	}
}

func itemSQL(t *testing.T, s *Store, query string) {
	t.Helper()
	if err := s.db.WriteTx(t.Context(), func(tx *sql.Tx) error { _, err := tx.ExecContext(t.Context(), query); return err }); err != nil {
		t.Fatal(err)
	}
}

func assertItemUnavailable(t *testing.T, s *Store, jobID string, err error) {
	t.Helper()
	if err == nil || s.Available() == nil || strings.Contains(err.Error(), "private") || strings.Contains(err.Error(), "token=") || strings.Contains(err.Error(), "fixture") {
		t.Fatalf("recording failure ignored or leaked details: %v / %v", err, s.Available())
	}
	if page, err := s.ReadItems(t.Context(), jobID, 0, 100); err == nil || page.Items != nil {
		t.Fatalf("incomplete records presented as available: %+v %v", page, err)
	}
	if totals, err := s.ReadItemTotals(t.Context(), jobID); err == nil || totals != (ItemTotals{}) {
		t.Fatalf("incomplete totals presented as exact: %+v %v", totals, err)
	}
	if _, err := s.Create(t.Context(), Job{Type: TypeCopy}); err == nil {
		t.Fatal("new job admitted after item recording failure")
	}
	if err := s.AdmitItems(t.Context(), jobID, []ItemIntent{transferIntent("bookmark", "later")}); err == nil {
		t.Fatal("new item admitted after recording failure")
	}
}

func TestItemInvalidLocationsAndEncoding(t *testing.T) {
	for _, fault := range []string{"key_utf8", "source_utf8", "destination_utf8", "stage_utf8", "kind", "swift_local", "swift_empty", "local_relative", "local_swift", "negative_bytes"} {
		t.Run(fault, func(t *testing.T) {
			s, bookmark := newStore(t)
			job := recordedJob(t, s, TypeCopy)
			item := transferIntent(bookmark, "valid")
			switch fault {
			case "key_utf8":
				item.Key = string([]byte{0xff})
			case "source_utf8":
				item.Source.Object = string([]byte{0xff})
			case "destination_utf8":
				item.Destination.BookmarkID = string([]byte{0xff})
			case "stage_utf8":
				stage := item.Source
				stage.Container = string([]byte{0xff})
				item.Staging = &stage
			case "kind":
				item.Source.Kind = "https://provider/?token=fixture"
			case "swift_local":
				item.Source.LocalPath = filepath.Join(t.TempDir(), "source")
			case "swift_empty":
				item.Source.Object = ""
			case "local_relative":
				item.Source = ItemLocation{Kind: ItemLocal, LocalPath: "relative"}
			case "local_swift":
				item.Source = ItemLocation{Kind: ItemLocal, LocalPath: filepath.Join(t.TempDir(), "source"), Object: "secret"}
			case "negative_bytes":
				item.ListedBytes = -1
			}
			err := s.AdmitItems(t.Context(), job.ID, []ItemIntent{item})
			if !errors.Is(err, ErrInvalidItemRecord) {
				t.Fatalf("invalid identity accepted: %v", err)
			}
			assertItemUnavailable(t, s, job.ID, err)
			if got := readAllItems(t, NewStore(s.db), job.ID, 0); len(got) != 0 {
				t.Fatalf("unrepresentable identity stored: %+v", got)
			}
		})
	}
}

func TestItemReadsRejectBadRequestsWithoutLatch(t *testing.T) {
	s, bookmark := newStore(t)
	job := recordedJob(t, s, TypeCopy)
	other := recordedJob(t, s, TypeCopy)
	if err := s.AdmitItems(t.Context(), other.ID, []ItemIntent{transferIntent(bookmark, "other")}); err != nil {
		t.Fatal(err)
	}
	otherID := readAllItems(t, s, other.ID, 0)[0].ID
	for _, request := range []struct {
		cursor int64
		limit  int
	}{{-1, 1}, {0, -1}, {0, 201}, {otherID, 100}, {999999, 100}} {
		if _, err := s.ReadItems(t.Context(), job.ID, request.cursor, request.limit); !errors.Is(err, ErrInvalidItemPage) {
			t.Fatalf("invalid page request accepted: %+v %v", request, err)
		}
	}
	if _, err := s.ReadItems(t.Context(), "missing", 0, 0); !errors.Is(err, ErrItemJobNotFound) {
		t.Fatalf("missing job presented as empty: %v", err)
	}
	if _, err := s.ReadItemTotals(t.Context(), "missing"); !errors.Is(err, ErrItemJobNotFound) {
		t.Fatalf("missing job totals presented as exact: %v", err)
	}
	if s.Available() != nil {
		t.Fatal("bad read request latched recording failure")
	}
	if err := s.AdmitItems(t.Context(), job.ID, []ItemIntent{transferIntent(bookmark, "after-invalid-read")}); err != nil {
		t.Fatal(err)
	}
	if err := s.db.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ReadItems(t.Context(), job.ID, 0, 0); !errors.Is(err, ErrItemRecordsUnavailable) {
		t.Fatalf("unavailable read presented as empty: %v", err)
	}
	if _, err := s.ReadItemTotals(t.Context(), job.ID); !errors.Is(err, ErrItemRecordsUnavailable) {
		t.Fatalf("unavailable totals presented as exact: %v", err)
	}
}

func TestItemPruningRetainsInterruptedRecords(t *testing.T) {
	s, bookmark := newStore(t)
	job := recordedJob(t, s, TypeCopy)
	intents := []ItemIntent{transferIntent(bookmark, "failed"), transferIntent(bookmark, "no-outcome")}
	if err := s.AdmitItems(t.Context(), job.ID, intents); err != nil {
		t.Fatal(err)
	}
	if err := s.RecordExecution(t.Context(), job.ID, "failed", itemProblem(ItemFailed, StageEngine, CodeUnknown)); err != nil {
		t.Fatal(err)
	}
	before := readAllItems(t, s, job.ID, 0)
	legacy, err := s.Create(t.Context(), Job{Type: TypeCopy})
	if err != nil {
		t.Fatal(err)
	}
	if n, err := s.FailInterrupted(t.Context()); err != nil || n != 2 {
		t.Fatalf("interrupted jobs: %d %v", n, err)
	}
	if n, err := s.PruneTerminal(t.Context(), time.Now().Add(time.Hour)); err != nil || n != 1 {
		t.Fatalf("pruning must retain item job and remove only ordinary history: %d %v", n, err)
	}
	if _, err := s.Get(t.Context(), legacy.ID); err == nil {
		t.Fatal("ordinary empty legacy job was not pruned")
	}
	if got := readAllItems(t, s, job.ID, 0); !reflect.DeepEqual(got, before) {
		t.Fatalf("restart/pruning changed retained facts: %+v", got)
	}
	if totals, err := s.ReadItemTotals(t.Context(), job.ID); err != nil || totals.Failed != 1 || totals.Unresolved != 1 {
		t.Fatalf("restart concealed failure or missing outcome: %+v %v", totals, err)
	}
	if err := s.db.WriteTx(t.Context(), func(tx *sql.Tx) error {
		_, err := tx.ExecContext(t.Context(), `DELETE FROM jobs WHERE id=?`, job.ID)
		return err
	}); err == nil {
		t.Fatal("owning job deletion discarded item facts")
	}
}
