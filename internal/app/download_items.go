package app

import (
	"context"
	"errors"

	"github.com/csmart/aeronautes/internal/jobs"
	transfercopy "github.com/csmart/aeronautes/internal/transfer/copy"
	"github.com/csmart/aeronautes/internal/transfer/local"
)

func downloadItemIdentity(req DownloadRequestDTO, root string, item local.DownloadItem) (jobs.ItemIdentity, error) {
	path, err := item.LocalPath(root, req.Prefix)
	if err != nil {
		return jobs.ItemIdentity{}, errors.Join(jobs.ErrInvalidItemRecord, err)
	}
	return jobs.ItemIdentity{Key: item.Name,
		Source: jobs.ItemLocation{Kind: jobs.ItemSwift, BookmarkID: req.BookmarkID,
			Container: req.Container, Object: item.Name},
		Destination: jobs.ItemLocation{Kind: jobs.ItemLocal, LocalPath: path}}, nil
}

func (s *TransferService) downloadItemRecorder(ctx context.Context, jobID string, req DownloadRequestDTO, root string) *local.DownloadRecorder {
	return &local.DownloadRecorder{
		Pause: func(live context.Context) error {
			return s.runner.checkpoint(live, jobID, req.BookmarkID)
		},
		Admit: func(admission local.DownloadAdmission) error {
			identity, err := downloadItemIdentity(req, root, admission.Item)
			if err != nil {
				return err
			}
			intent := jobs.ItemIntent{ItemIdentity: identity,
				ListedBytes: admission.Item.Size, Planned: true, Admitted: true}
			if err := intent.Validate(); err != nil {
				return err
			}
			return s.jobs.AdmitItems(ctx, jobID, []jobs.ItemIntent{intent})
		},
		Execute: func(item local.DownloadItem, observation transfercopy.Observation) error {
			identity, err := downloadItemIdentity(req, root, item)
			if err != nil {
				return err
			}
			if err := identity.Validate(); err != nil {
				return err
			}
			return s.jobs.RecordExecution(ctx, jobID, item.Name, copyItemObservation(observation))
		},
		Verify: func(item local.DownloadItem, observation transfercopy.Observation) error {
			identity, err := downloadItemIdentity(req, root, item)
			if err != nil {
				return err
			}
			if err := identity.Validate(); err != nil {
				return err
			}
			return s.jobs.RecordComparison(ctx, jobID, identity, copyItemObservation(observation))
		},
	}
}
