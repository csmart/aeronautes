package jobs

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/csmart/aeronautes/internal/audit"
	"github.com/csmart/aeronautes/internal/db"
)

func nativeDownloadIntent(bookmark, key string) ItemIntent {
	intent := transferIntent(bookmark, key)
	intent.Destination = ItemLocation{Kind: ItemLocal, LocalPath: filepath.Join(os.TempDir(), "download", key)}
	intent.Staging = &ItemLocation{Kind: ItemLocal, LocalPath: intent.Destination.LocalPath + ".temporary"}
	intent.PublicationRequired = true
	return intent
}

func recordCopiedDownload(t *testing.T, s *Store, job Job, intent ItemIntent) {
	t.Helper()
	if err := s.AdmitItems(t.Context(), job.ID, []ItemIntent{intent}); err != nil {
		t.Fatal(err)
	}
	if err := s.RecordExecution(t.Context(), job.ID, intent.Key, ItemObservation{Result: ItemCopied, Effect: EffectReportedApplied, CopiedBytes: 10}); err != nil {
		t.Fatal(err)
	}
	if err := s.RecordComparison(t.Context(), job.ID, intent.ItemIdentity, ItemObservation{Result: ItemVerified, Effect: EffectNotAttempted}); err != nil {
		t.Fatal(err)
	}
}

func publicationFailure() ItemObservation {
	return ItemObservation{Result: ItemFailed, Effect: EffectUnresolved,
		Diagnostic: ItemDiagnostic{Stage: StageVerification, Code: CodeUnverified}}
}

func TestDownloadPublicationFactsAndCompletion(t *testing.T) {
	for _, scenario := range []string{"verified", "failed", "failed_with_cleanup", "missing", "historical", "not_reached"} {
		t.Run(scenario, func(t *testing.T) {
			s, bookmark := newStore(t)
			job := recordedJob(t, s, TypeDownload)
			intent := nativeDownloadIntent(bookmark, "selected")
			intent.PublicationRequired = scenario != "historical"
			want := ItemTotals{Items: 1, Planned: 1, Admitted: 1}
			if scenario == "not_reached" {
				if err := s.AdmitItems(t.Context(), job.ID, []ItemIntent{intent}); err != nil {
					t.Fatal(err)
				}
				if err := s.RecordExecution(t.Context(), job.ID, intent.Key, itemProblem(ItemFailed, StageSourceRead, CodeIO)); err != nil {
					t.Fatal(err)
				}
				want.Failed, want.Problems = 1, 1
			} else {
				if scenario == "failed_with_cleanup" {
					if err := s.AdmitItems(t.Context(), job.ID, []ItemIntent{intent}); err != nil {
						t.Fatal(err)
					}
					if err := s.RecordExecution(t.Context(), job.ID, intent.Key, ItemObservation{Result: ItemCopied, Effect: EffectUnresolved, CopiedBytes: 10,
						Diagnostic: ItemDiagnostic{Stage: StageCleanup, Code: CodeIO}}); err != nil {
						t.Fatal(err)
					}
					if err := s.RecordComparison(t.Context(), job.ID, intent.ItemIdentity, ItemObservation{Result: ItemVerified, Effect: EffectNotAttempted}); err != nil {
						t.Fatal(err)
					}
				} else {
					recordCopiedDownload(t, s, job, intent)
				}
			}
			var publication *ItemObservation
			switch scenario {
			case "verified":
				publication = &ItemObservation{Result: ItemVerified, Effect: EffectNotAttempted}
			case "failed", "failed_with_cleanup":
				failed := publicationFailure()
				publication = &failed
				want.Failed, want.Unverified, want.Unresolved, want.Problems = 1, 1, 1, 1
			case "missing":
				want.Unverified = 1
			}
			early := readAllItems(t, s, job.ID, 1)[0]
			if publication != nil {
				if err := s.RecordPublication(t.Context(), job.ID, intent.ItemIdentity, *publication); err != nil {
					t.Fatalf("record final publication: %v", err)
				}
			}
			items := readAllItems(t, s, job.ID, 1)
			if len(items) != 1 || !reflect.DeepEqual(items[0].ItemIntent, intent) ||
				(publication == nil) != (items[0].Publication == nil) ||
				(publication != nil && items[0].Publication.ItemObservation != *publication) {
				t.Fatalf("publication requirement or fact changed: %+v", items)
			}
			if !reflect.DeepEqual(items[0].Execution, early.Execution) || !reflect.DeepEqual(items[0].Verification, early.Verification) {
				t.Fatal("final publication rewrote the acknowledged early facts")
			}
			if totals, err := s.ReadItemTotals(t.Context(), job.ID); err != nil || totals != want {
				t.Fatalf("publication facets: %+v want=%+v err=%v", totals, want, err)
			}
			trail := audit.NewStore(s.db)
			completion := itemCompletion(job, StateComplete)
			completion.Audit.Operation = "transfer.local.complete"
			if scenario == "not_reached" {
				completion.Counters.ObjectsCopied, completion.Counters.ObjectsVerified, completion.Counters.BytesCopied = 0, 0, 0
			}
			if scenario == "failed" || scenario == "failed_with_cleanup" {
				completion.Counters.ObjectsVerified = 0
			}
			err := s.FinishFromItems(t.Context(), job.ID, completion, trail)
			if scenario == "verified" || scenario == "historical" {
				if err != nil {
					t.Fatal(err)
				}
			} else {
				if !errors.Is(err, ErrIncompleteItemRecords) {
					t.Fatalf("incomplete final evidence permitted complete: %v", err)
				}
				completion.State = StatePartial
				completion.Audit.Operation = "transfer.local.partial"
				completion.Audit.Result = audit.ResultVerifyMismatch
				if err := s.FinishFromItems(t.Context(), job.ID, completion, trail); err != nil {
					t.Fatal(err)
				}
			}
			observer := NewStore(s.db)
			got, err := observer.Get(t.Context(), job.ID)
			if err != nil || got.State != completion.State || got.Counters.ObjectsFailed != want.Failed ||
				got.Counters.ObjectsCopied != completion.Counters.ObjectsCopied || got.Counters.BytesCopied != completion.Counters.BytesCopied || got.Counters.ObjectsVerified != completion.Counters.ObjectsVerified {
				t.Fatalf("completion erased witnessed counters: %+v %v", got, err)
			}
			events, err := trail.List(t.Context(), audit.Query{Limit: 2})
			wantSummary := fmt.Sprintf(" · item outcomes: %d failed names, 0 conflicts, 0 refusals, %d unresolved, %d unverified, %d with problems", want.Failed, want.Unresolved, want.Unverified, want.Problems)
			if err != nil || len(events) != 1 || events[0].Summary != wantSummary {
				t.Fatalf("audit disagrees with final facts: %+v %v", events, err)
			}
		})
	}
}

func TestDownloadPublicationAdmissionScope(t *testing.T) {
	for _, scenario := range []string{"copy", "upload", "directory", "unadmitted", "unplanned", "swift_destination", "local_source", "no_staging", "swift_staging"} {
		t.Run(scenario, func(t *testing.T) {
			s, bookmark := newStore(t)
			kind := TypeDownload
			if scenario == "copy" {
				kind = TypeCopy
			}
			if scenario == "upload" {
				kind = TypeUpload
			}
			job := recordedJob(t, s, kind)
			intent := nativeDownloadIntent(bookmark, "selected")
			switch scenario {
			case "directory":
				intent.Source.Object += "/"
			case "unadmitted":
				intent.Admitted = false
			case "unplanned":
				intent.Planned = false
			case "swift_destination":
				intent.Destination = intent.Source
			case "local_source":
				intent.Source = intent.Destination
			case "no_staging":
				intent.Staging = nil
			case "swift_staging":
				intent.Staging = &intent.Source
			}
			err := s.AdmitItems(t.Context(), job.ID, []ItemIntent{intent})
			if !errors.Is(err, ErrInvalidItemRecord) {
				t.Fatalf("invalid publication requirement admitted: %v", err)
			}
			if got := readAllItems(t, NewStore(s.db), job.ID, 1); len(got) != 0 {
				t.Fatalf("invalid admission survived: %+v", got)
			}
		})
	}
}

func TestDownloadPublicationRequiresAcknowledgedIdentity(t *testing.T) {
	for _, scenario := range []string{"not_admitted", "unflagged", "missing_execution", "missing_verification", "failed_verification", "identity", "terminal", "sql", "invalid_effect", "invalid_result", "invalid_bytes", "invalid_diagnostic"} {
		t.Run(scenario, func(t *testing.T) {
			s, bookmark := newStore(t)
			job := recordedJob(t, s, TypeDownload)
			intent := nativeDownloadIntent(bookmark, "selected")
			intent.PublicationRequired = scenario != "unflagged"
			if scenario != "not_admitted" {
				if err := s.AdmitItems(t.Context(), job.ID, []ItemIntent{intent}); err != nil {
					t.Fatal(err)
				}
				if scenario != "missing_execution" {
					if err := s.RecordExecution(t.Context(), job.ID, intent.Key, ItemObservation{Result: ItemCopied, Effect: EffectReportedApplied, CopiedBytes: 10}); err != nil {
						t.Fatal(err)
					}
				}
				if scenario != "missing_verification" {
					verification := ItemObservation{Result: ItemVerified, Effect: EffectNotAttempted}
					if scenario == "failed_verification" {
						verification = itemProblem(ItemFailed, StageVerification, CodeUnverified)
					}
					if err := s.RecordComparison(t.Context(), job.ID, intent.ItemIdentity, verification); err != nil {
						t.Fatal(err)
					}
				}
			}
			if scenario == "terminal" {
				if err := s.FinishFromItems(t.Context(), job.ID, itemCompletion(job, StatePartial), audit.NewStore(s.db)); err != nil {
					t.Fatal(err)
				}
			}
			before := readAllItems(t, s, job.ID, 1)
			identity := intent.ItemIdentity
			observation := ItemObservation{Result: ItemVerified, Effect: EffectNotAttempted}
			switch scenario {
			case "identity":
				identity.Destination.LocalPath += "-other"
			case "sql":
				itemSQL(t, s, `CREATE TRIGGER refuse_publication BEFORE INSERT ON job_item_publications BEGIN SELECT RAISE(ABORT,'private token=fixture'); END`)
			case "invalid_effect":
				observation.Effect = EffectReportedApplied
			case "invalid_result":
				observation.Result = ItemCopied
			case "invalid_bytes":
				observation.CopiedBytes = 10
			case "invalid_diagnostic":
				observation.Diagnostic.Code = "private token=fixture"
			}
			err := s.RecordPublication(t.Context(), job.ID, identity, observation)
			assertItemUnavailable(t, s, job.ID, err)
			if got := readAllItems(t, NewStore(s.db), job.ID, 1); !reflect.DeepEqual(got, before) {
				t.Fatalf("refused publication altered prior facts: %+v", got)
			}
		})
	}
}

func TestDownloadPublicationIsImmutableAndSurvivesLatch(t *testing.T) {
	s, bookmark := newStore(t)
	job := recordedJob(t, s, TypeDownload)
	intent := nativeDownloadIntent(bookmark, "selected")
	recordCopiedDownload(t, s, job, intent)
	if err := s.AdmitItems(t.Context(), job.ID, []ItemIntent{intent}); err == nil {
		t.Fatal("repeated admission did not latch the store")
	}
	verified := ItemObservation{Result: ItemVerified, Effect: EffectNotAttempted}
	if err := s.RecordPublication(t.Context(), job.ID, intent.ItemIdentity, verified); err != nil {
		t.Fatalf("an earlier admission failure lost completed publication facts: %v", err)
	}
	observer := NewStore(s.db)
	before := readAllItems(t, observer, job.ID, 1)
	if err := s.RecordPublication(t.Context(), job.ID, intent.ItemIdentity, verified); err != nil {
		t.Fatal(err)
	}
	if got := readAllItems(t, observer, job.ID, 1); !reflect.DeepEqual(got, before) {
		t.Fatal("identical publication changed timestamp or facts")
	}
	if err := s.RecordPublication(t.Context(), job.ID, intent.ItemIdentity, publicationFailure()); !errors.Is(err, ErrItemRecordConflict) {
		t.Fatalf("different publication replaced the immutable fact: %v", err)
	}
	if got := readAllItems(t, observer, job.ID, 1); !reflect.DeepEqual(got, before) {
		t.Fatal("conflicting publication changed prior facts")
	}
	if s.Available() == nil {
		t.Fatal("salvaged publication cleared the failure latch")
	}
}

func TestDownloadPublicationReopenAndExactPages(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.sqlite")
	d, err := db.Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if d != nil {
			_ = d.Close()
		}
	}()
	s := NewStore(d)
	job := recordedJob(t, s, TypeDownload)
	const failures = 201
	for i := range failures + 2 {
		intent := nativeDownloadIntent("account", fmt.Sprintf("selected-%03d", i))
		recordCopiedDownload(t, s, job, intent)
		if i == failures {
			continue
		}
		observation := publicationFailure()
		if i > failures {
			observation = ItemObservation{Result: ItemVerified, Effect: EffectNotAttempted}
		}
		if err := s.RecordPublication(t.Context(), job.ID, intent.ItemIdentity, observation); err != nil {
			t.Fatal(err)
		}
	}
	wantItems := readAllItems(t, s, job.ID, 100)
	wantTotals := ItemTotals{Items: failures + 2, Planned: failures + 2, Admitted: failures + 2,
		Failed: failures, Unresolved: failures, Unverified: failures + 1, Problems: failures}
	if totals, err := s.ReadItemTotals(t.Context(), job.ID); err != nil || totals != wantTotals {
		t.Fatalf("exact publication totals: %+v %v", totals, err)
	}
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
	d = nil
	reopened, err := db.Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	s = NewStore(reopened)
	if got := readAllItems(t, s, job.ID, 1); !reflect.DeepEqual(got, wantItems) {
		t.Fatal("orderly reopen changed publication facts")
	}
	if got, err := s.ReadItemTotals(t.Context(), job.ID); err != nil || got != wantTotals {
		t.Fatalf("reopen changed publication totals: %+v %v", got, err)
	}
}
