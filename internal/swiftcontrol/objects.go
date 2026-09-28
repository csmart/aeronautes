package swiftcontrol

import (
	"context"
	"fmt"
	"iter"
	"strings"
	"time"

	"github.com/ncw/swift/v2"
)

// ListingEntry is one typed row of an object listing (§14): an object or
// a prefix, never a flattened fake inode. Consumers type-switch.
type ListingEntry interface{ isListingEntry() }

// ObjectEntry is a real object row. Listing rows are cheap classification
// only — authoritative state (DLO/SLO detection included) comes from a
// selection-triggered HEAD, never from listing every row (§14.2).
type ObjectEntry struct {
	Name         string
	Bytes        int64
	ETag         string
	LastModified time.Time
	ContentType  string
	// SLOListed is set when the listing itself carried an slo_etag for
	// this row — a cheap hint, not authoritative classification.
	SLOListed bool
	// SLOETag is that slo_etag value: the MD5 of the concatenated
	// segment ETags. Matching values (with matching logical Bytes) prove
	// identical segmentation — the listing-grade §28.4
	// IdenticalSLOManifest evidence.
	SLOETag string
}

func (ObjectEntry) isListingEntry() {}

// PrefixEntry is a virtual-folder row from a delimiter listing (§14.1).
// The listing cannot say whether an explicit marker object backs it: the
// server folds any object named exactly at the boundary — marker or not —
// into this one group row. PrefixDetail answers that question on
// selection (§14.2).
type PrefixEntry struct {
	Prefix string // includes the trailing delimiter
}

func (PrefixEntry) isListingEntry() {}

// ObjectListOptions controls an object listing stream.
type ObjectListOptions struct {
	// Prefix is the server-side prefix (§16); include the trailing "/"
	// while inside a virtual folder.
	Prefix string
	// PageSize is the server page size; 0 means the §15.1 default.
	PageSize int
	// Marker resumes the listing after this name — the last row of a
	// previously consumed page (for a PrefixEntry, its Prefix value; the
	// server then skips the whole group).
	Marker string
	// Deep lists the flat namespace with no delimiter: every real object
	// including folder markers, no PrefixEntry rows. This is the stream
	// jobs consume; the browse UI uses the default delimiter view.
	Deep bool
}

// ObjectPage is one listing response as this client validated it: the rows
// it carried, and whether the server filled the page it was asked for.
//
// Full is evidence about the CONTAINER, not a continuation signal: a page that
// is not full does not mean the listing is finished, because the server may
// answer with fewer rows than the limit and still have more (§15, ADR 0021).
// Only an empty page ends a listing, and the iterator consumes that itself.
// What Full supports is a caller deciding whether another REQUEST is worth
// making, which is a question rows alone cannot answer.
type ObjectPage struct {
	Entries []ListingEntry
	Full    bool
}

// ObjectPages streams one container's listing one validated response at a
// time, with the same bounded-memory contract as Containers (§15): at most one
// page is held. Every name check, marker progress rule and deep/delimiter
// decision Objects applies is applied here, because Objects is this function
// with the pages flattened.
//
// In the default delimiter view the server groups everything below each child
// prefix into one subdir row — including any object named exactly at the
// boundary (verified against real Swift; §14.1) — and an object named exactly
// at a slash-terminated folder's own prefix is skipped: it belongs to that
// folder's PrefixDetail, not to its child rows. Deep mode reports every real
// object verbatim.
func (s *Session) ObjectPages(ctx context.Context, container string, opts ObjectListOptions) iter.Seq2[ObjectPage, error] {
	pageSize := opts.PageSize
	if pageSize <= 0 {
		pageSize = defaultPageSize
	}
	var delimiter rune
	if !opts.Deep {
		delimiter = '/'
	}

	return func(yield func(ObjectPage, error) bool) {
		marker := opts.Marker
		for {
			if err := ctx.Err(); err != nil {
				yield(ObjectPage{}, Normalize(err))
				return
			}
			page, err := s.conn.Objects(ctx, container, &swift.ObjectsOpts{
				Limit:     pageSize,
				Prefix:    opts.Prefix,
				Marker:    marker,
				Delimiter: delimiter,
			})
			if err != nil {
				yield(ObjectPage{}, Normalize(err))
				return
			}
			if len(page) == 0 {
				return
			}
			// The raw page owns marker progress, including self rows that
			// the delimiter view will filter out below.
			last := marker
			for _, row := range page {
				if err := validateListingName(row.Name, opts.Prefix, last); err != nil {
					yield(ObjectPage{}, Normalize(err))
					return
				}
				if opts.Deep && row.PseudoDirectory {
					yield(ObjectPage{}, Normalize(fmt.Errorf("deep listing contains a prefix row %q", row.Name)))
					return
				}
				last = row.Name
			}

			entries := make([]ListingEntry, 0, len(page))
			for _, row := range page {
				switch {
				case row.PseudoDirectory:
					entries = append(entries, PrefixEntry{Prefix: row.Name})
				case !opts.Deep && strings.HasSuffix(opts.Prefix, "/") && row.Name == opts.Prefix:
					// The folder's own object belongs to PrefixDetail.
					continue
				default:
					entries = append(entries, objectEntry(row))
				}
			}
			if !yield(ObjectPage{Entries: entries, Full: len(page) >= pageSize}, nil) {
				return
			}
			marker = last
		}
	}
}

// Objects streams one container's listing row by row: ObjectPages with the
// pages flattened, which is what almost every caller wants.
func (s *Session) Objects(ctx context.Context, container string, opts ObjectListOptions) iter.Seq2[ListingEntry, error] {
	return func(yield func(ListingEntry, error) bool) {
		for page, err := range s.ObjectPages(ctx, container, opts) {
			if err != nil {
				yield(nil, err)
				return
			}
			for _, entry := range page.Entries {
				if err := ctx.Err(); err != nil {
					yield(nil, Normalize(err))
					return
				}
				if !yield(entry, nil) {
					return
				}
			}
		}
	}
}

func objectEntry(o swift.Object) ObjectEntry {
	return ObjectEntry{
		Name:         o.Name,
		Bytes:        o.Bytes,
		ETag:         o.Hash,
		LastModified: o.LastModified,
		ContentType:  o.ContentType,
		SLOListed:    o.SLOHash != "",
		SLOETag:      o.SLOHash,
	}
}
