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
	"errors"
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

func (f fixedSettings) Set(_ context.Context, s AppSettingsDTO) (AppSettingsDTO, error) {
	return s, nil
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

func (s interleavingSettings) Set(ctx context.Context, dto AppSettingsDTO) (AppSettingsDTO, error) {
	return s.real.Set(ctx, dto)
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

	out := string(bookmarks.CertificatesOnly([]byte(bundle)))
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

// restorePicker chooses a specific backup file for a restore.
type restorePicker struct{ file string }

func (p restorePicker) PickFiles(string) ([]string, error)   { return []string{p.file}, nil }
func (p restorePicker) PickDirectory(string) (string, error) { return "", nil }

// writeBackup marshals a backup document to a fresh file and returns its path.
func writeBackup(t *testing.T, doc SettingsBackupDTO) string {
	t.Helper()
	raw, err := json.MarshalIndent(&doc, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "backup.json")
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// restoreInto imports a backup file into the given stores through a fresh
// service, as the shell would after the operator chose the file.
func restoreInto(t *testing.T, database *db.DB, store *bookmarks.Store, settings settingsPort, file string) (*RestoreResultDTO, error) {
	t.Helper()
	svc := NewBackupService(database, store, settings)
	svc.setFilePicker(restorePicker{file: file})
	return svc.ImportSettings(t.Context())
}

// A backup an export wrote restores onto a fresh instance: the application
// settings are replaced, and the bookmark is recreated with its full
// configuration and its CA but no credential, so its password is re-entered
// the next time the operator uses it (ADR 0098, Chris's 4 Nov decision).
func TestImportSettingsRestoresConfigurationFromExport(t *testing.T) {
	ctx := t.Context()
	sourceStore, sourceDB := newBackupStore(t)
	src := bookmarks.Bookmark{
		DisplayName:        "Prod AU",
		AuthURL:            "https://swift.example/auth/v1.0",
		Username:           "acct:user",
		StorageURLOverride: "https://swift.example/v1/AUTH_acct",
		TLSMode:            swiftcontrol.TLSModeCustomCA,
		SegmentPlacement:   bookmarks.PlacementSidecar,
		ChunkSizeBytes:     1 << 30,
	}
	if _, err := sourceStore.SaveWithCA(ctx, src, []byte("super-secret-key"), caBundle(t)); err != nil {
		t.Fatal(err)
	}
	wantSettings := defaults()
	wantSettings.RailWidthPx = 199

	exportDir := t.TempDir()
	exporter := NewBackupService(sourceDB, sourceStore, fixedSettings{dto: wantSettings})
	exporter.setFilePicker(folderPicker{dir: exportDir})
	exported, err := exporter.ExportSettings(ctx)
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	backupFile := filepath.Join(exportDir, exported.FileName)

	targetStore, targetDB := newBackupStore(t)
	targetSettings := NewSettingsService(targetDB, NewJobRunner(jobs.NewStore(targetDB), audit.NewStore(targetDB)))
	before, err := targetSettings.Get(ctx)
	if err != nil {
		t.Fatal(err)
	}
	result, err := restoreInto(t, targetDB, targetStore, targetSettings, backupFile)
	if err != nil {
		t.Fatalf("restore: %v", err)
	}
	if !result.SettingsApplied || result.BookmarksCreated != 1 || result.BookmarksSkipped != 0 {
		t.Fatalf("restore result = %+v", result)
	}

	after, err := targetSettings.Get(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if after.RailWidthPx != 199 {
		t.Fatalf("settings not restored: RailWidthPx=%d (was %d), want 199", after.RailWidthPx, before.RailWidthPx)
	}

	marks, err := targetStore.List(ctx)
	if err != nil || len(marks) != 1 {
		t.Fatalf("restored bookmarks = %+v %v", marks, err)
	}
	got := marks[0]
	if got.HasCredential {
		t.Fatal("restored bookmark carried a credential")
	}
	if got.DisplayName != src.DisplayName || got.AuthURL != src.AuthURL || got.Username != src.Username ||
		got.StorageURLOverride != src.StorageURLOverride || got.TLSMode != src.TLSMode ||
		got.SegmentPlacement != src.SegmentPlacement || got.ChunkSizeBytes != src.ChunkSizeBytes {
		t.Fatalf("restored configuration does not match the source: %+v", got)
	}
	caPEM, err := targetStore.CAPem(ctx, got.TLSCAID)
	if err != nil || len(caPEM) == 0 {
		t.Fatalf("restored CA missing: %v", err)
	}
	if !x509.NewCertPool().AppendCertsFromPEM(caPEM) {
		t.Fatal("restored CA is not a usable certificate")
	}
}

// A restore merges by a bookmark's complete configuration: it creates one not
// already present, skips an identical one so a repeat import adds nothing, and
// keeps as distinct two bookmarks that differ in any restorable field.
func TestImportSettingsMergesByCompleteConfiguration(t *testing.T) {
	ctx := t.Context()
	store, database := newBackupStore(t)
	settings := fixedSettings{dto: defaults()}

	bmA := BackupBookmarkDTO{
		DisplayName: "Prod", AuthURL: "https://swift.example/auth/v1.0", Username: "acct:user",
		StorageURLOverride: "https://swift.example/v1/AUTH_a",
	}
	fileA := writeBackup(t, SettingsBackupDTO{SchemaVersion: backupSchemaVersion, AppSettings: defaults(), Bookmarks: []BackupBookmarkDTO{bmA}})

	if r, err := restoreInto(t, database, store, settings, fileA); err != nil || r.BookmarksCreated != 1 || r.BookmarksSkipped != 0 {
		t.Fatalf("first restore = %+v %v", r, err)
	}
	if r, err := restoreInto(t, database, store, settings, fileA); err != nil || r.BookmarksCreated != 0 || r.BookmarksSkipped != 1 {
		t.Fatalf("repeat restore not idempotent = %+v %v", r, err)
	}

	bmB := bmA
	bmB.StorageURLOverride = "https://swift.example/v1/AUTH_b"
	fileB := writeBackup(t, SettingsBackupDTO{SchemaVersion: backupSchemaVersion, AppSettings: defaults(), Bookmarks: []BackupBookmarkDTO{bmB}})
	if r, err := restoreInto(t, database, store, settings, fileB); err != nil || r.BookmarksCreated != 1 {
		t.Fatalf("distinct restore = %+v %v", r, err)
	}

	marks, err := store.List(ctx)
	if err != nil || len(marks) != 2 {
		t.Fatalf("want 2 distinct bookmarks, got %+v %v", marks, err)
	}
}

// A restore validates the whole file before it changes anything: an
// unsupported version or any malformed bookmark refuses, and nothing — not the
// settings, not even the good bookmark that shared the bad file — is applied.
func TestImportSettingsValidatesWholeFileBeforeApplying(t *testing.T) {
	ctx := t.Context()
	store, database := newBackupStore(t)
	settings := NewSettingsService(database, NewJobRunner(jobs.NewStore(database), audit.NewStore(database)))
	baseline, err := settings.Get(ctx)
	if err != nil {
		t.Fatal(err)
	}

	changed := defaults()
	changed.RailWidthPx = 199

	future := writeBackup(t, SettingsBackupDTO{SchemaVersion: backupSchemaVersion + 1, AppSettings: changed})
	if _, err := restoreInto(t, database, store, settings, future); err == nil {
		t.Fatal("restore accepted an unsupported version")
	}

	bad := SettingsBackupDTO{
		SchemaVersion: backupSchemaVersion,
		AppSettings:   changed,
		Bookmarks: []BackupBookmarkDTO{
			{DisplayName: "Good", AuthURL: "https://swift.example/auth/v1.0", Username: "acct:user"},
			{DisplayName: "Bad CA", AuthURL: "https://swift.example/auth/v1.0", Username: "acct:user",
				TLSMode: string(swiftcontrol.TLSModeCustomCA), CACertPEM: "this is not a certificate"},
		},
	}
	if _, err := restoreInto(t, database, store, settings, writeBackup(t, bad)); err == nil {
		t.Fatal("restore accepted a file with a malformed bookmark")
	}

	after, err := settings.Get(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if after.RailWidthPx != baseline.RailWidthPx {
		t.Fatalf("a refused restore changed settings: RailWidthPx %d -> %d", baseline.RailWidthPx, after.RailWidthPx)
	}
	marks, err := store.List(ctx)
	if err != nil || len(marks) != 0 {
		t.Fatalf("a refused restore created a bookmark: %+v %v", marks, err)
	}
}

func TestImportSettingsRefusesHeadlessAndReportsCancellation(t *testing.T) {
	ctx := t.Context()
	store, database := newBackupStore(t)

	svc := NewBackupService(database, store, fixedSettings{dto: defaults()})
	if _, err := svc.ImportSettings(ctx); err == nil {
		t.Fatal("restore without a file picker did not refuse")
	}

	// An empty file choice is an ordinary cancellation: nil result, no error.
	svc.setFilePicker(folderPicker{})
	result, err := svc.ImportSettings(ctx)
	if err != nil || result != nil {
		t.Fatalf("cancelled restore = %+v, %v; want nil, nil", result, err)
	}
}

// A hand-edited backup whose CA bundle is a certificate followed by its
// private key is refused before anything is applied, so no key reaches trust
// storage and no partial restore lands (ADR 0098; review db:991 finding 2).
func TestImportSettingsRefusesABackupCarryingAPrivateKey(t *testing.T) {
	ctx := t.Context()
	store, database := newBackupStore(t)
	settings := NewSettingsService(database, NewJobRunner(jobs.NewStore(database), audit.NewStore(database)))
	before, err := settings.Get(ctx)
	if err != nil {
		t.Fatal(err)
	}

	changed := defaults()
	changed.RailWidthPx = 199
	doc := SettingsBackupDTO{
		SchemaVersion: backupSchemaVersion,
		AppSettings:   changed,
		Bookmarks: []BackupBookmarkDTO{{
			DisplayName: "Keyed", AuthURL: "https://swift.example/auth/v1.0", Username: "acct:user",
			TLSMode: string(swiftcontrol.TLSModeCustomCA), CACertPEM: string(caBundle(t)),
		}},
	}
	if _, err := restoreInto(t, database, store, settings, writeBackup(t, doc)); err == nil {
		t.Fatal("restore accepted a backup carrying a private key")
	}

	after, err := settings.Get(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if after.RailWidthPx != before.RailWidthPx {
		t.Fatalf("a refused restore changed settings: %d -> %d", before.RailWidthPx, after.RailWidthPx)
	}
	if marks, _ := store.List(ctx); len(marks) != 0 {
		t.Fatalf("a refused restore created a bookmark: %d", len(marks))
	}
}

// Exporting a custom-CA bookmark and restoring it into the same database adds
// nothing: the stored bundle (a certificate and its key) and the exported
// certificate-only form reduce to the same trust set, so duplicate detection
// skips it rather than creating a second credential-less copy (review db:991
// finding 5).
func TestImportSettingsDedupsExportIntoSamePopulatedDatabase(t *testing.T) {
	ctx := t.Context()
	store, database := newBackupStore(t)
	b := bookmarks.Bookmark{
		DisplayName: "Prod", AuthURL: "https://swift.example/auth/v1.0", Username: "acct:user",
		TLSMode: swiftcontrol.TLSModeCustomCA,
	}
	if _, err := store.SaveWithCA(ctx, b, []byte("key"), caBundle(t)); err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()
	exporter := NewBackupService(database, store, fixedSettings{dto: defaults()})
	exporter.setFilePicker(folderPicker{dir: dir})
	exported, err := exporter.ExportSettings(ctx)
	if err != nil {
		t.Fatalf("export: %v", err)
	}

	result, err := restoreInto(t, database, store, fixedSettings{dto: defaults()}, filepath.Join(dir, exported.FileName))
	if err != nil {
		t.Fatalf("restore: %v", err)
	}
	if result.BookmarksCreated != 0 || result.BookmarksSkipped != 1 {
		t.Fatalf("export then restore into the same database duplicated trust: %+v", result)
	}
	if marks, _ := store.List(ctx); len(marks) != 1 {
		t.Fatalf("want 1 bookmark after an idempotent restore, got %d", len(marks))
	}
}

// failingSettings models a settings write that fails at its commit boundary,
// where the new values may already be durable (review db:992 finding 2).
type failingSettings struct{}

func (failingSettings) getFrom(context.Context, dbQuerier) (AppSettingsDTO, error) {
	return defaults(), nil
}

func (failingSettings) Set(context.Context, AppSettingsDTO) (AppSettingsDTO, error) {
	return AppSettingsDTO{}, errors.New("injected lost settings commit reply")
}

// A settings write that fails after it may have committed is an uncertain
// outcome, not a proved refusal: the restore carries it in the result (so the
// shell reloads rather than writing a stale document over it) instead of
// returning an error the bridge would drop, and it stops before bookmarks.
func TestImportSettingsReportsUncertainSettings(t *testing.T) {
	ctx := t.Context()
	store, database := newBackupStore(t)
	doc := SettingsBackupDTO{
		SchemaVersion: backupSchemaVersion,
		AppSettings:   defaults(),
		Bookmarks: []BackupBookmarkDTO{{
			DisplayName: "Prod", AuthURL: "https://swift.example/auth/v1.0", Username: "acct:user",
		}},
	}
	result, err := restoreInto(t, database, store, failingSettings{}, writeBackup(t, doc))
	if err != nil {
		t.Fatalf("an uncertain settings outcome must be carried in the result, not an error: %v", err)
	}
	if result == nil || result.SettingsApplied || result.Complete || result.Detail == "" {
		t.Fatalf("uncertain settings result = %+v", result)
	}
	if result.BookmarksCreated != 0 {
		t.Fatalf("bookmarks were applied despite an uncertain settings outcome: %+v", result)
	}
	if marks, _ := store.List(ctx); len(marks) != 0 {
		t.Fatalf("a stopped restore created a bookmark: %d", len(marks))
	}
}

// Strict CA validation refuses a bundle with any undecodable or non-certificate
// material, whether it comes before or after a valid certificate, so a
// hand-edited backup cannot smuggle it past pem.Decode's skipping (review
// db:992 finding 1).
func TestImportSettingsRefusesUndecodableCA(t *testing.T) {
	ctx := t.Context()
	certDER, _ := selfSigned(t)
	valid := string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDER}))
	bad := "-----BEGIN CERTIFICATE-----\nnot valid base64 certificate data\n-----END CERTIFICATE-----\n"

	for _, ca := range []string{
		valid + "trailing material that is not a certificate\n",
		"leading material that is not a certificate\n" + valid,
		valid + bad,
		bad + valid,
	} {
		store, database := newBackupStore(t)
		doc := SettingsBackupDTO{
			SchemaVersion: backupSchemaVersion,
			AppSettings:   defaults(),
			Bookmarks: []BackupBookmarkDTO{{
				DisplayName: "Prod", AuthURL: "https://swift.example/auth/v1.0", Username: "acct:user",
				TLSMode: string(swiftcontrol.TLSModeCustomCA), CACertPEM: ca,
			}},
		}
		if _, err := restoreInto(t, database, store, fixedSettings{dto: defaults()}, writeBackup(t, doc)); err == nil {
			t.Fatalf("restore accepted an undecodable CA bundle: %q", ca)
		}
		if marks, _ := store.List(ctx); len(marks) != 0 {
			t.Fatalf("a refused restore created a bookmark for CA %q: %d", ca, len(marks))
		}
	}
}
