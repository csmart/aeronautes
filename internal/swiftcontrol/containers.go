package swiftcontrol

import (
	"context"
	"iter"

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
			page, err := s.conn.Containers(ctx, &swift.ContainersOpts{
				Limit:  pageSize,
				Prefix: opts.Prefix,
				Marker: marker,
			})
			if err != nil {
				yield(ContainerEntry{}, Normalize(err))
				return
			}
			for _, c := range page {
				entry := ContainerEntry{Name: c.Name, Count: c.Count, Bytes: c.Bytes}
				if !yield(entry, nil) {
					return
				}
			}
			// A short page means the listing is exhausted. (Servers that
			// return short pages while holding more data are the §43.5
			// compatibility case, handled by typed settings later.)
			if len(page) < pageSize {
				return
			}
			marker = page[len(page)-1].Name
		}
	}
}
