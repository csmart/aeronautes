package bookmarks

import (
	"context"
	"database/sql"
)

// Querier is the read surface ExportAll needs — the pooled reader or a read
// transaction — so a caller can fold the bookmark read into a wider snapshot.
type Querier interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

// ExportRecord is a bookmark's complete non-secret configuration for a
// settings backup (ADR 0098): every restorable field and the stored PEM of
// the custom CA it trusts, and never the credential or its secret reference.
// The caller re-encodes CACertPEM to certificates only before it leaves the
// process, so a bundle that also held a private key cannot travel in a
// backup.
type ExportRecord struct {
	DisplayName        string
	AuthURL            string
	Username           string
	StorageURLOverride string
	TLSMode            string
	PerformanceProfile string
	SegmentPlacement   string
	ChunkSizeBytes     int64
	CACertPEM          string
}

// ExportAll reads every bookmark's complete non-secret configuration in one
// query, so the returned set is a single coherent snapshot. The order is
// stable (by creation then id) so the same configuration produces the same
// file. The credential is never read; only the CA certificate is, which is
// public trust material, not a secret (K-SEC-003).
func (st *Store) ExportAll(ctx context.Context, q Querier) ([]ExportRecord, error) {
	rows, err := q.QueryContext(ctx, selectColumns+` ORDER BY created_at, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ExportRecord
	for rows.Next() {
		r, err := scanRecord(rows.Scan)
		if err != nil {
			return nil, err
		}
		out = append(out, ExportRecord{
			DisplayName:        r.Bookmark.DisplayName,
			AuthURL:            r.Bookmark.AuthURL,
			Username:           r.Bookmark.Username,
			StorageURLOverride: r.Bookmark.StorageURLOverride,
			TLSMode:            string(r.Bookmark.TLSMode),
			PerformanceProfile: r.Bookmark.PerformanceProfile,
			SegmentPlacement:   string(r.Bookmark.SegmentPlacement),
			ChunkSizeBytes:     r.Bookmark.ChunkSizeBytes,
			CACertPEM:          r.ca.String,
		})
	}
	return out, rows.Err()
}
