package systemtest

import (
	"context"
	"fmt"
	"testing"

	"github.com/csmart/aeronautes/internal/app"
)

// TestPrefixDeleteSparesEverythingElse is the deletion matrix's core
// safety proof — the owner's "no data is accidentally deleted": a
// prefix delete removes EXACTLY the objects under that prefix and leaves
// every object outside it (including near-miss neighbours whose names
// share a leading substring) byte-for-byte identical.
func TestPrefixDeleteSparesEverythingElse(t *testing.T) {
	h := New(t)
	ctx := context.Background()
	c := h.Container(h.Primary, "del")

	// Doomed objects under "scope/", plus adversarial survivors: a
	// sibling prefix, and a name that SHARES the prefix string but is
	// not under it ("scope-keep" vs "scope/").
	for i := 0; i < 5; i++ {
		h.Seed(h.Primary, c, fmt.Sprintf("scope/doomed-%d", i), []byte("delete me"), nil)
	}
	survivors := map[string][]byte{
		"scope-keep.txt": []byte("prefix-substring, must survive"),
		"other/keep.txt": []byte("sibling prefix, must survive"),
		"top.txt":        []byte("top level, must survive"),
	}
	for name, body := range survivors {
		h.Seed(h.Primary, c, name, body, map[string]string{"X-Object-Meta-Keep": "yes"})
	}

	// Whole-namespace witness so a stray delete into another ns-prefixed
	// container would show, not just the target (grok war-room #2).
	before := h.SnapshotNamespace(h.Primary)

	rec := h.StartDelete(ctx, app.DeleteRequestDTO{
		BookmarkID: h.Primary.BookmarkID, Container: c, Prefix: "scope/",
		Mode: app.ModePrefix, Confirm: c,
	})
	if rec.Err() != nil {
		t.Fatal(rec.Err())
	}
	if job := h.AwaitJob(rec.JobID()); job.State != "complete" {
		t.Fatalf("delete job = %s: %s", job.State, job.Error)
	}

	after := h.SnapshotNamespace(h.Primary)

	// Every doomed object is gone.
	for name := range after.Containers[c].Objects {
		if len(name) >= 6 && name[:6] == "scope/" {
			t.Fatalf("object under the deleted prefix survived: %q", name)
		}
	}
	// Every survivor is byte-for-byte and metadata-for-metadata intact.
	for name := range survivors {
		sa := after.Containers[c].Objects[name]
		sb := before.Containers[c].Objects[name]
		if sa.Digest != sb.Digest || sa.Meta["Keep"] != "yes" {
			t.Fatalf("survivor %q changed: %+v -> %+v", name, sb, sa)
		}
	}
	// The negative-space invariant: the ONLY removals are the five
	// scope/ objects; nothing else on the account moved.
	var allowed []string
	for i := 0; i < 5; i++ {
		allowed = append(allowed, fmt.Sprintf("%s/scope/doomed-%d", c, i))
	}
	h.AssertNoCollateral(before, after, allowed...)
	rec.CreditHappy()
}
