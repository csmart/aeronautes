package systemtest

import (
	"crypto/md5"
	"fmt"
	"strings"
	"testing"

	"github.com/csmart/aeronautes/internal/app"
)

func TestCopyRefusesOverwritesRealSwift(t *testing.T) {
	h := New(t)
	src := h.Container(h.Primary, "refused-src")
	dst := h.Container(h.Secondary, "refused-dst")
	segments := h.Container(h.Secondary, "refused-segments")
	outside := h.Container(h.Secondary, "refused-outside")
	for n := range 205 {
		name := fmt.Sprintf("scope/refused-%03d", n)
		h.Seed(h.Primary, src, name, []byte("source replacement"), nil)
		h.Seed(h.Secondary, dst, name, []byte("retained bytes"), nil)
	}
	retained := []byte("retained")
	h.Seed(h.Secondary, segments, "shared/0", retained, nil)
	slo := fmt.Sprintf(`[{"path":%q,"etag":"%x","size_bytes":%d}]`, "/"+segments+"/shared/0", md5.Sum(retained), len(retained))
	h.Seed(h.Secondary, dst, "scope/dlo", nil, map[string]string{"X-Object-Manifest": segments + "/shared/"})
	h.SeedSLO(h.Secondary, dst, "scope/slo", slo)
	h.Seed(h.Secondary, outside, "dlo-alias", nil, map[string]string{"X-Object-Manifest": segments + "/shared/"})
	h.SeedSLO(h.Secondary, outside, "slo-alias", slo)
	h.Seed(h.Secondary, outside, "sentinel", retained, nil)
	h.Seed(h.Secondary, dst, "scope/link", nil, map[string]string{"X-Symlink-Target": outside + "/sentinel"})
	h.Seed(h.Secondary, dst, "scope/native\nname", retained, nil)
	h.Seed(h.Secondary, dst, "scope/", retained, nil)
	for _, name := range []string{"scope/dlo", "scope/slo", "scope/link", "scope/native\nname", "scope/"} {
		h.Seed(h.Primary, src, name, []byte("source replacement"), nil)
	}
	h.Seed(h.Primary, src, "scope/equal", []byte("same"), nil)
	h.Seed(h.Secondary, dst, "scope/equal", []byte("same"), nil)
	for _, name := range []string{"scope/z-new", "scope/z-native\nnew"} {
		h.Seed(h.Primary, src, name, []byte("eligible"), nil)
	}
	srcBefore, dstBefore := h.SnapshotNamespace(h.Primary), h.SnapshotNamespace(h.Secondary)
	for _, name := range []string{"dlo-alias", "slo-alias"} {
		object := dstBefore.Containers[outside].Objects[name]
		if object.Size != 8 || object.Digest == "" || len(object.Segments) != 1 {
			t.Fatalf("outside alias is not readable: %+v", object)
		}
	}
	receipt := h.StartCopy(t.Context(), app.CopyRequestDTO{
		SourceBookmarkID: h.Primary.BookmarkID, SourceContainer: src, SourcePrefix: "scope/",
		DestBookmarkID: h.Secondary.BookmarkID, DestContainer: dst, DestPrefix: "scope/",
	})
	if receipt.Err() != nil {
		t.Fatal(receipt.Err())
	}
	job := h.AwaitJob(receipt.JobID())
	if job.State != "partial" || job.Counters.OverwritesRefused != 210 || job.Counters.Overwrites != 0 || job.Counters.ObjectsCopied != 2 || job.Counters.BytesCopied != 16 || job.Counters.ObjectsVerified != 3 || job.Counters.ObjectsFailed != 0 {
		t.Fatalf("mixed Copy outcome: %+v", job)
	}
	if !strings.Contains(job.Error, "210 overwrites refused") || !strings.Contains(job.Error, "concurrent changes cannot be protected") {
		t.Fatalf("refusal detail missing: %q", job.Error)
	}
	events, err := h.Audit.Recent(t.Context(), 20)
	if err != nil {
		t.Fatal(err)
	}
	matched := 0
	for _, event := range events {
		if event.Operation == "transfer.copy.partial" && event.Summary == "job "+receipt.JobID()+" · 210 overwrites refused · item outcomes: 0 failed names, 0 conflicts, 210 refusals, 0 unresolved, 0 unverified, 210 with problems" {
			if event.Result != "verify_mismatch" {
				t.Fatalf("partial audit result: %+v", event)
			}
			matched++
		}
	}
	if matched != 1 {
		t.Fatalf("terminal Copy lacks its matching atomic audit: %d events", matched)
	}
	srcAfter, dstAfter := h.SnapshotNamespace(h.Primary), h.SnapshotNamespace(h.Secondary)
	h.AssertNoCollateral(srcBefore, srcAfter)
	h.AssertNoCollateral(dstBefore, dstAfter, dst+"/scope/z-new", dst+"/scope/z-native\nnew")
	for _, name := range []string{"scope/z-new", "scope/z-native\nnew"} {
		want, got := srcBefore.Containers[src].Objects[name], dstAfter.Containers[dst].Objects[name]
		if got.Size != want.Size || got.Digest != want.Digest {
			t.Fatalf("eligible object was not copied faithfully: %s: %+v", name, got)
		}
	}
	receipt.CreditPartial()
}
