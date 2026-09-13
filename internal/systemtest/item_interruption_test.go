package systemtest

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/csmart/aeronautes/internal/audit"
	"github.com/csmart/aeronautes/internal/db"
	"github.com/csmart/aeronautes/internal/jobs"
)

type interruptionSnapshot struct {
	Items    itemStorageState
	Recovery []jobs.RecoveryRecord
	Audit    []audit.Event
}

type interruptionReopened struct {
	Before, After         interruptionSnapshot
	Interrupted, Repeated int64
}

func readInterruption(t *testing.T, database *db.DB, store *jobs.Store, request itemStorageRequest) interruptionSnapshot {
	t.Helper()
	events, err := audit.NewStore(database).List(t.Context(), audit.Query{Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	return interruptionSnapshot{Items: readItemStorage(t, store, request), Recovery: uploadRecoveryRows(t, database), Audit: events}
}

func TestInterruptedItemRecordsReopen(t *testing.T) {
	if input := os.Getenv("AERONAUTES_INTERRUPTED_ITEMS_REOPEN"); input != "" {
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
		result := interruptionReopened{Before: readInterruption(t, database, store, request)}
		result.Interrupted, err = store.FailInterrupted(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		result.Repeated, err = store.FailInterrupted(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		result.After = readInterruption(t, database, store, request)
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
	saved := jobs.Counters{ObjectsPlanned: 503, ObjectsCopied: 37, ObjectsVerified: 29, ObjectsFailed: 400, BytesPlanned: 708, BytesCopied: 91}
	legacy, err := store.Create(t.Context(), jobs.Job{Type: jobs.TypeCopy, Counters: saved})
	if err != nil {
		t.Fatal(err)
	}
	request.Legacy = legacy.ID
	expectedFailures := map[string]int64{}
	// These are acknowledged logical facts, not claims of actual caller opt-in
	// or remote effects. No connections or native processes exist in this test.
	for _, count := range []int{1, 201, 401} {
		job, err := store.Create(t.Context(), jobs.Job{Type: jobs.TypeCopy, ItemRecordsVersion: 1, Counters: saved})
		if err != nil {
			t.Fatal(err)
		}
		request.Recorded = append(request.Recorded, job.ID)
		expectedFailures[job.ID] = int64(count)
		var intents []jobs.ItemIntent
		for i := range count {
			intents = append(intents, retainedIntent(fmt.Sprintf("failed-%03d\n\x00é�", i)))
		}
		intents = append(intents, retainedIntent("missing execution"), retainedIntent("copied without verification"))
		if err := store.AdmitItems(t.Context(), job.ID, intents); err != nil {
			t.Fatal(err)
		}
		for _, intent := range intents[:count] {
			observation := jobs.ItemObservation{Result: jobs.ItemFailed, Effect: jobs.EffectNotAttempted,
				Diagnostic: jobs.ItemDiagnostic{Stage: jobs.StageSourceRead, Code: jobs.CodeForbidden, HTTPStatus: 403}}
			if err := store.RecordExecution(t.Context(), job.ID, intent.Key, observation); err != nil {
				t.Fatal(err)
			}
			observation.Diagnostic = jobs.ItemDiagnostic{Stage: jobs.StageVerification, Code: jobs.CodeMissing, HTTPStatus: 404}
			if err := store.RecordComparison(t.Context(), job.ID, intent.ItemIdentity, observation); err != nil {
				t.Fatal(err)
			}
		}
		if err := store.RecordExecution(t.Context(), job.ID, intents[count+1].Key,
			jobs.ItemObservation{Result: jobs.ItemCopied, Effect: jobs.EffectReportedApplied, CopiedBytes: 10}); err != nil {
			t.Fatal(err)
		}
		if err := store.RecoveryAdd(t.Context(), jobs.RecoveryRecord{JobID: job.ID, BookmarkID: "dest-binding", Kind: jobs.RecoveryCommitting,
			Container: "dest-segments", Prefix: ".staging/" + job.ID + "/", Object: "dest/live"}); err != nil {
			t.Fatal(err)
		}
	}
	want := readInterruption(t, database, store, request)
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
	child := exec.CommandContext(ctx, self, "-test.run=^TestInterruptedItemRecordsReopen$", "-test.timeout=25s")
	child.Env = append(os.Environ(), "AERONAUTES_INTERRUPTED_ITEMS_REOPEN="+input)
	if output, err := child.CombinedOutput(); err != nil {
		t.Fatalf("orderly interruption reopen: %v\n%s", err, output)
	}
	data, err = os.ReadFile(request.Output)
	if err != nil {
		t.Fatal(err)
	}
	var got interruptionReopened
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got.Before, want) || got.Interrupted != 4 || got.Repeated != 0 {
		t.Fatalf("orderly reopen changed prior facts: equal=%v interrupted=%d repeated=%d", reflect.DeepEqual(got.Before, want), got.Interrupted, got.Repeated)
	}
	if !reflect.DeepEqual(got.After.Items.Records, want.Items.Records) || !reflect.DeepEqual(got.After.Items.Totals, want.Items.Totals) ||
		!reflect.DeepEqual(got.After.Recovery, want.Recovery) || !reflect.DeepEqual(got.After.Audit, want.Audit) || !got.After.Items.NotCaptured {
		t.Fatal("interruption fabricated, replaced or removed retained facts")
	}
	for id, failures := range expectedFailures {
		actual := got.After.Items.Jobs[id]
		counters := saved
		counters.ObjectsFailed = failures
		totals := got.After.Items.Totals[id]
		if actual.State != "failed" || actual.Counters != counters || actual.FinishedAt == "" || !strings.Contains(actual.Error, "other counters show the last saved progress") ||
			totals.Failed != failures || totals.Unresolved != 1 || totals.Unverified != 1 {
			t.Fatalf("interruption misrepresented retained outcomes: %+v totals=%+v", actual, totals)
		}
	}
}
