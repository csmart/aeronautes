package app

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/csmart/aeronautes/internal/applog"
	"github.com/csmart/aeronautes/internal/audit"
	"github.com/csmart/aeronautes/internal/evidence"
	"github.com/csmart/aeronautes/internal/jobs"
	"github.com/csmart/aeronautes/internal/swiftcontrol"
)

// DeleteService is the destructive-jobs surface (ADR 0011, §31–§33):
// preview → typed confirmation → native execution as a §46 job. All
// engines are Aeronautes-native and manifest-only, unless the request is
// the operator's explicit force that takes segments too (ADR 0119); the
// confirmation is validated in Go against the exact scope, never trusted
// from the UI.
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
	// Confirm must equal deleteConfirmation's text (typed confirmation,
	// §28.2); checked at Start, not Preview.
	Confirm string `json:"confirm"`
	// Segments asks that each large object in the scope lose its segments
	// too, after its manifest (ADR 0119). It is the operator's explicit force
	// for this one job: the confirmation names it, and without it the
	// deletion is manifest-only as before.
	Segments bool `json:"segments"`
}

// DeletePreviewDTO is the §32 optional count/preview step.
type DeletePreviewDTO struct {
	Objects int64 `json:"objects"`
	Bytes   int64 `json:"bytes"`
	// PossibleManifests counts SLO-listed and zero-byte rows: deletion
	// is manifest-only unless the request takes segments, so their
	// segments (if any) will otherwise remain as recoverable orphans.
	PossibleManifests int64 `json:"possibleManifests"`
	// RequestCost is the object requests the sweep makes for the scope,
	// not counting its listings: one DELETE for each member (ADR 0122
	// decision 6). A sweep that takes segments also reads each possible
	// manifest once and deletes the segments it finds, which a listing
	// cannot count beforehand.
	RequestCost int64 `json:"requestCost"`
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

// deleteConfirmation is what the operator types to arm a deletion (ADR 0095
// decision 7). For a prefix it is the scope as the dialog shows it, because
// typing only the container name would not show which prefix the operator
// read. Each container job is armed by its own verb and the name: emptying
// keeps the container and its settings and deleting destroys them, so the
// words for one never start the other, and the bare name starts neither
// (ADR 0121 decision 3). A deletion that takes segments is armed by other
// words than one that does not, so a confirmation typed for either never
// starts the other (ADR 0119 decision 2).
func deleteConfirmation(req DeleteRequestDTO) string {
	var words string
	switch req.Mode {
	case ModePrefix:
		words = req.Container + "/" + req.Prefix
	case ModeEmpty:
		words = "empty " + req.Container
	case ModeDeleteContainer:
		words = "delete " + req.Container
	}
	if req.Segments {
		return words + " and segments"
	}
	return words
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
	preview.RequestCost = preview.Objects
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

// sweepStrategy names, in the job's effective configuration, what a bulk
// deletion runs (ADR 0095 decision 1, ADR 0122 decision 6): one DELETE for
// each name the listing passes show.
const sweepStrategy = "per-name"

// StartDelete validates the typed confirmation and launches the job: a
// per-name sweep over the scope whose members are durable items of the job
// (ADR 0095, ADR 0122 decision 6). The three modes share it; the
// delete-container mode deletes the container only after a complete sweep.
func (s *DeleteService) StartDelete(ctx context.Context, req DeleteRequestDTO) (string, error) {
	if err := refuseIfDeferred("DeleteService.StartDelete"); err != nil {
		return "", err
	}
	if err := s.validate(req); err != nil {
		return "", err
	}
	if want := deleteConfirmation(req); req.Confirm != want {
		return "", fmt.Errorf("confirmation %q does not match %q; type it exactly", req.Confirm, want)
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
	if req.Segments {
		if err := refuseSegmentsWhereVersionsAreKept(ctx, session, req.Container); err != nil {
			return "", err
		}
	}
	// A sweep deletes every row it lists, and what a running upload or copy
	// is writing is ordinary rows, so the sweep claims what it will list and
	// is refused while a transfer writes inside it (ADR 0105 decision 3).
	if err := owner.claim(sweepClaim(swiftcontrol.AccountIdentity(session.StorageURL()), req.Container, req.Prefix)); err != nil {
		return "", err
	}
	jobType := map[DeleteMode]jobs.Type{
		ModePrefix: jobs.TypeDeletePrefix, ModeEmpty: jobs.TypeEmpty, ModeDeleteContainer: jobs.TypeDeleteCont,
	}[req.Mode]
	job, err := s.jobs.Create(ctx, jobs.Job{
		Type: jobType, ItemRecordsVersion: 1,
		Source: req.Container + "/" + req.Prefix, Destination: "(deleted)", SourceBookmarkID: req.BookmarkID, Engine: "native",
		// §32/§45: the strategy is the job's effective configuration, never a
		// silent shortcut. It is part of the row from its creation, so a
		// destructive job cannot exist without it.
		Settings: fmt.Sprintf(`{"strategy":%q,"passes":%d,"workers":%d,"segments":%t}`,
			sweepStrategy, sweepPasses, sweepWorkers, req.Segments),
	})
	if err != nil {
		return "", err
	}
	s.runner.register(job.ID, owner.cancel)
	s.runner.record(ctx, req.BookmarkID, "delete."+string(req.Mode)+".start",
		req.Container+"/"+req.Prefix, "job "+job.ID, audit.ResultApplied)
	owner.launch(job.ID, req.BookmarkID, func(runCtx context.Context) {
		s.runSweepJob(runCtx, job.ID, req, session, owner)
	}, func(background context.Context) {
		// Cancelled before it ran: nothing was listed, admitted or issued.
		s.finishSweep(background, job.ID, req, jobs.StateCancelled, "", 0)
	})
	return job.ID, nil
}

// runSweepJob is the job goroutine: the listing passes over the scope, then
// the terminal state from the members' answers, then the mode's own step
// (ADR 0095 decisions 5 and 6, ADR 0122 decision 6). A lost record leaves
// the job open with its status unavailable, as the selected set's does.
func (s *DeleteService) runSweepJob(ctx context.Context, jobID string, req DeleteRequestDTO, session *swiftcontrol.Session, owner *jobOwner) {
	background := context.WithoutCancel(ctx)
	for _, state := range []jobs.State{jobs.StatePlanning, jobs.StateRunning} {
		if err := s.jobs.Transition(background, jobID, state); err != nil {
			applog.Errorf("job %s: %v", jobID, err)
		}
	}
	sweep := &prefixSweep{s: s, jobID: jobID, bookmarkID: req.BookmarkID, container: req.Container, prefix: req.Prefix, session: session,
		segments: req.Segments, owner: owner}
	sweep.run(ctx)
	lost := sweep.lostReason()
	if lost == "" {
		lost = s.storeStoppedBeside()
	}
	if lost != "" {
		// The ledger's own latch: the job stays open with its status
		// unavailable, and no container is deleted (ADR 0095 decision 4).
		s.runner.release(jobID)
		s.runner.record(background, req.BookmarkID, "delete."+string(req.Mode)+".unavailable",
			req.Container+"/"+req.Prefix, "job "+jobID+" · "+lost, audit.ResultUnverified)
		applog.Warnf("job %s: %s", jobID, lost)
		return
	}
	state, reason := s.sweepEnd(background, jobID, sweep)
	// A job asked for segments that left some has not done all it was asked,
	// so it is partial and, in the delete-container mode, leaves the
	// container standing (ADR 0119 decision 7).
	if left := sweep.segmentsLeft(); left != "" && (state == jobs.StateComplete || state == jobs.StatePartial) {
		state = jobs.StatePartial
		if reason == "" {
			reason = left
		} else {
			reason += "; " + left
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
			// Only a sweep whose every member was deleted goes on to the
			// container; anything less leaves it standing (decision 6).
			if left := containerDeleteLeft(session.DeleteContainer(background, req.Container)); left != "" {
				state, reason = jobs.StatePartial, left
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

// containerDeleteLeft takes the store's answer to a container's DELETE (ADR
// 0122 decision 6). It is empty when the container is gone: the store
// accepted the DELETE, or answered that there is no such container. Otherwise
// it says why the container may still stand: the store refused, as it does
// for a container that still holds something, or did not answer.
func containerDeleteLeft(err error) string {
	var se *swiftcontrol.SwiftError
	switch {
	case err == nil, swiftcontrol.IsNotFound(err):
		return ""
	case errors.As(err, &se) && se.Kind == swiftcontrol.KindConflict:
		return "the objects listed were deleted, and the store refused to delete the container because it is not empty; run the deletion again"
	case errors.As(err, &se) && se.HTTPStatus != 0 && se.HTTPStatus < 500:
		return fmt.Sprintf("the objects were deleted, and the store refused to delete the container with HTTP %d", se.HTTPStatus)
	}
	return "the objects were deleted, and the store did not answer the container's DELETE or answered with a server error, so nothing says whether the container was deleted: " + err.Error()
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

// sweepEnd derives the terminal state from the members' answers (ADR 0095
// decision 5, ADR 0122 decision 6): complete when every member admitted was
// deleted; cancelled when the job was stopped before any DELETE; failed when
// the listing could not be read before any request, or the store refused
// every member; partial otherwise. Each member is counted once by its own
// outcome. The scope is not listed again to see what stands there: a name
// the listing never showed was never asked for.
func (s *DeleteService) sweepEnd(ctx context.Context, jobID string, sweep *prefixSweep) (jobs.State, string) {
	totals, err := s.jobs.ReadItemTotals(ctx, jobID)
	if err != nil {
		return jobs.StatePartial, "the members' records could not be read: " + err.Error()
	}
	counts := sweep.counts()
	sent := totals.Deleted + totals.Unverified + totals.Failed + totals.Unsettled
	breakdown := fmt.Sprintf("%d of %d members deleted; %d failed, %d unknown", totals.Deleted, totals.Admitted, totals.Failed, totals.Unverified)
	switch {
	case counts.cancelled && sent == 0:
		return jobs.StateCancelled, ""
	case counts.cancelled:
		return jobs.StatePartial, fmt.Sprintf("cancelled after %d of %d members were requested, %d deleted", sent, totals.Admitted, totals.Deleted)
	case counts.listErr != nil && sent == 0:
		return jobs.StateFailed, "the listing could not be read before any request: " + counts.listErr.Error()
	case counts.listErr != nil:
		return jobs.StatePartial, "the listing stopped: " + counts.listErr.Error() + "; " + breakdown
	case totals.Deleted == totals.Admitted:
		return jobs.StateComplete, ""
	case totals.Deleted == 0 && totals.Unverified == 0:
		return jobs.StateFailed, fmt.Sprintf("nothing was deleted: the store refused all %d members", totals.Failed)
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
