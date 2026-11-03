package app

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/csmart/aeronautes/internal/audit"
	"github.com/csmart/aeronautes/internal/db"
)

func newExportStore(t *testing.T) (*audit.Store, context.Context) {
	t.Helper()
	ctx := context.Background()
	d, err := db.Open(ctx, filepath.Join(t.TempDir(), "state.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	return audit.NewStore(d), ctx
}

func seedAudit(t *testing.T, store *audit.Store, ctx context.Context, operation string, n int) {
	t.Helper()
	for range n {
		if err := store.Append(ctx, audit.Event{
			BookmarkID: "bm", Operation: operation, Resource: "r", Summary: "s", Result: audit.ResultApplied,
		}); err != nil {
			t.Fatal(err)
		}
	}
}

func TestExportAuditArchivesTheTrail(t *testing.T) {
	store, ctx := newExportStore(t)
	seedAudit(t, store, ctx, "transfer.copy.start", 3)
	seedAudit(t, store, ctx, "delete.object", 1)

	// Headless: no dialog, so the export refuses rather than guessing.
	if _, err := NewAuditService(store).ExportAudit(ctx, AuditQueryDTO{}); err == nil {
		t.Fatal("headless export did not refuse")
	}

	service := NewAuditService(store)
	service.setFilePicker(fakePicker{dir: t.TempDir()})

	result, err := service.ExportAudit(ctx, AuditQueryDTO{})
	if err != nil || result == nil {
		t.Fatalf("export = %+v %v", result, err)
	}
	if result.Count != 4 {
		t.Fatalf("exported %d events, want 4", result.Count)
	}
	data, err := os.ReadFile(filepath.Join(result.Directory, result.FileName))
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 4 {
		t.Fatalf("file has %d lines, want 4", len(lines))
	}
	var first AuditEventDTO
	if err := json.Unmarshal([]byte(lines[0]), &first); err != nil || first.Operation == "" {
		t.Fatalf("first line is not an audit event: %q (%v)", lines[0], err)
	}

	filtered, err := service.ExportAudit(ctx, AuditQueryDTO{OperationPrefix: "transfer.copy"})
	if err != nil {
		t.Fatal(err)
	}
	if filtered.Count != 3 {
		t.Fatalf("filtered export = %d events, want 3", filtered.Count)
	}
}

// The export reads the whole matching trail, well past one 500-row page.
// (The former paging also read the whole stable trail; its defect was a
// prune between page reads, covered by the audit-package snapshot test.)
func TestExportAuditReadsTheWholeTrail(t *testing.T) {
	store, ctx := newExportStore(t)
	seedAudit(t, store, ctx, "transfer.copy.start", 600)

	service := NewAuditService(store)
	service.setFilePicker(fakePicker{dir: t.TempDir()})
	result, err := service.ExportAudit(ctx, AuditQueryDTO{})
	if err != nil {
		t.Fatal(err)
	}
	if result.Count != 600 {
		t.Fatalf("exported %d events, want all 600", result.Count)
	}
	data, err := os.ReadFile(filepath.Join(result.Directory, result.FileName))
	if err != nil {
		t.Fatal(err)
	}
	if lines := strings.Count(strings.TrimSpace(string(data)), "\n") + 1; lines != 600 {
		t.Fatalf("file has %d lines, want 600", lines)
	}
}

// A failed export must never publish a complete-looking archive: it
// writes to a .part and publishes by link only on success, so a failure
// leaves no .jsonl (db:971, db:972).
func TestExportAuditRemovesFailedOutput(t *testing.T) {
	store, ctx := newExportStore(t)
	seedAudit(t, store, ctx, "transfer.copy.start", 3)
	dir := t.TempDir()

	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	service := NewAuditService(store)
	service.setFilePicker(fakePicker{dir: dir})
	if _, err := service.ExportAudit(cancelled, AuditQueryDTO{}); err == nil {
		t.Fatal("a cancelled export did not fail")
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".jsonl") {
			t.Fatalf("a failed export left a complete-looking file: %s", entry.Name())
		}
	}
}

// Publication refuses a destination occupied after the .part was
// allocated (a late-arriving file at the final name), stepping to a fresh
// suffix instead of replacing it (db:973). os.Link is atomic, so there is
// no check-and-use gap to race.
func TestLinkPublishRefusesAnOccupiedDestination(t *testing.T) {
	dir := t.TempDir()
	const base = "aeronautes-audit-20260102T000000Z"
	partPath := filepath.Join(dir, base+".jsonl.part")
	if err := os.WriteFile(partPath, []byte(`{"id":1}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	final := filepath.Join(dir, base+".jsonl")
	if err := os.WriteFile(final, []byte("KEEP\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	name, err := linkPublish(partPath, dir, base, ".jsonl")
	if err != nil {
		t.Fatal(err)
	}
	if name != base+"-1.jsonl" {
		t.Fatalf("published as %q, want the stepped suffix", name)
	}
	if data, _ := os.ReadFile(final); string(data) != "KEEP\n" {
		t.Fatal("publication replaced the occupied final name")
	}
	if data, _ := os.ReadFile(filepath.Join(dir, name)); string(data) != `{"id":1}`+"\n" {
		t.Fatalf("published file %q does not hold the export content", name)
	}
	if _, err := os.Stat(partPath); !os.IsNotExist(err) {
		t.Fatal("the .part was not unlinked after publication")
	}
}

func TestLinkPublishDoesNotReplaceASymlink(t *testing.T) {
	dir := t.TempDir()
	const base = "aeronautes-audit-20260102T000000Z"
	partPath := filepath.Join(dir, base+".jsonl.part")
	if err := os.WriteFile(partPath, []byte(`{"id":1}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	victim := filepath.Join(dir, "victim.txt")
	if err := os.WriteFile(victim, []byte("VICTIM\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(victim, filepath.Join(dir, base+".jsonl")); err != nil {
		t.Fatal(err)
	}
	name, err := linkPublish(partPath, dir, base, ".jsonl")
	if err != nil {
		t.Fatal(err)
	}
	if name != base+"-1.jsonl" {
		t.Fatalf("published as %q, want the stepped suffix past the symlink", name)
	}
	if data, _ := os.ReadFile(victim); string(data) != "VICTIM\n" {
		t.Fatal("publication followed a symlink and clobbered its target")
	}
}
