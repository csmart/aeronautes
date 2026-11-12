package systemtest

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/csmart/aeronautes/internal/app"
	"github.com/csmart/aeronautes/internal/bookmarks"
	"github.com/csmart/aeronautes/internal/db"
	"github.com/csmart/aeronautes/internal/jobs"
	"github.com/csmart/aeronautes/internal/secrets"
)

// The ADR 0114 import against a local store: a save of a previewed rclone
// remote creates the bookmark with its key in the secret store, a save
// with a spent token is refused, and the status reads the outcome. No
// store is contacted, because nothing is verified.
func TestImportBookmarksSavesAPreview(t *testing.T) {
	ctx := context.Background()
	database, err := db.Open(ctx, filepath.Join(t.TempDir(), "import.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := database.Close(); err != nil {
			t.Error(err)
		}
	})
	store := bookmarks.NewStore(database, secrets.NewSessionStore())
	bookmarkService := app.NewBookmarkService(store, app.NewConnections(store), jobs.NewStore(database))
	conf := filepath.Join(t.TempDir(), "rclone.conf")
	if err := os.WriteFile(conf, []byte("[archive]\ntype = swift\nuser = acct:user\nkey = import-key\nauth = https://swift.example/auth/v1.0\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	service := app.NewBookmarkImportService(bookmarkService, store)
	app.WireImportFilePicker(service, restoreFilePicker{file: conf})
	h := &Harness{T: t, Import: service}

	preview, err := service.PreviewImport(ctx)
	if err != nil || len(preview.Rows) != 1 || preview.Rows[0].State != "importable" {
		t.Fatalf("preview = %+v, %v", preview, err)
	}
	result, receipt := h.SaveImport(ctx, preview.Token, []int{0}, false)
	if receipt.Err() != nil || len(result.Outcomes) != 1 || result.Outcomes[0].BookmarkID == "" {
		t.Fatalf("save = %+v, %v", result, receipt.Err())
	}
	receipt.CreditApplied()
	if key, err := store.Credential(ctx, result.Outcomes[0].BookmarkID); err != nil || string(key) != "import-key" {
		t.Fatalf("the imported key is not in the secret store: %v", err)
	}

	if _, refused := h.SaveImport(ctx, preview.Token, []int{0}, false); refused.Err() == nil {
		t.Fatal("a save with a spent token was not refused")
	} else {
		refused.CreditRefusal()
	}

	status, read := h.ImportStatus(preview.Token)
	if read.Err() != nil || !status.Done || len(status.Outcomes) != 1 {
		t.Fatalf("status = %+v, %v", status, read.Err())
	}
	read.CreditRead()
}
