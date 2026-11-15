package app

import (
	"context"
	"fmt"
	"strings"

	"github.com/csmart/aeronautes/internal/audit"
	"github.com/csmart/aeronautes/internal/deletion"
	"github.com/csmart/aeronautes/internal/jobs"
	"github.com/csmart/aeronautes/internal/swiftcontrol"
)

// A bulk deletion that takes segments (ADR 0119): the operator's explicit
// force for one job, applied to each large object the sweep meets. The rules
// are the single-object segment job's (ADR 0095 decision 8, ADR 0044); what
// is here is how a sweep applies them to a row at its turn, and when it
// leaves a large object's segments alone.

// refuseSegmentsWhereVersionsAreKept refuses to delete segments with their
// objects in a container that keeps versions, or whose versioning cannot be
// read: an earlier version, or the copy the store keeps of what is deleted,
// still names the segments. The bulk option and the one-object deletion
// both ask it (ADR 0119 decision 3, ADR 0120 decision 4).
func refuseSegmentsWhereVersionsAreKept(ctx context.Context, session *swiftcontrol.Session, container string) error {
	detail, err := session.ContainerDetail(ctx, container)
	if err != nil {
		return fmt.Errorf("whether %s keeps versions could not be read, so segments are not deleted with their objects: %w", container, err)
	}
	if detail.Versioning.Mode != swiftcontrol.VersioningOff {
		return fmt.Errorf("%s keeps versions (%s), and a kept version still uses its segments, so segments are not deleted with their objects here; delete without that option", container, detail.Versioning.Mode)
	}
	return nil
}

// largeObject deletes one row of the scope together with its segments, when
// the row is a large object whose segments the rules can bound: the manifest
// first, bound to the graph read here, and the segments only once that
// deletion is recorded (ADR 0119 decisions 4 and 5). It runs on the goroutine
// that reads the listing, so its admissions never race the listing's.
//
// handled is false for every other row, a plain object, a marker, a link, a
// row that cannot be read, and a large object whose segments are left; the
// caller then queues the row as an ordinary member, which deletes a manifest
// manifest-only exactly as a sweep without the option does.
func (r *prefixSweep) largeObject(ctx, background context.Context, queue chan<- sweepMember, m sweepMember) (handled, cancelled bool) {
	if r.lostReason() != "" || ctx.Err() != nil {
		return false, false
	}
	detail, err := r.session.ObjectDetail(ctx, m.container, m.name)
	if err != nil || detail.FollowedSymlink || detail.SymlinkTarget != "" {
		// The member protocol reads the row itself and records what it finds.
		return false, false
	}
	var manifest *swiftcontrol.SLOManifest
	var segments []sweepMember
	var segContainer, namespace string
	switch detail.Class {
	case swiftcontrol.ClassSLO:
		manifest, err = r.session.SLOManifest(ctx, m.container, m.name)
		if err != nil {
			return r.leave(ctx, m, "its manifest could not be read: "+err.Error())
		}
		entries, left := staticSegments(manifest)
		if left != "" {
			return r.leave(ctx, m, left)
		}
		account := swiftcontrol.AccountIdentity(r.session.StorageURL())
		seen := map[string]bool{}
		for _, entry := range entries {
			if holder, staged := r.s.runner.stagedByLiveJob(account, entry.Container, entry.Object); staged {
				return r.leave(ctx, m, "a segment of it is in the staging area of "+holder)
			}
			segment := sweepMember{container: entry.Container, name: entry.Object, bytes: entry.Size, etag: entry.ETag, recorded: true}
			if !seen[segment.key()] {
				seen[segment.key()] = true
				segments = append(segments, segment)
			}
		}
	case swiftcontrol.ClassDLO:
		// A dynamic manifest's length and entity tag are made from its
		// segments, so one read or compared while this job's workers delete
		// them would be refused as changed by the job's own doing. Its
		// segments can be rows of the scope, or the entries of a static
		// manifest this job has already deleted. The members in flight
		// settle first, and the manifest is read again as it then stands,
		// while nothing else of this sweep is running (ADR 0119 decision 4,
		// ADR 0120 decision 6).
		r.inflight.Wait()
		detail, err = r.session.ObjectDetail(ctx, m.container, m.name)
		if err != nil || detail.FollowedSymlink || detail.SymlinkTarget != "" || detail.Class != swiftcontrol.ClassDLO {
			return false, false
		}
		var segPrefix string
		var ok bool
		segContainer, segPrefix, ok = swiftcontrol.DynamicSegments(detail.Manifest)
		if !ok || segPrefix == "" {
			return r.leave(ctx, m, "its manifest names no segment prefix, so its segments have no bounded scope")
		}
		namespace = dloNamespace(segPrefix)
		if segContainer == r.container && strings.HasPrefix(namespace, r.prefix) {
			// The namespace is the scope or lies inside it, so its segments
			// are rows of the scope and the sweep deletes them as it deletes
			// every other row. Both end in a slash, or the scope is the whole
			// container, so this compares whole path parts. The manifest is
			// deleted here, by the ordinary member protocol, before the
			// workers are handed anything more.
			r.member(ctx, background, m)
			return true, false
		}
		if segContainer == r.container && strings.HasPrefix(r.prefix, namespace) {
			// Strictly wider than the scope: sweeping it would delete names
			// the operator did not ask for, segments or not.
			return r.leave(ctx, m, "its segments' namespace is wider than the scope being deleted")
		}
		outside, err := namesOutsideNamespace(ctx, r.session, segContainer, segPrefix, namespace)
		if err != nil {
			return r.leave(ctx, m, "the listing of its segments did not complete: "+err.Error())
		}
		if outside.count > 0 {
			return r.leave(ctx, m, fmt.Sprintf("its manifest also reads %d name(s) outside its own namespace %s/%s, which other objects may use",
				outside.count, segContainer, namespace))
		}
		if err := r.claimSegmentContainer(segContainer); err != nil {
			return r.leave(ctx, m, "another job is staging into the container that holds its segments")
		}
	default:
		return false, false
	}
	reviewed, err := reviewedMembership(detail, manifest)
	if err != nil {
		return r.leave(ctx, m, err.Error())
	}
	// A static object's segments are admitted before its manifest is
	// requested, and only the ones the job does not already hold: a segment
	// another manifest of this job named, or one that is also a row of the
	// scope, is requested once (decision 6).
	segments, _, readErr, latched := r.admitRows(background, segments)
	if latched {
		return true, false
	}
	if readErr != nil {
		return r.leave(ctx, m, readErr.Error())
	}
	observation, lost := r.s.deleteReviewedMember(background, r.session, r.jobID, m.container, m.name, reviewed)
	if lost != "" {
		r.latch(lost)
		return true, false
	}
	r.settled(background, observation)
	if observation.Result != jobs.ItemDeleted {
		// A manifest that was refused, had changed, failed or is unverified is
		// not consent to remove what it may still name (decision 5).
		for _, segment := range segments {
			r.notAttempted(background, segment)
		}
		return true, false
	}
	if detail.Class == swiftcontrol.ClassSLO {
		return true, r.enqueue(ctx, background, queue, segments)
	}
	return true, r.sweepNamespace(ctx, background, m, segContainer, namespace)
}

// claimSegmentContainer claims, for the rest of the job, a container in which
// this job sweeps dynamic large objects' namespaces, so that no transfer
// stages there while it does and none is staging there now (ADR 0105 decision
// 3). The claim is the whole container, taken once, and not one per
// namespace: a job holds its claims until it ends, and a scope may hold any
// number of dynamic large objects. It is called only on the goroutine that
// reads the listing.
func (r *prefixSweep) claimSegmentContainer(container string) error {
	if r.claimedSegments[container] {
		return nil
	}
	if err := r.owner.claim(sweepClaim(swiftcontrol.AccountIdentity(r.session.StorageURL()), container, "")); err != nil {
		return err
	}
	if r.claimedSegments == nil {
		r.claimedSegments = map[string]bool{}
	}
	r.claimedSegments[container] = true
	return nil
}

// staticSegments are the segments of a static large object this job may
// delete: its manifest's entries, when every one is a whole object with a
// recorded entity tag, which is what lets each be compared with what the
// manifest named. Otherwise it says why they are left (ADR 0119 decision 4).
// An entry without a tag is not read again to find out what stands there, as
// the single-object job may do for a graph the operator reviewed.
func staticSegments(manifest *swiftcontrol.SLOManifest) (entries []swiftcontrol.ReviewedEntry, left string) {
	for _, raw := range manifest.Entries {
		segment, ok := raw.(swiftcontrol.ObjectSegment)
		if !ok || segment.NestedSLO || segment.Range != nil {
			return nil, "its manifest has a nested, ranged or inline entry, so its segments are not its alone to name"
		}
		if segment.ETag == nil || *segment.ETag == "" {
			return nil, "its manifest has an entry with no recorded entity tag, so that segment cannot be compared with what the manifest named"
		}
	}
	entries, _ = manifest.WholeObjectEntries()
	return entries, ""
}

// sweepNamespace deletes a dynamic large object's segments once its manifest
// is recorded deleted: the namespace's rows by the same sweep the
// single-object job runs, without this option, so a manifest among the
// segments is deleted manifest-only and never followed. Its members are
// counted with the job's.
func (r *prefixSweep) sweepNamespace(ctx, background context.Context, m sweepMember, segContainer, namespace string) (cancelled bool) {
	nested := &prefixSweep{s: r.s, jobID: r.jobID, bookmarkID: r.bookmarkID, container: segContainer, prefix: namespace,
		session: r.session, nested: true}
	nested.run(ctx)
	counts := nested.counts()
	r.mu.Lock()
	r.tally.admitted += counts.admitted
	r.tally.settled += counts.settled
	r.tally.deleted += counts.deleted
	r.mu.Unlock()
	r.publishCounters(background)
	switch {
	case nested.lostReason() != "":
		r.latch(nested.lostReason())
	case counts.cancelled:
		return true
	case counts.listErr != nil:
		r.leave(background, m, "the listing of its segments stopped, so not all of them were deleted: "+counts.listErr.Error())
	default:
		// The namespace's members grow from its listing, so their records
		// alone cannot say that a name the listing omitted is gone.
		remaining, err := deletion.VerifyPrefixEmpty(background, r.session, segContainer, namespace)
		if err != nil {
			r.leave(background, m, "its segments could not be verified gone: "+err.Error())
		} else if remaining > 0 {
			r.leave(background, m, "a name still answers under its segments")
		}
	}
	return false
}

// leave records that a large object's segments were not deleted, or not all
// of them, and why. The job ends partial for it and its audit trail names
// each such object, because the ledger's outcome for the manifest itself is
// deleted and has no place for what became of its segments. A reason that is
// only the job's own cancellation is not recorded: the cancelled job says
// what it did not attempt.
func (r *prefixSweep) leave(ctx context.Context, m sweepMember, reason string) (handled, cancelled bool) {
	if ctx.Err() != nil {
		return false, false
	}
	r.mu.Lock()
	r.leftCount++
	if r.leftFirst == "" {
		r.leftFirst = m.key() + ": " + reason
	}
	r.mu.Unlock()
	r.s.runner.record(context.WithoutCancel(ctx), r.bookmarkID, "delete.segments.left", m.key(), "job "+r.jobID+" · "+reason, audit.ResultObserved)
	return false, false
}

// segmentsLeft is the sentence a job that left segments ends with, empty when
// it left none.
func (r *prefixSweep) segmentsLeft() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.leftCount == 0 {
		return ""
	}
	return fmt.Sprintf("the segments of %d large object(s) were left or not all deleted; the first: %s", r.leftCount, r.leftFirst)
}

// deleteReviewedMember issues one manifest member of a job and records its
// outcome: the manifest protocol bound to the graph that was read for it,
// with that graph's identity as the member's baseline (ADR 0095 decision 8).
// It returns the outcome, or the reason a record could not be written; the
// caller then stops, because a manifest with only an issued mark is
// unresolved and none of its segments may be attempted.
func (s *DeleteService) deleteReviewedMember(ctx context.Context, session *swiftcontrol.Session, jobID, container, name string, reviewed ReviewedMembershipDTO) (jobs.ItemObservation, string) {
	key := container + "/" + name
	if err := s.jobs.MarkIssued(ctx, jobID, key); err != nil {
		return jobs.ItemObservation{}, fmt.Sprintf("the manifest's issued mark could not be written: %v", err)
	}
	result, err := session.DeleteReviewedManifest(ctx, container, name, toReviewedManifest(reviewed))
	observation, successor, _ := selectedMemberOutcome(result, err)
	baseline := &jobs.MemberBaseline{Class: reviewed.Class, Length: reviewed.Length, ETag: reviewed.ETag}
	if observation.Result == jobs.ItemNotAttempted {
		baseline = nil
	}
	if recErr := s.jobs.RecordDeletion(ctx, jobID, key, observation, successor, baseline); recErr != nil {
		return observation, fmt.Sprintf("the manifest's outcome could not be recorded after its request: %v", recErr)
	}
	return observation, ""
}
