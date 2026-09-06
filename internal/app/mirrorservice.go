package app

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"strings"
	"sync"

	"github.com/csmart/aeronautes/internal/audit"
	"github.com/csmart/aeronautes/internal/deletion"
	"github.com/csmart/aeronautes/internal/jobs"
	"github.com/csmart/aeronautes/internal/swiftcontrol"
	transfercopy "github.com/csmart/aeronautes/internal/transfer/copy"
	"github.com/csmart/aeronautes/internal/transfer/plan"
)

// Executable Mirror (§27.2, §65.5 item 4): an Aeronautes-planned copy
// plus Aeronautes-native destination-only deletion. The delete half is
// manifest-only by construction, only ever touches names the execution
// plan classified DestOnly (an Unknown verdict is transferred, never
// deleted — it exists on both sides by definition), and runs ONLY
// after the copy half's independent verification comes back clean.

// MirrorRequestDTO is a copy request plus the §28.2 typed confirmation.
type MirrorRequestDTO struct {
	CopyRequestDTO
	// Confirm must equal the DESTINATION container name.
	Confirm string `json:"confirm"`
}

// mirrorDeleteChunk is how many dest-only names are held in memory at
// once during the delete half; the full set spills to disk (JSON-
// encoded per line, so hostile names survive), keeping Mirror
// bounded-memory at any scale (§15.2).
const mirrorDeleteChunk = 2000

// StartMirror validates the typed confirmation and launches the job.
func (s *TransferService) StartMirror(ctx context.Context, req MirrorRequestDTO) (string, error) {
	if err := validateCopyRequest(req.CopyRequestDTO); err != nil {
		return "", err
	}
	releasePins, err := s.connections.pinForJob(ctx, req.SourceBookmarkID, req.DestBookmarkID)
	if err != nil {
		return "", err
	}
	defer func() {
		if releasePins != nil {
			releasePins()
		}
	}()
	src, dst, _, err := s.resolve(ctx, req.CopyRequestDTO)
	if err != nil {
		return "", err
	}
	if req.Confirm != req.DestContainer {
		return "", fmt.Errorf("confirmation %q does not match the destination container — type it exactly", req.Confirm)
	}
	engine, err := s.buildEngine(ctx, req.CopyRequestDTO, src, dst)
	if err != nil {
		return "", fmt.Errorf("transfer engine: %w", err)
	}
	capabilities, err := dst.FetchCapabilities(ctx)
	if err != nil {
		return "", err
	}

	job, err := s.jobs.Create(ctx, jobs.Job{
		Type:             jobs.TypeMirror,
		Source:           req.SourceContainer + "/" + req.SourcePrefix,
		Destination:      req.DestContainer + "/" + req.DestPrefix,
		SourceBookmarkID: req.SourceBookmarkID,
		DestBookmarkID:   req.DestBookmarkID,
		Engine:           "rclone+native",
		Settings:         s.effectiveConfigJSON(req.CopyRequestDTO),
	})
	if err != nil {
		return "", err
	}
	jobCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	s.runner.register(job.ID, cancel)
	s.record(ctx, req.SourceBookmarkID, "transfer.mirror.start",
		job.Source+" → "+job.Destination, "job "+job.ID+" · "+job.Settings, audit.ResultApplied)
	s.launch(jobCtx, job.ID, req.SourceBookmarkID, releasePins, func(runCtx context.Context) {
		s.runMirror(runCtx, job.ID, req, src, dst, engine, capabilities)
	})
	releasePins = nil
	return job.ID, nil
}

func (s *TransferService) runMirror(ctx context.Context, jobID string, req MirrorRequestDTO,
	src, dst *swiftcontrol.Session, engine transfercopy.Engine, capabilities *swiftcontrol.Capabilities) {

	background := context.WithoutCancel(ctx)
	transition := func(state jobs.State) {
		_ = s.jobs.Transition(background, jobID, state)
	}
	finish := func(state jobs.State, message string) {
		if message != "" {
			_ = s.jobs.SetError(background, jobID, message)
		}
		// Deregister BEFORE the terminal state becomes visible: a job
		// that reads terminal must no longer be cancel-addressable.
		s.runner.release(jobID)
		transition(state)
		s.record(background, req.SourceBookmarkID, "transfer.mirror."+string(state),
			req.SourceContainer+"/"+req.SourcePrefix+" → "+req.DestContainer+"/"+req.DestPrefix,
			"job "+jobID, terminalAuditResult(state))
	}

	transition(jobs.StatePlanning)
	counters := jobs.Counters{}
	var countersMu sync.Mutex
	flush := func() {
		countersMu.Lock()
		snapshot := counters
		countersMu.Unlock()
		_ = s.jobs.UpdateCounters(background, jobID, snapshot)
	}

	executor, err := transfercopy.New(transfercopy.Config{
		Source: src, Dest: dst, Engine: engine,
		SrcSpec: "aeronautes_src:" + req.SourceContainer, DstSpec: "aeronautes_dst:" + req.DestContainer,
		SrcContainer: req.SourceContainer, DstContainer: req.DestContainer,
		SrcPrefix: req.SourcePrefix, DstPrefix: req.DestPrefix,
		JobID: jobID,
		OnAuthFailure: func() {
			src.ForceReauth()
			dst.ForceReauth()
		},
		OnRecovery: s.recoveryRecorder(background, jobID, req.DestBookmarkID),
		OnItem: func(outcome transfercopy.ItemOutcome) {
			countersMu.Lock()
			switch outcome.Outcome {
			case "copied":
				counters.ObjectsCopied++
			case "failed":
				counters.ObjectsFailed++
			}
			countersMu.Unlock()
			flush()
		},
	})
	if err != nil {
		finish(jobs.StateFailed, err.Error())
		return
	}
	if err := s.jobs.RecoveryAdd(background, jobs.RecoveryRecord{
		JobID: jobID, BookmarkID: req.DestBookmarkID, Kind: jobs.RecoveryStaging,
		Container: req.DestContainer, Prefix: executor.StagingPrefix(),
	}); err != nil {
		finish(jobs.StateFailed, "recovery store unavailable before first write: "+err.Error())
		return
	}
	transition(jobs.StateRunning)

	// One pass feeds the copy executor AND spills the delete set to
	// disk: the names this execution's plan proved destination-only,
	// JSON-encoded one per line so hostile names round-trip, with only
	// a chunk ever in memory.
	spill, err := os.CreateTemp("", "aeronautes-mirror-*.jsonl")
	if err != nil {
		finish(jobs.StateFailed, "delete-set spill: "+err.Error())
		return
	}
	spillPath := spill.Name()
	defer os.Remove(spillPath)
	spillWriter := bufio.NewWriter(spill)
	spillEncoder := json.NewEncoder(spillWriter)
	var destOnlyCount int64
	_, planErr := plan.Copy(ctx,
		src.Objects(ctx, req.SourceContainer, swiftcontrol.ObjectListOptions{Deep: true, Prefix: req.SourcePrefix}),
		dst.Objects(ctx, req.DestContainer, swiftcontrol.ObjectListOptions{Deep: true, Prefix: req.DestPrefix}),
		req.SourcePrefix, req.DestPrefix, func(item plan.Item) error {
			if item.Verdict == plan.VerdictDestOnly {
				if err := spillEncoder.Encode(spilledDelete{Name: req.DestPrefix + item.Name, ETag: item.DestETag}); err != nil {
					return fmt.Errorf("delete-set spill: %w", err)
				}
				destOnlyCount++
				countersMu.Lock()
				counters.ObjectsPlanned++
				countersMu.Unlock()
				return nil
			}
			countersMu.Lock()
			switch item.Verdict {
			case plan.VerdictNew, plan.VerdictChanged, plan.VerdictUnknown:
				counters.ObjectsPlanned++
				counters.BytesPlanned += item.SourceBytes
			}
			countersMu.Unlock()
			return executor.Handle(ctx, item)
		})
	if flushErr := spillWriter.Flush(); planErr == nil && flushErr != nil {
		planErr = fmt.Errorf("delete-set spill: %w", flushErr)
	}
	spill.Close()
	report, finishErr := executor.Finish(ctx)
	if planErr == nil {
		planErr = finishErr
	}
	countersMu.Lock()
	counters.BytesCopied = report.BytesCopied
	counters.Overwrites = report.Overwrites
	counters.NativeRouted = report.NativeRouted
	countersMu.Unlock()
	flush()

	if reconcile, err := executor.Reconcile(background, report); err == nil {
		log.Printf("job %s reconcile kept records: %s", jobID, strings.Join(reconcile.Notes, "; "))
	} else {
		log.Printf("job %s reconcile: %v", jobID, err)
	}
	s.recordOwnership(background, req.DestBookmarkID, jobID, report.CommittedPrefixes)

	if planErr != nil {
		if errors.Is(planErr, context.Canceled) {
			finish(jobs.StateCancelled, "")
		} else {
			finish(jobs.StateFailed, planErr.Error())
		}
		return
	}

	// Copy-half verification: the delete half runs ONLY on a clean pass.
	transition(jobs.StateVerifying)
	verify, err := executor.Verify(background,
		src.Objects(background, req.SourceContainer, swiftcontrol.ObjectListOptions{Deep: true, Prefix: req.SourcePrefix}),
		dst.Objects(background, req.DestContainer, swiftcontrol.ObjectListOptions{Deep: true, Prefix: req.DestPrefix}))
	if err != nil {
		finish(jobs.StateFailed, "verification: "+err.Error())
		return
	}
	countersMu.Lock()
	counters.ObjectsVerified = verify.Verified
	counters.ObjectsFailed = int64(len(report.Failures) + len(verify.Failures))
	countersMu.Unlock()
	flush()
	if len(report.Failures) > 0 || len(verify.Failures) > 0 || len(report.Conflicts) > 0 {
		finish(jobs.StatePartial, fmt.Sprintf(
			"copy half did not fully verify (%d failed, %d unverified, %d conflicts): the delete half was NOT run",
			len(report.Failures), len(verify.Failures), len(report.Conflicts)))
		return
	}

	// Delete half: native, manifest-only, streamed back off the spill in
	// bounded chunks — and each chunk RE-CHECKS source membership at the
	// last moment (§65.9 item 10): a name whose source reappeared since
	// the plan pass is no longer destination-only and is skipped, so an
	// ordinary concurrent writer cannot make yesterday's plan delete
	// today's data.
	deleteResult := &deletion.Result{}
	var skippedFresh int64
	recordFailure, closeSpill := failureSpill(jobID)
	var skippedReplaced int64
	err = streamSpill(spillPath, mirrorDeleteChunk, func(chunk []spilledDelete) error {
		names, freshErr := s.recheckDeleteChunk(ctx, src, dst, req, chunk, &skippedFresh, &skippedReplaced)
		if freshErr != nil {
			return fmt.Errorf("membership recheck: %w", freshErr)
		}
		chunkNames := names
		if len(chunkNames) == 0 {
			return nil
		}
		chunkResult, chunkErr := deletion.DeleteNames(ctx, dst, capabilities, req.DestContainer, chunkNames, deletion.Options{
			OnProgress: func(deleted, failed int64) {
				countersMu.Lock()
				counters.ObjectsDeleted = deleteResult.Deleted + deleted
				countersMu.Unlock()
				flush()
			},
			OnFailure: recordFailure,
		})
		if chunkResult != nil {
			deleteResult.Strategy = chunkResult.Strategy
			deleteResult.VerifyPolicy = chunkResult.VerifyPolicy
			deleteResult.Deleted += chunkResult.Deleted
			deleteResult.NotFound += chunkResult.NotFound
			deleteResult.Remaining += chunkResult.Remaining
			deleteResult.Failed += chunkResult.Failed
			if len(deleteResult.Failures) < 200 {
				deleteResult.Failures = append(deleteResult.Failures, chunkResult.Failures...)
			}
			countersMu.Lock()
			counters.ObjectsDeleted = deleteResult.Deleted
			counters.ObjectsFailed += chunkResult.Failed
			countersMu.Unlock()
			flush()
		}
		return chunkErr
	})
	failureSpillPath, _ := closeSpill()
	if err != nil {
		// Once any deletion succeeded the outcome is PARTIAL whatever
		// interrupted it: some destination objects are irreversibly
		// gone and the terminal state must say so exactly.
		switch {
		case deleteResult.Deleted > 0 && errors.Is(err, context.Canceled):
			finish(jobs.StatePartial, fmt.Sprintf("cancelled during deletions: %d of %d dest-only names deleted before the stop", deleteResult.Deleted, destOnlyCount))
		case deleteResult.Deleted > 0:
			finish(jobs.StatePartial, fmt.Sprintf("delete half stopped after %d of %d deletions: %v", deleteResult.Deleted, destOnlyCount, err))
		case errors.Is(err, context.Canceled):
			finish(jobs.StateCancelled, "cancelled before any deletion")
		default:
			finish(jobs.StateFailed, "delete half: "+err.Error())
		}
		return
	}
	settings := s.effectiveConfigJSON(req.CopyRequestDTO)
	settings = strings.TrimSuffix(settings, "}") + fmt.Sprintf(`,"deleteStrategy":%q,"deleteVerify":%q}`, deleteResult.Strategy, deleteResult.VerifyPolicy)
	_ = s.jobs.SetSettings(background, jobID, settings)

	if deleteResult.Failed > 0 || deleteResult.Remaining > 0 {
		detail := fmt.Sprintf("%d deletions failed, %d sampled names still answer", deleteResult.Failed, deleteResult.Remaining)
		if failureSpillPath != "" {
			detail += "; full list: " + failureSpillPath
		}
		finish(jobs.StatePartial, detail)
		return
	}
	if skippedFresh > 0 || skippedReplaced > 0 {
		parts := []string{}
		if skippedFresh > 0 {
			parts = append(parts, fmt.Sprintf("%d skipped: source reappeared", skippedFresh))
		}
		if skippedReplaced > 0 {
			parts = append(parts, fmt.Sprintf("%d skipped: destination replaced since planning (conflict, not deleted)", skippedReplaced))
		}
		finish(jobs.StateComplete, strings.Join(parts, "; "))
		return
	}
	finish(jobs.StateComplete, "")
}

// streamSpill replays the JSON-lines delete set in bounded chunks.
func streamSpill(path string, chunkSize int, handle func([]spilledDelete) error) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	decoder := json.NewDecoder(bufio.NewReader(file))
	chunk := make([]spilledDelete, 0, chunkSize)
	for {
		var name spilledDelete
		if err := decoder.Decode(&name); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return fmt.Errorf("delete-set spill read: %w", err)
		}
		chunk = append(chunk, name)
		if len(chunk) == chunkSize {
			if err := handle(chunk); err != nil {
				return err
			}
			chunk = chunk[:0]
		}
	}
	if len(chunk) > 0 {
		return handle(chunk)
	}
	return nil
}

// spilledDelete is one planned dest-only deletion: the name AND the
// destination identity the plan saw — deletion is bound to both.
type spilledDelete struct {
	Name string `json:"n"`
	ETag string `json:"e"`
}

// recheckDeleteChunk revalidates each planned deletion at the last
// moment, on BOTH sides (review F-02): the source must still lack the
// name, and the destination object must still carry the ETag the plan
// recorded — a replaced destination is a conflict skip, never a
// deletion of someone else's new data. Bounded worker fan-out; any
// HEAD failure aborts the chunk (doubt never deletes).
func (s *TransferService) recheckDeleteChunk(ctx context.Context, src, dst *swiftcontrol.Session,
	req MirrorRequestDTO, chunk []spilledDelete, skippedFresh, skippedReplaced *int64) ([]string, error) {

	type verdict struct {
		idx      int
		fresh    bool
		replaced bool
		err      error
	}
	const workers = 8
	jobsCh := make(chan int)
	results := make(chan verdict)
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for idx := range jobsCh {
				entry := chunk[idx]
				rel := strings.TrimPrefix(entry.Name, req.DestPrefix)
				_, srcErr := src.ObjectDetail(ctx, req.SourceContainer, req.SourcePrefix+rel)
				switch {
				case srcErr == nil:
					results <- verdict{idx: idx, fresh: true}
					continue
				case !swiftcontrol.IsNotFound(srcErr):
					results <- verdict{idx: idx, err: srcErr}
					continue
				}
				live, dstErr := dst.ObjectDetail(ctx, req.DestContainer, entry.Name)
				switch {
				case dstErr != nil && swiftcontrol.IsNotFound(dstErr):
					// Already gone: deleting it is a no-op either way.
					results <- verdict{idx: idx}
				case dstErr != nil:
					results <- verdict{idx: idx, err: dstErr}
				case entry.ETag == "":
					// No planned identity means nothing to bind the
					// delete to: fail CLOSED as a conflict, never
					// delete an unidentified target (Swift has no
					// conditional DELETE — measured: If-Match on
					// DELETE is ignored, wrong etag still 204s).
					results <- verdict{idx: idx, replaced: true}
				case strings.Trim(live.ETag, `"`) != strings.Trim(entry.ETag, `"`):
					results <- verdict{idx: idx, replaced: true}
				default:
					results <- verdict{idx: idx}
				}
			}
		}()
	}
	go func() {
		for idx := range chunk {
			jobsCh <- idx
		}
		close(jobsCh)
		wg.Wait()
		close(results)
	}()
	keepIdx := make([]bool, len(chunk))
	var firstErr error
	for v := range results {
		switch {
		case v.err != nil && firstErr == nil:
			firstErr = v.err
		case v.fresh:
			*skippedFresh++
		case v.replaced:
			*skippedReplaced++
		default:
			keepIdx[v.idx] = true
		}
	}
	if firstErr != nil {
		return nil, firstErr
	}
	var kept []string
	for idx, keep := range keepIdx {
		if keep {
			kept = append(kept, chunk[idx].Name)
		}
	}
	return kept, nil
}
