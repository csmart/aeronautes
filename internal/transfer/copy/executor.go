// Package copy writes each object of a copy at its name (ADR 0122 decision
// 4). The engine reads the source and writes the destination for the names it
// can carry; the application reads and writes a name the engine cannot carry,
// and a link, itself. Nothing is staged, no request carries a condition, and
// no name is read after its write: what the engine or the store answered for
// an object is that object's outcome.
package copy

import (
	"context"
	"errors"
	"fmt"
	"io"
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
}

// maxSinglePut is Swift's single-object limit: the application's own copy
// refuses anything larger (a large object with a name the engine cannot
// carry is an honest v1 failure, not a silent skip).
const maxSinglePut = 5 * 1024 * 1024 * 1024

// carryTimeout bounds the one request that gives an object the engine wrote
// its source's headers. That request is sent even when the job was cancelled
// meanwhile, so it needs a limit of its own.
const carryTimeout = time.Minute

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
	// Replace says the operator confirmed replacing what the destination
	// already holds (ADR 0122 decision 2). Without it a name that exists
	// and is not the same is refused.
	Replace bool
	// ChunkBytes is the size above which the engine writes an object in
	// segments. It is what tells a segmented destination from a plain one
	// when the engine did not say where it put the segments, so a run with
	// an engine needs it.
	ChunkBytes int64
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
	// OnRecovery keeps, as the engine reports it, where the engine put an
	// object's segments (ADR 0122 decision 8): a copy that is stopped can
	// leave segments with no manifest, and this record is what names
	// them. A record that cannot be kept stops the run. Optional (tests).
	OnRecovery func(kind string, container, prefix, object string) error
}

// ItemOutcome is one name's fate during execution.
type ItemOutcome struct {
	Name    string
	Outcome string // "copied", "skipped-equal", "overwrite-refused", "failed"
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
	Failures          []Failure
	// Verified counts the objects whose outcome is settled: written, or
	// already at the destination.
	Verified int64
	// CapturedPrefixes are the segment paths the engine reported writing
	// under.
	CapturedPrefixes []rclone.SegmentPrefix
}

const maxFailures = 200

// queued is one name waiting for the engine, with what the source HEAD said
// the engine will not carry for it.
type queued struct {
	name  string
	bytes int64
	// carry is the source's headers the engine does not copy, or nil when
	// it has none.
	carry map[string]string
	// replaces marks a name the operator agreed to replace.
	replaces bool
}

// Executor runs one job's transfer phase.
type Executor struct {
	cfg        Config
	report     *Report
	batch      []queued
	batchBytes int64
	printed    map[rclone.SegmentPrefix]bool
	stopped    error
}

// New validates the config and builds an executor.
func New(cfg Config) (*Executor, error) {
	if cfg.Recorder != nil && (cfg.Recorder.Admit == nil || cfg.Recorder.Execute == nil || cfg.Recorder.Verify == nil) {
		return nil, errors.New("item recording needs admission, execution and verification acknowledgements")
	}
	if cfg.Source == nil || cfg.Dest == nil {
		return nil, errors.New("executor needs both sessions")
	}
	if cfg.SrcContainer == "" || cfg.DstContainer == "" {
		return nil, errors.New("executor needs both containers")
	}
	if cfg.Engine != nil && cfg.ChunkBytes <= 0 {
		return nil, errors.New("executor needs the engine's chunk size")
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
	return &Executor{cfg: cfg, report: &Report{}, printed: map[rclone.SegmentPrefix]bool{}}, nil
}

// Handle consumes one plan item — wire it as plan.Copy's emit callback
// so planning streams straight into execution with bounded memory.
//
// The plan compared the two listings (ADR 0122 decision 4). A name they
// show as the same is skipped with no request. A name only the source has
// is copied. A name whose two rows differ is replaced when the operator
// confirmed replacing, and refused when they did not. A pair the listings
// cannot compare, an empty row or a large object, is read once on each side
// to learn what it is.
func (e *Executor) Handle(ctx context.Context, item plan.Item) error {
	if e.stopped != nil {
		return e.stopped
	}
	// The head of an item is one of the two places a copy parks (ADR 0104):
	// nothing has been done for this item, and the items before it are
	// settled or buffered for an engine batch that has not been sent. The
	// other place is the head of that batch, in flushBatch.
	if err := e.pause(ctx); err != nil {
		return err
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
		return e.observe(item.Name, same())
	case plan.VerdictUnknown:
		return e.resolveUnknown(ctx, item)
	case plan.VerdictNew:
		return e.copy(ctx, item, nil, false)
	case plan.VerdictChanged:
		return e.replace(ctx, item, nil)
	}
	return fmt.Errorf("unhandled verdict %q", item.Verdict)
}

// Finish flushes the pending engine batch and returns the report. The
// report is valid even after an error or cancellation mid-run.
func (e *Executor) Finish(ctx context.Context) (*Report, error) {
	err := e.flushBatch(ctx)
	return e.report, errors.Join(err, e.stopped)
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

// resolveUnknown reads both sides of a pair the listings cannot compare: an
// empty row, which may be an empty object, a dynamic manifest or a link, and
// a large object, whose listed size and tag are its manifest's. These two
// reads are to learn what the names hold, not to watch for another writer.
func (e *Executor) resolveUnknown(ctx context.Context, item plan.Item) error {
	src, err := e.cfg.Source.ObjectDetail(ctx, e.cfg.SrcContainer, e.cfg.SrcPrefix+item.Name)
	if err != nil {
		return e.observe(item.Name, failed(EffectNotAttempted, StageSourceRead, CodeUnknown, "source-head", err))
	}
	dst, err := e.cfg.Dest.ObjectDetail(ctx, e.cfg.DstContainer, e.cfg.DstPrefix+item.Name)
	switch {
	case swiftcontrol.IsNotFound(err):
		return e.copy(ctx, item, src, false)
	case err != nil:
		return e.observe(item.Name, failed(EffectNotAttempted, StagePlanning, CodeUnknown, "destination-head", err))
	case SameByStore(src, dst):
		return e.observe(item.Name, same())
	}
	return e.replace(ctx, item, src)
}

// SameByStore reports whether the store describes two objects as the same
// content: the same length and the same entity tag, neither of them a link.
// A large object's tag is made from its segments' tags, so two large objects
// compare equal only when they are cut at the same places; any other pair is
// treated as different, which costs a copy and never skips one that was
// needed. A link is never the same as anything: it is carried as a link.
func SameByStore(src, dst *swiftcontrol.ObjectDetail) bool {
	if isSymlink(src) || isSymlink(dst) {
		return false
	}
	tag := strings.Trim(src.ETag, `"`)
	return tag != "" && tag == strings.Trim(dst.ETag, `"`) && src.ContentLength == dst.ContentLength
}

// isSymlink reports whether a HEAD describes a Swift symlink in either
// representation: a followed link carries Content-Location (FollowedSymlink), and
// a link that was not followed — a dangling or cross-account link — carries
// X-Symlink-Target (SymlinkTarget). A copy carries the link itself, so neither
// is a plain object to skip or copy by its bytes (§17.1).
func isSymlink(d *swiftcontrol.ObjectDetail) bool {
	return d.FollowedSymlink || d.SymlinkTarget != ""
}

// replace writes over a name the destination already holds, when the
// operator confirmed replacing, and refuses it when they did not.
func (e *Executor) replace(ctx context.Context, item plan.Item, src *swiftcontrol.ObjectDetail) error {
	if !e.cfg.Replace {
		return e.observe(item.Name, executionResult{Observation: Observation{Result: ResultRefused, Effect: EffectNotAttempted,
			Diagnostic: ClassifyDiagnostic(StagePlanning, CodeUnsupported, nil)},
			detail: "the destination already holds this name, and replacing it was not confirmed for this copy"})
	}
	return e.copy(ctx, item, src, true)
}

// DefaultBatchNames and DefaultBatchBytes bound one engine child's
// batch: 500 names or 64 GiB of planned bytes, whichever comes first.
// The download route batches by the same values (ADR 0074 decision 3).
const (
	DefaultBatchNames = 500
	DefaultBatchBytes = int64(64 << 30)
)

// copy sends one name on its route. A name the engine cannot carry is read
// and written by the application at once. Any other name is read once at the
// source first, because two things about it are not in the listing and the
// engine handles neither: a link, which the engine would copy as its target's
// bytes, and the headers the engine does not carry. It then waits for the
// engine with the rest of its batch. src is that read when the caller has
// already made it.
func (e *Executor) copy(ctx context.Context, item plan.Item, src *swiftcontrol.ObjectDetail, replaces bool) error {
	if item.Route == plan.RouteNative {
		e.report.NativeRouted++
		return e.observe(item.Name, e.nativeCopy(ctx, item.Name, replaces))
	}
	if src == nil {
		var err error
		if src, err = e.cfg.Source.ObjectDetail(ctx, e.cfg.SrcContainer, e.cfg.SrcPrefix+item.Name); err != nil {
			return e.observe(item.Name, failed(EffectNotAttempted, StageSourceRead, CodeUnknown, "source-head", err))
		}
	}
	if isSymlink(src) {
		return e.observe(item.Name, e.copySymlink(ctx, item.Name, replaces))
	}
	e.batch = append(e.batch, queued{name: item.Name, bytes: src.ContentLength, carry: notCarriedByEngine(src), replaces: replaces})
	e.batchBytes += src.ContentLength
	if len(e.batch) >= e.cfg.BatchSize || e.batchBytes >= e.cfg.BatchBytes {
		return e.flushBatch(ctx)
	}
	return nil
}

// flushBatch gives the engine the queued names, which it writes at their
// own names under the destination prefix, and settles each from what the
// engine said about it.
func (e *Executor) flushBatch(ctx context.Context) error {
	if len(e.batch) == 0 {
		return nil
	}
	batch := e.batch
	e.batch = nil
	e.batchBytes = 0
	// An engine batch is a unit of its own, whether it filled or the plan
	// ended with it pending, so the run may park here before the child
	// starts (ADR 0104 decision 1). A batch that is not dispatched, for a
	// stopped run, a cancel or a pause that ended in either, is abandoned:
	// its names are admitted and are recorded as not attempted.
	held := errors.Join(e.stopped, ctx.Err())
	if held == nil {
		held = e.pause(ctx)
	}
	if held != nil {
		return e.abandonBatch(batch, held)
	}
	if e.cfg.Engine == nil {
		for _, item := range batch {
			if err := e.observe(item.name, failed(EffectNotAttempted, StageEngine, CodeUnsupported, "engine", errors.New("no engine configured"))); err != nil {
				return err
			}
		}
		return nil
	}
	names := make([]string, len(batch))
	for i, item := range batch {
		names[i] = item.name
	}
	answers := rclone.NewAnswers()
	segments := map[string]rclone.SegmentPrefix{}
	heard := func(event rclone.Event) {
		// The engine names the object each segment belongs to, so the path
		// it reports is that object's.
		if p, ok := rclone.PrefixFromEvent(event); ok {
			p = p.Canonical()
			if event.Object != "" {
				segments[event.Object] = p
			}
			e.capturePrefix(p)
		}
		answers.Hear(event)
		if e.cfg.OnEvent != nil {
			e.cfg.OnEvent(event)
		}
	}
	engineErr := e.runEngine(ctx, names, heard)
	// The engine has had the whole batch, so every name of it gets its
	// outcome, also when the run was stopped while the engine ran, as it is
	// when a segment path could not be kept: an object the engine wrote is
	// written whether or not the job can go on, and is given its headers
	// before anything is recorded. Only an item record that cannot itself be
	// kept ends the recording, because the rest have nowhere to go.
	results := make([]executionResult, len(batch))
	for i, item := range batch {
		results[i] = engineOutcome(answers, item, engineErr)
		if results[i].Result == ResultCopied && item.carry != nil {
			results[i] = e.carry(ctx, item, segments[item.name])
		}
	}
	for i, item := range batch {
		if !e.settle(item.name, results[i]) {
			break
		}
	}
	if e.stopped != nil {
		return e.stopped
	}
	return ctx.Err()
}

// runEngine runs one engine copy of the names from the source prefix to the
// destination prefix, and once more with fresh credentials when the first
// child failed in a way that looks like a rejected token.
func (e *Executor) runEngine(ctx context.Context, names []string, heard func(rclone.Event)) error {
	for attempt := 0; ; attempt++ {
		result, err := e.cfg.Engine.Copy(ctx, e.enginePath(e.cfg.SrcSpec, e.cfg.SrcPrefix),
			e.enginePath(e.cfg.DstSpec, e.cfg.DstPrefix), names, heard)
		if result != nil {
			for _, p := range result.SegmentPrefixes {
				e.capturePrefix(p.Canonical())
			}
		}
		if err != nil || e.stopped != nil {
			return err
		}
		failure := ResultFailure(result)
		// One fresh child for an auth-shaped failure: tokens are
		// force-invalidated so the next spawn AUTHENTICATES, never
		// replays the rejected token (K-XFER-001, review F-06).
		if failure != nil && attempt == 0 && IsAuthFailure(result) && ctx.Err() == nil {
			if e.cfg.OnAuthFailure != nil {
				e.cfg.OnAuthFailure()
			}
			continue
		}
		return failure
	}
}

// engineOutcome is one object's outcome from what the engine said (ADR 0122
// decision 7). An object the engine reported as copied is written. One it
// reported an error for failed with that error. One it said nothing about is
// unknown when the engine itself stopped in failure, and was not written
// when the engine finished cleanly: the pinned engine lists every object it
// writes, and gives no line and no error to a listed name that is no longer
// at the source.
func engineOutcome(answers rclone.Answers, item queued, engineErr error) executionResult {
	switch {
	case answers.Copied[item.name]:
		result := copied(item.bytes, "engine")
		result.overwrote = item.replaces
		return result
	case answers.Refused[item.name] != "":
		return failed(EffectUnresolved, StageEngine, CodeIO, "engine", errors.New(answers.Refused[item.name]))
	case engineErr != nil:
		return failed(EffectUnresolved, StageEngine, CodeUnknown, "engine",
			fmt.Errorf("the engine stopped without reporting this object: %w", engineErr))
	}
	return failed(EffectNotAttempted, StageEngine, CodeIncomplete, "engine",
		errors.New("the engine finished without reporting this object as written; it may no longer be at the source"))
}

// enginePath appends a prefix to a "remote:container" spec.
func (e *Executor) enginePath(spec, prefix string) string {
	if prefix == "" {
		return spec
	}
	return spec + "/" + strings.TrimSuffix(prefix, "/")
}

func (e *Executor) capturePrefix(p rclone.SegmentPrefix) {
	if e.printed[p] {
		return
	}
	e.printed[p] = true
	e.report.CapturedPrefixes = append(e.report.CapturedPrefixes, p)
	if e.cfg.OnRecovery != nil && e.stopped == nil {
		if err := e.cfg.OnRecovery("captured", p.Container, p.Prefix, ""); err != nil {
			e.stop(fmt.Errorf("captured-prefix record: %w", err))
		}
	}
}

// carriedHeaders is the source's writable header set a copy gives the
// destination (§27.4): user metadata, content encoding and disposition, and
// scheduled expiry. Never manifest or system headers. The content type
// travels beside it.
func carriedHeaders(src *swiftcontrol.ObjectDetail) map[string]string {
	headers := map[string]string{}
	for k, v := range src.Meta {
		headers["X-Object-Meta-"+k] = v
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

// notCarriedByEngine is the source's carried set when the engine will not
// deliver all of it, or nil when it will. Measured on the pinned engine: it
// copies the content type and a modification time, the source's own when it
// states one and the time the source was last changed when it does not, and
// nothing else. So a source with only those needs no request of ours, and a
// request of ours states the same modification time the engine wrote, which
// the POST would otherwise replace with nothing.
func notCarriedByEngine(src *swiftcontrol.ObjectDetail) map[string]string {
	headers := carriedHeaders(src)
	_, hasModTime := headers[swiftcontrol.EngineModTimeHeader]
	if len(headers) == 0 || (len(headers) == 1 && hasModTime) {
		return nil
	}
	if !hasModTime && !src.LastModified.IsZero() {
		name, value := swiftcontrol.EngineModTime(src.LastModified)
		headers[name] = value
	}
	// The POST states the content type too, so that none is left to the
	// store's default for a request that names none.
	if src.ContentType != "" {
		headers["Content-Type"] = src.ContentType
	}
	return headers
}

// carry gives an object the engine wrote the source's headers the engine
// does not copy, in one POST that replaces the destination's set with the
// source's. It is sent even when the job was cancelled while the engine ran,
// so that a cancel does not leave an object the engine wrote without them.
//
// A POST to a dynamic manifest that does not repeat its manifest header
// turns it into an empty object (measured on the owned Swift), so an object
// the engine wrote in segments is sent the segment path the engine reported.
// An object too large to be anything but segmented, whose path the engine
// did not report, is not sent a POST at all.
func (e *Executor) carry(ctx context.Context, item queued, segments rclone.SegmentPrefix) executionResult {
	headers := item.carry
	switch {
	case segments.Prefix != "":
		headers["X-Object-Manifest"] = swiftcontrol.DynamicManifestHeader(segments.Container, segments.Prefix)
	case item.bytes > e.cfg.ChunkBytes:
		return failed(EffectUnresolved, StageCommit, CodeIncomplete, "metadata", errors.New(
			"the object was copied, and its metadata was not set: the engine did not report where it put the object's segments, and a metadata request that does not name them would empty the object"))
	}
	carryCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), carryTimeout)
	defer cancel()
	if err := e.cfg.Dest.PostObject(carryCtx, e.cfg.DstContainer, e.cfg.DstPrefix+item.name, headers); err != nil {
		return failed(EffectUnresolved, StageCommit, CodeUnknown, "metadata",
			fmt.Errorf("the object was copied, and its metadata was not set: %w", err))
	}
	result := copied(item.bytes, "engine")
	result.overwrote = item.replaces
	return result
}

// nativeCopy carries a name the engine cannot: the application reads the
// source and writes the destination in one pass. A plain source's entity
// tag is the MD5 of its content, so it is declared and the store refuses a
// body that does not hash to it. A large source has no such tag for its
// assembled bytes and declares none.
func (e *Executor) nativeCopy(ctx context.Context, name string, replaces bool) (result executionResult) {
	body, src, err := e.cfg.Source.OpenObject(ctx, e.cfg.SrcContainer, e.cfg.SrcPrefix+name)
	if err != nil {
		return failed(EffectNotAttempted, StageSourceRead, CodeUnknown, "native-get", err)
	}
	defer func() { result.addProblem(StageSourceRead, body.Close()) }()
	if isSymlink(src) {
		return e.copySymlink(ctx, name, replaces)
	}
	if src.ContentLength > maxSinglePut {
		return failed(EffectNotAttempted, StageSourceRead, CodeUnsupported, "native-size", fmt.Errorf(
			"logical size %d exceeds the single-PUT limit and the name cannot ride the engine (v1 limitation)", src.ContentLength))
	}
	headers := carriedHeaders(src)
	if _, complete := runDigest(src.ETag); complete && src.Class == swiftcontrol.ClassPlain {
		headers["ETag"] = strings.Trim(src.ETag, `"`)
	}
	stream := &nativeCopyReader{Reader: body}
	if _, err := e.cfg.Dest.PutObject(ctx, e.cfg.DstContainer, e.cfg.DstPrefix+name, stream, src.ContentType, headers); err != nil {
		return putFailed("native-put", err)
	}
	result = copied(stream.bytes, "native")
	result.overwrote = replaces
	return result
}

// A chunked GET has no declared length: count the bytes sent and enforce
// the single-PUT ceiling without asking a later HEAD to describe this body.
type nativeCopyReader struct {
	io.Reader
	bytes int64
}

func (r *nativeCopyReader) Read(p []byte) (int, error) {
	n, err := r.Reader.Read(p)
	r.bytes += int64(n)
	if r.bytes > maxSinglePut {
		return 0, errors.New("native copy exceeds the single-PUT limit")
	}
	return n, err
}

// copySymlink carries a Swift symlink AS a symlink (§17.1's
// carry-when-present row): the link object's own target headers and
// user metadata land at the destination in one empty-body PUT; the
// target itself moves (or not) as its own listed name. A dangling
// destination link is legal Swift and reported honestly.
func (e *Executor) copySymlink(ctx context.Context, name string, replaces bool) executionResult {
	link, err := e.cfg.Source.SymlinkGet(ctx, e.cfg.SrcContainer, e.cfg.SrcPrefix+name)
	if err != nil {
		return failed(EffectNotAttempted, StageSourceRead, CodeUnknown, "symlink-head", err)
	}
	if link.SymlinkTarget == "" {
		return failed(EffectNotAttempted, StageSourceRead, CodeIncomplete, "symlink-head", errors.New("followed a symlink but ?symlink=get shows no target"))
	}
	// No leading slash: real Swift silently stores a plain object when
	// the target carries one (measured on SAIO — the mock mimics it).
	headers := map[string]string{"X-Symlink-Target": link.SymlinkTarget}
	if link.SymlinkTargetAccount != "" {
		headers["X-Symlink-Target-Account"] = link.SymlinkTargetAccount
	}
	for k, v := range link.Meta {
		headers["X-Object-Meta-"+k] = v
	}
	if _, err := e.cfg.Dest.PutObject(ctx, e.cfg.DstContainer, e.cfg.DstPrefix+name, strings.NewReader(""), link.ContentType, headers); err != nil {
		return putFailed("symlink-put", err)
	}
	result := copied(0, "symlink")
	result.overwrote = replaces
	return result
}

// putFailed puts the store's answer to a refused or unanswered PUT in the
// record's words (ADR 0122 decisions 1 and 7): a refusal wrote nothing, and
// no answer or a server error leaves it unknown.
func putFailed(label string, err error) executionResult {
	var refused *swiftcontrol.SwiftError
	if errors.As(err, &refused) && refused.HTTPStatus >= 400 && refused.HTTPStatus < 500 {
		return failed(EffectNotAttempted, StageCommit, CodeIO, label, err)
	}
	return failed(EffectUnresolved, StageCommit, CodeUnknown, label, err)
}
