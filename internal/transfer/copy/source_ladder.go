package copy

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"errors"
	"fmt"
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
// listing can be bound to it the same way and it has no ladder here.
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
	slash := strings.Index(src.Manifest, "/")
	if slash <= 0 {
		return nil, fmt.Errorf("dynamic large object source carries malformed manifest %q", src.Manifest)
	}
	container, prefix := src.Manifest[:slash], src.Manifest[slash+1:]
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
