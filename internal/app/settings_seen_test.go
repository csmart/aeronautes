package app

import (
	"context"
	"database/sql"
	"testing"
)

// lastSeenRow lists the account's containers and returns the named row.
func lastSeenRow(t *testing.T, h *harness, name string) ContainerRowDTO {
	t.Helper()
	page, err := h.browse.Containers(context.Background(), h.connected, "", 100)
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range page.Rows {
		if row.Name == name {
			return row
		}
	}
	t.Fatalf("container %q not listed", name)
	return ContainerRowDTO{}
}

// newGeneration saves the bookmark again with the same key, which publishes
// a new generation, so the in-memory facts of the old one no longer apply.
func newGeneration(t *testing.T, h *harness) {
	t.Helper()
	list, err := h.bookmarks.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, b := range list {
		if b.ID == h.connected {
			if _, err := h.bookmarks.Save(context.Background(), b, "testing"); err != nil {
				t.Fatal(err)
			}
		}
	}
	if _, err := h.browse.Connect(context.Background(), h.connected); err != nil {
		t.Fatal(err)
	}
}

// A settings fact is remembered and shown as last seen on a later
// connection for a row with no current fact, never beside a current one,
// and only under the storage URL it was read under (ADR 0110 decision 3).
func TestSettingsAreRememberedAsLastSeen(t *testing.T) {
	h := newHarness(t)
	h.connect(t)
	ctx := context.Background()
	detail, err := h.inspector.Container(ctx, h.connected, "fixtures")
	if err != nil {
		t.Fatal(err)
	}
	if row := lastSeenRow(t, h, "fixtures"); row.Settings == nil || row.LastSeen != nil {
		t.Fatalf("under the same connection the row carries %+v and last seen %+v; want only the current fact", row.Settings, row.LastSeen)
	}

	newGeneration(t, h)
	row := lastSeenRow(t, h, "fixtures")
	if row.Settings != nil || row.LastSeen == nil || row.LastSeen.ObservedAt != detail.Settings.ObservedAt {
		t.Fatalf("on a later connection the row carries %+v and last seen %+v; want the remembered fact only", row.Settings, row.LastSeen)
	}

	// Remembered under another storage URL, it is not this account's.
	session, err := h.browse.connections.SessionFor(ctx, h.connected)
	if err != nil {
		t.Fatal(err)
	}
	if err := h.browse.connections.seen.record(ctx, h.connected, session.StorageURL()+"/other", "confirmed_segments", detail.Settings); err != nil {
		t.Fatal(err)
	}
	if row := lastSeenRow(t, h, "confirmed_segments"); row.LastSeen != nil {
		t.Fatalf("settings remembered under another storage URL were shown: %+v", row.LastSeen)
	}
}

// A container a HEAD finds gone loses its remembered settings, and
// deleting the bookmark removes all of them.
func TestRememberedSettingsGoWithTheContainerAndTheBookmark(t *testing.T) {
	h := newHarness(t)
	h.connect(t)
	ctx := context.Background()
	session, err := h.browse.connections.SessionFor(ctx, h.connected)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.inspector.Container(ctx, h.connected, "fixtures"); err != nil {
		t.Fatal(err)
	}
	if err := h.browse.connections.seen.record(ctx, h.connected, session.StorageURL(), "gone", ContainerSettingsDTO{ObservedAt: "2026-11-12T00:00:00Z"}); err != nil {
		t.Fatal(err)
	}
	if _, err := h.inspector.Container(ctx, h.connected, "gone"); err == nil {
		t.Fatal("a missing container was read")
	}
	if remembered := rememberedCount(t, h); remembered != 1 {
		t.Fatalf("%d remembered rows after the missing container's HEAD, want only fixtures'", remembered)
	}
	if err := h.bookmarks.Delete(ctx, h.connected); err != nil {
		t.Fatal(err)
	}
	if remembered := rememberedCount(t, h); remembered != 0 {
		t.Fatalf("%d remembered rows outlived their bookmark", remembered)
	}
}

// A remembered value is a convenience: a write that fails leaves the HEAD's
// own result intact.
func TestAFailedRememberLeavesTheReadIntact(t *testing.T) {
	h := newHarness(t)
	h.connect(t)
	ctx := context.Background()
	if err := h.db.WriteTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.Exec(`CREATE TRIGGER refuse_remember BEFORE INSERT ON container_settings_seen BEGIN SELECT RAISE(ABORT, 'fixture refusal'); END`)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	detail, err := h.inspector.Container(ctx, h.connected, "fixtures")
	if err != nil || detail.Settings.ObservedAt == "" {
		t.Fatalf("a failed remember broke the read: %+v %v", detail.Settings, err)
	}
	if row := lastSeenRow(t, h, "fixtures"); row.Settings == nil {
		t.Fatal("a failed remember lost the current fact")
	}
}

func rememberedCount(t *testing.T, h *harness) int {
	t.Helper()
	var n int
	if err := h.db.Reader().QueryRow(`SELECT COUNT(*) FROM container_settings_seen`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}
