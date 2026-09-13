package swiftcontrol

import (
	"context"
	"crypto/md5"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/ncw/swift/v2"
)

// The §23 SLO model: one ordered entry list preserving the original
// position of every entry — recognised, inline, or unknown — so display
// and equality logic never work from a lossy reshaping.

// SLOManifest is an SLO's authoritative exact manifest.
type SLOManifest struct {
	Entries []SLOEntry
}

// SLOEntry is a tagged union; each variant keeps RawFields so unknown
// fields on an otherwise-recognised entry survive an exact round-trip.
type SLOEntry interface{ isSLOEntry() }

// ObjectRef names one object.
type ObjectRef struct {
	Container string
	Object    string
}

// ByteRange is a manifest entry's inclusive byte range.
type ByteRange struct {
	Start int64
	End   int64
}

// ObjectSegment is a manifest entry referencing (part of) an object.
type ObjectSegment struct {
	Path ObjectRef
	// ETag is nil when the manifest omits it.
	ETag *string
	Size uint64
	// Range is nil for whole-object entries; ranged entries contribute
	// `etag:range;` to the SLO ETag input (§23).
	Range *ByteRange
	// NestedSLO marks a sub-SLO entry.
	NestedSLO bool
	RawFields map[string]any
}

func (ObjectSegment) isSLOEntry() {}

// InlineDataSegment is an inline `data` entry; Data is the decoded
// base64 and contributes MD5(decoded bytes) to the SLO ETag input.
type InlineDataSegment struct {
	Data      []byte
	RawFields map[string]any
}

func (InlineDataSegment) isSLOEntry() {}

// RawEntry is an unrecognised/future entry, retained verbatim in place.
type RawEntry struct {
	Fields map[string]any
}

func (RawEntry) isSLOEntry() {}

// SLOManifest GETs ?multipart-manifest=get and parses the manifest into
// the exact model. It does not verify that the object is an SLO first —
// callers hold an ObjectDetail with ClassSLO from the HEAD (§20).
func (s *Session) SLOManifest(ctx context.Context, container, name string) (*SLOManifest, error) {
	// The library's operation wrappers authenticate lazily; a direct Call
	// (needed for the query parameter) must do the same, and refresh its
	// target on mid-call re-auth.
	credentials, err := s.Credentials(ctx, 0)
	if err != nil {
		return nil, err
	}
	resp, _, err := s.conn.Call(ctx, credentials.StorageURL, swift.RequestOpts{
		Container:  container,
		ObjectName: name,
		Operation:  "GET",
		Parameters: url.Values{"multipart-manifest": []string{"get"}},
		OnReAuth:   s.auth.storageTarget,
	})
	if err != nil {
		return nil, Normalize(err)
	}
	defer resp.Body.Close()

	// Field order inside entries doesn't matter, entry order does; numbers
	// stay json.Number so large sizes survive the round-trip untouched.
	decoder := json.NewDecoder(resp.Body)
	decoder.UseNumber()
	var rawEntries []map[string]any
	if err := decoder.Decode(&rawEntries); err != nil {
		return nil, fmt.Errorf("parse SLO manifest for %s/%s: %w", container, name, err)
	}

	manifest := &SLOManifest{Entries: make([]SLOEntry, 0, len(rawEntries))}
	for i, fields := range rawEntries {
		entry, err := parseSLOEntry(fields)
		if err != nil {
			return nil, fmt.Errorf("SLO manifest entry %d of %s/%s: %w", i, container, name, err)
		}
		manifest.Entries = append(manifest.Entries, entry)
	}
	return manifest, nil
}

// SLOSegmentDigest is one manifest entry reduced to the two facts a
// content comparison needs: the length the manifest recorded and the MD5
// it recorded for those bytes.
type SLOSegmentDigest struct {
	Size int64
	ETag string
}

// errManifestBudget stops a decoder the moment it asks for more than the
// caller allowed, so the budget bounds what is read rather than
// describing it afterwards.
var errManifestBudget = errors.New("manifest exceeds its byte budget")

// budgetReader hands out at most budget bytes and then refuses. It counts
// what it actually delivered so a test can assert the bound was enforced
// during the read rather than checked after it.
//
// The limit is inclusive: a body that ends exactly at the budget is
// admitted. Distinguishing that from one byte too many costs a single
// probe read once the budget is spent, which is the same bounded overrun
// check the staged-content comparison makes, and never an unbounded read.
type budgetReader struct {
	inner  io.Reader
	budget int64
	count  int64
	probes int
}

func (b *budgetReader) Read(p []byte) (int, error) {
	if b.count >= b.budget {
		var probe [1]byte
		n, err := b.inner.Read(probe[:])
		switch {
		case n > 0:
			return 0, errManifestBudget
		case err == io.EOF:
			return 0, io.EOF
		case err != nil:
			return 0, err
		default:
			// A reader that reports neither progress nor an end must not
			// spin the decoder forever.
			b.probes++
			if b.probes >= 100 {
				return 0, io.ErrNoProgress
			}
			return 0, nil
		}
	}
	if remaining := b.budget - b.count; int64(len(p)) > remaining {
		p = p[:remaining]
	}
	n, err := b.inner.Read(p)
	b.count += int64(n)
	return n, err
}

// SLOManifestDigests reads the manifest as a bounded, complete input for
// a safety decision, rather than for display.
//
// It differs from SLOManifest in the ways a safety input has to. It
// requires a complete response, HTTP 200 with no Content-Range, on the
// same contract OpenObjectContent uses, because a partial body that
// happens to parse is not the manifest. It never reads past its byte
// budget: the budget bounds the decoder's input, including the opening
// token and the read that confirms the end of the stream, rather than
// being checked once an entry has already been buffered. It refuses
// instead of truncating once maxEntries is passed, refuses trailing
// content after the entry list, and refuses when the body cannot be
// closed cleanly, because a read that could not be completed cleanly is
// not evidence.
//
// It reduces each entry to the length and MD5 of the bytes that entry
// contributes, and refuses any entry that states neither. An inline data
// entry carries its own bytes, so its run is read from them. A ranged
// entry records the containing segment's tag rather than the range's,
// and a nested manifest records an aggregate rather than content, so
// neither can be read this way.
func (s *Session) SLOManifestDigests(ctx context.Context, container, name string, maxEntries int, maxBytes int64) (digests []SLOSegmentDigest, err error) {
	credentials, err := s.Credentials(ctx, 0)
	if err != nil {
		return nil, err
	}
	resp, headers, err := s.conn.Call(ctx, credentials.StorageURL, swift.RequestOpts{
		Container:  container,
		ObjectName: name,
		Operation:  "GET",
		Parameters: url.Values{"multipart-manifest": []string{"get"}},
		OnReAuth:   s.auth.storageTarget,
	})
	if err != nil {
		if resp != nil && resp.Body != nil {
			err = errors.Join(err, resp.Body.Close())
		}
		return nil, WithTransID(Normalize(err), headers)
	}
	defer func() { err = errors.Join(err, resp.Body.Close()) }()

	complete := resp.StatusCode == http.StatusOK
	for key := range resp.Header {
		// Header.Get cannot distinguish an absent field from an empty one.
		if strings.EqualFold(key, "Content-Range") {
			complete = false
		}
	}
	if !complete {
		return nil, fmt.Errorf("SLO manifest for %s/%s requires HTTP 200 without Content-Range (received HTTP %d)",
			container, name, resp.StatusCode)
	}
	return parseSLOManifestDigests(resp.Body, maxEntries, maxBytes, container+"/"+name)
}

// parseSLOManifestDigests is the bounded parse, separated from the
// request so its consumption can be measured exactly.
func parseSLOManifestDigests(body io.Reader, maxEntries int, maxBytes int64, where string) ([]SLOSegmentDigest, error) {
	var digests []SLOSegmentDigest
	bounded := &budgetReader{inner: body, budget: maxBytes}
	fail := func(format string, args ...any) ([]SLOSegmentDigest, error) {
		return nil, fmt.Errorf(format, args...)
	}
	decoder := json.NewDecoder(bounded)
	decoder.UseNumber()
	opening, err := decoder.Token()
	if err != nil {
		if errors.Is(err, errManifestBudget) {
			return fail("SLO manifest for %s exceeds %d bytes", where, maxBytes)
		}
		return fail("read SLO manifest for %s: %w", where, err)
	}
	if opening != json.Delim('[') {
		return fail("SLO manifest for %s is not an entry list", where)
	}
	for decoder.More() {
		if len(digests) == maxEntries {
			return fail("SLO manifest for %s lists more than %d entries", where, maxEntries)
		}
		var fields map[string]any
		if err := decoder.Decode(&fields); err != nil {
			if errors.Is(err, errManifestBudget) {
				return fail("SLO manifest for %s exceeds %d bytes", where, maxBytes)
			}
			return fail("parse SLO manifest entry %d of %s: %w", len(digests), where, err)
		}
		entry, err := parseSLOEntry(fields)
		if err != nil {
			return fail("SLO manifest entry %d of %s: %w", len(digests), where, err)
		}
		if inline, ok := entry.(InlineDataSegment); ok {
			// An inline entry carries its own bytes, so its length and MD5
			// are read from them rather than recorded separately. Swift
			// contributes the same MD5 to the object's entity tag.
			sum := md5.Sum(inline.Data)
			digests = append(digests, SLOSegmentDigest{Size: int64(len(inline.Data)), ETag: hex.EncodeToString(sum[:])})
			continue
		}
		segment, ok := entry.(ObjectSegment)
		switch {
		case !ok:
			return fail("SLO manifest entry %d of %s states no whole-segment content MD5", len(digests), where)
		case segment.NestedSLO:
			return fail("SLO manifest entry %d of %s is a nested manifest", len(digests), where)
		case segment.Range != nil:
			return fail("SLO manifest entry %d of %s is ranged, so its recorded tag is not the range's content MD5", len(digests), where)
		case segment.ETag == nil:
			return fail("SLO manifest entry %d of %s records no MD5", len(digests), where)
		case segment.Size > math.MaxInt64:
			return fail("SLO manifest entry %d of %s records an unusable length", len(digests), where)
		}
		digests = append(digests, SLOSegmentDigest{Size: int64(segment.Size), ETag: *segment.ETag})
	}
	if closing, err := decoder.Token(); err != nil || closing != json.Delim(']') {
		if errors.Is(err, errManifestBudget) {
			return fail("SLO manifest for %s exceeds %d bytes", where, maxBytes)
		}
		return fail("SLO manifest for %s is not a complete entry list", where)
	}
	// A clean end, not merely an end: errors.Is would also accept an EOF
	// joined with a read failure, which is a body that did not finish and
	// so is not evidence. Same rule as the staged-content comparison.
	if _, err := decoder.Token(); err != io.EOF {
		switch {
		case errors.Is(err, errManifestBudget):
			return fail("SLO manifest for %s exceeds %d bytes", where, maxBytes)
		case err == nil:
			return fail("SLO manifest for %s carries trailing content", where)
		default:
			return fail("SLO manifest for %s did not end cleanly: %w", where, err)
		}
	}
	return digests, nil
}

// parseSLOEntry classifies one manifest entry. The internal manifest
// format (multipart-manifest=get) uses name/hash/bytes/content_type,
// plus range, sub_slo, and data.
func parseSLOEntry(fields map[string]any) (SLOEntry, error) {
	if data, ok := fields["data"].(string); ok {
		decoded, err := base64.StdEncoding.DecodeString(data)
		if err != nil {
			return nil, fmt.Errorf("inline data entry: %w", err)
		}
		return InlineDataSegment{Data: decoded, RawFields: fields}, nil
	}

	name, ok := fields["name"].(string)
	if !ok {
		// Unrecognised shape: keep it verbatim, in place.
		return RawEntry{Fields: fields}, nil
	}

	segment := ObjectSegment{RawFields: fields}
	path := strings.TrimPrefix(name, "/")
	slash := strings.Index(path, "/")
	if slash <= 0 {
		return nil, fmt.Errorf("segment path %q has no container/object split", name)
	}
	segment.Path = ObjectRef{Container: path[:slash], Object: path[slash+1:]}

	if hash, ok := fields["hash"].(string); ok {
		segment.ETag = &hash
	}
	if bytes, ok := fields["bytes"].(json.Number); ok {
		size, err := strconv.ParseUint(bytes.String(), 10, 64)
		if err != nil {
			return nil, fmt.Errorf("segment size %q: %w", bytes.String(), err)
		}
		segment.Size = size
	}
	if sub, ok := fields["sub_slo"].(bool); ok {
		segment.NestedSLO = sub
	}
	if rangeSpec, ok := fields["range"].(string); ok && rangeSpec != "" {
		parsed, err := parseByteRange(rangeSpec)
		if err != nil {
			return nil, err
		}
		segment.Range = parsed
	}
	return segment, nil
}

func parseByteRange(spec string) (*ByteRange, error) {
	dash := strings.Index(spec, "-")
	if dash <= 0 || dash == len(spec)-1 {
		return nil, fmt.Errorf("segment range %q is not start-end", spec)
	}
	start, err := strconv.ParseInt(spec[:dash], 10, 64)
	if err != nil {
		return nil, fmt.Errorf("segment range %q: %w", spec, err)
	}
	end, err := strconv.ParseInt(spec[dash+1:], 10, 64)
	if err != nil {
		return nil, fmt.Errorf("segment range %q: %w", spec, err)
	}
	return &ByteRange{Start: start, End: end}, nil
}
