package app

import (
	"context"
	"errors"
	"testing"
)

func keystoneV3DTO(authURL string) BookmarkDTO {
	return BookmarkDTO{
		DisplayName: "Keystone",
		AuthURL:     authURL + "/v3",
		Username:    "keystoneuser",
		AuthVersion: 3,
		ProjectName: "demo",
		Region:      "RegionOne",
		TLSMode:     "system",
	}
}

// Saving a Keystone v3 bookmark carries its identity across the bridge and
// defaults the blank domains, so the frontend reads back what it will later
// edit (slice C). This is the app-layer contract slice B adds.
func TestBookmarkServiceSaveCarriesKeystoneV3Fields(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	saved, err := h.bookmarks.Save(ctx, keystoneV3DTO(h.authURL), "keystone-pass")
	if err != nil {
		t.Fatal(err)
	}
	if saved.AuthVersion != 3 || saved.ProjectName != "demo" || saved.Region != "RegionOne" {
		t.Fatalf("saved DTO missing the v3 identity: %+v", saved)
	}
	if saved.UserDomain != "Default" || saved.ProjectDomain != "Default" {
		t.Fatalf("blank domains not defaulted to Default: %+v", saved)
	}

	list, err := h.bookmarks.List(ctx)
	if err != nil || len(list) != 1 {
		t.Fatalf("list after save: %v (%d bookmarks)", err, len(list))
	}
	if list[0] != saved {
		t.Fatalf("listed bookmark = %+v, want %+v", list[0], saved)
	}
}

// A v1 bookmark still crosses the bridge as version 1 after the field was
// added, so the default auth type is unchanged.
func TestBookmarkServiceSaveDefaultsV1(t *testing.T) {
	h := newHarness(t)
	saved, err := h.bookmarks.Save(context.Background(), BookmarkDTO{
		DisplayName: "TempAuth", AuthURL: h.authURL, Username: "test:tester", TLSMode: "system",
	}, "testing")
	if err != nil {
		t.Fatal(err)
	}
	if saved.AuthVersion != 1 {
		t.Fatalf("AuthVersion = %d, want 1 for a bookmark saved without one", saved.AuthVersion)
	}
	if saved.UserDomain != "" || saved.ProjectName != "" || saved.Region != "" {
		t.Fatalf("v1 bookmark carries stray Keystone fields: %+v", saved)
	}
}

// Reusing a saved v3 key against a different project scope is refused before
// any network call, because the credential was saved for the original scope
// (ADR 0100). This exercises verify's own binding check, distinct from the
// store's.
func TestBookmarkServiceVerifyV3ReentryOnScopeChange(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	saved, err := h.bookmarks.Save(ctx, keystoneV3DTO(h.authURL), "keystone-pass")
	if err != nil {
		t.Fatal(err)
	}

	// Each field of the v3 identity is part of the key's binding, so a draft
	// that changes exactly one of them must refuse to reuse the stored key,
	// before any network call. Each case changes a single field so it fails if
	// that field's comparison is dropped from verify's binding check. The
	// saved DTO already carries explicit "Default" domains, so the auth-version
	// case differs only in the version, not in domain normalisation.
	for _, tc := range []struct {
		name   string
		change func(*BookmarkDTO)
	}{
		{"auth version", func(d *BookmarkDTO) { d.AuthVersion = 1 }},
		{"user domain", func(d *BookmarkDTO) { d.UserDomain = "ldap-users" }},
		{"project name", func(d *BookmarkDTO) { d.ProjectName = "other" }},
		{"project domain", func(d *BookmarkDTO) { d.ProjectDomain = "ops" }},
		{"region", func(d *BookmarkDTO) { d.Region = "RegionTwo" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			draft := saved
			tc.change(&draft)
			if _, err := h.bookmarks.Verify(ctx, draft, ""); !errors.Is(err, errVerifyKeyReentry) {
				t.Fatalf("verify after a %s change = %v, want errVerifyKeyReentry", tc.name, err)
			}
		})
	}

	// A blank domain normalises back to "Default", which equals the stored
	// value, so it must not look like a binding change and demand the key.
	t.Run("blank domain normalises, no re-entry", func(t *testing.T) {
		draft := saved
		draft.UserDomain, draft.ProjectDomain = "", ""
		if _, err := h.bookmarks.Verify(ctx, draft, ""); errors.Is(err, errVerifyKeyReentry) {
			t.Fatal("verify with blank (normalised) domains wrongly demanded re-entry")
		}
	})
}
