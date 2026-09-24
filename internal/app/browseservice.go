package app

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/csmart/aeronautes/internal/evidence"
	"github.com/csmart/aeronautes/internal/swiftcontrol"
)

// BrowseService is the read-only browsing surface (ADR 0011): connect,
// account portrait, and paged container/object listings. Pages are
// bounded — the frontend asks for the next page with the returned
// marker, never for "everything" (§15, K-PERF-*).
type BrowseService struct {
	connections *Connections
	evidence    *evidence.Store
}

// NewBrowseService builds the service over the connection registry and
// the evidence store (container rows carry their §24 classification).
func NewBrowseService(connections *Connections, ev *evidence.Store) *BrowseService {
	return &BrowseService{connections: connections, evidence: ev}
}

// Connect authenticates a bookmark and returns the account portrait.
func (s *BrowseService) Connect(ctx context.Context, bookmarkID string) (AccountDTO, error) {
	if err := s.connections.Connect(ctx, bookmarkID); err != nil {
		return AccountDTO{}, err
	}
	return s.Account(ctx, bookmarkID)
}

// Account binds the portrait to the bookmark the caller intended to read.
func (s *BrowseService) Account(ctx context.Context, bookmarkID string) (AccountDTO, error) {
	session, scope, _, err := s.scopedSnapshot(bookmarkID)
	if err != nil {
		return AccountDTO{}, err
	}
	p, err := session.AccountPortrait(ctx)
	if err != nil {
		return AccountDTO{}, err
	}
	storageURL := session.StorageURL()
	return AccountDTO{
		Scope:           scope,
		Name:            storageURL[strings.LastIndex(storageURL, "/")+1:],
		Containers:      p.Containers,
		Objects:         p.Objects,
		BytesUsed:       p.BytesUsed,
		QuotaBytes:      p.QuotaBytes,
		Meta:            p.Meta,
		TempURLKeySlots: p.TempURLKeySlots,
	}, nil
}

// Capabilities returns the connect-time capability snapshot (§10) the
// UI gates on: storage policies for the create picker, versioning flags
// for the configuration editor.
func (s *BrowseService) Capabilities(ctx context.Context, bookmarkID string) (CapabilitiesDTO, error) {
	_, scope, caps, err := s.scopedSnapshot(bookmarkID)
	if err != nil {
		return CapabilitiesDTO{}, err
	}
	if caps == nil {
		return CapabilitiesDTO{Source: "unknown", Scope: scope}, nil
	}
	dto := CapabilitiesDTO{
		Scope:           scope,
		Source:          string(caps.Source),
		MaxFileSize:     caps.MaxFileSize(),
		VersioningFlags: caps.VersioningAllowedFlags(),
		SLOSupported:    caps.State("slo") == swiftcontrol.CapabilitySupported,
	}
	for _, policy := range caps.StoragePolicies() {
		dto.StoragePolicies = append(dto.StoragePolicies, StoragePolicyDTO{
			Name:    policy.Name,
			Default: policy.Default,
		})
	}
	return dto, nil
}

// A later Connect may replace the active account during the HEAD. Keep
// identity and capabilities with the validated session instead of resolving
// the active connection again after remote I/O (ADR 0063).
func (s *BrowseService) scopedSnapshot(bookmarkID string) (*swiftcontrol.Session, ListingScopeDTO, *swiftcontrol.Capabilities, error) {
	if bookmarkID == "" {
		return nil, ListingScopeDTO{}, nil, fmt.Errorf("%w: name the bookmark the read is for", ErrNotConnected)
	}
	entry, id, caps, err := s.connections.activeSnapshot()
	if err != nil {
		return nil, ListingScopeDTO{}, nil, err
	}
	if id != bookmarkID {
		return nil, ListingScopeDTO{}, nil, ErrAccountScopeChanged
	}
	return entry.session, ListingScopeDTO{BookmarkID: id, Generation: entry.generation}, caps, nil
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

// ErrAccountScopeChanged refuses a listing read issued under a stale belief
// about which account is connected. A listing names no account in the
// protocol and the connect action is reachable from the bridge, so without
// this a page begun under one account is continued under another and the
// caller sees one listing spanning two (ADR 0049).
var ErrAccountScopeChanged = errors.New("the connected account changed; the listing was not read")

// scopedListing binds a listing read to the account the caller named. The
// caller states the bookmark it believes it is reading; every value used to
// judge that belief is the backend's own, so a caller cannot assert a scope
// it does not have. A mismatch refuses rather than answering from whichever
// connection happens to be active (ADR 0049).
//
// The bookmark's generation is resolved afresh rather than taken from the
// connected endpoint, because a bookmark re-saved to a different account
// WITHOUT reconnecting is exactly the case a cached endpoint value cannot
// see: the endpoint still reports the generation it connected under. The
// resolution is a local store read rather than a request to the cluster.
func (s *BrowseService) scopedListing(ctx context.Context, bookmarkID string) (*swiftcontrol.Session, ListingScopeDTO, error) {
	return s.connections.scopedSession(ctx, bookmarkID)
}

// Containers returns one page of the account's containers starting after
// marker.
func (s *BrowseService) Containers(ctx context.Context, bookmarkID, marker string, limit int) (ContainerPageDTO, error) {
	session, scope, err := s.scopedListing(ctx, bookmarkID)
	if err != nil {
		return ContainerPageDTO{}, err
	}
	limit = clampLimit(limit)

	page := ContainerPageDTO{Rows: []ContainerRowDTO{}, Scope: scope, EffectiveLimit: limit}
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

	// Fold in the §24 classification so the §52 modes are one page fetch.
	names := make([]string, len(page.Rows))
	for i, row := range page.Rows {
		names[i] = row.Name
	}
	classes, err := s.evidence.ClassifyContainers(ctx, scope.BookmarkID, names)
	if err != nil {
		return ContainerPageDTO{}, err
	}
	known := s.connections.settings.known(scope.BookmarkID, scope.Generation, names)
	for i := range page.Rows {
		page.Rows[i].Class = string(classes[page.Rows[i].Name])
		if fact, ok := known[page.Rows[i].Name]; ok {
			page.Rows[i].Settings = &fact
		}
	}
	return page, nil
}

// Objects returns one delimiter-view page of a container under prefix,
// starting after marker.
func (s *BrowseService) Objects(ctx context.Context, bookmarkID, container, prefix, marker string, limit int) (ObjectPageDTO, error) {
	session, scope, err := s.scopedListing(ctx, bookmarkID)
	if err != nil {
		return ObjectPageDTO{}, err
	}
	limit = clampLimit(limit)

	page := ObjectPageDTO{Rows: []ListingRowDTO{}, Scope: scope, EffectiveLimit: limit}
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
			row = ListingRowDTO{Kind: "prefix", Name: v.Prefix}
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
