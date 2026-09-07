package systemtest

import (
	"fmt"
	"testing"

	"github.com/csmart/aeronautes/internal/app"
	"github.com/csmart/aeronautes/internal/jobs"
)

func shortListingLargeCopy(t *testing.T, h *Harness, class string) {
	t.Helper()
	defer h.CloseFind()
	source := h.Container(h.Primary, "paged-large-src")
	segments := h.Container(h.Primary, "paged-large-parts")
	dest := h.Container(h.Secondary, "paged-large-dst")
	plain := map[string][]byte{
		"scope/a": []byte("abc"), "scope/b%2F +日本語": []byte("defg"),
		"scope/c\nnative": []byte("hijkl"), "scope/nested/plain": []byte("mnopqrs"),
		"scope/z-tail": []byte("tuvwxyz!"),
	}
	for name, body := range plain {
		h.Seed(h.Primary, source, name, body, nil)
	}
	h.Seed(h.Primary, segments, "parts/0", []byte("123"), nil)
	h.Seed(h.Primary, segments, "parts/1", []byte("456"), nil)
	switch class {
	case "dlo":
		h.Seed(h.Primary, source, "scope/x-large", nil, map[string]string{"X-Object-Manifest": segments + "/parts/"})
	case "slo":
		h.SeedSLO(h.Primary, source, "scope/x-large", fmt.Sprintf(`[{"path":"/%s/parts/0","size_bytes":3},{"path":"/%s/parts/1","size_bytes":3}]`, segments, segments))
	default:
		t.Fatal("unknown large-object fixture")
	}
	h.Seed(h.Primary, source, "outside", []byte("source sentinel"), nil)
	h.Seed(h.Secondary, dest, "outside", []byte("destination sentinel"), nil)
	beforeA, beforeB := h.SnapshotNamespace(h.Primary), h.SnapshotNamespace(h.Secondary)
	large := beforeA.Containers[source].Objects["scope/x-large"]
	if large.Class != class || large.Size != 6 || len(large.Segments) != 2 {
		t.Fatalf("large-object fixture was not assembled: %+v", large)
	}
	sourceTrace := limitListingPages(t, h, h.Primary, source, "short", "")
	limitListingPages(t, h, h.Secondary, dest, "short", "")
	request := app.CopyRequestDTO{
		SourceBookmarkID: h.Primary.BookmarkID, SourceContainer: source, SourcePrefix: "scope/",
		DestBookmarkID: h.Secondary.BookmarkID, DestContainer: dest, DestPrefix: "scope/",
	}
	receipt := h.StartCopy(t.Context(), request)
	if receipt.Err() != nil {
		t.Fatal(receipt.Err())
	}
	job := h.AwaitJob(receipt.JobID())
	plannedBytes := int64(33)
	if class == "dlo" {
		// Planning still counts the zero-byte DLO listing. P13 owns that
		// estimate; execution must nevertheless deliver all logical bytes.
		plannedBytes = 27
	}
	wantCounters := jobs.Counters{
		ObjectsPlanned: 6, ObjectsCopied: 6, ObjectsVerified: 6,
		BytesPlanned: plannedBytes, BytesCopied: 33, NativeRouted: 1,
	}
	if job.State != "complete" || job.Error != "" || job.Counters != wantCounters {
		t.Fatalf("short pages truncated the mixed large-object selection: %+v", job)
	}
	afterA, afterB := h.SnapshotNamespace(h.Primary), h.SnapshotNamespace(h.Secondary)
	h.AssertNoCollateral(beforeA, afterA)
	allowed := make([]string, 0, 6)
	for _, name := range []string{"scope/a", "scope/b%2F +日本語", "scope/c\nnative", "scope/nested/plain", "scope/x-large", "scope/z-tail"} {
		want := beforeA.Containers[source].Objects[name]
		got, found := afterB.Containers[dest].Objects[name]
		if !found || got.Size != want.Size || got.Digest != want.Digest {
			t.Fatalf("selected %q differs or is absent: got=%+v want=%+v", name, got, want)
		}
		allowed = append(allowed, dest+"/"+name)
	}
	h.AssertNoCollateral(beforeB, afterB, allowed...)
	terminalReads := 0
	for _, call := range sourceTrace.snapshot() {
		if call.prefix == "scope/" && call.delimiter == "" && call.marker == "scope/z-tail" {
			terminalReads++
		}
	}
	if terminalReads < 2 {
		t.Fatalf("source tail reached only %d times across planning and fresh verification", terminalReads)
	}
	receipt.CreditHappy()
}

func TestShortListingDLOCopyMock(t *testing.T) {
	shortListingLargeCopy(t, findMockHarness(t), "dlo")
}

func TestShortListingDLOCopyRealSwift(t *testing.T) {
	shortListingLargeCopy(t, New(t), "dlo")
}

func TestShortListingSLOCopyRealSwift(t *testing.T) {
	shortListingLargeCopy(t, New(t), "slo")
}
