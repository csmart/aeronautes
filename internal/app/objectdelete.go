package app

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/csmart/aeronautes/internal/audit"
	"github.com/csmart/aeronautes/internal/mutationfacts"
	"github.com/csmart/aeronautes/internal/swiftcontrol"
)

// The residue errors are returned when the DELETE landed, or may have
// landed, and the outcome transaction did not. The identity's only durable
// row is then its intent, so a reopen reports no terminal outcome and the
// error carries the identity (ADR 0092 decision 4, ADR 0094 decision 1).
var (
	errObjectDeleteResidue   = errors.New("object delete residue: the delete landed or may have landed and the outcome could not be recorded")
	errMarkerDeleteResidue   = errors.New("marker delete residue: the delete landed or may have landed and the outcome could not be recorded")
	errManifestDeleteResidue = errors.New("manifest delete residue: the delete landed or may have landed and the outcome could not be recorded")
)

// shownDeletion is one of the three deletions of an object the operator was
// shown: the deferred-table action, the ledger operation, the words its
// records use, its residue error and the session protocol it runs. The
// service path around them is one (ADR 0094 decision 1).
type shownDeletion struct {
	action    string
	operation string
	noun      string
	residue   error
	run       func(*swiftcontrol.Session, context.Context, string, string, string) (*swiftcontrol.ObjectDeleteResult, error)
}

var (
	objectDeletion = shownDeletion{action: "MutateService.DeleteObject", operation: "object.delete", noun: "object", residue: errObjectDeleteResidue,
		run: (*swiftcontrol.Session).DeletePlainObject}
	markerDeletion = shownDeletion{action: "MutateService.DeleteMarker", operation: "marker.delete", noun: "marker", residue: errMarkerDeleteResidue,
		run: (*swiftcontrol.Session).DeleteMarkerObject}
	manifestDeletion = shownDeletion{action: "MutateService.DeleteManifest", operation: "manifest.delete", noun: "manifest", residue: errManifestDeleteResidue,
		run: (*swiftcontrol.Session).DeleteManifestObject}
)

// DeleteObject deletes ONE plain object the operator was shown (ADR 0092).
// It is bound to the portrait the inspector's detail was read under
// (ADR 0079), carries the content identity that detail showed, records its
// intent before the store is asked and its outcome with the audit event in
// one transaction (ADR 0058), and returns the literal successor the
// verifying read observed so the interface can show it. Manifests, the
// marker journey on a prefix row and symlinks keep their own paths.
func (s *MutateService) DeleteObject(ctx context.Context, scope ListingScopeDTO, container, name, contentFingerprint string) (MutationResultDTO, error) {
	return s.deleteShown(ctx, objectDeletion, scope, container, name, contentFingerprint)
}

// DeleteMarker deletes ONE directory marker the operator was shown, the
// prefix detail's own zero-byte object, by the plain-object protocol plus
// the marker rule (ADR 0094 decision 2): the comparison HEAD must show the
// identity shown AND a zero-byte object of the directory content type, so
// a real object in a folder's clothing never leaves through this path. The
// objects under the prefix are never read or touched.
func (s *MutateService) DeleteMarker(ctx context.Context, scope ListingScopeDTO, container, prefix, contentFingerprint string) (MutationResultDTO, error) {
	return s.deleteShown(ctx, markerDeletion, scope, container, prefix, contentFingerprint)
}

// DeleteManifest deletes ONE dynamic or static large-object manifest the
// operator was shown, manifest-only: the segments stay by construction (§60
// Phase 4). It is the plain-object protocol with the class inverted (ADR
// 0094 decision 3), and a manifest of the same kind still observed at the
// name afterwards is unverified rather than compared.
func (s *MutateService) DeleteManifest(ctx context.Context, scope ListingScopeDTO, container, name, contentFingerprint string) (MutationResultDTO, error) {
	return s.deleteShown(ctx, manifestDeletion, scope, container, name, contentFingerprint)
}

func (s *MutateService) deleteShown(ctx context.Context, what shownDeletion, scope ListingScopeDTO, container, name, contentFingerprint string) (MutationResultDTO, error) {
	if err := refuseIfDeferred(what.action); err != nil {
		return MutationResultDTO{}, err
	}
	if container == "" || name == "" {
		// Refused before the writer gate, the scope and any intent: an empty
		// object name is the container's own path, and this action may never
		// broaden into a container action.
		return MutationResultDTO{}, errors.New("deleting an object needs both its container and its name; an empty name would address the container itself")
	}
	if err := s.occupy(); err != nil {
		return MutationResultDTO{}, err
	}
	defer s.writers.leave()
	entry, bookmarkID, _, err := s.boundSnapshot(ctx, scope)
	if err != nil {
		return MutationResultDTO{}, err
	}
	resource := container + "/" + name
	identity, err := mutationfacts.NewIdentity()
	if err != nil {
		return MutationResultDTO{}, err
	}
	if err := s.facts.RecordIntent(ctx, mutationfacts.Intent{
		Identity: identity, BookmarkID: bookmarkID, Operation: what.operation, Resource: resource,
	}); err != nil {
		return MutationResultDTO{}, err
	}

	result, err := what.run(entry.session, ctx, container, name, contentFingerprint)
	outcome, auditRes, summary, transIDs := what.fact(result, err)
	if recErr := s.recordMutationOutcome(ctx, identity, bookmarkID, what.operation, resource, outcome, auditRes, summary, transIDs); recErr != nil {
		if objectDeleteMayHaveLanded(result, err) {
			return MutationResultDTO{}, fmt.Errorf("%w: identity %s: %v", what.residue, identity, recErr)
		}
		if err != nil {
			// The refusal or failure destroyed nothing and is still the answer;
			// the caller also learns that it went unrecorded.
			return MutationResultDTO{}, errors.Join(err, fmt.Errorf("%s: the outcome could not be recorded: %w", resource, recErr))
		}
		return MutationResultDTO{}, recErr
	}
	if err != nil {
		if errors.Is(err, swiftcontrol.ErrUnverifiedDelete) {
			return MutationResultDTO{}, fmt.Errorf("%w: identity %s", err, identity)
		}
		return MutationResultDTO{}, err
	}
	dto := MutationResultDTO{Outcome: string(result.Outcome), TransIDs: result.TransIDs}
	if result.After != nil {
		after := toObjectDetailDTO(result.After)
		dto.ObjectAfter = &after
	}
	return dto, nil
}

// objectDeleteMayHaveLanded reports whether the store may have applied the
// DELETE: it answered success, or the reply was lost or was a server error,
// which ErrUnverifiedDelete marks. A refusal, a conflict or a client-side
// status destroyed nothing and is never residue.
func objectDeleteMayHaveLanded(result *swiftcontrol.ObjectDeleteResult, err error) bool {
	return result != nil && (result.Accepted || errors.Is(err, swiftcontrol.ErrUnverifiedDelete))
}

// fact classifies a deletion attempt into the ledger's outcome, the audit
// result and a summary. The object name stays out of the prose: it is the
// event's own Resource, and a valid name could make the whole record
// unrecordable through the summary filter.
func (what shownDeletion) fact(result *swiftcontrol.ObjectDeleteResult, err error) (mutationfacts.Outcome, audit.Result, string, []string) {
	var transIDs []string
	if result != nil {
		transIDs = result.TransIDs
	}
	var conflict *swiftcontrol.IdentityConflictError
	var refusal *swiftcontrol.ShownRefusalError
	switch {
	case errors.As(err, &conflict):
		summary := fmt.Sprintf("%s delete refused: the name no longer holds what was shown; expected identity %s, found %s on a %s of %d bytes with entity tag %s",
			what.noun, conflict.Expected, conflict.Found, conflict.FoundClass, conflict.FoundLength, conflict.FoundETag)
		return mutationfacts.OutcomeConflict, audit.ResultConflict, summary, transIDs
	case errors.As(err, &refusal):
		// What the comparison found explains the refusal: the class, the
		// length, the entity tag and the content type are the store's own
		// facts about the name, and the link target is withheld because it is
		// a name this code does not control. The content type is free prose
		// the store supplies, so it is asked of the filter before it is
		// composed: a type the filter would refuse costs those words and never
		// the record of a refusal that destroyed nothing.
		found := fmt.Sprintf("a %s of %d bytes with entity tag %s", refusal.FoundClass, refusal.FoundLength, refusal.FoundETag)
		if refusal.Link {
			found = "a symbolic link, whose target this record withholds"
		} else if withType := found + " and content type " + refusal.FoundType; audit.SummaryRefusal(withType) == "" {
			found = withType
		} else {
			found += " and a content type this record withholds"
		}
		summary := fmt.Sprintf("%s delete refused: the name holds %s, not what this path deletes; expected identity %s, found %s; this request destroyed nothing",
			what.noun, found, refusal.Expected, refusal.Found)
		return mutationfacts.OutcomeFailed, audit.ResultFailed, summary, transIDs
	case errors.Is(err, swiftcontrol.ErrUnverifiedDelete):
		return mutationfacts.OutcomeUnverified, audit.ResultUnverified, what.noun + " delete unverified; " + observedAfterDelete(result), transIDs
	case err != nil:
		return mutationfacts.OutcomeFailed, audit.ResultFailed, what.noun + " delete failed or refused; this request destroyed nothing", transIDs
	}
	return mutationfacts.OutcomeApplied, audit.ResultApplied, what.deletedSummary(result) + "; " + observedAfterDelete(result), transIDs
}

// deletedSummary says what was deleted and what stayed. For a dynamic large
// object the one thing worth keeping is where its segments are, the DLO's
// own X-Object-Manifest value, a name this code does not control: it is
// asked of the filter before it is composed, so a prefix the filter would
// refuse costs the prose and never the event.
func (what shownDeletion) deletedSummary(result *swiftcontrol.ObjectDeleteResult) string {
	switch what.noun {
	case "marker":
		return "deleted directory marker (children untouched)"
	case "manifest":
		summary := fmt.Sprintf("deleted %s manifest; segments remain", result.Before.Class)
		if result.Before.Manifest != "" {
			if withPrefix := summary + " at " + result.Before.Manifest; audit.SummaryRefusal(withPrefix) == "" {
				return withPrefix
			}
			return summary + " at a prefix this record withholds; the object name is in the event's resource"
		}
		return summary
	}
	return "deleted plain object"
}

// safeReadFailure names a read's failure by kind and status only. A transport
// error's text carries the request URL and with it the object name, which
// ADR 0092 keeps out of the prose and the summary filter may refuse outright,
// turning an ordinary unverified outcome into an unrecordable one.
func safeReadFailure(err error) string {
	var se *swiftcontrol.SwiftError
	if errors.As(err, &se) {
		if se.HTTPStatus != 0 {
			return fmt.Sprintf("%s, HTTP %d", se.Kind, se.HTTPStatus)
		}
		return string(se.Kind)
	}
	return "unknown"
}

// literalPlainDTO mirrors the session's classification: a plain object in
// every representation, so a link or a manifest is never compared by tuple.
func literalPlainDTO(d *swiftcontrol.ObjectDetail) bool {
	return d.Class == swiftcontrol.ClassPlain && d.SymlinkTarget == "" && !d.FollowedSymlink
}

// observedAfterDelete says what the literal read found at the name, as a
// kind, a length and an entity tag, and what that finding can and cannot
// mean (ADR 0092 decision 3).
func observedAfterDelete(result *swiftcontrol.ObjectDeleteResult) string {
	prefix := ""
	if result != nil && !result.Accepted {
		prefix = "no store answer proves the delete, and "
	}
	if result == nil || result.After == nil {
		observed := "nothing observed at the name afterwards"
		if result != nil && result.AfterRead != nil {
			observed = "the literal read of the name failed afterwards (" + safeReadFailure(result.AfterRead) + ")"
		}
		return prefix + observed
	}
	if result.Accepted && literalPlainDTO(result.After) && !result.AfterComparable {
		return "the name holds a plain object whose identity the literal read did not report completely, so it was not compared"
	}
	after := result.After
	kind := "plain object"
	switch {
	case after.SymlinkTarget != "":
		kind = "symlink"
	case after.Class != swiftcontrol.ClassPlain:
		kind = string(after.Class) + " manifest"
	}
	found := fmt.Sprintf("the name now holds a %s of %d bytes with entity tag %s", kind, after.ContentLength, strings.Trim(after.ETag, `"`))
	if result.SameIdentityObserved() {
		return prefix + found + ", the identity that was shown, so the delete may not have taken effect"
	}
	if result.SameKindManifestObserved() {
		return prefix + found + ", a manifest of the kind that was shown, which the literal read cannot tell from it, so the delete may not have taken effect"
	}
	return prefix + found + ", which may be the archived predecessor or a concurrent write"
}
