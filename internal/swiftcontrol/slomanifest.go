package swiftcontrol

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
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
