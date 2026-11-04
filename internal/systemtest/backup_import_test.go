package systemtest

import (
	"path/filepath"
	"testing"

	"github.com/csmart/aeronautes/internal/app"
	"github.com/csmart/aeronautes/internal/audit"
	"github.com/csmart/aeronautes/internal/bookmarks"
	"github.com/csmart/aeronautes/internal/db"
	"github.com/csmart/aeronautes/internal/jobs"
	"github.com/csmart/aeronautes/internal/secrets"
)

// restoreFilePicker chooses a specific backup file for a restore, standing in
// for the shell's native open dialog.
type restoreFilePicker struct{ file string }

func (p restoreFilePicker) PickFiles(string) ([]string, error)   { return []string{p.file}, nil }
func (p restoreFilePicker) PickDirectory(string) (string, error) { return "", nil }

// A restore refuses on a headless shell and, with a file dialog wired, applies
// a backup an export wrote: the settings are replaced and each bookmark is
// recreated once, credential-less, on a fresh instance (ADR 0098).
func TestRestoreSettingsAppliesBackup(t *testing.T) {
	ctx := t.Context()

	// Write a backup from a populated source instance.
	sourceDB, err := db.Open(ctx, filepath.Join(t.TempDir(), "source.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := sourceDB.Close(); err != nil {
			t.Error(err)
		}
	})
	sourceStore := bookmarks.NewStore(sourceDB, secrets.NewSessionStore())
	if _, err := sourceStore.Save(ctx, bookmarks.Bookmark{
		DisplayName: "Prod AU",
		AuthURL:     "https://swift.example/auth/v1.0",
		Username:    "acct:user",
	}, []byte("swift-key")); err != nil {
		t.Fatal(err)
	}
	sourceSettings := app.NewSettingsService(sourceDB, app.NewJobRunner(jobs.NewStore(sourceDB), audit.NewStore(sourceDB)))
	exporter := app.NewBackupService(sourceDB, sourceStore, sourceSettings)
	backupDir := t.TempDir()
	app.WireBackupFilePicker(exporter, exportDirPicker{dir: backupDir})
	exported, err := exporter.ExportSettings(ctx)
	if err != nil || exported == nil {
		t.Fatalf("export for restore fixture: %+v %v", exported, err)
	}
	backupFile := filepath.Join(backupDir, exported.FileName)

	// A fresh target instance starts empty.
	targetDB, err := db.Open(ctx, filepath.Join(t.TempDir(), "target.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := targetDB.Close(); err != nil {
			t.Error(err)
		}
	})
	targetStore := bookmarks.NewStore(targetDB, secrets.NewSessionStore())
	targetSettings := app.NewSettingsService(targetDB, app.NewJobRunner(jobs.NewStore(targetDB), audit.NewStore(targetDB)))

	// Refusal: a headless target has no open dialog.
	headless := &Harness{T: t, Backup: app.NewBackupService(targetDB, targetStore, targetSettings)}
	if _, refused := headless.ImportSettings(ctx); refused.Err() == nil {
		t.Fatal("headless restore did not refuse")
	} else {
		refused.CreditRefusal()
	}

	// Happy: a wired dialog applies the backup to the fresh target.
	restorer := app.NewBackupService(targetDB, targetStore, targetSettings)
	app.WireBackupFilePicker(restorer, restoreFilePicker{file: backupFile})
	h := &Harness{T: t, Backup: restorer}
	result, receipt := h.ImportSettings(ctx)
	if receipt.Err() != nil || result == nil || !result.SettingsApplied || result.BookmarksCreated != 1 || result.BookmarksSkipped != 0 {
		t.Fatalf("restore = %+v %v", result, receipt.Err())
	}
	receipt.CreditApplied()

	// The bookmark now exists on the target with no credential: its password
	// must be re-entered the next time the operator uses the account.
	marks, err := targetStore.List(ctx)
	if err != nil || len(marks) != 1 {
		t.Fatalf("target bookmarks after restore = %+v %v", marks, err)
	}
	if marks[0].HasCredential {
		t.Fatalf("restore carried a credential: %+v", marks[0])
	}
}
