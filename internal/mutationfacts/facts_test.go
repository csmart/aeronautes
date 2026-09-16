package mutationfacts

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/csmart/aeronautes/internal/db"
)

func TestRecordIntentAndOutcomeSurviveReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.sqlite")
	ctx := context.Background()
	d, err := db.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	store := NewStore(d)
	identity, err := NewIdentity()
	if err != nil {
		t.Fatal(err)
	}
	if err := store.RecordIntent(ctx, Intent{
		Identity: identity, BookmarkID: "bm", Operation: "object.metadata.update",
		Resource: "fixtures/normal/hello.txt",
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.RecordOutcome(ctx, identity, OutcomeApplied, "set 1 metadata keys", []string{"tx-a"}, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `SELECT 1`)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	d.Close()

	reopened, err := db.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	got, err := NewStore(reopened).Lookup(ctx, identity)
	if err != nil {
		t.Fatal(err)
	}
	if got.Outcome != OutcomeApplied || got.Resource != "fixtures/normal/hello.txt" {
		t.Fatalf("reopened = %+v", got)
	}
	if len(got.TransIDs) != 1 || got.TransIDs[0] != "tx-a" {
		t.Fatalf("trans ids = %v", got.TransIDs)
	}
}

func TestRecordOutcomeRequiresIntentRow(t *testing.T) {
	d, err := db.Open(context.Background(), filepath.Join(t.TempDir(), "state.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	store := NewStore(d)
	err = store.RecordOutcome(context.Background(), "missing", OutcomeApplied, "x", nil, func(*sql.Tx) error {
		return nil
	})
	if err == nil {
		t.Fatal("expected missing intent to fail")
	}
}

func TestRecordOutcomeDoesNotRedecide(t *testing.T) {
	d, err := db.Open(context.Background(), filepath.Join(t.TempDir(), "state.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	store := NewStore(d)
	ctx := context.Background()
	identity, err := NewIdentity()
	if err != nil {
		t.Fatal(err)
	}
	if err := store.RecordIntent(ctx, Intent{
		Identity: identity, BookmarkID: "bm", Operation: "object.metadata.update",
		Resource: "fixtures/a",
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.RecordOutcome(ctx, identity, OutcomeApplied, "applied", nil, func(*sql.Tx) error {
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.RecordOutcome(ctx, identity, OutcomeUnverified, "rewrite", nil, func(*sql.Tx) error {
		return nil
	}); err == nil {
		t.Fatal("decided fact must not be rewritten")
	}
	got, err := store.Lookup(ctx, identity)
	if err != nil {
		t.Fatal(err)
	}
	if got.Outcome != OutcomeApplied {
		t.Fatalf("redecide left outcome %s", got.Outcome)
	}
}
