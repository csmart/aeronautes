package swiftcontrol

import (
	"context"
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
}

func (ObjectEntry) isListingEntry() {}

// PrefixEntry is a virtual-folder row from a delimiter listing (§14.1).
// MarkerBacked means an explicit zero-byte application/directory marker
// object exists; otherwise the prefix is implied by children only. Both
// are equally navigable.
type PrefixEntry struct {
	Prefix       string // includes the trailing delimiter
	MarkerBacked bool
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
// view, folder markers and subdir rows for the same name are merged into
// a single marker-backed PrefixEntry; an object that merely *looks*
// folder-ish (trailing slash but real bytes or a non-directory type) is
// never converted — it stays an ObjectEntry, possibly alongside an
// implied PrefixEntry for its children.
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
		// pending holds one PrefixEntry candidate so a marker object and
		// its subdir row (identical names, adjacent in the sorted
		// listing, either order, possibly across a page boundary) merge
		// into one row.
		var pending *PrefixEntry
		flushPending := func() bool {
			if pending == nil {
				return true
			}
			entry := *pending
			pending = nil
			return yield(entry, nil)
		}

		marker := opts.Marker
		for {
			page, err := s.conn.Objects(ctx, container, &swift.ObjectsOpts{
				Limit:     pageSize,
				Prefix:    opts.Prefix,
				Marker:    marker,
				Delimiter: delimiter,
			})
			if err != nil {
				if !flushPending() {
					return
				}
				yield(nil, Normalize(err))
				return
			}

			for _, row := range page {
				var candidate *PrefixEntry
				switch {
				case row.PseudoDirectory:
					candidate = &PrefixEntry{Prefix: row.Name}
				case !opts.Deep && isFolderMarker(row):
					candidate = &PrefixEntry{Prefix: row.Name, MarkerBacked: true}
				default:
					if !flushPending() {
						return
					}
					if !yield(objectEntry(row), nil) {
						return
					}
					continue
				}

				if pending != nil && pending.Prefix == candidate.Prefix {
					merged := PrefixEntry{
						Prefix:       candidate.Prefix,
						MarkerBacked: pending.MarkerBacked || candidate.MarkerBacked,
					}
					pending = nil
					if !yield(merged, nil) {
						return
					}
					continue
				}
				if !flushPending() {
					return
				}
				pending = candidate
			}

			if len(page) < pageSize {
				flushPending()
				return
			}
			marker = page[len(page)-1].Name
		}
	}
}

// isFolderMarker matches the marker convention (§14.1): zero bytes,
// application/directory, name ending in the delimiter. Content-type and
// size decide — never the name alone.
func isFolderMarker(o swift.Object) bool {
	return strings.HasSuffix(o.Name, "/") &&
		o.Bytes == 0 &&
		o.ContentType == "application/directory"
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
