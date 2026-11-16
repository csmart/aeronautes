package app

import (
	"context"

	"github.com/csmart/aeronautes/internal/jobs"
	transfercopy "github.com/csmart/aeronautes/internal/transfer/copy"
	"github.com/csmart/aeronautes/internal/transfer/local"
)

func uploadItemIdentity(req UploadRequestDTO, item local.UploadItem) jobs.ItemIdentity {
	return jobs.ItemIdentity{Key: item.Name,
		Source: jobs.ItemLocation{Kind: jobs.ItemLocal, LocalPath: item.Path},
		Destination: jobs.ItemLocation{Kind: jobs.ItemSwift, BookmarkID: req.BookmarkID,
			Container: req.Container, Object: item.Name}}
}

// uploadItemRecorder is the durable record of one upload run: each file
// admitted before any request for it, and what the engine or the store
// answered for it (ADR 0122 decision 7).
func (s *TransferService) uploadItemRecorder(ctx context.Context, jobID string, req UploadRequestDTO) *local.UploadRecorder {
	return &local.UploadRecorder{
		Admit: func(admission local.UploadAdmission) error {
			intent := jobs.ItemIntent{ItemIdentity: uploadItemIdentity(req, admission.Item),
				ListedBytes: admission.Item.Size, Planned: true, Admitted: true}
			if err := intent.Validate(); err != nil {
				return err
			}
			return s.jobs.AdmitItems(ctx, jobID, []jobs.ItemIntent{intent})
		},
		Execute: func(item local.UploadItem, observation transfercopy.Observation) error {
			if err := uploadItemIdentity(req, item).Validate(); err != nil {
				return err
			}
			return s.jobs.RecordExecution(ctx, jobID, item.Name, copyItemObservation(observation))
		},
		Verify: func(item local.UploadItem, observation transfercopy.Observation) error {
			identity := uploadItemIdentity(req, item)
			if err := identity.Validate(); err != nil {
				return err
			}
			return s.jobs.RecordComparison(ctx, jobID, identity, copyItemObservation(observation))
		},
		Pause: func(live context.Context) error {
			return s.runner.checkpoint(live, jobID, req.BookmarkID)
		},
	}
}
