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

// errObjectDeleteResidue is returned when the DELETE landed, or may have
// landed, and the outcome transaction did not. The identity's only durable
// row is then its intent, so a reopen reports no terminal outcome and the
// error carries the identity (ADR 0092 decision 4).
var errObjectDeleteResidue = errors.New("object delete residue: the delete landed or may have landed and the outcome could not be recorded")

// DeleteObject deletes ONE plain object the operator was shown (ADR 0092).
// It is bound to the portrait the inspector's detail was read under
// (ADR 0079), carries the content identity that detail showed, records its
// intent before the store is asked and its outcome with the audit event in
// one transaction (ADR 0058), and returns the literal successor the
// verifying read observed so the interface can show it. Manifests, the
// marker journey on a prefix row and symlinks keep their own paths.
func (s *MutateService) DeleteObject(ctx context.Context, scope ListingScopeDTO, container, name, contentFingerprint string) (MutationResultDTO, error) {
	if err := refuseIfDeferred("MutateService.DeleteObject"); err != nil {
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
		Identity: identity, BookmarkID: bookmarkID, Operation: "object.delete", Resource: resource,
	}); err != nil {
		return MutationResultDTO{}, err
	}

	result, err := entry.session.DeletePlainObject(ctx, container, name, contentFingerprint)
	outcome, auditRes, summary, transIDs := objectDeleteFact(result, err)
	if recErr := s.recordMutationOutcome(ctx, identity, bookmarkID, "object.delete", resource, outcome, auditRes, summary, transIDs); recErr != nil {
		if objectDeleteMayHaveLanded(result, err) {
			return MutationResultDTO{}, fmt.Errorf("%w: identity %s: %v", errObjectDeleteResidue, identity, recErr)
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

// objectDeleteFact classifies a deletion attempt into the ledger's outcome,
// the audit result and a summary. The object name stays out of the prose,
// as it does for the manifest deletion: it is the event's own Resource, and
// a valid name could make the whole record unrecordable through the
// summary filter.
func objectDeleteFact(result *swiftcontrol.ObjectDeleteResult, err error) (mutationfacts.Outcome, audit.Result, string, []string) {
	var transIDs []string
	if result != nil {
		transIDs = result.TransIDs
	}
	var conflict *swiftcontrol.IdentityConflictError
	switch {
	case errors.As(err, &conflict):
		summary := fmt.Sprintf("object delete refused: the name no longer holds what was shown; expected identity %s, found %s on a %s of %d bytes with entity tag %s",
			conflict.Expected, conflict.Found, conflict.FoundClass, conflict.FoundLength, conflict.FoundETag)
		return mutationfacts.OutcomeConflict, audit.ResultConflict, summary, transIDs
	case errors.Is(err, swiftcontrol.ErrUnverifiedDelete):
		return mutationfacts.OutcomeUnverified, audit.ResultUnverified, "object delete unverified; " + observedAfterDelete(result), transIDs
	case err != nil:
		return mutationfacts.OutcomeFailed, audit.ResultFailed, "object delete failed or refused; this request destroyed nothing", transIDs
	}
	return mutationfacts.OutcomeApplied, audit.ResultApplied, "deleted plain object; " + observedAfterDelete(result), transIDs
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
	return prefix + found + ", which may be the archived predecessor or a concurrent write"
}
