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

// stagedAggregateMetaKey records the aggregate identity observed on the
// staged manifest just before publication, stored as
// X-Object-Meta-Aeronautes-Staged-Aggregate and carried by the same
// conditional PUT that publishes the name. It identifies THAT earlier
// staged observation: it is not the live object's identity, it does not
// freeze segment membership, and it says nothing about whether the
// staged bytes were ever verified against the source.
const stagedAggregateMetaKey = "Aeronautes-Staged-Aggregate"

func aggregateStamp(logicalBytes int64, etag string) string {
	return fmt.Sprintf("v1:%d:%s", logicalBytes, strings.Trim(etag, `"`))
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
	OnItem   func(ItemOutcome)
	Recorder *Recorder
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
	stopped        error
}

// New validates the config and builds an executor.
func New(cfg Config) (*Executor, error) {
	if cfg.Recorder != nil && (cfg.Recorder.Admit == nil || cfg.Recorder.Execute == nil || cfg.Recorder.Verify == nil) {
		return nil, errors.New("item recording needs admission, execution and verification acknowledgements")
	}
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
	if e.stopped != nil {
		return e.stopped
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if item.Verdict == plan.VerdictDestOnly {
		return nil
	}
	if err := e.admitItem(item); err != nil {
		return err
	}
	switch item.Verdict {
	case plan.VerdictSame:
		if e.cfg.Recorder != nil {
			return e.stop(e.cfg.Recorder.Execute(item.Name, Observation{Result: ResultSame, Effect: EffectNotAttempted}))
		}
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
	return e.report, errors.Join(err, e.stopped, e.recoveryBroken, e.refusals.broken)
}

// Stop prevents a later Finish from dispatching buffered work after planning
// fails. Already admitted, unstarted items can still save their final facts.
func (e *Executor) Stop(cause error) {
	e.stop(cause)
}

// Run accepts the same strictly name-ordered items as plan.Copy so
// execution decisions can be checked without an in-memory namespace.
func (e *Executor) Run(ctx context.Context, items []plan.Item) (*Report, error) {
	for _, item := range items {
		if err := e.Handle(ctx, item); err != nil {
			e.Stop(err)
			return e.Finish(ctx)
		}
	}
	return e.Finish(ctx)
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
		return e.observe(item.Name, failed(EffectNotAttempted, StageSourceRead, CodeUnknown, "resolve-head-source", err))
	}
	dst, err := e.cfg.Dest.ObjectDetail(ctx, e.cfg.DstContainer, e.cfg.DstPrefix+item.Name)
	if err != nil {
		if swiftcontrol.IsNotFound(err) {
			item.SourceBytes = src.ContentLength
			return e.runNew(ctx, item)
		}
		return e.observe(item.Name, failed(EffectNotAttempted, StagePlanning, CodeUnknown, "resolve-head-dest", err))
	}
	if provenEqual(src, dst) {
		return e.observe(item.Name, executionResult{Observation: Observation{Result: ResultSame, Effect: EffectNotAttempted}})
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
	// Every condition that admitted a non-plain skip before still has to
	// hold; the aggregate check below only ever removes members from that
	// set, so nothing refused today becomes skipped.
	want := fingerprint(src.ContentLength, src.ETag)
	if dst.Meta[fingerprintMetaKey] != want || dst.ContentLength != src.ContentLength {
		return false
	}
	switch dst.Class {
	case swiftcontrol.ClassDLO:
		// A destination whose segments were replaced by different bytes
		// of the same length leaves both of those facts true, so require
		// the live aggregate to still match what this executor observed
		// when it published the name. An absent or malformed stamp is not
		// evidence, and a stale segment listing can still hide a
		// substitution, so a match says only that no difference was
		// observed, never that the destination equals the source.
		stamp := dst.Meta[stagedAggregateMetaKey]
		return stamp != "" && stamp == aggregateStamp(dst.ContentLength, dst.ETag)
	case swiftcontrol.ClassSLO:
		// A static large object reports the entity tag its manifest
		// recorded, so it carries no live aggregate to compare. Refuse
		// rather than reason about it.
		return false
	default:
		// Anything else, including a large source materialised as a plain
		// destination, keeps the decision it had before this guard: the
		// fingerprint and the logical length. That case has no aggregate
		// of its own to check and is recorded as still open.
		return true
	}
}

// runNew copies a name with no live destination: engine-routed names
// batch toward the staging namespace; native-routed names stream
// through the client immediately.
func (e *Executor) runNew(ctx context.Context, item plan.Item) error {
	if item.Route == plan.RouteNative {
		e.report.NativeRouted++
		return e.observe(item.Name, e.nativeCopy(ctx, item))
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
	if err := errors.Join(e.stopped, ctx.Err()); err != nil {
		names := e.batch
		e.batch = nil
		e.batchBytes = 0
		return e.abandonBatch(names, err)
	}
	names := e.batch
	e.batch = nil
	e.batchBytes = 0
	if e.cfg.Engine == nil {
		for _, name := range names {
			if err := e.observe(name, failed(EffectNotAttempted, StageEngine, CodeUnsupported, "engine", errors.New("no engine configured"))); err != nil {
				return err
			}
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
		if e.recoveryBroken != nil {
			if result != nil {
				err = errors.Join(err, ResultFailure(result))
			}
			return e.stop(errors.Join(err, e.recoveryBroken))
		}
		if err != nil {
			for _, name := range names {
				if recordErr := e.observe(name, failed(EffectUnresolved, StageEngine, CodeUnknown, "engine-copy", err)); recordErr != nil {
					return errors.Join(err, recordErr)
				}
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
			code := CodeUnknown
			if result != nil && result.Cancelled {
				code = CodeCancelled
			} else if result != nil && result.LogStreamErr != "" {
				code = CodeIncomplete
			}
			if err := e.observe(name, failed(EffectUnresolved, StageEngine, code, "engine-status", failure)); err != nil {
				return errors.Join(failure, err)
			}
		}
		return ctx.Err()
	}
	for _, name := range names {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := e.observe(name, e.commitStaged(ctx, plan.Item{Name: name}, batchPrefix+name)); err != nil {
			return err
		}
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
		if k == fingerprintMetaKey || k == stagedAggregateMetaKey {
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
func (e *Executor) nativeCopy(ctx context.Context, item plan.Item) (result executionResult) {
	srcName := e.cfg.SrcPrefix + item.Name
	dstName := e.cfg.DstPrefix + item.Name

	body, src, err := e.cfg.Source.OpenObject(ctx, e.cfg.SrcContainer, srcName)
	if err != nil {
		return failed(EffectNotAttempted, StageSourceRead, CodeUnknown, "native-get", err)
	}
	defer func() { result.addProblem(StageSourceRead, body.Close()) }()
	if src.FollowedSymlink {
		return e.copySymlink(ctx, srcName, dstName, EffectNotAttempted)
	}
	if src.ContentLength > maxSinglePut {
		return failed(EffectNotAttempted, StageSourceRead, CodeUnsupported, "native-size", fmt.Errorf(
			"logical size %d exceeds the single-PUT limit and the name cannot ride the engine (v1 limitation)", src.ContentLength))
	}
	headers := metadataHeaders(src, "X-Object-Meta-")
	headers["X-Object-Meta-"+fingerprintMetaKey] = fingerprint(src.ContentLength, src.ETag)
	headers["If-None-Match"] = "*"
	if _, err := e.cfg.Dest.PutObject(ctx, e.cfg.DstContainer, dstName, body, src.ContentType, headers); err != nil {
		if swiftcontrol.IsPreconditionFailed(err) {
			return conflict(EffectUnresolved, err, "destination appeared at commit time")
		}
		return failed(EffectUnresolved, StageCommit, CodeUnknown, "native-put", err)
	}
	dst, err := e.cfg.Dest.ObjectDetail(ctx, e.cfg.DstContainer, dstName)
	if err != nil {
		return failed(EffectUnresolved, StageVerification, CodeUnknown, "native-verify", err)
	}
	if err := verifyCopied(src, dst); err != nil {
		return failed(EffectUnresolved, StageVerification, CodeUnverified, "native-verify", err)
	}
	return copied(src.ContentLength, "native")
}

// Refusal is an execution decision, separate from copied-object errors.
// Persist its exact relative identity before allowing planning to continue.
func (e *Executor) refuseOverwrite(item plan.Item) error {
	result := executionResult{Observation: Observation{Result: ResultRefused, Effect: EffectNotAttempted,
		Diagnostic: ClassifyDiagnostic(StagePlanning, CodeUnsupported, nil)},
		detail: "overwriting existing destinations is unavailable while concurrent changes cannot be protected"}
	result.addProblem(StageRecording, e.refusals.append(item.Name))
	return e.observe(item.Name, result)
}

// Close removes only this executor's private local refusal record.
// It must run after verification, including on cancellation and failures.
func (e *Executor) Close() error {
	return e.refusals.close()
}

// Staged bytes must verify before becoming visible at a new live name.
// Every commit uses conditional PUT: Swift conditions server-side COPY
// against its source, which cannot protect a destination's competing writer.
func (e *Executor) commitStaged(ctx context.Context, item plan.Item, stagedName string) (result executionResult) {
	srcName := e.cfg.SrcPrefix + item.Name
	dstName := e.cfg.DstPrefix + item.Name

	if e.recoveryBroken != nil {
		// A safety fact failed to persist: nothing commits past a
		// boundary the crash sweep could not replay.
		result = failed(EffectUnresolved, StageRecording, CodeUnknown, "recovery-store", e.recoveryBroken)
		result.addProblem(StageRecording, e.recoveryBroken)
		return result
	}

	src, err := e.cfg.Source.ObjectDetail(ctx, e.cfg.SrcContainer, srcName)
	if err != nil {
		return failed(EffectUnresolved, StageSourceRead, CodeUnknown, "stage-head-source", err)
	}
	if src.FollowedSymlink {
		// The source is a Swift symlink: the staged bytes are the
		// followed TARGET's — faithful §17.1 semantics copy the LINK.
		result = e.copySymlink(ctx, srcName, dstName, EffectUnresolved)
		result.addProblem(StageCleanup, e.cleanStaged(ctx, stagedName))
		return result
	}
	staged, err := e.cfg.Dest.ObjectDetail(ctx, e.cfg.DstContainer, stagedName)
	if err != nil {
		if swiftcontrol.IsNotFound(err) {
			return failed(EffectUnresolved, StageStaging, CodeMissing, "stage-verify", errors.New("engine reported success but the staged object is missing (silent drop)"))
		}
		return failed(EffectUnresolved, StageStaging, CodeUnknown, "stage-verify", fmt.Errorf("staged object missing: %w", err))
	}
	if err := verifyCopied(src, staged); err != nil {
		result = failed(EffectUnresolved, StageStaging, CodeUnverified, "stage-verify", err)
		result.addProblem(StageCleanup, e.cleanStaged(ctx, stagedName))
		return result
	}

	if staged.Class == swiftcontrol.ClassDLO {
		// K-CON-001: segment-listing completeness with bounded retries
		// BEFORE the commit — a truncated upload whose manifest HEAD
		// happens to sum right transiently must not become live.
		if err := e.verifyStagedSegments(ctx, staged, src.ContentLength); err != nil {
			return failed(EffectUnresolved, StageStaging, CodeIncomplete, "stage-segments", err)
		}
	}

	_, err = e.cfg.Dest.ObjectDetail(ctx, e.cfg.DstContainer, dstName)
	if err != nil && !swiftcontrol.IsNotFound(err) {
		return failed(EffectUnresolved, StageCommit, CodeUnknown, "commit-rehead", err)
	}
	if err == nil {
		result = conflict(EffectUnresolved, nil, "destination appeared since planning")
		result.addProblem(StageCleanup, e.cleanStaged(ctx, stagedName))
		return result
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
				result = failed(EffectUnresolved, StageRecording, CodeUnknown, "commit-intent", err)
				result.addProblem(StageRecording, err)
				return result
			}
		}
		// Manifest-swap PUT: the live name becomes a DLO pointing at the
		// staged segments in one atomic write (validated on real Swift).
		// The staged aggregate rides in this same conditional write: a
		// value observed after it could not, and persisting one later
		// would mean another live mutation with its own authority,
		// concurrency and response-loss protocol.
		headers["X-Object-Meta-"+stagedAggregateMetaKey] = aggregateStamp(staged.ContentLength, staged.ETag)
		headers["X-Object-Manifest"] = staged.Manifest
		if _, err := e.cfg.Dest.PutObject(ctx, e.cfg.DstContainer, dstName, strings.NewReader(""), src.ContentType, headers); err != nil {
			if swiftcontrol.IsPreconditionFailed(err) {
				result = conflict(EffectUnresolved, err, "destination appeared at commit time")
				result.addProblem(StageCleanup, e.cleanStaged(ctx, stagedName))
				return result
			}
			return failed(EffectUnresolved, StageCommit, CodeUnknown, "commit-manifest", err)
		}
		if err := e.markCommitted(staged.Manifest); err != nil {
			// Verify the reported copy, but retain its staging and stop commits
			// when the post-PUT recovery fact could not be acknowledged.
			defer func() { result.addProblem(StageRecording, err) }()
		}
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
			return failed(EffectUnresolved, StageStaging, CodeUnknown, "commit-open-staged", err)
		}
		putHeaders := map[string]string{"If-None-Match": "*"}
		for k, v := range headers {
			putHeaders[k] = v
		}
		_, putErr := e.cfg.Dest.PutObject(ctx, e.cfg.DstContainer, dstName, body, staged.ContentType, putHeaders)
		closeErr := body.Close()
		defer func() { result.addProblem(StageStaging, closeErr) }()
		if putErr != nil {
			if swiftcontrol.IsPreconditionFailed(putErr) {
				result = conflict(EffectUnresolved, putErr, "destination appeared at commit time")
				result.addProblem(StageCleanup, e.cleanStaged(ctx, stagedName))
				return result
			}
			return failed(EffectUnresolved, StageCommit, CodeUnknown, "commit-put", putErr)
		}
	}

	committed, err := e.cfg.Dest.ObjectDetail(ctx, e.cfg.DstContainer, dstName)
	if err != nil {
		return failed(EffectUnresolved, StageVerification, CodeUnknown, "commit-verify", err)
	}
	if err := verifyCopied(src, committed); err != nil {
		return failed(EffectUnresolved, StageVerification, CodeUnverified, "commit-verify", err)
	}

	result = copied(src.ContentLength, "stage-and-commit")
	if e.recoveryBroken == nil {
		result.addProblem(StageCleanup, e.cleanStaged(ctx, stagedName))
	}
	return result
}

// markCommitted records that a captured segment prefix is now referenced
// by a committed manifest ("container/prefix"), so reconciliation never
// sweeps it.
func (e *Executor) markCommitted(manifest string) error {
	if slash := strings.Index(manifest, "/"); slash > 0 {
		p := rclone.SegmentPrefix{Container: manifest[:slash], Prefix: manifest[slash+1:]}.Canonical()
		if !e.printed[p] {
			// Never promote a manifest this job did not OBSERVE its own
			// child create: an unobserved prefix is not ours to own
			// (§28.6(b) — the review wave's H-01).
			return nil
		}
		e.report.CommittedPrefixes = append(e.report.CommittedPrefixes, p)
		if e.cfg.OnRecovery != nil {
			if err := e.cfg.OnRecovery("committed", p.Container, p.Prefix, ""); err != nil && e.recoveryBroken == nil {
				e.recoveryBroken = fmt.Errorf("committed record: %w", err)
			}
		}
	}
	return e.recoveryBroken
}

func (e *Executor) cleanStaged(ctx context.Context, stagedName string) error {
	if err := e.cfg.Dest.DeleteObject(ctx, e.cfg.DstContainer, stagedName); err != nil && !swiftcontrol.IsNotFound(err) {
		return err
	}
	for i, name := range e.report.StagedResidue {
		if name == stagedName {
			e.report.StagedResidue = append(e.report.StagedResidue[:i], e.report.StagedResidue[i+1:]...)
			return nil
		}
	}
	return nil
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
func (e *Executor) copySymlink(ctx context.Context, srcName, dstName string, priorEffect Effect) executionResult {
	link, err := e.cfg.Source.SymlinkGet(ctx, e.cfg.SrcContainer, srcName)
	if err != nil {
		return failed(priorEffect, StageSourceRead, CodeUnknown, "symlink-head", err)
	}
	if link.SymlinkTarget == "" {
		return failed(priorEffect, StageSourceRead, CodeIncomplete, "symlink-head", errors.New("followed a symlink but ?symlink=get shows no target"))
	}
	_, err = e.cfg.Dest.ObjectDetail(ctx, e.cfg.DstContainer, dstName)
	if err != nil && !swiftcontrol.IsNotFound(err) {
		return failed(priorEffect, StageCommit, CodeUnknown, "symlink-rehead", err)
	}
	if err == nil {
		return conflict(priorEffect, nil, "destination appeared since planning")
	}

	// No leading slash: real Swift silently stores a plain object when
	// the target carries one (measured on SAIO — the mock mimics it).
	headers := map[string]string{"X-Symlink-Target": link.SymlinkTarget}
	headers["If-None-Match"] = "*"
	if link.SymlinkTargetAccount != "" {
		headers["X-Symlink-Target-Account"] = link.SymlinkTargetAccount
	}
	for k, v := range link.Meta {
		if k != fingerprintMetaKey && k != stagedAggregateMetaKey {
			headers["X-Object-Meta-"+k] = v
		}
	}
	if _, err := e.cfg.Dest.PutObject(ctx, e.cfg.DstContainer, dstName, strings.NewReader(""), link.ContentType, headers); err != nil {
		if swiftcontrol.IsPreconditionFailed(err) {
			return conflict(EffectUnresolved, err, "destination appeared at commit time")
		}
		return failed(EffectUnresolved, StageCommit, CodeUnknown, "symlink-put", err)
	}
	written, err := e.cfg.Dest.SymlinkGet(ctx, e.cfg.DstContainer, dstName)
	if err != nil {
		return failed(EffectUnresolved, StageVerification, CodeUnknown, "symlink-verify", err)
	}
	if written.SymlinkTarget != link.SymlinkTarget {
		return failed(EffectUnresolved, StageVerification, CodeUnverified, "symlink-verify", errors.New("destination link does not carry the target"))
	}
	return copied(0, "symlink")
}
