package swiftcontrol

import (
	"context"
	"errors"
	"fmt"
	"net/textproto"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/ncw/swift/v2"
)

// Object metadata update (§17, §18): Swift's object POST replaces the
// whole editable surface, so the caller states the COMPLETE intended
// outcome — every metadata key, the content type, the expiry — built
// from a working copy of the current state. The update preflights
// against the working copy's fingerprint, re-sends what the edit must
// not destroy (the DLO manifest above all), and verifies by re-reading.

// ObjectMetadataEdit is the complete intended editable state.
type ObjectMetadataEdit struct {
	// Meta is the complete intended user metadata set — not a delta.
	// Anything absent here is removed by Swift's replace-all POST.
	Meta map[string]string
	// ContentType "" means keep the current content type.
	ContentType string
	// DeleteAt zero means no scheduled expiry (an existing one is
	// cleared — state the current value to keep it).
	DeleteAt time.Time
}

// ConcurrentEditLimitation is what every completed metadata save must
// say about itself. It is a statement of what was NOT established, never
// a claim that a conflict was detected: this path cannot tell a
// concurrent write from an undisturbed one, so the qualification is
// unconditional (ADR 0046).
const ConcurrentEditLimitation = "this does not establish that no concurrent edit was overwritten: the check before the write cannot see a change that lands after it"

// UpdateOutcome is how an update ended.
type UpdateOutcome string

const (
	// UpdateApplied: written and verified.
	UpdateApplied UpdateOutcome = "applied"
	// UpdateConflict: the object changed since the working copy was
	// taken; nothing was written (§18 best-effort detection).
	UpdateConflict UpdateOutcome = "conflict"
	// UpdateVerifyMismatch: written, but the re-read differs from the
	// intended outcome; surfaced, never hidden.
	UpdateVerifyMismatch UpdateOutcome = "verify_mismatch"
	// UpdateUnverified: written, but the verify HEAD did not complete.
	// The POST is unknown/unverified, not failed (ADR 0058). ADR 0046
	// still applies: a completed POST does not establish exclusivity.
	UpdateUnverified UpdateOutcome = "unverified"
)

// ErrUnverifiedWrite is returned with a result when a metadata POST
// succeeded and the verify read failed.
var ErrUnverifiedWrite = errors.New("object metadata write unverified")

// ObjectUpdateResult reports an update attempt.
type ObjectUpdateResult struct {
	Outcome UpdateOutcome
	// Before is the preflight HEAD; After the verify HEAD (nil when the
	// update conflicted and nothing was written).
	Before *ObjectDetail
	After  *ObjectDetail
	// Mismatch says what verify found different, for UpdateVerifyMismatch.
	Mismatch string
	// Limitation states what a COMPLETED write does not establish, and is
	// empty when nothing was written. The preflight comparison cannot see
	// a write that lands after it, and the verify read asserts the
	// intended post-state, which an overwrite satisfies exactly — so a
	// write that succeeded is written and verified, never exclusive
	// (ADR 0046, K-META-003). Reported rather than detected because this
	// store offers no conditional metadata write to build a comparison
	// on, and the entity tag a condition would carry is the content
	// digest, which a metadata POST does not change.
	Limitation string
	// TransIDs are the Swift transaction ids: preflight, write, verify.
	TransIDs []string
}

// UpdateObjectMetadata applies edit to one object. baseFingerprint is
// the EditableFingerprint of the working copy the edit was built from;
// "" skips conflict detection (first write after a fresh read still
// preflights for the manifest state).
func (s *Session) UpdateObjectMetadata(ctx context.Context, container, name, baseFingerprint string, edit ObjectMetadataEdit) (*ObjectUpdateResult, error) {
	if err := validateMetadata(edit.Meta, true); err != nil {
		return nil, err
	}
	before, err := s.ObjectDetail(ctx, container, name)
	if err != nil {
		return nil, err
	}
	if before.FollowedSymlink || before.SymlinkTarget != "" {
		// A symlink in ANY form is refused: a followed link (same
		// account) OR a link whose target headers are present without
		// being followed (cross-account — review R-08). A POST would
		// apply metadata to the LINK and 307 (measured on SAIO, F-05),
		// so the edit is refused until the ?symlink=get protocol exists
		// (§65.10); edit the target object directly.
		return nil, fmt.Errorf("%s/%s is a symlink: editing through it would write the TARGET's metadata onto the LINK (Swift POSTs to the link, then redirects) — open and edit the target object instead", container, name)
	}
	result := &ObjectUpdateResult{Before: before, TransIDs: []string{before.TransID}}

	if baseFingerprint != "" && before.EditableFingerprint != baseFingerprint {
		result.Outcome = UpdateConflict
		return result, nil
	}

	headers := buildObjectPostHeaders(before, edit)
	writeTransID, err := s.post(ctx, container, name, headers)
	if err != nil {
		return nil, err
	}
	result.TransIDs = append(result.TransIDs, writeTransID)
	// The write has happened; from here every outcome is a completed save
	// and carries the same limit, including a verify mismatch.
	result.Limitation = ConcurrentEditLimitation

	after, err := s.ObjectDetail(ctx, container, name)
	if err != nil {
		// The POST completed; ADR 0046's limitation applies. The verify
		// HEAD did not, so this is unverified rather than failed (ADR 0058).
		result.Outcome = UpdateUnverified
		return result, fmt.Errorf("%w: update written (trans %s) but verify read failed: %v", ErrUnverifiedWrite, writeTransID, err)
	}
	result.After = after
	result.TransIDs = append(result.TransIDs, after.TransID)

	if mismatch := verifyObjectOutcome(before, edit, after); mismatch != "" {
		result.Outcome = UpdateVerifyMismatch
		result.Mismatch = mismatch
		return result, nil
	}
	result.Outcome = UpdateApplied
	return result, nil
}

// buildObjectPostHeaders assembles the COMPLETE replace-all POST: the
// intended metadata, an explicit content type, the preserved DLO
// manifest (the §60 gate: a DLO must remain a DLO), and the intended
// expiry. Nothing is left to server-side defaults.
func buildObjectPostHeaders(before *ObjectDetail, edit ObjectMetadataEdit) swift.Headers {
	headers := swift.Headers{}
	for key, value := range edit.Meta {
		headers[objectMetaPrefix+key] = value
	}
	contentType := edit.ContentType
	if contentType == "" {
		contentType = before.ContentType
	}
	if contentType != "" {
		headers["Content-Type"] = contentType
	}
	if before.Class == ClassDLO && before.Manifest != "" {
		headers["X-Object-Manifest"] = before.Manifest
	}
	if !edit.DeleteAt.IsZero() {
		headers["X-Delete-At"] = strconv.FormatInt(edit.DeleteAt.Unix(), 10)
	}
	return headers
}

// verifyObjectOutcome compares the verify HEAD against the intent; ""
// means the outcome matches. Key comparison is canonical-case, matching
// how the server reports metadata back.
func verifyObjectOutcome(before *ObjectDetail, edit ObjectMetadataEdit, after *ObjectDetail) string {
	intended := map[string]string{}
	for key, value := range edit.Meta {
		intended[textproto.CanonicalMIMEHeaderKey(key)] = value
	}
	var problems []string

	for key, want := range intended {
		if got, ok := after.Meta[key]; !ok {
			problems = append(problems, fmt.Sprintf("metadata %q missing after write", key))
		} else if got != want {
			problems = append(problems, fmt.Sprintf("metadata %q = %q, want %q", key, got, want))
		}
	}
	for key := range after.Meta {
		if _, ok := intended[key]; !ok {
			problems = append(problems, fmt.Sprintf("unexpected metadata %q present after write", key))
		}
	}

	wantContentType := edit.ContentType
	if wantContentType == "" {
		wantContentType = before.ContentType
	}
	if wantContentType != "" && after.ContentType != wantContentType {
		problems = append(problems, fmt.Sprintf("content type %q, want %q", after.ContentType, wantContentType))
	}

	if before.Class == ClassDLO && after.Class != ClassDLO {
		problems = append(problems, "object is no longer a DLO after the edit (manifest lost)")
	}
	if before.Class == ClassSLO && after.Class != ClassSLO {
		problems = append(problems, "object is no longer an SLO after the edit")
	}

	switch {
	case edit.DeleteAt.IsZero() && !after.DeleteAt.IsZero():
		problems = append(problems, "scheduled expiry still present, intended none")
	case !edit.DeleteAt.IsZero() && after.DeleteAt.Unix() != edit.DeleteAt.Unix():
		problems = append(problems, fmt.Sprintf("expiry %v, want %v", after.DeleteAt, edit.DeleteAt))
	}

	sort.Strings(problems)
	return strings.Join(problems, "; ")
}
