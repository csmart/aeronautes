package app

import (
	"context"
	"errors"
	"fmt"
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

// maxMirrorDeleteSet bounds the in-memory dest-only name list; beyond
// it the job refuses honestly rather than growing unbounded (§15.2's
// spirit — a v1 limit stated in the error, not a silent truncation).
const maxMirrorDeleteSet = 200_000

// StartMirror validates the typed confirmation and launches the job.
func (s *TransferService) StartMirror(ctx context.Context, req MirrorRequestDTO) (string, error) {
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
	s.launch(jobCtx, job.ID, req.SourceBookmarkID, func(runCtx context.Context) {
		s.runMirror(runCtx, job.ID, req, src, dst, engine, capabilities)
	})
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
		transition(state)
		s.record(background, req.SourceBookmarkID, "transfer.mirror."+string(state),
			req.SourceContainer+"/"+req.SourcePrefix+" → "+req.DestContainer+"/"+req.DestPrefix,
			"job "+jobID, terminalAuditResult(state))
		s.runner.release(jobID)
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
		JobID:      jobID,
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
	_ = s.jobs.RecoveryAdd(background, jobs.RecoveryRecord{
		JobID: jobID, BookmarkID: req.DestBookmarkID, Kind: jobs.RecoveryStaging,
		Container: req.DestContainer, Prefix: executor.StagingPrefix(),
	})
	transition(jobs.StateRunning)

	// One pass feeds the copy executor AND collects the delete set: the
	// names this execution's plan proved destination-only.
	var destOnly []string
	tooMany := false
	_, planErr := plan.Copy(ctx,
		src.Objects(ctx, req.SourceContainer, swiftcontrol.ObjectListOptions{Deep: true, Prefix: req.SourcePrefix}),
		dst.Objects(ctx, req.DestContainer, swiftcontrol.ObjectListOptions{Deep: true, Prefix: req.DestPrefix}),
		req.SourcePrefix, req.DestPrefix, func(item plan.Item) error {
			if item.Verdict == plan.VerdictDestOnly {
				if len(destOnly) >= maxMirrorDeleteSet {
					tooMany = true
					return fmt.Errorf("destination-only set exceeds the v1 mirror limit (%d names)", maxMirrorDeleteSet)
				}
				destOnly = append(destOnly, req.DestPrefix+item.Name)
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

	if _, err := executor.Reconcile(background, report); err == nil {
		_ = s.jobs.RecoveryClear(background, jobID)
	}
	s.recordOwnership(background, req.DestBookmarkID, jobID, report.CommittedPrefixes)

	if planErr != nil {
		if errors.Is(planErr, context.Canceled) {
			finish(jobs.StateCancelled, "")
		} else if tooMany {
			finish(jobs.StateFailed, planErr.Error()+"; nothing was deleted")
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

	// Delete half: native, manifest-only, exactly the planned names.
	deleteResult, err := deletion.DeleteNames(ctx, dst, capabilities, req.DestContainer, destOnly, deletion.Options{
		OnProgress: func(deleted, failed int64) {
			countersMu.Lock()
			counters.ObjectsDeleted = deleted
			countersMu.Unlock()
			flush()
		},
	})
	if deleteResult != nil {
		countersMu.Lock()
		counters.ObjectsDeleted = deleteResult.Deleted
		counters.ObjectsFailed += int64(len(deleteResult.Failures))
		countersMu.Unlock()
		flush()
	}
	if err != nil {
		if errors.Is(err, context.Canceled) {
			finish(jobs.StateCancelled, fmt.Sprintf("cancelled during deletions (%d of %d done)", deleteResult.Deleted, len(destOnly)))
		} else {
			finish(jobs.StateFailed, "delete half: "+err.Error())
		}
		return
	}
	settings := s.effectiveConfigJSON(req.CopyRequestDTO)
	settings = strings.TrimSuffix(settings, "}") + fmt.Sprintf(`,"deleteStrategy":%q,"deleteVerify":%q}`, deleteResult.Strategy, deleteResult.VerifyPolicy)
	_ = s.jobs.SetSettings(background, jobID, settings)

	if len(deleteResult.Failures) > 0 || deleteResult.Remaining > 0 {
		finish(jobs.StatePartial, fmt.Sprintf("%d deletions failed, %d sampled names still answer",
			len(deleteResult.Failures), deleteResult.Remaining))
		return
	}
	finish(jobs.StateComplete, "")
}
