package app

import (
	"context"
	"time"

	"github.com/csmart/aeronautes/internal/swiftcontrol"
)

// BrowseService is the read-only browsing surface (ADR 0011): connect,
// account portrait, and paged container/object listings. Pages are
// bounded — the frontend asks for the next page with the returned
// marker, never for "everything" (§15, K-PERF-*).
type BrowseService struct {
	connections *Connections
}

// NewBrowseService builds the service over the connection registry.
func NewBrowseService(connections *Connections) *BrowseService {
	return &BrowseService{connections: connections}
}

// Connect authenticates a bookmark and returns the account portrait.
func (s *BrowseService) Connect(ctx context.Context, bookmarkID string) (AccountDTO, error) {
	if err := s.connections.Connect(ctx, bookmarkID); err != nil {
		return AccountDTO{}, err
	}
	return s.Account(ctx)
}

// Account returns the active connection's account portrait.
func (s *BrowseService) Account(ctx context.Context) (AccountDTO, error) {
	session, err := s.connections.Session()
	if err != nil {
		return AccountDTO{}, err
	}
	p, err := session.AccountPortrait(ctx)
	if err != nil {
		return AccountDTO{}, err
	}
	return AccountDTO{
		Name:            s.connections.AccountName(),
		Containers:      p.Containers,
		Objects:         p.Objects,
		BytesUsed:       p.BytesUsed,
		QuotaBytes:      p.QuotaBytes,
		Meta:            p.Meta,
		TempURLKeySlots: p.TempURLKeySlots,
	}, nil
}

// maxPageRows caps a single bridge page regardless of what the frontend
// asks for; larger requests page, they do not balloon.
const maxPageRows = 5000

func clampLimit(limit int) int {
	if limit <= 0 || limit > maxPageRows {
		return maxPageRows
	}
	return limit
}

// Containers returns one page of the account's containers starting after
// marker.
func (s *BrowseService) Containers(ctx context.Context, marker string, limit int) (ContainerPageDTO, error) {
	session, err := s.connections.Session()
	if err != nil {
		return ContainerPageDTO{}, err
	}
	limit = clampLimit(limit)

	page := ContainerPageDTO{Rows: []ContainerRowDTO{}}
	for entry, err := range session.Containers(ctx, swiftcontrol.ListOptions{Marker: marker, PageSize: limit}) {
		if err != nil {
			return ContainerPageDTO{}, err
		}
		page.Rows = append(page.Rows, ContainerRowDTO{Name: entry.Name, Count: entry.Count, Bytes: entry.Bytes})
		if len(page.Rows) == limit {
			page.NextMarker = entry.Name
			break
		}
	}
	return page, nil
}

// Objects returns one delimiter-view page of a container under prefix,
// starting after marker.
func (s *BrowseService) Objects(ctx context.Context, container, prefix, marker string, limit int) (ObjectPageDTO, error) {
	session, err := s.connections.Session()
	if err != nil {
		return ObjectPageDTO{}, err
	}
	limit = clampLimit(limit)

	page := ObjectPageDTO{Rows: []ListingRowDTO{}}
	opts := swiftcontrol.ObjectListOptions{Prefix: prefix, Marker: marker, PageSize: limit}
	for entry, err := range session.Objects(ctx, container, opts) {
		if err != nil {
			return ObjectPageDTO{}, err
		}
		var row ListingRowDTO
		switch v := entry.(type) {
		case swiftcontrol.ObjectEntry:
			row = ListingRowDTO{
				Kind:         "object",
				Name:         v.Name,
				Bytes:        v.Bytes,
				ETag:         v.ETag,
				LastModified: v.LastModified.UTC().Format(time.RFC3339),
				ContentType:  v.ContentType,
				SLOListed:    v.SLOListed,
			}
		case swiftcontrol.PrefixEntry:
			row = ListingRowDTO{Kind: "prefix", Name: v.Prefix, MarkerBacked: v.MarkerBacked}
		default:
			continue
		}
		page.Rows = append(page.Rows, row)
		if len(page.Rows) == limit {
			page.NextMarker = row.Name
			break
		}
	}
	return page, nil
}
