package bookmarks

import (
	"context"
	"errors"
	"testing"

	"github.com/csmart/aeronautes/internal/swiftcontrol"
)

// ImportBookmark recreates a backed-up bookmark without a credential, never
// overwrites or duplicates an existing one, and keeps bookmarks that differ in
// any restorable field apart (ADR 0098 restore).
func TestImportBookmarkIsAdditiveAndDeduplicates(t *testing.T) {
	st, _, _ := newTestStore(t)
	ctx := context.Background()

	b := sample()
	b.StorageURLOverride = "https://swift.example/v1/AUTH_a"

	outcome, err := st.ImportBookmark(ctx, b, nil)
	if err != nil || outcome != ImportCreated {
		t.Fatalf("first import: outcome=%v err=%v", outcome, err)
	}
	marks, err := st.List(ctx)
	if err != nil || len(marks) != 1 {
		t.Fatalf("after import: %+v %v", marks, err)
	}
	if marks[0].HasCredential {
		t.Fatal("import stored a credential")
	}
	// The store fills the same defaults Save does, so a restored bookmark is a
	// complete record rather than one with an empty placement or TLS mode.
	if marks[0].SegmentPlacement != PlacementRcloneDefault || marks[0].TLSMode != swiftcontrol.TLSModeSystem {
		t.Fatalf("defaults not applied on import: %+v", marks[0])
	}

	// An identical configuration is skipped, so a repeat restore adds nothing.
	outcome, err = st.ImportBookmark(ctx, b, nil)
	if err != nil || outcome != ImportSkipped {
		t.Fatalf("duplicate import: outcome=%v err=%v", outcome, err)
	}
	if again, _ := st.List(ctx); len(again) != 1 {
		t.Fatalf("duplicate import added a bookmark: %d", len(again))
	}

	// A bookmark that differs in one restorable field is a distinct connection.
	distinct := b
	distinct.StorageURLOverride = "https://swift.example/v1/AUTH_b"
	outcome, err = st.ImportBookmark(ctx, distinct, nil)
	if err != nil || outcome != ImportCreated {
		t.Fatalf("distinct import: outcome=%v err=%v", outcome, err)
	}
	if again, _ := st.List(ctx); len(again) != 2 {
		t.Fatalf("distinct import not kept apart: %d", len(again))
	}
}

// A custom-CA bookmark with no usable certificate, and an otherwise invalid
// bookmark, are both refused, so a restore cannot create a broken connection.
func TestImportBookmarkRefusesInvalidInput(t *testing.T) {
	st, _, _ := newTestStore(t)
	ctx := context.Background()

	customCA := sample()
	customCA.TLSMode = swiftcontrol.TLSModeCustomCA
	if _, err := st.ImportBookmark(ctx, customCA, []byte("not a certificate")); err == nil {
		t.Fatal("import accepted a custom CA with no usable certificate")
	}
	if _, err := st.ImportBookmark(ctx, customCA, nil); err == nil {
		t.Fatal("import accepted a custom CA bookmark with no certificate")
	}

	bad := sample()
	bad.AuthURL = ""
	if _, err := st.ImportBookmark(ctx, bad, nil); err == nil {
		t.Fatal("import accepted a bookmark with no auth URL")
	}

	if marks, _ := st.List(ctx); len(marks) != 0 {
		t.Fatalf("a refused import created a bookmark: %d", len(marks))
	}
}

// identity keeps field boundaries exact, so two bookmarks cannot be merged by
// shifting text across fields — the collision a single delimiter-joined key
// allowed when a display name or username itself contained the delimiter.
func TestIdentityDistinguishesFieldsThatWouldJoinAmbiguously(t *testing.T) {
	a := identityOf(Bookmark{
		DisplayName: "n", AuthURL: "https://a.example/auth", Username: "u\x00https://b.example/auth\x00v",
	}, nil)
	b := identityOf(Bookmark{
		DisplayName: "n\x00https://a.example/auth\x00u", AuthURL: "https://b.example/auth", Username: "v",
	}, nil)
	if a == b {
		t.Fatal("distinct configurations collided in the identity key")
	}
}

// A lost commit reply after the insert is reported as unresolved, not as a
// plain failure, so a restore can say the outcome is uncertain rather than
// claim the bookmark was not created when the row may be durable.
func TestImportBookmarkReportsUnresolvedOnLostCommit(t *testing.T) {
	st, d, _ := newTestStore(t)
	ctx := context.Background()
	st.db = &lostCommitReply{DB: d, at: 1, failRead: true, closed: unavailablePublicationReader(t)}
	outcome, err := st.ImportBookmark(ctx, sample(), nil)
	if outcome != ImportUnresolved || !errors.Is(err, ErrPublicationUnresolved) {
		t.Fatalf("lost commit reply = (%v, %v), want (ImportUnresolved, ErrPublicationUnresolved)", outcome, err)
	}
}
