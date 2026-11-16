package app

import (
	"context"

	"github.com/csmart/aeronautes/internal/jobs"
	transfercopy "github.com/csmart/aeronautes/internal/transfer/copy"
	"github.com/csmart/aeronautes/internal/transfer/plan"
)

func copyItemIdentity(req CopyRequestDTO, key string) jobs.ItemIdentity {
	return jobs.ItemIdentity{Key: key,
		Source: jobs.ItemLocation{Kind: jobs.ItemSwift, BookmarkID: req.SourceBookmarkID,
			Container: req.SourceContainer, Object: req.SourcePrefix + key},
		Destination: jobs.ItemLocation{Kind: jobs.ItemSwift, BookmarkID: req.DestBookmarkID,
			Container: req.DestContainer, Object: req.DestPrefix + key}}
}

func copyItemObservation(observation transfercopy.Observation) jobs.ItemObservation {
	return jobs.ItemObservation{Result: jobs.ItemResult(observation.Result), Effect: jobs.ItemEffect(observation.Effect),
		CopiedBytes: observation.CopiedBytes, Diagnostic: jobs.ItemDiagnostic{
			Stage: jobs.ItemStage(observation.Diagnostic.Stage), Code: jobs.ItemCode(observation.Diagnostic.Code),
			HTTPStatus: observation.Diagnostic.HTTPStatus}}
}

// copyItemRecorder is the durable record of one copy run: each object
// admitted before any request for it, and what the engine or the store
// answered for it (ADR 0122 decision 7).
func (s *TransferService) copyItemRecorder(ctx context.Context, jobID string, req CopyRequestDTO) *transfercopy.Recorder {
	return &transfercopy.Recorder{
		Pause: func(live context.Context) error {
			return s.runner.checkpoint(live, jobID, req.SourceBookmarkID)
		},
		Admit: func(admission transfercopy.Admission) error {
			item := admission.Item
			// A copied item must be admitted (the ledger refuses a copied
			// outcome for an unadmitted item), so a name the operator agreed
			// to replace is admitted alongside New and Unknown. A Changed
			// item in a copy that replaces nothing stays unadmitted and only
			// ever records a refusal, which needs no admission.
			intent := jobs.ItemIntent{ItemIdentity: copyItemIdentity(req, item.Name), ListedBytes: item.SourceBytes,
				Planned: item.Verdict == plan.VerdictNew || item.Verdict == plan.VerdictUnknown || item.Verdict == plan.VerdictChanged,
				Admitted: item.Verdict == plan.VerdictNew || item.Verdict == plan.VerdictUnknown ||
					(item.Verdict == plan.VerdictChanged && req.AcknowledgeOverwrite)}
			if err := intent.Validate(); err != nil {
				return err
			}
			return s.jobs.AdmitItems(ctx, jobID, []jobs.ItemIntent{intent})
		},
		Execute: func(key string, observation transfercopy.Observation) error {
			return s.jobs.RecordExecution(ctx, jobID, key, copyItemObservation(observation))
		},
		Verify: func(key string, observation transfercopy.Observation) error {
			identity := copyItemIdentity(req, key)
			if err := identity.Validate(); err != nil {
				return err
			}
			return s.jobs.RecordComparison(ctx, jobID, identity, copyItemObservation(observation))
		},
	}
}
