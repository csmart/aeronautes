package plan

import (
	"context"
	"errors"
	"fmt"
	"iter"
	"testing"

	"github.com/csmart/aeronautes/internal/swiftcontrol"
)

func stream(entries ...swiftcontrol.ListingEntry) iter.Seq2[swiftcontrol.ListingEntry, error] {
	return func(yield func(swiftcontrol.ListingEntry, error) bool) {
		for _, e := range entries {
			if !yield(e, nil) {
				return
			}
		}
	}
}

func obj(name string, bytes int64, etag string) swiftcontrol.ObjectEntry {
	return swiftcontrol.ObjectEntry{Name: name, Bytes: bytes, ETag: etag}
}

func slo(name string, bytes int64, sloETag string) swiftcontrol.ObjectEntry {
	return swiftcontrol.ObjectEntry{Name: name, Bytes: bytes, ETag: "manifest-md5", SLOListed: true, SLOETag: sloETag}
}

func collect(t *testing.T, src, dst iter.Seq2[swiftcontrol.ListingEntry, error], srcPrefix, dstPrefix string) (map[string]Item, Summary) {
	t.Helper()
	items := map[string]Item{}
	summary, err := Copy(context.Background(), src, dst, srcPrefix, dstPrefix, func(i Item) error {
		items[string(i.Verdict)+":"+i.Name] = i
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return items, summary
}

func TestMergeJoinVerdicts(t *testing.T) {
	src := stream(
		obj("changed-etag.bin", 10, "aaaa"),
		obj("changed-size.bin", 20, "bbbb"),
		obj("new.bin", 5, "cccc"),
		obj("same.bin", 10, "dddd"),
	)
	dst := stream(
		obj("changed-etag.bin", 10, "eeee"),
		obj("changed-size.bin", 25, "ffff"),
		obj("gone.bin", 7, "0123"),
		obj("same.bin", 10, "dddd"),
	)
	items, summary := collect(t, src, dst, "", "")

	for key, verdict := range map[string]Verdict{
		"changed:changed-etag.bin": VerdictChanged,
		"changed:changed-size.bin": VerdictChanged,
		"new:new.bin":              VerdictNew,
		"same:same.bin":            VerdictSame,
		"dest_only:gone.bin":       VerdictDestOnly,
	} {
		if _, ok := items[key]; !ok {
			t.Errorf("missing %s (%s); got %v", key, verdict, keys(items))
		}
	}
	want := Summary{
		New: 1, Same: 1, Changed: 2, DestOnly: 1,
		TransferObjects: 3, TransferBytes: 10 + 20 + 5,
		Overwrites: 2, DestOnlyBytes: 7,
	}
	if summary != want {
		t.Errorf("summary = %+v\nwant      %+v", summary, want)
	}
	if items["same:same.bin"].Route != RouteNone || items["new:new.bin"].Route != RouteRclone {
		t.Error("routes wrong on same/new")
	}
}

func TestSLOComparison(t *testing.T) {
	src := stream(
		slo("differs.bin", 100, "slo-x"),
		slo("matches.bin", 100, "slo-a"),
		slo("resegmented.bin", 100, "slo-b"),
	)
	dst := stream(
		obj("differs.bin", 100, "plainhash"), // dest not SLO-listed
		slo("matches.bin", 100, "slo-a"),
		slo("resegmented.bin", 100, "slo-c"),
	)
	items, summary := collect(t, src, dst, "", "")

	if items["same:matches.bin"].Verdict != VerdictSame {
		t.Error("identical slo_etag + size not Same")
	}
	// Unequal manifests do not prove difference (§28.4): both are
	// Unknown, transferred conservatively, never Changed.
	for _, name := range []string{"differs.bin", "resegmented.bin"} {
		item, ok := items["unknown:"+name]
		if !ok || item.Reason != ReasonLargeObject {
			t.Errorf("%s = %+v, want Unknown/large_object", name, item)
		}
	}
	if summary.Unknown != 2 || summary.Overwrites != 2 || summary.Same != 1 {
		t.Errorf("summary = %+v", summary)
	}
}

func TestZeroByteConservative(t *testing.T) {
	empty := "d41d8cd98f00b204e9800998ecf8427e"
	src := stream(
		obj("marker/", 0, empty),
		obj("maybe-dlo.bin", 0, empty),
	)
	dst := stream(
		obj("marker/", 0, empty),
		obj("maybe-dlo.bin", 3<<30, "bighash"),
	)
	items, _ := collect(t, src, dst, "", "")

	// Even "0 == 0, ETags match" proves nothing: either side may be a
	// DLO manifest. Both pairs resolve at executor HEAD time.
	for _, name := range []string{"marker/", "maybe-dlo.bin"} {
		item, ok := items["unknown:"+name]
		if !ok || item.Reason != ReasonPossibleManifest {
			t.Errorf("%s = %+v, want Unknown/possible_manifest", name, item)
		}
	}
	// A NEW zero-byte row carries the same flag for the executor.
	items2, _ := collect(t, stream(obj("fresh-marker/", 0, empty)), stream(), "", "")
	if item := items2["new:fresh-marker/"]; item.Reason != ReasonPossibleManifest {
		t.Errorf("new zero-byte = %+v", item)
	}
}

func TestRouting(t *testing.T) {
	src := stream(
		obj("/lead", 1, "aa"),
		obj("a b", 1, "bb"),
		obj("a. b.", 1, "b2"),
		obj("a/../b", 1, "c2"),
		obj("a/./b", 1, "c3"),
		obj("a//b", 1, "cc"),
		obj("line\nbreak", 1, "dd"),
		obj("line\rbreak", 1, "dd"),
		obj("trail/", 1, "ee"),
	)
	items, summary := collect(t, src, stream(), "", "")

	// Dot-segment names are unsafe both ways on the pin: silently
	// skipped by --files-from-raw, silently renamed to fullwidth dots by
	// traversal (measured 2026-08-29). Dots inside a segment are fine.
	wantNative := map[string]bool{
		"/lead": true, "a b": false, "a. b.": false, "a//b": true,
		"a/../b": true, "a/./b": true, "line\nbreak": true, "line\rbreak": true, "trail/": true,
	}
	for name, native := range wantNative {
		item := items["new:"+name]
		got := item.Route == RouteNative
		if got != native {
			t.Errorf("%q routed %s", name, item.Route)
		}
		if native && item.Reason != ReasonUnsafeName {
			t.Errorf("%q reason = %s", name, item.Reason)
		}
	}
	if summary.NativeRouted != 7 {
		t.Errorf("NativeRouted = %d", summary.NativeRouted)
	}
}

func TestPrefixInducedUnsafety(t *testing.T) {
	// "photos//x.jpg" under source prefix "photos//" strips to a safe
	// relative name — but the full names are unsafe on both sides.
	src := stream(obj("photos//x.jpg", 1, "aa"))
	items, _ := collect(t, src, stream(), "photos//", "backup//")
	if item := items["new:x.jpg"]; item.Route != RouteNative {
		t.Errorf("prefix-induced unsafety missed: %+v", item)
	}

	// An object named exactly the source prefix (folder marker at the
	// job root) is the "" relative name: legal, native (trailing "/").
	src2 := stream(obj("photos/", 0, "d41d8cd98f00b204e9800998ecf8427e"))
	items2, _ := collect(t, src2, stream(), "photos/", "backup/")
	item, ok := items2["new:"]
	if !ok || item.Route != RouteNative {
		t.Errorf("prefix-self object = %+v (present %v)", item, ok)
	}
}

func TestStreamErrorsPropagate(t *testing.T) {
	boom := errors.New("listing failed mid-page")
	src := func(yield func(swiftcontrol.ListingEntry, error) bool) {
		if !yield(obj("a", 1, "aa"), nil) {
			return
		}
		yield(nil, boom)
	}
	_, err := Copy(context.Background(), src, stream(), "", "", nil)
	if err == nil || !errors.Is(err, boom) {
		t.Errorf("err = %v", err)
	}

	// Out-of-order listings are a paging bug, never silently joined.
	disorder := stream(obj("b", 1, "aa"), obj("a", 1, "bb"))
	if _, err := Copy(context.Background(), disorder, stream(), "", "", nil); err == nil {
		t.Error("out-of-order listing accepted")
	}

	// A delimiter listing (PrefixEntry rows) is a caller bug.
	shallow := stream(swiftcontrol.PrefixEntry{Prefix: "photos/"})
	if _, err := Copy(context.Background(), shallow, stream(), "", "", nil); err == nil {
		t.Error("delimiter listing accepted as deep")
	}

	// A row outside the declared prefix is a caller bug.
	stray := stream(obj("elsewhere/x", 1, "aa"))
	if _, err := Copy(context.Background(), stray, stream(), "photos/", "", nil); err == nil {
		t.Error("out-of-prefix row accepted")
	}
}

func TestEmitAbortAndCancel(t *testing.T) {
	stop := errors.New("enough")
	_, err := Copy(context.Background(), stream(obj("a", 1, "x"), obj("b", 1, "y")), stream(), "", "",
		func(Item) error { return stop })
	if !errors.Is(err, stop) {
		t.Errorf("emit abort = %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Copy(ctx, stream(obj("a", 1, "x")), stream(), "", "", nil); !errors.Is(err, context.Canceled) {
		t.Errorf("cancel = %v", err)
	}
}

func TestQuotedETagNormalized(t *testing.T) {
	src := stream(obj("a", 5, `"abcd"`))
	dst := stream(obj("a", 5, "abcd"))
	items, _ := collect(t, src, dst, "", "")
	if _, ok := items["same:a"]; !ok {
		t.Errorf("quoted etag not normalized: %v", keys(items))
	}
}

func keys(m map[string]Item) []string {
	var out []string
	for k := range m {
		out = append(out, fmt.Sprintf("%q", k))
	}
	return out
}

// An object named exactly the source prefix is the folder's marker. Copied to
// the top of a container its destination name would be empty, the container
// itself, so it is neither planned nor counted; copied into a folder it is
// that folder's marker, as before (DESIGN §65.9 item 9).
func TestFolderMarkerIsNotPlannedAtTheTop(t *testing.T) {
	source := func() iter.Seq2[swiftcontrol.ListingEntry, error] {
		return stream(obj("files/", 0, "d41d8cd98f00b204e9800998ecf8427e"), obj("files/a", 1, "aaaa"))
	}
	items, summary := collect(t, source(), stream(), "files/", "")
	if len(items) != 1 || summary.New != 1 || summary.TransferObjects != 1 {
		t.Fatalf("to the top: items %v, summary %+v; want only a", keys(items), summary)
	}
	if _, ok := items["new:a"]; !ok {
		t.Fatalf("to the top: a missing from %v", keys(items))
	}
	items, _ = collect(t, source(), stream(), "files/", "backup/")
	if len(items) != 2 {
		t.Fatalf("into a folder: items %v, want the marker and a", keys(items))
	}
}
