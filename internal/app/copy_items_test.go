package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/csmart/aeronautes/internal/audit"
	"github.com/csmart/aeronautes/internal/jobs"
	"github.com/csmart/aeronautes/internal/swiftcontrol"
	transfercopy "github.com/csmart/aeronautes/internal/transfer/copy"
	"github.com/csmart/aeronautes/internal/transfer/plan"
	"github.com/csmart/aeronautes/internal/transfer/rclone"
)

func TestCopyItemAdapterRejectsInvalidIdentityBeforeWrite(t *testing.T) {
	for _, fault := range []string{"key", "source", "destination", "container", "bytes"} {
		t.Run(fault, func(t *testing.T) {
			h := newHarness(t)
			store := h.transfer.jobs
			job, err := store.Create(t.Context(), jobs.Job{Type: jobs.TypeCopy, ItemRecordsVersion: 1})
			if err != nil {
				t.Fatal(err)
			}
			req := CopyRequestDTO{SourceBookmarkID: "source", SourceContainer: "c", SourcePrefix: "scope/", DestBookmarkID: "dest", DestContainer: "c", DestPrefix: "scope/"}
			admission := transfercopy.Admission{Item: plan.Item{Name: "name", SourceBytes: 10, Verdict: plan.VerdictNew, Route: plan.RouteRclone}}
			switch fault {
			case "key":
				admission.Item.Name = "invalid\xff"
			case "source":
				req.SourcePrefix, admission.Item.Name = "", ""
			case "destination":
				req.DestPrefix, admission.Item.Name = "", ""
			case "container":
				req.SourceContainer = ""
			case "bytes":
				admission.Item.SourceBytes = -1
			}
			if err := h.db.WriteTx(t.Context(), func(tx *sql.Tx) error {
				_, err := tx.Exec(`CREATE TRIGGER reject_any_admission BEFORE INSERT ON job_items BEGIN SELECT RAISE(ABORT,'fixture must not write'); END`)
				return err
			}); err != nil {
				t.Fatal(err)
			}
			err = h.transfer.copyItemRecorder(t.Context(), job.ID, req).Admit(admission)
			if err == nil || store.Available() != nil {
				t.Fatalf("invalid representation reached persistence or was accepted: %v latch=%v", err, store.Available())
			}
			page, err := store.ReadItems(t.Context(), job.ID, 0, 100)
			if err != nil || len(page.Items) != 0 {
				t.Fatalf("invalid admission changed records: %+v %v", page, err)
			}
		})
	}
}

const copyHostileDiagnostic = "fixture private <script> token=copy-secret https://user:password@fixture.invalid/?token=copy-secret HTTP 403 Forbidden"

type copyDiagnosticEngine struct {
	result *rclone.Result
	err    error
}

func (e copyDiagnosticEngine) Copy(context.Context, string, string, []string, func(rclone.Event)) (*rclone.Result, error) {
	return e.result, e.err
}

func (e copyDiagnosticEngine) CopyTo(context.Context, string, string, func(rclone.Event)) (*rclone.Result, error) {
	return nil, errors.New("unexpected CopyTo invocation")
}

func TestCopyItemDiagnosticsExcludeProviderText(t *testing.T) {
	for _, mode := range []string{"engine_status", "engine_error", "typed_swift"} {
		t.Run(mode, func(t *testing.T) {
			h := newHarness(t)
			h.connect(t)
			source := h.transfer.connections.ActiveBookmarkID()
			dest, destination := secondCluster(t, h)
			h.mock.AddContainer("media", "standard")
			destination.AddContainer("backup", "standard")
			seedObject(h.mock, "media", "name", "copy bytes", nil)
			engine := copyDiagnosticEngine{err: errors.New(copyHostileDiagnostic)}
			want := jobs.ItemDiagnostic{Stage: jobs.StageEngine, Code: jobs.CodeUnknown}
			if mode == "engine_status" {
				engine.err = nil
				engine.result = &rclone.Result{ExitCode: 7, Errors: []string{copyHostileDiagnostic}}
			} else if mode == "typed_swift" {
				engine.err = &swiftcontrol.SwiftError{Kind: swiftcontrol.KindForbidden, HTTPStatus: 403, Message: copyHostileDiagnostic, TransID: "fixture-private-header"}
				want.Code, want.HTTPStatus = jobs.CodeForbidden, 403
			}
			h.transfer.setEngineFactory(func(transfercopy.RemoteSource, rclone.Options) (transfercopy.Engine, error) { return engine, nil })
			id, err := h.transfer.StartCopy(t.Context(), CopyRequestDTO{SourceBookmarkID: source, SourceContainer: "media", DestBookmarkID: dest, DestContainer: "backup"})
			if err != nil {
				t.Fatal(err)
			}
			job := awaitTerminal(t, h, id)
			page, err := h.transfer.jobs.ReadItems(t.Context(), id, 0, 100)
			if err != nil || len(page.Items) != 1 || job.Counters.ObjectsFailed != 1 || page.Items[0].Execution == nil || page.Items[0].Verification != nil ||
				page.Items[0].Execution.Diagnostic != want || page.Items[0].Execution.Effect != jobs.EffectUnresolved || page.Items[0].Execution.Result != jobs.ItemFailed {
				t.Fatalf("actual Copy inferred diagnostics from arbitrary text or lost typed facts: job=%+v rows=%+v %v", job, page, err)
			}
			public, err := h.jobsSvc.Items(t.Context(), h.bookmarkID(), id, "", 100)
			if err != nil || len(public.Items) != 1 || !reflect.DeepEqual(public.Items[0], jobItemDTO(page.Items[0])) {
				t.Fatalf("typed Copy diagnostic changed at the bridge: %+v %v", public, err)
			}
			for _, value := range []any{page, public} {
				encoded, err := json.Marshal(value)
				if err != nil {
					t.Fatal(err)
				}
				for _, secret := range []string{"fixture private", "script", "copy-secret", "fixture.invalid", "password", "fixture-private-header", "HTTP 403"} {
					if strings.Contains(string(encoded), secret) {
						t.Fatalf("provider text entered persisted items or DTO: %s", encoded)
					}
				}
			}
		})
	}
}

func TestCopyItemRecordingCost(t *testing.T) {
	h := newHarness(t)
	store := h.transfer.jobs
	job, err := store.Create(t.Context(), jobs.Job{Type: jobs.TypeCopy, ItemRecordsVersion: 1})
	if err != nil {
		t.Fatal(err)
	}
	for _, state := range []jobs.State{jobs.StatePlanning, jobs.StateRunning, jobs.StateVerifying} {
		if err := store.Transition(t.Context(), job.ID, state); err != nil {
			t.Fatal(err)
		}
	}
	req := CopyRequestDTO{SourceBookmarkID: "source", SourceContainer: "source", DestBookmarkID: "dest", DestContainer: "dest"}
	recorder := h.transfer.copyItemRecorder(t.Context(), job.ID, req)
	const count = 201
	started := time.Now()
	for i := range count {
		key := fmt.Sprintf("same-%04d", i)
		if err := recorder.Admit(transfercopy.Admission{Item: plan.Item{Name: key, SourceBytes: 10, Verdict: plan.VerdictSame}}); err != nil {
			t.Fatal(err)
		}
		if err := recorder.Execute(key, transfercopy.Observation{Result: transfercopy.ResultSame, Effect: transfercopy.EffectNotAttempted}); err != nil {
			t.Fatal(err)
		}
		if err := recorder.Verify(key, transfercopy.Observation{Result: transfercopy.ResultVerified, Effect: transfercopy.EffectNotAttempted}); err != nil {
			t.Fatal(err)
		}
	}
	recording := time.Since(started)
	if err := store.FinishFromItems(t.Context(), job.ID, jobs.Completion{State: jobs.StateComplete, Counters: jobs.Counters{ObjectsVerified: count},
		Audit: audit.Event{Operation: "transfer.copy.complete", Resource: "owned fixture", Result: audit.ResultApplied}}, audit.NewStore(h.db)); err != nil {
		t.Fatal(err)
	}
	if totals, err := store.ReadItemTotals(t.Context(), job.ID); err != nil || totals != (jobs.ItemTotals{Items: count}) {
		t.Fatalf("same-name recording did not retain the complete scope: %+v %v", totals, err)
	}
	var guardMS []float64
	for range 5 {
		started = time.Now()
		if err := h.transfer.checkRerunItems(t.Context(), job.ID); err != nil {
			t.Fatal(err)
		}
		guardMS = append(guardMS, float64(time.Since(started))/float64(time.Millisecond))
	}
	encoded, err := json.Marshal(map[string]any{"names": count, "acknowledged_writes": count * 3, "recording_ms": float64(recording) / float64(time.Millisecond), "complete_rerun_guard_ms": guardMS})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("copy_record_cost=%s", encoded)
}

func TestCopyItemAdapterPreservesExactIdentities(t *testing.T) {
	for _, verdict := range []plan.Verdict{plan.VerdictNew, plan.VerdictUnknown, plan.VerdictChanged, plan.VerdictSame, "comparison_only"} {
		t.Run(string(verdict), func(t *testing.T) {
			h := newHarness(t)
			bm := h.ensureBookmark(t)
			job, err := h.transfer.jobs.Create(t.Context(), jobs.Job{Type: jobs.TypeCopy, ItemRecordsVersion: 1, SourceBookmarkID: bm, DestBookmarkID: bm})
			if err != nil {
				t.Fatal(err)
			}
			req := CopyRequestDTO{SourceBookmarkID: "source", SourceContainer: "source-c", SourcePrefix: "source/", DestBookmarkID: "dest", DestContainer: "dest-c", DestPrefix: "dest/"}
			recorder := h.transfer.copyItemRecorder(t.Context(), job.ID, req)
			keys := []string{"", "a\nname", "zero\x00name", "snow☃", "replacement�", "a%2Fb"}
			for _, key := range keys {
				if verdict != "comparison_only" {
					if err := recorder.Admit(transfercopy.Admission{Item: plan.Item{Name: key, SourceBytes: 10, Verdict: verdict, Route: plan.RouteNative}}); err != nil {
						t.Fatal(err)
					}
				}
				if err := recorder.Verify(key, transfercopy.Observation{Result: transfercopy.ResultVerified, Effect: transfercopy.EffectNotAttempted}); err != nil {
					t.Fatal(err)
				}
			}
			page, err := h.transfer.jobs.ReadItems(t.Context(), job.ID, 0, 100)
			if err != nil || len(page.Items) != len(keys) {
				t.Fatalf("lost exact identities: %+v %v", page, err)
			}
			public, err := h.jobsSvc.Items(t.Context(), bm, job.ID, "", 100)
			if err != nil || len(public.Items) != len(keys) {
				t.Fatalf("lost public identities: %+v %v", public, err)
			}
			for i, item := range page.Items {
				key := keys[i]
				planned := verdict != plan.VerdictSame && verdict != "comparison_only"
				admitted := verdict == plan.VerdictNew || verdict == plan.VerdictUnknown
				if item.Key != key || item.Source.Object != "source/"+key || item.Source.BookmarkID != "source" || item.Source.Container != "source-c" ||
					item.Destination.Object != "dest/"+key || item.Destination.BookmarkID != "dest" || item.Destination.Container != "dest-c" ||
					item.Planned != planned || item.Admitted != admitted || item.Staging != nil || item.Execution != nil || item.Verification == nil ||
					item.Verification.Result != jobs.ItemVerified || !reflect.DeepEqual(public.Items[i], jobItemDTO(item)) || public.Items[i].Key != key {
					t.Fatalf("adapter normalised identity or invented effects: %+v %+v", item, public.Items[i])
				}
			}
		})
	}
}
