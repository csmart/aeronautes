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

func (s *TransferService) copyItemRecorder(ctx context.Context, jobID string, req CopyRequestDTO) *transfercopy.Recorder {
	return &transfercopy.Recorder{
		Admit: func(admission transfercopy.Admission) error {
			item := admission.Item
			intent := jobs.ItemIntent{ItemIdentity: copyItemIdentity(req, item.Name), ListedBytes: item.SourceBytes,
				Planned:  item.Verdict == plan.VerdictNew || item.Verdict == plan.VerdictUnknown || item.Verdict == plan.VerdictChanged,
				Admitted: item.Verdict == plan.VerdictNew || item.Verdict == plan.VerdictUnknown}
			if admission.StagingObject != "" {
				intent.Staging = &jobs.ItemLocation{Kind: jobs.ItemSwift, BookmarkID: req.DestBookmarkID,
					Container: req.DestContainer, Object: admission.StagingObject}
			}
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
