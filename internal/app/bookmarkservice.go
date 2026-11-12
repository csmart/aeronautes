package app

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/csmart/aeronautes/internal/applog"
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
	b.LargeObjects = dto.LargeObjects
	connectionFields(&b, dto)
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
// it is closed before returning, since nothing keeps it; and the result
// names the account and its HEAD counts and nothing else.
func (s *BookmarkService) Verify(ctx context.Context, dto BookmarkDTO, secret string) (VerifyResultDTO, error) {
	config, published, err := s.verifyConfig(ctx, dto, secret)
	if err != nil {
		return VerifyResultDTO{}, err
	}
	session, err := swiftcontrol.New(config)
	if err != nil {
		return VerifyResultDTO{}, err
	}
	defer session.Close()
	if err := session.Authenticate(ctx); err != nil {
		return VerifyResultDTO{}, err
	}
	portrait, err := session.AccountPortrait(ctx)
	if err != nil {
		return VerifyResultDTO{}, err
	}
	// A stored key's binding must still be the one acquired now that the
	// exchanges are done, as a saved session checks before publication.
	if published != nil {
		if err := published(ctx); err != nil {
			return VerifyResultDTO{}, err
		}
		// What verified is the saved bookmark itself, so the import's
		// warning no longer applies (ADR 0114 decision 9); details typed
		// but not saved prove nothing about the stored ones.
		if err := s.store.SetVerifyFailure(ctx, dto.ID, ""); err != nil {
			applog.Warnf("clearing a bookmark's verification flag: %v", err)
		}
	}
	storageURL := session.StorageURL()
	return VerifyResultDTO{
		Account:    storageURL[strings.LastIndex(storageURL, "/")+1:],
		Containers: portrait.Containers,
		Objects:    portrait.Objects,
		BytesUsed:  portrait.BytesUsed,
	}, nil
}

var (
	errVerifyNeedsKey    = errors.New("enter the API key to verify these details")
	errVerifyNeedsCA     = errors.New("custom-CA TLS needs a CA bundle (paste the PEM)")
	errVerifyKeyReentry  = errors.New("re-enter the API key to verify changed details")
	errVerifyNoSavedKey  = errors.New("no saved key is available; enter it to verify")
	errVerifyKeyUnproven = errors.New("the credential store holding the saved key cannot be reached; enter the key to verify")
)

// verifyConfig is the session config for the details as typed, and, when
// a saved key is used, the check that its binding is still current.
//
// A typed key verifies the typed details and the saved key is not read;
// only the saved CA bundle may stand in for an empty PEM. Without a typed
// key, an existing bookmark's saved key serves only while the binding it
// was saved under is unchanged, the rule Save applies (ADR 0015), so a
// saved key never reaches an endpoint it was not saved for. That binding is
// read from the one configuration the store acquires together with the key
// and its generation, never from a separate earlier read, and every request
// the session then makes is admitted under that generation, as a saved
// bookmark's requests are.
func (s *BookmarkService) verifyConfig(ctx context.Context, dto BookmarkDTO, secret string) (swiftcontrol.Config, func(context.Context) error, error) {
	var typed bookmarks.Bookmark
	connectionFields(&typed, dto)
	// An omitted mode is system trust, as Save stores it (ADR 0015), so a
	// draft without one names the same binding as the saved bookmark.
	if typed.TLSMode == "" {
		typed.TLSMode = swiftcontrol.TLSModeSystem
	}
	// Match Save's auth normalisation so a v3 draft authenticates with the
	// same domains a saved bookmark would, and so the binding check below
	// compares the stored and drafted auth fields like with like (ADR 0100).
	bookmarks.NormalizeAuth(&typed)
	var caPEM []byte
	if dto.CAPem != "" {
		caPEM = []byte(dto.CAPem)
	}
	if secret != "" {
		if typed.TLSMode == swiftcontrol.TLSModeCustomCA && caPEM == nil {
			saved, err := s.savedCA(ctx, dto.ID)
			if err != nil {
				return swiftcontrol.Config{}, nil, err
			}
			caPEM = saved
		}
		return typed.SwiftConfig([]byte(secret), caPEM), nil, nil
	}
	if dto.ID == "" {
		return swiftcontrol.Config{}, nil, errVerifyNeedsKey
	}
	stored, generation, err := s.store.ConnectionConfig(ctx, dto.ID)
	switch {
	case errors.Is(err, secrets.ErrNotFound):
		return swiftcontrol.Config{}, nil, errVerifyNoSavedKey
	case errors.Is(err, secrets.ErrAuthorityUnreachable):
		return swiftcontrol.Config{}, nil, errVerifyKeyUnproven
	case err != nil:
		return swiftcontrol.Config{}, nil, err
	}
	// The store's own rule for the binding a saved key serves; the CA bytes
	// are compared here because this side holds them as PEM.
	sameCA := typed.TLSMode != swiftcontrol.TLSModeCustomCA || caPEM == nil || bytes.Equal(caPEM, stored.TLS.CACertPEM)
	if !bookmarks.SameBinding(stored, typed.SwiftConfig(nil, nil)) || !sameCA {
		return swiftcontrol.Config{}, nil, errVerifyKeyReentry
	}
	current := func(ctx context.Context) error {
		return s.store.WithCurrentGeneration(ctx, dto.ID, generation, nil)
	}
	stored.AdmitRequest = current
	return stored, current, nil
}

// savedCA is the CA bundle a saved custom-CA bookmark carries, read without
// touching the credential store, so a new key can be checked against the
// trust the operator already saved.
func (s *BookmarkService) savedCA(ctx context.Context, id string) ([]byte, error) {
	if id == "" {
		return nil, errVerifyNeedsCA
	}
	existing, err := s.store.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if existing.TLSCAID == "" {
		return nil, errVerifyNeedsCA
	}
	return s.store.CAPem(ctx, existing.TLSCAID)
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
		applog.Warnf("credential cleanup: %v", err)
	}
}

// connectionFields sets the fields of a bookmark that name its endpoint and
// identity, the ones the editor supplies and the store binds a credential
// to; everything else the bookmark holds survives the edit untouched.
func connectionFields(b *bookmarks.Bookmark, dto BookmarkDTO) {
	b.AuthURL = dto.AuthURL
	b.Username = dto.Username
	b.StorageURLOverride = dto.StorageURLOverride
	b.AuthVersion = dto.AuthVersion
	b.UserDomain = dto.UserDomain
	b.ProjectName = dto.ProjectName
	b.ProjectDomain = dto.ProjectDomain
	b.UserID = dto.UserID
	b.ProjectID = dto.ProjectID
	b.Region = dto.Region
	b.TLSMode = swiftcontrol.TLSMode(dto.TLSMode)
}

func toBookmarkDTO(b bookmarks.Bookmark) BookmarkDTO {
	return BookmarkDTO{
		ID:                 b.ID,
		DisplayName:        b.DisplayName,
		AuthURL:            b.AuthURL,
		Username:           b.Username,
		StorageURLOverride: b.StorageURLOverride,
		AuthVersion:        b.AuthVersion,
		UserDomain:         b.UserDomain,
		ProjectName:        b.ProjectName,
		ProjectDomain:      b.ProjectDomain,
		UserID:             b.UserID,
		ProjectID:          b.ProjectID,
		Region:             b.Region,
		TLSMode:            string(b.TLSMode),
		HasCA:              b.TLSCAID != "",
		HasCredential:      b.HasCredential,
		LargeObjects:       b.LargeObjects,
		VerifyFailure:      b.VerifyFailure,
	}
}
