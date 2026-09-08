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
// name other than an execution-time refusal must now prove Same —
// listing-grade, or by authoritative HEADs
// plus the destination fingerprint for conservative rows. Destination-
// only names are not this job's business (Copy never deletes).
func (e *Executor) Verify(ctx context.Context, source, dest listingStream) (*VerifyReport, error) {
	report := &VerifyReport{}
	if e.stopped != nil {
		return report, e.stopped
	}
	refused, err := e.refusals.reader(e.report.OverwritesRefused)
	if err != nil {
		return report, err
	}
	_, err = plan.Copy(ctx, source, dest, e.cfg.SrcPrefix, e.cfg.DstPrefix, func(item plan.Item) error {
		excluded, err := refused.contains(item.Name)
		if err != nil {
			return err
		}
		if excluded || item.Verdict == plan.VerdictDestOnly {
			return nil
		}
		observation, failure := e.verifyItem(ctx, item)
		if failure == nil {
			report.Verified++
		} else {
			report.fail(item.Name, "verify", failure)
		}
		if e.cfg.Recorder != nil {
			return e.stop(e.cfg.Recorder.Verify(item.Name, observation))
		}
		return nil
	})
	return report, errors.Join(err, refused.finish())
}

func (e *Executor) verifyItem(ctx context.Context, item plan.Item) (Observation, error) {
	var err error
	code := CodeUnverified
	switch item.Verdict {
	case plan.VerdictSame:
		return Observation{Result: ResultVerified, Effect: EffectNotAttempted}, nil
	case plan.VerdictUnknown:
		var src, dst *swiftcontrol.ObjectDetail
		src, err = e.cfg.Source.ObjectDetail(ctx, e.cfg.SrcContainer, e.cfg.SrcPrefix+item.Name)
		if err == nil {
			dst, err = e.cfg.Dest.ObjectDetail(ctx, e.cfg.DstContainer, e.cfg.DstPrefix+item.Name)
		}
		if err != nil {
			code = CodeUnknown
		} else if provenEqual(src, dst) {
			return Observation{Result: ResultVerified, Effect: EffectNotAttempted}, nil
		} else {
			err = errors.New("equality unprovable after copy")
		}
	case plan.VerdictNew:
		err = errors.New("still missing at the destination")
	case plan.VerdictChanged:
		err = errors.New("destination still differs from source")
	default:
		err = errors.New("unsupported verification verdict")
		code = CodeUnsupported
	}
	return Observation{Result: ResultFailed, Effect: EffectNotAttempted,
		Diagnostic: diagnostic(StageVerification, code, err)}, err
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
