package integration

import (
	"context"
	"crypto/md5"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/csmart/aeronautes/internal/systemtest/oracle"
	transfercopy "github.com/csmart/aeronautes/internal/transfer/copy"
	"github.com/csmart/aeronautes/internal/transfer/rclone"
	"github.com/ncw/swift/v2"
)

func TestRecoveryRetentionRealSwift(t *testing.T) {
	h := harness(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	data, segments, aliases := h.Container("recovery"), h.Container("recovery_segments"), h.Container("recovery-aliases")
	for _, container := range []string{data, segments, aliases} {
		if err := h.conn.ContainerCreate(ctx, container, nil); err != nil {
			t.Fatal(err)
		}
	}
	const payload = "outside live bytes"
	const stage = ".aeronautes/stage/old/"
	captured := rclone.SegmentPrefix{Container: segments, Prefix: stage + "batch/object/2026-random/"}
	for _, target := range []struct{ kind, container, prefix string }{
		{"staging", data, stage + "held/"},
		{"captured", captured.Container, captured.Prefix},
	} {
		name := target.prefix + "00000000"
		if err := h.put(ctx, target.container, name, []byte(payload), "text/plain", nil); err != nil {
			t.Fatal(err)
		}
		if err := h.put(ctx, aliases, target.kind+"-dlo", nil, "text/plain", swift.Headers{"X-Object-Manifest": target.container + "/" + target.prefix}); err != nil {
			t.Fatal(err)
		}
		manifest, err := json.Marshal([]map[string]any{{
			"path": "/" + target.container + "/" + name, "size_bytes": len(payload), "etag": fmt.Sprintf("%x", md5.Sum([]byte(payload))),
		}})
		if err != nil {
			t.Fatal(err)
		}
		if err := h.rawPut(ctx, aliases+"/"+target.kind+"-slo?multipart-manifest=put", manifest, "text/plain"); err != nil {
			t.Fatal(err)
		}
	}
	if err := h.put(ctx, aliases, "keep", []byte("unchanged"), "text/plain", nil); err != nil {
		t.Fatal(err)
	}
	witness, err := oracle.Connect(os.Getenv("AERONAUTES_SAIO_AUTH_URL"), envOr("AERONAUTES_SAIO_USER", "test:tester"), envOr("AERONAUTES_SAIO_KEY", "testing"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(witness.Close)
	before, err := witness.SnapshotContainers(data, segments, aliases)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"staging-dlo", "staging-slo", "captured-dlo", "captured-slo"} {
		object := before.Containers[aliases].Objects[name]
		if object.Size != int64(len(payload)) || object.Digest != fmt.Sprintf("%x", sha256.Sum256([]byte(payload))) || len(object.Segments) != 1 {
			t.Fatalf("alias fixture %s = %+v", name, object)
		}
	}
	for _, live := range []string{data + "/missing-live", "malformed"} {
		report, err := transfercopy.SweepLitter(ctx, h.Session, transfercopy.SweepInput{
			DstContainer: data, StagingPrefix: stage, Captured: []rclone.SegmentPrefix{captured},
			Committing: map[rclone.SegmentPrefix]string{captured: live},
		})
		if err != nil || report.SweptStaged != 0 || report.SweptSegments != 0 || len(report.Notes) == 0 {
			t.Fatalf("recovery = %+v, %v", report, err)
		}
		after, err := witness.SnapshotContainers(data, segments, aliases)
		if err != nil {
			t.Fatal(err)
		}
		if delta := oracle.Diff(before, after); !delta.Empty() {
			t.Fatalf("real Swift recovery changed outside aliases or backing data: %s", delta)
		}
	}
}
