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
	if !observed.Present {
		return "observed nothing at this name immediately before publishing; it was live at preflight"
	}
	class := observed.Class
	if class == "" {
		class = "object"
	}
	return fmt.Sprintf(
		"observed a %s of %d bytes, entity tag %s, last modified %s, immediately before publishing over this name; "+
			"a racing writer means the object replaced may not be that one%s",
		class, observed.Length, observed.ETag, observed.LastModified.UTC().Format(time.RFC3339),
		fragmentSummary(observed.Fragments))
}

// fragmentSummary states what a replaced manifest named and what the
// inventory cannot account for, so the space is visible to the operator and
// its limits are visible with it. Nothing here was deleted: clearing it
// stays the explicit, force-gated cleanup's work (ADR 0081 decision 5).
func fragmentSummary(inventory local.FragmentInventory) string {
	if inventory.Unread != "" {
		return "; its fragments were not inventoried because " + inventory.Unread + ", and none was removed"
	}
	if inventory.Counted == 0 && inventory.Inline == 0 {
		return ""
	}
	out := fmt.Sprintf("; it named %d segment", inventory.Counted)
	if inventory.Counted != 1 {
		out += "s"
	}
	if inventory.Prefix != "" {
		out += " listed under " + inventory.Prefix + ", which a listing may undercount"
	} else if !inventory.Complete {
		out += ", and an entry shape this build does not recognise means the list is not the whole of it"
	}
	if inventory.Truncated() {
		out += fmt.Sprintf(", of which %d are recorded by name and the rest by count alone", len(inventory.Names))
	}
	// A fixed order, because a map's iteration is not one and this text is
	// durable.
	for _, kind := range []struct {
		count int
		what  string
	}{{inventory.Inline, "inline"}, {inventory.Ranged, "ranged"}, {inventory.Nested, "nested"}} {
		if kind.count > 0 {
			out += fmt.Sprintf(", %d %s", kind.count, kind.what)
		}
	}
	// Retention is claimed only for the remote segments. Inline data lives
	// inside the manifest that was replaced, so it has no remote part to
	// retain and went with the manifest; saying otherwise would report
	// bytes as surviving that did not.
	out += "; every segment it named was retained and none was removed"
	if inventory.Inline > 0 {
		out += ", and its inline data went with the manifest because it had no remote part"
	}
	return out
}

// itemPredecessor is the durable form of one observation. The segment names
// go with it, because once the manifest is replaced they cannot be read
// again and a count cannot tell an explicit cleanup which objects it would
// be clearing (ADR 0081 decision 5).
func itemPredecessor(observed local.ObservedPredecessor) jobs.ItemPredecessor {
	if !observed.Present {
		return jobs.ItemPredecessor{}
	}
	record := jobs.ItemPredecessor{Present: true, ETag: observed.ETag, Length: observed.Length,
		LastModified: observed.LastModified, Class: observed.Class,
		Prefix: observed.Fragments.Prefix, Counted: observed.Fragments.Counted,
		Inline: observed.Fragments.Inline, Ranged: observed.Fragments.Ranged,
		Nested: observed.Fragments.Nested, Exact: observed.Fragments.Complete,
		Unread: observed.Fragments.Unread}
	for _, fragment := range observed.Fragments.Names {
		record.Fragments = append(record.Fragments, jobs.ItemFragment{
			Container: fragment.Container, Object: fragment.Name})
	}
	return record
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
			// The record is durable before the publication it precedes, and
			// it names the segments: once the manifest is replaced they
			// cannot be read again. A record that cannot be saved stops the
			// replacement rather than letting it land unrecorded.
			identity := uploadItemIdentity(req, item)
			record := itemPredecessor(observed)
			// Validated first, so a record this store would refuse is refused
			// here rather than latching the store on the way to the same
			// answer, and the replacement stops either way.
			if err := identity.Validate(); err != nil {
				return err
			}
			if err := record.Validate(); err != nil {
				return err
			}
			if err := s.jobs.RecordPredecessor(ctx, jobID, identity, record); err != nil {
				return err
			}
			// The trail then says the same thing in one sentence. It says
			// "observed" rather than "replaced" on purpose: this contract
			// admits a racing writer, so the object actually replaced may
			// not be this one (ADR 0081 decision 7).
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
