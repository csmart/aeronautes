package app

import (
	"context"
	"fmt"

	"github.com/csmart/aeronautes/internal/audit"
	"github.com/csmart/aeronautes/internal/jobs"
	"github.com/csmart/aeronautes/internal/swiftcontrol"
	transfercopy "github.com/csmart/aeronautes/internal/transfer/copy"
	"github.com/csmart/aeronautes/internal/transfer/plan"
)

// sameCollisionSet reports whether the collision names enumerated at execution
// are exactly the set the operator confirmed replacing. A name in one and not
// the other — a collision that appeared, or a confirmed one that is no longer
// live or no longer has a source — means the plan changed since the
// confirmation, which ADR 0087 decision 4 refuses as a whole rather than a
// member at a time.
func sameCollisionSet(confirmed map[string]jobs.ItemBaseline, executable map[string]struct{}) bool {
	if len(confirmed) != len(executable) {
		return false
	}
	for name := range confirmed {
		if _, ok := executable[name]; !ok {
			return false
		}
	}
	return true
}

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

// copyItemRecorder wires the executor's acknowledgements to the job ledger.
// acknowledged holds the baselines captured when the operator confirmed
// replacing live destinations, keyed by name; it is empty for an
// unacknowledged copy, which then replaces nothing. authorised is the
// destination's versioning as observed for the question, which the fixed
// point compares against a fresh read (ADR 0102 decision 2).
func (s *TransferService) copyItemRecorder(ctx context.Context, jobID string, req CopyRequestDTO,
	acknowledged map[string]jobs.ItemBaseline, authorised swiftcontrol.ContainerVersioning) *transfercopy.Recorder {
	return &transfercopy.Recorder{
		VersioningUnchanged: func(live context.Context, key string) (string, error) {
			// The read on the route's live context, the record on this
			// recorder's cancellation-free one, like every record here.
			return s.versioningUnchanged(live, ctx, req.DestBookmarkID, req.DestContainer, authorised,
				"transfer.copy.refused", req.DestContainer+"/"+req.DestPrefix+key)
		},
		Admit: func(admission transfercopy.Admission) error {
			item := admission.Item
			// A copied item must be admitted (the ledger refuses a copied
			// outcome for an unadmitted item), so an acknowledged overwrite —
			// which may copy — is admitted alongside New and Unknown. A
			// Changed item that is not acknowledged stays unadmitted and only
			// ever records a refusal, which needs no admission.
			_, isAcknowledged := acknowledged[item.Name]
			intent := jobs.ItemIntent{ItemIdentity: copyItemIdentity(req, item.Name), ListedBytes: item.SourceBytes,
				Planned:  item.Verdict == plan.VerdictNew || item.Verdict == plan.VerdictUnknown || item.Verdict == plan.VerdictChanged,
				Admitted: item.Verdict == plan.VerdictNew || item.Verdict == plan.VerdictUnknown || isAcknowledged}
			if admission.StagingObject != "" {
				intent.Staging = &jobs.ItemLocation{Kind: jobs.ItemSwift, BookmarkID: req.DestBookmarkID,
					Container: req.DestContainer, Object: admission.StagingObject}
			}
			if err := intent.Validate(); err != nil {
				return err
			}
			if err := s.jobs.AdmitItems(ctx, jobID, []jobs.ItemIntent{intent}); err != nil {
				return err
			}
			// Record the acknowledged baseline now the item row exists; the
			// baseline references that row. A baseline that cannot be saved
			// stops the item rather than letting it replace against nothing.
			baseline, ok := acknowledged[item.Name]
			if !ok {
				return nil
			}
			return s.jobs.RecordAcknowledgedBaseline(ctx, jobID, copyItemIdentity(req, item.Name), baseline)
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
		Acknowledged: func(key string) (transfercopy.ShownObject, bool, error) {
			// What the operator was shown when they accepted replacing this
			// name, for the comparison the route makes immediately before
			// publishing. A name whose acknowledgement captured no identity
			// reads as absent, which the route treats as a refusal.
			baseline, ok, err := s.jobs.ReadAcknowledgedBaseline(ctx, jobID, key)
			if err != nil || !ok {
				return transfercopy.ShownObject{}, false, err
			}
			return transfercopy.ShownObject{Class: baseline.Class, Length: baseline.Length, ETag: baseline.ETag}, true, nil
		},
		Replacing: func(key string, observed transfercopy.ObservedPredecessor) error {
			// What was live immediately before the replacement, kept WITH THE
			// item (ADR 0081 decision 7, ADR 0087 decision 6): the publication
			// destroys the ability to read it again, and the audit line below is
			// pruned on its own age and event-cap policy, so the durable record
			// is the item's. It is "observed" rather than "replaced" because this
			// contract admits a racing writer, so the object actually replaced
			// may not be the one this reading saw; copy retains its segments, so
			// the record names no fragments to reclaim. A record that cannot be
			// saved stops the replacement rather than letting it land unrecorded.
			identity := copyItemIdentity(req, key)
			record := copyItemPredecessor(observed)
			if err := identity.Validate(); err != nil {
				return err
			}
			if err := record.Validate(); err != nil {
				return err
			}
			if err := s.jobs.RecordPredecessor(ctx, jobID, identity, record); err != nil {
				return err
			}
			return s.runner.audit.Append(ctx, audit.Event{
				BookmarkID: req.DestBookmarkID,
				Operation:  "transfer.copy.replace",
				Resource:   req.DestContainer + "/" + req.DestPrefix + key,
				Summary:    copyObservedPredecessorSummary(observed),
				Result:     audit.ResultObserved,
			})
		},
		Refused: func(key string, acknowledged, found transfercopy.ShownObject) error {
			// Both identities, durably, for this one name. The acknowledged
			// baseline already rides with the item; the found identity is kept
			// with it too, as the observation seen at the name when the
			// comparison refused — an observation record, not reclamation
			// authority, and it does not change the comparison tuple. Audit
			// alone is pruned on its own policy, so a refusal recorded only
			// there loses which object blocked the replacement. It carries no
			// fragments and no last-modified: the comparison compares class,
			// length and entity tag, which is all a ShownObject holds.
			//
			// The name is kept in the event's resource field and out of the
			// summary, which the summary filter does not inspect, so a valid
			// object cannot make its own refusal unrecordable through a crafted
			// name.
			identity := copyItemIdentity(req, key)
			record := jobs.ItemPredecessor{Present: true, ETag: found.ETag,
				Length: found.Length, Class: found.Class}
			if err := identity.Validate(); err != nil {
				return err
			}
			if err := record.Validate(); err != nil {
				return err
			}
			if err := s.jobs.RecordPredecessor(ctx, jobID, identity, record); err != nil {
				return err
			}
			return s.runner.audit.Append(ctx, audit.Event{
				BookmarkID: req.DestBookmarkID,
				Operation:  "transfer.copy.refused",
				Resource:   req.DestContainer + "/" + req.DestPrefix + key,
				Summary: fmt.Sprintf("replacement refused at the last observation: the name no longer holds what was acknowledged; it was a %s of %d bytes with entity tag %s, and it is now a %s of %d bytes with entity tag %s",
					acknowledged.Class, acknowledged.Length, acknowledged.ETag, found.Class, found.Length, found.ETag),
				Result: audit.ResultConflict,
			})
		},
	}
}

// copyItemPredecessor is the durable form of what the commit observed at a
// name. Copy retains a replaced manifest's segments (ADR 0087 decision 6), so
// unlike the upload form it names no fragments; it keeps the entity tag, length
// and last-modified ADR 0081 decision 7 requires. An absent observation is kept
// as the absence the race admits rather than an empty identity.
func copyItemPredecessor(observed transfercopy.ObservedPredecessor) jobs.ItemPredecessor {
	if !observed.Present {
		return jobs.ItemPredecessor{}
	}
	return jobs.ItemPredecessor{Present: true, ETag: observed.ETag, Length: observed.Length,
		LastModified: observed.LastModified, Class: observed.Class}
}

// copyObservedPredecessorSummary states what was live before a replacement,
// without the name, which travels in the audit event's resource field.
func copyObservedPredecessorSummary(observed transfercopy.ObservedPredecessor) string {
	if !observed.Present {
		return "observed nothing live at the name immediately before replacing it"
	}
	return fmt.Sprintf("observed a %s of %d bytes with entity tag %s immediately before replacing it",
		observed.Class, observed.Length, observed.ETag)
}
