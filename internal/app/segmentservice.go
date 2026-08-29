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

// The §34/§35 segment-cleanup half of DeleteService: manifest-only
// stays the default everywhere; manifest+segments requires
// ExclusiveOwnershipProof, and without it only the deliberate Backstage
// flow (force + typed confirmation) proceeds — with the honest §34
// warning that sharing cannot be disproved from one manifest.

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
}

// SegmentsDeleteResultDTO reports what actually died.
type SegmentsDeleteResultDTO struct {
	ManifestDeleted bool   `json:"manifestDeleted"`
	SegmentsDeleted int64  `json:"segmentsDeleted"`
	Basis           string `json:"basis"` // "exclusive-ownership" | "backstage-forced"
}

// sloSegmentRefs flattens a manifest's direct object references.
// Nested sub-SLO entries are references too; v1 does not chase them —
// a nested manifest in scope simply fails the proof.
func sloSegmentRefs(manifest *swiftcontrol.SLOManifest) []swiftcontrol.ObjectRef {
	var refs []swiftcontrol.ObjectRef
	for _, entry := range manifest.Entries {
		if segment, ok := entry.(swiftcontrol.ObjectSegment); ok {
			refs = append(refs, segment.Path)
		}
	}
	return refs
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
		ownership.Target = detail.Manifest
		slash := strings.Index(detail.Manifest, "/")
		if slash <= 0 {
			return nil, nil, nil, fmt.Errorf("malformed X-Object-Manifest %q", detail.Manifest)
		}
		proven, err := s.evidence.HasExclusiveOwnership(ctx, req.BookmarkID, detail.Manifest[:slash], detail.Manifest[slash+1:])
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
		refs := sloSegmentRefs(manifest)
		ownership.Segments = int64(len(refs))
		ownership.Target = fmt.Sprintf("%d segments", len(refs))
		if len(refs) != len(manifest.Entries) {
			// Nested or unrecognised entries: never provable in v1.
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
			if strings.HasPrefix(ref.Object, proof.Prefix) {
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

// DeleteManifestWithSegments removes a manifest AND its segments —
// automatically only under proof; otherwise only through the Backstage
// force with typed confirmation. The HEAD here is the §28.6 required
// authoritative re-check: the operation follows segments, so identity
// at execution time matters.
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
	detail, ownership, _, err := s.segmentScope(ctx, session, req)
	if err != nil {
		return nil, err
	}
	resource := req.Container + "/" + req.Name
	if !ownership.Proven && !req.Force {
		s.runner.record(ctx, req.BookmarkID, "segments.delete", resource,
			"refused: no exclusive-ownership proof for "+ownership.Target, audit.ResultFailed)
		return nil, fmt.Errorf("segments are not proven exclusively this app's (%s): sharing cannot be disproved from one manifest — use the Backstage force only if you know better than the evidence", ownership.Target)
	}
	basis := "exclusive-ownership"
	if !ownership.Proven {
		basis = "backstage-forced"
	}

	result := &SegmentsDeleteResultDTO{Basis: basis}
	switch detail.Class {
	case swiftcontrol.ClassSLO:
		// Swift-native manifest+segments (§34): faster, not safer — the
		// gate above is the safety.
		bulk, err := session.DeleteSLOWithSegments(ctx, req.Container, req.Name)
		if err != nil {
			s.runner.record(ctx, req.BookmarkID, "segments.delete", resource, "slo delete failed: "+err.Error(), audit.ResultFailed)
			return nil, err
		}
		result.ManifestDeleted = true
		result.SegmentsDeleted = bulk.Deleted - 1 // the manifest is one of the count
		if result.SegmentsDeleted < 0 {
			result.SegmentsDeleted = 0
		}
	case swiftcontrol.ClassDLO:
		// Manifest first, then the exact prefix through the native
		// engine (manifest-only per object; this scope IS the segments).
		if err := session.DeleteObject(ctx, req.Container, req.Name); err != nil {
			s.runner.record(ctx, req.BookmarkID, "segments.delete", resource, "manifest delete failed: "+err.Error(), audit.ResultFailed)
			return nil, err
		}
		result.ManifestDeleted = true
		slash := strings.Index(detail.Manifest, "/")
		segContainer, segPrefix := detail.Manifest[:slash], detail.Manifest[slash+1:]
		capabilities, err := session.FetchCapabilities(ctx)
		if err != nil {
			return result, err
		}
		sweep, err := deletion.DeletePrefix(ctx, session, capabilities, segContainer, segPrefix, deletion.Options{})
		if sweep != nil {
			result.SegmentsDeleted = sweep.Deleted
		}
		if err != nil {
			s.runner.record(ctx, req.BookmarkID, "segments.delete", resource, "prefix sweep failed: "+err.Error(), audit.ResultFailed)
			return result, err
		}
	}
	s.runner.record(ctx, req.BookmarkID, "segments.delete", resource,
		fmt.Sprintf("%s: manifest + %d segments (%s)", ownership.Target, result.SegmentsDeleted, basis), audit.ResultApplied)
	return result, nil
}
