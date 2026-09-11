package systemtest

import (
	"fmt"
	"net/http"
	"net/url"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/csmart/aeronautes/internal/app"
	"github.com/csmart/aeronautes/internal/audit"
	"github.com/csmart/aeronautes/internal/jobs"
	"github.com/csmart/aeronautes/internal/systemtest/oracle"
)

func assertStagedContentEffects(t *testing.T, h *Harness, f stagedContentFixture, id, stageName string, stage oracle.ObjectState,
	accepted bool, sourceTrace *uploadDeleteTrace, trace *stagedContentTrace) {
	t.Helper()
	afterA, afterB := h.SnapshotNamespace(h.Primary), h.SnapshotNamespace(h.Secondary)
	h.AssertNoCollateral(f.beforeA, afterA)
	if writes := sourceTrace.writeSnapshot(); len(writes) != 0 {
		t.Fatalf("Copy attempted source mutation: %v", writes)
	}
	control := afterB.Containers[f.dst].Objects["scope/a-control"]
	if control.Class != "plain" || control.Size != int64(len(f.control)) || control.Digest != stagedContentDigest(f.control) || control.Meta["Team"] != "content-proof" {
		t.Fatalf("first independently witnessed effect was lost or changed: %+v", control)
	}
	live, exists := afterB.Containers[f.dst].Objects["scope/b-subject"]
	if !accepted && exists {
		t.Fatalf("wrong staged bytes reached the live subject: digest=%s expected_source=%s", live.Digest, stagedContentDigest(f.subject))
	}
	retained, stageExists := afterB.Containers[f.dst].Objects[stageName]
	if !accepted && (!stageExists || !reflect.DeepEqual(retained, stage)) {
		t.Fatalf("refused stage or backing segment evidence was not retained: got=%+v want=%+v", retained, stage)
	}
	if accepted && (!exists || stageExists || live.Class != "dlo" || live.Manifest != stage.Manifest || live.Size != int64(len(f.subject)) ||
		live.Digest != stagedContentDigest(f.subject) || live.Meta["Team"] != "content-proof" || !reflect.DeepEqual(live.Segments, stage.Segments)) {
		t.Fatalf("valid content was refused or changed across publication: live=%+v retained_stage=%v", live, stageExists)
	}
	if _, exists := afterB.Containers[f.dst].Objects["scope/.aeronautes/stage/"+id+"/batch/a-control"]; exists {
		t.Fatal("successfully published control stage was not cleaned")
	}
	allowed := []string{f.dst + "/scope/a-control"}
	if accepted {
		allowed = append(allowed, f.dst+"/scope/b-subject")
	} else {
		allowed = append(allowed, f.dst+"/"+stageName)
	}
	for _, segment := range stage.Segments {
		allowed = append(allowed, segment.Name)
	}
	if f.later != nil {
		laterStage := "scope/.aeronautes/stage/" + id + "/batch/c-later"
		allowed = append(allowed, f.dst+"/"+laterStage)
		if later := afterB.Containers[f.dst].Objects[laterStage]; later.Class != "plain" || later.Size != int64(len(f.later)) || later.Digest != stagedContentDigest(f.later) {
			t.Fatalf("unstarted later stage was not retained: %+v", later)
		}
		if _, exists := afterB.Containers[f.dst].Objects["scope/c-later"]; exists {
			t.Fatal("later commit crossed the required record refusal")
		}
	}
	h.AssertNoCollateral(f.beforeB, afterB, allowed...)
	assertStagedContentWrites(t, h, f, id, stageName, stage, accepted, trace.snapshot())
}

func assertStagedContentResult(t *testing.T, h *Harness, f stagedContentFixture, id, stageName string, stage oracle.ObjectState,
	accepted bool, failureCode jobs.ItemCode, saved copyCompletionState, sourceTrace *uploadDeleteTrace, trace *stagedContentTrace) {
	t.Helper()
	assertStagedContentEffects(t, h, f, id, stageName, stage, accepted, sourceTrace, trace)
	wantState, wantResult := "partial", audit.ResultVerifyMismatch
	wantCounters := jobs.Counters{ObjectsPlanned: 2, BytesPlanned: int64(len(f.control) + len(f.subject)),
		ObjectsCopied: 1, BytesCopied: int64(len(f.control)), ObjectsVerified: 1, ObjectsFailed: 1}
	wantTotals := jobs.ItemTotals{Items: 2, Planned: 2, Admitted: 2, Failed: 1, Unresolved: 1, Problems: 1}
	if accepted {
		wantState, wantResult = "complete", audit.ResultApplied
		wantCounters.ObjectsCopied, wantCounters.ObjectsVerified, wantCounters.ObjectsFailed = 2, 2, 0
		wantCounters.BytesCopied += int64(len(f.subject))
		wantTotals = jobs.ItemTotals{Items: 2, Planned: 2, Admitted: 2}
	}
	if saved.Job.State != wantState || saved.Job.Counters != wantCounters || saved.Job.FinishedAt == "" ||
		!saved.ItemsCaptured || saved.ItemTotals != wantTotals || len(saved.Items) != 2 {
		t.Fatalf("staged content outcome differs: job=%+v totals=%+v want=%s %+v %+v", saved.Job, saved.ItemTotals, wantState, wantCounters, wantTotals)
	}
	if accepted && saved.Job.Error != "" {
		t.Fatalf("valid content acquired a job error: %q", saved.Job.Error)
	}
	if !accepted && !strings.Contains(saved.Job.Error, "b-subject") {
		t.Fatalf("refused content lost the named failure: %q", saved.Job.Error)
	}
	for i, name := range []string{"a-control", "b-subject"} {
		body := f.control
		if i == 1 {
			body = f.subject
		}
		item := saved.Items[i]
		wantStage := jobs.ItemLocation{Kind: jobs.ItemSwift, BookmarkID: h.Secondary.BookmarkID, Container: f.dst,
			Object: "scope/.aeronautes/stage/" + id + "/batch/" + name}
		if item.JobID != id || item.Key != name || !item.Planned || !item.Admitted || item.ListedBytes != int64(len(body)) ||
			item.Source != (jobs.ItemLocation{Kind: jobs.ItemSwift, BookmarkID: h.Primary.BookmarkID, Container: f.src, Object: "scope/" + name}) ||
			item.Destination != (jobs.ItemLocation{Kind: jobs.ItemSwift, BookmarkID: h.Secondary.BookmarkID, Container: f.dst, Object: "scope/" + name}) ||
			item.Staging == nil || *item.Staging != wantStage || item.Execution == nil || item.Verification == nil {
			t.Fatalf("content outcome lost captured identity: %+v", item)
		}
		wantExecution := jobs.ItemObservation{Result: jobs.ItemCopied, Effect: jobs.EffectReportedApplied, CopiedBytes: int64(len(body))}
		wantVerification := jobs.ItemObservation{Result: jobs.ItemVerified, Effect: jobs.EffectNotAttempted}
		if i == 1 && !accepted {
			wantExecution = jobs.ItemObservation{Result: jobs.ItemFailed, Effect: jobs.EffectUnresolved,
				Diagnostic: jobs.ItemDiagnostic{Stage: jobs.StageStaging, Code: failureCode}}
			wantVerification = jobs.ItemObservation{Result: jobs.ItemFailed, Effect: jobs.EffectNotAttempted,
				Diagnostic: jobs.ItemDiagnostic{Stage: jobs.StageVerification, Code: jobs.CodeUnverified}}
		}
		if item.Execution.ItemObservation != wantExecution || item.Verification.ItemObservation != wantVerification {
			t.Fatalf("staged content observations changed their effect or failure stage: %+v", item)
		}
	}
	assertStagedContentRecovery(t, h, f, id, stage, accepted, saved)
	assertStagedContentPublic(t, h, id, saved)
	suffix := fmt.Sprintf(" · item outcomes: %d failed names, 0 conflicts, 0 refusals, %d unresolved, 0 unverified, %d with problems",
		wantTotals.Failed, wantTotals.Unresolved, wantTotals.Problems)
	segmentChangeAudit(t, h, saved, segmentChangeScope{src: f.src, dst: f.dst}, id, "transfer.copy."+wantState, wantResult, suffix)
	if !accepted {
		requests := sourceTrace.requestCount() + len(trace.snapshot())
		if rerun, err := h.Transfer.RerunJob(t.Context(), id); err == nil || rerun != "" || requests != sourceTrace.requestCount()+len(trace.snapshot()) {
			t.Fatalf("unresolved staged content allowed rerun or traffic: %q %v", rerun, err)
		}
	}
	if repeat := copyCompletionRead(t, h.database, id); !reflect.DeepEqual(repeat, saved) {
		t.Fatal("public outcome reads changed the retained content evidence")
	}
}

func assertStagedContentWrites(t *testing.T, h *Harness, f stagedContentFixture, id, stageName string, stage oracle.ObjectState, accepted bool, requests []stagedContentRequest) {
	t.Helper()
	storage, err := url.Parse(h.Secondary.storageURL)
	if err != nil {
		t.Fatal(err)
	}
	prefix := storage.Path + "/"
	controlStage := f.dst + "/scope/.aeronautes/stage/" + id + "/batch/a-control"
	allowed := map[string]int{
		http.MethodPut + " " + prefix + controlStage:               1,
		http.MethodDelete + " " + prefix + controlStage:            1,
		http.MethodPut + " " + prefix + f.dst + "/" + stageName:    1,
		http.MethodPut + " " + prefix + f.dst + "/scope/a-control": 1,
	}
	// Fixture mutations bypass this proxy; engine staging always uploads
	// the original two pieces, even when their content is later changed.
	segmentPrefix := strings.TrimSuffix(stage.Manifest, "/") + "/"
	for i := 0; i < 2; i++ {
		allowed[http.MethodPut+" "+prefix+segmentPrefix+fmt.Sprintf("%08d", i)] = 1
	}
	if accepted {
		allowed[http.MethodPut+" "+prefix+f.dst+"/scope/b-subject"] = 1
		allowed[http.MethodDelete+" "+prefix+f.dst+"/"+stageName] = 1
	}
	if f.later != nil {
		allowed[http.MethodPut+" "+prefix+f.dst+"/scope/.aeronautes/stage/"+id+"/batch/c-later"] = 1
	}
	stagedReads, readIndex, publicationIndex := 0, -1, -1
	for index, request := range requests {
		if request.Method == http.MethodGet && request.Path == prefix+f.dst+"/"+stageName {
			if request.Query != "" || request.AcceptEncoding != "identity" {
				t.Fatalf("staged content read changed its complete opaque request: %+v", request)
			}
			stagedReads++
			readIndex = index
		}
		if request.Method == http.MethodPut && request.Path == prefix+f.dst+"/scope/b-subject" {
			publicationIndex = index
		}
		if request.Method != http.MethodPut && request.Method != http.MethodPost && request.Method != http.MethodDelete {
			continue
		}
		key := request.Method + " " + request.Path
		if allowed[key] != 1 || request.Query != "" {
			t.Fatalf("unexpected or repeated staged content mutation: %+v", request)
		}
		delete(allowed, key)
		if request.Path == prefix+f.dst+"/scope/a-control" || request.Path == prefix+f.dst+"/scope/b-subject" {
			if request.IfNoneMatch != "*" || (request.Path == prefix+f.dst+"/scope/b-subject" && request.Manifest != stage.Manifest) {
				t.Fatalf("publication changed its conditional or manifest identity: %+v", request)
			}
		}
	}
	if len(allowed) != 0 {
		t.Fatalf("expected staged content writes were not observed: %+v", allowed)
	}
	if stagedReads != 1 || (accepted && (publicationIndex < 0 || readIndex >= publicationIndex)) {
		t.Fatalf("staged content read count or publication ordering differs: reads=%d read_index=%d publication_index=%d", stagedReads, readIndex, publicationIndex)
	}
	t.Logf("staged content GET boundary: requests=%d expected_assembled_bytes=%d accepted=%v read_index=%d publication_index=%d",
		stagedReads, len(f.subject), accepted, readIndex, publicationIndex)
}

func assertStagedContentRecovery(t *testing.T, h *Harness, f stagedContentFixture, id string, stage oracle.ObjectState, accepted bool, saved copyCompletionState) {
	t.Helper()
	prefix := strings.TrimSuffix(strings.TrimPrefix(stage.Manifest, f.segments+"/"), "/") + "/"
	want := map[jobs.RecoveryRecord]bool{
		{JobID: id, BookmarkID: h.Secondary.BookmarkID, Kind: jobs.RecoveryStaging, Container: f.dst, Prefix: "scope/.aeronautes/stage/" + id + "/"}: true,
		{JobID: id, BookmarkID: h.Secondary.BookmarkID, Kind: jobs.RecoveryCaptured, Container: f.segments, Prefix: prefix}:                          true,
	}
	if accepted {
		for _, kind := range []jobs.RecoveryKind{jobs.RecoveryCommitting, jobs.RecoveryCommitted} {
			object := ""
			if kind == jobs.RecoveryCommitting {
				object = f.dst + "/scope/b-subject"
			}
			want[jobs.RecoveryRecord{JobID: id, BookmarkID: h.Secondary.BookmarkID, Kind: kind, Container: f.segments, Prefix: prefix, Object: object}] = true
		}
	}
	wantStatus := app.RecoveryStatusDTO{}
	if jobs.Terminal(jobs.State(saved.Job.State)) {
		wantStatus = app.RecoveryStatusDTO{Jobs: 1, Records: int64(len(want))}
	}
	if len(saved.Rows) != len(want) || saved.Status != wantStatus || saved.Job.RecoveryRecords != int64(len(want)) {
		t.Fatalf("staged content recovery facts differ: %+v want=%+v", saved, want)
	}
	for _, row := range saved.Rows {
		if !want[row] {
			t.Fatalf("staged content invented or changed a recovery fact: %+v", row)
		}
		delete(want, row)
	}
	if len(want) != 0 {
		t.Fatalf("staged content lost distinct recovery facts: %+v", want)
	}
}

func assertStagedContentPublic(t *testing.T, h *Harness, id string, saved copyCompletionState) {
	t.Helper()
	get, err := h.Jobs.Get(t.Context(), id)
	if err != nil || !reflect.DeepEqual(get, saved.Job) {
		t.Fatalf("original public Get differs from durable job: %+v %v", get, err)
	}
	for _, bookmark := range []string{"", h.Primary.BookmarkID, h.Secondary.BookmarkID} {
		list, err := h.Jobs.List(t.Context(), bookmark, false, 100)
		if err != nil || len(list) != 1 || !reflect.DeepEqual(list[0], *saved.Job) {
			t.Fatalf("original public List differs: %+v %v", list, err)
		}
		history, err := h.Jobs.History(t.Context(), bookmark, "", 100)
		if err != nil || len(history.Jobs) != 1 || history.NextCursor != "" || !reflect.DeepEqual(history.Jobs[0], *saved.Job) {
			t.Fatalf("original public History differs: %+v %v", history, err)
		}
	}
	page, err := h.Jobs.Items(t.Context(), id, "", 100)
	if err != nil || page.Status != "available" || page.NextCursor != "" || len(page.Items) != len(saved.Items) {
		t.Fatalf("public staged item page differs: %+v %v", page, err)
	}
	for i, item := range saved.Items {
		location := func(loc jobs.ItemLocation) app.JobItemLocationDTO {
			return app.JobItemLocationDTO{Kind: string(loc.Kind), BookmarkID: loc.BookmarkID, Container: loc.Container, Object: loc.Object,
				LocalPath: loc.LocalPath, Display: strconv.Quote(loc.BookmarkID) + " / " + strconv.Quote(loc.Container) + " / " + strconv.Quote(loc.Object)}
		}
		observation := func(obs *jobs.RecordedObservation) *app.JobItemObservationDTO {
			if obs == nil {
				return nil
			}
			return &app.JobItemObservationDTO{Result: string(obs.Result), Effect: string(obs.Effect), Stage: string(obs.Diagnostic.Stage), Code: string(obs.Diagnostic.Code),
				HTTPStatus: obs.Diagnostic.HTTPStatus, CopiedBytes: obs.CopiedBytes, CreatedAt: obs.CreatedAt.UTC().Format(time.RFC3339Nano)}
		}
		want := app.JobItemDTO{ID: strconv.FormatInt(item.ID, 10), Key: item.Key, KeyDisplay: strconv.Quote(item.Key), Source: location(item.Source), Destination: location(item.Destination),
			Planned: item.Planned, Admitted: item.Admitted, ListedBytes: item.ListedBytes, CreatedAt: item.CreatedAt.UTC().Format(time.RFC3339Nano),
			Execution: observation(item.Execution), Verification: observation(item.Verification)}
		if item.Staging != nil {
			stage := location(*item.Staging)
			want.Staging = &stage
		}
		if !reflect.DeepEqual(page.Items[i], want) {
			t.Fatalf("public staged item lost identity or diagnostic detail: got=%+v want=%+v", page.Items[i], want)
		}
	}
	counts := saved.ItemTotals
	wantTotals := app.JobItemTotalsDTO{Status: "available", Items: counts.Items, Planned: counts.Planned, Admitted: counts.Admitted,
		Failed: counts.Failed, Conflicts: counts.Conflicts, Refused: counts.Refused, Unresolved: counts.Unresolved, Unverified: counts.Unverified, Problems: counts.Problems}
	if totals, err := h.Jobs.ItemTotals(t.Context(), id); err != nil || totals != wantTotals {
		t.Fatalf("public staged item facets differ: %+v %v want=%+v", totals, err, wantTotals)
	}
	events, err := h.Audit.Search(t.Context(), app.AuditQueryDTO{OperationPrefix: "transfer.copy.", Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	terminal := 0
	for _, event := range events {
		if event.Operation == "transfer.copy.start" || !strings.HasPrefix(event.Summary, "job "+id+" · ") {
			continue
		}
		terminal++
		if len(saved.Audit) != 1 || event.ID != saved.Audit[0].ID || event.Operation != saved.Audit[0].Operation || event.Result != string(saved.Audit[0].Result) ||
			event.Resource != saved.Audit[0].Resource || event.Summary != saved.Audit[0].Summary {
			t.Fatalf("public staged terminal audit differs from retained facts: %+v", event)
		}
	}
	if terminal != 1 {
		t.Fatalf("public staged terminal audit count=%d", terminal)
	}
}

func assertStagedContentFault(t *testing.T, h *Harness, f stagedContentFixture, id, stageName string, stage oracle.ObjectState,
	mode string, saved copyCompletionState, sourceTrace *uploadDeleteTrace, trace *stagedContentTrace) {
	t.Helper()
	if mode == "cancel_final_read" {
		if !strings.Contains(saved.Job.Error, "context canceled") {
			t.Fatalf("final-name staged read lost its cancellation cause: %q", saved.Job.Error)
		}
		assertStagedContentResult(t, h, f, id, stageName, stage, false, jobs.CodeCancelled, saved, sourceTrace, trace)
		t.Logf("final-name cancellation: state=%s copied=%d verified=%d failed=%d execution=%+v verification=%+v",
			saved.Job.State, saved.Job.Counters.ObjectsCopied, saved.Job.Counters.ObjectsVerified, saved.Job.Counters.ObjectsFailed,
			saved.Items[1].Execution, saved.Items[1].Verification)
		return
	}
	assertStagedContentEffects(t, h, f, id, stageName, stage, false, sourceTrace, trace)
	assertStagedContentRecovery(t, h, f, id, stage, false, saved)
	wantCount := 2
	wantTotals := jobs.ItemTotals{Items: 2, Planned: 2, Admitted: 2, Failed: 1, Unresolved: 1, Problems: 1}
	if mode == "record_refusal" {
		wantCount = 3
		wantTotals = jobs.ItemTotals{Items: 3, Planned: 3, Admitted: 3, Unresolved: 2, Unverified: 1}
	}
	if !saved.ItemsCaptured || len(saved.Items) != wantCount || saved.ItemTotals != wantTotals {
		t.Fatalf("staged fault invented or lost item facets: captured=%v items=%+v totals=%+v want=%+v", saved.ItemsCaptured, saved.Items, saved.ItemTotals, wantTotals)
	}
	for i, item := range saved.Items {
		name, body := "a-control", f.control
		if i == 1 {
			name, body = "b-subject", f.subject
		} else if i == 2 {
			name, body = "c-later", f.later
		}
		if item.JobID != id || item.Key != name || !item.Planned || !item.Admitted || item.ListedBytes != int64(len(body)) ||
			item.Source != (jobs.ItemLocation{Kind: jobs.ItemSwift, BookmarkID: h.Primary.BookmarkID, Container: f.src, Object: "scope/" + name}) ||
			item.Destination != (jobs.ItemLocation{Kind: jobs.ItemSwift, BookmarkID: h.Secondary.BookmarkID, Container: f.dst, Object: "scope/" + name}) ||
			item.Staging == nil || *item.Staging != (jobs.ItemLocation{Kind: jobs.ItemSwift, BookmarkID: h.Secondary.BookmarkID,
			Container: f.dst, Object: "scope/.aeronautes/stage/" + id + "/batch/" + name}) {
			t.Fatalf("staged fault lost exact admitted identity: %+v", item)
		}
		if i == 0 {
			want := jobs.ItemObservation{Result: jobs.ItemCopied, Effect: jobs.EffectReportedApplied, CopiedBytes: int64(len(f.control))}
			if item.Execution == nil || item.Execution.ItemObservation != want {
				t.Fatalf("staged fault lost earlier copied effect: %+v", item)
			}
		}
		if mode == "record_refusal" {
			if item.Verification != nil || (i > 0 && item.Execution != nil) {
				t.Fatalf("required record refusal invented an observation or permitted later verification: %+v", item)
			}
			continue
		}
		wantVerification := jobs.ItemObservation{Result: jobs.ItemVerified, Effect: jobs.EffectNotAttempted}
		if i == 1 {
			wantExecution := jobs.ItemObservation{Result: jobs.ItemFailed, Effect: jobs.EffectUnresolved,
				Diagnostic: jobs.ItemDiagnostic{Stage: jobs.StageStaging, Code: jobs.CodeUnverified}}
			if item.Execution == nil || item.Execution.ItemObservation != wantExecution {
				t.Fatalf("terminal refusal lost the acknowledged content failure: %+v", item)
			}
			wantVerification = jobs.ItemObservation{Result: jobs.ItemFailed, Effect: jobs.EffectNotAttempted,
				Diagnostic: jobs.ItemDiagnostic{Stage: jobs.StageVerification, Code: jobs.CodeUnverified}}
		}
		if item.Verification == nil || item.Verification.ItemObservation != wantVerification {
			t.Fatalf("terminal refusal changed earlier verification evidence: %+v", item)
		}
	}
	if mode == "record_refusal" {
		wantCounters := jobs.Counters{ObjectsPlanned: 3, BytesPlanned: int64(len(f.control) + len(f.subject) + len(f.later)),
			ObjectsCopied: 1, BytesCopied: int64(len(f.control))}
		if saved.Job.State != "partial" || saved.Job.Counters != wantCounters || saved.Job.FinishedAt == "" ||
			!strings.Contains(saved.Job.Error, "execution failed (example: b-subject:") || !strings.Contains(saved.Job.Error, "item record") {
			t.Fatalf("content and record failures lost their earlier effect or distinct reasons: %+v", saved.Job)
		}
		segmentChangeAudit(t, h, saved, segmentChangeScope{src: f.src, dst: f.dst}, id, "transfer.copy.partial", audit.ResultVerifyMismatch,
			" · item outcomes: 0 failed names, 0 conflicts, 0 refusals, 2 unresolved, 1 unverified, 0 with problems")
	} else if saved.Job.State != "verifying" || saved.Job.FinishedAt != "" || saved.Job.Error != "" || len(saved.Audit) != 0 {
		t.Fatalf("terminal refusal partially saved the terminal job/audit transaction: %+v", saved)
	}
	assertStagedContentUnavailable(t, h, id)
	requests := sourceTrace.requestCount() + len(trace.snapshot())
	if rerun, err := h.Transfer.RerunJob(t.Context(), id); err == nil || rerun != "" || requests != sourceTrace.requestCount()+len(trace.snapshot()) {
		t.Fatalf("unavailable staged records allowed rerun or traffic: %q %v", rerun, err)
	}
	beforeSourceRequests, beforeDestRequests := sourceTrace.requestCount(), len(trace.snapshot())
	copyCompletionRefuseNew(t, h, f.src, f.dst, 1)
	if len(sourceTrace.writeSnapshot()) != 0 {
		t.Fatal("unavailable staged records permitted a new source mutation")
	}
	assertStagedContentWrites(t, h, f, id, stageName, stage, false, trace.snapshot())
	// StartCopy resolves and prepares its engine before the job-store latch;
	// those existing reads confer neither a new job nor mutation authority.
	t.Logf("refused new start storage preparation: source_requests=%d destination_requests=%+v",
		sourceTrace.requestCount()-beforeSourceRequests, trace.snapshot()[beforeDestRequests:])
	if repeat := copyCompletionRead(t, h.database, id); !reflect.DeepEqual(repeat, saved) {
		t.Fatal("unavailable public reads changed retained staged facts")
	}
	t.Logf("staged content persistence boundary: mode=%s durable_state=%s counters=%+v totals=%+v recovery=%d terminal_audit=%d",
		mode, saved.Job.State, saved.Job.Counters, saved.ItemTotals, len(saved.Rows), len(saved.Audit))
}

func assertStagedContentUnavailable(t *testing.T, h *Harness, id string) {
	t.Helper()
	if job, err := h.Jobs.Get(t.Context(), id); err == nil || job != nil || !strings.Contains(err.Error(), "unavailable") {
		t.Fatalf("record refusal exposed stale public Get: %+v %v", job, err)
	}
	if list, err := h.Jobs.List(t.Context(), "", false, 100); err == nil || list != nil {
		t.Fatalf("record refusal exposed stale public List: %+v %v", list, err)
	}
	if history, err := h.Jobs.History(t.Context(), "", "", 100); err == nil || history.Jobs != nil || history.NextCursor != "" {
		t.Fatalf("record refusal exposed stale public History: %+v %v", history, err)
	}
	if items, err := h.Jobs.Items(t.Context(), id, "", 100); err == nil || items.Items != nil || items.Status != "" {
		t.Fatalf("record refusal exposed a partial public ledger: %+v %v", items, err)
	}
	if totals, err := h.Jobs.ItemTotals(t.Context(), id); err == nil || totals != (app.JobItemTotalsDTO{}) {
		t.Fatalf("record refusal invented public totals: %+v %v", totals, err)
	}
}
