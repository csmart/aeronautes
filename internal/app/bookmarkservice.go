package app

import (
	"context"

	"github.com/csmart/aeronautes/internal/bookmarks"
	"github.com/csmart/aeronautes/internal/swiftcontrol"
)

// BookmarkService is the bookmark CRUD surface (§11, ADR 0011). A
// credential crosses the bridge only as the Save argument, straight into
// the SecretStore; it is never readable back (K-SEC-002).
type BookmarkService struct {
	store *bookmarks.Store
}

// NewBookmarkService builds the service over the store.
func NewBookmarkService(store *bookmarks.Store) *BookmarkService {
	return &BookmarkService{store: store}
}

// List returns every bookmark, display-name ordered.
func (s *BookmarkService) List(ctx context.Context) ([]BookmarkDTO, error) {
	list, err := s.store.List(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]BookmarkDTO, 0, len(list))
	for _, b := range list {
		out = append(out, toBookmarkDTO(b))
	}
	return out, nil
}

// Save inserts (empty id) or updates a bookmark. A non-empty secret
// replaces the stored credential; an empty one keeps whatever exists.
func (s *BookmarkService) Save(ctx context.Context, dto BookmarkDTO, secret string) (BookmarkDTO, error) {
	b := bookmarks.Bookmark{}
	if dto.ID != "" {
		// Start from the stored bookmark so fields this DTO does not carry
		// (segment placement, performance profile) survive the update.
		existing, err := s.store.Get(ctx, dto.ID)
		if err != nil {
			return BookmarkDTO{}, err
		}
		b = existing
	}
	b.DisplayName = dto.DisplayName
	b.AuthURL = dto.AuthURL
	b.Username = dto.Username
	b.StorageURLOverride = dto.StorageURLOverride
	b.TLSMode = swiftcontrol.TLSMode(dto.TLSMode)

	var secretBytes []byte
	if secret != "" {
		secretBytes = []byte(secret)
	}
	saved, err := s.store.Save(ctx, b, secretBytes)
	if err != nil {
		return BookmarkDTO{}, err
	}
	return toBookmarkDTO(saved), nil
}

// Delete removes a bookmark and its stored credential.
func (s *BookmarkService) Delete(ctx context.Context, id string) error {
	return s.store.Delete(ctx, id)
}

func toBookmarkDTO(b bookmarks.Bookmark) BookmarkDTO {
	return BookmarkDTO{
		ID:                 b.ID,
		DisplayName:        b.DisplayName,
		AuthURL:            b.AuthURL,
		Username:           b.Username,
		StorageURLOverride: b.StorageURLOverride,
		TLSMode:            string(b.TLSMode),
		HasCredential:      b.HasCredential,
	}
}
