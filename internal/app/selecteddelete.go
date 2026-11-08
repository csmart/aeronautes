package app

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/csmart/aeronautes/internal/applog"
	"github.com/csmart/aeronautes/internal/audit"
	"github.com/csmart/aeronautes/internal/jobs"
	"github.com/csmart/aeronautes/internal/swiftcontrol"
)

// SelectedDeleteRequestDTO starts the deletion of the selected objects as one
// job (ADR 0093 decision 2): the scope the view was built under, the
// container, the member rows as the working list carries them, and the
// typed confirmation, which is the number of members.
type SelectedDeleteRequestDTO struct {
	Scope     ListingScopeDTO `json:"scope"`
	Container string          `json:"container"`
	Members   []ListingRowDTO `json:"members"`
	Confirm   string          `json:"confirm"`
}

// selectionCeiling is the most members one selected-set deletion admits:
// the selection ceiling of ADR 0072 decision 4, the operator's download
// object ceiling setting, which ADR 0093 decision 2 reuses for selected
// sets. The shell wires the setting; without it the default applies.
func (s *DeleteService) selectionCeiling(ctx context.Context) int {
	if s.ceiling != nil {
		if n := s.ceiling(ctx); n > 0 {
			return n
		}
	}
	return defaultDownloadObjectCeiling
}

// WireDeleteSettings gives the deletion service the operator's selection
// ceiling, the download object ceiling of ADR 0072 decision 4.
func WireDeleteSettings(s *DeleteService, settings *SettingsService) {
	s.ceiling = func(ctx context.Context) int {
		current, err := settings.Get(ctx)
		if err != nil {
			return 0
		}
		return current.DownloadObjectCeiling
	}
}

// StartSelectedDelete deletes the selected objects as one job whose members
// are each deleted exactly, by the verified per-name protocol (ADR 0093).
// The request is refused before any job row when the scope is empty or has
// moved, when it names no member, a prefix row, an empty or repeated name,
// or more members than the selection ceiling, or when the typed confirmation
// is not the member count. The whole member set is admitted to the ledger
// once, before any request, and a set that cannot be recorded sends nothing.
func (s *DeleteService) StartSelectedDelete(ctx context.Context, req SelectedDeleteRequestDTO) (string, error) {
	if err := refuseIfDeferred("DeleteService.StartSelectedDelete"); err != nil {
		return "", err
	}
	if req.Container == "" {
		return "", errors.New("a selected-set deletion needs its container")
	}
	if len(req.Members) == 0 {
		return "", errors.New("nothing is selected; a selected-set deletion needs at least one object")
	}
	if ceiling := s.selectionCeiling(ctx); len(req.Members) > ceiling {
		return "", fmt.Errorf("%d members exceed the selection ceiling of %d; narrow the selection", len(req.Members), ceiling)
	}
	seen := make(map[string]bool, len(req.Members))
	for _, member := range req.Members {
		switch {
		case member.Kind != "object":
			return "", fmt.Errorf("%q is a %s row, not an object; a prefix is deleted by the bulk deletion that owns it", member.Name, member.Kind)
		case member.Name == "":
			return "", errors.New("a member needs its name; an empty name would address the container itself")
		case seen[member.Name]:
			return "", fmt.Errorf("%q is selected twice; a member is deleted once", member.Name)
		}
		seen[member.Name] = true
	}
	if req.Confirm != strconv.Itoa(len(req.Members)) {
		return "", fmt.Errorf("confirmation %q does not match the number of members, %d; type it exactly", req.Confirm, len(req.Members))
	}
	entry, bookmarkID, _, err := s.connections.boundSnapshot(ctx, req.Scope)
	if err != nil {
		return "", err
	}
	owner, ctx, err := s.runner.admit(ctx)
	if err != nil {
		return "", err
	}
	defer owner.finishStart()
	releasePins, err := s.connections.pinForJob(ctx, bookmarkID)
	if err != nil {
		return "", err
	}
	owner.hold(releasePins)
	job, err := s.jobs.Create(ctx, jobs.Job{
		Type: jobs.TypeDeleteSelected, ItemRecordsVersion: 1,
		Source: req.Container, Destination: "(deleted)", SourceBookmarkID: bookmarkID, Engine: "native",
	})
	if err != nil {
		return "", err
	}
	// Admission once and fail-closed: the ledger takes the set in pages, and
	// a page it cannot record ends the job before any request leaves.
	intents := make([]jobs.ItemIntent, 0, len(req.Members))
	for _, member := range req.Members {
		intents = append(intents, jobs.ItemIntent{
			ItemIdentity: jobs.ItemIdentity{Key: member.Name, Source: jobs.ItemLocation{
				Kind: jobs.ItemSwift, BookmarkID: bookmarkID, Container: req.Container, Object: member.Name}},
			Planned: true, Admitted: true, ListedBytes: member.Bytes, ListedETag: member.ETag, ListedSLO: member.SLOListed,
		})
	}
	for start := 0; start < len(intents); start += 500 {
		end := min(start+500, len(intents))
		if err := s.jobs.AdmitItems(ctx, job.ID, intents[start:end]); err != nil {
			owner.cancel()
			s.finishSelected(context.WithoutCancel(ctx), job.ID, req, bookmarkID, jobs.StateFailed,
				"the members could not be recorded before any request: "+err.Error())
			return "", fmt.Errorf("the members could not be recorded, so nothing was sent: %w", err)
		}
	}
	s.runner.register(job.ID, owner.cancel)
	s.runner.record(ctx, bookmarkID, "delete.selected.start", req.Container,
		fmt.Sprintf("job %s · %d members", job.ID, len(req.Members)), audit.ResultApplied)
	owner.launch(job.ID, bookmarkID, func(runCtx context.Context) {
		s.runSelectedDelete(runCtx, job.ID, req, bookmarkID, entry.session)
	}, func(background context.Context) {
		// Cancelled before it ran: no member was issued, each is recorded as
		// not attempted, and the job is cancelled.
		s.recordNotAttempted(background, job.ID, req.Members)
		s.finishSelected(background, job.ID, req, bookmarkID, jobs.StateCancelled, "")
	})
	return job.ID, nil
}

// runSelectedDelete is the job goroutine: each member in turn, its issued mark
// before its final HEAD, the protocol under a context cancellation does not
// reach, and its outcome recorded with what the verifying read observed;
// cancellation lands between members, and a record that cannot be written
// stops the job with no later member touched (ADR 0093 decisions 3 and 4).
func (s *DeleteService) runSelectedDelete(ctx context.Context, jobID string, req SelectedDeleteRequestDTO, bookmarkID string, session *swiftcontrol.Session) {
	background := context.WithoutCancel(ctx)
	for _, state := range []jobs.State{jobs.StatePlanning, jobs.StateRunning} {
		if err := s.jobs.Transition(background, jobID, state); err != nil {
			applog.Errorf("job %s: %v", jobID, err)
		}
	}
	counters := jobs.Counters{ObjectsPlanned: int64(len(req.Members))}
	var (
		cancelled     bool
		recordingLost string
	)
	for i, member := range req.Members {
		if ctx.Err() != nil {
			cancelled = true
			s.recordNotAttempted(background, jobID, req.Members[i:])
			break
		}
		if err := s.jobs.MarkIssued(background, jobID, member.Name); err != nil {
			recordingLost = fmt.Sprintf("the issued mark for %q could not be written, so it and every later member were not attempted: %v", member.Name, err)
			break
		}
		// The member's own requests run to their end whatever the job's
		// context does: cancellation lands between members, never inside the
		// comparison, the DELETE and the verifying read.
		result, err := session.DeleteMember(background, req.Container, swiftcontrol.MemberRow{
			Name: member.Name, Bytes: member.Bytes, ETag: member.ETag, SLOListed: member.SLOListed})
		observation, successor, baseline := selectedMemberOutcome(result, err)
		if recErr := s.jobs.RecordDeletion(background, jobID, member.Name, observation, successor, baseline); recErr != nil {
			recordingLost = fmt.Sprintf("the outcome of %q could not be recorded after its request, so it is unresolved and no later member was touched: %v", member.Name, recErr)
			break
		}
		counters.ObjectsCopied++
		if observation.Result == jobs.ItemDeleted {
			counters.ObjectsDeleted++
		}
		_ = s.jobs.UpdateCounters(background, jobID, counters)
	}
	if recordingLost != "" {
		// A record that could not be written leaves the job open with its
		// status unavailable, the ledger's own latch: the member in flight is
		// unresolved because its DELETE may have been sent, no later member
		// is touched, nothing is retried, and the reopen rule settles the
		// records when the store is next available (ADR 0093 decision 4). A
		// terminal snapshot written now could not say which members were
		// never requested.
		s.runner.release(jobID)
		s.runner.record(background, bookmarkID, "delete.selected.unavailable", req.Container, "job "+jobID+" · "+recordingLost, audit.ResultUnverified)
		applog.Warnf("job %s: %s", jobID, recordingLost)
		return
	}
	state, reason := s.selectedEnd(background, jobID, cancelled)
	if state == jobs.StateComplete {
		if err := s.jobs.Transition(background, jobID, jobs.StateVerifying); err != nil {
			applog.Errorf("job %s: %v", jobID, err)
		}
	}
	s.finishSelected(background, jobID, req, bookmarkID, state, reason)
}

// selectedEnd derives the terminal state from the members (ADR 0093
// decision 4): complete when every member was deleted; partial whenever a
// DELETE was sent and the job is not complete, because an uncertain effect
// is never presented as no effect; cancelled only before any DELETE was
// sent, whatever refusals preceded it; failed when nothing was sent because
// every member was refused or changed since listed. A DELETE was sent for
// every member deleted, unverified or failed by the store's answer, and may
// have been for one issued and never settled; each member is counted once,
// by its own outcome, and never through a facet that overlaps another.
func (s *DeleteService) selectedEnd(ctx context.Context, jobID string, cancelled bool) (jobs.State, string) {
	totals, err := s.jobs.ReadItemTotals(ctx, jobID)
	if err != nil {
		return jobs.StatePartial, "the members' records could not be read: " + err.Error()
	}
	sent := totals.Deleted + totals.Unverified + totals.Failed + totals.Unsettled
	switch {
	case cancelled && sent == 0:
		return jobs.StateCancelled, ""
	case cancelled:
		return jobs.StatePartial, fmt.Sprintf("cancelled after %d of %d members were requested, %d deleted", sent, totals.Admitted, totals.Deleted)
	case totals.Deleted == totals.Admitted:
		return jobs.StateComplete, ""
	case sent == 0:
		return jobs.StateFailed, fmt.Sprintf("nothing was deleted: every member was refused or changed since listed (%d refused, %d changed)", totals.Refused, totals.Conflicts)
	default:
		return jobs.StatePartial, fmt.Sprintf("%d of %d members deleted; %d refused, %d changed since listed, %d failed, %d unverified", totals.Deleted, totals.Admitted, totals.Refused, totals.Conflicts, totals.Failed, totals.Unverified)
	}
}

// recordNotAttempted settles the members a cancellation leaves untouched;
// a record it cannot write is logged, because the job's end is derived from
// the records and a missing one keeps the member unresolved.
func (s *DeleteService) recordNotAttempted(ctx context.Context, jobID string, members []ListingRowDTO) {
	for _, member := range members {
		if err := s.jobs.RecordDeletion(ctx, jobID, member.Name, jobs.ItemObservation{Result: jobs.ItemNotAttempted, Effect: jobs.EffectNotAttempted}, nil, nil); err != nil {
			applog.Errorf("job %s: member %q not attempted but unrecorded: %v", jobID, member.Name, err)
			return
		}
	}
}

func (s *DeleteService) finishSelected(ctx context.Context, jobID string, req SelectedDeleteRequestDTO, bookmarkID string, state jobs.State, message string) {
	s.runner.release(jobID)
	err := s.jobs.FinishFromItems(ctx, jobID, jobs.Completion{
		State: state, Counters: jobs.Counters{ObjectsPlanned: int64(len(req.Members))}, Error: message,
		Audit: audit.Event{BookmarkID: bookmarkID, Operation: "delete.selected." + string(state), Resource: req.Container,
			Summary: fmt.Sprintf("job %s · %d members", jobID, len(req.Members)), Result: terminalAuditResult(state)},
	}, s.runner.audit)
	if err != nil {
		applog.Errorf("job %s terminal outcome was not saved: %v", jobID, err)
	}
}

// selectedMemberOutcome classifies one member's attempt into the ledger's
// words (ADR 0093 decision 4), each with the baseline its comparison was made
// against: deleted with what the literal read observed; unverified with the
// same; changed since listed as a conflict with what the comparison found as
// the successor that stopped it; a store answer that refused the DELETE as
// failed; and a refusal of ours before any DELETE, a link, a missing name or
// a read that could not be made, as refused.
func selectedMemberOutcome(result *swiftcontrol.ObjectDeleteResult, err error) (jobs.ItemObservation, *jobs.ItemSuccessor, *jobs.MemberBaseline) {
	baseline := memberBaseline(result)
	var conflict *swiftcontrol.IdentityConflictError
	var refusal *swiftcontrol.ShownRefusalError
	var se *swiftcontrol.SwiftError
	switch {
	case err == nil:
		return jobs.ItemObservation{Result: jobs.ItemDeleted, Effect: jobs.EffectReportedApplied}, observedSuccessor(result), baseline
	case errors.Is(err, swiftcontrol.ErrUnverifiedDelete):
		diagnostic := jobs.ItemDiagnostic{Stage: jobs.StageVerification, Code: jobs.CodeUnverified}
		return jobs.ItemObservation{Result: jobs.ItemUnverified, Effect: jobs.EffectUnresolved, Diagnostic: diagnostic}, observedSuccessor(result), baseline
	case errors.As(err, &conflict):
		found := &jobs.ItemSuccessor{Present: true, Class: conflict.FoundClass, Length: conflict.FoundLength, ETag: conflict.FoundETag, Role: jobs.SuccessorComparison}
		return jobs.ItemObservation{Result: jobs.ItemConflict, Effect: jobs.EffectNotAttempted,
			Diagnostic: jobs.ItemDiagnostic{Stage: jobs.StageSourceRead, Code: jobs.CodeConflict}}, found, baseline
	case errors.As(err, &refusal) && refusal.Link:
		// A link at the read that fixes the member's baseline: refused with
		// its kind, the fact the record needs to say why nothing was deleted
		// (ADR 0093 decision 4); the target is never named.
		return jobs.ItemObservation{Result: jobs.ItemRefused, Effect: jobs.EffectNotAttempted,
			Diagnostic: jobs.ItemDiagnostic{Stage: jobs.StageSourceRead, Code: jobs.CodeLink}}, nil, baseline
	case result != nil && result.Requested && errors.As(err, &se):
		// The DELETE left and the store answered with something other than
		// success: a client-side refusal, a refused token or a status outside
		// success such as a redirect. The member failed at its request, and
		// a request was sent whatever the answer (ADR 0093 decision 4).
		return jobs.ItemObservation{Result: jobs.ItemFailed, Effect: jobs.EffectNotAttempted,
			Diagnostic: jobs.ItemDiagnostic{Stage: jobs.StageCommit, Code: itemCodeOf(se.Kind), HTTPStatus: se.HTTPStatus}}, nil, baseline
	default:
		diagnostic := jobs.ItemDiagnostic{Stage: jobs.StageSourceRead, Code: jobs.CodeUnsupported}
		if errors.As(err, &se) {
			diagnostic.Code, diagnostic.HTTPStatus = itemCodeOf(se.Kind), se.HTTPStatus
		}
		return jobs.ItemObservation{Result: jobs.ItemRefused, Effect: jobs.EffectNotAttempted, Diagnostic: diagnostic}, nil, baseline
	}
}

// memberBaseline is the identity the member's comparison was made against,
// as the session reports it; nil when the member never reached a comparison.
func memberBaseline(result *swiftcontrol.ObjectDeleteResult) *jobs.MemberBaseline {
	if result == nil || result.Baseline == nil {
		return nil
	}
	return &jobs.MemberBaseline{Class: string(result.Baseline.Class), Length: result.Baseline.ContentLength, ETag: strings.Trim(result.Baseline.ETag, `"`)}
}

func itemCodeOf(kind swiftcontrol.ErrorKind) jobs.ItemCode {
	switch kind {
	case swiftcontrol.KindUnauthorized:
		return jobs.CodeAuthentication
	case swiftcontrol.KindForbidden:
		return jobs.CodeForbidden
	case swiftcontrol.KindNotFound:
		return jobs.CodeMissing
	case swiftcontrol.KindConflict:
		return jobs.CodeConflict
	case swiftcontrol.KindTimeout:
		return jobs.CodeTimeout
	case swiftcontrol.KindNetwork:
		return jobs.CodeIO
	default:
		return jobs.CodeUnknown
	}
}

// observedSuccessor is what the literal read found at the name after the
// DELETE: an observed absence, the object now there, or nothing when the
// read could not be made or no DELETE was issued.
func observedSuccessor(result *swiftcontrol.ObjectDeleteResult) *jobs.ItemSuccessor {
	if result == nil || result.TransIDs == nil || result.AfterRead != nil {
		return nil
	}
	if result.After == nil {
		if !result.Accepted && result.Outcome == "" {
			return nil
		}
		return &jobs.ItemSuccessor{}
	}
	class := string(result.After.Class)
	if result.After.SymlinkTarget != "" {
		class = "symlink"
	}
	return &jobs.ItemSuccessor{Present: true, Class: class, Length: result.After.ContentLength, ETag: strings.Trim(result.After.ETag, `"`)}
}
