package bookmarks

import (
	"context"
	"strings"
	"testing"
)

// ExportAll must return every restorable field of a bookmark and never the
// credential, so a settings backup can carry the whole connection without a
// secret.
func TestExportAllReturnsCompleteConfigWithoutSecret(t *testing.T) {
	st, d, _ := newTestStore(t)
	ctx := context.Background()

	b := sample()
	b.StorageURLOverride = "https://swift.example/v1/AUTH_acct"
	b.SegmentPlacement = PlacementSidecar
	b.ChunkSizeBytes = 1 << 30
	if _, err := st.Save(ctx, b, []byte("swift-key")); err != nil {
		t.Fatal(err)
	}

	records, err := st.ExportAll(ctx, d.Reader())
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 {
		t.Fatalf("want 1 record, got %d", len(records))
	}
	r := records[0]
	if r.DisplayName != b.DisplayName || r.AuthURL != b.AuthURL || r.Username != b.Username ||
		r.StorageURLOverride != b.StorageURLOverride ||
		r.SegmentPlacement != string(b.SegmentPlacement) || r.ChunkSizeBytes != b.ChunkSizeBytes {
		t.Fatalf("export dropped configuration: %+v", r)
	}
	for field, v := range map[string]string{
		"displayName": r.DisplayName, "authUrl": r.AuthURL, "username": r.Username,
		"storageUrlOverride": r.StorageURLOverride, "caCertPem": r.CACertPEM,
	} {
		if strings.Contains(v, "swift-key") {
			t.Fatalf("export leaked the credential through %s: %q", field, v)
		}
	}
}
