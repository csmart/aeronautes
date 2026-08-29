package app

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"

	"github.com/csmart/aeronautes/internal/audit"
	"github.com/csmart/aeronautes/internal/deletion"
	"github.com/csmart/aeronautes/internal/evidence"
	"github.com/csmart/aeronautes/internal/jobs"
	"github.com/csmart/aeronautes/internal/swiftcontrol"
)

// DeleteService is the destructive-jobs surface (ADR 0011, §31–§33):
// preview → typed confirmation → native execution as a §46 job. All
// engines are Aeronautes-native and manifest-only; the confirmation is
// validated in Go against the exact scope, never trusted from the UI.
type DeleteService struct {
	connections *Connections
	jobs        *jobs.Store
	runner      *jobRunner
	evidence    *evidence.Store
}

// NewDeleteService wires the service over the shared runner.
func NewDeleteService(connections *Connections, jobStore *jobs.Store, runner *jobRunner, evidenceStore *evidence.Store) *DeleteService {
	return &DeleteService{connections: connections, jobs: jobStore, runner: runner, evidence: evidenceStore}
}

// DeleteMode selects the §65.5 item-2 operation.
type DeleteMode string

const (
	// ModePrefix deletes every object under a non-empty prefix.
	ModePrefix DeleteMode = "prefix"
	// ModeEmpty deletes all contents, preserving the container (§33).
	ModeEmpty DeleteMode = "empty"
	// ModeDeleteContainer empties, then deletes the container itself.
	ModeDeleteContainer DeleteMode = "delete_container"
)

// DeleteRequestDTO scopes one destructive job.
type DeleteRequestDTO struct {
	BookmarkID string     `json:"bookmarkId"`
	Container  string     `json:"container"`
	Prefix     string     `json:"prefix"`
	Mode       DeleteMode `json:"mode"`
	// Confirm must equal the container name (typed confirmation, §28.2);
	// checked at Start, not Preview.
	Confirm string `json:"confirm"`
}

// DeletePreviewDTO is the §32 optional count/preview step.
type DeletePreviewDTO struct {
	Objects int64 `json:"objects"`
	Bytes   int64 `json:"bytes"`
	// PossibleManifests counts SLO-listed and zero-byte rows: deletion
	// is manifest-only, so their segments (if any) will remain as
	// recoverable orphans unless separately owned and cleaned.
	PossibleManifests int64 `json:"possibleManifests"`
	// Strategy is what execution would select right now.
	Strategy string `json:"strategy"`
}

func (s *DeleteService) validate(req DeleteRequestDTO) error {
	if req.BookmarkID == "" || req.Container == "" {
		return errors.New("bookmark and container are required")
	}
	switch req.Mode {
	case ModePrefix:
		if req.Prefix == "" || !strings.HasSuffix(req.Prefix, "/") {
			return errors.New("prefix mode needs a non-empty prefix ending with '/'")
		}
	case ModeEmpty, ModeDeleteContainer:
		if req.Prefix != "" {
			return fmt.Errorf("%s mode takes no prefix", req.Mode)
		}
	default:
		return fmt.Errorf("unknown delete mode %q", req.Mode)
	}
	return nil
}

// PreviewDelete counts the scope (a full listing pass — §28.3's honest
// cost) without writing anything.
func (s *DeleteService) PreviewDelete(ctx context.Context, req DeleteRequestDTO) (*DeletePreviewDTO, error) {
	if err := s.validate(req); err != nil {
		return nil, err
	}
	session, err := s.connections.SessionFor(ctx, req.BookmarkID)
	if err != nil {
		return nil, err
	}
	preview := &DeletePreviewDTO{Strategy: string(deletion.StrategyParallel)}
	if capabilities := s.connections.Capabilities(); capabilities != nil && capabilities.BulkDeleteMaxDeletes() > 0 {
		preview.Strategy = string(deletion.StrategyBulk)
	}
	for entry, err := range session.Objects(ctx, req.Container, swiftcontrol.ObjectListOptions{Deep: true, Prefix: req.Prefix}) {
		if err != nil {
			return nil, err
		}
		if object, ok := entry.(swiftcontrol.ObjectEntry); ok {
			preview.Objects++
			preview.Bytes += object.Bytes
			if object.SLOListed || object.Bytes == 0 {
				preview.PossibleManifests++
			}
		}
	}
	return preview, nil
}

// StartDelete validates the typed confirmation and launches the job.
func (s *DeleteService) StartDelete(ctx context.Context, req DeleteRequestDTO) (string, error) {
	if err := s.validate(req); err != nil {
		return "", err
	}
	if req.Confirm != req.Container {
		return "", fmt.Errorf("confirmation %q does not match the container name — type it exactly", req.Confirm)
	}
	session, err := s.connections.SessionFor(ctx, req.BookmarkID)
	if err != nil {
		return "", err
	}
	// The capability snapshot steers strategy; fetch through the session
	// so a non-active bookmark still gets one.
	capabilities, err := session.FetchCapabilities(ctx)
	if err != nil {
		return "", err
	}

	jobType := map[DeleteMode]jobs.Type{
		ModePrefix: jobs.TypeDeletePrefix, ModeEmpty: jobs.TypeEmpty, ModeDeleteContainer: jobs.TypeDeleteCont,
	}[req.Mode]
	job, err := s.jobs.Create(ctx, jobs.Job{
		Type:             jobType,
		Source:           req.Container + "/" + req.Prefix,
		Destination:      "(deleted)",
		SourceBookmarkID: req.BookmarkID,
		Engine:           "native",
	})
	if err != nil {
		return "", err
	}
	jobCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	s.runner.register(job.ID, cancel)
	s.runner.record(ctx, req.BookmarkID, "delete."+string(req.Mode)+".start",
		req.Container+"/"+req.Prefix, "job "+job.ID, audit.ResultApplied)

	s.runner.launch(jobCtx, job.ID, req.BookmarkID, func(runCtx context.Context) {
		s.runDelete(runCtx, job.ID, req, session, capabilities)
	})
	return job.ID, nil
}

func (s *DeleteService) runDelete(ctx context.Context, jobID string, req DeleteRequestDTO,
	session *swiftcontrol.Session, capabilities *swiftcontrol.Capabilities) {

	background := context.WithoutCancel(ctx)
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
		s.runner.record(background, req.BookmarkID, "delete."+string(req.Mode)+"."+string(state),
			req.Container+"/"+req.Prefix, "job "+jobID, terminalAuditResult(state))
		s.runner.release(jobID)
	}

	// Planning: count the scope for honest progress.
	transition(jobs.StatePlanning)
	counters := jobs.Counters{}
	for entry, err := range session.Objects(ctx, req.Container, swiftcontrol.ObjectListOptions{Deep: true, Prefix: req.Prefix}) {
		if err != nil {
			if errors.Is(err, context.Canceled) {
				finish(jobs.StateCancelled, "")
			} else {
				finish(jobs.StateFailed, "planning: "+err.Error())
			}
			return
		}
		if object, ok := entry.(swiftcontrol.ObjectEntry); ok {
			counters.ObjectsPlanned++
			counters.BytesPlanned += object.Bytes
		}
	}
	_ = s.jobs.UpdateCounters(background, jobID, counters)

	// §33 acceptance baseline for the container-preserving modes.
	var before *swiftcontrol.ContainerDetail
	if req.Mode == ModeEmpty {
		var err error
		before, err = session.ContainerDetail(ctx, req.Container)
		if err != nil {
			finish(jobs.StateFailed, "container baseline: "+err.Error())
			return
		}
	}

	transition(jobs.StateRunning)
	result, err := deletion.DeletePrefix(ctx, session, capabilities, req.Container, req.Prefix, deletion.Options{
		OnProgress: func(deleted, failed int64) {
			counters.ObjectsCopied = deleted // "processed" for the drawer's n/planned display
			counters.ObjectsFailed = failed
			_ = s.jobs.UpdateCounters(background, jobID, counters)
		},
	})
	if result != nil {
		counters.ObjectsCopied = result.Deleted
		counters.ObjectsFailed = int64(len(result.Failures))
		counters.ObjectsVerified = result.Deleted
	}
	_ = s.jobs.UpdateCounters(background, jobID, counters)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			finish(jobs.StateCancelled, "")
		} else {
			finish(jobs.StateFailed, err.Error())
		}
		return
	}
	// §32/§45: the chosen strategy and verification policy are never a
	// silent shortcut — they land on the job as effective configuration.
	settings := fmt.Sprintf(`{"strategy":%q,"verify":%q,"passes":%d}`, result.Strategy, result.VerifyPolicy, result.Passes)
	_ = s.jobs.SetSettings(background, jobID, settings)

	transition(jobs.StateVerifying)
	problems := []string{}
	if len(result.Failures) > 0 {
		problems = append(problems, fmt.Sprintf("%d deletes failed (first: %s: %s)",
			len(result.Failures), result.Failures[0].Name, result.Failures[0].Err))
	}
	if result.Remaining > 0 {
		problems = append(problems, "objects still listed after the final pass (concurrent writer or listing lag)")
	}

	if req.Mode == ModeEmpty && before != nil {
		after, err := session.ContainerDetail(background, req.Container)
		if err != nil {
			problems = append(problems, "config re-check failed: "+err.Error())
		} else if diff := deletion.SnapshotConfig(before).Diff(deletion.SnapshotConfig(after)); diff != "" {
			problems = append(problems, "container config changed during empty: "+diff)
		}
	}
	if req.Mode == ModeDeleteContainer && len(problems) == 0 {
		if err := session.DeleteContainer(ctx, req.Container); err != nil {
			problems = append(problems, "container delete: "+err.Error())
		} else if _, err := session.ContainerDetail(background, req.Container); !swiftcontrol.IsNotFound(err) {
			problems = append(problems, "container still answers after DELETE")
		}
	}

	if len(problems) > 0 {
		finish(jobs.StatePartial, strings.Join(problems, "; "))
		return
	}
	finish(jobs.StateComplete, "")
}
