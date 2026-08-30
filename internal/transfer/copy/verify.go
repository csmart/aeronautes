package copy

import (
	"context"
	"errors"
	"fmt"
	"iter"
	"strings"

	"github.com/csmart/aeronautes/internal/swiftcontrol"
	"github.com/csmart/aeronautes/internal/transfer/plan"
	"github.com/csmart/aeronautes/internal/transfer/rclone"
)

// listingStream is plan.Copy's input shape.
type listingStream = iter.Seq2[swiftcontrol.ListingEntry, error]

// VerifyReport is the §46 Verifying-phase outcome.
type VerifyReport struct {
	Verified int64
	// Failures are names the fresh listing-diff could not prove copied:
	// still-new (a silent drop), still-changed, or unprovable equality.
	Failures []Failure
}

// Verify is the independent completeness check (§28.4: never the
// engine's exit code): a FRESH merge-join over both namespaces. Every
// name must now prove Same — listing-grade, or by authoritative HEADs
// plus the destination fingerprint for conservative rows. Destination-
// only names are not this job's business (Copy never deletes).
func (e *Executor) Verify(ctx context.Context, source, dest listingStream) (*VerifyReport, error) {
	report := &VerifyReport{}
	_, err := plan.Copy(ctx, source, dest, e.cfg.SrcPrefix, e.cfg.DstPrefix, func(item plan.Item) error {
		switch item.Verdict {
		case plan.VerdictSame:
			report.Verified++
		case plan.VerdictDestOnly:
			// Staging litter under this job's own prefix is expected here
			// when verification runs before cleanup; everything else is
			// simply outside the copy scope.
		case plan.VerdictUnknown:
			src, err := e.cfg.Source.ObjectDetail(ctx, e.cfg.SrcContainer, e.cfg.SrcPrefix+item.Name)
			if err != nil {
				report.fail(item.Name, "verify-head-source", err)
				return nil
			}
			dst, err := e.cfg.Dest.ObjectDetail(ctx, e.cfg.DstContainer, e.cfg.DstPrefix+item.Name)
			if err != nil {
				report.fail(item.Name, "verify-head-dest", err)
				return nil
			}
			if provenEqual(src, dst) {
				report.Verified++
			} else {
				report.fail(item.Name, "verify", errors.New("equality unprovable after copy"))
			}
		case plan.VerdictNew:
			report.fail(item.Name, "verify", errors.New("still missing at the destination"))
		case plan.VerdictChanged:
			report.fail(item.Name, "verify", errors.New("destination still differs from source"))
		}
		return nil
	})
	if err != nil {
		return report, err
	}
	return report, nil
}

func (r *VerifyReport) fail(name, stage string, err error) {
	if len(r.Failures) < maxFailures {
		r.Failures = append(r.Failures, Failure{Name: name, Stage: stage, Err: err.Error()})
	}
}

// ReconcileReport says what the post-job sweep did (§26.4).
type ReconcileReport struct {
	SweptStaged   int
	SweptSegments int
	// KeptPrefixes were captured but left alone: committed, or their
	// derived object is a live DLO referencing them, or ownership could
	// not be re-proven — a recoverable orphan beats a wrong delete.
	KeptPrefixes []string
	Notes        []string
}

// Reconcile sweeps ONLY what this job itself created (§26.4): leftover
// staged objects, and segments under captured generated prefixes whose
// upload never became a live manifest. It never touches a prefix that a
// committed or live manifest references, and when in doubt it keeps the
// orphan and says so.
func (e *Executor) Reconcile(ctx context.Context, report *Report) (*ReconcileReport, error) {
	out := &ReconcileReport{}
	// Staged residue first: unique job-scoped names — always ours.
	for _, staged := range append([]string(nil), report.StagedResidue...) {
		if err := e.cfg.Dest.DeleteObject(ctx, e.cfg.DstContainer, staged); err != nil {
			if !swiftcontrol.IsNotFound(err) {
				out.Notes = append(out.Notes, fmt.Sprintf("staged %s: %v", staged, err))
				continue
			}
		}
		out.SweptStaged++
	}
	rest, err := SweepLitter(ctx, e.cfg.Dest, SweepInput{
		DstContainer: e.cfg.DstContainer, StagingPrefix: e.stage,
		Captured: report.CapturedPrefixes, Committed: report.CommittedPrefixes,
	})
	out.SweptStaged += rest.SweptStaged
	out.SweptSegments += rest.SweptSegments
	out.KeptPrefixes = append(out.KeptPrefixes, rest.KeptPrefixes...)
	out.Notes = append(out.Notes, rest.Notes...)
	return out, err
}

// SweepInput is what a sweep needs to decide safely: the persisted (or
// in-memory) recovery facts for ONE job.
type SweepInput struct {
	DstContainer  string
	StagingPrefix string // "" skips the staging half (upload jobs)
	Captured      []rclone.SegmentPrefix
	Committed     []rclone.SegmentPrefix
	// Committing maps a captured prefix to the LIVE "container/name" a
	// commit intended it for (§65.9 item 5): the sweep re-HEADs that
	// exact identity instead of deriving names.
	Committing map[rclone.SegmentPrefix]string
}

// SweepLitter is the reconciliation core, callable WITHOUT a live
// executor — the startup crash-recovery sweep replays it from persisted
// records. The rules: only job-created staging names and captured
// prefixes; a prefix a committed record, a committing record's live
// manifest, or a derived live DLO references is KEPT; any doubt —
// listing failure, HEAD failure, unparseable prefix, per-object delete
// failures — keeps the data and lands in Notes. Captured prefixes are
// decided BEFORE the staging namespace is touched, so the staged-
// manifest guard cannot be broken by our own earlier deletions (the
// review-wave C-01 sequence).
func SweepLitter(ctx context.Context, dst *swiftcontrol.Session, in SweepInput) (*ReconcileReport, error) {
	out := &ReconcileReport{}

	// EVERYTHING compares in the canonical (slash-terminated) form:
	// rows written by older builds or manifests read off the wire may
	// carry either shape (review C01-slash).
	committedSet := map[rclone.SegmentPrefix]bool{}
	for _, p := range in.Committed {
		committedSet[p.Canonical()] = true
	}
	committing := map[rclone.SegmentPrefix]string{}
	for p, live := range in.Committing {
		committing[p.Canonical()] = live
	}
	for _, rawCaptured := range in.Captured {
		capturedPrefix := rawCaptured.Canonical()
		ref := capturedPrefix.Container + "/" + capturedPrefix.Prefix
		if committedSet[capturedPrefix] {
			out.KeptPrefixes = append(out.KeptPrefixes, ref)
			continue
		}
		// A committing record binds this prefix to a live identity: the
		// live object's CURRENT manifest is the authority. Referencing →
		// the commit landed (the committed row was lost) — keep. Not
		// referencing → the PUT never landed or was superseded; fall
		// through to the remaining guards.
		if live, ok := committing[capturedPrefix]; ok {
			container, name, found := strings.Cut(live, "/")
			if !found {
				out.Notes = append(out.Notes, fmt.Sprintf("prefix %s: malformed committing identity %q — keeping", capturedPrefix.Prefix, live))
				out.KeptPrefixes = append(out.KeptPrefixes, ref)
				continue
			}
			detail, err := dst.ObjectDetail(ctx, container, name)
			if err != nil && !swiftcontrol.IsNotFound(err) {
				out.Notes = append(out.Notes, fmt.Sprintf("prefix %s: cannot re-check live %s: %v — keeping", capturedPrefix.Prefix, live, err))
				out.KeptPrefixes = append(out.KeptPrefixes, ref)
				continue
			}
			if err == nil && detail.Class == swiftcontrol.ClassDLO && strings.TrimSuffix(detail.Manifest, "/") == strings.TrimSuffix(ref, "/") {
				// The commit landed and only its committed record was
				// lost: kept, and NOT a blocking note — the litter is
				// resolved and the records may clear.
				out.KeptPrefixes = append(out.KeptPrefixes, ref)
				continue
			}
		}
		// Legacy guard: a completed engine object's live manifest may
		// reference the prefix (batch uploads without commit records).
		if object, ok := objectFromGeneratedPrefix(capturedPrefix.Prefix); ok && in.DstContainer != "" {
			live, err := dst.ObjectDetail(ctx, in.DstContainer, object)
			if err == nil && live.Class == swiftcontrol.ClassDLO && strings.TrimSuffix(live.Manifest, "/") == strings.TrimSuffix(ref, "/") {
				out.KeptPrefixes = append(out.KeptPrefixes, ref)
				continue
			}
			if err != nil && !swiftcontrol.IsNotFound(err) {
				out.Notes = append(out.Notes, fmt.Sprintf("prefix %s: cannot re-check %s: %v — keeping orphan",
					capturedPrefix.Prefix, object, err))
				out.KeptPrefixes = append(out.KeptPrefixes, ref)
				continue
			}
		}
		swept, failed, listErr := sweepSegmentPrefix(ctx, dst, capturedPrefix)
		out.SweptSegments += swept
		if listErr != nil {
			out.Notes = append(out.Notes, fmt.Sprintf("prefix %s: %v", capturedPrefix.Prefix, listErr))
		}
		if failed > 0 {
			out.Notes = append(out.Notes, fmt.Sprintf("prefix %s: %d segment deletes failed", capturedPrefix.Prefix, failed))
		}
	}

	if in.StagingPrefix != "" {
		for entry, err := range dst.Objects(ctx, in.DstContainer, swiftcontrol.ObjectListOptions{
			Deep: true, Prefix: in.StagingPrefix,
		}) {
			if err != nil {
				if !swiftcontrol.IsNotFound(err) {
					out.Notes = append(out.Notes, fmt.Sprintf("staging listing: %v", err))
				}
				break
			}
			object, ok := entry.(swiftcontrol.ObjectEntry)
			if !ok {
				continue
			}
			if err := dst.DeleteObject(ctx, in.DstContainer, object.Name); err == nil {
				out.SweptStaged++
			} else if !swiftcontrol.IsNotFound(err) {
				out.Notes = append(out.Notes, fmt.Sprintf("staged %s: %v", object.Name, err))
			}
		}
	}
	return out, nil
}

// objectFromGeneratedPrefix strips the generated "<date>-<time>-<rand>/"
// tail off a captured prefix, recovering the destination object name
// (which may itself contain slashes). Unparseable prefixes return
// ok=false — the caller keeps the orphan rather than guess.
func objectFromGeneratedPrefix(prefix string) (string, bool) {
	trimmed := strings.TrimSuffix(prefix, "/")
	slash := strings.LastIndex(trimmed, "/")
	if slash <= 0 {
		return "", false
	}
	return trimmed[:slash], true
}

func sweepSegmentPrefix(ctx context.Context, dst *swiftcontrol.Session, p rclone.SegmentPrefix) (swept, failed int, err error) {
	var names []string
	for entry, listErr := range dst.Objects(ctx, p.Container, swiftcontrol.ObjectListOptions{
		Deep: true, Prefix: p.Prefix,
	}) {
		if listErr != nil {
			if swiftcontrol.IsNotFound(listErr) {
				return swept, failed, nil // segment container never created
			}
			return swept, failed, listErr
		}
		if object, ok := entry.(swiftcontrol.ObjectEntry); ok {
			names = append(names, object.Name)
		}
	}
	for _, name := range names {
		if delErr := dst.DeleteObject(ctx, p.Container, name); delErr == nil || swiftcontrol.IsNotFound(delErr) {
			swept++
		} else {
			failed++
		}
	}
	return swept, failed, nil
}
