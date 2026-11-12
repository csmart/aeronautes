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

// The settings backup refuses on a headless shell and, with a directory
// dialog wired, writes the operator's configuration to a file (ADR 0098).
func TestBackupSettingsWritesConfiguration(t *testing.T) {
	ctx := t.Context()
	database, err := db.Open(ctx, filepath.Join(t.TempDir(), "state.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := database.Close(); err != nil {
			t.Error(err)
		}
	})
	store := bookmarks.NewStore(database, secrets.NewSessionStore())
	if _, err := store.Save(ctx, bookmarks.Bookmark{
		DisplayName: "Prod AU",
		AuthURL:     "https://swift.example/auth/v1.0",
		Username:    "acct:user",
	}, []byte("swift-key")); err != nil {
		t.Fatal(err)
	}
	runner := app.NewJobRunner(jobs.NewStore(database), audit.NewStore(database))
	settings := app.NewSettingsService(filepath.Join(t.TempDir(), "settings.json"), runner)

	// Refusal: a headless service has no directory dialog.
	headless := &Harness{T: t, Backup: app.NewBackupService(database, store, settings)}
	if _, refused := headless.ExportSettings(ctx); refused.Err() == nil {
		t.Fatal("headless backup did not refuse")
	} else {
		refused.CreditRefusal()
	}

	// Happy: a wired dialog writes the backup and reports what it wrote.
	wired := app.NewBackupService(database, store, settings)
	app.WireBackupFilePicker(wired, exportDirPicker{dir: t.TempDir()})
	h := &Harness{T: t, Backup: wired}
	result, receipt := h.ExportSettings(ctx)
	if receipt.Err() != nil || result == nil || result.BookmarkCount != 1 {
		t.Fatalf("backup = %+v %v", result, receipt.Err())
	}
	receipt.CreditRead()
}
