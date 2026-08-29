package app

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/csmart/aeronautes/internal/audit"
	"github.com/csmart/aeronautes/internal/jobs"
	"github.com/csmart/aeronautes/internal/swiftcontrol"
	transfercopy "github.com/csmart/aeronautes/internal/transfer/copy"
	"github.com/csmart/aeronautes/internal/transfer/plan"
	"github.com/csmart/aeronautes/internal/transfer/rclone"
)

// CopyRequestDTO scopes one cross-account copy: bookmark + container +
// prefix on each end. Prefixes are "" or end with "/".
type CopyRequestDTO struct {
	SourceBookmarkID string `json:"sourceBookmarkId"`
	SourceContainer  string `json:"sourceContainer"`
	SourcePrefix     string `json:"sourcePrefix"`
	DestBookmarkID   string `json:"destBookmarkId"`
	DestContainer    string `json:"destContainer"`
	DestPrefix       string `json:"destPrefix"`
}

// CopyPlanDTO is the §65.4 preflight surface: honest conservative
// counts (§28.4) plus the disclosures the operator must see before
// execution.
type CopyPlanDTO struct {
	Summary plan.Summary `json:"summary"`
	// VersionedDestination: commits on this container archive a version
	// per overwrite (§28.6 seam finding) — disclosed, not hidden.
	VersionedDestination bool `json:"versionedDestination"`
	// LargeObjectsAsDLO: large sources commit as destination DLOs whose
	// reads depend on listing consistency (§28.6 disclosure).
	LargeObjectsAsDLO bool `json:"largeObjectsAsDlo"`
}

// TransferService is the bridge's transfer surface (ADR 0011): planning
// previews, job start, and cancellation. Execution itself runs in a
// background goroutine against the §46 job store; the drawer follows it
// through JobService.
type TransferService struct {
	connections *Connections
	jobs        *jobs.Store
	audit       *audit.Store

	// engineFactory builds the byte-moving engine for a job; nil uses
	// the pinned rclone. Tests inject fakes here.
	engineFactory func(remotes []rclone.Remote) (transfercopy.Engine, error)

	mu      sync.Mutex
	cancels map[string]context.CancelFunc
}

// NewTransferService wires the service.
func NewTransferService(connections *Connections, jobStore *jobs.Store, auditStore *audit.Store) *TransferService {
	return &TransferService{
		connections: connections,
		jobs:        jobStore,
		audit:       auditStore,
		cancels:     map[string]context.CancelFunc{},
	}
}

// SetEngineFactory overrides engine construction (tests; the future
// compatibility matrix).
func (s *TransferService) SetEngineFactory(f func(remotes []rclone.Remote) (transfercopy.Engine, error)) {
	s.engineFactory = f
}

func validateCopyRequest(req CopyRequestDTO) error {
	if req.SourceBookmarkID == "" || req.DestBookmarkID == "" {
		return errors.New("both bookmarks are required")
	}
	if req.SourceContainer == "" || req.DestContainer == "" {
		return errors.New("both containers are required")
	}
	for _, p := range []string{req.SourcePrefix, req.DestPrefix} {
		if p != "" && !strings.HasSuffix(p, "/") {
			return fmt.Errorf("prefix %q must be empty or end with '/'", p)
		}
	}
	return nil
}

// resolve opens both sessions and fail-fast checks the destination
// container (copying into a void 404s on the first write — say it now).
func (s *TransferService) resolve(ctx context.Context, req CopyRequestDTO) (src, dst *swiftcontrol.Session, versioned bool, err error) {
	if err := validateCopyRequest(req); err != nil {
		return nil, nil, false, err
	}
	src, err = s.connections.SessionFor(ctx, req.SourceBookmarkID)
	if err != nil {
		return nil, nil, false, fmt.Errorf("source: %w", err)
	}
	dst, err = s.connections.SessionFor(ctx, req.DestBookmarkID)
	if err != nil {
		return nil, nil, false, fmt.Errorf("destination: %w", err)
	}
	detail, err := dst.ContainerDetail(ctx, req.DestContainer)
	if err != nil {
		return nil, nil, false, fmt.Errorf("destination container %q: %w", req.DestContainer, err)
	}
	versioned = detail.Versioning.Mode != swiftcontrol.VersioningOff
	return src, dst, versioned, nil
}

// PlanCopy runs the §15.2 merge-join for the preview — counts only, no
// writes, no audit (planning is a read).
func (s *TransferService) PlanCopy(ctx context.Context, req CopyRequestDTO) (*CopyPlanDTO, error) {
	src, dst, versioned, err := s.resolve(ctx, req)
	if err != nil {
		return nil, err
	}
	largeConservative := false
	summary, err := plan.Copy(ctx,
		src.Objects(ctx, req.SourceContainer, swiftcontrol.ObjectListOptions{Deep: true, Prefix: req.SourcePrefix}),
		dst.Objects(ctx, req.DestContainer, swiftcontrol.ObjectListOptions{Deep: true, Prefix: req.DestPrefix}),
		req.SourcePrefix, req.DestPrefix, func(item plan.Item) error {
			if item.Reason == plan.ReasonLargeObject {
				largeConservative = true
			}
			return nil
		})
	if err != nil {
		return nil, err
	}
	return &CopyPlanDTO{
		Summary:              summary,
		VersionedDestination: versioned,
		LargeObjectsAsDLO:    largeConservative,
	}, nil
}

// buildEngine constructs the pinned rclone engine for both endpoints.
func (s *TransferService) buildEngine(ctx context.Context, req CopyRequestDTO, src, dst *swiftcontrol.Session) (transfercopy.Engine, error) {
	srcInsecure, err := s.connections.TLSInsecure(ctx, req.SourceBookmarkID)
	if err != nil {
		return nil, err
	}
	dstInsecure, err := s.connections.TLSInsecure(ctx, req.DestBookmarkID)
	if err != nil {
		return nil, err
	}
	remotes := []rclone.Remote{
		{Name: "aeronautes_src", StorageURL: src.StorageURL(), Token: src.AuthToken(), InsecureTLS: srcInsecure},
		{Name: "aeronautes_dst", StorageURL: dst.StorageURL(), Token: dst.AuthToken(), InsecureTLS: dstInsecure},
	}
	if s.engineFactory != nil {
		return s.engineFactory(remotes)
	}
	runner, err := rclone.NewRunner(rclone.Options{})
	if err != nil {
		return nil, err
	}
	return transfercopy.BindRunner(runner, remotes), nil
}

// StartCopy creates the job and launches execution in the background;
// the returned job id is the handle for JobService and CancelJob.
func (s *TransferService) StartCopy(ctx context.Context, req CopyRequestDTO) (string, error) {
	src, dst, _, err := s.resolve(ctx, req)
	if err != nil {
		return "", err
	}
	engine, err := s.buildEngine(ctx, req, src, dst)
	if err != nil {
		return "", fmt.Errorf("transfer engine: %w", err)
	}

	job, err := s.jobs.Create(ctx, jobs.Job{
		Type:             jobs.TypeCopy,
		Source:           req.SourceContainer + "/" + req.SourcePrefix,
		Destination:      req.DestContainer + "/" + req.DestPrefix,
		SourceBookmarkID: req.SourceBookmarkID,
		DestBookmarkID:   req.DestBookmarkID,
		Engine:           "rclone+native",
	})
	if err != nil {
		return "", err
	}

	jobCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	s.mu.Lock()
	s.cancels[job.ID] = cancel
	s.mu.Unlock()

	s.record(ctx, req.SourceBookmarkID, "transfer.copy.start",
		job.Source+" → "+job.Destination, "job "+job.ID, audit.ResultApplied)
	go s.runCopy(jobCtx, job.ID, req, src, dst, engine)
	return job.ID, nil
}

// runCopy is the job goroutine: Planning → Running (single-pass plan
// streamed straight into the executor — the plan is advisory either way,
// §15.3, so the planned counters grow live rather than costing a second
// full listing pass) → Verifying → terminal, with reconciliation on
// every exit path.
func (s *TransferService) runCopy(ctx context.Context, jobID string, req CopyRequestDTO,
	src, dst *swiftcontrol.Session, engine transfercopy.Engine) {

	background := context.WithoutCancel(ctx) // store/reconcile writes survive cancel
	transition := func(state jobs.State) {
		if err := s.jobs.Transition(background, jobID, state); err != nil {
			log.Printf("job %s: %v", jobID, err)
		}
	}
	finish := func(state jobs.State, message string) {
		if message != "" {
			_ = s.jobs.SetError(background, jobID, message)
		}
		transition(state)
		s.record(background, req.SourceBookmarkID, "transfer.copy."+string(state),
			req.SourceContainer+"/"+req.SourcePrefix+" → "+req.DestContainer+"/"+req.DestPrefix,
			"job "+jobID, terminalAuditResult(state))
		s.mu.Lock()
		delete(s.cancels, jobID)
		s.mu.Unlock()
	}

	transition(jobs.StatePlanning)

	counters := jobs.Counters{}
	var countersMu sync.Mutex
	lastFlush := time.Time{}
	flush := func(force bool) {
		countersMu.Lock()
		snapshot := counters
		now := time.Now()
		flushable := force || now.Sub(lastFlush) >= 500*time.Millisecond
		if flushable {
			lastFlush = now
		}
		countersMu.Unlock()
		if flushable {
			_ = s.jobs.UpdateCounters(background, jobID, snapshot)
		}
	}

	executor, err := transfercopy.New(transfercopy.Config{
		Source: src, Dest: dst, Engine: engine,
		SrcSpec: "aeronautes_src:" + req.SourceContainer, DstSpec: "aeronautes_dst:" + req.DestContainer,
		SrcContainer: req.SourceContainer, DstContainer: req.DestContainer,
		SrcPrefix: req.SourcePrefix, DstPrefix: req.DestPrefix,
		JobID: jobID,
		OnItem: func(outcome transfercopy.ItemOutcome) {
			countersMu.Lock()
			switch outcome.Outcome {
			case "copied":
				counters.ObjectsCopied++
			case "failed":
				counters.ObjectsFailed++
			}
			countersMu.Unlock()
			flush(false)
		},
	})
	if err != nil {
		finish(jobs.StateFailed, err.Error())
		return
	}
	transition(jobs.StateRunning)

	planned := func(item plan.Item) error {
		countersMu.Lock()
		switch item.Verdict {
		case plan.VerdictNew, plan.VerdictChanged, plan.VerdictUnknown:
			counters.ObjectsPlanned++
			counters.BytesPlanned += item.SourceBytes
		}
		countersMu.Unlock()
		return executor.Handle(ctx, item)
	}

	_, planErr := plan.Copy(ctx,
		src.Objects(ctx, req.SourceContainer, swiftcontrol.ObjectListOptions{Deep: true, Prefix: req.SourcePrefix}),
		dst.Objects(ctx, req.DestContainer, swiftcontrol.ObjectListOptions{Deep: true, Prefix: req.DestPrefix}),
		req.SourcePrefix, req.DestPrefix, planned)
	report, finishErr := executor.Finish(ctx)
	if planErr == nil {
		planErr = finishErr
	}

	countersMu.Lock()
	counters.ObjectsCopied = report.Copied
	counters.BytesCopied = report.BytesCopied
	counters.Overwrites = report.Overwrites
	counters.NativeRouted = report.NativeRouted
	counters.ObjectsFailed = int64(len(report.Failures))
	countersMu.Unlock()
	flush(true)

	// Reconciliation runs on EVERY exit path (§26.4: after graceful
	// cancellation too, never only after hard interruption).
	if reconcile, err := executor.Reconcile(background, report); err != nil {
		log.Printf("job %s reconcile: %v", jobID, err)
	} else if reconcile.SweptStaged > 0 || reconcile.SweptSegments > 0 {
		log.Printf("job %s reconcile: swept %d staged, %d segments", jobID, reconcile.SweptStaged, reconcile.SweptSegments)
	}

	if planErr != nil {
		if errors.Is(planErr, context.Canceled) {
			finish(jobs.StateCancelled, "")
		} else {
			finish(jobs.StateFailed, planErr.Error())
		}
		return
	}

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
	flush(true)

	switch {
	case len(report.Failures) > 0 || len(verify.Failures) > 0 || len(report.Conflicts) > 0:
		parts := []string{}
		if n := len(report.Failures); n > 0 {
			parts = append(parts, fmt.Sprintf("%d failed (first: %s: %s)", n, report.Failures[0].Name, report.Failures[0].Err))
		}
		if n := len(verify.Failures); n > 0 {
			parts = append(parts, fmt.Sprintf("%d unverified (first: %s)", n, verify.Failures[0].Name))
		}
		if n := len(report.Conflicts); n > 0 {
			parts = append(parts, fmt.Sprintf("%d conflicts (destination changed externally)", n))
		}
		finish(jobs.StatePartial, strings.Join(parts, "; "))
	default:
		finish(jobs.StateComplete, "")
	}
}

func terminalAuditResult(state jobs.State) audit.Result {
	switch state {
	case jobs.StateComplete:
		return audit.ResultApplied
	case jobs.StatePartial:
		return audit.ResultVerifyMismatch
	default:
		return audit.ResultFailed
	}
}

// CancelJob requests graceful cancellation (§26.4: the engine child gets
// SIGINT and cleans up; reconciliation then sweeps).
func (s *TransferService) CancelJob(ctx context.Context, jobID string) error {
	s.mu.Lock()
	cancel, ok := s.cancels[jobID]
	s.mu.Unlock()
	if !ok {
		return fmt.Errorf("job %s is not running in this process", jobID)
	}
	cancel()
	s.record(ctx, "", "transfer.cancel-requested", "job "+jobID, "operator cancel", audit.ResultApplied)
	return nil
}

func (s *TransferService) record(ctx context.Context, bookmarkID, operation, resource, summary string, result audit.Result) {
	if bookmarkID == "" {
		bookmarkID = s.connections.ActiveBookmarkID()
	}
	if err := s.audit.Append(ctx, audit.Event{
		BookmarkID: bookmarkID,
		Operation:  operation,
		Resource:   resource,
		Summary:    summary,
		Result:     result,
	}); err != nil {
		log.Printf("AUDIT WRITE FAILED for %s %s: %v", operation, resource, err)
	}
}

// JobDTO is one §46 job for the drawer.
type JobDTO struct {
	ID               string        `json:"id"`
	Type             string        `json:"type"`
	State            string        `json:"state"`
	Source           string        `json:"source"`
	Destination      string        `json:"destination"`
	SourceBookmarkID string        `json:"sourceBookmarkId"`
	DestBookmarkID   string        `json:"destBookmarkId"`
	CreatedAt        string        `json:"createdAt"`
	StartedAt        string        `json:"startedAt"`
	FinishedAt       string        `json:"finishedAt"`
	Engine           string        `json:"engine"`
	Counters         jobs.Counters `json:"counters"`
	Error            string        `json:"error"`
}

// JobService is the read-only §47 drawer surface.
type JobService struct {
	jobs *jobs.Store
}

// NewJobService builds the service.
func NewJobService(jobStore *jobs.Store) *JobService {
	return &JobService{jobs: jobStore}
}

// List returns jobs newest first; bookmarkID "" means all, matching
// either end otherwise (§47 owner input: filter by account).
func (s *JobService) List(ctx context.Context, bookmarkID string, activeOnly bool, limit int) ([]JobDTO, error) {
	list, err := s.jobs.List(ctx, jobs.Filter{BookmarkID: bookmarkID, ActiveOnly: activeOnly, Limit: limit})
	if err != nil {
		return nil, err
	}
	out := make([]JobDTO, 0, len(list))
	for _, j := range list {
		out = append(out, jobDTO(j))
	}
	return out, nil
}

// Get returns one job.
func (s *JobService) Get(ctx context.Context, jobID string) (*JobDTO, error) {
	j, err := s.jobs.Get(ctx, jobID)
	if err != nil {
		return nil, err
	}
	dto := jobDTO(j)
	return &dto, nil
}

func jobDTO(j jobs.Job) JobDTO {
	formatTime := func(t time.Time) string {
		if t.IsZero() {
			return ""
		}
		return t.UTC().Format(time.RFC3339)
	}
	return JobDTO{
		ID: j.ID, Type: string(j.Type), State: string(j.State),
		Source: j.Source, Destination: j.Destination,
		SourceBookmarkID: j.SourceBookmarkID, DestBookmarkID: j.DestBookmarkID,
		CreatedAt: formatTime(j.CreatedAt), StartedAt: formatTime(j.StartedAt),
		FinishedAt: formatTime(j.FinishedAt),
		Engine:     j.Engine, Counters: j.Counters, Error: j.Error,
	}
}
