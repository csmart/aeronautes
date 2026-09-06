package systemtest

import (
	"crypto/md5"
	"fmt"
	"reflect"
	"testing"

	"github.com/csmart/aeronautes/internal/app"
)

func TestMirrorRefusesAllWorkRealSwift(t *testing.T) {
	h := New(t)
	ctx := t.Context()
	src := h.Container(h.Primary, "mirsrc")
	dst := h.Container(h.Secondary, "mirdst")
	segments := h.Container(h.Secondary, "mirsegments")
	outside := h.Container(h.Secondary, "miroutside")
	h.Seed(h.Primary, src, "scope/same", []byte("shared"), nil)
	h.Seed(h.Primary, src, "scope/new", []byte("only on source"), nil)
	h.Seed(h.Primary, src, "scope/changed", []byte("updated source"), nil)
	h.Seed(h.Secondary, dst, "scope/same", []byte("shared"), nil)
	h.Seed(h.Secondary, dst, "scope/changed", []byte("concurrent writer"), nil)
	h.Seed(h.Secondary, dst, "scope/dest-only", []byte("preserve me"), nil)
	h.Seed(h.Secondary, outside, "sentinel", []byte("outside scope"), nil)
	data := []byte("retained")
	h.Seed(h.Secondary, segments, ".aeronautes/stage/other/batch/asset/0", data, nil)
	manifest := segments + "/.aeronautes/stage/other/batch/asset/"
	h.Seed(h.Secondary, dst, "scope/dlo", nil, map[string]string{"X-Object-Manifest": manifest})
	h.Seed(h.Secondary, outside, "dlo-alias", nil, map[string]string{"X-Object-Manifest": manifest})
	slo := fmt.Sprintf(`[{"path":%q,"etag":"%x","size_bytes":%d}]`, "/"+manifest+"0", md5.Sum(data), len(data))
	h.SeedSLO(h.Secondary, dst, "scope/slo", slo)
	h.SeedSLO(h.Secondary, outside, "slo-alias", slo)
	h.Seed(h.Secondary, dst, "scope/link", nil, map[string]string{"X-Symlink-Target": outside + "/sentinel"})

	srcBefore := h.SnapshotNamespace(h.Primary)
	dstBefore := h.SnapshotNamespace(h.Secondary)
	for _, name := range []string{"dlo-alias", "slo-alias"} {
		object := dstBefore.Containers[outside].Objects[name]
		if object.Size != int64(len(data)) || object.Digest == "" || len(object.Segments) != 1 {
			t.Fatalf("outside alias %s is not readable: %+v", name, object)
		}
	}
	auditBefore, err := h.Audit.Recent(ctx, 100)
	if err != nil {
		t.Fatal(err)
	}
	request := app.MirrorRequestDTO{CopyRequestDTO: app.CopyRequestDTO{
		SourceBookmarkID: h.Primary.BookmarkID, SourceContainer: src, SourcePrefix: "scope/",
		DestBookmarkID: h.Secondary.BookmarkID, DestContainer: dst, DestPrefix: "scope/",
	}, Confirm: dst}
	var receipt *Receipt
	for range 2 {
		receipt = h.StartMirror(ctx, request)
		if receipt.JobID() != "" {
			h.AwaitJob(receipt.JobID())
		}
		if receipt.JobID() != "" || receipt.Err() == nil || receipt.Err().Error() != "Mirror is unavailable while concurrent changes cannot be protected" {
			t.Fatalf("Mirror was not refused before admission: id=%q, err=%v", receipt.JobID(), receipt.Err())
		}
		h.AssertNoCollateral(srcBefore, h.SnapshotNamespace(h.Primary))
		h.AssertNoCollateral(dstBefore, h.SnapshotNamespace(h.Secondary))
		if rows, err := h.Jobs.List(ctx, "", false, 100); err != nil || len(rows) != 0 {
			t.Fatalf("refused Mirror created jobs: %+v, %v", rows, err)
		}
		if events, err := h.Audit.Recent(ctx, 100); err != nil || !reflect.DeepEqual(events, auditBefore) {
			t.Fatalf("refused Mirror changed audit history: %+v, %v", events, err)
		}
	}
	receipt.CreditRefusal()
}
