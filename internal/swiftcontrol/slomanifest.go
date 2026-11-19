package swiftcontrol

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
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

// SLOManifest uses the application's manifest limits for display and for
// destructive membership: neither may mistake a partial answer for the list.
// Callers hold an ObjectDetail with ClassSLO from the HEAD (§20).
func (s *Session) SLOManifest(ctx context.Context, container, name string) (*SLOManifest, error) {
	entries, err := s.SLOManifestEntries(ctx, container, name, 10000, 8<<20)
	if err != nil {
		return nil, err
	}
	return &SLOManifest{Entries: entries}, nil
}

// completeManifestResponse reports whether resp carries the whole manifest:
// HTTP 200 with no Content-Range. Header.Get cannot distinguish an absent
// field from an empty one, so the keys are scanned.
func completeManifestResponse(resp *http.Response) bool {
	if resp.StatusCode != http.StatusOK {
		return false
	}
	for key := range resp.Header {
		if strings.EqualFold(key, "Content-Range") {
			return false
		}
	}
	return true
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
// probe read once the budget is spent, and never an unbounded read.
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

// SLOManifestEntries requires a complete HTTP 200 without Content-Range,
// bounded input and entry count, a clean end, and a successful body close.
// A manifest this build cannot read whole is refused, never truncated.
func (s *Session) SLOManifestEntries(ctx context.Context, container, name string, maxEntries int, maxBytes int64) (entries []SLOEntry, err error) {
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

	if !completeManifestResponse(resp) {
		return nil, fmt.Errorf("SLO manifest for %s/%s requires HTTP 200 without Content-Range (received HTTP %d)",
			container, name, resp.StatusCode)
	}
	body := &countingReader{reader: resp.Body}
	entries, err = parseSLOManifestEntries(body, maxEntries, maxBytes, container+"/"+name)
	if err != nil && body.read == 0 {
		return nil, fmt.Errorf("SLO manifest for %s/%s: %w", container, name, ErrManifestNotReturned)
	}
	return entries, err
}

// ErrManifestNotReturned is a complete 200 answer to a static manifest read
// with an empty body: the store holds the static large object but does not
// return its manifest, as Ceph RGW was measured to (ADR 0111 decision 9).
var ErrManifestNotReturned = errors.New("the store answered the static manifest read with an empty body")

type countingReader struct {
	reader io.Reader
	read   int64
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.reader.Read(p)
	c.read += int64(n)
	return n, err
}

// parseSLOManifestEntries is the bounded, complete-list parse that keeps
// every entry in its own position, including an unrecognised one: a record
// of what a manifest named cannot silently drop an entry it did not
// understand.
func parseSLOManifestEntries(body io.Reader, maxEntries int, maxBytes int64, where string) ([]SLOEntry, error) {
	var entries []SLOEntry
	bounded := &budgetReader{inner: body, budget: maxBytes}
	fail := func(format string, args ...any) ([]SLOEntry, error) {
		return nil, fmt.Errorf(format, args...)
	}
	budgeted := func(err error, format string, args ...any) ([]SLOEntry, error) {
		if errors.Is(err, errManifestBudget) {
			return fail("SLO manifest for %s exceeds %d bytes", where, maxBytes)
		}
		return fail(format, args...)
	}
	decoder := json.NewDecoder(bounded)
	decoder.UseNumber()
	opening, err := decoder.Token()
	if err != nil {
		return budgeted(err, "read SLO manifest for %s: %w", where, err)
	}
	if opening != json.Delim('[') {
		return fail("SLO manifest for %s is not an entry list", where)
	}
	for decoder.More() {
		if len(entries) == maxEntries {
			return fail("SLO manifest for %s lists more than %d entries", where, maxEntries)
		}
		var fields map[string]any
		if err := decoder.Decode(&fields); err != nil {
			return budgeted(err, "parse SLO manifest entry %d of %s: %w", len(entries), where, err)
		}
		entry, err := parseSLOEntry(fields)
		if err != nil {
			return fail("SLO manifest entry %d of %s: %w", len(entries), where, err)
		}
		entries = append(entries, entry)
	}
	if closing, err := decoder.Token(); err != nil || closing != json.Delim(']') {
		return budgeted(err, "SLO manifest for %s is not a complete entry list", where)
	}
	// A clean end, not merely an end: errors.Is would also accept an EOF
	// joined with a read failure, which is a body that did not finish.
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
	return entries, nil
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
	if slash == len(path)-1 {
		return nil, fmt.Errorf("segment path %q names no object", name)
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
