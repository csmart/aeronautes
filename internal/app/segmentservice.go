package app

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/csmart/aeronautes/internal/audit"
	"github.com/csmart/aeronautes/internal/evidence"
	"github.com/csmart/aeronautes/internal/jobs"
	"github.com/csmart/aeronautes/internal/swiftcontrol"
)

// The §34/§35 segment-cleanup half of DeleteService: manifest-only stays
// the default everywhere. Manifest+segments ALWAYS requires the
// deliberate Backstage flow (force + typed confirmation) — ownership
// evidence is advisory context in the dialog and never auto-authorizes
// destruction, because no live reverse-reference scan exists yet to
// prove exclusivity (review R-02). The honest §34 warning stands:
// sharing cannot be disproved from one manifest.

// SegmentsDeleteRequestDTO names one manifest and the operator's intent.
type SegmentsDeleteRequestDTO struct {
	BookmarkID string `json:"bookmarkId"`
	Container  string `json:"container"`
	Name       string `json:"name"`
	// Force is the Backstage override for manifests whose segments are
	// NOT proven exclusively ours.
	Force bool `json:"force"`
	// Confirm must equal the object name.
	Confirm string `json:"confirm"`
	// Reviewed is the membership the review returned, carried back so the job
	// acts on the graph the operator saw (ADR 0095 decision 8).
	Reviewed ReviewedMembershipDTO `json:"reviewed"`
}

// SegmentRefDTO is one reviewed whole-object segment the request carries from
// the review to the execution (ADR 0095 decision 8).
type SegmentRefDTO struct {
	Container string `json:"container"`
	Object    string `json:"object"`
	Size      int64  `json:"size"`
	ETag      string `json:"etag"`
}

// ReviewedMembershipDTO is the graph the operator reviewed, carried back on
// the request so the job acts on it instead of a graph resolved again at
// execution (ADR 0095 decision 8): the manifest's reviewed identity and, for
// a static large object, its whole-object entries, or, for a dynamic large
// object, its canonical namespace.
type ReviewedMembershipDTO struct {
	Class       string          `json:"class"` // dlo | slo
	Fingerprint string          `json:"fingerprint"`
	Namespace   string          `json:"namespace,omitempty"`
	Entries     []SegmentRefDTO `json:"entries,omitempty"`
}

// SegmentOwnershipDTO reports what a segments-delete would rest on.
type SegmentOwnershipDTO struct {
	Class string `json:"class"` // dlo | slo
	// Reviewed is the graph a deletion would carry back and act on.
	Reviewed ReviewedMembershipDTO `json:"reviewed"`
	// Target describes the segment scope ("container/prefix" for a DLO;
	// "N segments" for an SLO).
	Target string `json:"target"`
	// Proven: every referenced segment is covered by a recorded
	// ExclusiveOwnershipProof (§28.6(b)).
	Proven bool `json:"proven"`
	// Segments counted (SLO) or 0 (DLO — prefix-scoped).
	Segments int64 `json:"segments"`
	// InlineEntries counts SLO `data` entries: they live ONLY in the
	// manifest, so even a manifest-only delete destroys them (§34
	// honesty — the dialog must not claim every segment survives).
	InlineEntries int64 `json:"inlineEntries"`
}

// sloSegmentRefs flattens a manifest's direct whole-object references
// and says whether the graph is PROVABLE at all. A nested sub-SLO entry
// makes it unprovable — the proof would cover the nested manifest
// object, not its descendants, and Swift's recursive delete follows
// them. Ranged entries (referencing part of an object this job would
// not have written that way), inline data, and unrecognised entry
// shapes are equally unprovable (§28.6(b), K-SEG-003).
func sloSegmentRefs(manifest *swiftcontrol.SLOManifest) (refs []swiftcontrol.ObjectRef, provable bool) {
	provable = true
	for _, entry := range manifest.Entries {
		segment, ok := entry.(swiftcontrol.ObjectSegment)
		if !ok {
			provable = false // inline data or an unrecognised shape
			continue
		}
		if segment.NestedSLO || segment.Range != nil {
			provable = false
		}
		refs = append(refs, segment.Path)
	}
	return refs, provable
}

// dloNamespace is the boundary a dynamic large object's segment
// deletion is authorised over: the manifest prefix when it already ends
// in a slash, and that prefix followed by one when it does not.
//
// Swift reads X-Object-Manifest as a RAW string match, so the set the
// manifest reads can be wider than this boundary — "job1" reads
// "job10/0000" too. The destructive set never is. The evidence store
// canonicalises the same way, so the proof shown to the operator and
// the set the sweep acts on describe one set (ADR 0044).
func dloNamespace(prefix string) string {
	if strings.HasSuffix(prefix, "/") {
		return prefix
	}
	return prefix + "/"
}

// outsideNames is what a preflight retains: how many names the manifest
// reads from outside its namespace, and at most a sample of them for the
// message. The full list is never held — a wide namespace would otherwise
// grow resident data without a bound for a five-name diagnostic — but
// every page is still consumed and every name still counted, so the
// bound is on memory and never on the decision (ADR 0044).
type outsideNames struct {
	count  int
	sample []string
}

// outsideSample bounds the MESSAGE only.
const outsideSample = 5

func (o *outsideNames) add(name string) {
	o.count++
	if len(o.sample) < outsideSample {
		o.sample = append(o.sample, name)
	}
}

// namesOutsideNamespace lists the RAW manifest prefix and reports the
// names the manifest reads that fall outside its namespace.
//
// The listing is consumed completely and any error refuses: a truncated
// listing would otherwise read as "no outside names" and authorise the
// deletion it should have stopped. Listings are advisory and eventually
// consistent (K-CON-001), so an empty result means none were OBSERVED
// and never that none exist — the caller must not report absence.
func namesOutsideNamespace(ctx context.Context, session *swiftcontrol.Session,
	container, rawPrefix, namespace string) (outsideNames, error) {

	var outside outsideNames
	for entry, err := range session.Objects(ctx, container,
		swiftcontrol.ObjectListOptions{Deep: true, Prefix: rawPrefix}) {
		if err != nil {
			return outsideNames{}, fmt.Errorf("listing %s/%s before deleting segments: %w", container, rawPrefix, err)
		}
		object, ok := entry.(swiftcontrol.ObjectEntry)
		if !ok {
			continue
		}
		if !strings.HasPrefix(object.Name, namespace) {
			outside.add(object.Name)
		}
	}
	if err := ctx.Err(); err != nil {
		return outsideNames{}, fmt.Errorf("listing %s/%s before deleting segments: %w", container, rawPrefix, err)
	}
	return outside, nil
}

// outsideRefusal names the boundary, the count and the retained sample.
func outsideRefusal(container, namespace string, outside outsideNames) error {
	more := ""
	if outside.count > len(outside.sample) {
		more = fmt.Sprintf(" and %d more", outside.count-len(outside.sample))
	}
	return fmt.Errorf("this manifest also reads %d name(s) outside its own namespace %s/%s, which other objects may reference, so its segments were not deleted and nothing was removed: %s%s",
		outside.count, container, namespace, strings.Join(outside.sample, ", "), more)
}

// segmentScope resolves a manifest's authoritative class and scope.
func (s *DeleteService) segmentScope(ctx context.Context, session *swiftcontrol.Session,
	req SegmentsDeleteRequestDTO) (*swiftcontrol.ObjectDetail, *SegmentOwnershipDTO, []swiftcontrol.ObjectRef, error) {

	detail, err := session.ObjectDetail(ctx, req.Container, req.Name)
	if err != nil {
		return nil, nil, nil, err
	}
	ownership := &SegmentOwnershipDTO{Class: string(detail.Class)}
	switch detail.Class {
	case swiftcontrol.ClassDLO:
		slash := strings.Index(detail.Manifest, "/")
		if slash <= 0 {
			return nil, nil, nil, fmt.Errorf("malformed X-Object-Manifest %q", detail.Manifest)
		}
		segContainer, segPrefix := detail.Manifest[:slash], detail.Manifest[slash+1:]
		if segPrefix == "" {
			// A manifest naming a container and no prefix would make the
			// namespace the whole container, which no manifest's
			// authority reaches. This store rejects the header at
			// creation, so the guard is defence in depth against one
			// that does not (ADR 0044).
			return nil, nil, nil, fmt.Errorf("X-Object-Manifest %q names a whole container and no segment prefix — segment deletion has no bounded scope here", detail.Manifest)
		}
		// The dialog states the boundary that would be swept, not the
		// raw header, so the operator is shown the set the operation
		// acts on.
		ownership.Target = segContainer + "/" + dloNamespace(segPrefix)
		proven, err := s.evidence.HasExclusiveOwnership(ctx, req.BookmarkID, segContainer, segPrefix)
		if err != nil {
			return nil, nil, nil, err
		}
		ownership.Proven = proven
		return detail, ownership, nil, nil
	case swiftcontrol.ClassSLO:
		manifest, err := session.SLOManifest(ctx, req.Container, req.Name)
		if err != nil {
			return nil, nil, nil, err
		}
		refs, provable := sloSegmentRefs(manifest)
		for _, entry := range manifest.Entries {
			if _, inline := entry.(swiftcontrol.InlineDataSegment); inline {
				ownership.InlineEntries++
			}
		}
		ownership.Segments = int64(len(refs))
		ownership.Target = fmt.Sprintf("%d segments", len(refs))
		if !provable || len(refs) != len(manifest.Entries) {
			// Nested, ranged, inline, or unrecognised entries: never
			// provable in v1 — the proof cannot see past them.
			return detail, ownership, refs, nil
		}
		owned, err := s.ownedRefs(ctx, req.BookmarkID, refs)
		if err != nil {
			return nil, nil, nil, err
		}
		ownership.Proven = owned
		return detail, ownership, refs, nil
	default:
		return nil, nil, nil, fmt.Errorf("%s/%s is a plain object — segment deletion does not apply", req.Container, req.Name)
	}
}

// ownedRefs proves every segment sits under a recorded owned prefix in
// its container (an owned prefix covers the job-created objects below
// it; the proof is exact-prefix, so coverage is prefix-match against
// recorded prefixes, never inference).
func (s *DeleteService) ownedRefs(ctx context.Context, bookmarkID string, refs []swiftcontrol.ObjectRef) (bool, error) {
	if len(refs) == 0 {
		return false, nil
	}
	byContainer := map[string][]evidence.Ownership{}
	for _, ref := range refs {
		owned, cached := byContainer[ref.Container]
		if !cached {
			var err error
			owned, err = s.evidence.Ownerships(ctx, bookmarkID, ref.Container)
			if err != nil {
				return false, err
			}
			byContainer[ref.Container] = owned
		}
		covered := false
		for _, proof := range owned {
			// Prefix coverage requires a real namespace boundary: an
			// owned "job/aa/" must not cover "job/aa-evil/x", so only
			// slash-terminated proofs match.
			if strings.HasSuffix(proof.Prefix, "/") && strings.HasPrefix(ref.Object, proof.Prefix) {
				covered = true
				break
			}
		}
		if !covered {
			return false, nil
		}
	}
	return true, nil
}

// CheckSegmentOwnership is the read-only status the dialog shows before
// the operator decides anything.
func (s *DeleteService) CheckSegmentOwnership(ctx context.Context, req SegmentsDeleteRequestDTO) (*SegmentOwnershipDTO, error) {
	if err := refuseIfDeferred("DeleteService.CheckSegmentOwnership"); err != nil {
		return nil, err
	}
	if req.BookmarkID == "" || req.Container == "" || req.Name == "" {
		return nil, errors.New("bookmark, container, and object are required")
	}
	session, err := s.connections.SessionFor(ctx, req.BookmarkID)
	if err != nil {
		return nil, err
	}
	detail, ownership, _, err := s.segmentScope(ctx, session, req)
	if err != nil {
		return nil, err
	}
	reviewed, err := s.reviewedMembership(ctx, session, req.Container, req.Name, detail)
	if err != nil {
		return nil, err
	}
	ownership.Reviewed = reviewed
	return ownership, nil
}

// reviewedMembership resolves the graph a deletion would carry: for a static
// large object its whole-object entries with the size and entity tag the
// manifest records, for a dynamic large object its canonical namespace, each
// beside the manifest's reviewed identity (ADR 0095 decision 8). A static
// graph that is not purely whole-object yields no entries: the manifest-only
// delete still applies, but no segment is a reviewed member to reclaim.
func (s *DeleteService) reviewedMembership(ctx context.Context, session *swiftcontrol.Session,
	container, name string, detail *swiftcontrol.ObjectDetail) (ReviewedMembershipDTO, error) {

	reviewed := ReviewedMembershipDTO{Class: string(detail.Class), Fingerprint: detail.ContentFingerprint()}
	switch detail.Class {
	case swiftcontrol.ClassSLO:
		manifest, err := session.SLOManifest(ctx, container, name)
		if err != nil {
			return ReviewedMembershipDTO{}, err
		}
		entries, _ := manifest.WholeObjectEntries()
		for _, entry := range entries {
			reviewed.Entries = append(reviewed.Entries, SegmentRefDTO{
				Container: entry.Container, Object: entry.Object, Size: entry.Size, ETag: entry.ETag})
		}
	case swiftcontrol.ClassDLO:
		slash := strings.Index(detail.Manifest, "/")
		if slash <= 0 {
			return ReviewedMembershipDTO{}, fmt.Errorf("malformed X-Object-Manifest %q", detail.Manifest)
		}
		reviewed.Namespace = detail.Manifest[:slash] + "/" + dloNamespace(detail.Manifest[slash+1:])
	}
	return reviewed, nil
}

// DeleteManifestWithSegments deletes a manifest and its segments as one job
// (ADR 0095 decision 8). It is always gated on the Backstage force with the
// typed object name, because ownership evidence is advisory until a live
// reverse-reference scan exists. The job owns the reviewed graph the request
// carries: it deletes the manifest by the reviewed-manifest protocol, which
// refuses a graph swap or a moved namespace, and only after that is durably
// recorded deleted does it delete the segments by the per-name protocol. The
// result is the job id; the drawer shows the manifest and each segment.
func (s *DeleteService) DeleteManifestWithSegments(ctx context.Context, req SegmentsDeleteRequestDTO) (string, error) {
	if err := refuseIfDeferred("DeleteService.DeleteManifestWithSegments"); err != nil {
		return "", err
	}
	if req.BookmarkID == "" || req.Container == "" || req.Name == "" {
		return "", errors.New("bookmark, container, and object are required")
	}
	if req.Confirm != req.Name {
		return "", fmt.Errorf("confirmation does not match the object name — type it exactly")
	}
	session, err := s.connections.SessionFor(ctx, req.BookmarkID)
	if err != nil {
		return "", err
	}
	resource := req.Container + "/" + req.Name
	// The startup read also confirms the class and resolves the dynamic
	// namespace for the outside-name refusal, all before any job row exists.
	detail, ownership, _, err := s.segmentScope(ctx, session, req)
	if err != nil {
		return "", err
	}
	if !req.Force {
		s.runner.record(ctx, req.BookmarkID, "segments.delete", resource,
			"refused: segment deletion needs Backstage force (ownership evidence is advisory until the live reference scan exists)", audit.ResultFailed)
		return "", fmt.Errorf("deleting %s's segments needs the Backstage force with typed confirmation: ownership evidence identifies likely-owned segments but cannot yet PROVE no other object references them (%s)", resource, ownership.Target)
	}
	if req.Reviewed.Class != "" && req.Reviewed.Class != string(detail.Class) {
		return "", fmt.Errorf("the reviewed graph is a %s but %s is a %s now — re-open the dialog to review the current graph", req.Reviewed.Class, resource, detail.Class)
	}
	job := &segmentJob{s: s, bookmarkID: req.BookmarkID, container: req.Container, name: req.Name,
		resource: resource, reviewed: req.Reviewed, basis: segmentBasis(ownership)}
	if detail.Class == swiftcontrol.ClassDLO {
		slash := strings.Index(detail.Manifest, "/")
		job.segContainer, job.segPrefix = detail.Manifest[:slash], detail.Manifest[slash+1:]
		job.namespace = dloNamespace(job.segPrefix)
		// The outside-name refusal is made before any effect and before any
		// job row exists: a manifest that reads names outside its namespace is
		// never acted on (ADR 0044). The listing is admitted startup I/O.
		outside, err := namesOutsideNamespace(ctx, session, job.segContainer, job.segPrefix, job.namespace)
		if err != nil {
			s.runner.record(ctx, req.BookmarkID, "segments.delete", resource,
				"refused: the segment listing did not complete, so the destructive scope is unknown", audit.ResultFailed)
			return "", err
		}
		if outside.count > 0 {
			s.runner.record(ctx, req.BookmarkID, "segments.delete", resource,
				fmt.Sprintf("refused: %d observed name(s) outside the namespace %s/%s", outside.count, job.segContainer, job.namespace), audit.ResultFailed)
			return "", outsideRefusal(job.segContainer, job.namespace, outside)
		}
	}
	// The reviewed facts recorded against the manifest member.
	job.manifestListed = jobs.ItemIntent{
		ItemIdentity: jobs.ItemIdentity{Key: req.Container + "/" + req.Name, Source: jobs.ItemLocation{
			Kind: jobs.ItemSwift, BookmarkID: req.BookmarkID, Container: req.Container, Object: req.Name}},
		Planned: true, Admitted: true, ListedBytes: detail.ContentLength,
		ListedETag: strings.Trim(detail.ETag, `"`), ListedSLO: detail.Class == swiftcontrol.ClassSLO,
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
	created, err := s.jobs.Create(ctx, jobs.Job{
		Type: jobs.TypeDeleteSegments, ItemRecordsVersion: 1,
		Source: resource, Destination: "(manifest + segments)", SourceBookmarkID: req.BookmarkID, Engine: "native",
	})
	if err != nil {
		return "", err
	}
	job.jobID = created.ID
	job.session = session
	_ = s.jobs.SetSettings(ctx, created.ID, fmt.Sprintf(`{"strategy":%q,"basis":%q,"class":%q}`, sweepStrategy, job.basis, detail.Class))
	s.runner.register(created.ID, owner.cancel)
	s.runner.record(ctx, req.BookmarkID, "segments.delete.start", resource, "job "+created.ID, audit.ResultApplied)
	owner.launch(created.ID, req.BookmarkID, releasePins, job.run, func(background context.Context) {
		job.finish(background, jobs.StateCancelled, "", 0)
	})
	releasePins = nil
	return created.ID, nil
}

// segmentBasis names what the force rests on, for the job's configuration.
func segmentBasis(ownership *SegmentOwnershipDTO) string {
	if ownership.Proven {
		return "backstage-forced-with-evidence"
	}
	return "backstage-forced"
}
