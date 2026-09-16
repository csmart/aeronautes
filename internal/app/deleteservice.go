package app

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"

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
	if err := refuseIfDeferred("DeleteService.PreviewDelete"); err != nil {
		return nil, err
	}
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
	if err := refuseIfDeferred("DeleteService.StartDelete"); err != nil {
		return "", err
	}
	if err := s.validate(req); err != nil {
		return "", err
	}
	if req.Confirm != req.Container {
		return "", fmt.Errorf("confirmation %q does not match the container name — type it exactly", req.Confirm)
	}
	owner, ctx, err := s.runner.admit(ctx)
	if err != nil {
		return "", err
	}
	defer owner.finishStart()
	releasePins, err := s.connections.pinForJob(ctx, req.BookmarkID)
	if err != nil {
		return "", err
	}
	defer func() {
		if releasePins != nil {
			releasePins()
		}
	}()
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
	s.runner.register(job.ID, owner.cancel)
	s.runner.record(ctx, req.BookmarkID, "delete."+string(req.Mode)+".start",
		req.Container+"/"+req.Prefix, "job "+job.ID, audit.ResultApplied)

	owner.launch(job.ID, req.BookmarkID, releasePins, func(runCtx context.Context) {
		s.runDelete(runCtx, job.ID, req, session, capabilities)
	}, nil)
	releasePins = nil
	return job.ID, nil
}

func (s *DeleteService) runDelete(ctx context.Context, jobID string, req DeleteRequestDTO,
	session *swiftcontrol.Session, capabilities *swiftcontrol.Capabilities) {

	background := context.WithoutCancel(ctx)
	counters := jobs.Counters{}
	transition := func(state jobs.State) {
		if err := s.jobs.Transition(background, jobID, state); err != nil {
			log.Printf("job %s: %v", jobID, err)
		}
	}
	finish := func(state jobs.State, message string) {
		// Deregister BEFORE the terminal state becomes visible: a job
		// that reads terminal must no longer be cancel-addressable.
		s.runner.release(jobID)
		summary := "job " + jobID
		if counters.DeletionAccountingIncomplete {
			summary += "; deletion accounting incomplete"
		}
		if err := s.jobs.Finish(background, jobID, jobs.Completion{
			State: state, Counters: counters, Error: message,
			Audit: audit.Event{BookmarkID: req.BookmarkID,
				Operation: "delete." + string(req.Mode) + "." + string(state),
				Resource:  req.Container + "/" + req.Prefix, Summary: summary, Result: terminalAuditResult(state)},
		}, s.runner.audit); err != nil {
			log.Printf("job %s: finish deletion: %v", jobID, err)
		}
	}

	// Planning: count the scope for honest progress.
	transition(jobs.StatePlanning)
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
	recordFailure, closeSpill := failureSpill(jobID)
	// Unresolved names are a different public fact from reported
	// failures and get their own retained set, never folded into the
	// failure count and never presented as a deletion.
	recordUnresolved, closeUnresolvedRetention := unresolvedRetention(jobID)
	var countersMu sync.Mutex
	result, err := deletion.DeletePrefix(ctx, session, capabilities, req.Container, req.Prefix, deletion.Options{
		OnProgress: func(deleted, failed int64) {
			countersMu.Lock()
			counters.ObjectsCopied = deleted // "processed" for the drawer's n/planned display
			counters.ObjectsFailed = failed
			snapshot := counters
			countersMu.Unlock()
			_ = s.jobs.UpdateCounters(background, jobID, snapshot)
		},
		OnFailure:    recordFailure,
		OnUnresolved: recordUnresolved,
	})
	spillPath, failedTotal := closeSpill()
	unresolvedPath, _, retentionProblem := closeUnresolvedRetention()
	if result != nil {
		// Only a validated reply's aggregate reaches these counters, so
		// an untrusted reply can no longer publish claimed deletions as
		// verified beside the same names as failures. These remain the
		// service's report rather than an independent observation of the
		// store.
		countersMu.Lock()
		counters.ObjectsCopied = result.Deleted
		counters.ObjectsFailed = result.Failed
		counters.ObjectsVerified = result.Deleted
		counters.DeletionAccountingIncomplete = result.AccountingIncomplete
		countersMu.Unlock()
	}
	if result != nil && result.AccountingIncomplete {
		reason := incompleteDeletionReason(result)
		if err != nil {
			reason += "; " + err.Error()
		}
		if result.Failed > 0 && spillPath != "" {
			reason += "; partial reported-failure observations: " + spillPath
		}
		if result.Unresolved > 0 && unresolvedPath != "" {
			reason += "; partial unresolved observations: " + unresolvedPath
		}
		if retentionProblem != "" {
			reason += "; " + retentionProblem
		}
		finish(jobs.StatePartial, reason)
		return
	}
	// A name whose effect the reply did not establish is neither a
	// deletion nor a proven failure, and it must reach the operator on
	// every terminal path, including the one an error takes.
	unresolvedDetail := ""
	if result != nil && result.Unresolved > 0 {
		unresolvedDetail = fmt.Sprintf("%d names have an unresolved effect: the reply did not establish whether the object was destroyed (first: %s)",
			result.Unresolved, result.Unresolveds[0].Name)
		if unresolvedPath != "" {
			unresolvedDetail += "; full list: " + unresolvedPath
		}
		if retentionProblem != "" {
			// The count and the bounded sample are all that survived, and
			// the operator is told so rather than shown a ledger that
			// reads as complete.
			unresolvedDetail += "; " + retentionProblem
		}
	}
	// A name a later listing offered again was not requested again by
	// this job: whatever stands there now was never part of its consent.
	if result != nil && result.NotRequestedAgain > 0 && len(result.NotRequestedAgainNames) > 0 {
		detail := fmt.Sprintf("%d names were still listed after this job's only request for them and were not requested again (first: %s)",
			result.NotRequestedAgain, result.NotRequestedAgainNames[0])
		if unresolvedDetail == "" {
			unresolvedDetail = detail
		} else {
			unresolvedDetail += "; " + detail
		}
	}
	withUnresolved := func(reason string) string {
		switch {
		case unresolvedDetail == "":
			return reason
		case reason == "":
			return unresolvedDetail
		default:
			return reason + "; " + unresolvedDetail
		}
	}
	if err != nil {
		// Once anything was irreversibly deleted, an interruption is
		// PARTIAL with the exact count — never a plain cancel/fail
		// that understates what happened (review F-10).
		deleted := int64(0)
		if result != nil {
			deleted = result.Deleted
		}
		switch {
		// An unresolved effect is not zero effect: crediting no deletion
		// does not establish that none landed, so the run is partial
		// whatever else happened to it.
		case unresolvedDetail != "" && errors.Is(err, context.Canceled):
			finish(jobs.StatePartial, withUnresolved(fmt.Sprintf("cancelled after %d credited deletions", deleted)))
		case unresolvedDetail != "":
			finish(jobs.StatePartial, withUnresolved(fmt.Sprintf("stopped after %d credited deletions: %v", deleted, err)))
		case deleted > 0 && errors.Is(err, context.Canceled):
			finish(jobs.StatePartial, fmt.Sprintf("cancelled after %d deletions", deleted))
		case deleted > 0:
			finish(jobs.StatePartial, fmt.Sprintf("stopped after %d deletions: %v", deleted, err))
		case errors.Is(err, context.Canceled):
			finish(jobs.StateCancelled, "")
		default:
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
	if result.Failed > 0 {
		detail := fmt.Sprintf("%d deletes failed (first: %s: %s)",
			result.Failed, result.Failures[0].Name, result.Failures[0].Err)
		if spillPath != "" {
			detail += "; full list: " + spillPath
		}
		problems = append(problems, detail)
	}
	_ = failedTotal
	if unresolvedDetail != "" {
		// A run holding an unresolved name has not completed.
		problems = append(problems, unresolvedDetail)
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

// unresolvedRetention keeps the complete set of names whose effect a
// run could not state. It is opened before any destructive request, so
// a run cannot discover at the end that it had nowhere to put the list,
// and it reports its own failures instead of degrading to a count in
// silence: a ledger that quietly loses its identities is not a ledger.
func unresolvedRetention(jobID string) (record func(name, detail string), closeRetention func() (path string, count int64, problem string)) {
	dir, err := os.UserCacheDir()
	if err != nil {
		dir = os.TempDir()
	}
	dir = filepath.Join(dir, "aeronautes", "unresolved")
	var (
		mu            sync.Mutex
		count         int64
		file          *os.File
		writer        *bufio.Writer
		enc           *json.Encoder
		storageErrors int64
		openErr       error
	)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		openErr = err
	} else if f, err := os.OpenFile(filepath.Join(dir, jobID+".jsonl"), os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600); err != nil {
		openErr = err
	} else {
		file = f
		writer = bufio.NewWriter(f)
		enc = json.NewEncoder(writer)
	}
	record = func(name, detail string) {
		mu.Lock()
		defer mu.Unlock()
		count++
		if enc == nil {
			return
		}
		if err := enc.Encode(map[string]string{"name": name, "unresolved": detail}); err != nil {
			storageErrors++
		}
	}
	closeRetention = func() (string, int64, string) {
		mu.Lock()
		defer mu.Unlock()
		// A buffered write that fails at the flush loses records the
		// encoder already accepted, so a storage error anywhere means the
		// artifact cannot be offered as the complete list, and the number
		// of identities lost is not something this recorder can state.
		if writer != nil {
			if err := writer.Flush(); err != nil {
				storageErrors++
			}
		}
		if file != nil {
			if err := file.Close(); err != nil {
				storageErrors++
			}
		}
		switch {
		case openErr != nil:
			return "", count, "the complete unresolved list could not be retained: " + openErr.Error()
		case file == nil:
			return "", count, "the complete unresolved list could not be retained"
		case storageErrors > 0:
			return "", count, fmt.Sprintf(
				"the retained unresolved list is incomplete: %d storage errors occurred while writing it, and how many identities were lost is not established",
				storageErrors)
		}
		return file.Name(), count, ""
	}
	return record, closeRetention
}
