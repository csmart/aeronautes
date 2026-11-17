package app

import (
	"context"
	"errors"
	"fmt"

	"github.com/csmart/aeronautes/internal/audit"
	"github.com/csmart/aeronautes/internal/mutationfacts"
	"github.com/csmart/aeronautes/internal/swiftcontrol"
)

// The residue errors are returned when the DELETE landed, or may have
// landed, and the outcome transaction did not. The identity's only durable
// row is then its intent, so a reopen reports no terminal outcome and the
// error carries the identity (ADR 0058).
var (
	errObjectDeleteResidue   = errors.New("object delete residue: the delete landed or may have landed and the outcome could not be recorded")
	errMarkerDeleteResidue   = errors.New("marker delete residue: the delete landed or may have landed and the outcome could not be recorded")
	errManifestDeleteResidue = errors.New("manifest delete residue: the delete landed or may have landed and the outcome could not be recorded")
)

// shownDeletion is one of the three deletions of a single name the operator
// chose: the deferred-table action, the ledger operation, the word its
// records use and its residue error. They send the same request, an object
// DELETE of the name, and differ in what the window offered them for and in
// what their records say stayed (ADR 0122 decision 6).
type shownDeletion struct {
	action    string
	operation string
	noun      string
	residue   error
	// typed is whether the operator confirms this deletion by typing
	// "delete " and the name (ADR 0093 decision 9). The manifest deletion
	// is confirmed by its own force in Backstage instead.
	typed bool
}

var (
	objectDeletion   = shownDeletion{action: "MutateService.DeleteObject", operation: "object.delete", noun: "object", residue: errObjectDeleteResidue, typed: true}
	markerDeletion   = shownDeletion{action: "MutateService.DeleteMarker", operation: "marker.delete", noun: "marker", residue: errMarkerDeleteResidue, typed: true}
	manifestDeletion = shownDeletion{action: "MutateService.DeleteManifest", operation: "manifest.delete", noun: "manifest", residue: errManifestDeleteResidue}
)

// DeleteObject deletes ONE object by its name. It is bound to the portrait
// the inspector's detail was read under (ADR 0079), records its intent
// before the store is asked and its outcome with the audit event in one
// transaction (ADR 0058), and reports the store's answer: nothing is read
// before the DELETE or after it (ADR 0122 decision 6). confirm is what the
// operator typed, which must be "delete " and the name.
func (s *MutateService) DeleteObject(ctx context.Context, scope ListingScopeDTO, container, name, confirm string) (MutationResultDTO, error) {
	return s.deleteShown(ctx, objectDeletion, scope, container, name, confirm)
}

// DeleteMarker deletes ONE directory marker, the zero-byte object at a
// prefix's own name. The objects under the prefix are never read or touched.
// confirm must be "delete " and the marker's name.
func (s *MutateService) DeleteMarker(ctx context.Context, scope ListingScopeDTO, container, prefix, confirm string) (MutationResultDTO, error) {
	return s.deleteShown(ctx, markerDeletion, scope, container, prefix, confirm)
}

// DeleteManifest deletes ONE dynamic or static large-object manifest,
// manifest-only: the DELETE is the plain one, so every segment stays (§60
// Phase 4).
func (s *MutateService) DeleteManifest(ctx context.Context, scope ListingScopeDTO, container, name string) (MutationResultDTO, error) {
	return s.deleteShown(ctx, manifestDeletion, scope, container, name, "")
}

func (s *MutateService) deleteShown(ctx context.Context, what shownDeletion, scope ListingScopeDTO, container, name, confirm string) (MutationResultDTO, error) {
	if err := refuseIfDeferred(what.action); err != nil {
		return MutationResultDTO{}, err
	}
	if want := "delete " + name; what.typed && confirm != want {
		return MutationResultDTO{}, fmt.Errorf("confirmation %q does not match %q; type it exactly", confirm, want)
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

	result, err := entry.session.DeleteName(ctx, container, name)
	outcome, auditRes, summary, transIDs := what.fact(result, err)
	if recErr := s.recordMutationOutcome(ctx, identity, bookmarkID, what.operation, resource, outcome, auditRes, summary, transIDs); recErr != nil {
		if objectDeleteMayHaveLanded(result) {
			return MutationResultDTO{}, fmt.Errorf("%w: identity %s: %v", what.residue, identity, recErr)
		}
		if err != nil {
			// The refusal destroyed nothing and is still the answer; the
			// caller also learns that it went unrecorded.
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
	return MutationResultDTO{Outcome: string(result.Outcome), TransIDs: result.TransIDs}, nil
}

// objectDeleteMayHaveLanded reports whether the store may have applied the
// DELETE: it accepted it, or no answer came, or the answer was a server
// error. A refusal destroyed nothing and is never residue.
func objectDeleteMayHaveLanded(result *swiftcontrol.ObjectDeleteResult) bool {
	return result != nil && result.Outcome != ""
}

// fact puts the store's answer in the ledger's outcome, the audit result and
// a summary. The object name stays out of the prose: it is the event's own
// Resource, and a valid name could make the whole record unrecordable
// through the summary filter.
func (what shownDeletion) fact(result *swiftcontrol.ObjectDeleteResult, err error) (mutationfacts.Outcome, audit.Result, string, []string) {
	var transIDs []string
	if result != nil {
		transIDs = result.TransIDs
	}
	var refused *swiftcontrol.SwiftError
	switch {
	case errors.Is(err, swiftcontrol.ErrUnverifiedDelete):
		return mutationfacts.OutcomeUnverified, audit.ResultUnverified,
			what.noun + " delete unknown: the store did not answer the DELETE, or answered with a server error, so nothing says whether it was deleted", transIDs
	case errors.As(err, &refused) && refused.HTTPStatus != 0:
		return mutationfacts.OutcomeFailed, audit.ResultFailed,
			fmt.Sprintf("%s delete refused by the store with HTTP %d; nothing was deleted", what.noun, refused.HTTPStatus), transIDs
	case err != nil:
		return mutationfacts.OutcomeFailed, audit.ResultFailed, what.noun + " delete failed before the store was asked; nothing was deleted", transIDs
	}
	return mutationfacts.OutcomeApplied, audit.ResultApplied, what.deletedSummary(result), transIDs
}

// deletedSummary says what the store answered and what a deletion of this
// kind leaves.
func (what shownDeletion) deletedSummary(result *swiftcontrol.ObjectDeleteResult) string {
	said := "the store accepted the DELETE"
	if result.Gone {
		said = "the store answered that the name was already gone"
	}
	switch what.noun {
	case "marker":
		return "deleted directory marker (children untouched); " + said
	case "manifest":
		return "deleted manifest; segments remain; " + said
	}
	return "deleted object; " + said
}
