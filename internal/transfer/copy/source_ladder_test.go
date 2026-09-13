package copy

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/csmart/aeronautes/internal/swiftcontrol"
	"github.com/csmart/aeronautes/internal/swifttest"
	"github.com/csmart/aeronautes/internal/transfer/plan"
	"github.com/csmart/aeronautes/internal/transfer/rclone"
)

// windowEngine copies exactly as the fake engine does and then changes the
// source before returning, so the change lands after the engine has read
// the bytes and before the executor HEADs the source at commit. That is
// the window a concurrent writer occupies, reproduced without timing.
type windowEngine struct {
	*fakeEngine
	after func()
}

func (m *windowEngine) Copy(ctx context.Context, src, dst string, names []string, onEvent func(rclone.Event)) (*rclone.Result, error) {
	result, err := m.fakeEngine.Copy(ctx, src, dst, names, onEvent)
	if err == nil && m.after != nil {
		m.after()
	}
	return result, err
}

// seedDynamicSource seeds a dynamic large object source of the given
// segments under media_segments/asset/src/ and returns its assembled body.
func seedDynamicSource(t *testing.T, w *world, segments ...string) string {
	t.Helper()
	w.srcMock.AddContainer("media_segments", "standard")
	for i, segment := range segments {
		seed(w.srcMock, "media_segments", srcSegmentName(i), []byte(segment), nil)
	}
	w.srcMock.PutObject("media", "asset", swifttest.Object{Manifest: "media_segments/asset/src/"})
	body := strings.Join(segments, "")
	src, err := w.src.ObjectDetail(context.Background(), "media", "asset")
	if err != nil {
		t.Fatal(err)
	}
	if src.Class != swiftcontrol.ClassDLO || src.ContentLength != int64(len(body)) {
		t.Fatalf("source = class %v length %d, want a dynamic large object of %d bytes", src.Class, src.ContentLength, len(body))
	}
	if got := readSrc(t, w, "asset"); got != body {
		t.Fatalf("source body = %q, want %q", got, body)
	}
	return body
}

func srcSegmentName(index int) string {
	return "asset/src/" + strings.Repeat("0", 7) + string(rune('0'+index))
}

// srcNames lists every object name in both source namespaces, so a
// refusal can be shown to have left the source exactly as it found it.
func srcNames(t *testing.T, w *world) []string {
	t.Helper()
	var names []string
	for _, container := range []string{"media", "media_segments"} {
		for entry, err := range w.src.Objects(context.Background(), container,
			swiftcontrol.ObjectListOptions{Deep: true}) {
			if err != nil {
				continue
			}
			if object, ok := entry.(swiftcontrol.ObjectEntry); ok {
				names = append(names, container+"/"+object.Name)
			}
		}
	}
	sort.Strings(names)
	return names
}

// runWindow copies "asset" once with the given destination chunking and
// applies the source change, if any, inside the copy window.
func runWindow(t *testing.T, w *world, chunk int64, sourceBytes int64, after func()) *Report {
	t.Helper()
	e := newExecutor(t, w, &windowEngine{
		fakeEngine: &fakeEngine{w: w, chunk: chunk, interruptAfter: -1},
		after:      after,
	})
	report, err := e.Run(context.Background(), []plan.Item{
		{Name: "asset", Verdict: plan.VerdictNew, Route: plan.RouteRclone, SourceBytes: sourceBytes},
	})
	if err != nil {
		t.Fatal(err)
	}
	return report
}

// assertRefusedUnpublished checks the whole refusal shape: nothing live,
// the staged bytes named as retained residue, and the source untouched.
func assertRefusedUnpublished(t *testing.T, w *world, report *Report, srcBefore []string, stage string) {
	t.Helper()
	if report.Copied != 0 || report.SkippedEqual != 0 || report.Overwrites != 0 {
		t.Fatalf("report = %+v, want nothing copied and nothing overwritten", report)
	}
	if len(report.Failures) != 1 || report.Failures[0].Name != "asset" {
		t.Fatalf("failures = %+v, want exactly one for the covered name", report.Failures)
	}
	if stage != "" && report.Failures[0].Stage != stage {
		t.Fatalf("failure stage = %q, want %q (%v)", report.Failures[0].Stage, stage, report.Failures[0].Err)
	}
	if _, err := w.dst.ObjectDetail(context.Background(), "backup", "asset"); err == nil {
		t.Fatal("refused content became live")
	} else if !swiftcontrol.IsNotFound(err) {
		t.Fatalf("HEAD backup/asset: %v", err)
	}
	staged := ".aeronautes/stage/job01/batch/asset"
	var retained bool
	for _, residue := range report.StagedResidue {
		if residue == staged {
			retained = true
		}
	}
	if !retained {
		t.Fatalf("staged residue %v does not name the retained stage", report.StagedResidue)
	}
	if _, err := w.dst.ObjectDetail(context.Background(), "backup", staged); err != nil {
		t.Fatalf("uncertain staged bytes were not retained: %v", err)
	}
	if after := srcNames(t, w); !equalNames(srcBefore, after) {
		t.Fatalf("source namespace changed by the refusal: before=%v after=%v", srcBefore, after)
	}
}

func TestSegmentedSourceSubstitutedInWindowRefused(t *testing.T) {
	// DS-04: a four-byte segment of a dynamic large object source replaced
	// with different bytes of the same length inside the copy window was
	// published as copied, holding the old content under the new source's
	// fingerprint. The staged bytes must never become live.
	w := newWorld(t)
	body := seedDynamicSource(t, w, "GOOD", "DATA")
	report := runWindow(t, w, 4, int64(len(body)), func() {
		seed(w.srcMock, "media_segments", srcSegmentName(0), []byte("EVIL"), nil)
	})
	assertRefusedUnpublished(t, w, report, srcNames(t, w), "stage-content")
}

func TestSegmentedSourcePlainDestinationSubstitutionRefused(t *testing.T) {
	// The same substitution with an unsegmented destination: the check is
	// not confined to the dynamic-large-object branch, which is the
	// large-source to plain-destination case DS-04 named.
	w := newWorld(t)
	body := seedDynamicSource(t, w, "GOOD", "DATA")
	report := runWindow(t, w, 0, int64(len(body)), func() {
		seed(w.srcMock, "media_segments", srcSegmentName(1), []byte("EVIL"), nil)
	})
	assertRefusedUnpublished(t, w, report, srcNames(t, w), "stage-content")
}

func TestSegmentedSourcePermutedInWindowRefused(t *testing.T) {
	// Permutation preserves every length and the total, so only an ordered
	// ladder sees it.
	w := newWorld(t)
	body := seedDynamicSource(t, w, "GOOD", "DATA")
	report := runWindow(t, w, 4, int64(len(body)), func() {
		seed(w.srcMock, "media_segments", srcSegmentName(0), []byte("DATA"), nil)
		seed(w.srcMock, "media_segments", srcSegmentName(1), []byte("GOOD"), nil)
	})
	assertRefusedUnpublished(t, w, report, srcNames(t, w), "stage-content")
}

func TestSegmentedSourceMissingCompensatedByDuplicateRefused(t *testing.T) {
	// One segment removed and another duplicated keeps the summed bytes
	// intact, so the earlier length checks cannot see the change.
	w := newWorld(t)
	body := seedDynamicSource(t, w, "GOOD", "DATA")
	report := runWindow(t, w, 4, int64(len(body)), func() {
		seed(w.srcMock, "media_segments", srcSegmentName(0), []byte("DATA"), nil)
		if err := w.src.DeleteObject(context.Background(), "media_segments", srcSegmentName(1)); err != nil {
			t.Fatal(err)
		}
		seed(w.srcMock, "media_segments", srcSegmentName(2), []byte("DATA"), nil)
	})
	assertRefusedUnpublished(t, w, report, srcNames(t, w), "stage-content")
}

func TestResegmentedDestinationPublishesOnItsContent(t *testing.T) {
	// A correct copy the engine chunked differently must still publish:
	// the destination's own boundaries never enter the comparison.
	w := newWorld(t)
	body := seedDynamicSource(t, w, "GOOD", "DATA")
	before := stagedContentReads(t, w)
	report := runWindow(t, w, 3, int64(len(body)), nil)
	if report.Copied != 1 || len(report.Failures) != 0 {
		t.Fatalf("report = %+v, want the resegmented copy published", report)
	}
	live := headDst(t, w, "asset")
	if live.Class != swiftcontrol.ClassDLO || countSegments(t, w, live) != 3 {
		t.Fatalf("destination = class %v with %d segments, want three", live.Class, countSegments(t, w, live))
	}
	if got := readDst(t, w, "asset"); got != body {
		t.Fatalf("destination body = %q, want %q", got, body)
	}
	if reads := stagedContentReads(t, w) - before; reads != 1 {
		t.Fatalf("resegmented publication performed %d staged content reads, want exactly one", reads)
	}
}

func TestAlignedSegmentationIsStillReadBeforePublication(t *testing.T) {
	// A staged segmentation identical to the source's is not evidence of
	// content. Its listing rows, and the aggregate a manifest HEAD derives
	// from them, are container metadata; the bytes are read anyway.
	w := newWorld(t)
	body := seedDynamicSource(t, w, "GOOD", "DATA")
	before := stagedContentReads(t, w)
	report := runWindow(t, w, 4, int64(len(body)), nil)
	if report.Copied != 1 || len(report.Failures) != 0 {
		t.Fatalf("report = %+v, want the aligned copy published", report)
	}
	if got := readDst(t, w, "asset"); got != body {
		t.Fatalf("destination body = %q, want %q", got, body)
	}
	if reads := stagedContentReads(t, w) - before; reads != 1 {
		t.Fatalf("aligned publication performed %d staged content reads, want exactly one", reads)
	}
}

func TestStaleStagedListingCannotStandInForContent(t *testing.T) {
	// The reviewer's case on db:467. A same-size segment replacement can
	// leave the container row at its previous length and entity tag, and
	// the aggregate a dynamic large object reports is derived from those
	// same rows, so both agree with the source while the stored bytes do
	// not. Only reading the staged content sees it.
	w := newWorld(t)
	body := seedDynamicSource(t, w, "GOOD", "DATA")
	report := runWindow(t, w, 4, int64(len(body)), func() {
		// The engine's staged segment now holds different bytes of the same
		// length while its row still describes the bytes it replaced.
		prefix := stagedSegmentPrefix(t, w)
		w.dstMock.PutObject("backup_segments", prefix+"00000000", swifttest.Object{
			Bytes: 4, ETag: md5hex("GOOD"), Data: []byte("EVIL"),
		})
	})
	assertRefusedUnpublished(t, w, report, srcNames(t, w), "stage-content")
}

// stagedSegmentPrefix finds the generated prefix the engine used for this
// job's staged segments.
func stagedSegmentPrefix(t *testing.T, w *world) string {
	t.Helper()
	staged, err := w.dst.ObjectDetail(context.Background(), "backup", ".aeronautes/stage/job01/batch/asset")
	if err != nil {
		t.Fatalf("HEAD staged asset: %v", err)
	}
	prefix, ok := strings.CutPrefix(staged.Manifest, "backup_segments/")
	if !ok {
		t.Fatalf("staged manifest %q is not in the expected segment container", staged.Manifest)
	}
	return prefix
}

// stagedContentReads counts assembled reads of this job's staged name.
func stagedContentReads(t *testing.T, w *world) int {
	t.Helper()
	var reads int
	for _, request := range w.dstMock.RequestLog() {
		if strings.HasPrefix(request, "GET ") && strings.HasSuffix(request, "/backup/.aeronautes/stage/job01/batch/asset") {
			reads++
		}
	}
	return reads
}

func TestUnusableSourceLadderRefused(t *testing.T) {
	// A ladder that cannot be bound to the source's own HEAD is not
	// evidence, and the covered name refuses rather than publishing on
	// logical length alone.
	t.Run("segment_without_md5", func(t *testing.T) {
		w := newWorld(t)
		w.srcMock.AddContainer("media_segments", "standard")
		w.srcMock.PutObject("media_segments", srcSegmentName(0), swifttest.Object{
			Bytes: 4, ETag: "not-a-digest", Data: []byte("GOOD"),
		})
		seed(w.srcMock, "media_segments", srcSegmentName(1), []byte("DATA"), nil)
		w.srcMock.PutObject("media", "asset", swifttest.Object{Manifest: "media_segments/asset/src/"})
		report := runWindow(t, w, 4, 8, nil)
		assertRefusedUnpublished(t, w, report, srcNames(t, w), "stage-ladder")
	})
	t.Run("listing_does_not_recombine", func(t *testing.T) {
		// The store reports an aggregate the listed rows do not produce,
		// which is what a stale or truncated listing looks like.
		w := newWorld(t)
		pinned := md5.Sum([]byte("a different aggregate"))
		w.srcMock.SegmentAggregateOverride = map[string]string{
			"media/asset": hex.EncodeToString(pinned[:]),
		}
		body := seedDynamicSourceUnchecked(t, w, "GOOD", "DATA")
		report := runWindow(t, w, 4, int64(len(body)), nil)
		assertRefusedUnpublished(t, w, report, srcNames(t, w), "stage-ladder")
	})
	t.Run("static_large_object_with_a_ranged_entry", func(t *testing.T) {
		// A ranged entry records the containing segment's tag rather than
		// the range's own content MD5, so the manifest states no ladder.
		w := newWorld(t)
		seedStaticSource(t, w, `[{"name":"/media_segments/asset/src/00000000","hash":"`+md5hex("GOOD")+
			`","bytes":4,"range":"0-3"},{"name":"/media_segments/asset/src/00000001","hash":"`+md5hex("DATA")+`","bytes":4}]`,
			sloAggregate("GOOD", "DATA"), "GOODDATA")
		report := runWindow(t, w, 4, 8, nil)
		assertRefusedUnpublished(t, w, report, srcNames(t, w), "stage-ladder")
	})
	t.Run("static_large_object_manifest_disagrees_with_its_tag", func(t *testing.T) {
		w := newWorld(t)
		seedStaticSource(t, w, sloManifest("GOOD", "DATA"), md5hex("GOODDATA"), "GOODDATA")
		report := runWindow(t, w, 4, 8, nil)
		assertRefusedUnpublished(t, w, report, srcNames(t, w), "stage-ladder")
	})
}

// sloManifest describes the two owned segments as a manifest.
func sloManifest(left, right string) string {
	return `[{"name":"/media_segments/asset/src/00000000","hash":"` + md5hex(left) + `","bytes":` + strconv.Itoa(len(left)) +
		`},{"name":"/media_segments/asset/src/00000001","hash":"` + md5hex(right) + `","bytes":` + strconv.Itoa(len(right)) + `}]`
}

// sloAggregate is the entity tag a static large object of those two
// segments reports: the MD5 of their concatenated entity tags.
func sloAggregate(left, right string) string {
	sum := md5.Sum([]byte(md5hex(left) + md5hex(right)))
	return hex.EncodeToString(sum[:])
}

// seedStaticSource seeds a static large object whose served content, its
// manifest and its reported entity tag are set independently, so a
// fixture can make any one of them disagree.
func seedStaticSource(t *testing.T, w *world, manifest, etag, content string) {
	t.Helper()
	w.srcMock.AddContainer("media_segments", "standard")
	seed(w.srcMock, "media_segments", srcSegmentName(0), []byte("GOOD"), nil)
	seed(w.srcMock, "media_segments", srcSegmentName(1), []byte("DATA"), nil)
	w.srcMock.PutObject("media", "asset", swifttest.Object{
		Bytes: int64(len(content)), ETag: etag, Data: []byte(content), SLO: true, SLOManifestJSON: manifest,
	})
	src, err := w.src.ObjectDetail(context.Background(), "media", "asset")
	if err != nil {
		t.Fatal(err)
	}
	if src.Class != swiftcontrol.ClassSLO {
		t.Fatalf("source class = %v, want a static large object", src.Class)
	}
}

func TestStaticLargeObjectSourceVerifiedAgainstItsManifest(t *testing.T) {
	// A static large object records a length and MD5 per entry, and their
	// recombination is the entity tag it reports. That manifest is its
	// ladder, and comparing the copy against it answers a stronger
	// question than the dynamic case: whether the bytes still match what
	// the manifest says they are.
	t.Run("consistent_source_publishes", func(t *testing.T) {
		w := newWorld(t)
		seedStaticSource(t, w, sloManifest("GOOD", "DATA"), sloAggregate("GOOD", "DATA"), "GOODDATA")
		report := runWindow(t, w, 4, 8, nil)
		if report.Copied != 1 || len(report.Failures) != 0 {
			t.Fatalf("report = %+v, want the static large object published", report)
		}
		if got := readDst(t, w, "asset"); got != "GOODDATA" {
			t.Fatalf("destination body = %q", got)
		}
	})
	t.Run("content_no_longer_matches_the_manifest", func(t *testing.T) {
		// The served bytes changed while the recorded manifest and the
		// reported entity tag did not, which is exactly the substitution a
		// static large object's own tag cannot show.
		w := newWorld(t)
		seedStaticSource(t, w, sloManifest("GOOD", "DATA"), sloAggregate("GOOD", "DATA"), "EVILDATA")
		report := runWindow(t, w, 4, 8, nil)
		assertRefusedUnpublished(t, w, report, srcNames(t, w), "stage-content")
	})
}

// seedDynamicSourceUnchecked seeds the same source without the identity
// assertions, for fixtures that deliberately break that identity.
func seedDynamicSourceUnchecked(t *testing.T, w *world, segments ...string) string {
	t.Helper()
	w.srcMock.AddContainer("media_segments", "standard")
	for i, segment := range segments {
		seed(w.srcMock, "media_segments", srcSegmentName(i), []byte(segment), nil)
	}
	w.srcMock.PutObject("media", "asset", swifttest.Object{Manifest: "media_segments/asset/src/"})
	return strings.Join(segments, "")
}

func TestSourceLadderCeilingRefusesRatherThanTruncating(t *testing.T) {
	// A source past the ceiling must refuse with that reason, not be
	// compared against an expectation that quietly stops early. The
	// production ceiling is a quarter of a million segments, which no
	// fixture can build, so the ceiling is driven directly.
	w := newWorld(t)
	seedDynamicSource(t, w, "GOOD", "DATA")
	src, err := w.src.ObjectDetail(context.Background(), "media", "asset")
	if err != nil {
		t.Fatal(err)
	}
	e := newExecutor(t, w, nil)
	ladder, err := e.dynamicSourceLadder(context.Background(), src, 1)
	if err == nil {
		t.Fatalf("a source past the ceiling produced a ladder of %d runs", len(ladder))
	}
	if !strings.Contains(err.Error(), "beyond what one content comparison holds") {
		t.Fatalf("refusal does not name the ceiling: %v", err)
	}
	// The same source inside the ceiling still builds, so the refusal is
	// the ceiling and not the fixture.
	if ladder, err := e.dynamicSourceLadder(context.Background(), src, 2); err != nil || len(ladder) != 2 {
		t.Fatalf("ladder inside the ceiling = %d runs, err = %v", len(ladder), err)
	}
}
