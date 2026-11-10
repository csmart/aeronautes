package app

import (
	"context"
	"fmt"
	"strings"
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
// its limits are visible with it. Recording it deletes nothing: what a
// verified replacement then reclaims is a separate event with its own
// per-object outcomes (ADR 0081 decision 5).
func fragmentSummary(inventory local.FragmentInventory) string {
	if inventory.Unread != "" {
		// A list this build could not read whole is not evidence of what to
		// remove, so this predecessor's segments are not reclaimable at all.
		return "; its fragments were not inventoried because " + inventory.Unread +
			", so none of them can be reclaimed"
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
	// Stated as what this record is, not as an outcome: it is written
	// before the publication, so it cannot know what the publication does.
	// Whether these segments are then reclaimed is that operation's own
	// record, with its own per-object result.
	out += "; recording them removes none of them"
	if inventory.Inline > 0 {
		out += ", and its inline data has no remote part, so a replacement would take that with the manifest"
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
			Container: fragment.Container, Object: fragment.Name,
			Shape: jobs.FragmentShape(fragment.Shape)})
	}
	return record
}

// retainedShapes says which recorded entries reclamation will never touch and
// why, so leaving them alone is an outcome in the record rather than an
// absence (ADR 0081 decision 5; db:756 finding 4). Empty when every entry is
// a private whole object.
func retainedShapes(record jobs.ItemPredecessor) string {
	var parts []string
	for _, kind := range []struct {
		count int
		what  string
	}{{record.Inline, "inline"}, {record.Ranged, "ranged"}, {record.Nested, "nested"}} {
		if kind.count > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", kind.count, kind.what))
		}
	}
	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, ", ") +
		" of the entries it named are shapes this operation never removes, so they were left alone"
}

// reclaimSummary states what was removed and what was not, naming the
// reason in either case. A partial success is reported as exactly that.
func reclaimSummary(outcome local.ReclaimOutcome) string {
	if outcome.Declined != "" {
		out := "removed none of the replaced object's segments: " + outcome.Declined
		if outcome.Retained != "" {
			out += "; " + outcome.Retained
		}
		return out
	}
	// A removal that could not name everything is not a complete one, so
	// the count is stated with what was left rather than on its own.
	// "gone or already absent" rather than "removed": a bulk delete reports
	// a name it did not find as not found, and the space is not held by it
	// either way, but this operation did not remove it and the record does
	// not say it did.
	out := fmt.Sprintf("the store reported %d segment object%s the replaced manifest named as gone or already absent",
		len(outcome.Removed), plural(len(outcome.Removed), "", "s"))
	// A name the store refused is a known non-removal. A name in a reply
	// that could not be read is neither removed nor known to be kept, and
	// saying "not removed" about it would claim knowledge nobody has
	// (db:756 finding 3).
	var refused, unknown []local.ReclaimFailure
	for _, failure := range outcome.Failed {
		if failure.Resolved {
			refused = append(refused, failure)
			continue
		}
		unknown = append(unknown, failure)
	}
	if len(refused) > 0 {
		out += fmt.Sprintf("; the store refused %d and kept %s", len(refused), plural(len(refused), "it", "them"))
		for _, failure := range refused {
			out += fmt.Sprintf(", %s (%s)", trailName(failure.Fragment.Container+"/"+failure.Fragment.Name), trailText(failure.Detail))
		}
	}
	if len(unknown) > 0 {
		out += fmt.Sprintf("; what the store did with %d further name%s is unknown",
			len(unknown), plural(len(unknown), "", "s"))
		for _, failure := range unknown {
			out += ", " + trailName(failure.Fragment.Container+"/"+failure.Fragment.Name)
		}
	}
	if outcome.Untrusted != "" {
		out += "; the store's reply could not be read as a disposition: " + trailText(outcome.Untrusted)
	}
	if outcome.Retained != "" {
		out += "; " + outcome.Retained
	}
	if outcome.LeftBehind != "" {
		out += "; " + outcome.LeftBehind
	}
	return out
}

// trailName writes a name into a record's sentence in a form the audit store
// keeps and a reader can turn back into the exact name. The store withholds a
// whole sentence that holds a word such as "password", and this record is the
// only place that says which segments were refused or left unknown after a
// deletion (ADR 0081 decision 5), so a segment named for a credential must
// still be nameable. A literal percent sign is always written as %25, and a
// name the store would refuse is written with every byte percent-encoded.
// Both spellings decode by the one ordinary rule, so two different names
// never share one, and the second is hex digits and percent signs only, which
// can spell no word the store refuses whatever the name's characters are.
func trailName(name string) string {
	escaped := strings.ReplaceAll(name, "%", "%25")
	if audit.SummaryRefusal(escaped) == "" {
		return escaped
	}
	var encoded strings.Builder
	for i := 0; i < len(name); i++ {
		fmt.Fprintf(&encoded, "%%%02X", name[i])
	}
	return encoded.String()
}

// trailText is trailName's counterpart for text that quotes the store: a
// reason, a reply or a confirmation sentence is not a name anyone needs back
// exactly, and it may hold what the filter exists to keep out, so one the
// audit store would refuse is withheld alone and the rest of the record
// stands.
func trailText(text string) string {
	if audit.SummaryRefusal(text) != "" {
		return "text this trail does not store"
	}
	return text
}

// reclaimResult is the audit result value. A reclamation that removed
// everything it meant to is applied; anything else is not, because
// reporting an incomplete removal as applied is the misreport this record
// exists to prevent. The vocabulary has no "partial", so this follows the
// precedent ADR 0025 and ADR 0027 set for partial outcomes and uses the
// coarse verify_mismatch value, which those ADRs already say proves
// neither a mismatch nor a successful write.
func reclaimResult(outcome local.ReclaimOutcome) audit.Result {
	unresolved := outcome.Untrusted != ""
	refused := false
	for _, failure := range outcome.Failed {
		if failure.Resolved {
			refused = true
			continue
		}
		unresolved = true
	}
	switch {
	case outcome.Declined != "":
		// Nothing was asked of the store: this event records what was
		// read and the decision taken, which is what observed means.
		return audit.ResultObserved
	case unresolved:
		// The request left the process and its reply could not be read,
		// so the effect is unknown rather than failed (ADR 0058).
		return audit.ResultUnverified
	case refused, outcome.LeftBehind != "":
		return audit.ResultVerifyMismatch
	}
	return audit.ResultApplied
}

// uploadItemRecorder is the durable record of one upload run.
//
// answered is what the answered confirmation said would happen to a replaced
// large object's segments. It is a CEILING: the policy below may still
// decline for a reason that appears later, but nothing may turn a question
// that promised retention into a removal (db:756 finding 1).
func (s *TransferService) uploadItemRecorder(ctx context.Context, jobID string, req UploadRequestDTO,
	answered answeredFragments) *local.UploadRecorder {
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
			if err := s.jobs.AdmitItems(ctx, jobID, []jobs.ItemIntent{intent}); err != nil {
				return err
			}
			// What the operator was shown at this name, kept for the comparison
			// ADR 0085 makes at the fixed point. It is recorded after the item
			// exists, because the baseline references that row, and a baseline
			// that cannot be saved stops the item rather than letting it publish
			// against nothing — the same rule the predecessor record follows.
			//
			// Only for a name being replaced, and only when the acknowledgement
			// identified it. An acknowledgement that identified nothing leaves no
			// baseline, which that decision makes a refusal to overwrite; this
			// slice records, and the comparison slice refuses.
			shown, identified := answered.Shown[admission.Item.Name]
			if !admission.Item.Replaces || !identified {
				return nil
			}
			return s.jobs.RecordAcknowledgedBaseline(ctx, jobID, uploadItemIdentity(req, admission.Item),
				jobs.ItemBaseline{Class: shown.Class, Length: shown.Length, ETag: shown.ETag})
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
		Acknowledged: func(item local.UploadItem) (local.ShownObject, bool, error) {
			// What the operator was shown when they accepted replacing this
			// name, for the comparison ADR 0085 makes immediately before the
			// publication. An item whose acknowledgement captured no identity
			// reads as absent here, and that decision makes the absence a
			// refusal to overwrite rather than a licence to proceed — which
			// the route applies, because only it knows when the fixed point
			// has been reached.
			baseline, ok, err := s.jobs.ReadAcknowledgedBaseline(ctx, jobID, item.Name)
			if err != nil || !ok {
				return local.ShownObject{}, false, err
			}
			return local.ShownObject{Class: baseline.Class, Length: baseline.Length, ETag: baseline.ETag}, true, nil
		},
		Refused: func(item local.UploadItem, acknowledged, found local.ShownObject) error {
			// Both identities, durably, for this one name. An operator told
			// only that a replacement was refused learns nothing; one shown
			// that the name now holds a different size with a different digest
			// learns that something else is writing to their account.
			//
			// The name is deliberately absent from the summary and present in
			// the event's own resource field, which the summary filter does not
			// inspect: a valid object called password-manifest must not be able
			// to make its own refusal unrecordable.
			return s.runner.audit.Append(ctx, audit.Event{
				BookmarkID: req.BookmarkID,
				Operation:  "object.upload.refused",
				Resource:   req.Container + "/" + item.Name,
				Summary: fmt.Sprintf("replacement refused at the last observation: the name no longer holds what was acknowledged; it was a %s of %d bytes with entity tag %s, and it is now a %s of %d bytes with entity tag %s",
					acknowledged.Class, acknowledged.Length, acknowledged.ETag, found.Class, found.Length, found.ETag),
				Result: audit.ResultConflict,
			})
		},
		Pause: func(live context.Context) error {
			return s.runner.checkpoint(live, jobID, req.BookmarkID)
		},
		VersioningUnchanged: func(live context.Context, item local.UploadItem) (string, error) {
			// The read on the route's live context, the record on this
			// recorder's cancellation-free one, like every record here.
			return s.versioningUnchanged(live, ctx, req.BookmarkID, req.Container, answered.Versioning,
				"object.upload.refused", req.Container+"/"+item.Name)
		},
		Reclaiming: func(item local.UploadItem) (local.ReclaimPlan, error) {
			return s.reclaimPlan(ctx, req, jobID, item, answered)
		},
		Reclaimed: func(item local.UploadItem, outcome local.ReclaimOutcome) error {
			return s.runner.audit.Append(ctx, audit.Event{
				BookmarkID: req.BookmarkID,
				Operation:  "object.upload.reclaim",
				Resource:   req.Container + "/" + item.Name,
				Summary:    reclaimSummary(outcome),
				Result:     reclaimResult(outcome),
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

// reclaimPlan decides what the upload may remove of a replaced predecessor's
// fragments, and says why when it removes nothing. It is decided here, not
// in the route, because it depends on the operator's setting, on what the
// container says about versions at this moment, and on the record taken
// before the publication (ADR 0081 decision 5).
func (s *TransferService) reclaimPlan(ctx context.Context, req UploadRequestDTO, jobID string, item local.UploadItem, answered answeredFragments) (local.ReclaimPlan, error) {
	// The answer first, because it is the only input that cannot be
	// re-read: the operator is not here to be asked again.
	if !answered.Removal {
		declined := "the confirmation that was answered did not say these segments would be removed"
		if answered.Said != "" {
			// The sentence the operator read names an object and quotes the
			// store, and it is quoted into the trail here. One the trail
			// would refuse is withheld alone, so the decision beside it and
			// the statement that nothing was removed are still recorded.
			declined += "; it said: " + trailText(answered.Said)
		}
		return local.ReclaimPlan{Declined: declined}, nil
	}
	reclaiming, err := s.reclamationSetting(ctx)
	if err != nil {
		return local.ReclaimPlan{Declined: "the reclamation setting could not be read, so nothing was removed"}, nil
	}
	if !reclaiming {
		return local.ReclaimPlan{Declined: "reclaiming replaced fragments is turned off"}, nil
	}
	// Read again rather than reusing what the confirmation said: the
	// question stated what was true when the operator was asked, and
	// what matters now is what is true now. A mode this build cannot
	// describe declines rather than proceeding.
	versioning, versioningErr := s.readVersioning(ctx, req.BookmarkID, req.Container)
	if versioningErr != nil {
		return local.ReclaimPlan{Declined: "the container's versioning could not be read, so nothing was removed"}, nil
	}
	if keepsVersions(versioning) {
		return local.ReclaimPlan{Declined: "the container keeps versions, so those segments belong to a retained version"}, nil
	}
	// The identities recorded BEFORE the publication, read back from
	// the durable record. Nothing is derived from a prefix at the
	// time of deletion, which is the whole of rule three.
	record, ok, err := s.jobs.ReadPredecessor(ctx, jobID, item.Name)
	if err != nil {
		return local.ReclaimPlan{}, err
	}
	if !ok {
		return local.ReclaimPlan{Declined: "no record of what was there, so nothing was removed"}, nil
	}
	if record.Unread != "" {
		return local.ReclaimPlan{Declined: "the predecessor's fragments were never inventoried, so nothing was removed"}, nil
	}
	var plan local.ReclaimPlan
	plan.Retained = retainedShapes(record)
	// A listed prefix's inventory holds a prefix of what it read
	// (local.MaxListedFragments), so what it does not name is left
	// behind and the trail has to say so rather than reporting a
	// partial removal as a whole one. The prefix stays listable, so
	// what was left can still be found.
	if record.Counted > record.Named {
		plan.LeftBehind = fmt.Sprintf("the predecessor named %d segments and this build recorded %d of them, so the rest were left where they are",
			record.Counted, record.Named)
	}
	for _, fragment := range record.Fragments {
		// Only a private whole object. A ranged entry references a
		// containing object and a nested entry names another
		// manifest, so neither is this operation's to remove.
		if fragment.Shape != jobs.FragmentWhole {
			continue
		}
		plan.Fragments = append(plan.Fragments, local.Fragment{
			Container: fragment.Container, Name: fragment.Object, Shape: local.FragmentWhole})
	}
	if len(plan.Fragments) == 0 {
		// Nothing this operation may remove, which is a decision
		// taken and not a removal of none (db:756 finding 4).
		plan.Declined = "nothing the predecessor's record named is a private whole object"
	}
	return plan, nil
}
