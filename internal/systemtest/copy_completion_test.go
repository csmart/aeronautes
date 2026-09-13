package systemtest

import (
	"bytes"
	"context"
	"crypto/md5"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/csmart/aeronautes/internal/app"
	"github.com/csmart/aeronautes/internal/audit"
	"github.com/csmart/aeronautes/internal/bookmarks"
	"github.com/csmart/aeronautes/internal/db"
	"github.com/csmart/aeronautes/internal/jobs"
	"github.com/csmart/aeronautes/internal/swifttest"
)

type copyCompletionState struct {
	Job           *app.JobDTO
	Rows          []jobs.RecoveryRecord
	Status        app.RecoveryStatusDTO
	Audit         []audit.Event
	ItemsCaptured bool
	Items         []jobs.ItemRecord
	ItemTotals    jobs.ItemTotals
}

func copyCompletionRead(t *testing.T, database *db.DB, id string) copyCompletionState {
	t.Helper()
	service := app.NewJobService(jobs.NewStore(database))
	job, err := service.Get(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	status, err := service.RecoveryStatus(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	events, err := audit.NewStore(database).List(t.Context(), audit.Query{OperationPrefix: "transfer.copy.", Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	state := copyCompletionState{Job: job, Status: status}
	store := jobs.NewStore(database)
	totals, err := store.ReadItemTotals(t.Context(), id)
	if err != nil && !errors.Is(err, jobs.ErrItemRecordsNotCaptured) {
		t.Fatal(err)
	}
	if err == nil {
		state.ItemsCaptured, state.ItemTotals = true, totals
		for cursor := int64(0); ; {
			page, err := store.ReadItems(t.Context(), id, cursor, 100)
			if err != nil || len(page.Items) > 100 {
				t.Fatalf("copy retained item page: %+v %v", page, err)
			}
			state.Items = append(state.Items, page.Items...)
			if page.NextCursor == 0 {
				break
			}
			if page.NextCursor <= cursor {
				t.Fatal("copy retained item cursor did not advance")
			}
			cursor = page.NextCursor
		}
	}
	for _, row := range uploadRecoveryRows(t, database) {
		if row.JobID == id {
			state.Rows = append(state.Rows, row)
		}
	}
	for _, event := range events {
		if event.Operation != "transfer.copy.start" && (event.Summary == "job "+id || strings.HasPrefix(event.Summary, "job "+id+" · ")) {
			state.Audit = append(state.Audit, event)
		}
	}
	return state
}

type copyCompletionReopened struct {
	Before, After copyCompletionState
	Interrupted   int64
}

func copyCompletionChild(t *testing.T) bool {
	input := os.Getenv("AERONAUTES_COPY_COMPLETION_REOPEN_INPUT")
	if input == "" {
		return false
	}
	var request struct{ Database, JobID, Output string }
	data, err := os.ReadFile(input)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &request); err != nil {
		t.Fatal(err)
	}
	database, err := db.Open(t.Context(), request.Database)
	if err != nil {
		t.Fatal(err)
	}
	result := copyCompletionReopened{Before: copyCompletionRead(t, database, request.JobID)}
	result.Interrupted, err = jobs.NewStore(database).FailInterrupted(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	result.After = copyCompletionRead(t, database, request.JobID)
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
	return true
}

func copyCompletionReopen(t *testing.T, h *Harness, id string, want copyCompletionState) {
	t.Helper()
	// Remove only the owned refusal to model an available database after
	// orderly restart; this does not simulate a crash or a successful fsync.
	if err := h.database.WriteTx(t.Context(), func(tx *sql.Tx) error {
		_, err := tx.Exec("DROP TRIGGER IF EXISTS refuse_copy_completion")
		return err
	}); err != nil {
		t.Fatal(err)
	}
	h.CloseFind()
	if err := h.database.Close(); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	request := struct{ Database, JobID, Output string }{h.statePath, id, filepath.Join(dir, "output.json")}
	data, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	input := filepath.Join(dir, "input.json")
	if err := os.WriteFile(input, data, 0600); err != nil {
		t.Fatal(err)
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	child := exec.CommandContext(ctx, executable, "-test.run=^TestCopyCompletionRealSwift$", "-test.timeout=15s")
	child.Env = append(os.Environ(), "AERONAUTES_COPY_COMPLETION_REOPEN_INPUT="+input)
	started := time.Now().UTC().Truncate(time.Second)
	if output, err := child.CombinedOutput(); err != nil {
		t.Fatalf("copy reopen: %v\n%s", err, output)
	}
	data, err = os.ReadFile(request.Output)
	if err != nil {
		t.Fatal(err)
	}
	var got copyCompletionReopened
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got.Before, want) {
		t.Errorf("copy durable result changed on reopen: got=%+v want=%+v", got.Before, want)
	}
	wantAfter := want
	wantInterrupted := int64(0)
	if !jobs.Terminal(jobs.State(want.Job.State)) {
		wantInterrupted = 1
		job := *want.Job
		job.State = "failed"
		job.Error = "interrupted by shutdown; data and recovery records retained, remote residue unverified; counters show the last saved progress"
		if want.ItemsCaptured {
			job.Error = "interrupted by shutdown; data and recovery records retained, remote residue unverified; other counters show the last saved progress"
			job.Counters.ObjectsFailed = want.ItemTotals.Failed
		}
		finished, err := time.Parse(time.RFC3339, got.After.Job.FinishedAt)
		if err != nil || finished.Before(started) || finished.After(time.Now().UTC()) {
			t.Fatalf("invalid restart finish time: %q", got.After.Job.FinishedAt)
		}
		job.FinishedAt = got.After.Job.FinishedAt
		wantAfter.Job = &job
		if len(want.Rows) > 0 {
			wantAfter.Status = app.RecoveryStatusDTO{Jobs: 1, Records: int64(len(want.Rows))}
		}
	}
	if got.Interrupted != wantInterrupted || !reflect.DeepEqual(got.After, wantAfter) {
		t.Errorf("copy restart invented/lost outcomes: got=%+v want=%+v", got, wantAfter)
	}
}

func copyCompletionProxy(t *testing.T, h *Harness, account *Account, refusedGet string, barriers ...*swifttest.HTTPResponseBarrier) *uploadDeleteTrace {
	t.Helper()
	storage, err := url.Parse(account.storageURL)
	if err != nil {
		t.Fatal(err)
	}
	target := *storage
	target.Path, target.RawPath, target.RawQuery = "", "", ""
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.DisableCompression = true
	transport.ResponseHeaderTimeout = 10 * time.Second
	t.Cleanup(transport.CloseIdleConnections)
	proxy := httputil.NewSingleHostReverseProxy(&target)
	proxy.Transport = transport
	proxy.ErrorHandler = func(w http.ResponseWriter, _ *http.Request, err error) {
		t.Errorf("copy proxy: %v", err)
		http.Error(w, "fixture proxy failed", http.StatusBadGateway)
	}
	var handler http.Handler = proxy
	for _, barrier := range barriers {
		handler = barrier.Wrap(handler)
	}
	trace := &uploadDeleteTrace{}
	front := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		trace.mu.Lock()
		trace.requests++
		if r.Method == http.MethodPut || r.Method == http.MethodPost || r.Method == http.MethodDelete {
			trace.writes = append(trace.writes, r.Method+" "+r.URL.Path)
		}
		trace.mu.Unlock()
		if refusedGet != "" && r.Method == http.MethodGet && r.URL.Path == storage.Path+"/"+refusedGet {
			http.Error(w, "fixture source read refusal", http.StatusForbidden)
			return
		}
		if len(barriers) != 0 && len(r.Header.Values("Expect")) != 0 {
			// Finish the request handshake before holding the final response;
			// otherwise net/http can bypass the barrier with an interim 100.
			prepared, err := copyCompletionContinue(r)
			if err != nil {
				t.Errorf("copy barrier request: %v", err)
				http.Error(w, "fixture request failed", http.StatusBadRequest)
				return
			}
			r = prepared
		}
		handler.ServeHTTP(w, r)
	}))
	t.Cleanup(front.Close)
	for _, barrier := range barriers {
		t.Cleanup(barrier.Close)
	}
	list, err := h.Bookmarks.List(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, bookmark := range list {
		if bookmark.ID != account.BookmarkID {
			continue
		}
		bookmark.StorageURLOverride = front.URL + storage.EscapedPath()
		if _, err := h.Bookmarks.Save(t.Context(), bookmark, account.key); err != nil {
			t.Fatal(err)
		}
		found = true
	}
	if !found {
		t.Fatal("owned copy bookmark missing")
	}
	return trace
}

func copyCompletionContinue(r *http.Request) (*http.Request, error) {
	expect := r.Header.Values("Expect")
	if len(expect) != 1 || !strings.EqualFold(strings.TrimSpace(expect[0]), "100-continue") {
		return nil, fmt.Errorf("unsupported fixture expectation")
	}
	const limit = 64 << 10
	body, readErr := io.ReadAll(io.LimitReader(r.Body, limit+1))
	closeErr := r.Body.Close()
	if err := errors.Join(readErr, closeErr); err != nil {
		return nil, err
	}
	if len(body) > limit || (r.ContentLength >= 0 && r.ContentLength != int64(len(body))) {
		return nil, fmt.Errorf("fixture request body size invalid: %d", len(body))
	}
	prepared := r.Clone(r.Context())
	prepared.Header.Del("Expect")
	prepared.Body = io.NopCloser(bytes.NewReader(body))
	prepared.ContentLength = int64(len(body))
	return prepared, nil
}

func copyCompletionAwaitPins(t *testing.T, h *Harness, accounts ...*Account) {
	t.Helper()
	list, err := h.Bookmarks.List(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	// A credential publication cannot pass while this actual job retains
	// its binding. Its success acknowledges all deferred body cleanup.
	if len(accounts) == 0 {
		accounts = []*Account{h.Primary, h.Secondary}
	}
	for _, account := range accounts {
		found := false
		for _, bookmark := range list {
			if bookmark.ID != account.BookmarkID {
				continue
			}
			found = true
			deadline := time.Now().Add(30 * time.Second)
			for {
				_, err := h.Bookmarks.Save(t.Context(), bookmark, account.key)
				if err == nil {
					break
				}
				if !errors.Is(err, bookmarks.ErrInUse) {
					t.Fatal(err)
				}
				if time.Now().After(deadline) {
					t.Fatalf("copy body did not release binding: %v", err)
				}
				time.Sleep(5 * time.Millisecond)
			}
		}
		if !found {
			t.Fatal("owned copy binding missing")
		}
	}
}

func copyCompletionRun(t *testing.T, h *Harness, fault string) {
	t.Helper()
	src := h.Container(h.Primary, "completion-src")
	dst := h.Container(h.Secondary, "completion-dst")
	segments := h.Container(h.Secondary, "completion-segments")
	outside := h.Container(h.Secondary, "completion-outside")
	h.Seed(h.Primary, src, "unselected", []byte("source sentinel"), nil)
	h.Seed(h.Primary, src, "blocked/new.bin", []byte("must stay at source"), nil)
	h.Seed(h.Secondary, dst, "scope/.aeronautes/stage/other-job/keep", []byte("other job sentinel"), nil)
	body := []byte("shared bytes")
	h.Seed(h.Secondary, segments, "shared/0", body, nil)
	h.Seed(h.Secondary, outside, "alias", nil, map[string]string{"X-Object-Manifest": segments + "/shared/"})
	slo := fmt.Sprintf(`[{"path":%q,"etag":"%x","size_bytes":%d}]`, "/"+segments+"/shared/0", md5.Sum(body), len(body))
	h.SeedSLO(h.Secondary, outside, "slo-alias", slo)
	verifyingRefused := strings.HasPrefix(fault, "verifying")
	refused := fault == "mixed" || fault == "refused" || fault == "error" || fault == "verifying_failed"
	newNames := fault != "refused" && fault != "error"
	newBody := []byte("copy bytes")
	names := []string{"a-native\nnew", "z-engine.bin"}
	if newNames {
		for _, name := range names {
			h.Seed(h.Primary, src, "scope/"+name, newBody, nil)
		}
	}
	if refused {
		h.Seed(h.Primary, src, "scope/existing", []byte("replacement bytes"), nil)
		h.Seed(h.Secondary, dst, "scope/existing", []byte("retained bytes"), nil)
	}
	beforeA, beforeB := h.SnapshotNamespace(h.Primary), h.SnapshotNamespace(h.Secondary)
	refusedGet := ""
	if fault == "verifying_failed" {
		refusedGet = src + "/scope/" + names[0]
	}
	srcTrace, dstTrace := copyCompletionProxy(t, h, h.Primary, refusedGet), copyCompletionProxy(t, h, h.Secondary, "")
	// Source authority must survive a different active browser account.
	if _, err := h.Browse.Connect(t.Context(), h.Secondary.BookmarkID); err != nil {
		t.Fatal(err)
	}
	query := ""
	switch fault {
	case "counters":
		query = `CREATE TRIGGER refuse_copy_completion BEFORE UPDATE OF counters_json ON jobs WHEN json_extract(NEW.counters_json,'$.objectsVerified')>0 BEGIN SELECT RAISE(ABORT,'fixture final counters refusal'); END`
	case "error":
		query = `CREATE TRIGGER refuse_copy_completion BEFORE UPDATE OF error ON jobs BEGIN SELECT RAISE(ABORT,'fixture error refusal'); END`
	case "terminal":
		query = `CREATE TRIGGER refuse_copy_completion BEFORE UPDATE OF state ON jobs WHEN NEW.state IN ('complete','partial','failed','cancelled') BEGIN SELECT RAISE(ABORT,'fixture terminal refusal'); END`
	case "audit":
		query = `CREATE TRIGGER refuse_copy_completion BEFORE INSERT ON audit_events WHEN NEW.operation IN ('transfer.copy.complete','transfer.copy.partial','transfer.copy.failed','transfer.copy.cancelled') BEGIN SELECT RAISE(ABORT,'fixture terminal audit refusal'); END`
	case "start_audit":
		query = `CREATE TRIGGER refuse_copy_completion BEFORE INSERT ON audit_events WHEN NEW.operation='transfer.copy.start' BEGIN SELECT RAISE(ABORT,'fixture start audit refusal'); END`
	case "planning", "running", "verifying", "verifying_failed":
		state := fault
		if verifyingRefused {
			state = "verifying"
		}
		query = fmt.Sprintf(`CREATE TRIGGER refuse_copy_completion BEFORE UPDATE OF state ON jobs WHEN NEW.state='%s' BEGIN SELECT RAISE(ABORT,'fixture admission refusal'); END`, state)
	case "recovery":
		query = `CREATE TRIGGER refuse_copy_completion BEFORE INSERT ON job_recovery BEGIN SELECT RAISE(ABORT,'fixture recovery refusal'); END`
	case "retention":
		if err := h.database.WriteTx(t.Context(), func(tx *sql.Tx) error {
			_, err := tx.Exec(`WITH RECURSIVE n(x) AS (SELECT 1 UNION ALL SELECT x+1 FROM n WHERE x<10000) INSERT INTO audit_events(at,operation,resource,summary,result) SELECT '2026-09-08T00:00:00Z','fixture.keep','owned','retained','applied' FROM n`)
			return err
		}); err != nil {
			t.Fatal(err)
		}
		query = `CREATE TRIGGER refuse_copy_completion BEFORE DELETE ON audit_events WHEN EXISTS(SELECT 1 FROM audit_events WHERE operation='transfer.copy.complete') BEGIN SELECT RAISE(ABORT,'fixture retention refusal'); END`
	}
	if query != "" {
		if err := h.database.WriteTx(t.Context(), func(tx *sql.Tx) error { _, err := tx.Exec(query); return err }); err != nil {
			t.Fatal(err)
		}
	}
	id, startErr := h.Transfer.StartCopy(t.Context(), app.CopyRequestDTO{
		SourceBookmarkID: h.Primary.BookmarkID, SourceContainer: src, SourcePrefix: "scope/",
		DestBookmarkID: h.Secondary.BookmarkID, DestContainer: dst, DestPrefix: "scope/",
	})
	if fault == "start_audit" {
		if startErr == nil || id != "" {
			t.Errorf("copy start accounting refusal ignored: %q %v", id, startErr)
		}
		rows, err := jobs.NewStore(h.database).List(t.Context(), jobs.Filter{})
		if err != nil || len(rows) != 1 {
			t.Fatalf("copy admission record: %+v %v", rows, err)
		}
		id = rows[0].ID
	} else if startErr != nil {
		t.Fatal(startErr)
	}
	copyCompletionAwaitPins(t, h)
	state := copyCompletionRead(t, h.database, id)
	visible, visibleErr := h.Jobs.Get(t.Context(), id)
	unavailable := fault == "counters" || fault == "error" || fault == "terminal" || fault == "audit" || fault == "retention"
	if unavailable {
		if visibleErr == nil || visible != nil || !strings.Contains(visibleErr.Error(), "final result could not be saved") {
			t.Errorf("copy persistence failure hidden: %+v %v", visible, visibleErr)
		}
		if _, err := h.Jobs.List(t.Context(), "", false, 50); err == nil {
			t.Error("copy job list hid persistence failure")
		}
		if jobs.Terminal(jobs.State(state.Job.State)) || state.Job.FinishedAt != "" || state.Job.Error != "" || len(state.Audit) != 0 {
			t.Errorf("copy partial terminal transaction: %+v", state)
		}
	} else if visibleErr != nil || !reflect.DeepEqual(visible, state.Job) {
		t.Errorf("copy visible/durable result differs: %+v %v %+v", visible, visibleErr, state)
	}
	admitted := fault != "start_audit" && fault != "planning" && fault != "running" && fault != "recovery"
	wantState := "complete"
	if !admitted {
		wantState = "failed"
	} else if refused || verifyingRefused {
		wantState = "partial"
	}
	wantCounters := jobs.Counters{}
	if admitted {
		if newNames {
			wantCounters = jobs.Counters{ObjectsPlanned: 2, BytesPlanned: 20, ObjectsCopied: 2, BytesCopied: 20, NativeRouted: 1, ObjectsVerified: 2}
		}
		if refused {
			wantCounters.ObjectsPlanned++
			wantCounters.BytesPlanned += 17
			wantCounters.OverwritesRefused = 1
		}
		if verifyingRefused {
			wantCounters.ObjectsVerified = 0
		}
		if fault == "verifying_failed" {
			wantCounters.ObjectsCopied = 1
			wantCounters.BytesCopied = 10
			wantCounters.ObjectsFailed = 1
		}
	}
	wantTotals := jobs.ItemTotals{Items: wantCounters.ObjectsPlanned, Planned: wantCounters.ObjectsPlanned,
		Admitted: wantCounters.ObjectsPlanned - wantCounters.OverwritesRefused, Failed: wantCounters.ObjectsFailed,
		Refused: wantCounters.OverwritesRefused, Problems: wantCounters.ObjectsFailed + wantCounters.OverwritesRefused}
	if verifyingRefused {
		wantTotals.Unverified = wantCounters.ObjectsCopied
	}
	if !state.ItemsCaptured || state.ItemTotals != wantTotals || int64(len(state.Items)) != wantTotals.Items {
		t.Fatalf("Copy completion lost recorded outcomes: captured=%v totals=%+v rows=%d want=%+v", state.ItemsCaptured, state.ItemTotals, len(state.Items), wantTotals)
	}
	if !unavailable {
		if state.Job.State != wantState || state.Job.Counters != wantCounters {
			t.Errorf("copy terminal snapshot differs: %+v want=%s %+v", state.Job, wantState, wantCounters)
		}
		result := audit.ResultApplied
		if wantState == "partial" {
			result = audit.ResultVerifyMismatch
		} else if wantState == "failed" {
			result = audit.ResultFailed
		}
		summary := "job " + id
		if refused {
			summary += " · 1 overwrite refused"
		}
		summary += fmt.Sprintf(" · item outcomes: %d failed names, 0 conflicts, %d refusals, 0 unresolved, %d unverified, %d with problems",
			wantTotals.Failed, wantTotals.Refused, wantTotals.Unverified, wantTotals.Problems)
		if len(state.Audit) != 1 || state.Audit[0].Operation != "transfer.copy."+wantState || state.Audit[0].Result != result || state.Audit[0].Summary != summary || state.Audit[0].Resource != src+"/scope/ → "+dst+"/scope/" || state.Audit[0].BookmarkID != h.Primary.BookmarkID {
			t.Errorf("copy audit differs: %+v", state.Audit)
		}
		if refused && !strings.Contains(state.Job.Error, "1 overwrite refused") {
			t.Errorf("copy refusal detail lost: %+v", state.Job)
		}
		if !admitted || verifyingRefused {
			if !strings.Contains(state.Job.Error, "refusal") {
				t.Errorf("copy accounting reason lost: %+v", state.Job)
			}
		}
		if fault == "verifying_failed" && (!strings.Contains(state.Job.Error, "execution failed (example: "+names[0]+":") || !strings.Contains(state.Job.Error, "verification state could not be saved:")) {
			t.Errorf("Copy verification refusal lost earlier failure or later reason: %q", state.Job.Error)
		}
		if wantState == "complete" && state.Job.Error != "" {
			t.Error("copy control acquired an error")
		}
	}
	if err := h.Transfer.CancelJob(t.Context(), id); err == nil {
		t.Error("finished copy body remains cancellable")
	}
	if unavailable {
		copyCompletionRefuseNew(t, h, src, dst, 1)
	}
	afterA, afterB := h.SnapshotNamespace(h.Primary), h.SnapshotNamespace(h.Secondary)
	if stray := collateral(beforeA, afterA, nil); len(stray) > 0 {
		t.Errorf("copy changed source collateral: %v", stray)
	}
	var allowed []string
	if admitted && newNames {
		for _, name := range names {
			if fault == "verifying_failed" && name == names[0] {
				continue
			}
			identity := "scope/" + name
			allowed = append(allowed, dst+"/"+identity)
			got, ok := afterB.Containers[dst].Objects[identity]
			if !ok || got.Size != 10 || got.Digest != fmt.Sprintf("%x", sha256.Sum256(newBody)) || got.Class != "plain" {
				t.Errorf("copy intended object differs: %s %+v", identity, got)
			}
		}
	}
	if stray := collateral(beforeB, afterB, allowed); len(stray) > 0 {
		t.Errorf("copy changed destination collateral: %v", stray)
	}
	if len(srcTrace.writeSnapshot()) != 0 {
		t.Errorf("copy wrote source account: %v", srcTrace.writeSnapshot())
	}
	if (!admitted || !newNames) && len(dstTrace.writeSnapshot()) != 0 {
		t.Errorf("unadmitted copy writes: %v", dstTrace.writeSnapshot())
	}
	var wantRows []jobs.RecoveryRecord
	if fault != "planning" && fault != "start_audit" && fault != "recovery" {
		wantRows = []jobs.RecoveryRecord{{JobID: id, BookmarkID: h.Secondary.BookmarkID, Kind: jobs.RecoveryStaging, Container: dst, Prefix: "scope/.aeronautes/stage/" + id + "/"}}
	}
	if !reflect.DeepEqual(state.Rows, wantRows) || state.Job.RecoveryRecords != int64(len(wantRows)) {
		t.Errorf("copy recovery identities differ: got=%+v want=%+v", state, wantRows)
	}
	encoded, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("copy completion observation: %s", encoded)
	requestCount := srcTrace.requestCount() + dstTrace.requestCount()
	copyCompletionReopen(t, h, id, state)
	if srcTrace.requestCount()+dstTrace.requestCount() != requestCount {
		t.Error("copy reopen issued remote requests")
	}
	h.AssertNoCollateral(afterA, h.SnapshotNamespace(h.Primary))
	h.AssertNoCollateral(afterB, h.SnapshotNamespace(h.Secondary))
}

func copyCompletionRefuseNew(t *testing.T, h *Harness, src, dst string, wantJobs int64) {
	t.Helper()
	id, err := h.Transfer.StartCopy(t.Context(), app.CopyRequestDTO{
		SourceBookmarkID: h.Primary.BookmarkID, SourceContainer: src, SourcePrefix: "blocked/",
		DestBookmarkID: h.Secondary.BookmarkID, DestContainer: dst, DestPrefix: "blocked/",
	})
	if err == nil || id != "" || !strings.Contains(err.Error(), "new jobs are blocked") {
		t.Errorf("Copy admitted after persistence failure: %q %v", id, err)
	}
	if id != "" {
		copyCompletionAwaitPins(t, h)
	}
	var count int64
	if err := h.database.Reader().QueryRowContext(t.Context(), "SELECT COUNT(*) FROM jobs").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != wantJobs {
		t.Errorf("blocked Copy created a job row: %d want %d", count, wantJobs)
	}
}

func copyCompletionQueued(t *testing.T, h *Harness, fault string) {
	t.Helper()
	settings, err := h.Settings.Get(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	settings.MaxActiveJobs = 1
	if _, err := h.Settings.Set(t.Context(), settings); err != nil {
		t.Fatal(err)
	}
	src := h.Container(h.Primary, "queued-copy-src")
	dst := h.Container(h.Secondary, "queued-copy-dst")
	h.Seed(h.Primary, src, "hold/first.bin", []byte("hold bytes"), nil)
	h.Seed(h.Primary, src, "scope/new.bin", []byte("queued bytes"), nil)
	h.Seed(h.Primary, src, "blocked/new.bin", []byte("must stay at source"), nil)
	h.Seed(h.Primary, src, "keep", []byte("source sentinel"), nil)
	h.Seed(h.Secondary, dst, "scope/.aeronautes/stage/other-job/keep", []byte("other job sentinel"), nil)
	beforeA, beforeB := h.SnapshotNamespace(h.Primary), h.SnapshotNamespace(h.Secondary)
	dstTrace := copyCompletionProxy(t, h, h.Secondary, "")
	barrier, ended, selected, srcTrace := interruptionProxy(t, h, src, http.MethodGet, "hold/first.bin", nil)
	root := t.TempDir()
	seedListingLocal(t, root, "keep", []byte("local sentinel"))
	localBefore := snapshotListingLocal(t, root)
	app.WireFilePicker(h.Transfer, listingFilePicker{dir: root})
	picked, err := h.Transfer.PickDownloadDir(t.Context(), app.SelectionScopeDTO{BookmarkID: h.Primary.BookmarkID, Container: src, Prefix: "hold/"})
	if err != nil {
		t.Fatal(err)
	}
	first, err := h.Transfer.StartDownload(t.Context(), app.DownloadRequestDTO{BookmarkID: h.Primary.BookmarkID, Container: src, Prefix: "hold/", DestDirToken: picked.Token})
	if err != nil {
		t.Fatal(err)
	}
	event, err := barrier.Wait(t.Context())
	if err != nil || event.StatusCode != http.StatusOK || selected.Load() != 1 {
		t.Fatalf("queued-copy barrier: %+v %v count=%d", event, err, selected.Load())
	}
	id, err := h.Transfer.StartCopy(t.Context(), app.CopyRequestDTO{SourceBookmarkID: h.Primary.BookmarkID, SourceContainer: src, SourcePrefix: "scope/", DestBookmarkID: h.Secondary.BookmarkID, DestContainer: dst, DestPrefix: "scope/"})
	if err != nil {
		t.Fatal(err)
	}
	queued, err := h.Jobs.Get(t.Context(), id)
	if err != nil || queued.State != "queued" || queued.StartedAt != "" {
		t.Fatalf("copy was not queued: %+v %v", queued, err)
	}
	query := ""
	if fault == "state" {
		query = fmt.Sprintf(`CREATE TRIGGER refuse_copy_completion BEFORE UPDATE OF state ON jobs WHEN NEW.id='%s' AND NEW.state='cancelled' BEGIN SELECT RAISE(ABORT,'fixture queued state refusal'); END`, id)
	}
	if fault == "audit" {
		query = `CREATE TRIGGER refuse_copy_completion BEFORE INSERT ON audit_events WHEN NEW.operation='transfer.copy.cancelled' BEGIN SELECT RAISE(ABORT,'fixture queued audit refusal'); END`
	}
	if fault == "unavailable" {
		query = `CREATE TRIGGER refuse_copy_completion BEFORE INSERT ON audit_events WHEN NEW.operation='transfer.local.complete' BEGIN SELECT RAISE(ABORT,'fixture running job completion refusal'); END`
	}
	if query != "" {
		if err := h.database.WriteTx(t.Context(), func(tx *sql.Tx) error { _, err := tx.Exec(query); return err }); err != nil {
			t.Fatal(err)
		}
	}
	releaseFirst := func() {
		if err := barrier.Release(); err != nil {
			t.Fatal(err)
		}
		select {
		case <-ended:
		case <-time.After(5 * time.Second):
			t.Fatal("first download response did not finish")
		}
		if barrier.Err() != nil || selected.Load() != 1 {
			t.Fatalf("queued-copy barrier error/retry: %v count=%d", barrier.Err(), selected.Load())
		}
	}
	if fault == "unavailable" {
		releaseFirst()
	} else {
		if err := h.Transfer.CancelJob(t.Context(), id); err != nil {
			t.Fatal(err)
		}
	}
	// Only the queued Copy holds the secondary binding. Its release
	// acknowledges that cancellation or the admission refusal has returned.
	copyCompletionAwaitPins(t, h, h.Secondary)
	state := copyCompletionRead(t, h.database, id)
	visible, visibleErr := h.Jobs.Get(t.Context(), id)
	if fault == "control" {
		if visibleErr != nil || visible.State != "cancelled" || state.Job.State != "cancelled" || state.Job.FinishedAt == "" || state.Job.Error != "" {
			t.Errorf("queued Copy result differs: %+v %v %+v", visible, visibleErr, state)
		}
		if len(state.Audit) != 1 || state.Audit[0].Operation != "transfer.copy.cancelled" || state.Audit[0].Result != audit.ResultFailed || state.Audit[0].BookmarkID != h.Primary.BookmarkID || state.Audit[0].Resource != src+"/scope/ → "+dst+"/scope/" || state.Audit[0].Summary != "job "+id+" · item outcomes: 0 failed names, 0 conflicts, 0 refusals, 0 unresolved, 0 unverified, 0 with problems" {
			t.Errorf("queued Copy audit differs: %+v", state.Audit)
		}
	} else {
		if visibleErr == nil || visible != nil || state.Job.State != "queued" || state.Job.FinishedAt != "" || state.Job.Error != "" || len(state.Audit) != 0 {
			t.Errorf("queued Copy persistence failure hidden: %+v %v %+v", visible, visibleErr, state)
		}
		if _, err := h.Jobs.List(t.Context(), "", false, 50); err == nil {
			t.Error("queued Copy job list hid persistence failure")
		}
	}
	if state.Job.StartedAt != "" || state.Job.Counters != queued.Counters || len(state.Rows) != 0 || state.Status != (app.RecoveryStatusDTO{}) {
		t.Errorf("queued Copy invented execution evidence: %+v", state)
	}
	if !state.ItemsCaptured || len(state.Items) != 0 || state.ItemTotals != (jobs.ItemTotals{}) {
		t.Fatalf("queued Copy did not retain explicit recorded-empty history: %+v", state)
	}
	if err := h.Transfer.CancelJob(t.Context(), id); err == nil {
		t.Error("queued Copy retained cancellation registration")
	}
	if fault != "unavailable" {
		releaseFirst()
	}
	copyCompletionAwaitPins(t, h)
	firstState := uploadRecoveryRead(t, h.database, first)
	wantFirstState := "complete"
	wantFirstCounters := jobs.Counters{ObjectsPlanned: 1, ObjectsCopied: 1, ObjectsVerified: 1, BytesPlanned: 10, BytesCopied: 10}
	if fault == "unavailable" {
		wantFirstState = "verifying"
		wantFirstCounters.ObjectsVerified = 0
		if len(firstState.Audit) != 0 || firstState.Job.FinishedAt != "" || firstState.Job.Error != "" {
			t.Errorf("partial running-job transaction: %+v", firstState)
		}
	} else if fault != "control" {
		// Already admitted work may retain its facts, while public totals
		// remain unavailable after another job's refused completion.
		wantFirstState = "partial"
		unavailable := h.jobStore.Available()
		if unavailable == nil || firstState.Job.Error != unavailable.Error() || firstState.Job.FinishedAt == "" ||
			len(firstState.Audit) != 1 || firstState.Audit[0].Operation != "transfer.local.partial" ||
			firstState.Audit[0].Result != "verify_mismatch" || firstState.Audit[0].Resource != "job "+first ||
			firstState.Audit[0].Summary != " · item outcomes: 0 failed names, 0 conflicts, 0 refusals, 0 unresolved, 0 unverified, 0 with problems" {
			t.Errorf("running download lost the earlier persistence refusal: %+v latch=%v", firstState, unavailable)
		}
	}
	if firstState.Job.State != wantFirstState || firstState.Job.Counters != wantFirstCounters {
		t.Errorf("queued Copy affected running download: %+v", firstState)
	}
	if !firstState.ItemsCaptured || firstState.ItemTotals != (jobs.ItemTotals{Items: 1, Planned: 1, Admitted: 1}) || len(firstState.Items) != 1 {
		t.Fatalf("running download lost its admitted item facts: %+v", firstState)
	}
	item := firstState.Items[0]
	if item.Key != "hold/first.bin" || item.JobID != first || !item.Planned || !item.Admitted || item.ListedBytes != 10 ||
		item.Source != (jobs.ItemLocation{Kind: jobs.ItemSwift, BookmarkID: h.Primary.BookmarkID, Container: src, Object: "hold/first.bin"}) ||
		item.Destination != (jobs.ItemLocation{Kind: jobs.ItemLocal, LocalPath: filepath.Join(root, "first.bin")}) ||
		item.Staging == nil || *item.Staging != (jobs.ItemLocation{Kind: jobs.ItemLocal, LocalPath: filepath.Join(root, ".aeronautes-stage-"+first, "first.bin")}) ||
		item.Execution == nil || item.Execution.ItemObservation != (jobs.ItemObservation{Result: jobs.ItemCopied, Effect: jobs.EffectReportedApplied, CopiedBytes: 10}) ||
		item.Verification == nil || item.Verification.ItemObservation != (jobs.ItemObservation{Result: jobs.ItemVerified, Effect: jobs.EffectNotAttempted}) {
		t.Fatalf("running download lost its exact placement/verification history: %+v", item)
	}
	if fault == "control" {
		assertDownloadItemPages(t, h, first, map[string]jobs.ItemRecord{item.Key: item}, firstState.ItemTotals)
	} else {
		if page, err := h.Jobs.Items(t.Context(), first, "", 100); err == nil || page.Items != nil {
			t.Errorf("shared persistence refusal exposed public download items: %+v %v", page, err)
		}
		if totals, err := h.Jobs.ItemTotals(t.Context(), first); err == nil || totals != (app.JobItemTotalsDTO{}) {
			t.Errorf("shared persistence refusal exposed public download totals: %+v %v", totals, err)
		}
	}
	if fault != "control" {
		copyCompletionRefuseNew(t, h, src, dst, 2)
	}
	assertListingLocalDelta(t, localBefore, snapshotListingLocal(t, root), map[string][]byte{"first.bin": []byte("hold bytes")})
	afterA, afterB := h.SnapshotNamespace(h.Primary), h.SnapshotNamespace(h.Secondary)
	h.AssertNoCollateral(beforeA, afterA)
	h.AssertNoCollateral(beforeB, afterB)
	if len(srcTrace.writeSnapshot()) != 0 || len(dstTrace.writeSnapshot()) != 0 {
		t.Errorf("queued cancellation allowed remote writes: %v %v", srcTrace.writeSnapshot(), dstTrace.writeSnapshot())
	}
	encoded, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("copy completion observation: %s", encoded)
	if fault == "unavailable" {
		// The other cases cover orderly process reopening. This case keeps
		// its two uncertain rows and proves admission without implying that
		// either missing terminal record reconstructs itself on restart.
		return
	}
	requests := srcTrace.requestCount() + dstTrace.requestCount()
	copyCompletionReopen(t, h, id, state)
	if requests != srcTrace.requestCount()+dstTrace.requestCount() {
		t.Error("queued Copy reopen contacted remote")
	}
	h.AssertNoCollateral(afterA, h.SnapshotNamespace(h.Primary))
	h.AssertNoCollateral(afterB, h.SnapshotNamespace(h.Secondary))
}

func copyCompletionCases(t *testing.T, makeHarness func(*testing.T) *Harness) {
	for _, fault := range []string{"control", "mixed", "refused", "counters", "error", "terminal", "audit", "retention", "start_audit", "planning", "running", "verifying", "verifying_failed", "recovery"} {
		t.Run(fault, func(t *testing.T) { copyCompletionRun(t, makeHarness(t), fault) })
	}
	for _, fault := range []string{"control", "state", "audit", "unavailable"} {
		t.Run("queued_"+fault, func(t *testing.T) { copyCompletionQueued(t, makeHarness(t), fault) })
	}
}

func TestCopyCompletionMock(t *testing.T) {
	t.Parallel()
	copyCompletionCases(t, findMockHarness)
}
func TestCopyCompletionRealSwift(t *testing.T) {
	t.Parallel()

	if copyCompletionChild(t) {
		return
	}
	copyCompletionCases(t, New)
}
