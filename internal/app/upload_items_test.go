package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/csmart/aeronautes/internal/jobs"
	"github.com/csmart/aeronautes/internal/swiftcontrol"
	transfercopy "github.com/csmart/aeronautes/internal/transfer/copy"
	"github.com/csmart/aeronautes/internal/transfer/local"
	"github.com/csmart/aeronautes/internal/transfer/rclone"
)

func TestUploadItemRecordingCost(t *testing.T) {
	h := newHarness(t)
	store := h.transfer.jobs
	job, err := store.Create(t.Context(), jobs.Job{Type: jobs.TypeUpload, ItemRecordsVersion: 1})
	if err != nil {
		t.Fatal(err)
	}
	for _, state := range []jobs.State{jobs.StatePlanning, jobs.StateRunning, jobs.StateVerifying} {
		if err := store.Transition(t.Context(), job.ID, state); err != nil {
			t.Fatal(err)
		}
	}
	recorder := h.transfer.uploadItemRecorder(t.Context(), job.ID, UploadRequestDTO{BookmarkID: "captured", Container: "media"}, answeredFragments{})
	const count = 201
	root := t.TempDir()
	started := time.Now()
	for i := range count {
		name := fmt.Sprintf("source-%04d", i)
		item := local.UploadItem{Path: filepath.Join(root, name), Name: "live/" + name, Size: 5}
		if err := recorder.Admit(local.UploadAdmission{Item: item, StagingObject: "stage/" + name}); err != nil {
			t.Fatal(err)
		}
		if err := recorder.Execute(item, transfercopy.Observation{Result: transfercopy.ResultCopied, Effect: transfercopy.EffectReportedApplied, CopiedBytes: 5}); err != nil {
			t.Fatal(err)
		}
		if err := recorder.Verify(item, transfercopy.Observation{Result: transfercopy.ResultVerified, Effect: transfercopy.EffectNotAttempted}); err != nil {
			t.Fatal(err)
		}
	}
	recording := time.Since(started)
	counters := jobs.Counters{ObjectsPlanned: count, BytesPlanned: count * 5, ObjectsCopied: count, ObjectsVerified: count, BytesCopied: count * 5}
	h.transfer.finishLocal(t.Context(), job.ID, "captured", 1, counters, jobs.StateComplete, "", "")
	if totals, err := store.ReadItemTotals(t.Context(), job.ID); err != nil || totals != (jobs.ItemTotals{Items: count, Planned: count, Admitted: count}) {
		t.Fatalf("recording lost the complete admitted scope: %+v %v", totals, err)
	}
	if finished, err := store.Get(t.Context(), job.ID); err != nil || finished.State != jobs.StateComplete || finished.Counters != counters {
		t.Fatalf("recording cost control did not finish: %+v %v", finished, err)
	}
	encoded, err := json.Marshal(map[string]any{"names": count, "acknowledged_writes": count * 3, "recording_ms": float64(recording) / float64(time.Millisecond)})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("upload_record_cost=%s", encoded)
}

func TestUploadItemAdapterRejectsInvalidIdentityBeforeWrite(t *testing.T) {
	for _, phase := range []string{"admit", "execute", "verify"} {
		for _, fault := range []string{"key", "path", "relative", "destination", "container", "bookmark", "bytes", "staging"} {
			if phase != "admit" && (fault == "bytes" || fault == "staging") {
				continue
			}
			t.Run(phase+"_"+fault, func(t *testing.T) {
				h := newHarness(t)
				job, err := h.transfer.jobs.Create(t.Context(), jobs.Job{Type: jobs.TypeUpload, ItemRecordsVersion: 1})
				if err != nil {
					t.Fatal(err)
				}
				req := UploadRequestDTO{BookmarkID: "captured", Container: "media"}
				admission := local.UploadAdmission{Item: local.UploadItem{Path: filepath.Join(t.TempDir(), "source"), Name: "scope/source", Size: 5}, StagingObject: "stage/source"}
				switch fault {
				case "key":
					admission.Item.Name = "invalid\xff"
				case "path":
					admission.Item.Path += "\xff"
				case "relative":
					admission.Item.Path = "relative"
				case "destination":
					admission.Item.Name = ""
				case "container":
					req.Container = ""
				case "bookmark":
					req.BookmarkID = ""
				case "bytes":
					admission.Item.Size = -1
				case "staging":
					admission.StagingObject = "invalid\xff"
				}
				if err := h.db.WriteTx(t.Context(), func(tx *sql.Tx) error {
					for _, table := range []string{"job_items", "job_item_observations"} {
						if _, err := tx.Exec("CREATE TRIGGER reject_" + table + " BEFORE INSERT ON " + table + " BEGIN SELECT RAISE(ABORT,'fixture must not write'); END"); err != nil {
							return err
						}
					}
					return nil
				}); err != nil {
					t.Fatal(err)
				}
				recorder := h.transfer.uploadItemRecorder(t.Context(), job.ID, req, answeredFragments{})
				switch phase {
				case "admit":
					err = recorder.Admit(admission)
				case "execute":
					err = recorder.Execute(admission.Item, transfercopy.Observation{Result: transfercopy.ResultNotAttempted, Effect: transfercopy.EffectNotAttempted})
				case "verify":
					err = recorder.Verify(admission.Item, transfercopy.Observation{Result: transfercopy.ResultVerified, Effect: transfercopy.EffectNotAttempted})
				}
				if !errors.Is(err, jobs.ErrInvalidItemRecord) || h.transfer.jobs.Available() != nil {
					t.Fatalf("invalid upload representation reached storage or was accepted: %v latch=%v", err, h.transfer.jobs.Available())
				}
				items, err := h.transfer.jobs.ReadItems(t.Context(), job.ID, 0, 100)
				if err != nil || len(items.Items) != 0 {
					t.Fatalf("invalid upload representation changed retained facts: %+v %v", items, err)
				}
			})
		}
	}
}

func TestUploadItemDiagnosticsExcludeProviderText(t *testing.T) {
	for _, mode := range []string{"engine_error", "engine_status", "typed_swift", "typed_timeout", "network", "cancelled"} {
		t.Run(mode, func(t *testing.T) {
			h := newHarness(t)
			h.connect(t)
			bookmark := h.transfer.connections.ActiveBookmarkID()
			h.mock.AddContainer("media", "standard")
			path := filepath.Join(t.TempDir(), "source")
			if err := os.WriteFile(path, []byte("bytes"), 0600); err != nil {
				t.Fatal(err)
			}
			h.transfer.setFilePicker(fakePicker{files: []string{path}})
			picked, err := h.transfer.PickUploadFiles(t.Context(), SelectionScopeDTO{BookmarkID: bookmark, Container: "media"})
			if err != nil {
				t.Fatal(err)
			}
			engine := copyDiagnosticEngine{err: errors.New(copyHostileDiagnostic)}
			want := jobs.ItemDiagnostic{Stage: jobs.StageEngine, Code: jobs.CodeUnknown}
			switch mode {
			case "engine_status":
				engine.err, engine.result = nil, &rclone.Result{ExitCode: 7, Errors: []string{copyHostileDiagnostic}}
			case "typed_swift":
				engine.err = &swiftcontrol.SwiftError{Kind: swiftcontrol.KindForbidden, HTTPStatus: 403, Message: copyHostileDiagnostic, TransID: "fixture-private-header"}
				want.Code, want.HTTPStatus = jobs.CodeForbidden, 403
			case "typed_timeout":
				engine.err = &swiftcontrol.SwiftError{Kind: swiftcontrol.KindTimeout, HTTPStatus: 504, Message: copyHostileDiagnostic}
				want.Code, want.HTTPStatus = jobs.CodeTimeout, 504
			case "network":
				engine.err = &net.OpError{Op: "write", Net: "tcp", Err: errors.New(copyHostileDiagnostic)}
				want.Code = jobs.CodeIO
			case "cancelled":
				engine.err = errors.Join(errors.New(copyHostileDiagnostic), context.Canceled)
				want.Code = jobs.CodeCancelled
			}
			h.transfer.setEngineFactory(func(transfercopy.RemoteSource, rclone.Options) (transfercopy.Engine, error) { return engine, nil })
			id, err := h.transfer.StartUpload(t.Context(), UploadRequestDTO{BookmarkID: bookmark, Container: "media", SelectionToken: picked.Token})
			if err != nil {
				t.Fatal(err)
			}
			job := awaitTerminal(t, h, id)
			page, err := h.transfer.jobs.ReadItems(t.Context(), id, 0, 100)
			if err != nil || len(page.Items) != 1 || job.Counters.ObjectsFailed != 1 || page.Items[0].Execution == nil || page.Items[0].Verification != nil ||
				page.Items[0].Execution.ItemObservation != (jobs.ItemObservation{Result: jobs.ItemFailed, Effect: jobs.EffectUnresolved, Diagnostic: want}) {
				t.Fatalf("upload inferred diagnostic text or invented verification: job=%+v page=%+v error=%v", job, page, err)
			}
			public, err := h.jobsSvc.Items(t.Context(), h.bookmarkID(), id, "", 100)
			if err != nil || len(public.Items) != 1 || public.Items[0].Source.LocalPath != path || public.Items[0].Verification != nil || public.Items[0].Execution == nil || public.Items[0].Execution.Code != string(want.Code) {
				t.Fatalf("upload public diagnostic changed: %+v %v", public, err)
			}
			for _, value := range []any{page, public} {
				encoded, err := json.Marshal(value)
				if err != nil {
					t.Fatal(err)
				}
				for _, secret := range []string{"fixture private", "script", "copy-secret", "fixture.invalid", "password", "fixture-private-header", "HTTP 403"} {
					if strings.Contains(string(encoded), secret) {
						t.Fatalf("provider text entered upload items or DTO: %s", encoded)
					}
				}
			}
		})
	}
}
