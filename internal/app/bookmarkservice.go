package app

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"

	"github.com/csmart/aeronautes/internal/bookmarks"
	"github.com/csmart/aeronautes/internal/jobs"
	"github.com/csmart/aeronautes/internal/secrets"
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
		Unverified: status.Unverified, CleanupReason: status.CleanupReason,
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
	s.cleanupCredentials(ctx)
	return toBookmarkDTO(saved), nil
}

// Verify authenticates the details as typed and saves nothing, so the
// operator learns whether the endpoint accepts the user and key before the
// bookmark and its key are stored. The session is built the way a saved
// bookmark's is, so the endpoint policy and the TLS trust apply unchanged;
// the result names the account and its HEAD counts and nothing else.
func (s *BookmarkService) Verify(ctx context.Context, dto BookmarkDTO, secret string) (VerifyResultDTO, error) {
	config, err := s.verifyConfig(ctx, dto, secret)
	if err != nil {
		return VerifyResultDTO{}, err
	}
	session, err := swiftcontrol.New(config)
	if err != nil {
		return VerifyResultDTO{}, err
	}
	if err := session.Authenticate(ctx); err != nil {
		return VerifyResultDTO{}, err
	}
	portrait, err := session.AccountPortrait(ctx)
	if err != nil {
		return VerifyResultDTO{}, err
	}
	storageURL := session.StorageURL()
	return VerifyResultDTO{
		Account:    storageURL[strings.LastIndex(storageURL, "/")+1:],
		Containers: portrait.Containers,
		Objects:    portrait.Objects,
		BytesUsed:  portrait.BytesUsed,
	}, nil
}

var errVerifyNeedsKey = errors.New("enter the API key to verify these details")

// verifyConfig is the session config for the details as typed. A typed key
// is used as typed. Without one, an existing bookmark's stored key serves
// only while the binding it was saved under (auth URL, username, storage
// override, TLS trust) is unchanged, the rule Save applies (ADR 0015), so a
// stored key never reaches an endpoint it was not saved for. The stored CA
// bundle stands in when the editor left the PEM empty.
func (s *BookmarkService) verifyConfig(ctx context.Context, dto BookmarkDTO, secret string) (swiftcontrol.Config, error) {
	typed := bookmarks.Bookmark{
		AuthURL:            dto.AuthURL,
		Username:           dto.Username,
		StorageURLOverride: dto.StorageURLOverride,
		TLSMode:            swiftcontrol.TLSMode(dto.TLSMode),
	}
	var caPEM []byte
	if dto.CAPem != "" {
		caPEM = []byte(dto.CAPem)
	}
	if dto.ID == "" {
		if secret == "" {
			return swiftcontrol.Config{}, errVerifyNeedsKey
		}
		if typed.TLSMode == swiftcontrol.TLSModeCustomCA && caPEM == nil {
			return swiftcontrol.Config{}, fmt.Errorf("custom-CA TLS needs a CA bundle (paste the PEM)")
		}
		return typed.SwiftConfig([]byte(secret), caPEM), nil
	}

	existing, err := s.store.Get(ctx, dto.ID)
	if err != nil {
		return swiftcontrol.Config{}, err
	}
	stored, _, storedErr := s.store.ConnectionConfig(ctx, dto.ID)
	sameBinding := existing.AuthURL == dto.AuthURL && existing.Username == dto.Username &&
		existing.StorageURLOverride == dto.StorageURLOverride && string(existing.TLSMode) == dto.TLSMode && caPEM == nil
	if secret == "" {
		if !sameBinding {
			return swiftcontrol.Config{}, fmt.Errorf("re-enter the API key to verify changed details")
		}
		if storedErr != nil {
			if errors.Is(storedErr, secrets.ErrNotFound) || errors.Is(storedErr, secrets.ErrAuthorityUnreachable) {
				return swiftcontrol.Config{}, fmt.Errorf("no saved key is available; enter it to verify: %w", storedErr)
			}
			return swiftcontrol.Config{}, storedErr
		}
		return stored, nil
	}
	if typed.TLSMode == swiftcontrol.TLSModeCustomCA && caPEM == nil {
		if storedErr != nil || len(stored.TLS.CACertPEM) == 0 {
			return swiftcontrol.Config{}, fmt.Errorf("paste the CA bundle again to verify with a new key")
		}
		caPEM = stored.TLS.CACertPEM
	}
	return typed.SwiftConfig([]byte(secret), caPEM), nil
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
	s.cleanupCredentials(ctx)
	return nil
}

// cleanupCredentials runs one physical cleanup pass after a delete or a
// key change (ADR 0076). The logical operation has already succeeded and
// is reported as such; the pass reports through the credential status,
// and it is not abandoned when the request that started it is cancelled,
// because an abandoned native delete is unresolved rather than undone.
func (s *BookmarkService) cleanupCredentials(ctx context.Context) {
	if _, err := s.store.Cleanup(context.WithoutCancel(ctx), ""); err != nil {
		log.Printf("credential cleanup: %v", err)
	}
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
