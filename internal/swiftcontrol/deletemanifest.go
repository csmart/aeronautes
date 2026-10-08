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
// baseContent is the ContentFingerprint the caller was shown, and ADR 0085
// makes it the third guard. This HEAD and the DELETE that follows it are
// adjacent — nothing of ours runs between them — which makes this the narrowest
// window any destructive path here can offer, and the comparison is what turns
// "delete this name" into "delete what I was shown at this name".
//
// It narrows and does not close: this store honours no conditional delete, so a
// writer landing between this HEAD's reply and the DELETE's arrival still loses
// its data. Nothing here may be described as exclusive ownership, a lock, or
// proof that nobody else is writing.
//
// An empty baseContent is refused rather than skipped, unlike the metadata
// editor's optional conflict base. A metadata write with no base is a first
// write after a fresh read; a delete with no base is a caller that cannot say
// what it meant to destroy.
//
// The preflight detail is returned for audit summaries; the delete is
// verified gone by re-read.
func (s *Session) DeleteManifestObject(ctx context.Context, container, name, baseContent string) (*ObjectDetail, error) {
	if baseContent == "" {
		return nil, fmt.Errorf("deleting %s/%s needs the content identity the caller was shown; a delete that cannot say what it meant to destroy is refused", container, name)
	}
	before, err := s.ObjectDetail(ctx, container, name)
	if err != nil {
		return nil, err
	}
	if before.Class == ClassPlain {
		return nil, fmt.Errorf("%s/%s is a plain object, not a manifest; the manifest-only delete refuses it", container, name)
	}
	if found := before.ContentFingerprint(); found != baseContent {
		// Both identities travel with the refusal. ADR 0085 requires the record
		// to carry what was expected as well as what was found, because an
		// operator told only that a delete was refused learns nothing, while one
		// shown that the object is now a different size with a different digest
		// learns that something else is writing to their account.
		return before, &IdentityConflictError{
			Container: container, Name: name,
			Expected: baseContent, Found: found,
			FoundClass: string(before.Class), FoundLength: before.ContentLength,
			FoundETag: etagValue(before.ETag),
		}
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

// IdentityConflictError is a destructive request refused because the name no
// longer holds what the caller was shown. It is a distinct type so a caller, an
// audit record and a reader of either can tell this apart from a transport
// failure, a missing object or a class refusal (ADR 0085).
type IdentityConflictError struct {
	Container, Name string
	// Expected is the identity the caller supplied; Found is the identity the
	// name holds now, with the fields it was derived from beside it so a reader
	// does not have to own the hashing to understand the refusal.
	Expected, Found string
	FoundClass      string
	FoundLength     int64
	FoundETag       string
}

func (e *IdentityConflictError) Error() string {
	return fmt.Sprintf("%s/%s is not what you were shown: it now reports a %s of %d bytes with entity tag %s, so nothing was deleted (expected identity %s, found %s)",
		e.Container, e.Name, e.FoundClass, e.FoundLength, e.FoundETag, e.Expected, e.Found)
}
