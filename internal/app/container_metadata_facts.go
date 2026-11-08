package app

import (
	"errors"
	"fmt"

	"github.com/csmart/aeronautes/internal/audit"
	"github.com/csmart/aeronautes/internal/mutationfacts"
	"github.com/csmart/aeronautes/internal/swiftcontrol"
)

var (
	errContainerMetadataResidue = errors.New("container metadata write residue: the store may have changed and the outcome could not be recorded")
	errContainerCreateResidue   = errors.New("container creation residue: the container may exist and the outcome could not be recorded")
	errMarkerCreateResidue      = errors.New("marker creation residue: the marker may exist and the outcome could not be recorded")
)

func containerMetadataPosted(result *swiftcontrol.ContainerUpdateResult, err error) bool {
	return errors.Is(err, swiftcontrol.ErrUnverifiedContainerWrite) || (result != nil && result.Outcome.Posted())
}

// containerCreateFact classifies a creation attempt (ADR 0077): a received
// status followed by a confirming read is applied, also when the read
// shows a policy the race did not honour, because the container exists;
// an unverified sentinel is unverified; anything else refused or failed
// before or at the PUT is failed.
func containerCreateFact(detail *swiftcontrol.ContainerDetail, err error) (mutationfacts.Outcome, audit.Result, string, []string) {
	var transIDs []string
	if detail != nil && detail.TransID != "" {
		transIDs = []string{detail.TransID}
	}
	if errors.Is(err, swiftcontrol.ErrUnverifiedContainerCreate) {
		return mutationfacts.OutcomeUnverified, audit.ResultUnverified, "container creation unverified", transIDs
	}
	if detail != nil {
		summary := fmt.Sprintf("created with policy %q", detail.StoragePolicy)
		if err != nil {
			summary = "created; " + err.Error()
		}
		return mutationfacts.OutcomeApplied, audit.ResultApplied, summary, transIDs
	}
	return mutationfacts.OutcomeFailed, audit.ResultFailed, "container create failed or refused", nil
}

// markerCreateFact classifies a marker creation attempt (ADR 0077).
func markerCreateFact(err error) (mutationfacts.Outcome, audit.Result, string) {
	switch {
	case err == nil:
		return mutationfacts.OutcomeApplied, audit.ResultApplied, "created directory marker"
	case errors.Is(err, swiftcontrol.ErrUnverifiedMarkerCreate):
		return mutationfacts.OutcomeUnverified, audit.ResultUnverified, "marker creation unverified"
	}
	return mutationfacts.OutcomeFailed, audit.ResultFailed, "marker create failed or refused"
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
