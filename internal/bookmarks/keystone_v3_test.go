package bookmarks

import (
	"context"
	"strings"
	"testing"

	"github.com/csmart/aeronautes/internal/swiftcontrol"
)

func v3Sample() Bookmark {
	return Bookmark{
		DisplayName: "Keystone AU",
		AuthURL:     "https://keystone.example/v3",
		Username:    "keystoneuser",
		AuthVersion: 3,
		ProjectName: "demo",
		Region:      "RegionOne",
	}
}

// A saved Keystone v3 bookmark round-trips with its identity intact, the
// blank domains defaulted, and reconstructs a v3 connection config — the
// whole point of slice B: a stored v3 bookmark produces a working v3 session
// config (ADR 0100).
func TestSaveKeystoneV3RoundTripAndConfig(t *testing.T) {
	st, _, _ := newTestStore(t)
	ctx := context.Background()

	saved, err := st.Save(ctx, v3Sample(), []byte("keystone-pass"))
	if err != nil {
		t.Fatal(err)
	}
	if saved.AuthVersion != 3 {
		t.Fatalf("AuthVersion = %d, want 3", saved.AuthVersion)
	}
	if saved.UserDomain != "Default" || saved.ProjectDomain != "Default" {
		t.Fatalf("blank domains not defaulted to Default: %+v", saved)
	}
	if saved.ProjectName != "demo" || saved.Region != "RegionOne" {
		t.Fatalf("v3 identity not persisted: %+v", saved)
	}

	got, err := st.Get(ctx, saved.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got != saved {
		t.Fatalf("Get = %+v, want %+v", got, saved)
	}

	// swiftcontrol.Config holds a []byte CA bundle, so compare the fields
	// this mapping sets rather than the whole (non-comparable) struct.
	cfg := got.SwiftConfig([]byte("keystone-pass"), nil)
	if cfg.AuthURL != "https://keystone.example/v3" || cfg.Username != "keystoneuser" ||
		cfg.APIKey != "keystone-pass" || cfg.StorageURLOverride != "" ||
		cfg.AuthVersion != 3 || cfg.UserDomain != "Default" || cfg.ProjectName != "demo" ||
		cfg.ProjectDomain != "Default" || cfg.Region != "RegionOne" ||
		cfg.TLS.Mode != swiftcontrol.TLSModeSystem {
		t.Fatalf("SwiftConfig = %+v, want the v3 identity", cfg)
	}
}

// Explicit domains survive a save unchanged, so an operator whose user and
// project live in a non-default domain is not silently overridden.
func TestSaveKeystoneV3KeepsExplicitDomains(t *testing.T) {
	st, _, _ := newTestStore(t)
	b := v3Sample()
	b.UserDomain, b.ProjectDomain = "ldap-users", "ops"
	saved, err := st.Save(context.Background(), b, []byte("k"))
	if err != nil {
		t.Fatal(err)
	}
	if saved.UserDomain != "ldap-users" || saved.ProjectDomain != "ops" {
		t.Fatalf("explicit domains not preserved: %+v", saved)
	}
}

func TestNormalizeAuth(t *testing.T) {
	cases := []struct {
		name     string
		in, want Bookmark
	}{
		{"unset version becomes v1", Bookmark{}, Bookmark{AuthVersion: 1}},
		{"v1 leaves keystone fields blank", Bookmark{AuthVersion: 1}, Bookmark{AuthVersion: 1}},
		{"v3 blank domains default", Bookmark{AuthVersion: 3}, Bookmark{AuthVersion: 3, UserDomain: "Default", ProjectDomain: "Default"}},
		{"v3 keeps explicit domains", Bookmark{AuthVersion: 3, UserDomain: "ldap", ProjectDomain: "ops"}, Bookmark{AuthVersion: 3, UserDomain: "ldap", ProjectDomain: "ops"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			b := tc.in
			NormalizeAuth(&b)
			if b != tc.want {
				t.Errorf("NormalizeAuth(%+v) = %+v, want %+v", tc.in, b, tc.want)
			}
		})
	}
}

// validate enforces the v3 requirements at the save boundary that New
// enforces at connect time, so an unusable v3 bookmark cannot be stored.
func TestValidateKeystoneV3(t *testing.T) {
	base := func() Bookmark {
		return Bookmark{DisplayName: "K", AuthURL: "https://keystone.example/v3", Username: "u", AuthVersion: 3, ProjectName: "demo", Region: "RegionOne"}
	}
	if err := validate(base()); err != nil {
		t.Fatalf("a valid v3 bookmark was rejected: %v", err)
	}
	cases := []struct {
		name string
		edit func(*Bookmark)
		want string
	}{
		{"no region", func(b *Bookmark) { b.Region = "" }, "region"},
		{"no project", func(b *Bookmark) { b.ProjectName = "" }, "project"},
		{"storage override", func(b *Bookmark) { b.StorageURLOverride = "https://keystone.example/v1/AUTH_x" }, "override"},
		{"unknown version", func(b *Bookmark) { b.AuthVersion = 2 }, "unsupported auth version"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			b := base()
			tc.edit(&b)
			err := validate(b)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("validate = %v, want an error mentioning %q", err, tc.want)
			}
		})
	}
}

// The credential is bound to the v3 identity and endpoint, so changing the
// region, project, a domain, or the auth version refuses to reuse the stored
// key until it is re-entered — the same rule v1 applies to its own binding
// (ADR 0015, ADR 0100).
func TestKeystoneV3BindingRequiresReentry(t *testing.T) {
	cases := []struct {
		name  string
		start Bookmark
		edit  func(*Bookmark)
	}{
		{"region", v3Sample(), func(b *Bookmark) { b.Region = "Region2" }},
		{"project", v3Sample(), func(b *Bookmark) { b.ProjectName = "other" }},
		{"user domain", v3Sample(), func(b *Bookmark) { b.UserDomain = "ldap-users" }},
		{"project domain", v3Sample(), func(b *Bookmark) { b.ProjectDomain = "ops" }},
		{"version v1 to v3", sample(), func(b *Bookmark) { b.AuthVersion = 3; b.ProjectName = "demo"; b.Region = "RegionOne" }},
		// Changes only the auth version, leaving every other field equal, so
		// this case fails if the auth-version term is dropped from the binding
		// check — the other cases would still refuse on their own field.
		{"version only v3 to v1", v3Sample(), func(b *Bookmark) { b.AuthVersion = 1 }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			st, _, _ := newTestStore(t)
			ctx := context.Background()
			before, err := st.Save(ctx, tc.start, []byte("original"))
			if err != nil {
				t.Fatal(err)
			}
			edited := before
			edited.HasCredential = false
			tc.edit(&edited)
			if _, err := st.Save(ctx, edited, nil); err == nil || !strings.Contains(err.Error(), "re-enter") {
				t.Errorf("binding change returned %v, want a key re-entry refusal", err)
			}
			if after, err := st.Get(ctx, before.ID); err != nil || after != before {
				t.Errorf("the refused edit changed the stored bookmark: %v", err)
			}
			if _, err := st.Save(ctx, edited, []byte("replacement")); err != nil {
				t.Fatalf("explicit re-entry failed: %v", err)
			}
			if key, err := st.Credential(ctx, before.ID); err != nil || string(key) != "replacement" {
				t.Errorf("the replacement key was not saved: %v", err)
			}
		})
	}
}

// A settings backup must carry the whole v3 identity and keep two bookmarks
// that differ only in region distinct, or a restore cannot reconstruct the
// original Keystone connections (ADR 0098).
func TestExportPreservesKeystoneV3Identity(t *testing.T) {
	st, d, _ := newTestStore(t)
	ctx := context.Background()
	one := v3Sample()
	one.Region = "RegionOne"
	two := v3Sample()
	two.Region = "RegionTwo"
	if _, err := st.Save(ctx, one, []byte("k")); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Save(ctx, two, []byte("k")); err != nil {
		t.Fatal(err)
	}

	records, err := st.ExportAll(ctx, d.Reader())
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 2 {
		t.Fatalf("want 2 export records, got %d", len(records))
	}
	regions := map[string]bool{}
	for _, r := range records {
		if r.AuthVersion != 3 || r.ProjectName != "demo" || r.UserDomain != "Default" || r.ProjectDomain != "Default" {
			t.Fatalf("export dropped the v3 identity: %+v", r)
		}
		regions[r.Region] = true
	}
	if !regions["RegionOne"] || !regions["RegionTwo"] {
		t.Fatalf("export conflated the two regions: %v", regions)
	}
}

// A restore keeps v3 connections that differ only in region as distinct
// bookmarks, still deduplicates an exact repeat, and normalises what it
// writes so an imported bookmark matches a saved one (ADR 0098, ADR 0100).
func TestImportKeystoneV3DistinctAndNormalised(t *testing.T) {
	st, _, _ := newTestStore(t)
	ctx := context.Background()
	one := v3Sample()
	one.Region = "RegionOne"
	two := v3Sample()
	two.Region = "RegionTwo"

	if o, err := st.ImportBookmark(ctx, one, nil); err != nil || o != ImportCreated {
		t.Fatalf("import RegionOne = %v (%v), want ImportCreated", o, err)
	}
	if o, err := st.ImportBookmark(ctx, two, nil); err != nil || o != ImportCreated {
		t.Fatalf("import RegionTwo = %v (%v), want ImportCreated (a distinct region)", o, err)
	}
	if o, err := st.ImportBookmark(ctx, one, nil); err != nil || o != ImportSkipped {
		t.Fatalf("re-import RegionOne = %v (%v), want ImportSkipped", o, err)
	}

	list, err := st.List(ctx)
	if err != nil || len(list) != 2 {
		t.Fatalf("want 2 bookmarks after import, got %d (%v)", len(list), err)
	}
	for _, b := range list {
		if b.AuthVersion != 3 || b.UserDomain != "Default" || b.ProjectDomain != "Default" {
			t.Fatalf("imported v3 bookmark not normalised: %+v", b)
		}
	}
}

// A backup written before this field existed carries no auth version; the
// import must normalise it to v1 rather than storing 0.
func TestImportNormalisesLegacyAuthVersion(t *testing.T) {
	st, _, _ := newTestStore(t)
	ctx := context.Background()
	if o, err := st.ImportBookmark(ctx, sample(), nil); err != nil || o != ImportCreated {
		t.Fatalf("import legacy bookmark = %v (%v), want ImportCreated", o, err)
	}
	list, err := st.List(ctx)
	if err != nil || len(list) != 1 || list[0].AuthVersion != 1 {
		t.Fatalf("legacy import not normalised to v1: %+v (%v)", list, err)
	}
}
