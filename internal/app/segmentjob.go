package app

import (
	"context"
	"fmt"
	"log"

	"github.com/csmart/aeronautes/internal/audit"
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
			log.Printf("job %s: %v", j.jobID, err)
		}
	}
	admitted, ok := j.admit(background)
	if !ok {
		return
	}
	// Phase 1: the manifest member, bound to the reviewed graph.
	if err := j.s.jobs.MarkIssued(background, j.jobID, j.manifestKey()); err != nil {
		j.lost(background, fmt.Sprintf("the manifest's issued mark could not be written: %v", err))
		return
	}
	result, err := j.session.DeleteReviewedManifest(background, j.container, j.name, toReviewedManifest(j.reviewed))
	observation, successor, baseline := selectedMemberOutcome(result, err)
	if recErr := j.s.jobs.RecordDeletion(background, j.jobID, j.manifestKey(), observation, successor, baseline); recErr != nil {
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
		switch observation.Result {
		case jobs.ItemConflict, jobs.ItemRefused:
			j.finish(background, jobs.StateFailed, "the manifest changed since it was reviewed or was refused; nothing was deleted", admitted)
		default:
			j.finish(background, jobs.StatePartial, "the manifest's deletion was not confirmed; its segments were left untouched", admitted)
		}
		return
	}
	// Phase 2: the segments, now that the manifest is recorded deleted.
	cancelled := j.deleteSegments(ctx, background)
	if j.lostLatched(background) {
		return
	}
	state, reason := j.segmentEnd(background, cancelled)
	if state == jobs.StateComplete {
		if err := j.s.jobs.Transition(background, j.jobID, jobs.StateVerifying); err != nil {
			log.Printf("job %s: %v", j.jobID, err)
		}
	}
	j.finish(background, state, reason, admitted)
}

// admit records the manifest member and, for a static large object, every
// reviewed whole-object entry once, before any effect. A page the ledger
// cannot record latches the job unavailable with nothing requested.
func (j *segmentJob) admit(ctx context.Context) (int64, bool) {
	intents := []jobs.ItemIntent{j.manifestListed}
	if j.reviewed.Class == string(swiftcontrol.ClassSLO) {
		seen := map[string]bool{j.manifestKey(): true}
		for _, entry := range j.reviewed.Entries {
			key := entry.Container + "/" + entry.Object
			if seen[key] {
				continue
			}
			seen[key] = true
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
			j.lost(ctx, "the reviewed members could not be recorded before any effect: "+err.Error())
			return 0, false
		}
	}
	return int64(len(intents)), true
}

// deleteSegments deletes the segment members after the manifest: the reviewed
// entries for a static large object, or the namespace for a dynamic one,
// through the same per-name protocol the bulk sweep uses. It reports whether
// a cancellation ended it.
func (j *segmentJob) deleteSegments(ctx, background context.Context) bool {
	if j.reviewed.Class == string(swiftcontrol.ClassDLO) {
		sweep := &prefixSweep{s: j.s, jobID: j.jobID, bookmarkID: j.bookmarkID, container: j.segContainer, prefix: j.namespace, session: j.session}
		sweep.run(ctx)
		return sweep.counts().cancelled
	}
	seen := map[string]bool{}
	for _, entry := range j.reviewed.Entries {
		key := entry.Container + "/" + entry.Object
		if seen[key] {
			continue
		}
		seen[key] = true
		if j.lostLatched(background) {
			return false
		}
		if ctx.Err() != nil {
			if err := j.s.jobs.RecordDeletion(background, j.jobID, key,
				jobs.ItemObservation{Result: jobs.ItemNotAttempted, Effect: jobs.EffectNotAttempted}, nil, nil); err != nil {
				log.Printf("job %s: segment %q not attempted but unrecorded: %v", j.jobID, key, err)
				return true
			}
			continue
		}
		if err := j.s.jobs.MarkIssued(background, j.jobID, key); err != nil {
			j.lost(background, fmt.Sprintf("the issued mark for segment %q could not be written: %v", key, err))
			return false
		}
		result, err := j.session.DeleteMember(background, entry.Container, swiftcontrol.MemberRow{Name: entry.Object, Bytes: entry.Size, ETag: entry.ETag})
		observation, successor, baseline := selectedMemberOutcome(result, err)
		if recErr := j.s.jobs.RecordDeletion(background, j.jobID, key, observation, successor, baseline); recErr != nil {
			// Boundary two: a later write failed after the manifest's deleted
			// outcome committed. That record stands; the job is unavailable.
			j.lost(background, fmt.Sprintf("a segment's outcome could not be recorded after its request: %v", recErr))
			return false
		}
	}
	return false
}

// segmentEnd derives the terminal state once the manifest is deleted: complete
// when every admitted member was deleted, partial otherwise with the segment
// breakdown, because a deleted manifest can never be undone by a partial
// segment phase.
func (j *segmentJob) segmentEnd(ctx context.Context, cancelled bool) (jobs.State, string) {
	totals, err := j.s.jobs.ReadItemTotals(ctx, j.jobID)
	if err != nil {
		return jobs.StatePartial, "the members' records could not be read: " + err.Error()
	}
	if totals.Deleted == totals.Admitted {
		return jobs.StateComplete, ""
	}
	breakdown := fmt.Sprintf("the manifest is gone; %d of %d members deleted; %d refused, %d changed since reviewed, %d failed, %d unverified",
		totals.Deleted, totals.Admitted, totals.Refused, totals.Conflicts, totals.Failed, totals.Unverified)
	if cancelled {
		return jobs.StatePartial, "cancelled after the manifest was deleted; " + breakdown
	}
	return jobs.StatePartial, breakdown
}

// lost latches the job unavailable: the ledger could not record something, so
// the job stays open, new jobs are blocked, and the reopen rule settles the
// members (ADR 0036, ADR 0095 decision 8).
func (j *segmentJob) lost(ctx context.Context, reason string) {
	j.s.runner.release(j.jobID)
	j.s.runner.record(ctx, j.bookmarkID, "segments.delete.unavailable", j.resource, "job "+j.jobID+" · "+reason, audit.ResultUnverified)
	log.Printf("job %s: %s", j.jobID, reason)
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
		log.Printf("job %s terminal outcome was not saved: %v", j.jobID, err)
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
