package app

import (
	"context"
	"fmt"
	"time"

	"github.com/csmart/aeronautes/internal/audit"
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

// observedPredecessorSummary states what was there in one sentence, or
// that nothing was, which is what a name that went away between the
// preflight and the publication looks like.
func observedPredecessorSummary(observed local.ObservedPredecessor) string {
	if observed.ETag == "" && observed.Length == 0 && observed.LastModified.IsZero() {
		return "observed nothing at this name immediately before publishing; it was live at preflight"
	}
	class := observed.Class
	if class == "" {
		class = "object"
	}
	return fmt.Sprintf(
		"observed a %s of %d bytes, entity tag %s, last modified %s, immediately before publishing over this name; "+
			"a racing writer means the object replaced may not be that one",
		class, observed.Length, observed.ETag, observed.LastModified.UTC().Format(time.RFC3339))
}

func (s *TransferService) uploadItemRecorder(ctx context.Context, jobID string, req UploadRequestDTO) *local.UploadRecorder {
	return &local.UploadRecorder{
		Admit: func(admission local.UploadAdmission) error {
			intent := jobs.ItemIntent{ItemIdentity: uploadItemIdentity(req, admission.Item),
				ListedBytes: admission.Item.Size, Planned: true, Admitted: true}
			if admission.StagingObject != "" {
				intent.Staging = &jobs.ItemLocation{Kind: jobs.ItemSwift, BookmarkID: req.BookmarkID,
					Container: req.Container, Object: admission.StagingObject}
			}
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
		Replacing: func(item local.UploadItem, observed local.ObservedPredecessor) error {
			// The durable trail names what the commit saw at the name a
			// moment before publishing over it. It says "observed" rather
			// than "replaced" on purpose: this contract admits a racing
			// writer, so the object actually replaced may not be this one
			// (ADR 0081 decision 7).
			return s.runner.audit.Append(ctx, audit.Event{
				BookmarkID: req.BookmarkID,
				Operation:  "object.upload.replace",
				Resource:   req.Container + "/" + item.Name,
				Summary:    observedPredecessorSummary(observed),
				Result:     audit.ResultObserved,
			})
		},
		Verify: func(item local.UploadItem, observation transfercopy.Observation) error {
			identity := uploadItemIdentity(req, item)
			if err := identity.Validate(); err != nil {
				return err
			}
			return s.jobs.RecordComparison(ctx, jobID, identity, copyItemObservation(observation))
		},
	}
}
