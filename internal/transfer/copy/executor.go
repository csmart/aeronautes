// Package copy transfers planned-new objects through conditional native
// creation. Engine writes stay in a unique staging namespace because
// writing a live destination can erase another writer's bytes or shared
// segments. Existing changed destinations are refused under ADR 0020.
// Verification excludes the exact refused names while reconciliation
// retains uncertain data and recovery records.
package copy

import (
	"bytes"
	"context"
	"crypto/md5"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"maps"
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

// heldManifestBound is how much of a manifest source this route will hold in
// memory to digest it before publishing (ADR 0084 decision 3). It is the value
// this application already names for a per-transfer buffer, so an operator holds
// one number rather than two, and the executor runs its items one at a time, so
// the peak this adds is one bound rather than one per item.
const heldManifestBound = 16 * 1024 * 1024

// cappedHold accumulates what a comparison streams through it and refuses past
// its limit, so a source that serves more than its expectation stated fails the
// comparison instead of being held.
type cappedHold struct {
	bytes []byte
	limit int64
}

func (h *cappedHold) Write(p []byte) (int, error) {
	if int64(len(h.bytes))+int64(len(p)) > h.limit {
		return 0, fmt.Errorf("the source served more than the %d-byte hold this route allows", h.limit)
	}
	h.bytes = append(h.bytes, p...)
	return len(p), nil
}

// readCloser joins a reader to a closer so a comparison can read through a tee
// while still closing the body it came from.
type readCloser struct {
	io.Reader
	io.Closer
}

// closeOnce makes a body's Close happen once however many holders call it, so a
// comparison that must close the body itself and a defer that must close it when
// no comparison ran can both hold the same body.
func closeOnce(body io.ReadCloser) io.Closer {
	closed := false
	return closerFunc(func() error {
		if closed {
			return nil
		}
		closed = true
		return body.Close()
	})
}

type closerFunc func() error

func (f closerFunc) Close() error { return f() }

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

// validAggregate reports whether an observed dynamic large object
// entity tag is shaped like the aggregate the store computes: the
// complete-listing MD5, sixteen bytes as lower-case hex. Anything else,
// including an empty value, is not an observation this executor will
// treat as evidence or mint a stamp from.
func validAggregate(etag string) bool {
	etag = strings.Trim(etag, `"`)
	if len(etag) != 32 {
		return false
	}
	for _, r := range etag {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return false
		}
	}
	return true
}

// metaBudget is the destination's advertised user-metadata allowance,
// read once per run. A zero field means the cluster advertised no such
// limit, and known records that the lookup already happened.
type metaBudget struct {
	count       int
	overall     int
	nameLength  int
	valueLength int
	known       bool
}

// complete reports whether every dimension the store enforces was
// advertised. A partly advertised allowance is not a budget: treating
// the missing dimension as unlimited is how optional evidence breaks a
// copy, so an incomplete answer means the stamp is simply omitted.
func (b metaBudget) complete() bool {
	return b.count > 0 && b.overall > 0 && b.nameLength > 0 && b.valueLength > 0
}

// metaBudgetFor reads the destination's advertised metadata allowance
// once per executor. An unreadable /info leaves the budget unknown.
func (e *Executor) metaBudgetFor(ctx context.Context) metaBudget {
	if e.budget.known {
		return e.budget
	}
	e.budget.known = true
	capabilities, err := e.cfg.Dest.FetchCapabilities(ctx)
	if err == nil && capabilities != nil {
		e.budget.count = capabilities.MaxMetaCount()
		e.budget.overall = capabilities.MaxMetaOverallSize()
		e.budget.nameLength = capabilities.MaxMetaNameLength()
		e.budget.valueLength = capabilities.MaxMetaValueLength()
	}
	return e.budget
}

// stampFits reports whether one more user metadata entry fits inside
// what the destination advertises, counting the complete added entry.
// The stamp is optional evidence: it must never displace preserved user
// metadata (§27.4) nor break an otherwise valid copy, so an unknown or
// exhausted allowance simply publishes without it, and the destination
// is then refused rather than skipped on a later decision.
func stampFits(headers map[string]string, key, value string, budget metaBudget) bool {
	if !budget.complete() {
		return false
	}
	if len(key) > budget.nameLength || len(value) > budget.valueLength {
		return false
	}
	entries, size := 0, 0
	for k, v := range headers {
		name, isMeta := strings.CutPrefix(k, "X-Object-Meta-")
		if !isMeta {
			continue
		}
		entries++
		size += len(name) + len(v)
	}
	return entries+1 <= budget.count && size+len(key)+len(value) <= budget.overall
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
	cfg      Config
	report   *Report
	stage    string // staging prefix inside DstContainer
	refusals refusalLog
	// refusalsDeferred holds the staged route's refusals, appended at flush out
	// of the name order the immediate route uses; verify merges both spools.
	refusalsDeferred refusalLog
	batch            []string
	batchBytes       int64
	printed          map[rclone.SegmentPrefix]bool
	// recoveryBroken latches a failed captured-fact persist: staged
	// bytes may keep landing, but nothing COMMITS past a boundary the
	// sweep could not replay (§65.9 item 5 fail-closed rule).
	recoveryBroken error
	stopped        error
	// budget caches the destination's advertised metadata allowance.
	budget metaBudget
	// evidence is what this job's native route observed about the names it
	// decided, written to a private sorted spool rather than held per name
	// (ADR 0045 for why it exists at all, K-PERF-001 for why it is not a map).
	evidence evidenceLog
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
		cfg.BatchSize = DefaultBatchNames
	}
	if cfg.BatchBytes <= 0 {
		cfg.BatchBytes = DefaultBatchBytes
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
		return e.resolveSame(ctx, item)
	case plan.VerdictUnknown:
		return e.runUnknown(ctx, item)
	case plan.VerdictNew:
		return e.runNew(ctx, item)
	case plan.VerdictChanged:
		return e.routeOverwrite(ctx, item)
	}
	return fmt.Errorf("unhandled verdict %q", item.Verdict)
}

// Finish flushes the pending engine batch and returns the report. The
// report is valid (and needed by Reconcile) even after an error or
// cancellation mid-run.
func (e *Executor) Finish(ctx context.Context) (*Report, error) {
	err := e.flushBatch(ctx)
	return e.report, errors.Join(err, e.stopped, e.recoveryBroken, e.refusals.broken, e.refusalsDeferred.broken)
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

// resolveSame confirms the advisory listing's equal verdict against both
// authoritative HEADs before skipping. A copy skips only a full-fidelity replica
// (ADR 0096), so a content-equal destination whose carried set or link differs,
// one that changed or vanished since the advisory plan, or one that cannot be
// read to confirm, is refused rather than skipped. A Same item is never offered
// for acknowledgement (ADR 0087 decisions 2 and 4), so it never replaces or
// creates and never needs admission: skip or refuse are its only outcomes, and
// the operator re-runs for the fresh verdict.
func (e *Executor) resolveSame(ctx context.Context, item plan.Item) error {
	src, err := e.cfg.Source.ObjectDetail(ctx, e.cfg.SrcContainer, e.cfg.SrcPrefix+item.Name)
	if err != nil {
		return e.observe(item.Name, e.refuseSame(item.Name, StageSourceRead, CodeUnknown, err,
			"the source counted equal could not be read to confirm a full-fidelity replica"))
	}
	dst, err := e.cfg.Dest.ObjectDetail(ctx, e.cfg.DstContainer, e.cfg.DstPrefix+item.Name)
	if err != nil {
		if swiftcontrol.IsNotFound(err) {
			return e.observe(item.Name, e.refuseSame(item.Name, StageCommit, CodeConflict, nil,
				"the destination the plan counted equal is gone, and a name the plan counted equal is not acknowledged for creation"))
		}
		return e.observe(item.Name, e.refuseSame(item.Name, StageCommit, CodeUnknown, err,
			"the destination the plan counted equal could not be read to confirm a full-fidelity replica"))
	}
	if skippableEqual(src, dst) {
		return e.observe(item.Name, executionResult{Observation: Observation{Result: ResultSame, Effect: EffectNotAttempted}})
	}
	return e.observe(item.Name, e.refuseSame(item.Name, StageCommit, CodeConflict, nil,
		"the destination the plan counted equal is not a full-fidelity replica: its content headers, expiry, metadata or link target differ from the source"))
}

// refuseSame records a Same item the executor declined to skip. A Same item is
// never admitted, so a refusal — not a failure or conflict — is its only non-skip
// outcome; the diagnostic carries why, and the name joins the immediate refusal
// spool in the ascending order the verify pass reads (ADR 0096 decision 5).
func (e *Executor) refuseSame(name string, stage Stage, code Code, cause error, detail string) executionResult {
	result := executionResult{Observation: Observation{Result: ResultRefused, Effect: EffectNotAttempted,
		Diagnostic: ClassifyDiagnostic(stage, code, cause)}, detail: detail}
	result.addProblem(StageRecording, e.refusals.append(name))
	return result
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
	if skippableEqual(src, dst) {
		return e.observe(item.Name, executionResult{Observation: Observation{Result: ResultSame, Effect: EffectNotAttempted}})
	}
	// The destination is live and not provably equal. An acknowledged overwrite
	// routes on to a replacement with its own fixed-point comparison; anything
	// else refuses, exactly as it did before overwrite existed.
	item.SourceBytes = src.ContentLength
	return e.routeOverwrite(ctx, item)
}

// provenEqual implements the executor-grade §28.4 test over two
// authoritative HEADs: plain pairs by size+ETag; anything manifest-y by
// the destination fingerprint (proof Aeronautes itself verified this
// copy of this content) AND live logical-length equality — a DLO whose
// backing segments changed or vanished re-sums differently on HEAD, so
// a stale fingerprint alone can no longer skip it (review H-04).
// isSymlink reports whether a HEAD describes a Swift symlink in either
// representation: a followed link carries Content-Location (FollowedSymlink), and
// a link that was not followed — a dangling or cross-account link — carries
// X-Symlink-Target (SymlinkTarget). A copy carries the link itself, so neither
// is a plain object to skip or copy by its bytes (§17.1, ADR 0096 decision 2).
func isSymlink(d *swiftcontrol.ObjectDetail) bool {
	return d.FollowedSymlink || d.SymlinkTarget != ""
}

// fidelityEqual reports whether the destination carries the same replica facts
// as the source beyond its bytes: the §27.4 set a copy applies on a transfer —
// content type, content encoding, content disposition, expiry and user metadata.
// metadataHeaders is the one definition of that set and already drops the
// reserved Aeronautes stamps the destination carries and the source does not, so
// comparing the two built sets is the fidelity test (P17, ADR 0096). The DLO
// manifest is not compared: §27.4 gives the destination its own.
func fidelityEqual(src, dst *swiftcontrol.ObjectDetail) bool {
	return maps.Equal(metadataHeaders(src, "X-Object-Meta-"), metadataHeaders(dst, "X-Object-Meta-"))
}

func provenEqual(src, dst *swiftcontrol.ObjectDetail) bool {
	if src.Class == swiftcontrol.ClassPlain && dst.Class == swiftcontrol.ClassPlain {
		return src.ContentLength == dst.ContentLength &&
			strings.Trim(src.ETag, `"`) == strings.Trim(dst.ETag, `"`)
	}
	want := fingerprint(src.ContentLength, src.ETag)
	return dst.Meta[fingerprintMetaKey] == want && dst.ContentLength == src.ContentLength
}

// skippableEqual decides whether an existing destination may be left
// alone instead of copied. It is strictly narrower than provenEqual:
// every condition that admitted a skip before still has to hold, and a
// segmented destination must additionally still match the aggregate
// this executor observed when it published the name.
//
// Only the skip decision is narrowed. Verifying what this job just wrote
// asks a different question with different evidence, so it keeps using
// provenEqual; otherwise a destination that legitimately carries no
// stamp would turn an ordinary copy into a verification failure.
func skippableEqual(src, dst *swiftcontrol.ObjectDetail) bool {
	// A symlink is carried AS a link (§17.1), never skipped on content, so it is
	// recognised in both HEAD representations: a followed link (Content-Location)
	// shows its target's content, and a link that was not followed — a dangling or
	// cross-account link — shows X-Symlink-Target. Either way it routes to the
	// symlink commit, which reads the link with ?symlink=get and compares it, so a
	// different target or account is never reported equivalent (ADR 0096 d2).
	if isSymlink(src) || isSymlink(dst) {
		return false
	}
	if !provenEqual(src, dst) {
		return false
	}
	// A skip must be a full-fidelity replica, not merely content-equal (P17, ADR
	// 0096 decision 1): the §27.4 carried set has to match too.
	if !fidelityEqual(src, dst) {
		return false
	}
	if src.Class == swiftcontrol.ClassPlain && dst.Class == swiftcontrol.ClassPlain {
		return true
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
		// The live observation has to look like an aggregate before it can
		// be compared: an unusable entity tag matched against a stamp
		// minted from the same unusable value would otherwise skip.
		if !validAggregate(dst.ETag) {
			return false
		}
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

// DefaultBatchNames and DefaultBatchBytes bound one engine child's
// batch: 500 names or 64 GiB of planned bytes, whichever comes first.
// The download route batches by the same values (ADR 0074 decision 3).
const (
	DefaultBatchNames = 500
	DefaultBatchBytes = int64(64 << 30)
)

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

// contentFailure records that this job observed a name's content disagree, so
// the verification pass can keep it out of a verified count, a Same verdict and
// a skip (ADR 0045, independently of any stamp). A spool write that fails is
// attached to the item rather than dropped: the pass refuses to run on evidence
// it could not store, and the item says why.
func (e *Executor) contentFailure(name string, result executionResult) executionResult {
	result.addProblem(StageRecording, e.evidence.contentFailure(name))
	return result
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
	// The source body is closed exactly once, by whichever of this defer and the
	// content comparison below reaches it first. The comparison has to close it
	// itself, because that is where a close failure still prevents a publication;
	// without the guard this defer would then close it a second time and report
	// the same failure twice.
	closeSource := closeOnce(body)
	defer func() { result.addProblem(StageSourceRead, closeSource.Close()) }()
	if isSymlink(src) {
		return e.copySymlink(ctx, item.Name, srcName, dstName, EffectNotAttempted)
	}
	if src.ContentLength > maxSinglePut {
		return failed(EffectNotAttempted, StageSourceRead, CodeUnsupported, "native-size", fmt.Errorf(
			"logical size %d exceeds the single-PUT limit and the name cannot ride the engine (v1 limitation)", src.ContentLength))
	}
	// ADR 0083 decision 1 declares a plain source's own entity tag, which for a
	// plain object IS the MD5 of its content, so the store refuses a body that
	// does not hash to it.
	//
	// A manifest source has no header describing its assembled bytes, so ADR 0084
	// compares the held content against the source's own LADDER first and declares
	// the digest of those verified bytes. Hashing whatever the GET delivered and
	// declaring that would bind the transport only: same-length bytes other than
	// the ones the manifest describes would hash to their own digest and be
	// accepted. The ladder is the independent expectation — a prefix listing for a
	// dynamic large object, the recorded manifest for a static one — and
	// CompareContent is the same driver the staged route uses, which also closes
	// the body and joins a close failure, so the close happens BEFORE anything is
	// published rather than after.
	declared := strings.Trim(src.ETag, `"`)
	if src.Class == swiftcontrol.ClassPlain {
		// Only a complete MD5 can be declared. Swift states one for every plain
		// object, so this is the shape of a source that is not what it says it
		// is; without the check the route would send the value anyway and the
		// store would refuse the PUT, reporting a commit failure for a source
		// that was never usable.
		if _, ok := runDigest(declared); !ok {
			return failed(EffectNotAttempted, StageSourceRead, CodeUnsupported, "native-etag", fmt.Errorf(
				"plain source states %q rather than a complete MD5, so its copy cannot be bound", src.ETag))
		}
	} else {
		ladder, err := e.sourceLadder(ctx, src)
		if err != nil {
			return failed(EffectNotAttempted, StageSourceRead, CodeUnsupported, "native-ladder", fmt.Errorf(
				"a %s source supplies no usable content expectation, so its copy cannot be bound (ADR 0084): %w", src.Class, err))
		}
		// The bound is checked against the EXPECTATION's total, not a header the
		// same stream could misreport.
		if ladder.bytes() > heldManifestBound {
			return failed(EffectNotAttempted, StageSourceRead, CodeUnsupported, "native-class", fmt.Errorf(
				"a %s source of %d logical bytes cannot be bound by this route: holding it to compare it would exceed the %d-byte bound (ADR 0084)",
				src.Class, ladder.bytes(), heldManifestBound))
		}
		held := &cappedHold{limit: heldManifestBound}
		code, err := CompareContent(ctx, readCloser{Reader: io.TeeReader(body, held), Closer: closeSource},
			Ladder{runs: ladder})
		if err != nil {
			return e.contentFailure(item.Name, failed(EffectNotAttempted, StageSourceRead, code, "native-hold", fmt.Errorf(
				"a %s source's content did not match its manifest, so nothing was published: %w", src.Class, err)))
		}
		sum := md5.Sum(held.bytes)
		declared = hex.EncodeToString(sum[:])
		body = io.NopCloser(bytes.NewReader(held.bytes))
	}
	// The native route publishes at the live name in a single request and has
	// no pre-PUT destination read of its own. An acknowledged replacement
	// therefore adds the fixed-point HEAD here — after the source content is
	// prepared and immediately before the live PUT, the point ADR 0085 keeps the
	// comparison — and gates an unconditional replace on it. A name the operator
	// did not acknowledge is a first copy: it skips the HEAD and keeps the
	// create-only publication below, so only an acknowledged name is allowed to
	// find a destination already there.
	overwrite := false
	shown, acknowledged, ackErr := e.acknowledged(item.Name)
	if ackErr != nil {
		return failed(EffectNotAttempted, StageCommit, CodeUnknown, "native-ack", ackErr)
	}
	if acknowledged {
		dstDetail, headErr := e.cfg.Dest.ObjectDetail(ctx, e.cfg.DstContainer, dstName)
		if headErr != nil && !swiftcontrol.IsNotFound(headErr) {
			return failed(EffectNotAttempted, StageCommit, CodeUnknown, "native-rehead", headErr)
		}
		replaces, stop := e.replaceAtFixedPoint(ctx, item.Name, shown, dstDetail, headErr == nil, EffectNotAttempted, "native")
		if stop != nil {
			return *stop
		}
		overwrite = replaces
	}
	// No fingerprint. The stamp asserts that the destination holds the source's
	// content, and this route publishes at the live name in ONE request, so a
	// stamp in that request is written before anything has been checked. ADR 0045
	// decided this route therefore publishes unstamped and does not add the stamp
	// afterwards either, because the only available metadata update is a
	// HEAD-preflighted replace-all POST that cannot be bound atomically to the
	// object just checked. Before this change the stamp made a DETECTED content
	// failure usable as success evidence: the fingerprint survived at the live
	// name, so the same job's verification pass credited it and a later job
	// skipped it as equal.
	headers := metadataHeaders(src, "X-Object-Meta-")
	headers["ETag"] = declared
	if !overwrite {
		headers["If-None-Match"] = "*"
	}
	if _, err := e.cfg.Dest.PutObject(ctx, e.cfg.DstContainer, dstName, body, src.ContentType, headers); err != nil {
		if swiftcontrol.IsPreconditionFailed(err) {
			return conflict(EffectUnresolved, err, "destination appeared at commit time")
		}
		if swiftcontrol.IsDeclaredDigestRefused(err) {
			// The store's own verdict that the body did not hash to what this
			// route declared. That is a content disagreement about this name,
			// stated by the only party that saw both, so ADR 0045's same-job
			// exclusion applies to it exactly as it does to the disagreements
			// this route detects for itself. Without the record a later equal
			// pair of listing rows would verify a name whose publication the
			// store refused. The result itself is unchanged: how a 422 is
			// classified is a separate question from whether it is remembered.
			return e.contentFailure(item.Name, failed(EffectUnresolved, StageCommit, CodeUnknown, "native-put", err))
		}
		return failed(EffectUnresolved, StageCommit, CodeUnknown, "native-put", err)
	}
	dst, err := e.cfg.Dest.ObjectDetail(ctx, e.cfg.DstContainer, dstName)
	if err != nil {
		return failed(EffectUnresolved, StageVerification, CodeUnknown, "native-verify", err)
	}
	// The declaration again, this time against what the store reports. It is the
	// weaker of the two checks — the bytes are already at the live name by now —
	// but it is the one that still works when something between this process and
	// the store removes the declared tag, which leaves the store nothing to
	// enforce. It also reaches a case verifyCopied cannot: this route always
	// creates a PLAIN destination, whose reported tag is the MD5 of what landed,
	// while a manifest source's own tag is an aggregate over its segments, so a
	// class-to-class comparison falls back to logical length alone and a
	// same-length difference passes it.
	if dst.Class == swiftcontrol.ClassPlain && strings.Trim(dst.ETag, `"`) != declared {
		return e.contentFailure(item.Name, failed(EffectUnresolved, StageVerification, CodeUnverified, "native-verify", fmt.Errorf(
			"destination hashes to %s, the copy declared %s", strings.Trim(dst.ETag, `"`), declared)))
	}
	if err := verifyCopied(src, dst); err != nil {
		return e.contentFailure(item.Name, failed(EffectUnresolved, StageVerification, CodeUnverified, "native-verify", err))
	}
	// Recorded only here, past every check. The source identity is kept beside
	// the destination digest because the identity is half the claim: the
	// verification pass reads the source again, and evidence that this content
	// was copied says nothing about a source that has changed since.
	result = copied(src.ContentLength, "native")
	result.overwrote = overwrite
	if src.Class != swiftcontrol.ClassPlain {
		// Recorded only for the case that needs it. A plain source's destination
		// is a plain object with the same entity tag, which the verification
		// pass settles from the two HEADs on its own, in this job and in any
		// later one. A manifest source's destination is a plain object whose tag
		// is not comparable to the source's aggregate, so without a stamp that
		// pass has nothing but what this job compared — which keeps the spool
		// proportional to manifest copies rather than to the namespace.
		//
		// The source identity is kept beside the destination digest because the
		// identity is half the claim: the pass reads the source again, and
		// evidence that this content was copied says nothing about a source that
		// has become different content of the same length since.
		result.addProblem(StageRecording, e.evidence.copied(item.Name, fingerprint(src.ContentLength, src.ETag), declared))
	}
	return result
}

// Refusal is an execution decision, separate from copied-object errors.
// Persist its exact relative identity before allowing planning to continue.
func (e *Executor) refuseOverwrite(item plan.Item) error {
	result := executionResult{Observation: Observation{Result: ResultRefused, Effect: EffectNotAttempted,
		Diagnostic: ClassifyDiagnostic(StagePlanning, CodeUnsupported, nil)},
		detail: "the destination already holds this name, and replacing it was not confirmed for this copy"}
	result.addProblem(StageRecording, e.refusals.append(item.Name))
	return e.observe(item.Name, result)
}

// Close removes only this executor's private local refusal record.
// It must run after verification, including on cancellation and failures.
func (e *Executor) Close() error {
	return errors.Join(e.refusals.close(), e.refusalsDeferred.close(), e.evidence.close())
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
	if isSymlink(src) {
		// The source is a Swift symlink in either HEAD representation: faithful
		// §17.1 semantics copy the LINK, never the followed target's bytes.
		return e.withStageCleanup(ctx, stagedName, e.copySymlink(ctx, item.Name, srcName, dstName, EffectUnresolved))
	}
	staged, err := e.cfg.Dest.ObjectDetail(ctx, e.cfg.DstContainer, stagedName)
	if err != nil {
		if swiftcontrol.IsNotFound(err) {
			return failed(EffectUnresolved, StageStaging, CodeMissing, "stage-verify", errors.New("engine reported success but the staged object is missing (silent drop)"))
		}
		return failed(EffectUnresolved, StageStaging, CodeUnknown, "stage-verify", fmt.Errorf("staged object missing: %w", err))
	}
	if err := verifyCopied(src, staged); err != nil {
		return e.withStageCleanup(ctx, stagedName, failed(EffectUnresolved, StageStaging, CodeUnverified, "stage-verify", err))
	}

	if staged.Class == swiftcontrol.ClassDLO {
		// K-CON-001: segment-listing completeness with bounded retries
		// BEFORE the commit — a truncated upload whose manifest HEAD
		// happens to sum right transiently must not become live.
		if err := e.verifyStagedSegments(ctx, staged, src.ContentLength); err != nil {
			return failed(EffectUnresolved, StageStaging, CodeIncomplete, "stage-segments", err)
		}
	}
	// ADR 0042: the source's own content ladder is the expectation every
	// staged object answers before it becomes live. A source that states
	// no usable ladder refuses rather than publishes on length alone.
	//
	// Two plain objects are the one pair this does not read. Their entity
	// tags come from their own object HEADs, which state stored content
	// rather than container metadata, and verifyCopied has already
	// compared them. Every other pair is answered by reading the staged
	// bytes: a segment listing and the aggregate a manifest HEAD derives
	// from it are both container metadata, and metadata that has not
	// caught up with a same-size replacement agrees with the source while
	// the bytes underneath do not.
	if src.Class != swiftcontrol.ClassPlain || staged.Class != swiftcontrol.ClassPlain {
		ladder, err := e.sourceLadder(ctx, src)
		if err != nil {
			return failed(EffectUnresolved, StageStaging, CodeIncomplete, "stage-ladder", err)
		}
		if code, err := e.verifyStagedLadder(ctx, ladder, stagedName); err != nil {
			return failed(EffectUnresolved, StageStaging, code, "stage-content", err)
		}
	}

	// Each shape finishes its preparation BEFORE the fixed-point comparison, so
	// no store round trip sits between the last reading of the destination and
	// the publication that replaces it (ADR 0087 decision 3, ADR 0085). A racer
	// that lands during this preparation is then caught by the comparison below.
	var stagedBody io.ReadCloser
	if staged.Class == swiftcontrol.ClassDLO {
		// metaBudgetFor can FetchCapabilities, a request; read it now so the
		// aggregate-stamp decision below is a cache hit. It is memoised and read
		// once per run, so reading it early changes nothing it decides.
		e.metaBudgetFor(ctx)
	} else {
		var openErr error
		stagedBody, _, openErr = e.cfg.Dest.OpenObject(ctx, e.cfg.DstContainer, stagedName)
		if openErr != nil {
			return failed(EffectUnresolved, StageStaging, CodeUnknown, "commit-open-staged", openErr)
		}
		closeStaged := closeOnce(stagedBody)
		defer func() { result.addProblem(StageStaging, closeStaged.Close()) }()
	}

	dstDetail, err := e.cfg.Dest.ObjectDetail(ctx, e.cfg.DstContainer, dstName)
	if err != nil && !swiftcontrol.IsNotFound(err) {
		return failed(EffectUnresolved, StageCommit, CodeUnknown, "commit-rehead", err)
	}
	// The destination is read at the fixed point, the one ADR 0085 keeps the
	// comparison at: after all preparation and immediately before the
	// publishing request. A live name the operator did not acknowledge
	// appeared since planning and conflicts rather than clobbers; an
	// acknowledged one is compared against what they were shown.
	shown, acknowledged, ackErr := e.acknowledged(item.Name)
	if ackErr != nil {
		return e.withStageCleanup(ctx, stagedName, failed(EffectUnresolved, StageCommit, CodeUnknown, "commit-ack", ackErr))
	}
	overwrite := false
	switch {
	case err == nil && !acknowledged:
		return e.withStageCleanup(ctx, stagedName, conflict(EffectUnresolved, nil, "destination appeared since planning"))
	case acknowledged:
		replaces, stop := e.replaceAtFixedPoint(ctx, item.Name, shown, dstDetail, err == nil, EffectUnresolved, "commit")
		if stop != nil {
			return e.withStageCleanup(ctx, stagedName, *stop)
		}
		overwrite = replaces
	}

	headers := metadataHeaders(src, "X-Object-Meta-")
	if !overwrite {
		headers["If-None-Match"] = "*"
	}
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
		// An unusable staged observation, or one that will not fit inside
		// the destination's advertised metadata allowance, mints no
		// stamp: the name still publishes with every preserved user
		// value, and the unstamped destination is refused on a later
		// decision rather than skipped on bad evidence. Omitting the
		// header keeps this the same single conditional write, and never
		// trades a working copy for optional evidence.
		if stamp := aggregateStamp(staged.ContentLength, staged.ETag); validAggregate(staged.ETag) &&
			stampFits(headers, stagedAggregateMetaKey, stamp, e.metaBudgetFor(ctx)) {
			headers["X-Object-Meta-"+stagedAggregateMetaKey] = stamp
		}
		headers["X-Object-Manifest"] = staged.Manifest
		if _, err := e.cfg.Dest.PutObject(ctx, e.cfg.DstContainer, dstName, strings.NewReader(""), src.ContentType, headers); err != nil {
			if swiftcontrol.IsPreconditionFailed(err) {
				return e.withStageCleanup(ctx, stagedName, conflict(EffectUnresolved, err, "destination appeared at commit time"))
			}
			return failed(EffectUnresolved, StageCommit, CodeUnknown, "commit-manifest", err)
		}
		if err := e.markCommitted(staged.Manifest); err != nil {
			// Verify the reported copy, but retain its staging and stop commits
			// when the post-PUT recovery fact could not be acknowledged.
			defer func() { result.addProblem(StageRecording, err) }()
		}
	} else {
		// Plain commit: server-side COPY cannot be destination-conditioned
		// (Swift evaluates If-None-Match on the COPY SOURCE — measured, 304),
		// so a concurrent writer between the re-HEAD and an unconditional COPY
		// would be overwritten (review R-01). Stream the staged bytes through a
		// live PUT instead, carrying whatever condition headers already holds:
		// create-only for a planned-new name, so a 412 means the racer won and
		// we CONFLICT rather than clobber; and, for an acknowledged overwrite
		// that matched at the fixed point, unconditional, because the create
		// conditional would reject the very name the operator asked to replace.
		// The reader was opened before the comparison above, so nothing reads
		// the store between that comparison and this PUT; its deferred close
		// was registered where it was opened.
		_, putErr := e.cfg.Dest.PutObject(ctx, e.cfg.DstContainer, dstName, stagedBody, staged.ContentType, headers)
		if putErr != nil {
			if swiftcontrol.IsPreconditionFailed(putErr) {
				return e.withStageCleanup(ctx, stagedName, conflict(EffectUnresolved, putErr, "destination appeared at commit time"))
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
	result.overwrote = overwrite
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

// withStageCleanup removes the one staged object the commit was given and
// joins a failure to remove it to the result's problems, for a result that
// stops short of publishing from the stage or published a link in its place.
// The removal is an attempt: a staged object the store will not delete is
// reported, not retried.
func (e *Executor) withStageCleanup(ctx context.Context, stagedName string, result executionResult) executionResult {
	result.addProblem(StageCleanup, e.cleanStaged(ctx, stagedName))
	return result
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
func (e *Executor) copySymlink(ctx context.Context, name, srcName, dstName string, priorEffect Effect) executionResult {
	link, err := e.cfg.Source.SymlinkGet(ctx, e.cfg.SrcContainer, srcName)
	if err != nil {
		return failed(priorEffect, StageSourceRead, CodeUnknown, "symlink-head", err)
	}
	if link.SymlinkTarget == "" {
		return failed(priorEffect, StageSourceRead, CodeIncomplete, "symlink-head", errors.New("followed a symlink but ?symlink=get shows no target"))
	}
	dstDetail, err := e.cfg.Dest.ObjectDetail(ctx, e.cfg.DstContainer, dstName)
	if err != nil && !swiftcontrol.IsNotFound(err) {
		return failed(priorEffect, StageCommit, CodeUnknown, "symlink-rehead", err)
	}
	overwrite := false
	if err == nil {
		// The symlink route's own pre-PUT destination read is the fixed point
		// (ADR 0085): nothing prepares between it and the empty-body PUT below.
		// A destination the operator did not acknowledge still conflicts; an
		// acknowledged one is replaced only when its fresh reading still matches
		// what they were shown, and refused with both identities otherwise.
		shown, acknowledged, ackErr := e.acknowledged(name)
		if ackErr != nil {
			return failed(priorEffect, StageCommit, CodeUnknown, "symlink-ack", ackErr)
		}
		if !acknowledged {
			return conflict(priorEffect, nil, "destination appeared since planning")
		}
		if stop := e.versioningUnchanged(ctx, name, priorEffect); stop != nil {
			return *stop
		}
		found, identifiable := shownFrom(dstDetail)
		if !identifiable {
			return e.overwriteRefused(name, priorEffect, "the destination could not be identified before replacing it: its HEAD stated no entity tag or no length")
		}
		if found != shown {
			e.recordRefused(name, shown, found)
			return e.overwriteRefused(name, priorEffect, fmt.Sprintf(
				"the destination no longer holds what was acknowledged: it was a %s of %d bytes with entity tag %s, and it is now a %s of %d bytes with entity tag %s",
				shown.Class, shown.Length, shown.ETag, found.Class, found.Length, found.ETag))
		}
		// Decision 6 retains the replaced object's segments, which sit on the
		// destination account the operator was not shown, so there is nothing to
		// reclaim here; record what is there before the PUT replaces it.
		if recErr := e.replacing(name, observedFrom(dstDetail)); recErr != nil {
			e.stop(recErr)
			return failed(priorEffect, StageRecording, CodeUnknown, "symlink-replace-record", recErr)
		}
		overwrite = true
	} else if _, acknowledged, ackErr := e.acknowledged(name); ackErr != nil {
		return failed(priorEffect, StageCommit, CodeUnknown, "symlink-ack", ackErr)
	} else if acknowledged {
		// IsNotFound: the destination the operator acknowledged is gone by the
		// fixed point. It publishes create-only below, but its observed absence
		// is recorded first, as ADR 0085 keeps the observed predecessor including
		// the absence this contract's race admits, rather than leaving a silent
		// first write. A recording failure stops it.
		if recErr := e.replacing(name, ObservedPredecessor{Present: false}); recErr != nil {
			e.stop(recErr)
			return failed(priorEffect, StageRecording, CodeUnknown, "symlink-absent-record", recErr)
		}
	}

	// No leading slash: real Swift silently stores a plain object when
	// the target carries one (measured on SAIO — the mock mimics it).
	headers := map[string]string{"X-Symlink-Target": link.SymlinkTarget}
	if !overwrite {
		headers["If-None-Match"] = "*"
	}
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
	result := copied(0, "symlink")
	result.overwrote = overwrite
	return result
}
