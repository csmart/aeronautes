package copy

import (
	"bytes"
	"context"
	"crypto/md5"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"io"
	"strings"

	"github.com/csmart/aeronautes/internal/swiftcontrol"
)

// contentRun is one length-and-digest run of a source's content ladder:
// the bytes a source says occupy one stretch of its logical content, and
// the MD5 it says they hash to.
type contentRun struct {
	length int64
	digest [md5.Size]byte
}

// contentLadder is the ordered content expectation of a source. A plain
// object contributes one run; a dynamic large object contributes one run
// per segment, in the order the store assembles them.
type contentLadder []contentRun

// maxLadderRuns bounds what one comparison will hold in memory. A run is
// twenty-four bytes, so this ceiling is a few megabytes for a source that
// declares a quarter of a million segments — far past any segmentation a
// store produces in practice, and far short of a source that could
// exhaust a workstation. A source beyond it refuses with its own reason
// rather than being read on an expectation that was silently truncated.
const maxLadderRuns = 250_000

// maxLadderBytes bounds the manifest body a static large object may
// present as its expectation. It is generous for a quarter of a million
// recorded entries and is a hard ceiling rather than a truncation point.
const maxLadderBytes = 64 << 20

func (ladder contentLadder) bytes() int64 {
	var total int64
	for _, run := range ladder {
		total += run.length
	}
	return total
}

// runDigest parses one entity tag into a run digest. Swift quotes entity
// tags on some responses and not others, and reports them in either case,
// so trim and fold before deciding whether the value is a complete MD5.
func runDigest(etag string) ([md5.Size]byte, bool) {
	trimmed := strings.Trim(etag, `"`)
	if len(trimmed) != md5.Size*2 {
		return [md5.Size]byte{}, false
	}
	raw, err := hex.DecodeString(trimmed)
	if err != nil {
		return [md5.Size]byte{}, false
	}
	return [md5.Size]byte(raw), true
}

// sourceLadder builds the source's content expectation, or refuses.
//
// A plain object states its own content MD5, so it is one run. A dynamic
// large object states the MD5 of its segments' concatenated entity tags
// (measured on the deployed service, modelled in the mock), so a listing
// of its manifest prefix supplies a ladder whose recombination has to
// reproduce the object's reported entity tag and logical length. That
// recombination is what binds an advisory listing to the authoritative
// HEAD: a stale, truncated, extended or reordered listing does not
// reproduce the aggregate and is refused.
//
// A static large object reports the entity tag its manifest recorded
// rather than one derived from the segments that are there now, so no
// listing can be bound to it the same way. Its own recorded manifest can
// be, and is: see staticSourceLadder.
func (e *Executor) sourceLadder(ctx context.Context, src *swiftcontrol.ObjectDetail) (contentLadder, error) {
	if src.ContentLength < 0 {
		return nil, fmt.Errorf("source reports a negative logical length of %d bytes", src.ContentLength)
	}
	switch src.Class {
	case swiftcontrol.ClassPlain:
		digest, ok := runDigest(src.ETag)
		if !ok {
			return nil, errors.New("plain source carries no complete MD5 expectation")
		}
		return contentLadder{{length: src.ContentLength, digest: digest}}, nil
	case swiftcontrol.ClassDLO:
		return e.dynamicSourceLadder(ctx, src, maxLadderRuns)
	case swiftcontrol.ClassSLO:
		return e.staticSourceLadder(ctx, src)
	default:
		return nil, fmt.Errorf("source class %v supplies no content expectation", src.Class)
	}
}

// maxRuns is a parameter rather than a constant read inside so a test can
// drive the ceiling with a fixture it can actually build; a source with a
// quarter of a million segments is not one of those.
func (e *Executor) dynamicSourceLadder(ctx context.Context, src *swiftcontrol.ObjectDetail, maxRuns int) (contentLadder, error) {
	container, prefix, ok := swiftcontrol.DynamicSegments(src.Manifest)
	if !ok {
		return nil, fmt.Errorf("dynamic large object source carries malformed manifest %q", src.Manifest)
	}
	var ladder contentLadder
	aggregate := md5.New()
	for entry, err := range e.cfg.Source.Objects(ctx, container, swiftcontrol.ObjectListOptions{Deep: true, Prefix: prefix}) {
		if err != nil {
			return nil, fmt.Errorf("source segment listing: %w", err)
		}
		object, ok := entry.(swiftcontrol.ObjectEntry)
		if !ok {
			continue
		}
		if object.SLOListed {
			return nil, fmt.Errorf("source segment %q is itself a static large object and states no content MD5", object.Name)
		}
		if object.Bytes < 0 {
			return nil, fmt.Errorf("source segment %q reports a negative length of %d bytes", object.Name, object.Bytes)
		}
		digest, ok := runDigest(object.ETag)
		if !ok {
			return nil, fmt.Errorf("source segment %q carries no complete MD5", object.Name)
		}
		if _, err := aggregate.Write([]byte(strings.Trim(object.ETag, `"`))); err != nil {
			return nil, err
		}
		if len(ladder) == maxRuns {
			return nil, fmt.Errorf("source declares more than %d segments, beyond what one content comparison holds", maxRuns)
		}
		ladder = append(ladder, contentRun{length: object.Bytes, digest: digest})
	}
	if total := ladder.bytes(); total != src.ContentLength {
		return nil, fmt.Errorf("source segments sum to %d bytes over %d objects, the source reports %d", total, len(ladder), src.ContentLength)
	}
	want, ok := runDigest(src.ETag)
	if !ok {
		return nil, errors.New("dynamic large object source carries no complete aggregate entity tag")
	}
	if got := [md5.Size]byte(aggregate.Sum(nil)); got != want {
		return nil, fmt.Errorf("source segment listing recombines to %x, the source reports %x — listing stale, incomplete or reordered", got, want)
	}
	return ladder, nil
}

// staticSourceLadder builds the ladder a static large object records in
// its own manifest.
//
// A static large object reports the entity tag its manifest recorded
// rather than one derived from the segments that are there now, so a
// listing of those segments cannot be bound to it. Its manifest can:
// each entry states the length and MD5 the object was created from, and
// the recombination of those values is exactly the entity tag the HEAD
// reports. Comparing the copied content against that ladder therefore
// answers a stronger question than the DLO case does — whether the bytes
// still match what the manifest says they are.
//
// Only entries that state a whole segment's own content MD5 qualify. A
// ranged entry records the containing segment's tag rather than the
// range's, a nested manifest records an aggregate rather than content,
// and an unrecognised entry states nothing; each refuses the covered
// path instead of being guessed at.
func (e *Executor) staticSourceLadder(ctx context.Context, src *swiftcontrol.ObjectDetail) (contentLadder, error) {
	// The bounded reader, not the display getter: it refuses rather than
	// truncating past its limits, admits only entries that state a whole
	// segment's own content MD5, and refuses on a failed body close, so
	// what it returns is a complete input to a safety decision.
	entries, err := e.cfg.Source.SLOManifestDigests(ctx, e.cfg.SrcContainer, src.Name, maxLadderRuns, maxLadderBytes)
	if err != nil {
		return nil, fmt.Errorf("static large object manifest: %w", err)
	}
	ladder := make(contentLadder, 0, len(entries))
	aggregate := md5.New()
	for i, segment := range entries {
		if segment.Size < 0 {
			return nil, fmt.Errorf("manifest entry %d records a negative length of %d bytes", i, segment.Size)
		}
		digest, ok := runDigest(segment.ETag)
		if !ok {
			return nil, fmt.Errorf("manifest entry %d records no complete MD5", i)
		}
		if _, err := aggregate.Write([]byte(hex.EncodeToString(digest[:]))); err != nil {
			return nil, err
		}
		ladder = append(ladder, contentRun{length: segment.Size, digest: digest})
	}
	if total := ladder.bytes(); total != src.ContentLength {
		return nil, fmt.Errorf("manifest entries sum to %d bytes over %d entries, the source reports %d", total, len(ladder), src.ContentLength)
	}
	want, ok := runDigest(src.ETag)
	if !ok {
		return nil, errors.New("static large object source carries no complete aggregate entity tag")
	}
	if got := [md5.Size]byte(aggregate.Sum(nil)); got != want {
		return nil, fmt.Errorf("manifest recombines to %x, the source reports %x — manifest and entity tag disagree", got, want)
	}
	return ladder, nil
}

// Ladder is a source's content expectation, carried across package
// boundaries so the local upload and download routes compare against the
// same expectation the copy routes do. It is opaque: callers feed bytes
// through a digester rather than inspecting runs.
type Ladder struct{ runs contentLadder }

// Bytes is the logical length the expectation accounts for. A caller
// compares it with the length it was independently told to expect: when
// those disagree the source is describing itself inconsistently, which
// is not something a content comparison can adjudicate.
func (l Ladder) Bytes() int64 { return l.runs.bytes() }

// SourceLadder builds one source object's content expectation, reading
// any segment listing or manifest it needs through the given session.
// It is the same construction the staged copy route uses; the routes
// differ only in which byte stream they compare against it.
func SourceLadder(ctx context.Context, session *swiftcontrol.Session, container string, src *swiftcontrol.ObjectDetail) (Ladder, error) {
	e := &Executor{cfg: Config{Source: session, SrcContainer: container}}
	runs, err := e.sourceLadder(ctx, src)
	if err != nil {
		return Ladder{}, err
	}
	return Ladder{runs: runs}, nil
}

// PlainLadder is the expectation for content whose whole-stream length
// and MD5 are already known, which is what a local file supplies.
func PlainLadder(length int64, etag string) (Ladder, error) {
	if length < 0 {
		return Ladder{}, fmt.Errorf("negative length of %d bytes", length)
	}
	digest, ok := runDigest(etag)
	if !ok {
		return Ladder{}, errors.New("no complete MD5 expectation")
	}
	return Ladder{runs: contentLadder{{length: length, digest: digest}}}, nil
}

// SegmentedLadder is the ordered expectation of concatenated runs.
// Each part is one run: its length and MD5. The GET the caller compares
// is assembled independently, so a permutation or duplicate is a
// different stream rather than a second hash of the same buffer.
func SegmentedLadder(parts ...[]byte) Ladder {
	runs := make(contentLadder, 0, len(parts))
	for _, part := range parts {
		runs = append(runs, contentRun{length: int64(len(part)), digest: md5.Sum(part)})
	}
	return Ladder{runs: runs}
}

// LadderDigest folds bytes into a source's ladder as they pass, so a
// route that already streams its content pays no second read. It is the
// one implementation of the comparison: the read-side comparator drives
// it too, because a second copy of this logic is exactly the dual-path
// divergence this project keeps finding.
type LadderDigest struct {
	ladder contentLadder
	// total is the expectation's length, computed once: contentLadder.bytes
	// walks every run, and Write consults it per chunk.
	total    int64
	digest   hash.Hash
	index    int
	runCount int64
	count    int64
	failure  error
	code     Code
}

// NewLadderDigest starts a comparison against the given expectation.
func NewLadderDigest(l Ladder) *LadderDigest {
	d := &LadderDigest{ladder: l.runs, total: l.runs.bytes(), digest: md5.New()}
	d.settle()
	return d
}

// Write folds the next bytes of the stream in. It never returns a short
// write: a comparison failure is reported by Result, so a caller copying
// through a MultiWriter is not told the destination write failed.
func (d *LadderDigest) Write(p []byte) (int, error) {
	if d.failure != nil {
		return len(p), nil
	}
	// Subtract before adding so even a maximal expectation cannot
	// overflow and turn an overrun into an accepted comparison.
	if int64(len(p)) > d.total-d.count {
		d.fail(CodeIncomplete, fmt.Errorf("content exceeds the source length of %d bytes", d.total))
		return len(p), nil
	}
	for chunk := p; len(chunk) > 0; {
		if d.index >= len(d.ladder) {
			d.fail(CodeIncomplete, fmt.Errorf("content exceeds the source length of %d bytes", d.total))
			return len(p), nil
		}
		take := d.ladder[d.index].length - d.runCount
		if int64(len(chunk)) < take {
			take = int64(len(chunk))
		}
		if _, err := d.digest.Write(chunk[:take]); err != nil {
			d.fail(CodeIO, err)
			return len(p), nil
		}
		d.runCount += take
		d.count += take
		chunk = chunk[take:]
		d.settle()
		if d.failure != nil {
			return len(p), nil
		}
	}
	return len(p), nil
}

// settle closes every run whose bytes are complete, zero-length runs
// included, so a run is compared the moment its last byte arrives and
// never after another run's bytes have been folded in.
func (d *LadderDigest) settle() {
	for d.index < len(d.ladder) && d.runCount == d.ladder[d.index].length {
		if !bytes.Equal(d.digest.Sum(nil), d.ladder[d.index].digest[:]) {
			d.fail(CodeUnverified, ladderMismatch(d.index, len(d.ladder)))
			return
		}
		d.digest.Reset()
		d.runCount = 0
		d.index++
	}
}

func (d *LadderDigest) fail(code Code, err error) {
	if d.failure == nil {
		d.failure, d.code = err, code
	}
}

// Result closes the comparison. An empty code means the content matched
// the expectation exactly, run for run and in order.
func (d *LadderDigest) Result() (Code, error) {
	if d.failure != nil {
		return d.code, d.failure
	}
	if d.count != d.total {
		return CodeIncomplete, fmt.Errorf("content ended after %d bytes, source has %d", d.count, d.total)
	}
	if d.index != len(d.ladder) {
		return CodeIncomplete, fmt.Errorf("content ended inside run %d of %d", d.index+1, len(d.ladder))
	}
	return "", nil
}

// CompareContent reads a stream to its end and compares it against the
// expectation, closing the body and joining any close or cancellation
// failure. It is the read-side driver of the one comparison.
func CompareContent(ctx context.Context, body io.ReadCloser, l Ladder) (code Code, err error) {
	defer func() {
		closeErr := body.Close()
		if closeErr != nil || ctx.Err() != nil {
			if err == nil {
				code = CodeIO
			}
			err = errors.Join(err, closeErr, ctx.Err())
		}
	}()
	digest := NewLadderDigest(l)
	buffer := make([]byte, 64*1024)
	emptyReads := 0
	for {
		if err := ctx.Err(); err != nil {
			return CodeIO, err
		}
		n, readErr := body.Read(buffer)
		if n < 0 || n > len(buffer) {
			return CodeIO, errors.Join(errors.New("content reader returned an invalid byte count"), readErr)
		}
		if n > 0 {
			if _, err := digest.Write(buffer[:n]); err != nil {
				return CodeIO, errors.Join(err, readErr)
			}
			if digest.failure != nil {
				if readErr != nil && readErr != io.EOF {
					return digest.code, errors.Join(digest.failure, readErr)
				}
				return digest.code, digest.failure
			}
		}
		if readErr != nil {
			if readErr != io.EOF {
				return CodeIO, readErr
			}
			return digest.Result()
		}
		if n == 0 {
			emptyReads++
			if emptyReads == 100 {
				return CodeIO, io.ErrNoProgress
			}
		} else {
			emptyReads = 0
		}
	}
}

func ladderMismatch(index, runs int) error {
	if runs == 1 {
		return errors.New("staged content MD5 does not match the plain source")
	}
	return fmt.Errorf("staged content MD5 does not match source segment %d of %d", index+1, runs)
}
