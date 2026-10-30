package jobs

import (
	"errors"
	"reflect"
	"testing"
)

func TestItemObservationRules(t *testing.T) {
	for _, test := range []struct {
		name         string
		admitted     bool
		verification bool
		observation  ItemObservation
		valid        bool
	}{
		{"copied", true, false, ItemObservation{Result: ItemCopied, Effect: EffectReportedApplied, CopiedBytes: 10}, true},
		{"copied_uncertain", true, false, ItemObservation{Result: ItemCopied, Effect: EffectUnresolved, CopiedBytes: 10, Diagnostic: ItemDiagnostic{Stage: StageRecording, Code: CodeIO}}, true},
		{"failed_before_effect", true, false, itemProblem(ItemFailed, StageSourceRead, CodeForbidden), true},
		{"conflict", true, false, itemProblem(ItemConflict, StageCommit, CodeConflict), true},
		{"same", false, false, ItemObservation{Result: ItemSame, Effect: EffectNotAttempted}, true},
		{"refused", false, false, itemProblem(ItemRefused, StagePlanning, CodeUnsupported), true},
		// A staged overwrite refused at the fixed point may carry the unresolved
		// effect its staged bytes leave behind, but only for an admitted item;
		// an unadmitted refusal never staged anything under an acknowledgement.
		{"refused_unresolved_admitted", true, false, ItemObservation{Result: ItemRefused, Effect: EffectUnresolved, Diagnostic: ItemDiagnostic{Stage: StageCommit, Code: CodeConflict}}, true},
		{"refused_unresolved_unadmitted", false, false, ItemObservation{Result: ItemRefused, Effect: EffectUnresolved, Diagnostic: ItemDiagnostic{Stage: StageCommit, Code: CodeConflict}}, false},
		{"not_attempted", false, false, ItemObservation{Result: ItemNotAttempted, Effect: EffectNotAttempted}, true},
		{"verified", false, true, ItemObservation{Result: ItemVerified, Effect: EffectNotAttempted}, true},
		{"verification_failed", false, true, itemProblem(ItemFailed, StageVerification, CodeMissing), true},
		{"unknown_classification", true, false, itemProblem(ItemFailed, StageUnknown, CodeUnknown), true},
		{"unadmitted_copy", false, false, ItemObservation{Result: ItemCopied, Effect: EffectReportedApplied}, false},
		{"unadmitted_failure", false, false, itemProblem(ItemFailed, StageEngine, CodeUnknown), false},
		{"unadmitted_conflict", false, false, itemProblem(ItemConflict, StageCommit, CodeConflict), false},
		{"failed_claims_applied", true, false, ItemObservation{Result: ItemFailed, Effect: EffectReportedApplied, Diagnostic: ItemDiagnostic{Stage: StageEngine, Code: CodeUnknown}}, false},
		{"same_claims_effect", true, false, ItemObservation{Result: ItemSame, Effect: EffectReportedApplied}, false},
		{"copied_claims_no_attempt", true, false, ItemObservation{Result: ItemCopied, Effect: EffectNotAttempted}, false},
		{"effect_absent", true, false, ItemObservation{Result: ItemCopied}, false},
		{"failed_without_reason", true, false, ItemObservation{Result: ItemFailed, Effect: EffectNotAttempted}, false},
		{"refused_without_reason", false, false, ItemObservation{Result: ItemRefused, Effect: EffectNotAttempted}, false},
		{"negative_copy_bytes", true, false, ItemObservation{Result: ItemCopied, Effect: EffectReportedApplied, CopiedBytes: -1}, false},
		{"same_with_bytes", true, false, ItemObservation{Result: ItemSame, Effect: EffectNotAttempted, CopiedBytes: 10}, false},
		{"verify_with_bytes", true, true, ItemObservation{Result: ItemVerified, Effect: EffectNotAttempted, CopiedBytes: 10}, false},
		{"verify_claims_effect", true, true, ItemObservation{Result: ItemVerified, Effect: EffectReportedApplied}, false},
		{"execution_claims_verified", true, false, ItemObservation{Result: ItemVerified, Effect: EffectNotAttempted}, false},
		{"verification_claims_copied", true, true, ItemObservation{Result: ItemCopied, Effect: EffectReportedApplied}, false},
		{"verification_failure_no_reason", false, true, ItemObservation{Result: ItemFailed, Effect: EffectNotAttempted}, false},
		{"unrecognised_result", true, false, ItemObservation{Result: "token=fixture", Effect: EffectNotAttempted}, false},
		{"unrecognised_effect", true, false, ItemObservation{Result: ItemCopied, Effect: "token=fixture"}, false},
		{"raw_stage", true, false, itemProblem(ItemFailed, "https://provider/?token=fixture", CodeUnknown), false},
		{"raw_code", true, false, itemProblem(ItemFailed, StageEngine, "token=fixture"), false},
		{"missing_stage", true, false, itemProblem(ItemFailed, "", CodeUnknown), false},
		{"missing_code", true, false, itemProblem(ItemFailed, StageUnknown, ""), false},
		{"status_without_diagnostic", true, false, ItemObservation{Result: ItemCopied, Effect: EffectReportedApplied, Diagnostic: ItemDiagnostic{HTTPStatus: 403}}, false},
		{"status_too_low", true, false, ItemObservation{Result: ItemFailed, Effect: EffectNotAttempted, Diagnostic: ItemDiagnostic{Stage: StageEngine, Code: CodeUnknown, HTTPStatus: 99}}, false},
		{"status_too_high", true, false, ItemObservation{Result: ItemFailed, Effect: EffectNotAttempted, Diagnostic: ItemDiagnostic{Stage: StageEngine, Code: CodeUnknown, HTTPStatus: 600}}, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			s, bookmark := newStore(t)
			job := recordedJob(t, s, TypeCopy)
			intent := transferIntent(bookmark, "name")
			intent.Admitted = test.admitted
			if err := s.AdmitItems(t.Context(), job.ID, []ItemIntent{intent}); err != nil {
				t.Fatal(err)
			}
			var err error
			if test.verification {
				err = s.RecordComparison(t.Context(), job.ID, intent.ItemIdentity, test.observation)
			} else {
				err = s.RecordExecution(t.Context(), job.ID, intent.Key, test.observation)
			}
			if test.valid {
				if err != nil {
					t.Fatal(err)
				}
			} else {
				if !errors.Is(err, ErrInvalidItemRecord) {
					t.Fatalf("invalid observation accepted: %v", err)
				}
				assertItemUnavailable(t, s, job.ID, err)
			}
			items := readAllItems(t, NewStore(s.db), job.ID, 0)
			if len(items) != 1 || !reflect.DeepEqual(items[0].ItemIntent, intent) {
				t.Fatalf("observation changed identity: %+v", items)
			}
			if !test.valid {
				if items[0].Execution != nil || items[0].Verification != nil {
					t.Fatalf("rejected observation survived: %+v", items[0])
				}
				return
			}
			observation := items[0].Execution
			if test.verification {
				observation = items[0].Verification
			}
			if observation == nil || observation.ItemObservation != test.observation {
				t.Fatalf("observation changed: %+v", observation)
			}
		})
	}
}

func TestItemObservationsDoNotReplaceFacts(t *testing.T) {
	for _, phase := range []string{"execution", "verification"} {
		t.Run(phase, func(t *testing.T) {
			s, bookmark := newStore(t)
			job := recordedJob(t, s, TypeCopy)
			intent := transferIntent(bookmark, "same-name")
			if err := s.AdmitItems(t.Context(), job.ID, []ItemIntent{intent}); err != nil {
				t.Fatal(err)
			}
			failure := itemProblem(ItemFailed, StageUnknown, CodeUnknown)
			record := func(observation ItemObservation) error {
				if phase == "verification" {
					return s.RecordComparison(t.Context(), job.ID, intent.ItemIdentity, observation)
				}
				return s.RecordExecution(t.Context(), job.ID, intent.Key, observation)
			}
			if err := record(failure); err != nil {
				t.Fatal(err)
			}
			before := readAllItems(t, s, job.ID, 0)
			if err := record(failure); err != nil {
				t.Fatalf("identical local persistence not idempotent: %v", err)
			}
			if got := readAllItems(t, s, job.ID, 0); !reflect.DeepEqual(got, before) {
				t.Fatal("idempotent observation replaced timestamp or facts")
			}
			changed := failure
			changed.Diagnostic.Code = CodeTimeout
			err := record(changed)
			if !errors.Is(err, ErrItemRecordConflict) {
				t.Fatalf("conflicting observation accepted: %v", err)
			}
			assertItemUnavailable(t, s, job.ID, err)
			if got := readAllItems(t, NewStore(s.db), job.ID, 0); !reflect.DeepEqual(got, before) {
				t.Fatal("conflicting observation erased earlier fact")
			}
		})
	}
}

func TestItemRefusalCannotBeVerifiedInEitherOrder(t *testing.T) {
	for _, first := range []string{"refusal", "verification"} {
		t.Run(first, func(t *testing.T) {
			s, bookmark := newStore(t)
			job := recordedJob(t, s, TypeCopy)
			intent := transferIntent(bookmark, "existing")
			intent.Admitted = false
			if err := s.AdmitItems(t.Context(), job.ID, []ItemIntent{intent}); err != nil {
				t.Fatal(err)
			}
			refuse := func() error {
				return s.RecordExecution(t.Context(), job.ID, intent.Key, itemProblem(ItemRefused, StagePlanning, CodeUnsupported))
			}
			verify := func() error {
				return s.RecordComparison(t.Context(), job.ID, intent.ItemIdentity, ItemObservation{Result: ItemVerified, Effect: EffectNotAttempted})
			}
			a, b := refuse, verify
			if first == "verification" {
				a, b = verify, refuse
			}
			if err := a(); err != nil {
				t.Fatal(err)
			}
			before := readAllItems(t, s, job.ID, 0)
			if err := b(); !errors.Is(err, ErrItemRecordConflict) {
				t.Fatalf("refused overwrite credited as verified: %v", err)
			}
			if got := readAllItems(t, NewStore(s.db), job.ID, 0); !reflect.DeepEqual(got, before) {
				t.Fatal("refusal incompatibility replaced earlier evidence")
			}
		})
	}
}

func TestItemComparisonCannotAdmitOrRewriteIdentity(t *testing.T) {
	for _, fault := range []string{"copy", "identity", "sql", "missing_execution"} {
		t.Run(fault, func(t *testing.T) {
			s, bookmark := newStore(t)
			job := recordedJob(t, s, TypeCopy)
			identity := transferIntent(bookmark, "comparison-only").ItemIdentity
			failure := itemProblem(ItemFailed, StageVerification, CodeMissing)
			if fault == "sql" {
				itemSQL(t, s, `CREATE TRIGGER refuse_observation BEFORE INSERT ON job_item_observations BEGIN SELECT RAISE(ABORT,'private token=fixture'); END`)
			} else if err := s.RecordComparison(t.Context(), job.ID, identity, failure); err != nil {
				t.Fatal(err)
			}
			before := readAllItems(t, s, job.ID, 0)
			var err error
			switch fault {
			case "copy":
				err = s.RecordExecution(t.Context(), job.ID, identity.Key, ItemObservation{Result: ItemCopied, Effect: EffectReportedApplied})
			case "identity":
				identity.Destination.BookmarkID = "another-account"
				err = s.RecordComparison(t.Context(), job.ID, identity, failure)
			case "sql":
				err = s.RecordComparison(t.Context(), job.ID, identity, failure)
			case "missing_execution":
				err = s.RecordExecution(t.Context(), job.ID, "never-admitted", ItemObservation{Result: ItemCopied, Effect: EffectReportedApplied})
			}
			assertItemUnavailable(t, s, job.ID, err)
			observer := NewStore(s.db)
			if got := readAllItems(t, observer, job.ID, 0); !reflect.DeepEqual(got, before) {
				t.Fatalf("comparison granted admission, changed identity or partially committed: %+v", got)
			}
			if fault != "sql" {
				got, err := observer.ReadItemTotals(t.Context(), job.ID)
				want := ItemTotals{Items: 1, Failed: 1, Problems: 1}
				if err != nil || got != want {
					t.Fatalf("comparison-only facts: %+v want=%+v %v", got, want, err)
				}
			}
		})
	}
}
