package swiftcontrol

import (
	"context"
	"iter"
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

// Objects streams one container's listing page by page with the same
// bounded-memory contract as Containers (§15). In the default delimiter
// view the server groups everything below each child prefix into one
// subdir row — including any object named exactly at the boundary
// (verified against real Swift; §14.1) — and an object named exactly at
// the *listing's own* prefix is skipped here: it belongs to this level's
// PrefixDetail, not to the child rows. Deep mode reports every real
// object verbatim.
func (s *Session) Objects(ctx context.Context, container string, opts ObjectListOptions) iter.Seq2[ListingEntry, error] {
	pageSize := opts.PageSize
	if pageSize <= 0 {
		pageSize = defaultPageSize
	}
	var delimiter rune
	if !opts.Deep {
		delimiter = '/'
	}

	return func(yield func(ListingEntry, error) bool) {
		marker := opts.Marker
		for {
			page, err := s.conn.Objects(ctx, container, &swift.ObjectsOpts{
				Limit:     pageSize,
				Prefix:    opts.Prefix,
				Marker:    marker,
				Delimiter: delimiter,
			})
			if err != nil {
				yield(nil, Normalize(err))
				return
			}

			for _, row := range page {
				switch {
				case row.PseudoDirectory:
					if !yield(PrefixEntry{Prefix: row.Name}, nil) {
						return
					}
				case !opts.Deep && row.Name == opts.Prefix:
					// This level's own marker/self object.
					continue
				default:
					if !yield(objectEntry(row), nil) {
						return
					}
				}
			}

			if len(page) < pageSize {
				return
			}
			marker = page[len(page)-1].Name
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
	}
}
