package app

import (
	"database/sql"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/csmart/aeronautes/internal/mutationfacts"
)

func TestContainerMetadataDrainWaitsForOutcome(t *testing.T) {
	h := newHarness(t)
	h.connect(t)
	held := make(chan struct{})
	releasePost := make(chan struct{})
	var once, postOnce sync.Once
	unblockPost := func() { postOnce.Do(func() { close(releasePost) }) }
	t.Cleanup(unblockPost)
	h.mock.AfterRequest = func(method, path string) {
		if method == "POST" && strings.HasSuffix(path, "/fixtures") {
			once.Do(func() { close(held); <-releasePost })
		}
	}
	written := make(chan error, 1)
	go func() {
		_, err := h.mutate.UpdateContainer(t.Context(), h.portrait(), "fixtures", "", ContainerEditDTO{Meta: map[string]string{"Held": "yes"}})
		written <- err
	}()
	select {
	case <-held:
	case <-time.After(2 * time.Second):
		t.Fatal("container POST never arrived")
	}
	// Hold the real database writer slot after the intent committed, so
	// shutdown must join the required outcome transaction, not just HTTP.
	dbHeld := make(chan struct{})
	releaseDB := make(chan struct{})
	var dbOnce sync.Once
	unblockDB := func() { dbOnce.Do(func() { close(releaseDB) }) }
	t.Cleanup(unblockDB)
	dbDone := make(chan error, 1)
	go func() {
		dbDone <- h.db.WriteTx(t.Context(), func(*sql.Tx) error {
			close(dbHeld)
			<-releaseDB
			return nil
		})
	}()
	select {
	case <-dbHeld:
	case <-time.After(2 * time.Second):
		t.Fatal("database writer slot was not acquired")
	}
	unblockPost()
	drained := make(chan error, 1)
	go func() { drained <- h.mutate.shutdownWriters(t.Context()) }()
	select {
	case err := <-written:
		t.Fatalf("write returned before its outcome transaction: %v", err)
	case err := <-drained:
		t.Fatalf("drain returned before its outcome transaction: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	unblockDB()
	for name, done := range map[string]<-chan error{"database": dbDone, "write": written, "drain": drained} {
		select {
		case err := <-done:
			if err != nil {
				t.Fatalf("%s returned %v", name, err)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("%s did not finish", name)
		}
	}
	facts, err := mutationfacts.NewStore(h.db).All(t.Context())
	if err != nil || len(facts) != 1 || facts[0].Outcome != mutationfacts.OutcomeApplied {
		t.Fatalf("drained facts = %+v, %v", facts, err)
	}
}
