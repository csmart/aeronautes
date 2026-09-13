package systemtest

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/csmart/aeronautes/internal/app"
	"github.com/csmart/aeronautes/internal/audit"
	"github.com/csmart/aeronautes/internal/db"
	"github.com/csmart/aeronautes/internal/jobs"
)

type itemStorageState struct {
	Records     map[string][]jobs.ItemRecord
	Totals      map[string]jobs.ItemTotals
	Jobs        map[string]*app.JobDTO
	NotCaptured bool
}

type itemStorageRequest struct {
	Database, Output, Legacy, Pending string
	Recorded                          []string
}

type itemStorageReopened struct {
	Before, After itemStorageState
	Interrupted   int64
	Pruned        int64
}

func readItemStorage(t *testing.T, store *jobs.Store, request itemStorageRequest) itemStorageState {
	t.Helper()
	state := itemStorageState{Records: map[string][]jobs.ItemRecord{}, Totals: map[string]jobs.ItemTotals{}, Jobs: map[string]*app.JobDTO{}}
	service := app.NewJobService(store)
	for _, id := range request.Recorded {
		state.Records[id] = []jobs.ItemRecord{}
		var cursor int64
		for {
			page, err := store.ReadItems(t.Context(), id, cursor, 73)
			if err != nil {
				t.Fatal(err)
			}
			state.Records[id] = append(state.Records[id], page.Items...)
			if page.NextCursor == 0 {
				break
			}
			if page.NextCursor <= cursor || len(page.Items) != 73 {
				t.Fatal("item storage page made no progress")
			}
			cursor = page.NextCursor
		}
		totals, err := store.ReadItemTotals(t.Context(), id)
		if err != nil {
			t.Fatal(err)
		}
		state.Totals[id] = totals
		job, err := service.Get(t.Context(), id)
		if err != nil {
			t.Fatal(err)
		}
		state.Jobs[id] = job
	}
	if request.Legacy != "" {
		_, pageErr := store.ReadItems(t.Context(), request.Legacy, 0, 0)
		_, totalsErr := store.ReadItemTotals(t.Context(), request.Legacy)
		state.NotCaptured = errors.Is(pageErr, jobs.ErrItemRecordsNotCaptured) && errors.Is(totalsErr, jobs.ErrItemRecordsNotCaptured)
		if !state.NotCaptured {
			t.Fatalf("historical job presented as captured or unavailable: %v %v", pageErr, totalsErr)
		}
	}
	return state
}

// This exercises storage and the existing summary service, not transfer effects or crash durability.
func TestTransferItemStorageReopen(t *testing.T) {
	if input := os.Getenv("AERONAUTES_ITEM_STORAGE_REOPEN"); input != "" {
		data, err := os.ReadFile(input)
		if err != nil {
			t.Fatal(err)
		}
		var request itemStorageRequest
		if err := json.Unmarshal(data, &request); err != nil {
			t.Fatal(err)
		}
		database, err := db.Open(t.Context(), request.Database)
		if err != nil {
			t.Fatal(err)
		}
		defer database.Close()
		store := jobs.NewStore(database)
		result := itemStorageReopened{Before: readItemStorage(t, store, request)}
		result.Interrupted, err = store.FailInterrupted(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		result.Pruned, err = store.PruneTerminal(t.Context(), time.Now().Add(time.Hour))
		if err != nil {
			t.Fatal(err)
		}
		// Empty history follows existing pruning; compare retained item jobs below.
		request.Recorded = request.Recorded[:len(request.Recorded)-1]
		request.Legacy = ""
		result.After = readItemStorage(t, store, request)
		if err := database.Close(); err != nil {
			t.Fatal(err)
		}
		data, err = json.Marshal(result)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(request.Output, data, 0600); err != nil {
			t.Fatal(err)
		}
		return
	}
	dir := t.TempDir()
	request := itemStorageRequest{Database: filepath.Join(dir, "state.sqlite"), Output: filepath.Join(dir, "out.json")}
	database, err := db.Open(t.Context(), request.Database)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	store := jobs.NewStore(database)
	legacy, err := store.Create(t.Context(), jobs.Job{Type: jobs.TypeCopy})
	if err != nil {
		t.Fatal(err)
	}
	request.Legacy = legacy.ID
	for _, kind := range []jobs.Type{jobs.TypeCopy, jobs.TypeUpload, jobs.TypeDownload} {
		job, err := store.Create(t.Context(), jobs.Job{Type: kind, ItemRecordsVersion: 1})
		if err != nil {
			t.Fatal(err)
		}
		for _, state := range []jobs.State{jobs.StatePlanning, jobs.StateRunning, jobs.StateVerifying} {
			if err := store.Transition(t.Context(), job.ID, state); err != nil {
				t.Fatal(err)
			}
		}
		request.Recorded = append(request.Recorded, job.ID)
		count := 1
		if kind == jobs.TypeCopy {
			count = 401
		}
		var intents []jobs.ItemIntent
		for i := range count {
			key := fmt.Sprintf("f-%04d\nidentity", i)
			source := jobs.ItemLocation{Kind: jobs.ItemSwift, BookmarkID: "source", Container: "source", Object: "scope/" + key}
			destination := jobs.ItemLocation{Kind: jobs.ItemSwift, BookmarkID: "destination", Container: "destination", Object: "scope/" + key}
			if kind == jobs.TypeUpload {
				source = jobs.ItemLocation{Kind: jobs.ItemLocal, LocalPath: filepath.Join(dir, fmt.Sprint(i))}
			}
			if kind == jobs.TypeDownload {
				destination = jobs.ItemLocation{Kind: jobs.ItemLocal, LocalPath: filepath.Join(dir, fmt.Sprint(i))}
			}
			intents = append(intents, jobs.ItemIntent{ItemIdentity: jobs.ItemIdentity{Key: key, Source: source, Destination: destination}, Planned: true, Admitted: true})
		}
		if err := store.AdmitItems(t.Context(), job.ID, intents); err != nil {
			t.Fatal(err)
		}
		for _, intent := range intents {
			failure := jobs.ItemObservation{Result: jobs.ItemFailed, Effect: jobs.EffectNotAttempted, Diagnostic: jobs.ItemDiagnostic{Stage: jobs.StageSourceRead, Code: jobs.CodeForbidden, HTTPStatus: 403}}
			if err := store.RecordExecution(t.Context(), job.ID, intent.Key, failure); err != nil {
				t.Fatal(err)
			}
			failure.Diagnostic = jobs.ItemDiagnostic{Stage: jobs.StageVerification, Code: jobs.CodeMissing, HTTPStatus: 404}
			if err := store.RecordComparison(t.Context(), job.ID, intent.ItemIdentity, failure); err != nil {
				t.Fatal(err)
			}
		}
		if kind == jobs.TypeDownload {
			request.Pending = job.ID
			missing := intents[0]
			missing.Key = "missing-outcome"
			missing.Source.Object = "scope/missing-outcome"
			if err := store.AdmitItems(t.Context(), job.ID, []jobs.ItemIntent{missing}); err != nil {
				t.Fatal(err)
			}
		} else {
			result := jobs.Completion{State: jobs.StatePartial, Counters: jobs.Counters{ObjectsFailed: 9999}, Error: "retained failures", Audit: audit.Event{Operation: "transfer.storage.partial", Resource: "job " + job.ID, Result: audit.ResultVerifyMismatch}}
			if err := store.FinishFromItems(t.Context(), job.ID, result, audit.NewStore(database)); err != nil {
				t.Fatal(err)
			}
			visible, err := app.NewJobService(store).Get(t.Context(), job.ID)
			if err != nil || visible.Counters.ObjectsFailed != int64(count) {
				t.Fatalf("existing job service lost exact terminal total: %+v %v", visible, err)
			}
		}
	}
	empty, err := store.Create(t.Context(), jobs.Job{Type: jobs.TypeCopy, ItemRecordsVersion: 1})
	if err != nil {
		t.Fatal(err)
	}
	request.Recorded = append(request.Recorded, empty.ID)
	want := readItemStorage(t, store, request)
	if len(want.Records[empty.ID]) != 0 || want.Totals[request.Pending].Failed != 1 || want.Totals[request.Pending].Unresolved != 1 {
		t.Fatalf("bad setup: %+v", want.Totals)
	}
	if err := database.WriteTx(t.Context(), func(tx *sql.Tx) error {
		_, err := tx.ExecContext(t.Context(), `CREATE TRIGGER refuse_item_reopen BEFORE INSERT ON job_item_observations
			WHEN NEW.item_id IN (SELECT id FROM job_items WHERE item_key='missing-outcome')
			BEGIN SELECT RAISE(ABORT,'synthetic observation refusal'); END`)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.RecordExecution(t.Context(), request.Pending, "missing-outcome", jobs.ItemObservation{Result: jobs.ItemCopied, Effect: jobs.EffectReportedApplied}); !errors.Is(err, jobs.ErrItemRecordsUnavailable) {
		t.Fatalf("required observation refusal hidden: %v", err)
	}
	if page, err := store.ReadItems(t.Context(), request.Pending, 0, 0); err == nil || page.Items != nil {
		t.Fatalf("failed recording presented as available: %+v %v", page, err)
	}
	// Removing an owned SQL refusal models available storage on orderly reopen.
	if err := database.WriteTx(t.Context(), func(tx *sql.Tx) error {
		_, err := tx.ExecContext(t.Context(), `DROP TRIGGER refuse_item_reopen`)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	input := filepath.Join(dir, "input.json")
	if err := os.WriteFile(input, data, 0600); err != nil {
		t.Fatal(err)
	}
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	child := exec.CommandContext(ctx, self, "-test.run=^TestTransferItemStorageReopen$", "-test.timeout=25s")
	child.Env = append(os.Environ(), "AERONAUTES_ITEM_STORAGE_REOPEN="+input)
	if output, err := child.CombinedOutput(); err != nil {
		t.Fatalf("item storage reopen: %v\n%s", err, output)
	}
	data, err = os.ReadFile(request.Output)
	if err != nil {
		t.Fatal(err)
	}
	var got itemStorageReopened
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got.Before, want) || got.Interrupted != 3 || got.Pruned != 2 {
		t.Fatalf("orderly reopen changed exact facts: before-equal=%v interrupted=%d pruned=%d", reflect.DeepEqual(got.Before, want), got.Interrupted, got.Pruned)
	}
	delete(want.Records, empty.ID)
	delete(want.Totals, empty.ID)
	delete(want.Jobs, empty.ID)
	if !reflect.DeepEqual(got.After.Records, want.Records) || !reflect.DeepEqual(got.After.Totals, want.Totals) {
		t.Fatal("interruption or pruning changed item records or exact facets")
	}
	for id, job := range want.Jobs {
		actual := got.After.Jobs[id]
		if id == request.Pending {
			counters := job.Counters
			counters.ObjectsFailed = got.After.Totals[id].Failed
			if actual.State != "failed" || actual.FinishedAt == "" || actual.Counters != counters || actual.Error == "" {
				t.Fatalf("restart invented terminal counter projection: %+v", actual)
			}
		} else if !reflect.DeepEqual(actual, job) {
			t.Fatalf("restart replaced completed job: %+v", actual)
		}
	}
}
