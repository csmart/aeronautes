// Package copy executes copy plans under the §28.6 boundary: the engine
// (rclone) writes only planned-new or uniquely-staged names, and every
// overwrite commits natively — stage-and-commit for plain and large
// objects alike. Verification is Aeronautes' own (listing-diff plus
// per-object HEADs, §28.4); engine exit codes are never trusted for
// completeness. Reconciliation sweeps only names and segment prefixes
// this job itself created: the phase invariant is zero destruction
// paths.
package copy

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/csmart/aeronautes/internal/swiftcontrol"
	"github.com/csmart/aeronautes/internal/transfer/plan"
	"github.com/csmart/aeronautes/internal/transfer/rclone"
)

// Engine is the byte-moving subprocess boundary (the pinned rclone in
// production; a fake in contract tests). Paths are "remote:container" /
// "remote:container/prefix" specs; names are relative to the paths.
type Engine interface {
	Copy(ctx context.Context, src, dst string, names []string, onEvent func(rclone.Event)) (*rclone.Result, error)
	CopyTo(ctx context.Context, src, dst string, onEvent func(rclone.Event)) (*rclone.Result, error)
}

// maxSinglePut is Swift's single-object limit: the native byte path
// refuses anything larger (a large object with an engine-unsafe name is
// an honest v1 failure, not a silent skip).
const maxSinglePut = 5 * 1024 * 1024 * 1024

// fingerprintMetaKey is the §28.4 destination verification fingerprint,
// stored as X-Object-Meta-Aeronautes-Fingerprint. Format v1: logical
// bytes and the source's HEAD ETag at copy time.
const fingerprintMetaKey = "Aeronautes-Fingerprint"

func fingerprint(logicalBytes int64, sourceETag string) string {
	return fmt.Sprintf("v1:%d:%s", logicalBytes, strings.Trim(sourceETag, `"`))
}

// Config wires one job's executor.
type Config struct {
	Source *swiftcontrol.Session
	Dest   *swiftcontrol.Session
	// Engine may be nil: every engine-routed item then fails honestly
	// (native-routed items still run).
	Engine Engine
	// SrcSpec/DstSpec are the engine path specs for the job scope
	// ("aeronautes_src:container" — prefixes are appended here).
	SrcSpec, DstSpec           string
	SrcContainer, DstContainer string
	// SrcPrefix/DstPrefix must be "" or end with "/".
	SrcPrefix, DstPrefix string
	// JobID namespaces staging ("<DstPrefix>.aeronautes/stage/<job>/").
	JobID string
	// BatchSize caps names per engine invocation (default 500).
	BatchSize int
	// BatchBytes caps a batch's planned bytes (default 64 GiB): no
	// single child should outlive a token on sheer transfer size —
	// each spawn re-resolves fresh remotes (§26.2).
	BatchBytes int64
	// OnEvent receives engine events (progress streaming); optional.
	OnEvent func(rclone.Event)
	// OnItem is told each item's outcome as it happens; optional.
	OnItem func(ItemOutcome)
}

// ItemOutcome is one name's fate during execution.
type ItemOutcome struct {
	Name    string
	Outcome string // "copied", "skipped-equal", "conflict", "failed"
	Detail  string
}

// Failure is one per-object failure (bounded collection).
type Failure struct {
	Name  string
	Stage string
	Err   string
}

// Report is the execution result the service maps onto §46 states.
type Report struct {
	Copied       int64
	SkippedEqual int64
	BytesCopied  int64
	Overwrites   int64
	NativeRouted int64
	Conflicts    []string
	Failures     []Failure
	// CapturedPrefixes are every generated segment prefix observed
	// (§26.4 ownership evidence); CommittedPrefixes the subset now
	// referenced by live or staged-committed manifests.
	CapturedPrefixes  []rclone.SegmentPrefix
	CommittedPrefixes []rclone.SegmentPrefix
	// StagedResidue lists staging names known to exist if execution was
	// interrupted before cleanup; Reconcile sweeps them.
	StagedResidue []string
	// OldManifests records overwritten destinations' previous DLO
	// manifest values ("container/prefix") — never reclaimed in v1
	// (§28.6(b): exclusive ownership is normally unprovable), kept as
	// evidence.
	OldManifests map[string]string
}

const maxFailures = 200

// Executor runs one job's transfer phase.
type Executor struct {
	cfg        Config
	report     *Report
	stage      string // staging prefix inside DstContainer
	stageN     int
	batch      []string
	batchBytes int64
	printed    map[rclone.SegmentPrefix]bool
}

// New validates the config and builds an executor.
func New(cfg Config) (*Executor, error) {
	if cfg.Source == nil || cfg.Dest == nil {
		return nil, errors.New("executor needs both sessions")
	}
	if cfg.SrcContainer == "" || cfg.DstContainer == "" || cfg.JobID == "" {
		return nil, errors.New("executor needs containers and a job id")
	}
	for _, p := range []string{cfg.SrcPrefix, cfg.DstPrefix} {
		if p != "" && !strings.HasSuffix(p, "/") {
			return nil, fmt.Errorf("prefix %q must be empty or end with '/'", p)
		}
	}
	if cfg.BatchSize <= 0 {
		cfg.BatchSize = 500
	}
	if cfg.BatchBytes <= 0 {
		cfg.BatchBytes = 64 << 30
	}
	return &Executor{
		cfg:     cfg,
		report:  &Report{OldManifests: map[string]string{}},
		stage:   cfg.DstPrefix + ".aeronautes/stage/" + cfg.JobID + "/",
		printed: map[rclone.SegmentPrefix]bool{},
	}, nil
}

// StagingPrefix exposes the job's staging namespace (for tests and the
// reconciler).
func (e *Executor) StagingPrefix() string { return e.stage }

// Handle consumes one plan item — wire it as plan.Copy's emit callback
// so planning streams straight into execution with bounded memory.
func (e *Executor) Handle(ctx context.Context, item plan.Item) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	switch item.Verdict {
	case plan.VerdictSame, plan.VerdictDestOnly:
		return nil
	case plan.VerdictUnknown:
		return e.runUnknown(ctx, item)
	case plan.VerdictNew:
		return e.runNew(ctx, item)
	case plan.VerdictChanged:
		return e.runOverwrite(ctx, item)
	}
	return fmt.Errorf("unhandled verdict %q", item.Verdict)
}

// Finish flushes the pending engine batch and returns the report. The
// report is valid (and needed by Reconcile) even after an error or
// cancellation mid-run.
func (e *Executor) Finish(ctx context.Context) (*Report, error) {
	err := e.flushBatch(ctx)
	return e.report, err
}

// Run executes a fully materialized item list (tests, small plans).
func (e *Executor) Run(ctx context.Context, items []plan.Item) (*Report, error) {
	for _, item := range items {
		if err := e.Handle(ctx, item); err != nil {
			report, _ := e.Finish(ctx)
			return report, err
		}
	}
	return e.Finish(ctx)
}

func (e *Executor) fail(name, stage string, err error) {
	if len(e.report.Failures) < maxFailures {
		e.report.Failures = append(e.report.Failures, Failure{Name: name, Stage: stage, Err: err.Error()})
	}
	e.emit(name, "failed", stage+": "+err.Error())
}

func (e *Executor) emit(name, outcome, detail string) {
	if e.cfg.OnItem != nil {
		e.cfg.OnItem(ItemOutcome{Name: name, Outcome: outcome, Detail: detail})
	}
}

// runUnknown HEAD-resolves a conservative pair (§28.4): equality proven
// → skip; destination gone → New; otherwise an overwrite.
func (e *Executor) runUnknown(ctx context.Context, item plan.Item) error {
	src, err := e.cfg.Source.ObjectDetail(ctx, e.cfg.SrcContainer, e.cfg.SrcPrefix+item.Name)
	if err != nil {
		e.fail(item.Name, "resolve-head-source", err)
		return nil
	}
	dst, err := e.cfg.Dest.ObjectDetail(ctx, e.cfg.DstContainer, e.cfg.DstPrefix+item.Name)
	if err != nil {
		if swiftcontrol.IsNotFound(err) {
			item.SourceBytes = src.ContentLength
			return e.runNew(ctx, item)
		}
		e.fail(item.Name, "resolve-head-dest", err)
		return nil
	}
	if provenEqual(src, dst) {
		e.report.SkippedEqual++
		e.emit(item.Name, "skipped-equal", "")
		return nil
	}
	item.SourceBytes = src.ContentLength
	item.DestBytes = dst.ContentLength
	item.DestETag = dst.ETag
	return e.runOverwrite(ctx, item)
}

// provenEqual implements the executor-grade §28.4 test over two
// authoritative HEADs: plain pairs by size+ETag; anything manifest-y by
// the destination fingerprint (proof Aeronautes itself verified this
// copy of this content).
func provenEqual(src, dst *swiftcontrol.ObjectDetail) bool {
	if src.Class == swiftcontrol.ClassPlain && dst.Class == swiftcontrol.ClassPlain {
		return src.ContentLength == dst.ContentLength &&
			strings.Trim(src.ETag, `"`) == strings.Trim(dst.ETag, `"`)
	}
	want := fingerprint(src.ContentLength, src.ETag)
	return dst.Meta[fingerprintMetaKey] == want
}

// runNew copies a name with no live destination: engine-routed names
// batch; native-routed names stream through the client immediately.
func (e *Executor) runNew(ctx context.Context, item plan.Item) error {
	if item.Route == plan.RouteNative {
		e.report.NativeRouted++
		e.nativeCopy(ctx, item, false)
		return nil
	}
	e.batch = append(e.batch, item.Name)
	e.batchBytes += item.SourceBytes
	if len(e.batch) >= e.cfg.BatchSize || e.batchBytes >= e.cfg.BatchBytes {
		return e.flushBatch(ctx)
	}
	return nil
}

// flushBatch runs one engine copy over the queued planned-new names and
// then finalizes each: per-object destination HEAD (the engine's exit
// code proves nothing, §14.3), §27.4 metadata re-application, and the
// destination fingerprint.
func (e *Executor) flushBatch(ctx context.Context) error {
	if len(e.batch) == 0 {
		return nil
	}
	if err := ctx.Err(); err != nil {
		// Cancelled before this batch started: the names are simply not
		// copied — never spawn an engine child after cancellation.
		e.batch = nil
		e.batchBytes = 0
		return err
	}
	names := e.batch
	e.batch = nil
	e.batchBytes = 0
	if e.cfg.Engine == nil {
		for _, name := range names {
			e.fail(name, "engine", errors.New("no engine configured"))
		}
		return nil
	}
	result, err := e.cfg.Engine.Copy(ctx, e.enginePath(e.cfg.SrcSpec, e.cfg.SrcPrefix),
		e.enginePath(e.cfg.DstSpec, e.cfg.DstPrefix), names, e.captureEvents)
	if result != nil {
		e.capturePrefixes(result.SegmentPrefixes)
	}
	if err != nil {
		for _, name := range names {
			e.fail(name, "engine-copy", err)
		}
		return nil
	}
	for _, name := range names {
		if err := ctx.Err(); err != nil {
			return err
		}
		e.finalizeEngineCopy(ctx, name)
	}
	return nil
}

// enginePath appends a prefix to a "remote:container" spec.
func (e *Executor) enginePath(spec, prefix string) string {
	if prefix == "" {
		return spec
	}
	return spec + "/" + strings.TrimSuffix(prefix, "/")
}

func (e *Executor) captureEvents(event rclone.Event) {
	if e.cfg.OnEvent != nil {
		e.cfg.OnEvent(event)
	}
}

func (e *Executor) capturePrefixes(prefixes []rclone.SegmentPrefix) {
	for _, p := range prefixes {
		if !e.printed[p] {
			e.printed[p] = true
			e.report.CapturedPrefixes = append(e.report.CapturedPrefixes, p)
		}
	}
}

// finalizeEngineCopy verifies one engine-written destination and applies
// the §27.4 set. The destination of a large source is the engine's own
// DLO — its manifest header MUST ride the metadata POST (a naive POST
// demotes a DLO, the measured §17.1 trap).
func (e *Executor) finalizeEngineCopy(ctx context.Context, name string) {
	src, err := e.cfg.Source.ObjectDetail(ctx, e.cfg.SrcContainer, e.cfg.SrcPrefix+name)
	if err != nil {
		e.fail(name, "finalize-head-source", err)
		return
	}
	dst, err := e.cfg.Dest.ObjectDetail(ctx, e.cfg.DstContainer, e.cfg.DstPrefix+name)
	if err != nil {
		if swiftcontrol.IsNotFound(err) {
			e.fail(name, "verify", errors.New("engine reported success but the destination is missing (silent drop)"))
			return
		}
		e.fail(name, "finalize-head-dest", err)
		return
	}
	if verifyErr := verifyCopied(src, dst); verifyErr != nil {
		e.fail(name, "verify", verifyErr)
		return
	}
	if dst.Class == swiftcontrol.ClassDLO {
		e.markCommitted(dst.Manifest)
	}

	headers := metadataHeaders(src, "X-Object-Meta-")
	headers["X-Object-Meta-"+fingerprintMetaKey] = fingerprint(src.ContentLength, src.ETag)
	if dst.Class == swiftcontrol.ClassDLO {
		headers["X-Object-Manifest"] = dst.Manifest
	}
	if err := e.cfg.Dest.PostObjectHeaders(ctx, e.cfg.DstContainer, e.cfg.DstPrefix+name, headers); err != nil {
		e.fail(name, "metadata-apply", err)
		return
	}
	e.report.Copied++
	e.report.BytesCopied += src.ContentLength
	e.emit(name, "copied", "")
}

// verifyCopied is the post-write §28.4 check: plain→plain by ETag+size,
// anything larger by logical size (the engine may legitimately
// re-segment; equality-of-manifest is not required).
func verifyCopied(src, dst *swiftcontrol.ObjectDetail) error {
	if src.ContentLength != dst.ContentLength {
		return fmt.Errorf("logical size %d != source %d", dst.ContentLength, src.ContentLength)
	}
	if src.Class == swiftcontrol.ClassPlain && dst.Class == swiftcontrol.ClassPlain &&
		strings.Trim(src.ETag, `"`) != strings.Trim(dst.ETag, `"`) {
		return fmt.Errorf("etag %s != source %s", dst.ETag, src.ETag)
	}
	return nil
}

// metadataHeaders builds the §27.4 preserved writable set from a source
// HEAD, as headers under the given meta prefix: user metadata (minus
// Aeronautes' own fingerprint — re-stamped fresh), content headers, and
// scheduled expiry. Never manifest/system headers.
func metadataHeaders(src *swiftcontrol.ObjectDetail, metaPrefix string) map[string]string {
	headers := map[string]string{}
	for k, v := range src.Meta {
		if k == fingerprintMetaKey {
			continue
		}
		headers[metaPrefix+k] = v
	}
	if src.ContentType != "" {
		headers["Content-Type"] = src.ContentType
	}
	if src.ContentEncoding != "" {
		headers["Content-Encoding"] = src.ContentEncoding
	}
	if src.ContentDisposition != "" {
		headers["Content-Disposition"] = src.ContentDisposition
	}
	if !src.DeleteAt.IsZero() {
		headers["X-Delete-At"] = fmt.Sprint(src.DeleteAt.Unix())
	}
	return headers
}

// nativeCopy is the §14.3 fallback: a raw logical GET streamed into a
// single PUT carrying the full §27.4 set. A native PUT is atomic and
// manifest-only, so overwrites need no staging dance — but it cannot
// exceed the single-object limit; larger unsafe-named objects fail
// honestly.
func (e *Executor) nativeCopy(ctx context.Context, item plan.Item, overwrite bool) {
	srcName := e.cfg.SrcPrefix + item.Name
	dstName := e.cfg.DstPrefix + item.Name

	body, src, err := e.cfg.Source.OpenObject(ctx, e.cfg.SrcContainer, srcName)
	if err != nil {
		e.fail(item.Name, "native-get", err)
		return
	}
	defer body.Close()
	if src.ContentLength > maxSinglePut {
		e.fail(item.Name, "native-size", fmt.Errorf(
			"logical size %d exceeds the single-PUT limit and the name cannot ride the engine (v1 limitation)", src.ContentLength))
		return
	}
	if overwrite {
		// §28.6 re-HEAD: an intervening external write turns this into a
		// conflict, never a blind overwrite.
		live, err := e.cfg.Dest.ObjectDetail(ctx, e.cfg.DstContainer, dstName)
		if err != nil && !swiftcontrol.IsNotFound(err) {
			e.fail(item.Name, "overwrite-rehead", err)
			return
		}
		if err == nil {
			if e.conflicted(item, live) {
				return
			}
			if live.Class == swiftcontrol.ClassDLO {
				e.report.OldManifests[dstName] = live.Manifest
			}
		}
	}

	headers := metadataHeaders(src, "X-Object-Meta-")
	headers["X-Object-Meta-"+fingerprintMetaKey] = fingerprint(src.ContentLength, src.ETag)
	if _, err := e.cfg.Dest.PutObject(ctx, e.cfg.DstContainer, dstName, body, src.ContentType, headers); err != nil {
		e.fail(item.Name, "native-put", err)
		return
	}
	dst, err := e.cfg.Dest.ObjectDetail(ctx, e.cfg.DstContainer, dstName)
	if err != nil {
		e.fail(item.Name, "native-verify", err)
		return
	}
	if err := verifyCopied(src, dst); err != nil {
		e.fail(item.Name, "native-verify", err)
		return
	}
	e.report.Copied++
	e.report.BytesCopied += src.ContentLength
	if overwrite {
		e.report.Overwrites++
	}
	e.emit(item.Name, "copied", "native")
}

// conflicted applies the intervening-write check against the plan-time
// destination state; a mismatch is recorded and the item skipped.
func (e *Executor) conflicted(item plan.Item, live *swiftcontrol.ObjectDetail) bool {
	if item.DestETag != "" && strings.Trim(live.ETag, `"`) != strings.Trim(item.DestETag, `"`) {
		e.report.Conflicts = append(e.report.Conflicts, item.Name)
		e.emit(item.Name, "conflict", "destination changed since planning")
		return true
	}
	return false
}

// runOverwrite is §28.6 stage-and-commit: the engine writes a unique
// staged name; Aeronautes verifies it, re-HEADs the live name, and
// commits natively — server-side COPY for a plain staged object, a
// manifest-swap PUT for a staged DLO — then cleans the staging litter.
func (e *Executor) runOverwrite(ctx context.Context, item plan.Item) error {
	if item.Route == plan.RouteNative {
		e.report.NativeRouted++
		e.nativeCopy(ctx, item, true)
		return nil
	}
	if e.cfg.Engine == nil {
		e.fail(item.Name, "engine", errors.New("no engine configured"))
		return nil
	}

	srcName := e.cfg.SrcPrefix + item.Name
	dstName := e.cfg.DstPrefix + item.Name

	// 1. persist the live destination's manifest for evidence.
	live, err := e.cfg.Dest.ObjectDetail(ctx, e.cfg.DstContainer, dstName)
	if err != nil && !swiftcontrol.IsNotFound(err) {
		e.fail(item.Name, "overwrite-head", err)
		return nil
	}
	if err == nil && live.Class == swiftcontrol.ClassDLO {
		e.report.OldManifests[dstName] = live.Manifest
	}

	// 2. engine copy to the unique staged name.
	e.stageN++
	stagedName := fmt.Sprintf("%s%06d", e.stage, e.stageN)
	e.report.StagedResidue = append(e.report.StagedResidue, stagedName)
	result, err := e.cfg.Engine.CopyTo(ctx,
		e.cfg.SrcSpec+"/"+srcName, e.cfg.DstSpec+"/"+stagedName, e.captureEvents)
	if result != nil {
		e.capturePrefixes(result.SegmentPrefixes)
	}
	if err != nil {
		e.fail(item.Name, "stage-copy", err)
		return nil
	}

	// 3. verify the staged object against the source.
	src, err := e.cfg.Source.ObjectDetail(ctx, e.cfg.SrcContainer, srcName)
	if err != nil {
		e.fail(item.Name, "stage-head-source", err)
		return nil
	}
	staged, err := e.cfg.Dest.ObjectDetail(ctx, e.cfg.DstContainer, stagedName)
	if err != nil {
		e.fail(item.Name, "stage-verify", fmt.Errorf("staged object missing: %w", err))
		return nil
	}
	if err := verifyCopied(src, staged); err != nil {
		e.fail(item.Name, "stage-verify", err)
		e.cleanStaged(ctx, stagedName)
		return nil
	}

	// 4. re-HEAD the live name: intervening external write → conflict.
	liveNow, err := e.cfg.Dest.ObjectDetail(ctx, e.cfg.DstContainer, dstName)
	if err != nil && !swiftcontrol.IsNotFound(err) {
		e.fail(item.Name, "commit-rehead", err)
		return nil
	}
	if err == nil && e.conflicted(item, liveNow) {
		e.cleanStaged(ctx, stagedName)
		return nil
	}

	// 5. commit natively.
	headers := metadataHeaders(src, "X-Object-Meta-")
	headers["X-Object-Meta-"+fingerprintMetaKey] = fingerprint(src.ContentLength, src.ETag)
	if staged.Class == swiftcontrol.ClassDLO {
		// Manifest-swap PUT: the live name becomes a DLO pointing at the
		// staged segments in one atomic write (validated on real Swift).
		headers["X-Object-Manifest"] = staged.Manifest
		if _, err := e.cfg.Dest.PutObject(ctx, e.cfg.DstContainer, dstName, strings.NewReader(""), src.ContentType, headers); err != nil {
			e.fail(item.Name, "commit-manifest", err)
			return nil
		}
		e.markCommitted(staged.Manifest)
	} else {
		if err := e.cfg.Dest.CopyObject(ctx, e.cfg.DstContainer, stagedName, e.cfg.DstContainer, dstName, headers); err != nil {
			e.fail(item.Name, "commit-copy", err)
			return nil
		}
	}

	// 6. verify the committed live object.
	committed, err := e.cfg.Dest.ObjectDetail(ctx, e.cfg.DstContainer, dstName)
	if err != nil {
		e.fail(item.Name, "commit-verify", err)
		return nil
	}
	if err := verifyCopied(src, committed); err != nil {
		e.fail(item.Name, "commit-verify", err)
		return nil
	}

	// 7. clean the staging litter: for a staged DLO the manifest ONLY —
	// its segments now belong to the live manifest.
	e.cleanStaged(ctx, stagedName)
	e.report.Copied++
	e.report.Overwrites++
	e.report.BytesCopied += src.ContentLength
	e.emit(item.Name, "copied", "stage-and-commit")
	return nil
}

// markCommitted records that a captured segment prefix is now referenced
// by a committed manifest ("container/prefix"), so reconciliation never
// sweeps it.
func (e *Executor) markCommitted(manifest string) {
	if slash := strings.Index(manifest, "/"); slash > 0 {
		e.report.CommittedPrefixes = append(e.report.CommittedPrefixes,
			rclone.SegmentPrefix{Container: manifest[:slash], Prefix: manifest[slash+1:]})
	}
}

func (e *Executor) cleanStaged(ctx context.Context, stagedName string) {
	if err := e.cfg.Dest.DeleteObject(ctx, e.cfg.DstContainer, stagedName); err != nil && !swiftcontrol.IsNotFound(err) {
		return // Reconcile retries via StagedResidue.
	}
	for i, name := range e.report.StagedResidue {
		if name == stagedName {
			e.report.StagedResidue = append(e.report.StagedResidue[:i], e.report.StagedResidue[i+1:]...)
			return
		}
	}
}
