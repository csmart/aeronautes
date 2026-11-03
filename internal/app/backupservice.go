package app

import (
	"context"
	"crypto/x509"
	"database/sql"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/csmart/aeronautes/internal/bookmarks"
	"github.com/csmart/aeronautes/internal/db"
)

// backupSchemaVersion is the on-disk format version of a settings backup
// (ADR 0098). A restore refuses a version it does not understand, including
// a newer one, so an older build never misreads a file a newer one wrote.
const backupSchemaVersion = 1

// SettingsBackupDTO is the whole configuration backup: the application
// settings and every bookmark's non-secret configuration, as one versioned
// document. It never carries a credential; the opt-in encrypted credential
// bundle is a separate, later addition (ADR 0098, ADR 0006).
type SettingsBackupDTO struct {
	SchemaVersion int                 `json:"schemaVersion"`
	ExportedAt    string              `json:"exportedAt"`
	AppSettings   AppSettingsDTO      `json:"appSettings"`
	Bookmarks     []BackupBookmarkDTO `json:"bookmarks"`
}

// BackupBookmarkDTO is a bookmark's complete restorable configuration, with
// no credential, no secret reference, and no backend-generated id (a restore
// creates fresh ids, ADR 0015). The CA is a certificate only.
type BackupBookmarkDTO struct {
	DisplayName        string `json:"displayName"`
	AuthURL            string `json:"authUrl"`
	Username           string `json:"username"`
	StorageURLOverride string `json:"storageUrlOverride"`
	TLSMode            string `json:"tlsMode"`
	CACertPEM          string `json:"caCertPem"`
	PerformanceProfile string `json:"performanceProfile"`
	SegmentPlacement   string `json:"segmentPlacement"`
	ChunkSizeBytes     int64  `json:"chunkSizeBytes"`
}

// BackupResultDTO reports a completed export: how many bookmarks it wrote and
// the folder and file the operator chose.
type BackupResultDTO struct {
	BookmarkCount int    `json:"bookmarkCount"`
	Directory     string `json:"directory"`
	FileName      string `json:"fileName"`
}

// dbQuerier is the read surface shared by the pooled reader and a read
// transaction, so the backup can take both its projections from one
// consistent snapshot.
type dbQuerier interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

// settingsReader is the slice of SettingsService the backup needs: the
// application settings, read through a caller-supplied querier so they share
// the backup's snapshot. A small consumer interface keeps the backup testable
// without the whole settings service.
type settingsReader interface {
	getFrom(ctx context.Context, q dbQuerier) (AppSettingsDTO, error)
}

// BackupService exports the operator's configuration to a file they choose
// (ADR 0098). It reads through the existing settings and bookmark stores
// rather than touching SQLite directly, and never reads a credential.
type BackupService struct {
	db        *db.DB
	bookmarks *bookmarks.Store
	settings  settingsReader
	picker    FilePicker
}

// NewBackupService builds the service over the state database, the bookmark
// store and a settings reader. The database is used only to take one
// read-transaction snapshot of the configuration; a file picker is wired
// separately by the shell.
func NewBackupService(database *db.DB, store *bookmarks.Store, settings settingsReader) *BackupService {
	return &BackupService{db: database, bookmarks: store, settings: settings}
}

// setFilePicker wires the shell's native directory dialog for export.
func (s *BackupService) setFilePicker(picker FilePicker) {
	s.picker = picker
}

// ExportSettings writes the operator's configuration to a versioned JSON file
// in a folder they choose. The bytes go to an exclusively-created .part file
// that is flushed and closed before an atomic, no-replace link publishes it
// under its final name, so a failed or interrupted export never leaves a file
// that looks complete. The backup carries no credential, and a custom CA is
// reduced to its certificate, so no private key can travel in it.
func (s *BackupService) ExportSettings(ctx context.Context) (*BackupResultDTO, error) {
	if s.picker == nil {
		return nil, errors.New("no file picker available (headless shell)")
	}
	// Take the whole configuration from one read-transaction snapshot, so a
	// concurrent edit between the two projections cannot put a combination in
	// the backup that never existed. Under WAL a read transaction is a
	// consistent point-in-time view; it is read-only and released before the
	// chooser opens.
	tx, err := s.db.Reader().BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	settings, serr := s.settings.getFrom(ctx, tx)
	records, rerr := s.bookmarks.ExportAll(ctx, tx)
	_ = tx.Rollback()
	if serr != nil {
		return nil, serr
	}
	if rerr != nil {
		return nil, rerr
	}
	doc := SettingsBackupDTO{
		SchemaVersion: backupSchemaVersion,
		ExportedAt:    time.Now().UTC().Format(time.RFC3339),
		AppSettings:   settings,
		Bookmarks:     make([]BackupBookmarkDTO, 0, len(records)),
	}
	for _, r := range records {
		doc.Bookmarks = append(doc.Bookmarks, BackupBookmarkDTO{
			DisplayName:        r.DisplayName,
			AuthURL:            r.AuthURL,
			Username:           r.Username,
			StorageURLOverride: r.StorageURLOverride,
			TLSMode:            r.TLSMode,
			CACertPEM:          certificatesOnly(r.CACertPEM),
			PerformanceProfile: r.PerformanceProfile,
			SegmentPlacement:   r.SegmentPlacement,
			ChunkSizeBytes:     r.ChunkSizeBytes,
		})
	}

	dir, err := s.picker.PickDirectory("Choose a folder to save the settings backup")
	if err != nil || dir == "" {
		// An empty selection with no error is an ordinary cancellation, which
		// the caller reports as cancelled rather than as a failure.
		return nil, err
	}

	base := "aeronautes-settings-" + time.Now().UTC().Format("20060102T150405Z")
	file, partPath, err := createExclusivePart(dir, base, ".json")
	if err != nil {
		return nil, err
	}
	published := false
	defer func() {
		if !published {
			os.Remove(partPath)
		}
	}()
	encoder := json.NewEncoder(file)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(&doc); err != nil {
		file.Close()
		return nil, err
	}
	if err := file.Sync(); err != nil {
		file.Close()
		return nil, err
	}
	if err := file.Close(); err != nil {
		return nil, err
	}
	name, err := linkPublish(partPath, dir, base, ".json")
	if err != nil {
		return nil, err
	}
	published = true
	return &BackupResultDTO{BookmarkCount: len(doc.Bookmarks), Directory: dir, FileName: name}, nil
}

// createExclusivePart reserves a fresh, exclusively-created temporary file in
// dir for a writer to fill before linkPublish moves it to its final name;
// O_EXCL also refuses to reuse a leftover part from an earlier failed run.
func createExclusivePart(dir, base, ext string) (*os.File, string, error) {
	for attempt := 0; attempt <= 1024; attempt++ {
		name := base + ext + ".part"
		if attempt > 0 {
			name = fmt.Sprintf("%s-%d%s.part", base, attempt, ext)
		}
		partPath := filepath.Join(dir, name)
		file, err := os.OpenFile(partPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err == nil {
			return file, partPath, nil
		}
		if !errors.Is(err, os.ErrExist) {
			return nil, "", err
		}
	}
	return nil, "", fmt.Errorf("could not create a new backup file in the chosen folder")
}

// certificatesOnly re-encodes PEM input keeping only its CERTIFICATE blocks,
// dropping a private key or any other block type a stored CA bundle might
// hold, so the default backup cannot carry a secret. It is the export
// boundary's guarantee that trust material travels and nothing else does.
func certificatesOnly(stored string) string {
	var out []byte
	rest := []byte(stored)
	for {
		block, remaining := pem.Decode(rest)
		if block == nil {
			break
		}
		rest = remaining
		// Match the trust loader, which ignores a block that is not a bare
		// CERTIFICATE, that carries PEM headers, or whose bytes are not a real
		// X.509 certificate. Exporting only what the loader would accept keeps
		// the backup's trust set identical to the one on record and stops a
		// private key mislabelled CERTIFICATE from travelling.
		if block.Type != "CERTIFICATE" || len(block.Headers) != 0 {
			continue
		}
		if _, err := x509.ParseCertificate(block.Bytes); err != nil {
			continue
		}
		out = append(out, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: block.Bytes})...)
	}
	return string(out)
}
