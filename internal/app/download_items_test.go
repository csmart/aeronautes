package app

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/csmart/aeronautes/internal/jobs"
	transfercopy "github.com/csmart/aeronautes/internal/transfer/copy"
	"github.com/csmart/aeronautes/internal/transfer/local"
)

func TestDownloadItemAdapterRejectsInvalidIdentityBeforeWrite(t *testing.T) {
	for _, phase := range []string{"admit", "execute", "verify"} {
		for _, fault := range []string{"key", "path", "relative_root", "source", "container", "bookmark", "bytes", "prefix", "relative", "dot", "empty_component", "directory_size", "directory_name"} {
			t.Run(phase+"_"+fault, func(t *testing.T) {
				h := newHarness(t)
				job, err := h.transfer.jobs.Create(t.Context(), jobs.Job{Type: jobs.TypeDownload, ItemRecordsVersion: 1})
				if err != nil {
					t.Fatal(err)
				}
				root := t.TempDir()
				req := DownloadRequestDTO{BookmarkID: "captured", Container: "media", Prefix: "scope/"}
				admission := local.DownloadAdmission{Item: local.DownloadItem{Name: "scope/source", RelPath: "source", Size: 5}}
				switch fault {
				case "key":
					admission.Item.Name = "scope/invalid\xff"
				case "path":
					admission.Item.RelPath += "\xff"
				case "relative_root":
					root = "relative"
				case "source":
					admission.Item.Name = ""
				case "container":
					req.Container = ""
				case "bookmark":
					req.BookmarkID = ""
				case "bytes":
					admission.Item.Size = -1
				case "prefix":
					req.Prefix = "other/"
				case "relative":
					admission.Item.RelPath = "another"
				case "dot":
					admission.Item.Name, admission.Item.RelPath = "scope/nested/../source", "nested/../source"
				case "empty_component":
					admission.Item.Name, admission.Item.RelPath = "scope/nested//source", "nested//source"
				case "directory_size":
					admission.Item.Name, admission.Item.Dir = "scope/source/", true
				case "directory_name":
					admission.Item.Dir, admission.Item.Size = true, 0
				}
				if err := h.db.WriteTx(t.Context(), func(tx *sql.Tx) error {
					for _, table := range []string{"job_items", "job_item_observations", "job_item_publications"} {
						if _, err := tx.Exec("CREATE TRIGGER reject_" + table + " BEFORE INSERT ON " + table + " BEGIN SELECT RAISE(ABORT,'fixture must not write'); END"); err != nil {
							return err
						}
					}
					return nil
				}); err != nil {
					t.Fatal(err)
				}
				recorder := h.transfer.downloadItemRecorder(t.Context(), job.ID, req, root)
				switch phase {
				case "admit":
					err = recorder.Admit(admission)
				case "execute":
					err = recorder.Execute(admission.Item, transfercopy.Observation{Result: transfercopy.ResultNotAttempted, Effect: transfercopy.EffectNotAttempted})
				case "verify":
					err = recorder.Verify(admission.Item, transfercopy.Observation{Result: transfercopy.ResultVerified, Effect: transfercopy.EffectNotAttempted})
				}
				if !errors.Is(err, jobs.ErrInvalidItemRecord) || h.transfer.jobs.Available() != nil {
					t.Fatalf("invalid download representation reached storage or was accepted: %v latch=%v", err, h.transfer.jobs.Available())
				}
				page, err := h.transfer.jobs.ReadItems(t.Context(), job.ID, 0, 100)
				if err != nil || len(page.Items) != 0 {
					t.Fatalf("invalid download representation changed retained facts: %+v %v", page, err)
				}
			})
		}
	}
}

func TestDownloadItemRecordingCost(t *testing.T) {
	for _, native := range []bool{false, true} {
		t.Run(fmt.Sprint(native), func(t *testing.T) {
			h := newHarness(t)
			store := h.transfer.jobs
			job, err := store.Create(t.Context(), jobs.Job{Type: jobs.TypeDownload, ItemRecordsVersion: 1})
			if err != nil {
				t.Fatal(err)
			}
			for _, state := range []jobs.State{jobs.StatePlanning, jobs.StateRunning, jobs.StateVerifying} {
				if err := store.Transition(t.Context(), job.ID, state); err != nil {
					t.Fatal(err)
				}
			}
			root := t.TempDir()
			req := DownloadRequestDTO{BookmarkID: "captured", Container: "media", Prefix: "scope\n/"}
			recorder := h.transfer.downloadItemRecorder(t.Context(), job.ID, req, root)
			const count = 201
			started := time.Now()
			for i := range count {
				name := fmt.Sprintf("source-%04d", i)
				item := local.DownloadItem{Name: req.Prefix + name, RelPath: name, Size: 5}
				if native {
					item.Route = "native"
				}
				if err := recorder.Admit(local.DownloadAdmission{Item: item}); err != nil {
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
			h.transfer.finishLocal(t.Context(), job.ID, "captured", counters, jobs.StateComplete, "", "")
			if totals, err := store.ReadItemTotals(t.Context(), job.ID); err != nil || totals != (jobs.ItemTotals{Items: count, Planned: count, Admitted: count}) {
				t.Fatalf("recording lost the complete admitted scope: %+v %v", totals, err)
			}
			if finished, err := store.Get(t.Context(), job.ID); err != nil || finished.State != jobs.StateComplete || finished.Counters != counters {
				t.Fatalf("recording cost control did not finish: %+v %v", finished, err)
			}
			writes := count * 3
			encoded, err := json.Marshal(map[string]any{"native": native, "names": count, "acknowledged_writes": writes, "recording_ms": float64(recording) / float64(time.Millisecond), "completion_ms": float64(time.Since(started)-recording) / float64(time.Millisecond)})
			if err != nil {
				t.Fatal(err)
			}
			t.Logf("download_record_cost=%s", encoded)
		})
	}
}

func TestDownloadItemFailedNameCountsBothPhasesOnce(t *testing.T) {
	h := newHarness(t)
	store := h.transfer.jobs
	job, err := store.Create(t.Context(), jobs.Job{Type: jobs.TypeDownload, ItemRecordsVersion: 1})
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	req := DownloadRequestDTO{BookmarkID: "captured", Container: "media", Prefix: "scope\n/"}
	item := local.DownloadItem{Name: req.Prefix + "source", RelPath: "source", Size: 5, Route: "native"}
	recorder := h.transfer.downloadItemRecorder(t.Context(), job.ID, req, root)
	if err := recorder.Admit(local.DownloadAdmission{Item: item}); err != nil {
		t.Fatal(err)
	}
	for _, observation := range []struct {
		record func(local.DownloadItem, transfercopy.Observation) error
		value  transfercopy.Observation
	}{
		{recorder.Execute, transfercopy.Observation{Result: transfercopy.ResultFailed, Effect: transfercopy.EffectUnresolved, Diagnostic: transfercopy.Diagnostic{Stage: transfercopy.StageSourceRead, Code: transfercopy.CodeIO}}},
		{recorder.Verify, transfercopy.Observation{Result: transfercopy.ResultFailed, Effect: transfercopy.EffectNotAttempted, Diagnostic: transfercopy.Diagnostic{Stage: transfercopy.StageVerification, Code: transfercopy.CodeUnverified}}},
	} {
		if err := observation.record(item, observation.value); err != nil {
			t.Fatal(err)
		}
	}
	if totals, err := store.ReadItemTotals(t.Context(), job.ID); err != nil || totals != (jobs.ItemTotals{Items: 1, Planned: 1, Admitted: 1, Failed: 1, Unresolved: 1, Problems: 1}) {
		t.Fatalf("one failed name counted its independent read and close phases twice: %+v %v", totals, err)
	}
	page, err := store.ReadItems(t.Context(), job.ID, 0, 100)
	if err != nil || len(page.Items) != 1 || page.Items[0].Execution == nil || page.Items[0].Verification == nil ||
		page.Items[0].Execution.Diagnostic.Stage != jobs.StageSourceRead || page.Items[0].Verification.Diagnostic.Stage != jobs.StageVerification {
		t.Fatalf("distinct phase observations were collapsed: %+v %v", page, err)
	}
}
