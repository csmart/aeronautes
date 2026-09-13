package app

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/csmart/aeronautes/internal/audit"
	"github.com/csmart/aeronautes/internal/deletion"
	"github.com/csmart/aeronautes/internal/evidence"
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
}

// SegmentOwnershipDTO reports what a segments-delete would rest on.
type SegmentOwnershipDTO struct {
	Class string `json:"class"` // dlo | slo
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

// SegmentsDeleteResultDTO retains the manifest result and reported segment counts.
type SegmentsDeleteResultDTO struct {
	ManifestDeleted bool  `json:"manifestDeleted"`
	SegmentsDeleted int64 `json:"segmentsDeleted"`
	// SegmentsFailed counts reported errors, not proof of survival.
	// Incomplete accounting makes these counts last-confirmed progress.
	SegmentsFailed               int64  `json:"segmentsFailed"`
	Basis                        string `json:"basis"` // "exclusive-ownership" | "backstage-forced"
	DeletionAccountingIncomplete bool   `json:"deletionAccountingIncomplete,omitempty"`
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
	if req.BookmarkID == "" || req.Container == "" || req.Name == "" {
		return nil, errors.New("bookmark, container, and object are required")
	}
	session, err := s.connections.SessionFor(ctx, req.BookmarkID)
	if err != nil {
		return nil, err
	}
	_, ownership, _, err := s.segmentScope(ctx, session, req)
	return ownership, err
}

// DeleteManifestWithSegments removes a manifest AND its segments. It is
// ALWAYS gated on the Backstage force with typed confirmation — ownership
// evidence is advisory and never auto-authorizes (no live
// reverse-reference scan exists yet). The HEAD here is the §28.6 required
// authoritative re-check: the operation follows segments, so identity at
// execution time matters. The residual HEAD-to-DELETE window and the
// content-ETag-only recheck are stated honestly below (review W-10).
func (s *DeleteService) DeleteManifestWithSegments(ctx context.Context, req SegmentsDeleteRequestDTO) (*SegmentsDeleteResultDTO, error) {
	if req.BookmarkID == "" || req.Container == "" || req.Name == "" {
		return nil, errors.New("bookmark, container, and object are required")
	}
	if req.Confirm != req.Name {
		return nil, fmt.Errorf("confirmation does not match the object name — type it exactly")
	}
	session, err := s.connections.SessionFor(ctx, req.BookmarkID)
	if err != nil {
		return nil, err
	}
	detail, ownership, refs, err := s.segmentScope(ctx, session, req)
	if err != nil {
		return nil, err
	}
	resource := req.Container + "/" + req.Name
	// Evidence rows IDENTIFY candidate-owned segments; they do NOT
	// PROVE exclusivity — no live reverse-reference scan exists yet, so
	// an external or later manifest can share an Aeronautes-created
	// prefix (review R-02, K-SEG-004 second clause = §65.10). Until that
	// scan lands, a row-only "proof" MUST NOT auto-authorize
	// destruction: every manifest+segments delete requires the
	// deliberate Backstage force + typed confirmation. The proof still
	// shows in the dialog as context; it just never deletes on its own.
	if !req.Force {
		s.runner.record(ctx, req.BookmarkID, "segments.delete", resource,
			"refused: segment deletion needs Backstage force (ownership evidence is advisory until the live reference scan exists)", audit.ResultFailed)
		return nil, fmt.Errorf("deleting %s's segments needs the Backstage force with typed confirmation: ownership evidence identifies likely-owned segments but cannot yet PROVE no other object references them (%s)", resource, ownership.Target)
	}
	basis := "backstage-forced"
	if ownership.Proven {
		basis = "backstage-forced-with-evidence"
	}

	// The segment preflight runs BEFORE the final identity recheck. It
	// issues a listing, and a listing between that recheck and the DELETE
	// would widen the very window the recheck exists to close, so the
	// order is: resolve the destructive scope, refuse when it is wider
	// than this manifest's namespace, and only then re-read identity.
	//
	// A refusal here leaves the graph exactly as it was, including the
	// selected manifest, which is a separate authorised deletion that
	// normally sits outside the namespace it points at.
	var segContainer, namespace string
	if detail.Class == swiftcontrol.ClassDLO {
		slash := strings.Index(detail.Manifest, "/")
		segPrefix := detail.Manifest[slash+1:]
		segContainer, namespace = detail.Manifest[:slash], dloNamespace(segPrefix)
		outside, err := namesOutsideNamespace(ctx, session, segContainer, segPrefix, namespace)
		if err != nil {
			s.runner.record(ctx, req.BookmarkID, "segments.delete", resource,
				"refused: the segment listing did not complete, so the destructive scope is unknown", audit.ResultFailed)
			return nil, err
		}
		if outside.count > 0 {
			s.runner.record(ctx, req.BookmarkID, "segments.delete", resource,
				fmt.Sprintf("refused: %d observed name(s) outside the namespace %s/%s", outside.count, segContainer, namespace), audit.ResultFailed)
			return nil, outsideRefusal(segContainer, namespace, outside)
		}
	}

	// The proof/delete gap is bound as tightly as Swift allows: DELETE
	// ignores If-Match (measured on SAIO — a wrong etag still 204s), so
	// the manifest is re-HEADed IMMEDIATELY before deletion and any
	// identity change refuses. The residual is one HEAD-to-DELETE hop,
	// stated here rather than implied closed.
	recheck, err := session.ObjectDetail(ctx, req.Container, req.Name)
	if err != nil {
		return nil, err
	}
	if strings.Trim(recheck.ETag, `"`) != strings.Trim(detail.ETag, `"`) {
		s.runner.record(ctx, req.BookmarkID, "segments.delete", resource,
			"refused: the manifest changed between review and deletion", audit.ResultFailed)
		return nil, fmt.Errorf("%s changed since the segments were reviewed (etag moved) — re-open the dialog to review the CURRENT graph", resource)
	}

	result := &SegmentsDeleteResultDTO{Basis: basis}
	switch detail.Class {
	case swiftcontrol.ClassSLO:
		// Manifest-only DELETE first, then native deletion of EXACTLY
		// the fetched identities. Swift's ?multipart-manifest=delete
		// would follow whatever manifest occupies the live name at
		// execution time — a proof/delete TOCTOU — so it is never used:
		// the destructive set is the graph that was actually reviewed.
		if err := session.DeleteObject(ctx, req.Container, req.Name); err != nil {
			s.runner.record(ctx, req.BookmarkID, "segments.delete", resource, "manifest delete failed: "+err.Error(), audit.ResultFailed)
			return nil, err
		}
		result.ManifestDeleted = true
		for _, ref := range refs {
			if err := session.DeleteObject(ctx, ref.Container, ref.Object); err != nil {
				if swiftcontrol.IsNotFound(err) {
					continue
				}
				result.SegmentsFailed++
				continue
			}
			result.SegmentsDeleted++
		}
		if result.SegmentsFailed > 0 {
			s.runner.record(ctx, req.BookmarkID, "segments.delete", resource,
				fmt.Sprintf("PARTIAL: manifest + %d segments deleted, %d segment deletes failed (%s)", result.SegmentsDeleted, result.SegmentsFailed, basis), audit.ResultVerifyMismatch)
			return result, fmt.Errorf("%d segment deletes failed; the manifest is gone and %d segments were removed — retry the leftover names", result.SegmentsFailed, result.SegmentsDeleted)
		}
	case swiftcontrol.ClassDLO:
		// Manifest first, then the namespace through the native engine
		// (manifest-only per object; this scope IS the segments).
		if err := session.DeleteObject(ctx, req.Container, req.Name); err != nil {
			s.runner.record(ctx, req.BookmarkID, "segments.delete", resource, "manifest delete failed: "+err.Error(), audit.ResultFailed)
			return nil, err
		}
		result.ManifestDeleted = true
		capabilities, err := session.FetchCapabilities(ctx)
		if err != nil {
			return result, err
		}
		sweep, err := deletion.DeletePrefix(ctx, session, capabilities, segContainer, namespace, deletion.Options{})
		if sweep != nil {
			result.SegmentsDeleted = sweep.Deleted
			result.SegmentsFailed = sweep.Failed
			result.DeletionAccountingIncomplete = sweep.AccountingIncomplete
		}
		if errors.Is(err, deletion.ErrIdentityAccounting) || result.DeletionAccountingIncomplete {
			reason := fmt.Sprintf("the manifest is gone; segment-prefix deletion stopped after %d credited deletions and %d reported failures", result.SegmentsDeleted, result.SegmentsFailed)
			summary := "partial: manifest deleted; segment deletion accounting storage unavailable"
			if result.DeletionAccountingIncomplete {
				reason = "the manifest is gone; " + incompleteDeletionReason(sweep)
				summary = "partial: manifest deleted; deletion accounting incomplete"
			}
			problem := errors.New(reason)
			if err != nil {
				problem = fmt.Errorf("%s: %w", reason, err)
			}
			if auditErr := s.runner.audit.Append(context.WithoutCancel(ctx), audit.Event{
				BookmarkID: req.BookmarkID, Operation: "segments.delete", Resource: resource,
				Summary: summary, Result: audit.ResultVerifyMismatch,
			}); auditErr != nil {
				problem = errors.Join(problem, fmt.Errorf("record partial segment deletion audit: %w", auditErr))
			}
			return result, problem
		}
		if err != nil {
			s.runner.record(ctx, req.BookmarkID, "segments.delete", resource, "prefix sweep failed: "+err.Error(), audit.ResultFailed)
			return result, err
		}
		if sweep != nil && (sweep.Failed > 0 || sweep.Remaining > 0) {
			// Never "applied" with residue: the manifest is gone but the
			// prefix retains objects — the operator gets exact numbers
			// and the retained prefix for a retry.
			s.runner.record(ctx, req.BookmarkID, "segments.delete", resource,
				fmt.Sprintf("PARTIAL: %d segments deleted, %d failed, %d still answering under %s/%s (%s)",
					sweep.Deleted, sweep.Failed, sweep.Remaining, segContainer, namespace, basis), audit.ResultVerifyMismatch)
			return result, fmt.Errorf("%d segment deletes failed and %d names still answer under %s/%s — the manifest is gone; retry the prefix",
				sweep.Failed, sweep.Remaining, segContainer, namespace)
		}
	}
	s.runner.record(ctx, req.BookmarkID, "segments.delete", resource,
		fmt.Sprintf("%s: manifest + %d segments (%s)", ownership.Target, result.SegmentsDeleted, basis), audit.ResultApplied)
	return result, nil
}
