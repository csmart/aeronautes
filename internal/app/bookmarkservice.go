package app

import (
	"context"
	"errors"
	"fmt"

	"github.com/csmart/aeronautes/internal/bookmarks"
	"github.com/csmart/aeronautes/internal/jobs"
	"github.com/csmart/aeronautes/internal/swiftcontrol"
)

// BookmarkService is the bookmark CRUD surface (§11, ADR 0011). A
// credential crosses the bridge only as the Save argument, straight into
// the SecretStore; it is never readable back (K-SEC-002).
type BookmarkService struct {
	store       *bookmarks.Store
	connections *Connections
	jobs        *jobs.Store
}

// NewBookmarkService builds the service over the store. connections
// and jobStore may be nil in narrow tests; the lifecycle guards then
// no-op.
func NewBookmarkService(store *bookmarks.Store, connections *Connections, jobStore *jobs.Store) *BookmarkService {
	return &BookmarkService{store: store, connections: connections, jobs: jobStore}
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

func (s *BookmarkService) CredentialStatus(ctx context.Context) (CredentialStatusDTO, error) {
	status, err := s.store.CredentialStatus(ctx)
	if err != nil {
		return CredentialStatusDTO{}, err
	}
	return CredentialStatusDTO{
		Retired: status.Retired, Unfinished: status.Unfinished, Saving: status.Saving,
		Unverified: status.Unverified,
	}, nil
}

// Save inserts (empty id) or updates a bookmark. A non-empty secret
// replaces the stored credential; an empty one retains it only while
// the authentication and trust binding stays the same.
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
	if b.TLSMode != swiftcontrol.TLSModeCustomCA {
		b.TLSCAID = ""
	} else if b.TLSCAID == "" && dto.CAPem == "" {
		return BookmarkDTO{}, fmt.Errorf("custom-CA TLS needs a CA bundle (paste the PEM)")
	}

	var secretBytes []byte
	if secret != "" {
		secretBytes = []byte(secret)
	}
	var caPEM []byte
	if dto.CAPem != "" {
		caPEM = []byte(dto.CAPem)
	}
	saved, err := s.store.SaveWithCA(ctx, b, secretBytes, caPEM)
	if err != nil {
		if errors.Is(err, bookmarks.ErrPublicationUnresolved) && s.connections != nil {
			s.connections.Invalidate(dto.ID)
		}
		return BookmarkDTO{}, err
	}
	if dto.ID != "" && s.connections != nil {
		// An edited endpoint, credential, or TLS mode must not keep
		// operating through the old cached session (review H-12 — the
		// first fix for this anchored on the wrong line and no-oped).
		s.connections.Invalidate(dto.ID)
	}
	return toBookmarkDTO(saved), nil
}

// Delete removes a bookmark. It refuses while any non-terminal job
// references the bookmark (a running transfer must not lose its
// endpoint identity mid-write), and invalidates any cached session so
// a deleted bookmark cannot keep operating (§65.9 item 15).
func (s *BookmarkService) Delete(ctx context.Context, id string) error {
	if s.jobs != nil {
		active, err := s.jobs.ActiveForBookmark(ctx, id)
		if err != nil {
			return err
		}
		if active > 0 {
			return fmt.Errorf("%d running or queued job(s) still use this bookmark — cancel them first", active)
		}
	}
	if err := s.store.Delete(ctx, id); err != nil {
		if errors.Is(err, bookmarks.ErrPublicationUnresolved) && s.connections != nil {
			s.connections.Invalidate(id)
		}
		return err
	}
	if s.connections != nil {
		s.connections.Invalidate(id)
	}
	return nil
}

func toBookmarkDTO(b bookmarks.Bookmark) BookmarkDTO {
	return BookmarkDTO{
		ID:                 b.ID,
		DisplayName:        b.DisplayName,
		AuthURL:            b.AuthURL,
		Username:           b.Username,
		StorageURLOverride: b.StorageURLOverride,
		TLSMode:            string(b.TLSMode),
		HasCA:              b.TLSCAID != "",
		HasCredential:      b.HasCredential,
	}
}
