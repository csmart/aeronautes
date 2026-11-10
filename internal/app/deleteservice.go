package app

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/csmart/aeronautes/internal/applog"
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
	// ceiling is the operator's retention ceiling, the most members one
	// selected-set deletion admits (ADR 0093 decision 2); nil means the
	// default ceiling.
	ceiling func(context.Context) int
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
	// LaterBaselineRows is how many rows take a HEAD of their own before
	// their comparison, the zero-byte and SLO-listed ones, and RequestCost
	// the object requests the sweep makes for the scope before listing,
	// retries or verification: two per member plus one per such row (ADR
	// 0085, ADR 0095 decision 7).
	LaterBaselineRows int64 `json:"laterBaselineRows"`
	RequestCost       int64 `json:"requestCost"`
	// Strategy is what execution would select right now.
	Strategy string `json:"strategy"`
	// ArchiveOf names the containers whose versions or history location is
	// this container, so the dialog can say whose earlier versions the
	// deletion destroys (ADR 0102 decision 3). ArchiveUnestablished says why
	// the scan could not answer for the whole account, empty when it did.
	ArchiveOf            []string `json:"archiveOf"`
	ArchiveUnestablished string   `json:"archiveUnestablished"`
}

// archiveScanBound caps the container HEADs a preview spends finding who
// archives into the container (ADR 0102 decision 3): a stop against the
// pathological, far above any account the product has been pointed at.
const archiveScanBound = 1000

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
	preview := &DeletePreviewDTO{Strategy: sweepStrategy}
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
	// ADR 0085's cost, stated before the job: one HEAD and one DELETE per
	// member, and a baseline HEAD for each row that cannot say what it holds.
	preview.LaterBaselineRows = preview.PossibleManifests
	preview.RequestCost = 2*preview.Objects + preview.LaterBaselineRows
	// Whose archive this is, which the operator cannot see from inside it.
	// The owners are stored names on a sentence the operator reads before
	// destroying, so they are sanitised and bounded here, as the archive
	// name in the overwrite question is; the match itself used the raw
	// names.
	owners := session.ArchiveOwners(ctx, req.Container, archiveScanBound)
	for _, owner := range owners.Owners {
		preview.ArchiveOf = append(preview.ArchiveOf, titleText(owner, 80))
	}
	preview.ArchiveUnestablished = owners.Unestablished
	return preview, nil
}

// sweepStrategy and sweepVerifyPolicy name, in the job's effective
// configuration, what a bulk deletion runs (ADR 0095 decisions 1 and 5):
// the verified per-name protocol over listing passes, and the one-row
// postcondition after bounded retries.
const (
	sweepStrategy     = "per-name-verified"
	sweepVerifyPolicy = "prefix limit=1 empty after ≤3 passes"
)

// StartDelete validates the typed confirmation and launches the job: a
// verified per-name sweep over the scope whose members are durable items of
// the job (ADR 0095). The three modes share it; the empty mode compares the
// container's configuration before and after, and the delete-container mode
// deletes the container only after a complete sweep.
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
	// The shutdown guard runs before any store or database access, so a start
	// during shutdown is refused cleanly ("job shutdown has begun") instead of
	// surfacing a closed-database or missing-bookmark error from the session
	// lookup, the way every other job entrypoint admits first.
	owner, ctx, err := s.runner.admit(ctx)
	if err != nil {
		return "", err
	}
	defer owner.finishStart()
	session, err := s.connections.SessionFor(ctx, req.BookmarkID)
	if err != nil {
		return "", err
	}
	releasePins, err := s.connections.pinForJob(ctx, req.BookmarkID)
	if err != nil {
		return "", err
	}
	owner.hold(releasePins)
	// A destructive job's startup I/O: confirm the session still has
	// authority to reach the store before any job row exists, so a bookmark
	// whose generation moved is refused here and shutdown can cancel this
	// admitted request with no row left behind, as every job family does.
	if _, err := session.FetchCapabilities(ctx); err != nil {
		return "", err
	}
	// A sweep deletes every row it lists, and another job's stage is ordinary
	// rows, so the sweep claims what it will list and is refused while a
	// transfer is staging inside it (ADR 0105 decision 3).
	if err := owner.claim(sweepClaim(swiftcontrol.AccountIdentity(session.StorageURL()), req.Container, req.Prefix)); err != nil {
		return "", err
	}
	jobType := map[DeleteMode]jobs.Type{
		ModePrefix: jobs.TypeDeletePrefix, ModeEmpty: jobs.TypeEmpty, ModeDeleteContainer: jobs.TypeDeleteCont,
	}[req.Mode]
	job, err := s.jobs.Create(ctx, jobs.Job{
		Type: jobType, ItemRecordsVersion: 1,
		Source: req.Container + "/" + req.Prefix, Destination: "(deleted)", SourceBookmarkID: req.BookmarkID, Engine: "native",
		// §32/§45: the strategy and the postcondition are the job's effective
		// configuration, never a silent shortcut. They are part of the row
		// from its creation, so a destructive job cannot exist without them.
		Settings: fmt.Sprintf(`{"strategy":%q,"verify":%q,"passes":%d,"workers":%d}`,
			sweepStrategy, sweepVerifyPolicy, sweepPasses, sweepWorkers),
	})
	if err != nil {
		return "", err
	}
	s.runner.register(job.ID, owner.cancel)
	s.runner.record(ctx, req.BookmarkID, "delete."+string(req.Mode)+".start",
		req.Container+"/"+req.Prefix, "job "+job.ID, audit.ResultApplied)
	owner.launch(job.ID, req.BookmarkID, func(runCtx context.Context) {
		s.runSweepJob(runCtx, job.ID, req, session)
	}, func(background context.Context) {
		// Cancelled before it ran: nothing was listed, admitted or issued.
		s.finishSweep(background, job.ID, req, jobs.StateCancelled, "", 0)
	})
	return job.ID, nil
}

// runSweepJob is the job goroutine: the listing passes over the scope, then
// the terminal state from the members and the postcondition, then the mode's
// own step (ADR 0095 decisions 5 and 6). A lost record leaves the job open
// with its status unavailable, as the selected set's does.
func (s *DeleteService) runSweepJob(ctx context.Context, jobID string, req DeleteRequestDTO, session *swiftcontrol.Session) {
	background := context.WithoutCancel(ctx)
	for _, state := range []jobs.State{jobs.StatePlanning, jobs.StateRunning} {
		if err := s.jobs.Transition(background, jobID, state); err != nil {
			applog.Errorf("job %s: %v", jobID, err)
		}
	}
	// §33 acceptance baseline for the container-preserving mode; a
	// cancellation this early requested nothing.
	var before *swiftcontrol.ContainerDetail
	if req.Mode == ModeEmpty {
		detail, err := session.ContainerDetail(ctx, req.Container)
		if ctx.Err() != nil {
			s.finishSweep(background, jobID, req, jobs.StateCancelled, "", 0)
			return
		}
		if err != nil {
			s.finishSweep(background, jobID, req, jobs.StateFailed, "container baseline: "+err.Error(), 0)
			return
		}
		before = detail
	}
	sweep := &prefixSweep{s: s, jobID: jobID, bookmarkID: req.BookmarkID, container: req.Container, prefix: req.Prefix, session: session}
	sweep.run(ctx)
	lost := sweep.lostReason()
	if lost == "" {
		lost = s.storeStoppedBeside()
	}
	if lost != "" {
		// The ledger's own latch: the job stays open with its status
		// unavailable, no postcondition is run and no container is deleted
		// (ADR 0095 decision 4).
		s.runner.release(jobID)
		s.runner.record(background, req.BookmarkID, "delete."+string(req.Mode)+".unavailable",
			req.Container+"/"+req.Prefix, "job "+jobID+" · "+lost, audit.ResultUnverified)
		applog.Warnf("job %s: %s", jobID, lost)
		return
	}
	state, reason := s.sweepEnd(background, jobID, req, session, sweep)
	// Emptying a container compares its configuration before and after
	// whenever the sweep reached a terminal with an effect, complete or
	// partial, so a change an external writer made during a partial sweep is
	// still reported (ADR 0095 decision 6). The lost path returned above, so
	// an accounting failure never reaches this follow-up verification.
	if req.Mode == ModeEmpty && before != nil && (state == jobs.StateComplete || state == jobs.StatePartial) {
		configProblem := ""
		if after, err := session.ContainerDetail(background, req.Container); err != nil {
			configProblem = "config re-check failed: " + err.Error()
		} else if diff := deletion.SnapshotConfig(before).Diff(deletion.SnapshotConfig(after)); diff != "" {
			configProblem = "container config changed during empty: " + diff
		}
		if configProblem != "" {
			state = jobs.StatePartial
			if reason == "" {
				reason = configProblem
			} else {
				reason += "; " + configProblem
			}
		}
	}
	if req.Mode == ModeDeleteContainer {
		sent := s.deletionRequested(background, jobID)
		switch {
		case state == jobs.StateComplete && ctx.Err() != nil:
			// A cancellation at this boundary leaves the container standing. It
			// is cancelled only when no destructive request was issued at all,
			// as for an already-empty container; otherwise the objects are gone
			// and it is partial.
			if sent == 0 {
				state, reason = jobs.StateCancelled, ""
			} else {
				state, reason = jobs.StatePartial, "cancelled after the objects were deleted and before the container was"
			}
		case state == jobs.StateComplete:
			// Only a complete sweep with an empty postcondition deletes the
			// container; anything less leaves it standing (decision 6).
			if err := session.DeleteContainer(background, req.Container); err != nil {
				state, reason = jobs.StatePartial, "container delete: "+err.Error()
			} else if _, err := session.ContainerDetail(background, req.Container); !swiftcontrol.IsNotFound(err) {
				state, reason = jobs.StatePartial, "container still answers after DELETE"
			}
		}
	}
	if state == jobs.StateComplete {
		if err := s.jobs.Transition(background, jobID, jobs.StateVerifying); err != nil {
			applog.Errorf("job %s: %v", jobID, err)
		}
	}
	s.finishSweep(background, jobID, req, state, reason, sweep.counts().admitted)
}

// deletionRequested reports how many members had a destructive request sent,
// each counted once by its own outcome; it decides whether a cancellation at
// the container-delete boundary claims any irreversible effect.
func (s *DeleteService) deletionRequested(ctx context.Context, jobID string) int64 {
	totals, err := s.jobs.ReadItemTotals(ctx, jobID)
	if err != nil {
		// A read failure here cannot prove nothing was sent, so the caller
		// treats the boundary as a partial rather than claiming cancelled.
		return 1
	}
	return totals.Deleted + totals.Unverified + totals.Failed + totals.Unsettled
}

// sweepEnd derives the terminal state from the members and the postcondition
// (ADR 0095 decision 5): complete only when every member issued was deleted
// and the scope lists nothing; partial whenever a DELETE was sent and the
// job is not complete; cancelled only before any DELETE; failed when nothing
// was sent, because every member was refused or changed, because the
// listing or the scope could not be read before any request, or because the
// scope held a name this job never listed. Each member is counted once by
// its own outcome. The postcondition runs to its end whatever the job's
// context does now: the sweep's requests are over and this is their
// evidence.
func (s *DeleteService) sweepEnd(ctx context.Context, jobID string, req DeleteRequestDTO, session *swiftcontrol.Session, sweep *prefixSweep) (jobs.State, string) {
	totals, err := s.jobs.ReadItemTotals(ctx, jobID)
	if err != nil {
		return jobs.StatePartial, "the members' records could not be read: " + err.Error()
	}
	counts := sweep.counts()
	sent := totals.Deleted + totals.Unverified + totals.Failed + totals.Unsettled
	breakdown := fmt.Sprintf("%d of %d members deleted; %d refused, %d changed since listed, %d failed, %d unverified",
		totals.Deleted, totals.Admitted, totals.Refused, totals.Conflicts, totals.Failed, totals.Unverified)
	switch {
	case counts.cancelled && sent == 0:
		return jobs.StateCancelled, ""
	case counts.cancelled:
		return jobs.StatePartial, fmt.Sprintf("cancelled after %d of %d members were requested, %d deleted", sent, totals.Admitted, totals.Deleted)
	case counts.listErr != nil && sent == 0:
		return jobs.StateFailed, "the listing could not be read before any request: " + counts.listErr.Error()
	case counts.listErr != nil:
		return jobs.StatePartial, "the listing stopped: " + counts.listErr.Error() + "; " + breakdown
	}
	remaining, err := deletion.VerifyPrefixEmpty(ctx, session, req.Container, req.Prefix)
	switch {
	case err != nil && sent == 0:
		return jobs.StateFailed, "the scope could not be verified and no request was sent: " + err.Error()
	case err != nil:
		return jobs.StatePartial, "the scope could not be verified: " + err.Error() + "; " + breakdown
	case sent == 0 && totals.Admitted > 0:
		return jobs.StateFailed, fmt.Sprintf("nothing was deleted: every member was refused or changed since listed (%d refused, %d changed)", totals.Refused, totals.Conflicts)
	case remaining > 0 && totals.Admitted == 0:
		return jobs.StateFailed, fmt.Sprintf("no request was sent and the scope was not empty at verification (%d row sampled): a name this job never listed stands under it; run the deletion again for fresh authority", remaining)
	case remaining > 0 && counts.heldAgain > 0:
		return jobs.StatePartial, fmt.Sprintf("%s; %d names still listed after this job's only request for them were not requested again", breakdown, counts.heldAgain)
	case remaining > 0:
		return jobs.StatePartial, breakdown + "; a name this job never listed stood under the scope at verification; run the deletion again for fresh authority"
	case totals.Deleted == totals.Admitted:
		return jobs.StateComplete, ""
	default:
		return jobs.StatePartial, breakdown
	}
}

func (s *DeleteService) finishSweep(ctx context.Context, jobID string, req DeleteRequestDTO, state jobs.State, message string, admitted int64) {
	s.runner.release(jobID)
	err := s.jobs.FinishFromItems(ctx, jobID, jobs.Completion{
		State: state, Counters: jobs.Counters{ObjectsPlanned: admitted}, Error: message,
		Audit: audit.Event{BookmarkID: req.BookmarkID, Operation: "delete." + string(req.Mode) + "." + string(state),
			Resource: req.Container + "/" + req.Prefix, Summary: fmt.Sprintf("job %s · %d members", jobID, admitted), Result: terminalAuditResult(state)},
	}, s.runner.audit)
	if err != nil {
		applog.Errorf("job %s terminal outcome was not saved: %v", jobID, err)
	}
}
