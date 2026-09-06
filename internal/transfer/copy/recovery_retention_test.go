package copy

import (
	"context"
	"crypto/md5"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/csmart/aeronautes/internal/swifttest"
	"github.com/csmart/aeronautes/internal/systemtest/oracle"
	"github.com/csmart/aeronautes/internal/transfer/rclone"
)

func seedRecoveryAliases(t *testing.T, mock *swifttest.Server, container, prefix string) {
	t.Helper()
	data := []byte("LIVEDATA")
	name := prefix + "00000000"
	seed(mock, container, name, data, nil)
	mock.PutObject("unrelated", "outside-dlo", swifttest.Object{Manifest: container + "/" + prefix})
	manifest, err := json.Marshal([]map[string]any{{"name": "/" + container + "/" + name, "bytes": len(data), "hash": fmt.Sprintf("%x", md5.Sum(data))}})
	if err != nil {
		t.Fatal(err)
	}
	// The mock does not assemble SLO bodies. The independent reader also
	// checks the referenced leaf; the real-Swift test covers assembly.
	mock.PutObject("unrelated", "outside-slo", swifttest.Object{
		Bytes: int64(len(data)), Data: data, SLO: true, SLOManifestJSON: string(manifest),
	})
	seed(mock, "unrelated", "keep", []byte("unchanged"), nil)
}

func recoverySnapshot(t *testing.T, witness *oracle.Oracle) *oracle.AccountSnapshot {
	t.Helper()
	snapshot, err := witness.SnapshotContainers("backup", "backup_segments", "unrelated")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"outside-dlo", "outside-slo"} {
		object := snapshot.Containers["unrelated"].Objects[name]
		if object.Size != 8 || object.Digest != fmt.Sprintf("%x", sha256.Sum256([]byte("LIVEDATA"))) || len(object.Segments) != 1 {
			t.Fatalf("outside alias %s = %+v", name, object)
		}
	}
	return snapshot
}

func TestRecoveryRetainsOutsideAliases(t *testing.T) {
	for _, provenance := range []string{"captured", "committed", "committing", "malformed-prefix", "malformed-live", "missing-facts"} {
		t.Run(provenance, func(t *testing.T) {
			w := newWorld(t)
			prefix := ".aeronautes/stage/old/batch/object/2026-random/"
			if provenance == "malformed-prefix" {
				prefix = "orphan/"
			}
			p := rclone.SegmentPrefix{Container: "backup_segments", Prefix: prefix}
			seedRecoveryAliases(t, w.dstMock, p.Container, p.Prefix)
			input := SweepInput{DstContainer: "backup", Captured: []rclone.SegmentPrefix{p}}
			switch provenance {
			case "committed":
				input.Committed = []rclone.SegmentPrefix{p}
			case "committing":
				input.Committing = map[rclone.SegmentPrefix]string{p: "backup/missing-live"}
			case "malformed-live":
				input.Committing = map[rclone.SegmentPrefix]string{p: "malformed"}
			case "missing-facts":
				input.Captured = nil
			}
			witness := copyOracle(t, w.dstMock)
			before := recoverySnapshot(t, witness)
			requests := len(w.dstMock.RequestLog())
			report, err := SweepLitter(context.Background(), w.dst, input)
			if err != nil || report.SweptSegments != 0 || report.SweptStaged != 0 || len(report.Notes) == 0 {
				t.Errorf("recovery = %+v, %v", report, err)
			}
			if got := len(w.dstMock.RequestLog()); got != requests {
				t.Errorf("recovery contacted Swift: %d requests", got-requests)
			}
			after := recoverySnapshot(t, witness)
			if delta := oracle.Diff(before, after); !delta.Empty() {
				t.Fatalf("recovery changed outside aliases or backing data: %s", delta)
			}
		})
	}
}

func TestReconcileRetainsStagingAliases(t *testing.T) {
	w := newWorld(t)
	executor := newExecutor(t, w, nil)
	prefix := executor.StagingPrefix() + "residue/"
	seedRecoveryAliases(t, w.dstMock, "backup", prefix)
	witness := copyOracle(t, w.dstMock)
	before := recoverySnapshot(t, witness)
	requests := len(w.dstMock.RequestLog())
	report, err := executor.Reconcile(context.Background(), &Report{StagedResidue: []string{prefix + "00000000"}})
	if err != nil || report.SweptStaged != 0 || len(report.Notes) == 0 {
		t.Errorf("reconciliation = %+v, %v", report, err)
	}
	if got := len(w.dstMock.RequestLog()); got != requests {
		t.Errorf("reconciliation contacted Swift: %d requests", got-requests)
	}
	if delta := oracle.Diff(before, recoverySnapshot(t, witness)); !delta.Empty() {
		t.Fatalf("reconciliation changed staging aliases or backing data: %s", delta)
	}
}
