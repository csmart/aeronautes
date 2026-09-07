package swiftcontrol

import (
	"context"
	"fmt"
	"iter"
	"strings"

	"github.com/ncw/swift/v2"
)

// defaultPageSize is the §15.1 starting page size: try high (Swift's own
// per-request listing cap) and let compatibility settings lower it later.
const defaultPageSize = 10000

// ContainerEntry is one row of the account's container listing.
type ContainerEntry struct {
	Name  string
	Count int64
	Bytes int64
}

// ListOptions controls a listing stream.
type ListOptions struct {
	// PageSize is the server page size; 0 means the §15.1 default.
	PageSize int
	// Prefix limits results to names beginning with it (server-side).
	Prefix string
	// Marker resumes the listing after this name — the last row of a
	// previously consumed page.
	Marker string
}

// Containers streams the account's containers page by page (§15): each
// page is fetched only as the consumer advances, so breaking out of the
// loop stops further requests and memory stays bounded regardless of
// account size. On failure the last yielded pair carries the normalized
// error and the stream ends.
func (s *Session) Containers(ctx context.Context, opts ListOptions) iter.Seq2[ContainerEntry, error] {
	pageSize := opts.PageSize
	if pageSize <= 0 {
		pageSize = defaultPageSize
	}
	return func(yield func(ContainerEntry, error) bool) {
		marker := opts.Marker
		for {
			if err := ctx.Err(); err != nil {
				yield(ContainerEntry{}, Normalize(err))
				return
			}
			page, err := s.conn.Containers(ctx, &swift.ContainersOpts{
				Limit:  pageSize,
				Prefix: opts.Prefix,
				Marker: marker,
			})
			if err != nil {
				yield(ContainerEntry{}, Normalize(err))
				return
			}
			if len(page) == 0 {
				return
			}
			// Validate before publication so a bad later row cannot make
			// part of this page look accepted.
			last := marker
			for _, row := range page {
				if err := validateListingName(row.Name, opts.Prefix, last); err != nil {
					yield(ContainerEntry{}, Normalize(err))
					return
				}
				last = row.Name
			}
			for _, c := range page {
				if err := ctx.Err(); err != nil {
					yield(ContainerEntry{}, Normalize(err))
					return
				}
				entry := ContainerEntry{Name: c.Name, Count: c.Count, Bytes: c.Bytes}
				if !yield(entry, nil) {
					return
				}
			}
			marker = last
		}
	}
}

func validateListingName(name, prefix, previous string) error {
	if name <= previous {
		return fmt.Errorf("listing name %q did not advance after %q", name, previous)
	}
	if !strings.HasPrefix(name, prefix) {
		return fmt.Errorf("listing name %q is outside prefix %q", name, prefix)
	}
	return nil
}
