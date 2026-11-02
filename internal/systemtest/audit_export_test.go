package systemtest

import (
	"path/filepath"
	"testing"

	"github.com/csmart/aeronautes/internal/app"
	"github.com/csmart/aeronautes/internal/audit"
	"github.com/csmart/aeronautes/internal/db"
)

type exportDirPicker struct{ dir string }

func (p exportDirPicker) PickFiles(string) ([]string, error)   { return nil, nil }
func (p exportDirPicker) PickDirectory(string) (string, error) { return p.dir, nil }

// The §51 export refuses on a headless shell and, with a directory
// dialog wired, archives the trail to a file the operator can keep.
func TestExportAuditArchivesTheTrail(t *testing.T) {
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
	store := audit.NewStore(database)
	if err := store.Append(ctx, audit.Event{
		BookmarkID: "bm", Operation: "transfer.copy.start", Resource: "bm/object",
		Summary: "copied", Result: audit.ResultApplied,
	}); err != nil {
		t.Fatal(err)
	}

	// Refusal: a headless service has no directory dialog.
	headless := &Harness{T: t, Audit: app.NewAuditService(store)}
	if _, refused := headless.ExportAudit(ctx, app.AuditQueryDTO{}); refused.Err() == nil {
		t.Fatal("headless export did not refuse")
	} else {
		refused.CreditRefusal()
	}

	// Happy: a wired dialog writes the archive and reports what it wrote.
	wired := app.NewAuditService(store)
	app.WireAuditFilePicker(wired, exportDirPicker{dir: t.TempDir()})
	archive := &Harness{T: t, Audit: wired}
	result, receipt := archive.ExportAudit(ctx, app.AuditQueryDTO{})
	if receipt.Err() != nil || result == nil || result.Count != 1 {
		t.Fatalf("export = %+v %v", result, receipt.Err())
	}
	receipt.CreditRead()
}
