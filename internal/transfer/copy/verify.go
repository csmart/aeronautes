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
	rest, err := SweepLitter(ctx, e.cfg.Dest, e.cfg.DstContainer, e.stage,
		report.CapturedPrefixes, report.CommittedPrefixes)
	out.SweptStaged += rest.SweptStaged
	out.SweptSegments += rest.SweptSegments
	out.KeptPrefixes = append(out.KeptPrefixes, rest.KeptPrefixes...)
	out.Notes = append(out.Notes, rest.Notes...)
	return out, err
}

// SweepLitter is the reconciliation core, callable WITHOUT a live
// executor — the startup crash-recovery sweep replays it from
// persisted records (§65.8 item 2). stagingPrefix "" skips the staging
// half (upload jobs have none). The rules are the in-job ones: only
// job-created staging names and captured prefixes; a prefix referenced
// by a committed or live manifest is kept, and doubt keeps the orphan.
func SweepLitter(ctx context.Context, dst *swiftcontrol.Session, dstContainer, stagingPrefix string,
	captured, committed []rclone.SegmentPrefix) (*ReconcileReport, error) {

	out := &ReconcileReport{}
	if stagingPrefix != "" {
		for entry, err := range dst.Objects(ctx, dstContainer, swiftcontrol.ObjectListOptions{
			Deep: true, Prefix: stagingPrefix,
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
			if err := dst.DeleteObject(ctx, dstContainer, object.Name); err == nil {
				out.SweptStaged++
			}
		}
	}

	committedSet := map[rclone.SegmentPrefix]bool{}
	for _, p := range committed {
		committedSet[p] = true
	}
	for _, capturedPrefix := range captured {
		if committedSet[capturedPrefix] {
			out.KeptPrefixes = append(out.KeptPrefixes, capturedPrefix.Container+"/"+capturedPrefix.Prefix)
			continue
		}
		// Guard: the engine may have completed this object before the
		// interruption — its live manifest then references the prefix.
		if object, ok := objectFromGeneratedPrefix(capturedPrefix.Prefix); ok && dstContainer != "" {
			live, err := dst.ObjectDetail(ctx, dstContainer, object)
			if err == nil && live.Class == swiftcontrol.ClassDLO &&
				live.Manifest == capturedPrefix.Container+"/"+capturedPrefix.Prefix {
				out.KeptPrefixes = append(out.KeptPrefixes, capturedPrefix.Container+"/"+capturedPrefix.Prefix)
				continue
			}
			if err != nil && !swiftcontrol.IsNotFound(err) {
				out.Notes = append(out.Notes, fmt.Sprintf("prefix %s: cannot re-check %s: %v — keeping orphan",
					capturedPrefix.Prefix, object, err))
				out.KeptPrefixes = append(out.KeptPrefixes, capturedPrefix.Container+"/"+capturedPrefix.Prefix)
				continue
			}
		}
		swept, listErr := sweepSegmentPrefix(ctx, dst, capturedPrefix)
		out.SweptSegments += swept
		if listErr != nil {
			out.Notes = append(out.Notes, fmt.Sprintf("prefix %s: %v", capturedPrefix.Prefix, listErr))
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

func sweepSegmentPrefix(ctx context.Context, dst *swiftcontrol.Session, p rclone.SegmentPrefix) (int, error) {
	swept := 0
	var names []string
	for entry, err := range dst.Objects(ctx, p.Container, swiftcontrol.ObjectListOptions{
		Deep: true, Prefix: p.Prefix,
	}) {
		if err != nil {
			if swiftcontrol.IsNotFound(err) {
				return swept, nil // segment container never created
			}
			return swept, err
		}
		if object, ok := entry.(swiftcontrol.ObjectEntry); ok {
			names = append(names, object.Name)
		}
	}
	for _, name := range names {
		if err := dst.DeleteObject(ctx, p.Container, name); err == nil {
			swept++
		}
	}
	return swept, nil
}
