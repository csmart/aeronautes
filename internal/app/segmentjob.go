package app

import (
	"context"
	"fmt"

	"github.com/csmart/aeronautes/internal/applog"
	"github.com/csmart/aeronautes/internal/audit"
	"github.com/csmart/aeronautes/internal/deletion"
	"github.com/csmart/aeronautes/internal/jobs"
	"github.com/csmart/aeronautes/internal/swiftcontrol"
)

// segmentJob deletes a manifest and its segments as one job (ADR 0095
// decision 8): the manifest member first, by the reviewed-manifest protocol,
// and the segments only after the manifest's deletion is durably recorded.
// The reviewed graph comes from the request, never resolved again here.
type segmentJob struct {
	s          *DeleteService
	jobID      string
	bookmarkID string
	container  string
	name       string
	resource   string
	basis      string
	reviewed   ReviewedMembershipDTO
	session    *swiftcontrol.Session

	manifestListed jobs.ItemIntent
	// lostSaid is set once the job has released itself and said that its
	// records were lost, so a second reason for the same ending adds nothing.
	lostSaid bool

	// Dynamic large objects sweep a namespace for their segments; static ones
	// delete the reviewed entries.
	segContainer string
	segPrefix    string
	namespace    string
}

func (j *segmentJob) manifestKey() string { return j.container + "/" + j.name }

// run is the job goroutine. It admits the reviewed members before any effect,
// deletes the manifest member, and, only once that is recorded deleted,
// deletes the segments; a record it cannot write latches the job unavailable.
func (j *segmentJob) run(ctx context.Context) {
	background := context.WithoutCancel(ctx)
	for _, state := range []jobs.State{jobs.StatePlanning, jobs.StateRunning} {
		if err := j.s.jobs.Transition(background, j.jobID, state); err != nil {
			applog.Errorf("job %s: %v", j.jobID, err)
		}
	}
	admitted, admittedKeys, ok := j.admit(background)
	if !ok {
		return
	}
	// Cancellation before the manifest member is issued: nothing was
	// requested, so every admitted member is not attempted and the job is
	// cancelled (ADR 0043). An already-issued member finishes whole; here
	// none is issued yet.
	if ctx.Err() != nil {
		if !j.settleNotAttempted(background, admittedKeys) {
			return
		}
		j.finish(background, jobs.StateCancelled, "", admitted)
		return
	}
	// Phase 1: the manifest member, bound to the reviewed graph. Its baseline
	// is the identity the operator reviewed, so a conflict keeps the expected
	// and observed identities (ADR 0095 decision 8).
	if err := j.s.jobs.MarkIssued(background, j.jobID, j.manifestKey()); err != nil {
		j.lost(background, fmt.Sprintf("the manifest's issued mark could not be written: %v", err))
		return
	}
	result, err := j.session.DeleteReviewedManifest(background, j.container, j.name, toReviewedManifest(j.reviewed))
	observation, successor, _ := selectedMemberOutcome(result, err)
	manifestBaseline := &jobs.MemberBaseline{Class: j.reviewed.Class, Length: j.reviewed.Length, ETag: j.reviewed.ETag}
	if observation.Result == jobs.ItemNotAttempted {
		manifestBaseline = nil
	}
	if recErr := j.s.jobs.RecordDeletion(background, j.jobID, j.manifestKey(), observation, successor, manifestBaseline); recErr != nil {
		// Boundary one: the manifest's own outcome could not be recorded after
		// its DELETE. Only the issued mark survives, the manifest is unresolved
		// on reopen, and no segment is attempted (ADR 0095 decision 8).
		j.lost(background, fmt.Sprintf("the manifest's outcome could not be recorded after its request: %v", recErr))
		return
	}
	if observation.Result != jobs.ItemDeleted {
		// A manifest refused or changed since reviewed ends the job failed with
		// nothing deleted; one whose DELETE failed or is unverified ends it
		// partial with every segment untouched, because an unconfirmed manifest
		// deletion is not consent to remove what it may still reference.
		// The static segments were admitted before any effect, so each one,
		// never issued, is settled not attempted (ADR 0095 decision 4) before
		// the job finishes, rather than left as a phantom unresolved effect the
		// terminal job can no longer settle; the manifest's own refused,
		// conflict or uncertain outcome already stands.
		var segmentKeys []string
		for _, key := range admittedKeys {
			if key != j.manifestKey() {
				segmentKeys = append(segmentKeys, key)
			}
		}
		if !j.settleNotAttempted(background, segmentKeys) {
			return
		}
		switch observation.Result {
		case jobs.ItemConflict, jobs.ItemRefused:
			j.finish(background, jobs.StateFailed, "the manifest changed since it was reviewed or was refused; nothing was deleted", admitted)
		default:
			j.finish(background, jobs.StatePartial, "the manifest's deletion was not confirmed; its segments were left untouched", admitted)
		}
		return
	}
	// Phase 2: the segments, now that the manifest is recorded deleted.
	cancelled, listErr := j.deleteSegments(ctx, background)
	if j.lostLatched(background) {
		// The segment phase reports no reason of its own when the store
		// stopped under it, whether for a record of this job's sweep or for
		// another job's, so the job says so here unless it already has.
		j.lost(background, j.s.storeStoppedBeside())
		return
	}
	state, reason := j.segmentEnd(background, cancelled, listErr)
	if state == jobs.StateComplete {
		if err := j.s.jobs.Transition(background, j.jobID, jobs.StateVerifying); err != nil {
			applog.Errorf("job %s: %v", j.jobID, err)
		}
	}
	j.finish(background, state, reason, admitted)
}

// settleNotAttempted records each key as not attempted, the outcome ADR 0095
// decision 4 requires for an admitted member that was never issued; a record
// it cannot write latches the job unavailable. It reports whether every key
// settled.
func (j *segmentJob) settleNotAttempted(ctx context.Context, keys []string) bool {
	for _, key := range keys {
		if err := j.s.jobs.RecordDeletion(ctx, j.jobID, key,
			jobs.ItemObservation{Result: jobs.ItemNotAttempted, Effect: jobs.EffectNotAttempted}, nil, nil); err != nil {
			j.lost(ctx, fmt.Sprintf("a not-attempted member could not be recorded: %v", err))
			return false
		}
	}
	return true
}

// admit records the manifest member and, for a static large object, every
// reviewed whole-object entry once, before any effect. A page the ledger
// cannot record latches the job unavailable with nothing requested.
func (j *segmentJob) admit(ctx context.Context) (int64, []string, bool) {
	intents := []jobs.ItemIntent{j.manifestListed}
	keys := []string{j.manifestKey()}
	if j.reviewed.Class == string(swiftcontrol.ClassSLO) {
		seen := map[string]bool{j.manifestKey(): true}
		for _, entry := range j.reviewed.Entries {
			key := entry.Container + "/" + entry.Object
			if seen[key] {
				continue
			}
			seen[key] = true
			keys = append(keys, key)
			intents = append(intents, jobs.ItemIntent{
				ItemIdentity: jobs.ItemIdentity{Key: key, Source: jobs.ItemLocation{
					Kind: jobs.ItemSwift, BookmarkID: j.bookmarkID, Container: entry.Container, Object: entry.Object}},
				Planned: true, Admitted: true, ListedBytes: entry.Size, ListedETag: entry.ETag,
			})
		}
	}
	for start := 0; start < len(intents); start += 500 {
		end := min(start+500, len(intents))
		if err := j.s.jobs.AdmitItems(ctx, j.jobID, intents[start:end]); err != nil {
			// AdmitItems is a write that latches the ledger unavailable, so the
			// job is left open with that genuine status rather than a terminal
			// a latched ledger could not save.
			j.lost(ctx, "the reviewed members could not be recorded before any effect: "+err.Error())
			return 0, nil, false
		}
	}
	return int64(len(intents)), keys, true
}

// deleteSegments deletes the segment members after the manifest: the reviewed
// entries for a static large object, or the namespace for a dynamic one,
// through the same per-name protocol the bulk sweep uses. It reports whether
// a cancellation ended it.
func (j *segmentJob) deleteSegments(ctx, background context.Context) (bool, error) {
	if j.reviewed.Class == string(swiftcontrol.ClassDLO) {
		sweep := &prefixSweep{s: j.s, jobID: j.jobID, bookmarkID: j.bookmarkID, container: j.segContainer, prefix: j.namespace, session: j.session}
		sweep.run(ctx)
		counts := sweep.counts()
		return counts.cancelled, counts.listErr
	}
	seen := map[string]bool{}
	for _, entry := range j.reviewed.Entries {
		key := entry.Container + "/" + entry.Object
		if seen[key] {
			continue
		}
		seen[key] = true
		if j.lostLatched(background) {
			return false, nil
		}
		if ctx.Err() != nil {
			if err := j.s.jobs.RecordDeletion(background, j.jobID, key,
				jobs.ItemObservation{Result: jobs.ItemNotAttempted, Effect: jobs.EffectNotAttempted}, nil, nil); err != nil {
				applog.Errorf("job %s: segment %q not attempted but unrecorded: %v", j.jobID, key, err)
				return true, nil
			}
			continue
		}
		if err := j.s.jobs.MarkIssued(background, j.jobID, key); err != nil {
			j.lost(background, fmt.Sprintf("the issued mark for segment %q could not be written: %v", key, err))
			return false, nil
		}
		result, err := j.session.DeleteMember(background, entry.Container, swiftcontrol.MemberRow{Name: entry.Object, Bytes: entry.Size, ETag: entry.ETag})
		observation, successor, baseline := selectedMemberOutcome(result, err)
		if recErr := j.s.jobs.RecordDeletion(background, j.jobID, key, observation, successor, baseline); recErr != nil {
			// Boundary two: a later write failed after the manifest's deleted
			// outcome committed. That record stands; the job is unavailable.
			j.lost(background, fmt.Sprintf("a segment's outcome could not be recorded after its request: %v", recErr))
			return false, nil
		}
	}
	return false, nil
}

// segmentEnd derives the terminal state once the manifest is deleted: complete
// when every admitted member was deleted, partial otherwise with the segment
// breakdown, because a deleted manifest can never be undone by a partial
// segment phase.
func (j *segmentJob) segmentEnd(ctx context.Context, cancelled bool, listErr error) (jobs.State, string) {
	totals, err := j.s.jobs.ReadItemTotals(ctx, j.jobID)
	if err != nil {
		return jobs.StatePartial, "the members' records could not be read: " + err.Error()
	}
	breakdown := fmt.Sprintf("the manifest is gone; %d of %d members deleted; %d refused, %d changed since reviewed, %d failed, %d unverified",
		totals.Deleted, totals.Admitted, totals.Refused, totals.Conflicts, totals.Failed, totals.Unverified)
	if cancelled {
		return jobs.StatePartial, "cancelled after the manifest was deleted; " + breakdown
	}
	// A dynamic sweep that could not read its namespace listing has not swept
	// it, so its ledger totals cannot establish an empty namespace; the job is
	// partial with the deleted manifest disclosed (ADR 0095 decision 8).
	if listErr != nil {
		return jobs.StatePartial, "the manifest is gone, but the segment listing stopped, so the namespace was not fully swept: " + listErr.Error() + "; " + breakdown
	}
	// For a dynamic manifest the namespace is verified empty before the job
	// claims complete, because the reviewed members grow from the listing and
	// their totals alone cannot say an unlisted name is gone.
	if j.reviewed.Class == string(swiftcontrol.ClassDLO) {
		remaining, err := deletion.VerifyPrefixEmpty(ctx, j.session, j.segContainer, j.namespace)
		if err != nil {
			return jobs.StatePartial, "the manifest is gone, but its namespace could not be verified empty: " + err.Error() + "; " + breakdown
		}
		if remaining > 0 {
			return jobs.StatePartial, "the manifest is gone, but a name still answers under its namespace; " + breakdown
		}
	}
	if totals.Deleted == totals.Admitted {
		return jobs.StateComplete, ""
	}
	return jobs.StatePartial, breakdown
}

// lost latches the job unavailable: the ledger could not record something, so
// the job stays open, new jobs are blocked, and the reopen rule settles the
// members (ADR 0036, ADR 0095 decision 8).
func (j *segmentJob) lost(ctx context.Context, reason string) {
	if j.lostSaid {
		return
	}
	j.lostSaid = true
	j.s.runner.release(j.jobID)
	j.s.runner.record(ctx, j.bookmarkID, "segments.delete.unavailable", j.resource, "job "+j.jobID+" · "+reason, audit.ResultUnverified)
	applog.Warnf("job %s: %s", j.jobID, reason)
}

func (j *segmentJob) lostLatched(ctx context.Context) bool {
	return j.s.jobs.Available() != nil
}

func (j *segmentJob) finish(ctx context.Context, state jobs.State, message string, admitted int64) {
	j.s.runner.release(j.jobID)
	err := j.s.jobs.FinishFromItems(ctx, j.jobID, jobs.Completion{
		State: state, Counters: jobs.Counters{ObjectsPlanned: admitted}, Error: message,
		Audit: audit.Event{BookmarkID: j.bookmarkID, Operation: "segments.delete." + string(state), Resource: j.resource,
			Summary: fmt.Sprintf("job %s · %s", j.jobID, j.basis), Result: terminalAuditResult(state)},
	}, j.s.runner.audit)
	if err != nil {
		applog.Errorf("job %s terminal outcome was not saved: %v", j.jobID, err)
	}
}

func toReviewedManifest(dto ReviewedMembershipDTO) swiftcontrol.ReviewedManifest {
	reviewed := swiftcontrol.ReviewedManifest{
		Class: swiftcontrol.ObjectClass(dto.Class), Fingerprint: dto.Fingerprint, Namespace: dto.Namespace,
	}
	for _, entry := range dto.Entries {
		reviewed.Entries = append(reviewed.Entries, swiftcontrol.ReviewedEntry{
			Container: entry.Container, Object: entry.Object, Size: entry.Size, ETag: entry.ETag})
	}
	return reviewed
}
