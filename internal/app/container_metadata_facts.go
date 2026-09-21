package app

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/csmart/aeronautes/internal/audit"
	"github.com/csmart/aeronautes/internal/mutationfacts"
	"github.com/csmart/aeronautes/internal/swiftcontrol"
)

var errContainerMetadataResidue = errors.New("container metadata write residue: the store may have changed and the outcome could not be recorded")

func (s *MutateService) recordContainerOutcome(ctx context.Context, identity, bookmarkID, resource string, outcome mutationfacts.Outcome, result audit.Result, summary string, transIDs []string) error {
	return s.facts.RecordOutcome(ctx, identity, outcome, summary, transIDs, func(tx *sql.Tx) error {
		return s.audit.AppendTx(ctx, tx, audit.Event{
			BookmarkID: bookmarkID, Operation: "container.update", Resource: resource,
			Summary: summary, Result: result, TransIDs: transIDs,
		})
	})
}

func containerMetadataFact(result *swiftcontrol.ContainerUpdateResult, err error, edit swiftcontrol.ContainerEdit) (mutationfacts.Outcome, audit.Result, string, []string) {
	var transIDs []string
	if result != nil {
		transIDs = result.TransIDs
	}
	if errors.Is(err, swiftcontrol.ErrUnverifiedContainerWrite) || (result != nil && result.Outcome == swiftcontrol.UpdateUnverified) {
		return mutationfacts.OutcomeUnverified, audit.ResultUnverified, "container metadata write unverified", transIDs
	}
	if err == nil && result != nil {
		summary := fmt.Sprintf("set %d metadata keys, versioning %s", len(edit.Meta), edit.Versioning.Mode)
		switch result.Outcome {
		case swiftcontrol.UpdateApplied:
			return mutationfacts.OutcomeApplied, audit.ResultApplied, summary, transIDs
		case swiftcontrol.UpdateConflict:
			return mutationfacts.OutcomeConflict, audit.ResultConflict, summary, transIDs
		case swiftcontrol.UpdateVerifyMismatch:
			return mutationfacts.OutcomeVerifyMismatch, audit.ResultVerifyMismatch, summary, transIDs
		}
	}
	return mutationfacts.OutcomeFailed, audit.ResultFailed, "container metadata update failed", transIDs
}
