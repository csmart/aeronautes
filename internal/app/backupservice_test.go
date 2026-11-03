package app

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/csmart/aeronautes/internal/audit"
	"github.com/csmart/aeronautes/internal/bookmarks"
	"github.com/csmart/aeronautes/internal/db"
	"github.com/csmart/aeronautes/internal/jobs"
	"github.com/csmart/aeronautes/internal/secrets"
	"github.com/csmart/aeronautes/internal/swiftcontrol"
)

type fixedSettings struct{ dto AppSettingsDTO }

func (f fixedSettings) getFrom(context.Context, dbQuerier) (AppSettingsDTO, error) {
	return f.dto, nil
}

type folderPicker struct{ dir string }

func (p folderPicker) PickFiles(string) ([]string, error)   { return nil, nil }
func (p folderPicker) PickDirectory(string) (string, error) { return p.dir, nil }

// interleavingSettings reads settings through the supplied querier, then runs
// onRead before returning, so a test can commit changes between the backup's
// two reads and prove they come from one snapshot.
type interleavingSettings struct {
	real   *SettingsService
	onRead func()
}

func (s interleavingSettings) getFrom(ctx context.Context, q dbQuerier) (AppSettingsDTO, error) {
	dto, err := s.real.getFrom(ctx, q)
	if s.onRead != nil {
		s.onRead()
	}
	return dto, err
}

// selfSigned builds a self-signed certificate and returns its DER and the DER
// of its private key, so a test can assemble real PEM controls.
func selfSigned(t *testing.T) (certDER, keyDER []byte) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := x509.Certificate{
		SerialNumber:          big.NewInt(time.Now().UnixNano()),
		Subject:               pkix.Name{CommonName: "aeronautes test ca"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
	}
	certDER, err = x509.CreateCertificate(rand.Reader, &tmpl, &tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err = x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	return certDER, keyDER
}

// caBundle is a certificate followed by its private key, the shape SaveWithCA
// accepts verbatim today; the export must strip the key.
func caBundle(t *testing.T) []byte {
	t.Helper()
	certDER, keyDER := selfSigned(t)
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDER})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	return append(certPEM, keyPEM...)
}

func newBackupStore(t *testing.T) (*bookmarks.Store, *db.DB) {
	t.Helper()
	d, err := db.Open(t.Context(), filepath.Join(t.TempDir(), "state.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	return bookmarks.NewStore(d, secrets.NewSessionStore()), d
}

func TestCertificatesOnlyKeepsOnlyRealHeaderFreeCertificates(t *testing.T) {
	cert1, key1 := selfSigned(t)
	cert2, _ := selfSigned(t)
	bundle := string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert1})) +
		// A private key mislabelled as a certificate must not travel.
		string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: key1})) +
		// A header-bearing certificate the trust pool ignores must not become
		// trusted by being re-encoded without its header.
		string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Headers: map[string]string{"Comment": "ignored"}, Bytes: cert2})) +
		string(pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: key1})) +
		string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert2}))

	out := certificatesOnly(bundle)
	if strings.Contains(out, "PRIVATE KEY") || strings.Contains(out, "Comment") {
		t.Fatalf("filter kept a private key or a header-bearing block: %q", out)
	}
	// Only the two bare, valid certificates survive: cert1 and the header-free
	// cert2 at the end; the mislabelled key and the header-bearing block drop.
	if got := strings.Count(out, "BEGIN CERTIFICATE"); got != 2 {
		t.Fatalf("want 2 certificates, got %d: %q", got, out)
	}
	if !x509.NewCertPool().AppendCertsFromPEM([]byte(out)) {
		t.Fatal("exported certificates are not loadable by the trust pool")
	}
}

func TestExportSettingsWritesCompleteBackupWithoutSecrets(t *testing.T) {
	ctx := t.Context()
	store, database := newBackupStore(t)
	const secret = "super-secret-key"
	b := bookmarks.Bookmark{
		DisplayName:        "Prod AU",
		AuthURL:            "https://swift.example/auth/v1.0",
		Username:           "acct:user",
		StorageURLOverride: "https://swift.example/v1/AUTH_acct",
		TLSMode:            swiftcontrol.TLSModeCustomCA,
		SegmentPlacement:   bookmarks.PlacementSidecar,
		ChunkSizeBytes:     1 << 30,
	}
	if _, err := store.SaveWithCA(ctx, b, []byte(secret), caBundle(t)); err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()
	svc := NewBackupService(database, store, fixedSettings{dto: AppSettingsDTO{RailWidthPx: 321, MaxActiveJobs: 4}})
	svc.setFilePicker(folderPicker{dir: dir})

	result, err := svc.ExportSettings(ctx)
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	if result.BookmarkCount != 1 {
		t.Fatalf("bookmark count = %d, want 1", result.BookmarkCount)
	}
	raw, err := os.ReadFile(filepath.Join(dir, result.FileName))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), secret) {
		t.Fatal("backup file carried the credential")
	}
	if strings.Contains(string(raw), "PRIVATE KEY") {
		t.Fatal("backup file carried a private key from the CA bundle")
	}
	if entries, _ := filepath.Glob(filepath.Join(dir, "*.part")); len(entries) != 0 {
		t.Fatalf("export left a .part file: %v", entries)
	}

	var doc SettingsBackupDTO
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("backup is not valid JSON: %v", err)
	}
	if doc.SchemaVersion != backupSchemaVersion {
		t.Fatalf("schema version = %d, want %d", doc.SchemaVersion, backupSchemaVersion)
	}
	if doc.AppSettings.RailWidthPx != 321 || doc.AppSettings.MaxActiveJobs != 4 {
		t.Fatalf("app settings not captured: %+v", doc.AppSettings)
	}
	if len(doc.Bookmarks) != 1 {
		t.Fatalf("want 1 bookmark, got %d", len(doc.Bookmarks))
	}
	got := doc.Bookmarks[0]
	if got.DisplayName != b.DisplayName || got.AuthURL != b.AuthURL || got.Username != b.Username ||
		got.StorageURLOverride != b.StorageURLOverride || got.TLSMode != string(b.TLSMode) ||
		got.SegmentPlacement != string(b.SegmentPlacement) || got.ChunkSizeBytes != b.ChunkSizeBytes {
		t.Fatalf("bookmark configuration not fully captured: %+v", got)
	}
	if !strings.Contains(got.CACertPEM, "BEGIN CERTIFICATE") {
		t.Fatalf("CA certificate not exported: %q", got.CACertPEM)
	}
}

func TestExportSettingsRefusesHeadlessAndReportsCancellation(t *testing.T) {
	ctx := t.Context()
	store, database := newBackupStore(t)

	// A headless service has no dialog and refuses with an error.
	svc := NewBackupService(database, store, fixedSettings{})
	if _, err := svc.ExportSettings(ctx); err == nil {
		t.Fatal("export without a file picker did not refuse")
	}

	// A cancelled folder choice is not an error: it returns a nil result so
	// the caller reports it as cancelled.
	svc.setFilePicker(folderPicker{dir: ""})
	result, err := svc.ExportSettings(ctx)
	if err != nil || result != nil {
		t.Fatalf("cancelled export = %+v, %v; want nil, nil", result, err)
	}
}

// A backup must be one coherent snapshot: if a settings commit and a bookmark
// edit both land between the two reads, the file must show the state from
// before them, never a combination that never existed. This uses the real
// settings and bookmark stores and fails if the reads stop sharing the
// read transaction.
func TestExportSettingsTakesOneCoherentSnapshot(t *testing.T) {
	ctx := t.Context()
	store, database := newBackupStore(t)
	settings := NewSettingsService(database, NewJobRunner(jobs.NewStore(database), audit.NewStore(database)))

	base := defaults()
	base.RailWidthPx = 111
	if _, err := settings.Set(ctx, base); err != nil {
		t.Fatal(err)
	}
	saved, err := store.Save(ctx, bookmarks.Bookmark{
		DisplayName: "before", AuthURL: "https://swift.example/auth/v1.0", Username: "acct:user",
	}, []byte("key"))
	if err != nil {
		t.Fatal(err)
	}

	// Between the settings read and the bookmark read, commit a new settings
	// value and rename the bookmark. One snapshot must see neither.
	interleave := func() {
		changed := defaults()
		changed.RailWidthPx = 222
		if _, err := settings.Set(ctx, changed); err != nil {
			t.Error(err)
		}
		edited := saved
		edited.DisplayName = "after"
		if _, err := store.Save(ctx, edited, nil); err != nil {
			t.Error(err)
		}
	}

	dir := t.TempDir()
	svc := NewBackupService(database, store, interleavingSettings{real: settings, onRead: interleave})
	svc.setFilePicker(folderPicker{dir: dir})
	result, err := svc.ExportSettings(ctx)
	if err != nil {
		t.Fatalf("export: %v", err)
	}

	raw, err := os.ReadFile(filepath.Join(dir, result.FileName))
	if err != nil {
		t.Fatal(err)
	}
	var doc SettingsBackupDTO
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	if doc.AppSettings.RailWidthPx != 111 {
		t.Fatalf("settings not from the snapshot: RailWidthPx=%d, want 111", doc.AppSettings.RailWidthPx)
	}
	if len(doc.Bookmarks) != 1 || doc.Bookmarks[0].DisplayName != "before" {
		t.Fatalf("bookmark not from the same snapshot (never-existed combination): %+v", doc.Bookmarks)
	}
}
