package swiftcontrol

import (
	"context"
	"errors"
	"fmt"
)

// DeleteManifestObject deletes ONLY a manifest object — the low-risk
// Phase 4 delete (§60): removing a DLO or SLO manifest unlinks the
// logical object while every segment stays untouched by construction.
//
// Two guards are the point:
//   - the preflight HEAD must classify DLO or SLO; a plain object
//     refuses — this path exists to delete manifests, nothing else;
//   - the DELETE is always plain: `?multipart-manifest=delete` (which
//     would take the segments with it) is exactly what this path must
//     never send. Segment-touching deletion is Phase 6, behind
//     ExclusiveOwnershipProof.
//
// The preflight detail is returned for audit summaries; the delete is
// verified gone by re-read.
func (s *Session) DeleteManifestObject(ctx context.Context, container, name string) (*ObjectDetail, error) {
	before, err := s.ObjectDetail(ctx, container, name)
	if err != nil {
		return nil, err
	}
	if before.Class == ClassPlain {
		return nil, fmt.Errorf("%s/%s is a plain object, not a manifest; the manifest-only delete refuses it", container, name)
	}

	if err := s.conn.ObjectDelete(ctx, container, name); err != nil {
		return before, Normalize(err)
	}

	_, err = s.ObjectDetail(ctx, container, name)
	if err == nil {
		return before, fmt.Errorf("manifest %s/%s still present after delete", container, name)
	}
	var se *SwiftError
	if errors.As(err, &se) && se.Kind == KindNotFound {
		return before, nil
	}
	return before, fmt.Errorf("manifest deleted but verify read failed: %w", err)
}
