package copy

import (
	"context"
	"errors"
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
// name other than an execution-time refusal must now prove Same —
// listing-grade, or by authoritative HEADs
// plus the destination fingerprint for conservative rows. Destination-
// only names are not this job's business (Copy never deletes).
func (e *Executor) Verify(ctx context.Context, source, dest listingStream) (*VerifyReport, error) {
	report := &VerifyReport{}
	if e.stopped != nil {
		return report, e.stopped
	}
	// Refusals land in two ascending spools: the immediate route's and the staged
	// route's, the latter flushed out of name order. Verify merges them.
	if e.refusals.count+e.refusalsDeferred.count != e.report.OverwritesRefused {
		return report, errors.New("refusal spool counts do not sum to the refusal total")
	}
	immediate, err := e.refusals.reader(e.refusals.count)
	if err != nil {
		return report, err
	}
	deferredRefusals, err := e.refusalsDeferred.reader(e.refusalsDeferred.count)
	if err != nil {
		return report, err
	}
	refused := refusalReaders{a: immediate, b: deferredRefusals}
	// Opened once and read forward beside the refusal spool, in the same
	// ascending name order execution wrote both.
	evidence, err := e.evidence.reader()
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
		kind, payload, err := evidence.at(item.Name)
		if err != nil {
			return err
		}
		observation, failure := e.verifyItem(ctx, item, kind, payload)
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
	return report, errors.Join(err, refused.finish(), evidence.finish())
}

func (e *Executor) verifyItem(ctx context.Context, item plan.Item, kind evidenceKind, payload string) (Observation, error) {
	var err error
	code := CodeUnverified
	// Before any success path, including the listing-grade Same verdict and the
	// HEAD-grade provenEqual below. ADR 0045: a name this executor recorded as a
	// content failure is not eligible for a verified count, a Same verdict or a
	// skip in the same job, independently of the stamp. A fresh listing can say
	// Same about a name whose publication this job already found wrong — a
	// destination replaced with equal bytes after the fact is enough — and that
	// agreement is about two listing rows, not about the copy that failed.
	if kind == evidenceContentFailure {
		err = errors.New("this job recorded a content failure for this name, so no later agreement verifies it")
		return Observation{Result: ResultFailed, Effect: EffectNotAttempted,
			Diagnostic: ClassifyDiagnostic(StageVerification, code, err)}, err
	}
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
		} else if provenEqual(src, dst) || nativeCopyHolds(kind, payload, src, dst) {
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
		Diagnostic: ClassifyDiagnostic(StageVerification, code, err)}, err
}

// nativeCopyHolds answers whether this job's own native route published this
// name, compared the published object against the source's content, and whether
// BOTH ends still look the way they did: the live destination still reports the
// digest that comparison settled on, and the source still presents the identity
// the comparison was made against.
//
// Both halves are needed, and the source half is the one a first version of this
// left out. A destination can still hold exactly the verified bytes while the
// source has since been changed to different content of the same length, in which
// case the copy was true when it was made and the two sides no longer agree. The
// fresh pass is what notices that, and crediting it would turn a stale proof into
// a current one. The identity compared is the same value the fingerprint used to
// assert remotely, so the check is as strong as the stamp was, kept in this job
// instead of written onto the object.
//
// This exists because ADR 0045 stops the native route stamping: the stamp would
// have to be written in the publishing request, before anything is checked, which
// is what made a detected failure usable as proof. Without a stamp, provenEqual
// cannot settle a manifest source whose destination is a plain object, so the
// successful comparison is retained for this job and nothing else. It is
// deliberately weaker than a stamp in the one way that matters: a later run holds
// none of it, so a later equality or skip decision stays conservative.
func nativeCopyHolds(kind evidenceKind, payload string, src, dst *swiftcontrol.ObjectDetail) bool {
	if kind != evidenceCopied {
		return false
	}
	identity, digest, ok := sourceIdentityAndDigest(payload)
	if !ok || identity == "" || digest == "" {
		return false
	}
	return identity == fingerprint(src.ContentLength, src.ETag) &&
		dst.Class == swiftcontrol.ClassPlain &&
		strings.Trim(dst.ETag, `"`) == digest
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
