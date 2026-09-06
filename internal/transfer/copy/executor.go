// Package copy transfers planned-new objects through conditional native
// creation. Engine writes stay in a unique staging namespace because
// writing a live destination can erase another writer's bytes or shared
// segments. Existing changed destinations are refused under ADR 0020.
// Verification excludes the exact refused names while reconciliation
// retains uncertain data and recovery records.
package copy

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

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
	// OnAuthFailure invalidates the endpoint tokens before the one
	// auth-shaped retry: the fresh child must authenticate anew, never
	// replay the rejected token (review F-06). Optional.
	OnAuthFailure func()
	// OnRecovery persists crash-recovery facts AS THEY HAPPEN (§65.8
	// item 2, §65.9 item 5): captured prefixes stream durably DURING the
	// child run, a committing record (with the live object identity)
	// lands BEFORE every live manifest PUT, and committed records after.
	// A persistence failure fails closed: no commit crosses the
	// boundary without its durable intent. Optional (tests).
	OnRecovery func(kind string, container, prefix, object string) error
}

// ItemOutcome is one name's fate during execution.
type ItemOutcome struct {
	Name    string
	Outcome string // "copied", "skipped-equal", "overwrite-refused", "conflict", "failed"
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
	Copied            int64
	SkippedEqual      int64
	BytesCopied       int64
	Overwrites        int64
	OverwritesRefused int64
	NativeRouted      int64
	Conflicts         []string
	Failures          []Failure
	// CapturedPrefixes are every generated segment prefix observed
	// (§26.4 ownership evidence); CommittedPrefixes the subset now
	// referenced by live or staged-committed manifests.
	CapturedPrefixes  []rclone.SegmentPrefix
	CommittedPrefixes []rclone.SegmentPrefix
	// StagedResidue lists staging names known to exist if execution was
	// interrupted before cleanup; recovery retains them.
	StagedResidue []string
	// OldManifests preserves the historical report shape. Refusal never
	// populates it and historical references cannot authorise cleanup.
	OldManifests map[string]string
}

const maxFailures = 200

// Executor runs one job's transfer phase.
type Executor struct {
	cfg        Config
	report     *Report
	stage      string // staging prefix inside DstContainer
	refusals   refusalLog
	batch      []string
	batchBytes int64
	printed    map[rclone.SegmentPrefix]bool
	// recoveryBroken latches a failed captured-fact persist: staged
	// bytes may keep landing, but nothing COMMITS past a boundary the
	// sweep could not replay (§65.9 item 5 fail-closed rule).
	recoveryBroken error
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
		return e.refuseOverwrite(item)
	}
	return fmt.Errorf("unhandled verdict %q", item.Verdict)
}

// Finish flushes the pending engine batch and returns the report. The
// report is valid (and needed by Reconcile) even after an error or
// cancellation mid-run.
func (e *Executor) Finish(ctx context.Context) (*Report, error) {
	err := e.flushBatch(ctx)
	return e.report, errors.Join(err, e.refusals.broken)
}

// Run accepts the same strictly name-ordered items as plan.Copy so
// execution decisions can be checked without an in-memory namespace.
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
// → skip; destination gone → New; otherwise refuse the overwrite.
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
	return e.refuseOverwrite(item)
}

// provenEqual implements the executor-grade §28.4 test over two
// authoritative HEADs: plain pairs by size+ETag; anything manifest-y by
// the destination fingerprint (proof Aeronautes itself verified this
// copy of this content) AND live logical-length equality — a DLO whose
// backing segments changed or vanished re-sums differently on HEAD, so
// a stale fingerprint alone can no longer skip it (review H-04).
func provenEqual(src, dst *swiftcontrol.ObjectDetail) bool {
	if src.Class == swiftcontrol.ClassPlain && dst.Class == swiftcontrol.ClassPlain {
		return src.ContentLength == dst.ContentLength &&
			strings.Trim(src.ETag, `"`) == strings.Trim(dst.ETag, `"`)
	}
	want := fingerprint(src.ContentLength, src.ETag)
	return dst.Meta[fingerprintMetaKey] == want && dst.ContentLength == src.ContentLength
}

// runNew copies a name with no live destination: engine-routed names
// batch toward the staging namespace; native-routed names stream
// through the client immediately.
func (e *Executor) runNew(ctx context.Context, item plan.Item) error {
	if item.Route == plan.RouteNative {
		e.report.NativeRouted++
		e.nativeCopy(ctx, item)
		return nil
	}
	e.batch = append(e.batch, item.Name)
	e.batchBytes += item.SourceBytes
	if len(e.batch) >= e.cfg.BatchSize || e.batchBytes >= e.cfg.BatchBytes {
		return e.flushBatch(ctx)
	}
	return nil
}

// flushBatch runs one engine copy of the queued planned-new names into
// the job staging namespace, then commits each natively — per-object
// verification (the engine's exit proves nothing for success, §14.3),
// conflict re-HEAD, §27.4 metadata, and the destination fingerprint.
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
	// The batch lands under the job staging namespace — rclone NEVER
	// receives a live destination name (§28.6, K-XFER-014). Each name
	// then commits natively with conditional creation.
	batchPrefix := e.stage + "batch/"
	for _, name := range names {
		e.report.StagedResidue = append(e.report.StagedResidue, batchPrefix+name)
	}
	var result *rclone.Result
	var err error
	for attempt := 0; ; attempt++ {
		result, err = e.cfg.Engine.Copy(ctx, e.enginePath(e.cfg.SrcSpec, e.cfg.SrcPrefix),
			e.enginePath(e.cfg.DstSpec, batchPrefix), names, e.captureEvents)
		if result != nil {
			e.capturePrefixes(result.SegmentPrefixes)
		}
		if err != nil {
			for _, name := range names {
				e.fail(name, "engine-copy", err)
			}
			return nil
		}
		failure := ResultFailure(result)
		if failure == nil {
			break
		}
		// One fresh child for an auth-shaped failure: tokens are
		// force-invalidated so the next spawn AUTHENTICATES, never
		// replays the rejected token (K-XFER-001, review F-06).
		if attempt == 0 && IsAuthFailure(result) && ctx.Err() == nil {
			if e.cfg.OnAuthFailure != nil {
				e.cfg.OnAuthFailure()
			}
			continue
		}
		for _, name := range names {
			e.fail(name, "engine-status", failure)
		}
		return ctx.Err()
	}
	for _, name := range names {
		if err := ctx.Err(); err != nil {
			return err
		}
		e.commitStaged(ctx, plan.Item{Name: name}, batchPrefix+name)
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
	// Streamed capture: the fact is durable at OBSERVATION time, so a
	// process that dies mid-child still knows its generated prefixes.
	if p, ok := rclone.PrefixFromEvent(event); ok {
		e.capturePrefix(p)
	}
	if e.cfg.OnEvent != nil {
		e.cfg.OnEvent(event)
	}
}

func (e *Executor) capturePrefix(p rclone.SegmentPrefix) {
	p = p.Canonical()
	if e.printed[p] {
		return
	}
	e.printed[p] = true
	e.report.CapturedPrefixes = append(e.report.CapturedPrefixes, p)
	if e.cfg.OnRecovery != nil {
		if err := e.cfg.OnRecovery("captured", p.Container, p.Prefix, ""); err != nil && e.recoveryBroken == nil {
			e.recoveryBroken = fmt.Errorf("captured-prefix record: %w", err)
		}
	}
}

func (e *Executor) capturePrefixes(prefixes []rclone.SegmentPrefix) {
	for _, p := range prefixes {
		e.capturePrefix(p)
	}
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

// Native fallback preserves names the engine cannot carry. Conditional
// creation protects a destination that appeared after the advisory plan.
func (e *Executor) nativeCopy(ctx context.Context, item plan.Item) {
	srcName := e.cfg.SrcPrefix + item.Name
	dstName := e.cfg.DstPrefix + item.Name

	body, src, err := e.cfg.Source.OpenObject(ctx, e.cfg.SrcContainer, srcName)
	if err != nil {
		e.fail(item.Name, "native-get", err)
		return
	}
	defer body.Close()
	if src.FollowedSymlink {
		e.copySymlink(ctx, item, srcName, dstName)
		return
	}
	if src.ContentLength > maxSinglePut {
		e.fail(item.Name, "native-size", fmt.Errorf(
			"logical size %d exceeds the single-PUT limit and the name cannot ride the engine (v1 limitation)", src.ContentLength))
		return
	}
	headers := metadataHeaders(src, "X-Object-Meta-")
	headers["X-Object-Meta-"+fingerprintMetaKey] = fingerprint(src.ContentLength, src.ETag)
	headers["If-None-Match"] = "*"
	if _, err := e.cfg.Dest.PutObject(ctx, e.cfg.DstContainer, dstName, body, src.ContentType, headers); err != nil {
		if swiftcontrol.IsPreconditionFailed(err) {
			e.report.Conflicts = append(e.report.Conflicts, item.Name)
			e.emit(item.Name, "conflict", "destination appeared at commit time")
			return
		}
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
	e.emit(item.Name, "copied", "native")
}

// Refusal is an execution decision, separate from copied-object errors.
// Persist its exact relative identity before allowing planning to continue.
func (e *Executor) refuseOverwrite(item plan.Item) error {
	e.report.OverwritesRefused++
	e.emit(item.Name, "overwrite-refused", "overwriting existing destinations is unavailable while concurrent changes cannot be protected")
	return e.refusals.append(item.Name)
}

// Close removes only this executor's private local refusal record.
// It must run after verification, including on cancellation and failures.
func (e *Executor) Close() error {
	return e.refusals.close()
}

// Staged bytes must verify before becoming visible at a new live name.
// Every commit uses conditional PUT: Swift conditions server-side COPY
// against its source, which cannot protect a destination's competing writer.
func (e *Executor) commitStaged(ctx context.Context, item plan.Item, stagedName string) {
	srcName := e.cfg.SrcPrefix + item.Name
	dstName := e.cfg.DstPrefix + item.Name

	if e.recoveryBroken != nil {
		// A safety fact failed to persist: nothing commits past a
		// boundary the crash sweep could not replay.
		e.fail(item.Name, "recovery-store", e.recoveryBroken)
		return
	}

	src, err := e.cfg.Source.ObjectDetail(ctx, e.cfg.SrcContainer, srcName)
	if err != nil {
		e.fail(item.Name, "stage-head-source", err)
		return
	}
	if src.FollowedSymlink {
		// The source is a Swift symlink: the staged bytes are the
		// followed TARGET's — faithful §17.1 semantics copy the LINK.
		e.copySymlink(ctx, item, srcName, e.cfg.DstPrefix+item.Name)
		e.cleanStaged(ctx, stagedName)
		return
	}
	staged, err := e.cfg.Dest.ObjectDetail(ctx, e.cfg.DstContainer, stagedName)
	if err != nil {
		if swiftcontrol.IsNotFound(err) {
			e.fail(item.Name, "stage-verify", errors.New("engine reported success but the staged object is missing (silent drop)"))
			return
		}
		e.fail(item.Name, "stage-verify", fmt.Errorf("staged object missing: %w", err))
		return
	}
	if err := verifyCopied(src, staged); err != nil {
		e.fail(item.Name, "stage-verify", err)
		e.cleanStaged(ctx, stagedName)
		return
	}

	if staged.Class == swiftcontrol.ClassDLO {
		// K-CON-001: segment-listing completeness with bounded retries
		// BEFORE the commit — a truncated upload whose manifest HEAD
		// happens to sum right transiently must not become live.
		if err := e.verifyStagedSegments(ctx, staged, src.ContentLength); err != nil {
			e.fail(item.Name, "stage-segments", err)
			return
		}
	}

	_, err = e.cfg.Dest.ObjectDetail(ctx, e.cfg.DstContainer, dstName)
	if err != nil && !swiftcontrol.IsNotFound(err) {
		e.fail(item.Name, "commit-rehead", err)
		return
	}
	if err == nil {
		e.report.Conflicts = append(e.report.Conflicts, item.Name)
		e.emit(item.Name, "conflict", "destination appeared since planning")
		e.cleanStaged(ctx, stagedName)
		return
	}

	headers := metadataHeaders(src, "X-Object-Meta-")
	headers["If-None-Match"] = "*"
	headers["X-Object-Meta-"+fingerprintMetaKey] = fingerprint(src.ContentLength, src.ETag)
	if staged.Class == swiftcontrol.ClassDLO {
		// The durable COMMITTING intent rides ahead of the live PUT: it
		// binds the segment prefix to the LIVE identity, so a crash in
		// the gap resolves from the live name's actual manifest instead
		// of guesswork. No intent, no PUT.
		if slash := strings.Index(staged.Manifest, "/"); slash > 0 && e.cfg.OnRecovery != nil {
			intent := rclone.SegmentPrefix{Container: staged.Manifest[:slash], Prefix: staged.Manifest[slash+1:]}.Canonical()
			if err := e.cfg.OnRecovery("committing", intent.Container, intent.Prefix,
				e.cfg.DstContainer+"/"+dstName); err != nil {
				e.fail(item.Name, "commit-intent", err)
				e.cleanStaged(ctx, stagedName)
				return
			}
		}
		// Manifest-swap PUT: the live name becomes a DLO pointing at the
		// staged segments in one atomic write (validated on real Swift).
		headers["X-Object-Manifest"] = staged.Manifest
		if _, err := e.cfg.Dest.PutObject(ctx, e.cfg.DstContainer, dstName, strings.NewReader(""), src.ContentType, headers); err != nil {
			if swiftcontrol.IsPreconditionFailed(err) {
				e.report.Conflicts = append(e.report.Conflicts, item.Name)
				e.emit(item.Name, "conflict", "destination appeared at commit time")
				e.cleanStaged(ctx, stagedName)
				return
			}
			e.fail(item.Name, "commit-manifest", err)
			return
		}
		e.markCommitted(staged.Manifest)
	} else {
		// Planned-new plain commit: server-side COPY cannot be
		// destination-conditioned (Swift evaluates If-None-Match on the
		// COPY SOURCE — measured, 304), so a concurrent writer between
		// the re-HEAD and an unconditional COPY would be overwritten
		// (review R-01). Stream the staged bytes through a CONDITIONAL
		// live PUT instead: costs one dest-internal GET+PUT, but a 412
		// means the racer won and we CONFLICT rather than clobber.
		body, _, err := e.cfg.Dest.OpenObject(ctx, e.cfg.DstContainer, stagedName)
		if err != nil {
			e.fail(item.Name, "commit-open-staged", err)
			return
		}
		putHeaders := map[string]string{"If-None-Match": "*"}
		for k, v := range headers {
			putHeaders[k] = v
		}
		_, putErr := e.cfg.Dest.PutObject(ctx, e.cfg.DstContainer, dstName, body, staged.ContentType, putHeaders)
		body.Close()
		if putErr != nil {
			if swiftcontrol.IsPreconditionFailed(putErr) {
				e.report.Conflicts = append(e.report.Conflicts, item.Name)
				e.emit(item.Name, "conflict", "destination appeared at commit time")
				e.cleanStaged(ctx, stagedName)
				return
			}
			e.fail(item.Name, "commit-put", putErr)
			return
		}
	}

	committed, err := e.cfg.Dest.ObjectDetail(ctx, e.cfg.DstContainer, dstName)
	if err != nil {
		e.fail(item.Name, "commit-verify", err)
		return
	}
	if err := verifyCopied(src, committed); err != nil {
		e.fail(item.Name, "commit-verify", err)
		return
	}

	e.cleanStaged(ctx, stagedName)
	e.report.Copied++
	e.report.BytesCopied += src.ContentLength
	e.emit(item.Name, "copied", "stage-and-commit")
}

// markCommitted records that a captured segment prefix is now referenced
// by a committed manifest ("container/prefix"), so reconciliation never
// sweeps it.
func (e *Executor) markCommitted(manifest string) {
	if slash := strings.Index(manifest, "/"); slash > 0 {
		p := rclone.SegmentPrefix{Container: manifest[:slash], Prefix: manifest[slash+1:]}.Canonical()
		if !e.printed[p] {
			// Never promote a manifest this job did not OBSERVE its own
			// child create: an unobserved prefix is not ours to own
			// (§28.6(b) — the review wave's H-01).
			return
		}
		e.report.CommittedPrefixes = append(e.report.CommittedPrefixes, p)
		if e.cfg.OnRecovery != nil {
			// Best-effort: the committing record already guards this
			// boundary (the sweep re-HEADs the live name it carries) —
			// but the key it writes is the CANONICAL one.
			if err := e.cfg.OnRecovery("committed", p.Container, p.Prefix, ""); err != nil && e.recoveryBroken == nil {
				e.recoveryBroken = fmt.Errorf("committed record: %w", err)
			}
		}
	}
}

func (e *Executor) cleanStaged(ctx context.Context, stagedName string) {
	if err := e.cfg.Dest.DeleteObject(ctx, e.cfg.DstContainer, stagedName); err != nil && !swiftcontrol.IsNotFound(err) {
		return // Keep the unresolved name for recovery; do not retry deletion.
	}
	for i, name := range e.report.StagedResidue {
		if name == stagedName {
			e.report.StagedResidue = append(e.report.StagedResidue[:i], e.report.StagedResidue[i+1:]...)
			return
		}
	}
}

// verifyStagedSegments lists the staged DLO's generated prefix and
// requires the summed segment bytes to equal the source's logical size,
// with bounded retries for listing consistency (§15.3, K-CON-001).
func (e *Executor) verifyStagedSegments(ctx context.Context, staged *swiftcontrol.ObjectDetail, want int64) error {
	slash := strings.Index(staged.Manifest, "/")
	if slash <= 0 {
		return fmt.Errorf("staged DLO carries malformed manifest %q", staged.Manifest)
	}
	container, prefix := staged.Manifest[:slash], staged.Manifest[slash+1:]
	var sum int64
	var count int
	for attempt := 0; attempt < 4; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(500 * time.Millisecond):
			}
		}
		sum, count = 0, 0
		for entry, err := range e.cfg.Dest.Objects(ctx, container, swiftcontrol.ObjectListOptions{Deep: true, Prefix: prefix}) {
			if err != nil {
				return fmt.Errorf("staged segment listing: %w", err)
			}
			if object, ok := entry.(swiftcontrol.ObjectEntry); ok {
				sum += object.Bytes
				count++
			}
		}
		if sum == want {
			return nil
		}
	}
	return fmt.Errorf("staged segments sum to %d bytes over %d objects, source is %d — listing incomplete or upload truncated", sum, count, want)
}

// copySymlink carries a Swift symlink AS a symlink (§17.1's
// carry-when-present row): the link object's own target headers and
// user metadata land at the destination in one empty-body PUT; the
// target itself moves (or not) as its own listed name. A dangling
// destination link is legal Swift and reported honestly.
func (e *Executor) copySymlink(ctx context.Context, item plan.Item, srcName, dstName string) {
	link, err := e.cfg.Source.SymlinkGet(ctx, e.cfg.SrcContainer, srcName)
	if err != nil {
		e.fail(item.Name, "symlink-head", err)
		return
	}
	if link.SymlinkTarget == "" {
		e.fail(item.Name, "symlink-head", errors.New("followed a symlink but ?symlink=get shows no target"))
		return
	}
	_, err = e.cfg.Dest.ObjectDetail(ctx, e.cfg.DstContainer, dstName)
	if err != nil && !swiftcontrol.IsNotFound(err) {
		e.fail(item.Name, "symlink-rehead", err)
		return
	}
	if err == nil {
		e.report.Conflicts = append(e.report.Conflicts, item.Name)
		e.emit(item.Name, "conflict", "destination appeared since planning")
		return
	}

	// No leading slash: real Swift silently stores a plain object when
	// the target carries one (measured on SAIO — the mock mimics it).
	headers := map[string]string{"X-Symlink-Target": link.SymlinkTarget}
	headers["If-None-Match"] = "*"
	if link.SymlinkTargetAccount != "" {
		headers["X-Symlink-Target-Account"] = link.SymlinkTargetAccount
	}
	for k, v := range link.Meta {
		if k != fingerprintMetaKey {
			headers["X-Object-Meta-"+k] = v
		}
	}
	if _, err := e.cfg.Dest.PutObject(ctx, e.cfg.DstContainer, dstName, strings.NewReader(""), link.ContentType, headers); err != nil {
		if swiftcontrol.IsPreconditionFailed(err) {
			e.report.Conflicts = append(e.report.Conflicts, item.Name)
			e.emit(item.Name, "conflict", "destination appeared at commit time")
			return
		}
		e.fail(item.Name, "symlink-put", err)
		return
	}
	written, err := e.cfg.Dest.SymlinkGet(ctx, e.cfg.DstContainer, dstName)
	if err != nil || written.SymlinkTarget != link.SymlinkTarget {
		e.fail(item.Name, "symlink-verify", fmt.Errorf("destination link does not carry the target (%v)", err))
		return
	}
	e.report.Copied++
	e.emit(item.Name, "copied", "symlink")
}
