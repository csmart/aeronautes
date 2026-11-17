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

// SLOManifest GETs ?multipart-manifest=get and parses the manifest into
// the exact model. It does not verify that the object is an SLO first —
// callers hold an ObjectDetail with ClassSLO from the HEAD (§20).
func (s *Session) SLOManifest(ctx context.Context, container, name string) (*SLOManifest, error) {
	// The library's operation wrappers authenticate lazily; a direct Call
	// (needed for the query parameter) must do the same, and refresh its
	// target on mid-call re-auth. This is the inspector's read, so a refused
	// token is re-authenticated transparently.
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
	return parseSLOManifest(resp.Body, container, name)
}

// The bounds the reviewed-graph re-read is parsed within, matching the
// project's manifest limits elsewhere, so the live graph that authorises a
// DELETE is read whole within the same budget rather than trusted unbounded.
const (
	maxReviewedManifestEntries = 10000
	maxReviewedManifestBytes   = 8 << 20
)

// sloManifestWith reads a static large object's manifest under credentials
// the caller already holds, pinned to that snapshot's storage URL and token
// with no re-authentication callback, so a segment job reads the graph
// immediately before its comparison HEAD under the one snapshot and names the
// same storage authority the comparison and the DELETE will use (ADR 0095
// decision 8). A token refused here invalidates the token and surfaces as
// unauthorized, and the caller restarts the whole comparison under fresh
// credentials rather than reading the graph from one account and deleting at
// another. It is never the inspector's read, which re-authenticates in place.
func (s *Session) sloManifestWith(ctx context.Context, credentials StorageCredentials, container, name string, maxEntries int, maxBytes int64) (manifest *SLOManifest, err error) {
	if container == "" || name == "" {
		return nil, &SwiftError{Kind: KindUnknown, Message: "read SLO manifest: the container and object name must both be present"}
	}
	target := credentials.StorageURL + "/" + escapeObjectPath(container) + "/" + escapeObjectPath(name) + "?multipart-manifest=get"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, Normalize(err)
	}
	req.Header.Set("X-Auth-Token", credentials.Token)
	resp, err := s.raw.Do(req)
	if err != nil {
		return nil, Normalize(err)
	}
	// A graph that could not be read and closed cleanly is not evidence a
	// DELETE may rest on, so the close error joins the result.
	defer func() { err = errors.Join(err, resp.Body.Close()) }()
	transID := transIDOf(resp.Header)
	switch {
	case resp.StatusCode == http.StatusUnauthorized:
		s.ForceReauth()
		return nil, &SwiftError{Kind: KindUnauthorized, HTTPStatus: resp.StatusCode, Message: "read SLO manifest: the token was refused", TransID: transID}
	case resp.StatusCode == http.StatusNotFound:
		return nil, &SwiftError{Kind: KindNotFound, HTTPStatus: resp.StatusCode, Message: "read SLO manifest: not found", TransID: transID}
	}
	// This graph authorises a DELETE, so it is read as an operation's
	// authoritative record and not as a display: a complete, bounded response —
	// HTTP 200 with no Content-Range, parsed whole with a clean end and no
	// trailing content — on the same contract the strict readers use. A partial
	// body (HTTP 206, or 200 with a Content-Range) or a valid array followed by
	// other bytes is refused rather than credited as the manifest (ADR 0081
	// decision 5), because a short or padded read credited as the exact graph
	// would authorise deleting against a membership the server never sent.
	if !completeManifestResponse(resp) {
		return nil, fmt.Errorf("read SLO manifest for %s/%s requires a complete HTTP 200 without Content-Range (received HTTP %d)", container, name, resp.StatusCode)
	}
	entries, err := parseSLOManifestEntries(resp.Body, maxEntries, maxBytes, container+"/"+name)
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

// parseSLOManifest decodes the ?multipart-manifest=get body into the exact
// model. Field order inside entries doesn't matter, entry order does; numbers
// stay json.Number so large sizes survive the round-trip untouched.
func parseSLOManifest(body io.Reader, container, name string) (*SLOManifest, error) {
	decoder := json.NewDecoder(body)
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

// SLOManifestEntries reads the manifest strictly and within bounds, as its
// ordered entries, for a caller that decides something from which entries
// it names.
//
// It exists because SLOManifest is a display read: it decodes one array
// without checking the status, the absence of a Content-Range, a clean
// end, or any byte bound, so a partial or trailing-garbage response parses
// as a manifest. This read requires a complete response, HTTP 200 with no
// Content-Range, on the same contract OpenObjectContent uses, because a
// partial body that happens to parse is not the manifest. It never reads
// past its byte budget: the budget bounds the decoder's input, including
// the opening token and the read that confirms the end of the stream,
// rather than being checked once an entry has already been buffered. It
// refuses instead of truncating once maxEntries is passed, refuses
// trailing content after the entry list, and refuses when the body cannot
// be closed cleanly. A response this build will not read whole is an
// error here rather than a shorter answer.
func (s *Session) SLOManifestEntries(ctx context.Context, container, name string, maxEntries int, maxBytes int64) (entries []SLOEntry, err error) {
	return readSLOManifest(ctx, s, container, name, func(body io.Reader, where string) ([]SLOEntry, error) {
		return parseSLOManifestEntries(body, maxEntries, maxBytes, where)
	})
}

// readSLOManifest performs the manifest GET and the response checks of the
// strict read: HTTP 200, no Content-Range, and the body closed with its
// error joined, because a read that could not be completed cleanly is not
// evidence.
func readSLOManifest[T any](ctx context.Context, s *Session, container, name string,
	parse func(io.Reader, string) (T, error)) (result T, err error) {
	var zero T
	credentials, err := s.Credentials(ctx, 0)
	if err != nil {
		return zero, err
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
		return zero, WithTransID(Normalize(err), headers)
	}
	defer func() { err = errors.Join(err, resp.Body.Close()) }()

	if !completeManifestResponse(resp) {
		return zero, fmt.Errorf("SLO manifest for %s/%s requires HTTP 200 without Content-Range (received HTTP %d)",
			container, name, resp.StatusCode)
	}
	body := &countingReader{reader: resp.Body}
	result, err = parse(body, container+"/"+name)
	if err != nil && body.read == 0 {
		return zero, fmt.Errorf("SLO manifest for %s/%s: %w", container, name, ErrManifestNotReturned)
	}
	return result, err
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
