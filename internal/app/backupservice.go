package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/csmart/aeronautes/internal/bookmarks"
	"github.com/csmart/aeronautes/internal/db"
	"github.com/csmart/aeronautes/internal/swiftcontrol"
)

// backupSchemaVersion is the on-disk format version a backup is written as,
// and minReadableBackupVersion the oldest a restore still reads (ADR 0098). A
// restore refuses any version outside that range, including a newer one, so an
// older build never misreads a file a newer one wrote. Version 2 added the
// Keystone v3 authentication identity; version 1 holds TempAuth bookmarks
// only, which still restore because their absent auth fields normalise to v1.
const (
	backupSchemaVersion      = 2
	minReadableBackupVersion = 1
)

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
	AuthVersion        int    `json:"authVersion"`
	UserDomain         string `json:"userDomain"`
	ProjectName        string `json:"projectName"`
	ProjectDomain      string `json:"projectDomain"`
	Region             string `json:"region"`
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

// RestoreResultDTO reports a restore's outcome: whether the application
// settings were replaced, how many bookmarks were created or skipped because
// an identical configuration was already present, and how many could not be
// confirmed. Complete is false when a bookmark failed or is unresolved, so the
// shell can show what landed and what remains rather than a dropped error.
type RestoreResultDTO struct {
	SettingsApplied     bool   `json:"settingsApplied"`
	BookmarksTotal      int    `json:"bookmarksTotal"`
	BookmarksCreated    int    `json:"bookmarksCreated"`
	BookmarksSkipped    int    `json:"bookmarksSkipped"`
	BookmarksUnresolved int    `json:"bookmarksUnresolved"`
	Complete            bool   `json:"complete"`
	Detail              string `json:"detail"`
}

// dbQuerier is the read surface shared by the pooled reader and a read
// transaction, so the backup can take both its projections from one
// consistent snapshot.
type dbQuerier interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

// settingsPort is the slice of SettingsService the backup needs: reading the
// application settings through a caller-supplied querier, so the export shares
// its snapshot, and replacing them on a restore. A small consumer interface
// keeps the backup testable without the whole settings service.
type settingsPort interface {
	getFrom(ctx context.Context, q dbQuerier) (AppSettingsDTO, error)
	Set(ctx context.Context, settings AppSettingsDTO) (AppSettingsDTO, error)
}

// BackupService exports the operator's configuration to a file they choose
// (ADR 0098). It reads through the existing settings and bookmark stores
// rather than touching SQLite directly, and never reads a credential.
type BackupService struct {
	db        *db.DB
	bookmarks *bookmarks.Store
	settings  settingsPort
	picker    FilePicker
}

// NewBackupService builds the service over the state database, the bookmark
// store and a settings reader. The database is used only to take one
// read-transaction snapshot of the configuration; a file picker is wired
// separately by the shell.
func NewBackupService(database *db.DB, store *bookmarks.Store, settings settingsPort) *BackupService {
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
			AuthVersion:        r.AuthVersion,
			UserDomain:         r.UserDomain,
			ProjectName:        r.ProjectName,
			ProjectDomain:      r.ProjectDomain,
			Region:             r.Region,
			TLSMode:            r.TLSMode,
			CACertPEM:          string(bookmarks.CertificatesOnly([]byte(r.CACertPEM))),
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

// ImportSettings restores the operator's configuration from a backup file
// they choose (ADR 0098). It validates the whole file before it changes
// anything — refusing a schema version it does not understand, a malformed
// bookmark, or a CA bundle carrying anything but bare certificates, so a bad
// file applies nothing and no key reaches trust storage — then replaces the
// application settings and merges each bookmark by its complete
// configuration, creating one not already present and leaving an existing one
// untouched. It carries no credential, so restored bookmarks need their
// passwords re-entered, and it never starts a transfer, touches a remote, or
// deletes anything.
// A pre-apply refusal and an ordinary cancellation both return no result: an
// error for the refusal, nil for a cancelled picker. Once settings are
// applied a later bookmark problem is a partial restore, not a failure, so the
// outcome is carried in the result — what landed, what remains, and anything
// unconfirmed — rather than an error the bridge would drop. A re-run is safe
// because the merge is by identity.
func (s *BackupService) ImportSettings(ctx context.Context) (*RestoreResultDTO, error) {
	if s.picker == nil {
		return nil, errors.New("no file picker available (headless shell)")
	}
	files, err := s.picker.PickFiles("Choose a settings backup to restore")
	if err != nil || len(files) == 0 {
		// An empty choice with no error is an ordinary cancellation.
		return nil, err
	}
	raw, err := os.ReadFile(files[0])
	if err != nil {
		return nil, err
	}
	var doc SettingsBackupDTO
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("this file is not a valid settings backup: %w", err)
	}
	if doc.SchemaVersion < minReadableBackupVersion || doc.SchemaVersion > backupSchemaVersion {
		return nil, fmt.Errorf("unsupported backup version %d; this build restores versions %d to %d", doc.SchemaVersion, minReadableBackupVersion, backupSchemaVersion)
	}

	// Validate the whole file before any effect: a malformed bookmark, or a CA
	// bundle that holds anything but bare certificates, refuses the restore
	// rather than applying part of it or storing non-trust material. The
	// canonical certificate bytes validated here are the ones stored.
	type pending struct {
		mark  bookmarks.Bookmark
		caPEM []byte
	}
	marks := make([]pending, 0, len(doc.Bookmarks))
	for i, bm := range doc.Bookmarks {
		mark := bookmarks.Bookmark{
			DisplayName:        bm.DisplayName,
			AuthURL:            bm.AuthURL,
			Username:           bm.Username,
			StorageURLOverride: bm.StorageURLOverride,
			AuthVersion:        bm.AuthVersion,
			UserDomain:         bm.UserDomain,
			ProjectName:        bm.ProjectName,
			ProjectDomain:      bm.ProjectDomain,
			Region:             bm.Region,
			TLSMode:            swiftcontrol.TLSMode(bm.TLSMode),
			PerformanceProfile: bm.PerformanceProfile,
			SegmentPlacement:   bookmarks.SegmentPlacement(bm.SegmentPlacement),
			ChunkSizeBytes:     bm.ChunkSizeBytes,
		}
		if err := bookmarks.Validate(mark); err != nil {
			return nil, fmt.Errorf("bookmark %d (%q): %w", i+1, bm.DisplayName, err)
		}
		var caPEM []byte
		if mark.TLSMode == swiftcontrol.TLSModeCustomCA {
			caPEM, err = bookmarks.CanonicalCACertificates([]byte(bm.CACertPEM))
			if err != nil {
				return nil, fmt.Errorf("bookmark %d (%q): %w", i+1, bm.DisplayName, err)
			}
		}
		marks = append(marks, pending{mark: mark, caPEM: caPEM})
	}

	result := &RestoreResultDTO{BookmarksTotal: len(marks)}
	if _, err := s.settings.Set(ctx, doc.AppSettings); err != nil {
		// Set can fail at its commit boundary after the new settings were
		// applied, so this is an uncertain outcome, not a proved refusal.
		// Report it and stop before touching bookmarks; the shell reloads the
		// settings to show whatever landed rather than writing a stale
		// document over it. SettingsApplied stays false because it is not
		// confirmed, and Complete stays false.
		result.Detail = "the settings could not be confirmed; reload to see what was applied"
		return result, nil
	}
	result.SettingsApplied = true
	for i, p := range marks {
		outcome, err := s.bookmarks.ImportBookmark(ctx, p.mark, p.caPEM)
		switch {
		case err == nil && outcome == bookmarks.ImportCreated:
			result.BookmarksCreated++
		case err == nil && outcome == bookmarks.ImportSkipped:
			result.BookmarksSkipped++
		case outcome == bookmarks.ImportUnresolved:
			// The commit reply was lost; the row may or may not be present.
			// Report it and stop, since a further insert could duplicate it.
			result.BookmarksUnresolved++
			result.Detail = fmt.Sprintf("bookmark %d (%q) could not be confirmed; reload when local state is available", i+1, p.mark.DisplayName)
			return result, nil
		default:
			result.Detail = fmt.Sprintf("bookmark %d (%q) could not be restored: %v; re-run to finish", i+1, p.mark.DisplayName, err)
			return result, nil
		}
	}
	result.Complete = true
	return result, nil
}
