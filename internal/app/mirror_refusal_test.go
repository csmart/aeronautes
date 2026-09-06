package app

import (
	"context"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/csmart/aeronautes/internal/swifttest"
	"github.com/csmart/aeronautes/internal/systemtest/oracle"
)

const mirrorRefusalMessage = "Mirror is unavailable while concurrent changes cannot be protected"

func mirrorWitness(t *testing.T, mock *swifttest.Server, user, key string) *oracle.Oracle {
	t.Helper()
	server := httptest.NewServer(mock)
	t.Cleanup(server.Close)
	witness, err := oracle.Connect(server.URL+"/auth/v1.0", user, key)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(witness.Close)
	return witness
}

func mirrorSnapshot(t *testing.T, witness *oracle.Oracle, containers ...string) *oracle.AccountSnapshot {
	t.Helper()
	snapshot, err := witness.SnapshotContainers(containers...)
	if err != nil {
		t.Fatal(err)
	}
	return snapshot
}

func TestMirrorRefusesBeforeWork(t *testing.T) {
	for _, variant := range []string{"confirmed", "wrong-confirmation", "empty-request", "cancelled"} {
		t.Run(variant, func(t *testing.T) {
			h := newHarness(t)
			h.connect(t)
			source := h.transfer.connections.ActiveBookmarkID()
			dest, dst := secondCluster(t, h)
			engines := h.installEngine(nil)
			h.mock.AddContainer("media", "standard")
			h.mock.AddContainer("unrelated", "standard")
			dst.AddContainer("backup", "standard")
			dst.AddContainer("backup_segments", "standard")
			dst.AddContainer("unrelated", "standard")
			seedObject(h.mock, "media", "scope/new", "new source bytes", nil)
			seedObject(h.mock, "media", "scope/changed", "updated source", nil)
			seedObject(h.mock, "unrelated", "keep", "source sentinel", nil)
			seedObject(dst, "backup", "scope/changed", "concurrent writer", nil)
			seedObject(dst, "backup", "scope/dest-only", "keep destination", nil)
			seedObject(dst, "backup_segments", "shared/leaf", "shared bytes", nil)
			dst.PutObject("backup", "scope/large", swifttest.Object{Manifest: "backup_segments/shared/"})
			dst.PutObject("unrelated", "alias", swifttest.Object{Manifest: "backup_segments/shared/"})
			seedObject(dst, "unrelated", "keep", "destination sentinel", nil)
			srcWitness := mirrorWitness(t, h.mock, "test:tester", "testing")
			dstWitness := mirrorWitness(t, dst, "test2:tester2", "testing2")
			srcBefore := mirrorSnapshot(t, srcWitness, "media", "unrelated")
			dstBefore := mirrorSnapshot(t, dstWitness, "backup", "backup_segments", "unrelated")
			if alias := dstBefore.Containers["unrelated"].Objects["alias"]; alias.Digest == "" || alias.Size != int64(len("shared bytes")) {
				t.Fatalf("outside alias fixture is not readable: %+v", alias)
			}
			facts := retainedRows(t, h.db)
			auditBefore, err := h.auditSvc.Recent(t.Context(), 100)
			if err != nil {
				t.Fatal(err)
			}
			requestsBefore := []int{len(h.mock.RequestLog()), len(dst.RequestLog())}
			request := MirrorRequestDTO{CopyRequestDTO: CopyRequestDTO{
				SourceBookmarkID: source, SourceContainer: "media", SourcePrefix: "scope/",
				DestBookmarkID: dest, DestContainer: "backup", DestPrefix: "scope/",
			}, Confirm: "backup"}
			ctx := t.Context()
			switch variant {
			case "wrong-confirmation":
				request.Confirm = "wrong"
			case "empty-request":
				request = MirrorRequestDTO{}
			case "cancelled":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}
			id, startErr := h.transfer.StartMirror(ctx, request)
			if id != "" {
				// A restored unsafe start must finish before its storage
				// effects are inspected or the fixture is torn down.
				awaitTerminal(t, h, id)
			}
			if id != "" || startErr == nil || startErr.Error() != mirrorRefusalMessage {
				t.Errorf("Mirror admission = %q, %v; want explicit unavailable refusal", id, startErr)
			}
			if len(*engines) != 0 {
				t.Errorf("refused Mirror constructed %d engines", len(*engines))
			}
			if got := []int{len(h.mock.RequestLog()), len(dst.RequestLog())}; !reflect.DeepEqual(got, requestsBefore) {
				t.Errorf("refused Mirror contacted Swift: before=%v after=%v", requestsBefore, got)
			}
			if rows, err := h.jobsSvc.List(t.Context(), "", false, 100); err != nil || len(rows) != 0 {
				t.Errorf("refused Mirror created jobs: %+v, %v", rows, err)
			}
			if rows := retainedRows(t, h.db); !reflect.DeepEqual(rows, facts) {
				t.Errorf("refused Mirror changed recovery records: %+v", rows)
			}
			if events, err := h.auditSvc.Recent(t.Context(), 100); err != nil || !reflect.DeepEqual(events, auditBefore) {
				t.Errorf("refused Mirror changed audit history: %+v, %v", events, err)
			}
			if delta := oracle.Diff(srcBefore, mirrorSnapshot(t, srcWitness, "media", "unrelated")); !delta.Empty() {
				t.Errorf("refused Mirror changed the source: %s", delta)
			}
			if delta := oracle.Diff(dstBefore, mirrorSnapshot(t, dstWitness, "backup", "backup_segments", "unrelated")); !delta.Empty() {
				t.Errorf("refused Mirror changed the destination or outside data: %s", delta)
			}
			assertJobBindingsReleased(t, h, []string{source, dest})
		})
	}
}

func TestMirrorRefusesWithoutServices(t *testing.T) {
	service := &TransferService{}
	id, err := service.StartMirror(t.Context(), MirrorRequestDTO{})
	if id != "" || err == nil || err.Error() != mirrorRefusalMessage {
		t.Fatalf("unavailable Mirror depended on request validation or services: %q, %v", id, err)
	}
}

func TestMirrorRefusesBeforeReadingBindings(t *testing.T) {
	h := newHarness(t)
	h.connect(t)
	source := h.transfer.connections.ActiveBookmarkID()
	dest, _ := secondCluster(t, h)
	if err := h.db.Close(); err != nil {
		t.Fatal(err)
	}
	id, err := h.transfer.StartMirror(t.Context(), MirrorRequestDTO{CopyRequestDTO: CopyRequestDTO{
		SourceBookmarkID: source, SourceContainer: "media", DestBookmarkID: dest, DestContainer: "backup",
	}, Confirm: "backup"})
	if id != "" || err == nil || err.Error() != mirrorRefusalMessage {
		t.Fatalf("unavailable Mirror tried to read a binding: %q, %v", id, err)
	}
}
