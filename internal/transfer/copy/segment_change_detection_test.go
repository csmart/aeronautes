package copy

import (
	"bytes"
	"context"
	"sort"
	"strings"
	"testing"

	"github.com/csmart/aeronautes/internal/swiftcontrol"
	"github.com/csmart/aeronautes/internal/swifttest"
	"github.com/csmart/aeronautes/internal/transfer/plan"
	"github.com/csmart/aeronautes/internal/transfer/rclone"
)

// dstNames lists every object name in both destination namespaces, the
// collateral a refusal must leave exactly as it found it.
func dstNames(t *testing.T, w *world) []string {
	t.Helper()
	var names []string
	for _, container := range []string{"backup", "backup_segments"} {
		for entry, err := range w.dst.Objects(context.Background(), container,
			swiftcontrol.ObjectListOptions{Deep: true}) {
			if err != nil {
				t.Fatalf("list %s: %v", container, err)
			}
			if object, ok := entry.(swiftcontrol.ObjectEntry); ok {
				names = append(names, container+"/"+object.Name)
			}
		}
	}
	sort.Strings(names)
	return names
}

func equalNames(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func readDst(t *testing.T, w *world, name string) string {
	t.Helper()
	body, _, err := w.dst.OpenObject(context.Background(), "backup", name)
	if err != nil {
		t.Fatalf("GET backup/%s: %v", name, err)
	}
	defer body.Close()
	var out bytes.Buffer
	if _, err := out.ReadFrom(body); err != nil {
		t.Fatal(err)
	}
	return out.String()
}

// copyThenHead runs one planned-new engine copy of "asset" and returns
// the report with the committed destination's HEAD.
func copyThenHead(t *testing.T, w *world, chunk int64) (*Report, *swiftcontrol.ObjectDetail) {
	t.Helper()
	e := newExecutor(t, w, &fakeEngine{w: w, chunk: chunk, interruptAfter: -1})
	report, err := e.Run(context.Background(), []plan.Item{
		{Name: "asset", Verdict: plan.VerdictNew, Route: plan.RouteRclone, SourceBytes: 8},
	})
	if err != nil {
		t.Fatal(err)
	}
	if report.Copied != 1 {
		t.Fatalf("initial copy = %+v, want one copied", report)
	}
	return report, headDst(t, w, "asset")
}

// runUnknownOver replays one unknown-verdict item over the same name,
// which is the decision this slice changes.
func runUnknownOver(t *testing.T, w *world) *Report {
	t.Helper()
	report, err := newExecutor(t, w, &fakeEngine{w: w, chunk: 4, interruptAfter: -1}).Run(context.Background(),
		[]plan.Item{{Name: "asset", Verdict: plan.VerdictUnknown, Route: plan.RouteRclone, SourceBytes: 8}})
	if err != nil {
		t.Fatal(err)
	}
	return report
}

func TestSubstitutedDestinationSegmentsRefusedNotSkipped(t *testing.T) {
	// DS-04: a committed destination whose segments are replaced by
	// different bytes of the same length keeps its logical length and
	// this executor's fingerprint, and was therefore skipped as equal.
	// It must now be refused instead. Promoted from the archived audit
	// reproduction with the outcome, collateral and record checks the
	// probe did not make.
	w := newWorld(t)
	seed(w.srcMock, "media", "asset", []byte("GOODDATA"), nil)
	report0, _ := copyThenHead(t, w, 4)
	before := dstNames(t, w)

	// Replace one segment with different bytes of the same length.
	p := report0.CapturedPrefixes[0]
	seed(w.dstMock, p.Container, p.Prefix+"00000000", []byte("EVIL"), nil)

	report := runUnknownOver(t, w)
	if report.SkippedEqual != 0 {
		t.Fatalf("substituted destination skipped as equal: %+v", report)
	}
	if report.OverwritesRefused != 1 || report.Overwrites != 0 || report.Copied != 0 {
		t.Fatalf("report = %+v, want exactly one refused overwrite and no write", report)
	}
	if len(report.Failures) != 0 {
		t.Fatalf("refusal reported as failure: %+v", report.Failures)
	}
	// The refusal must leave every name in both namespaces untouched:
	// refusing is not a licence to delete or rewrite anything.
	if after := dstNames(t, w); !equalNames(before, after) {
		t.Fatalf("destination collateral changed: before=%v after=%v", before, after)
	}
	// The live object still assembles to the substituted bytes: this
	// slice detects the change, it does not repair it.
	body := readDst(t, w, "asset")
	if body != "EVILDATA" {
		t.Fatalf("live body = %q, want the substituted bytes left in place", body)
	}
}

func TestUnstampedDestinationRefusedNotSkipped(t *testing.T) {
	// A non-plain destination carrying no staged aggregate is not
	// evidence of anything, so it is refused rather than skipped.
	w := newWorld(t)
	seed(w.srcMock, "media", "asset", []byte("GOODDATA"), nil)
	_, live := copyThenHead(t, w, 4)
	// Re-publish the same manifest without the staged aggregate.
	w.dstMock.PutObject("backup", "asset", swifttest.Object{
		Manifest: live.Manifest,
		Meta:     map[string]string{"Aeronautes-Fingerprint": live.Meta["Aeronautes-Fingerprint"]},
	})

	report := runUnknownOver(t, w)
	if report.SkippedEqual != 0 || report.OverwritesRefused != 1 {
		t.Fatalf("report = %+v, want the unstamped destination refused", report)
	}
}

func TestStaticLargeObjectDestinationRefusedNotSkipped(t *testing.T) {
	// A static large object reports the entity tag its manifest
	// recorded, so it carries no live aggregate. It is refused rather
	// than reasoned about, whether or not anything changed beneath it.
	w := newWorld(t)
	seed(w.srcMock, "media", "asset", []byte("GOODDATA"), nil)
	src, err := w.src.ObjectDetail(context.Background(), "media", "asset")
	if err != nil {
		t.Fatal(err)
	}
	w.dstMock.AddContainer("backup_segments", "standard")
	seed(w.dstMock, "backup_segments", "asset/gen/00000000", []byte("GOODDATA"), nil)
	w.dstMock.PutObject("backup", "asset", swifttest.Object{
		Bytes: 8, ETag: md5hex("GOODDATA"), SLO: true,
		SLOManifestJSON: `[{"name":"/backup_segments/asset/gen/00000000","bytes":8}]`,
		Meta: map[string]string{
			"Aeronautes-Fingerprint":      fingerprint(src.ContentLength, src.ETag),
			"Aeronautes-Staged-Aggregate": aggregateStamp(8, md5hex("GOODDATA")),
		},
	})
	dst, err := w.dst.ObjectDetail(context.Background(), "backup", "asset")
	if err != nil {
		t.Fatal(err)
	}
	if skippableEqual(src, dst) {
		t.Fatal("a static large object destination passed as proven-equal")
	}
}

func TestUnchangedStampedDestinationStillSkips(t *testing.T) {
	// The permitted-skip set must still contain the case it was there
	// for: an untouched destination this executor published.
	w := newWorld(t)
	seed(w.srcMock, "media", "asset", []byte("GOODDATA"), nil)
	copyThenHead(t, w, 4)
	if report := runUnknownOver(t, w); report.SkippedEqual != 1 || report.OverwritesRefused != 0 {
		t.Fatalf("report = %+v, want the unchanged destination skipped as equal", report)
	}
}

func TestResegmentedStampedDestinationStillSkips(t *testing.T) {
	// The comparison is against this executor's own observation, never
	// against the source's segment structure, so a destination the
	// engine chose to segment differently is unaffected.
	w := newWorld(t)
	seed(w.srcMock, "media", "asset", []byte("GOODDATA"), nil)
	copyThenHead(t, w, 3)
	if report := runUnknownOver(t, w); report.SkippedEqual != 1 || report.OverwritesRefused != 0 {
		t.Fatalf("report = %+v, want the resegmented destination skipped as equal", report)
	}
}

func TestPlainPairDecisionUnchanged(t *testing.T) {
	// The plain rule is untouched: size and entity tag still decide, and
	// no stamp is required.
	w := newWorld(t)
	seed(w.srcMock, "media", "asset", []byte("GOODDATA"), nil)
	seed(w.dstMock, "backup", "asset", []byte("GOODDATA"), nil)
	src, err := w.src.ObjectDetail(context.Background(), "media", "asset")
	if err != nil {
		t.Fatal(err)
	}
	dst, err := w.dst.ObjectDetail(context.Background(), "backup", "asset")
	if err != nil {
		t.Fatal(err)
	}
	if !skippableEqual(src, dst) {
		t.Fatal("identical plain pair no longer proves equal")
	}
	seed(w.dstMock, "backup", "asset", []byte("EVILDATA"), nil)
	dst, _ = w.dst.ObjectDetail(context.Background(), "backup", "asset")
	if skippableEqual(src, dst) {
		t.Fatal("differing plain pair proves equal")
	}
}

// corruptStageEngine replaces the first staged segment with different
// bytes of the same length after the engine reports success, which is
// the staged-verification gap this slice does not close.
type corruptStageEngine struct{ inner *fakeEngine }

func (e corruptStageEngine) Copy(ctx context.Context, src, dst string, names []string, onEvent func(rclone.Event)) (*rclone.Result, error) {
	r, err := e.inner.Copy(ctx, src, dst, names, onEvent)
	if err == nil && len(r.SegmentPrefixes) > 0 {
		p := r.SegmentPrefixes[0]
		seed(e.inner.w.dstMock, p.Container, p.Prefix+"00000000", []byte("EVIL"), nil)
	}
	return r, err
}

func (e corruptStageEngine) CopyTo(ctx context.Context, src, dst string, onEvent func(rclone.Event)) (*rclone.Result, error) {
	return e.inner.CopyTo(ctx, src, dst, onEvent)
}

func TestWrongStagedBytesRemainSkippableGap(t *testing.T) {
	// UNRESOLVED DS-04 reproduction, recorded with its actual outcome.
	// The staged commit path accepts a large object on logical size
	// alone, so bytes that never matched the source can be published.
	// The aggregate then stamps those wrong bytes, and because nothing
	// changes afterwards the destination still skips as equal. Change
	// detection cannot supply the missing first observation; only the
	// staged-content sequence can. This test asserts the gap so it
	// cannot close silently.
	w := newWorld(t)
	seed(w.srcMock, "media", "asset", []byte("GOODDATA"), nil)
	e := newExecutor(t, w, corruptStageEngine{inner: &fakeEngine{w: w, chunk: 4, interruptAfter: -1}})
	report, err := e.Run(context.Background(), []plan.Item{
		{Name: "asset", Verdict: plan.VerdictNew, Route: plan.RouteRclone, SourceBytes: 8},
	})
	if err != nil {
		t.Fatal(err)
	}
	if report.Copied != 1 {
		t.Fatalf("staged corruption no longer commits: %+v — the gap may have closed, update this record", report)
	}
	if body := readDst(t, w, "asset"); body != "EVILDATA" {
		t.Fatalf("live body = %q, want the unverified staged bytes", body)
	}
	if skip := runUnknownOver(t, w); skip.SkippedEqual != 1 {
		t.Fatalf("report = %+v — a wrong-at-stamp destination is expected to still skip; if this changed, the dependency is resolved and this record must be updated", skip)
	}
}

func TestMaterialisedPlainDestinationKeepsItsDecision(t *testing.T) {
	// A large source materialised as a plain destination has no aggregate
	// of its own, so it keeps exactly the decision it had before this
	// guard: the fingerprint and the logical length. Recorded as still
	// open rather than silently tightened, because refusing it would
	// reject copies this executor itself creates.
	w := newWorld(t)
	w.srcMock.AddContainer("media_segments", "standard")
	seed(w.srcMock, "media_segments", "asset/gen/00000000", []byte("GOOD"), nil)
	seed(w.srcMock, "media_segments", "asset/gen/00000001", []byte("DATA"), nil)
	w.srcMock.PutObject("media", "asset", swifttest.Object{Manifest: "media_segments/asset/gen/"})
	src, err := w.src.ObjectDetail(context.Background(), "media", "asset")
	if err != nil {
		t.Fatal(err)
	}
	if src.Class != swiftcontrol.ClassDLO {
		t.Fatalf("source class = %v, want a dynamic large object", src.Class)
	}
	seed(w.dstMock, "backup", "asset", []byte("GOODDATA"),
		map[string]string{"Aeronautes-Fingerprint": fingerprint(src.ContentLength, src.ETag)})
	dst, err := w.dst.ObjectDetail(context.Background(), "backup", "asset")
	if err != nil {
		t.Fatal(err)
	}
	if dst.Class != swiftcontrol.ClassPlain {
		t.Fatalf("destination class = %v, want plain", dst.Class)
	}
	if !skippableEqual(src, dst) {
		t.Fatal("a materialised plain destination lost the decision it had before this guard")
	}
}

func TestMalformedAggregateIsNotEvidence(t *testing.T) {
	// A stamp minted from an unusable observation would otherwise match
	// that same unusable observation and skip, so an observation must
	// look like the store's complete-listing digest before it counts.
	for _, bad := range []string{
		"",
		"not-a-digest",
		"abc",
		"zzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzz",
		"79153ca8aad1663e8c6e18831ea0f4bd79153ca8aad1663e8c6e18831ea0f4bd",
		"79153CA8AAD1663E8C6E18831EA0F4BD",
	} {
		if validAggregate(bad) {
			t.Errorf("observation %q accepted as an aggregate", bad)
		}
	}
	if !validAggregate("79153ca8aad1663e8c6e18831ea0f4bd") {
		t.Error("a well-formed aggregate was rejected")
	}

	// Driven through the store: each observation the mock can actually
	// report, stamped with itself, must still be refused. The empty
	// observation is covered above only, because a manifest read always
	// carries some entity tag.
	for _, bad := range []string{
		"not-a-digest",
		"abc",
		"zzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzz",
		"79153ca8aad1663e8c6e18831ea0f4bd79153ca8aad1663e8c6e18831ea0f4bd",
	} {
		t.Run("aggregate="+bad, func(t *testing.T) {
			w := newWorld(t)
			seed(w.srcMock, "media", "asset", []byte("GOODDATA"), nil)
			src, err := w.src.ObjectDetail(context.Background(), "media", "asset")
			if err != nil {
				t.Fatal(err)
			}
			w.dstMock.AddContainer("backup_segments", "standard")
			seed(w.dstMock, "backup_segments", "asset/gen/00000000", []byte("GOODDATA"), nil)
			w.dstMock.SegmentAggregateOverride = map[string]string{"backup/asset": bad}
			w.dstMock.PutObject("backup", "asset", swifttest.Object{
				Manifest: "backup_segments/asset/gen/",
				Meta: map[string]string{
					"Aeronautes-Fingerprint":      fingerprint(src.ContentLength, src.ETag),
					"Aeronautes-Staged-Aggregate": aggregateStamp(8, bad),
				},
			})
			dst, err := w.dst.ObjectDetail(context.Background(), "backup", "asset")
			if err != nil {
				t.Fatal(err)
			}
			if dst.ETag != bad {
				t.Fatalf("store reported %q, want the pinned observation %q", dst.ETag, bad)
			}
			if skippableEqual(src, dst) {
				t.Fatalf("malformed aggregate %q proved equal", bad)
			}
		})
	}
}

func TestSegmentedSourceCrossSegmentationStillSkips(t *testing.T) {
	// The agreed cross-segmentation control: a genuinely segmented source
	// of two four-byte segments, published to a destination the engine
	// segmented differently. The decision is against the destination's
	// own publication observation, so it is unaffected.
	w := newWorld(t)
	w.srcMock.AddContainer("media_segments", "standard")
	seed(w.srcMock, "media_segments", "asset/src/00000000", []byte("GOOD"), nil)
	seed(w.srcMock, "media_segments", "asset/src/00000001", []byte("DATA"), nil)
	w.srcMock.PutObject("media", "asset", swifttest.Object{Manifest: "media_segments/asset/src/"})
	src, err := w.src.ObjectDetail(context.Background(), "media", "asset")
	if err != nil {
		t.Fatal(err)
	}
	if src.Class != swiftcontrol.ClassDLO || src.ContentLength != 8 {
		t.Fatalf("source = class %v length %d, want a dynamic large object of eight bytes", src.Class, src.ContentLength)
	}
	if body := readSrc(t, w, "asset"); body != "GOODDATA" {
		t.Fatalf("source body = %q", body)
	}

	// Publish it with a three-byte chunking engine: three destination
	// segments against the source's two.
	_, live := copyThenHead(t, w, 3)
	if live.Class != swiftcontrol.ClassDLO {
		t.Fatalf("destination class = %v, want a dynamic large object", live.Class)
	}
	if segments := countSegments(t, w, live); segments != 3 {
		t.Fatalf("destination segments = %d, want the engine's three against the source's two", segments)
	}
	if body := readDst(t, w, "asset"); body != "GOODDATA" {
		t.Fatalf("destination body = %q, want the source bytes", body)
	}
	if live.Meta["Aeronautes-Staged-Aggregate"] == "" {
		t.Fatal("destination carries no publication stamp")
	}
	if report := runUnknownOver(t, w); report.SkippedEqual != 1 || report.OverwritesRefused != 0 {
		t.Fatalf("report = %+v, want the cross-segmented destination skipped as equal", report)
	}
}

func TestStaleAggregateObservationStillSkipsGap(t *testing.T) {
	// UNRESOLVED, recorded with its actual outcome. The store derives the
	// aggregate from a listing of the segment rows, so a listing that has
	// not caught up reports the previous aggregate after the segments
	// changed. This models exactly that boundary with the mock's pinned
	// aggregate, and it is not a reproduction of a real backend incident.
	// The bytes below are independently observed to have changed while
	// the aggregate did not, and the destination still skips: this guard
	// cannot detect it, and no code change here is expected to.
	w := newWorld(t)
	seed(w.srcMock, "media", "asset", []byte("GOODDATA"), nil)
	report0, live := copyThenHead(t, w, 4)
	fresh := live.ETag
	if !validAggregate(fresh) {
		t.Fatalf("published aggregate %q is not usable", fresh)
	}

	// Pin the aggregate to the value observed at publication, then change
	// the bytes beneath it.
	w.dstMock.SegmentAggregateOverride = map[string]string{"backup/asset": fresh}
	p := report0.CapturedPrefixes[0]
	seed(w.dstMock, p.Container, p.Prefix+"00000000", []byte("EVIL"), nil)
	if body := readDst(t, w, "asset"); body != "EVILDATA" {
		t.Fatalf("destination body = %q, want the substituted bytes", body)
	}
	after := headDst(t, w, "asset")
	if after.ETag != fresh {
		t.Fatalf("pinned aggregate = %q, want the stale value %q", after.ETag, fresh)
	}

	if report := runUnknownOver(t, w); report.SkippedEqual != 1 {
		t.Fatalf("report = %+v — a stale aggregate is expected to still skip; if this changed, the boundary moved and this record must be updated", report)
	}
}

func readSrc(t *testing.T, w *world, name string) string {
	t.Helper()
	body, _, err := w.src.OpenObject(context.Background(), "media", name)
	if err != nil {
		t.Fatalf("GET media/%s: %v", name, err)
	}
	defer body.Close()
	var out bytes.Buffer
	if _, err := out.ReadFrom(body); err != nil {
		t.Fatal(err)
	}
	return out.String()
}

func countSegments(t *testing.T, w *world, live *swiftcontrol.ObjectDetail) int {
	t.Helper()
	slash := strings.Index(live.Manifest, "/")
	if slash <= 0 {
		t.Fatalf("manifest %q is malformed", live.Manifest)
	}
	count := 0
	for entry, err := range w.dst.Objects(context.Background(), live.Manifest[:slash],
		swiftcontrol.ObjectListOptions{Deep: true, Prefix: live.Manifest[slash+1:]}) {
		if err != nil {
			t.Fatal(err)
		}
		if _, ok := entry.(swiftcontrol.ObjectEntry); ok {
			count++
		}
	}
	return count
}

func TestUnusableStagedObservationMintsNoStamp(t *testing.T) {
	// The staged observation is what the publishing write carries, so an
	// unusable one must mint no stamp at all. Publishing anyway and
	// leaving the destination unstamped is the safe outcome: the next
	// decision refuses it instead of trusting a value that means nothing.
	w := newWorld(t)
	seed(w.srcMock, "media", "asset", []byte("GOODDATA"), nil)
	// The staged manifest reports an unusable aggregate at the moment the
	// commit path reads it.
	w.dstMock.SegmentAggregateOverride = map[string]string{
		"backup/.aeronautes/stage/job01/batch/asset": "not-a-digest",
	}
	report, live := copyThenHead(t, w, 4)
	if report.Copied != 1 {
		t.Fatalf("publication did not proceed: %+v", report)
	}
	if stamp := live.Meta["Aeronautes-Staged-Aggregate"]; stamp != "" {
		t.Fatalf("an unusable staged observation minted the stamp %q", stamp)
	}
	if body := readDst(t, w, "asset"); body != "GOODDATA" {
		t.Fatalf("destination body = %q, want the source bytes", body)
	}
	// Unstamped, so the next decision refuses rather than skips.
	if skip := runUnknownOver(t, w); skip.SkippedEqual != 0 || skip.OverwritesRefused != 1 {
		t.Fatalf("report = %+v, want the unstamped destination refused", skip)
	}
}
