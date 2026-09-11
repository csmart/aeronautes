package copy

import (
	"bytes"
	"context"
	"fmt"
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
// bytes of the same length so length and listing checks cannot catch it.
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

func TestWrongStagedBytesRefusedBeforePublication(t *testing.T) {
	w := newWorld(t)
	seed(w.srcMock, "media", "asset", []byte("GOODDATA"), nil)
	e := newExecutor(t, w, corruptStageEngine{inner: &fakeEngine{w: w, chunk: 4, interruptAfter: -1}})
	var observed []Observation
	e.cfg.Recorder = &Recorder{
		Admit: func(Admission) error { return nil },
		Execute: func(_ string, observation Observation) error {
			observed = append(observed, observation)
			return nil
		},
		Verify: func(string, Observation) error { return nil },
	}
	var recovery []string
	e.cfg.OnRecovery = func(kind, _, _, _ string) error {
		recovery = append(recovery, kind)
		return nil
	}
	report, err := e.Run(context.Background(), []plan.Item{
		{Name: "asset", Verdict: plan.VerdictNew, Route: plan.RouteRclone, SourceBytes: 8},
	})
	if err != nil {
		t.Fatal(err)
	}
	if report.Copied != 0 || report.BytesCopied != 0 || len(report.Failures) != 1 {
		t.Fatalf("wrong staged content was not refused: %+v", report)
	}
	want := Observation{Result: ResultFailed, Effect: EffectUnresolved,
		Diagnostic: Diagnostic{Stage: StageStaging, Code: CodeUnverified}}
	if len(observed) != 1 || observed[0] != want {
		t.Fatalf("observations = %+v, want %+v", observed, want)
	}
	if _, err := w.dst.ObjectDetail(context.Background(), "backup", "asset"); !swiftcontrol.IsNotFound(err) {
		t.Fatalf("refused live name exists: %v", err)
	}
	stage := e.StagingPrefix() + "batch/asset"
	if len(report.StagedResidue) != 1 || report.StagedResidue[0] != stage ||
		len(report.CapturedPrefixes) != 1 || len(report.CommittedPrefixes) != 0 {
		t.Fatalf("staging recovery lost: %+v", report)
	}
	if len(recovery) != 1 || recovery[0] != "captured" {
		t.Fatalf("recovery events = %v, want only captured", recovery)
	}
	if body := readDst(t, w, stage); body != "EVILDATA" {
		t.Fatalf("retained staged body = %q", body)
	}
	prefix := report.CapturedPrefixes[0]
	wantNames := []string{"backup/" + stage, prefix.Container + "/" + prefix.Prefix + "00000000", prefix.Container + "/" + prefix.Prefix + "00000001"}
	sort.Strings(wantNames)
	if names := dstNames(t, w); !equalNames(names, wantNames) {
		t.Fatalf("retained namespace = %v, want %v", names, wantNames)
	}
	for _, request := range w.dstMock.RequestLog() {
		if request == "PUT /v1/AUTH_test/backup/asset" || strings.HasPrefix(request, "DELETE ") {
			t.Errorf("refused content caused a write or cleanup: %s", request)
		}
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

// metaInfo builds an /info document advertising exactly the metadata
// limits given; a zero value omits that limit from the advertisement.
func metaInfo(count, overall, nameLength, valueLength int) string {
	fields := []string{`"max_file_size": 5368709122`, `"version": "2.36.0"`,
		`"policies": [{"name": "standard", "default": true, "aliases": "standard"}]`}
	for name, value := range map[string]int{
		"max_meta_count": count, "max_meta_overall_size": overall,
		"max_meta_name_length": nameLength, "max_meta_value_length": valueLength,
	} {
		if value > 0 {
			fields = append(fields, fmt.Sprintf(`%q: %d`, name, value))
		}
	}
	sort.Strings(fields)
	return `{"swift": {` + strings.Join(fields, ", ") + `}, "slo": {"max_manifest_segments": 1000, "min_segment_size": 1}}`
}

// publishWithMeta stages "asset" through a chunking engine with the given
// source metadata and returns the published destination.
func publishWithMeta(t *testing.T, w *world, meta map[string]string) *swiftcontrol.ObjectDetail {
	t.Helper()
	seed(w.srcMock, "media", "asset", []byte("GOODDATA"), meta)
	e := newExecutor(t, w, &fakeEngine{w: w, chunk: 4, interruptAfter: -1})
	report, err := e.Run(context.Background(), []plan.Item{
		{Name: "asset", Verdict: plan.VerdictNew, Route: plan.RouteRclone, SourceBytes: 8},
	})
	if err != nil {
		t.Fatal(err)
	}
	if report.Copied != 1 || len(report.Failures) != 0 {
		t.Fatalf("publication did not succeed: %+v", report)
	}
	return headDst(t, w, "asset")
}

func assertPreserved(t *testing.T, live *swiftcontrol.ObjectDetail, meta map[string]string) {
	t.Helper()
	for key, value := range meta {
		if live.Meta[key] != value {
			t.Fatalf("preserved metadata %q = %q, want %q", key, live.Meta[key], value)
		}
	}
	if live.Meta[fingerprintMetaKey] == "" {
		t.Fatal("the fingerprint was displaced")
	}
}

func TestStampRespectsAdvertisedAllowance(t *testing.T) {
	// The stamp is optional evidence: it rides the publishing write only
	// when the complete added entry fits every limit the destination
	// advertises, and it never displaces a preserved value or breaks the
	// copy. The fixture enforces what it advertises, so a stamp added
	// past a limit would fail the publication outright.
	stampKeyLen := len(stagedAggregateMetaKey)      // 27
	fingerprintKeyLen := len(fingerprintMetaKey)    // 22
	valueLen := len(aggregateStamp(8, md5hex("x"))) // both stamp shapes: "v1:8:" + 32 hex
	cases := []struct {
		name    string
		info    string
		meta    map[string]string
		stamped bool
	}{
		{name: "complete limits with room", info: metaInfo(90, 4096, 128, 256), meta: map[string]string{"Kept": "v"}, stamped: true},
		{name: "entry count exhausted by preserved values", info: metaInfo(3, 4096, 128, 256),
			meta: map[string]string{"A": "1", "B": "2"}, stamped: false},
		{name: "total size leaves no room for the stamp", info: metaInfo(90, fingerprintKeyLen+valueLen+len("Pad")+40+stampKeyLen+valueLen-1, 128, 256),
			meta: map[string]string{"Pad": strings.Repeat("p", 40)}, stamped: false},
		{name: "name limit shorter than the stamp key", info: metaInfo(90, 4096, stampKeyLen-1, 256),
			meta: map[string]string{"Kept": "v"}, stamped: false},
		{name: "count advertised but total size missing", info: metaInfo(90, 0, 128, 256),
			meta: map[string]string{"Kept": "v"}, stamped: false},
		{name: "name and value lengths missing", info: metaInfo(90, 4096, 0, 0),
			meta: map[string]string{"Kept": "v"}, stamped: false},
		{name: "no limits advertised at all", info: `{"swift": {"max_file_size": 5368709122}}`,
			meta: map[string]string{"Kept": "v"}, stamped: false},
		{name: "unusable info document", info: `{"swift": "not-an-object"}`,
			meta: map[string]string{"Kept": "v"}, stamped: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := newWorld(t)
			w.dstMock.InfoJSON = tc.info
			live := publishWithMeta(t, w, tc.meta)
			assertPreserved(t, live, tc.meta)
			if got := live.Meta[stagedAggregateMetaKey] != ""; got != tc.stamped {
				t.Fatalf("stamped = %v, want %v (meta=%v)", got, tc.stamped, live.Meta)
			}
			if body := readDst(t, w, "asset"); body != "GOODDATA" {
				t.Fatalf("published body = %q", body)
			}
			// Unstamped destinations are then refused rather than skipped.
			if report := runUnknownOver(t, w); tc.stamped && report.SkippedEqual != 1 || !tc.stamped && report.OverwritesRefused != 1 {
				t.Fatalf("later decision = %+v for stamped=%v", report, tc.stamped)
			}
		})
	}
}

func TestStampFitsValueLength(t *testing.T) {
	// The value bound cannot be reached through the fixture without also
	// breaking the mandatory fingerprint, whose value has the same shape,
	// so it is held at the predicate: a value longer than the advertised
	// value length never fits, whatever room remains elsewhere.
	headers := map[string]string{"X-Object-Meta-" + fingerprintMetaKey: fingerprint(8, md5hex("x"))}
	value := aggregateStamp(8, md5hex("y"))
	if stampFits(headers, stagedAggregateMetaKey, value, metaBudget{count: 90, overall: 4096, nameLength: 128, valueLength: len(value) - 1, known: true}) {
		t.Fatal("a stamp longer than the advertised value length was accepted")
	}
	if !stampFits(headers, stagedAggregateMetaKey, value, metaBudget{count: 90, overall: 4096, nameLength: 128, valueLength: len(value), known: true}) {
		t.Fatal("a stamp exactly at the advertised value length was refused")
	}
}
