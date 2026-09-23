package systemtest

import (
	"errors"
	"testing"

	"github.com/csmart/aeronautes/internal/app"
)

// readerGenerationCases proves, for the six named live readers of ADR
// 0066 (object, prefix, container, SLO manifest, DLO segments and Find
// start), the generation half of the L11 acceptance basis that ADR 0067
// left source-inspected: a rekey of the connected bookmark refuses every
// one of them until the bookmark reconnects, and the reconnected reads
// answer the same account under a higher generation, so a caller holding
// the older portrait can tell the reply apart. The overlay schedules in
// named_read_snapshot_test.go cover a rekey that lands mid-read; this is
// the sequential case a settings edit produces.
func readerGenerationCases(t *testing.T, h *Harness, mock bool) {
	reads := namedReads(t, h, mock)
	beforeA, beforeB := h.SnapshotNamespace(h.Primary), h.SnapshotNamespace(h.Secondary)
	namedReadConnect(t, h, h.Primary)
	ctx := t.Context()
	first := map[string]int64{}
	for _, read := range reads {
		reply := read.read(ctx, h.Primary)
		if reply.err != nil || reply.owner != "primary" || reply.scope.BookmarkID != h.Primary.BookmarkID {
			t.Fatalf("%s before the rekey = %+v, want primary", read.name, reply)
		}
		first[read.name] = reply.scope.Generation
	}
	// Re-entering the key raises the bookmark's generation without moving
	// the account, and saving the connected bookmark retires its
	// connection. Until the bookmark reconnects, every reader must refuse
	// as not connected rather than answer under either generation.
	namedReadRekey(t, h, h.Primary)
	for _, read := range reads {
		reply := read.read(ctx, h.Primary)
		if !errors.Is(reply.err, app.ErrNotConnected) {
			t.Fatalf("%s after the rekey = %+v, want ErrNotConnected", read.name, reply)
		}
	}
	if _, err := h.Browse.Connect(ctx, h.Primary.BookmarkID); err != nil {
		t.Fatal(err)
	}
	for _, read := range reads {
		reply := read.read(ctx, h.Primary)
		if reply.err != nil || reply.owner != "primary" || reply.scope.BookmarkID != h.Primary.BookmarkID {
			t.Fatalf("%s after reconnecting = %+v, want primary", read.name, reply)
		}
		if reply.scope.Generation <= first[read.name] {
			t.Fatalf("%s after reconnecting answered generation %d, want above %d", read.name, reply.scope.Generation, first[read.name])
		}
	}
	h.AssertNoCollateral(beforeA, h.SnapshotNamespace(h.Primary))
	h.AssertNoCollateral(beforeB, h.SnapshotNamespace(h.Secondary))
}

func TestReaderGenerationMock(t *testing.T)      { readerGenerationCases(t, findMockHarness(t), true) }
func TestReaderGenerationRealSwift(t *testing.T) { readerGenerationCases(t, New(t), false) }
