package app

import (
	"context"
	"errors"
	"fmt"
	"net/textproto"

	"github.com/csmart/aeronautes/internal/audit"
	"github.com/csmart/aeronautes/internal/mutationfacts"
	"github.com/csmart/aeronautes/internal/swiftcontrol"
)

// errTempURLKeyResidue is returned when a slot write's outcome could not be
// recorded after a POST whose effect on the store is not known to be
// absent; the wording stays uncertain for the same reason as the metadata
// residues (§49, ADR 0062).
var errTempURLKeyResidue = errors.New("tempurl key write residue: the slot may have changed and the outcome could not be recorded")

// UpdateTempURLKey sets or clears one TempURL key slot of the connected
// account or of one container (§22, ADR 0101), the way the metadata
// editors write: a bound snapshot, a recorded intent, the verified adapter
// write and a recorded outcome with its audit event. The secret goes to the
// adapter and nowhere else; the intent, the outcome, the audit summary and
// the returned DTO carry the slot and the kind of change only.
func (s *MutateService) UpdateTempURLKey(ctx context.Context, scope ListingScopeDTO, edit TempURLKeyEditDTO) (MutationResultDTO, error) {
	if err := s.occupy(); err != nil {
		return MutationResultDTO{}, err
	}
	defer s.writers.leave()
	entry, bookmarkID, _, err := s.boundSnapshot(ctx, scope)
	if err != nil {
		return MutationResultDTO{}, err
	}
	// Each target records under its own operation name, as the account and
	// container metadata writes do, so an account write and a write to a
	// container that happens to be named "account" never share a record.
	operation, resource := "account.tempurl.update", "account"
	if edit.Container != "" {
		operation, resource = "container.tempurl.update", edit.Container
	}

	identity, err := mutationfacts.NewIdentity()
	if err != nil {
		return MutationResultDTO{}, err
	}
	if err := s.facts.RecordIntent(ctx, mutationfacts.Intent{
		Identity: identity, BookmarkID: bookmarkID, Operation: operation, Resource: resource,
	}); err != nil {
		return MutationResultDTO{}, err
	}

	result, err := entry.session.UpdateTempURLKey(ctx, edit.Container, edit.BaseSlots, swiftcontrol.TempURLKeyEdit{
		Slot: edit.Slot, Secret: edit.Secret, Clear: edit.Clear,
	})
	outcome, auditRes, summary, transIDs := tempURLKeyFact(result, err, edit.Slot, edit.Clear)
	if recErr := s.recordMutationOutcome(ctx, identity, bookmarkID, operation, resource, outcome, auditRes, summary, transIDs); recErr != nil {
		if tempURLKeyPosted(result, err) {
			return MutationResultDTO{}, fmt.Errorf("%w: identity %s: %v", errTempURLKeyResidue, identity, recErr)
		}
		return MutationResultDTO{}, recErr
	}
	if err != nil {
		if errors.Is(err, swiftcontrol.ErrUnverifiedTempURLKeyWrite) {
			return MutationResultDTO{}, fmt.Errorf("%w: identity %s", swiftcontrol.ErrUnverifiedTempURLKeyWrite, identity)
		}
		return MutationResultDTO{}, err
	}
	dto := MutationResultDTO{
		Outcome:  string(result.Outcome),
		Mismatch: result.Mismatch,
		TransIDs: result.TransIDs,
	}
	switch {
	case result.AccountAfter != nil:
		after := accountDTO(result.AccountAfter, scope, entry.session.StorageURL())
		dto.AccountAfter = &after
	case result.ContainerAfter != nil:
		after := s.containerAfterDTO(ctx, entry, bookmarkID, scope, result.ContainerAfter)
		dto.ContainerAfter = &after
	}
	return dto, nil
}

func tempURLKeyPosted(result *swiftcontrol.TempURLKeyResult, err error) bool {
	return errors.Is(err, swiftcontrol.ErrUnverifiedTempURLKeyWrite) || (result != nil && result.Outcome.Posted())
}

// tempURLKeyFact derives the recorded outcome, the audit result and the
// summary of a slot write. Every summary names the slot and whether it was
// to be set or cleared (ADR 0101 decision 2), an unverified or failed write
// included, since those are the ones an operator has to follow up. It takes
// the slot and the flag and not the edit, so the secret cannot reach a
// record from here (K-SEC-006).
func tempURLKeyFact(result *swiftcontrol.TempURLKeyResult, err error, slot string, clear bool) (mutationfacts.Outcome, audit.Result, string, []string) {
	var transIDs []string
	if result != nil {
		transIDs = result.TransIDs
	}
	// The slot is recorded before the adapter has validated it, so only the
	// two names Swift reads are quoted.
	named := "slot (unrecognised)"
	if canonical := textproto.CanonicalMIMEHeaderKey(slot); canonical == "Temp-Url-Key" || canonical == "Temp-Url-Key-2" {
		named = "slot " + canonical
	}
	done, doing := "set", "setting"
	if clear {
		done, doing = "cleared", "clearing"
	}
	if errors.Is(err, swiftcontrol.ErrUnverifiedTempURLKeyWrite) || (result != nil && result.Outcome == swiftcontrol.UpdateUnverified) {
		return mutationfacts.OutcomeUnverified, audit.ResultUnverified, fmt.Sprintf("%s TempURL key %s could not be verified", doing, named), transIDs
	}
	if err == nil && result != nil {
		summary := fmt.Sprintf("%s TempURL key %s", done, named)
		switch result.Outcome {
		case swiftcontrol.UpdateApplied:
			return mutationfacts.OutcomeApplied, audit.ResultApplied, summary, transIDs
		case swiftcontrol.UpdateConflict:
			return mutationfacts.OutcomeConflict, audit.ResultConflict, summary, transIDs
		case swiftcontrol.UpdateVerifyMismatch:
			return mutationfacts.OutcomeVerifyMismatch, audit.ResultVerifyMismatch, summary, transIDs
		}
	}
	return mutationfacts.OutcomeFailed, audit.ResultFailed, fmt.Sprintf("%s TempURL key %s failed", doing, named), transIDs
}
