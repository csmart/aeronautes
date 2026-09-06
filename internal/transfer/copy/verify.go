package copy

import (
	"context"
	"errors"
	"iter"

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

// ReconcileReport preserves the existing reporting shape while automatic
// reclamation is unavailable. Zero deletions do not prove residue absent.
type ReconcileReport struct {
	SweptStaged   int
	SweptSegments int
	// Captured prefixes remain evidence, not exclusive ownership.
	KeptPrefixes []string
	Notes        []string
}

// Reconcile retains every uncertain object after execution. Creation
// provenance cannot rule out outside aliases or a write still landing.
func (e *Executor) Reconcile(ctx context.Context, report *Report) (*ReconcileReport, error) {
	return SweepLitter(ctx, e.cfg.Dest, SweepInput{
		DstContainer: e.cfg.DstContainer, StagingPrefix: e.stage,
		Captured: report.CapturedPrefixes, Committed: report.CommittedPrefixes,
	})
}

// SweepInput preserves the facts supplied by current and older recovery
// callers. None of these facts grants deletion authority (ADR 0020).
type SweepInput struct {
	DstContainer  string
	StagingPrefix string
	Captured      []rclone.SegmentPrefix
	Committed     []rclone.SegmentPrefix
	Committing    map[rclone.SegmentPrefix]string
}

// SweepLitter keeps the existing recovery entry point fail-closed while
// reclamation is unavailable. The session is deliberately unused: neither
// a historical handle nor a current bookmark proves cleanup authority.
func SweepLitter(ctx context.Context, _ *swiftcontrol.Session, in SweepInput) (*ReconcileReport, error) {
	out := &ReconcileReport{
		Notes: []string{"automatic recovery cleanup is unavailable; data and records retained, remote residue unverified"},
	}
	for _, p := range in.Captured {
		out.KeptPrefixes = append(out.KeptPrefixes, p.Container+"/"+p.Prefix)
	}
	return out, ctx.Err()
}
