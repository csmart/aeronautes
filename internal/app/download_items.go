package app

import (
	"context"
	"errors"
	"path/filepath"
	"strings"

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
			staging := admission.StagingPath
			if admission.Item.Dir {
				if staging != "" {
					return jobs.ErrInvalidItemRecord
				}
			} else if !filepath.IsAbs(staging) || filepath.Clean(staging) != staging {
				return jobs.ErrInvalidItemRecord
			} else if admission.Item.Route == "native" {
				if filepath.Dir(staging) != filepath.Dir(identity.Destination.LocalPath) || !strings.HasPrefix(filepath.Base(staging), ".aeronautes-part-") {
					return jobs.ErrInvalidItemRecord
				}
			} else if staging != filepath.Join(root, ".aeronautes-stage-"+jobID, admission.Item.RelPath) {
				return jobs.ErrInvalidItemRecord
			}
			intent := jobs.ItemIntent{ItemIdentity: identity,
				ListedBytes: admission.Item.Size, Planned: true, Admitted: true,
				// Every placed file carries a final-publication fact, on the
				// native route (ADR 0061) and the engine route (ADR 0070).
				PublicationRequired: !admission.Item.Dir}
			if admission.StagingPath != "" {
				intent.Staging = &jobs.ItemLocation{Kind: jobs.ItemLocal, LocalPath: admission.StagingPath}
			}
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
		Publication: func(item local.DownloadItem, observation transfercopy.Observation) error {
			identity, err := downloadItemIdentity(req, root, item)
			if err != nil {
				return err
			}
			if err := identity.Validate(); err != nil {
				return err
			}
			return s.jobs.RecordPublication(ctx, jobID, identity, copyItemObservation(observation))
		},
	}
}
