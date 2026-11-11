package copy

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"errors"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/csmart/aeronautes/internal/swifttest"
	"github.com/csmart/aeronautes/internal/transfer/plan"
)

// overwriteRecorder stands in for the job ledger the app wires behind these
// hooks: it answers what was acknowledged, and captures what the route recorded
// about a replacement or a refusal, so a test can assert on both without a real
// store. Admit/Execute/Verify are no-ops because the overwrite contract these
// tests cover is carried entirely by the three overwrite hooks.
type overwriteRecorder struct {
	baselines map[string]ShownObject
	replaced  map[string]ObservedPredecessor
	refused   map[string][2]ShownObject
	executed  map[string]Observation
	// versioning answers ADR 0102 decision 2's question; nil means the
	// container still reports what the question was composed from.
	versioning func(context.Context) (string, error)
}

func newOverwriteRecorder() *overwriteRecorder {
	return &overwriteRecorder{
		baselines: map[string]ShownObject{},
		replaced:  map[string]ObservedPredecessor{},
		refused:   map[string][2]ShownObject{},
		executed:  map[string]Observation{},
	}
}

// acknowledge captures the baseline the way the app does: a destination HEAD
// read through shownFrom before the run, stored against the name. A name with
// no baseline reads as never acknowledged.
func (r *overwriteRecorder) acknowledge(t *testing.T, w *world, name string) {
	t.Helper()
	shown, ok := shownFrom(headDst(t, w, name))
	if !ok {
		t.Fatalf("destination %q is not identifiable to acknowledge", name)
	}
	r.baselines[name] = shown
}

func (r *overwriteRecorder) recorder() *Recorder {
	return &Recorder{
		Admit:   func(Admission) error { return nil },
		Execute: func(name string, o Observation) error { r.executed[name] = o; return nil },
		Verify:  func(string, Observation) error { return nil },
		Acknowledged: func(name string) (ShownObject, bool, error) {
			shown, ok := r.baselines[name]
			return shown, ok, nil
		},
		Replacing: func(name string, observed ObservedPredecessor) error {
			r.replaced[name] = observed
			return nil
		},
		Refused: func(name string, acknowledged, found ShownObject) error {
			r.refused[name] = [2]ShownObject{acknowledged, found}
			return nil
		},
		VersioningUnchanged: func(ctx context.Context, _ string) (string, error) {
			if r.versioning != nil {
				return r.versioning(ctx)
			}
			return "", nil
		},
	}
}

func etagOf(data []byte) string {
	sum := md5.Sum(data)
	return hex.EncodeToString(sum[:])
}

// A destination whose fresh reading still matches what the operator acknowledged
// is replaced through the staged route, and the predecessor is recorded before
// the publication that destroys it.
func TestAcknowledgedStagedOverwriteReplacesMatch(t *testing.T) {
	w := newWorld(t)
	source := []byte("NEW SOURCE CONTENT")
	seed(w.srcMock, "media", "doc", source, nil)
	seed(w.dstMock, "backup", "doc", []byte("OLD"), nil)

	rec := newOverwriteRecorder()
	rec.acknowledge(t, w, "doc")

	e := recordingExecutor(t, w, &fakeEngine{w: w, interruptAfter: -1}, rec.recorder())
	report, err := e.Run(context.Background(), []plan.Item{
		{Name: "doc", Verdict: plan.VerdictChanged, Route: plan.RouteRclone, SourceBytes: int64(len(source))},
	})
	if err != nil {
		t.Fatal(err)
	}
	if report.Copied != 1 || report.Overwrites != 1 || report.OverwritesRefused != 0 || len(report.Failures) != 0 || len(report.Conflicts) != 0 {
		t.Fatalf("acknowledged overwrite did not replace cleanly: %+v", report)
	}
	if d := headDst(t, w, "doc"); d.ContentLength != int64(len(source)) || d.ETag != etagOf(source) {
		t.Errorf("live name did not take the replacement: len=%d etag=%s", d.ContentLength, d.ETag)
	}
	if predecessor, ok := rec.replaced["doc"]; !ok || predecessor.Length != 3 || predecessor.ETag != etagOf([]byte("OLD")) {
		t.Errorf("predecessor was not recorded before the publication: %+v ok=%t", rec.replaced["doc"], ok)
	}
	if _, ok := rec.refused["doc"]; ok {
		t.Error("a matching overwrite recorded a refusal")
	}
}

// A destination that changed between the acknowledgement and the fixed point is
// refused, with both identities recorded and the racer's content left intact.
func TestAcknowledgedStagedOverwriteRefusesChanged(t *testing.T) {
	w := newWorld(t)
	source := []byte("NEW SOURCE CONTENT")
	seed(w.srcMock, "media", "doc", source, nil)
	seed(w.dstMock, "backup", "doc", []byte("OLD"), nil)

	rec := newOverwriteRecorder()
	rec.acknowledge(t, w, "doc") // baseline: a 3-byte object

	// A racing writer replaces the destination after the acknowledgement.
	racer := []byte("CHANGED BY SOMEONE ELSE")
	seed(w.dstMock, "backup", "doc", racer, nil)

	e := recordingExecutor(t, w, &fakeEngine{w: w, interruptAfter: -1}, rec.recorder())
	report, err := e.Run(context.Background(), []plan.Item{
		{Name: "doc", Verdict: plan.VerdictChanged, Route: plan.RouteRclone, SourceBytes: int64(len(source))},
	})
	if err != nil {
		t.Fatal(err)
	}
	if report.OverwritesRefused != 1 || report.Copied != 0 || len(report.Failures) != 0 {
		t.Fatalf("changed destination was not refused at the fixed point: %+v", report)
	}
	if d := headDst(t, w, "doc"); d.ContentLength != int64(len(racer)) || d.ETag != etagOf(racer) {
		t.Errorf("refused overwrite clobbered the changed destination: len=%d etag=%s", d.ContentLength, d.ETag)
	}
	pair, ok := rec.refused["doc"]
	if !ok {
		t.Fatal("the fixed-point refusal recorded no identities")
	}
	if pair[0].Length != 3 || pair[0].ETag != etagOf([]byte("OLD")) {
		t.Errorf("refusal did not record what was acknowledged: %+v", pair[0])
	}
	if pair[1].Length != int64(len(racer)) || pair[1].ETag != etagOf(racer) {
		t.Errorf("refusal did not record what was found: %+v", pair[1])
	}
	if _, ok := rec.replaced["doc"]; ok {
		t.Error("a refused overwrite recorded a replacement")
	}
}

// An unsafe-named object copies through the native route. A destination whose
// fresh reading still matches what the operator acknowledged is replaced by a
// single unconditional PUT, and the predecessor is recorded before it.
func TestAcknowledgedNativeOverwriteReplacesMatch(t *testing.T) {
	w := newWorld(t)
	name := "line\nname"
	source := []byte("NEW NATIVE CONTENT")
	seed(w.srcMock, "media", name, source, nil)
	seed(w.dstMock, "backup", name, []byte("OLD"), nil)

	rec := newOverwriteRecorder()
	rec.acknowledge(t, w, name)

	e := recordingExecutor(t, w, &fakeEngine{w: w, interruptAfter: -1}, rec.recorder())
	report, err := e.Run(context.Background(), []plan.Item{
		{Name: name, Verdict: plan.VerdictChanged, Route: plan.RouteNative},
	})
	if err != nil {
		t.Fatal(err)
	}
	if report.Copied != 1 || report.Overwrites != 1 || report.OverwritesRefused != 0 || len(report.Failures) != 0 || len(report.Conflicts) != 0 {
		t.Fatalf("acknowledged native overwrite did not replace cleanly: %+v", report)
	}
	if d := headDst(t, w, name); d.ContentLength != int64(len(source)) || d.ETag != etagOf(source) {
		t.Errorf("live name did not take the replacement: len=%d etag=%s", d.ContentLength, d.ETag)
	}
	if predecessor, ok := rec.replaced[name]; !ok || predecessor.Length != 3 || predecessor.ETag != etagOf([]byte("OLD")) {
		t.Errorf("predecessor was not recorded before the publication: %+v ok=%t", rec.replaced[name], ok)
	}
	if _, ok := rec.refused[name]; ok {
		t.Error("a matching native overwrite recorded a refusal")
	}
}

// A native destination that changed between the acknowledgement and the fixed
// point is refused, both identities recorded, and the racer's content intact.
func TestAcknowledgedNativeOverwriteRefusesChanged(t *testing.T) {
	w := newWorld(t)
	name := "line\nname"
	seed(w.srcMock, "media", name, []byte("NEW NATIVE CONTENT"), nil)
	seed(w.dstMock, "backup", name, []byte("OLD"), nil)

	rec := newOverwriteRecorder()
	rec.acknowledge(t, w, name) // baseline: a 3-byte object

	racer := []byte("CHANGED BY SOMEONE ELSE")
	seed(w.dstMock, "backup", name, racer, nil)

	e := recordingExecutor(t, w, &fakeEngine{w: w, interruptAfter: -1}, rec.recorder())
	report, err := e.Run(context.Background(), []plan.Item{
		{Name: name, Verdict: plan.VerdictChanged, Route: plan.RouteNative},
	})
	if err != nil {
		t.Fatal(err)
	}
	if report.OverwritesRefused != 1 || report.Copied != 0 || report.Overwrites != 0 || len(report.Failures) != 0 {
		t.Fatalf("changed native destination was not refused at the fixed point: %+v", report)
	}
	if d := headDst(t, w, name); d.ContentLength != int64(len(racer)) || d.ETag != etagOf(racer) {
		t.Errorf("refused native overwrite clobbered the changed destination: len=%d etag=%s", d.ContentLength, d.ETag)
	}
	pair, ok := rec.refused[name]
	if !ok {
		t.Fatal("the fixed-point refusal recorded no identities")
	}
	if pair[0].Length != 3 || pair[0].ETag != etagOf([]byte("OLD")) {
		t.Errorf("refusal did not record what was acknowledged: %+v", pair[0])
	}
	if pair[1].Length != int64(len(racer)) || pair[1].ETag != etagOf(racer) {
		t.Errorf("refusal did not record what was found: %+v", pair[1])
	}
	if _, ok := rec.replaced[name]; ok {
		t.Error("a refused native overwrite recorded a replacement")
	}
}

// A destination the operator acknowledged but that is gone by the fixed point
// is a free name again: the native route falls through to its create-only
// publication, so it copies as a first write and counts no overwrite.
func TestAcknowledgedNativeOverwriteVanishedFallsThroughToCreate(t *testing.T) {
	w := newWorld(t)
	name := "line\nname"
	source := []byte("NEW NATIVE CONTENT")
	seed(w.srcMock, "media", name, source, nil)
	seed(w.dstMock, "backup", name, []byte("OLD"), nil)

	rec := newOverwriteRecorder()
	rec.acknowledge(t, w, name)
	w.dstMock.RemoveObject("backup", name) // the acknowledged destination is deleted before the run

	e := recordingExecutor(t, w, &fakeEngine{w: w, interruptAfter: -1}, rec.recorder())
	report, err := e.Run(context.Background(), []plan.Item{
		{Name: name, Verdict: plan.VerdictChanged, Route: plan.RouteNative},
	})
	if err != nil {
		t.Fatal(err)
	}
	if report.Copied != 1 || report.Overwrites != 0 || report.OverwritesRefused != 0 || len(report.Failures) != 0 || len(report.Conflicts) != 0 {
		t.Fatalf("vanished acknowledged destination was not copied as a first write: %+v", report)
	}
	if d := headDst(t, w, name); d.ContentLength != int64(len(source)) || d.ETag != etagOf(source) {
		t.Errorf("free name did not take the copy: len=%d etag=%s", d.ContentLength, d.ETag)
	}
	observed, ok := rec.replaced[name]
	if !ok {
		t.Fatal("the vanished destination's absence was not recorded")
	}
	if observed.Present {
		t.Errorf("absence recorded as present: %+v", observed)
	}
}

// A name the operator did not acknowledge keeps the native route's create-only
// publication: a destination that appeared since planning is a conflict, never
// a clobber, so the unconditional replace is reached only through an
// acknowledgement.
func TestNativeNewNameStaysCreateOnly(t *testing.T) {
	w := newWorld(t)
	name := "line\nname"
	racer := []byte("RACER APPEARED")
	seed(w.srcMock, "media", name, []byte("NEW"), nil)
	seed(w.dstMock, "backup", name, racer, nil)

	rec := newOverwriteRecorder() // no baseline: this name was never acknowledged

	e := recordingExecutor(t, w, &fakeEngine{w: w, interruptAfter: -1}, rec.recorder())
	report, err := e.Run(context.Background(), []plan.Item{
		{Name: name, Verdict: plan.VerdictNew, Route: plan.RouteNative},
	})
	if err != nil {
		t.Fatal(err)
	}
	if report.Copied != 0 || report.Overwrites != 0 || len(report.Conflicts) != 1 {
		t.Fatalf("unacknowledged native copy did not conflict on the create-only condition: %+v", report)
	}
	if d := headDst(t, w, name); d.ContentLength != int64(len(racer)) || d.ETag != etagOf(racer) {
		t.Errorf("create-only native copy clobbered the racer: len=%d etag=%s", d.ContentLength, d.ETag)
	}
}

// A hook that answers but reports the name was never acknowledged refuses the
// overwrite before anything is staged — the state a build with the surface
// wired but this name not accepted stays in.
func TestOverwriteRefusesWhenHookReportsNotAcknowledged(t *testing.T) {
	w := newWorld(t)
	seed(w.srcMock, "media", "doc", []byte("NEW"), nil)
	seed(w.dstMock, "backup", "doc", []byte("OLD"), nil)

	rec := newOverwriteRecorder() // no baseline recorded for "doc"
	engine := &recordingEngine{inner: &fakeEngine{w: w, interruptAfter: -1}}
	e := recordingExecutor(t, w, engine, rec.recorder())
	report, err := e.Run(context.Background(), []plan.Item{
		{Name: "doc", Verdict: plan.VerdictChanged, Route: plan.RouteRclone},
	})
	if err != nil {
		t.Fatal(err)
	}
	if report.OverwritesRefused != 1 || report.Copied != 0 || len(report.StagedResidue) != 0 {
		t.Fatalf("unacknowledged overwrite was not refused before staging: %+v", report)
	}
	if len(engine.dsts) != 0 {
		t.Errorf("unacknowledged overwrite staged data: %v", engine.dsts)
	}
	if d := headDst(t, w, "doc"); d.ContentLength != 3 {
		t.Errorf("unacknowledged overwrite altered the destination")
	}
}

// A replacement that matched at the fixed point but has nowhere to record what
// it is about to destroy is refused rather than published unrecorded.
func TestAcknowledgedOverwriteRefusesWithoutReplacingHook(t *testing.T) {
	w := newWorld(t)
	source := []byte("NEW SOURCE CONTENT")
	seed(w.srcMock, "media", "doc", source, nil)
	seed(w.dstMock, "backup", "doc", []byte("OLD"), nil)

	rec := newOverwriteRecorder()
	rec.acknowledge(t, w, "doc")
	recorder := rec.recorder()
	recorder.Replacing = nil // matched, but no way to record the predecessor

	e := recordingExecutor(t, w, &fakeEngine{w: w, interruptAfter: -1}, recorder)
	report, err := e.Run(context.Background(), []plan.Item{
		{Name: "doc", Verdict: plan.VerdictChanged, Route: plan.RouteRclone, SourceBytes: int64(len(source))},
	})
	if err == nil {
		t.Fatal("overwrite published without recording what it replaced")
	}
	if report.Copied != 0 {
		t.Fatalf("overwrite copied despite having nowhere to record the predecessor: %+v", report)
	}
	if d := headDst(t, w, "doc"); d.ContentLength != 3 || d.ETag != etagOf([]byte("OLD")) {
		t.Errorf("destination changed despite the recording failure: len=%d", d.ContentLength)
	}
}

// ADR 0087 decision 6: a replaced manifest's segments live on the destination
// account the operator was not shown, so a copy retains them rather than
// reclaiming them the way an upload does.
func TestAcknowledgedOverwriteRetainsReplacedSegments(t *testing.T) {
	w := newWorld(t)
	w.dstMock.AddContainer("backup_segments", "standard")
	segment := []byte("old-dlo-segment")
	w.dstMock.PutObject("backup_segments", "doc/0001", swifttest.Object{
		Bytes: int64(len(segment)), ETag: etagOf(segment), Data: segment,
	})
	w.dstMock.PutObject("backup", "doc", swifttest.Object{
		Manifest: "backup_segments/doc/", ContentType: "application/octet-stream",
	})
	source := []byte("REPLACEMENT")
	seed(w.srcMock, "media", "doc", source, nil)

	rec := newOverwriteRecorder()
	rec.acknowledge(t, w, "doc")

	e := recordingExecutor(t, w, &fakeEngine{w: w, interruptAfter: -1}, rec.recorder())
	report, err := e.Run(context.Background(), []plan.Item{
		{Name: "doc", Verdict: plan.VerdictUnknown, Route: plan.RouteRclone, SourceBytes: int64(len(source))},
	})
	if err != nil || report.Copied != 1 || report.Overwrites != 1 || report.OverwritesRefused != 0 {
		t.Fatalf("DLO overwrite did not replace: %+v, %v", report, err)
	}
	if _, err := w.dst.ObjectDetail(context.Background(), "backup_segments", "doc/0001"); err != nil {
		t.Errorf("overwrite reclaimed the replaced object's segments instead of retaining them: %v", err)
	}
	if d := headDst(t, w, "doc"); d.ContentLength != int64(len(source)) || d.ETag != etagOf(source) {
		t.Errorf("live name did not take the replacement: len=%d etag=%s", d.ContentLength, d.ETag)
	}
}

// An acknowledged destination that has vanished by the fixed point is a free
// name again and is created, but its observed absence is recorded first, so the
// trail is not a silent create (db:937 finding 3).
func TestAcknowledgedOverwriteRecordsVanishedAbsence(t *testing.T) {
	w := newWorld(t)
	source := []byte("NEW SOURCE CONTENT")
	seed(w.srcMock, "media", "doc", source, nil)
	seed(w.dstMock, "backup", "doc", []byte("OLD"), nil)

	rec := newOverwriteRecorder()
	rec.acknowledge(t, w, "doc") // baseline captured while it was live

	// The destination goes away after the acknowledgement, before the commit.
	w.dstMock.RemoveObject("backup", "doc")

	e := recordingExecutor(t, w, &fakeEngine{w: w, interruptAfter: -1}, rec.recorder())
	report, err := e.Run(context.Background(), []plan.Item{
		{Name: "doc", Verdict: plan.VerdictChanged, Route: plan.RouteRclone, SourceBytes: int64(len(source))},
	})
	if err != nil {
		t.Fatal(err)
	}
	if report.Copied != 1 || report.Overwrites != 0 || report.OverwritesRefused != 0 {
		t.Fatalf("vanished acknowledged destination was not created cleanly: %+v", report)
	}
	observed, ok := rec.replaced["doc"]
	if !ok {
		t.Fatal("the vanished destination's absence was not recorded")
	}
	if observed.Present {
		t.Errorf("absence recorded as present: %+v", observed)
	}
	if d := headDst(t, w, "doc"); d.ContentLength != int64(len(source)) || d.ETag != etagOf(source) {
		t.Errorf("create did not land the source content: len=%d", d.ContentLength)
	}
}

// Each publication shape prepares before the fixed-point comparison, so a racer
// that lands DURING that preparation is still caught by the comparison and the
// overwrite refuses rather than clobbering it (db:937 finding 1). Without the
// ordering the comparison runs first, the preparation then opens a window, and
// the racer is overwritten unrefused.
func TestStagedOverwritePreparesBeforeTheFixedPoint(t *testing.T) {
	racer := []byte("A RACER WROTE THIS LATER ON")
	var once sync.Once
	var w *world
	w = newWorldWrappingDest(t, func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
			// The staged reader open is the plain shape's only preparation
			// request; replace the live destination as it is served.
			if r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/batch/doc") {
				once.Do(func() {
					sum := md5.Sum(racer)
					w.dstMock.PutObject("backup", "doc", swifttest.Object{
						Bytes: int64(len(racer)), ETag: hex.EncodeToString(sum[:]),
						Data: racer, ContentType: "application/octet-stream",
					})
				})
			}
			next.ServeHTTP(rw, r)
		})
	})
	source := []byte("NEW SOURCE CONTENT")
	seed(w.srcMock, "media", "doc", source, nil)
	seed(w.dstMock, "backup", "doc", []byte("OLD"), nil)

	rec := newOverwriteRecorder()
	rec.acknowledge(t, w, "doc") // baseline: the 3-byte OLD object

	e := recordingExecutor(t, w, &fakeEngine{w: w, interruptAfter: -1}, rec.recorder())
	report, err := e.Run(context.Background(), []plan.Item{
		{Name: "doc", Verdict: plan.VerdictChanged, Route: plan.RouteRclone, SourceBytes: int64(len(source))},
	})
	if err != nil {
		t.Fatal(err)
	}
	if report.OverwritesRefused != 1 || report.Overwrites != 0 || report.Copied != 0 {
		t.Fatalf("a racer during preparation was not caught by the comparison: %+v", report)
	}
	if d := headDst(t, w, "doc"); d.ContentLength != int64(len(racer)) || d.ETag != etagOf(racer) {
		t.Errorf("preparation-window overwrite clobbered the racer: len=%d etag=%s", d.ContentLength, d.ETag)
	}
}

func TestAcknowledgedSymlinkOverwriteReplacesMatch(t *testing.T) {
	w := newWorld(t)
	name := "linkobj"
	seed(w.srcMock, "media", "target", []byte("target bytes"), nil)
	w.srcMock.PutObject("media", name, swifttest.Object{
		ContentType: "application/octet-stream", SymlinkTarget: "media/target",
	})
	seed(w.dstMock, "backup", name, []byte("OLD"), nil)

	rec := newOverwriteRecorder()
	rec.acknowledge(t, w, name)

	e := recordingExecutor(t, w, &fakeEngine{w: w, interruptAfter: -1}, rec.recorder())
	report, err := e.Run(context.Background(), []plan.Item{
		{Name: name, Verdict: plan.VerdictChanged, Route: plan.RouteNative},
	})
	if err != nil {
		t.Fatal(err)
	}
	if report.Copied != 1 || report.Overwrites != 1 || report.OverwritesRefused != 0 || len(report.Failures) != 0 || len(report.Conflicts) != 0 {
		t.Fatalf("acknowledged symlink overwrite did not replace cleanly: %+v", report)
	}
	link, err := w.dst.SymlinkGet(context.Background(), "backup", name)
	if err != nil || link.SymlinkTarget != "media/target" {
		t.Fatalf("destination is not the replacement symlink: %+v %v", link, err)
	}
	if predecessor, ok := rec.replaced[name]; !ok || predecessor.Length != 3 || predecessor.ETag != etagOf([]byte("OLD")) {
		t.Errorf("predecessor was not recorded before the symlink replaced it: %+v ok=%t", rec.replaced[name], ok)
	}
}

func TestAcknowledgedSymlinkOverwriteRefusesChanged(t *testing.T) {
	w := newWorld(t)
	name := "linkobj"
	seed(w.srcMock, "media", "target", []byte("target bytes"), nil)
	w.srcMock.PutObject("media", name, swifttest.Object{
		ContentType: "application/octet-stream", SymlinkTarget: "media/target",
	})
	seed(w.dstMock, "backup", name, []byte("OLD"), nil)

	rec := newOverwriteRecorder()
	rec.acknowledge(t, w, name) // baseline: plain 3-byte object

	racer := []byte("CHANGED")
	seed(w.dstMock, "backup", name, racer, nil)

	e := recordingExecutor(t, w, &fakeEngine{w: w, interruptAfter: -1}, rec.recorder())
	report, err := e.Run(context.Background(), []plan.Item{
		{Name: name, Verdict: plan.VerdictChanged, Route: plan.RouteNative},
	})
	if err != nil {
		t.Fatal(err)
	}
	if report.OverwritesRefused != 1 || report.Copied != 0 || report.Overwrites != 0 || len(report.Failures) != 0 {
		t.Fatalf("changed destination under a symlink copy was not refused: %+v", report)
	}
	if d := headDst(t, w, name); d.ContentLength != int64(len(racer)) || d.ETag != etagOf(racer) {
		t.Errorf("refused symlink overwrite clobbered the racer: len=%d etag=%s", d.ContentLength, d.ETag)
	}
	pair, ok := rec.refused[name]
	if !ok || pair[0].ETag != etagOf([]byte("OLD")) || pair[1].ETag != etagOf(racer) {
		t.Errorf("symlink refusal did not record both identities: %+v ok=%t", rec.refused[name], ok)
	}
	if _, ok := rec.replaced[name]; ok {
		t.Error("a refused symlink overwrite recorded a replacement")
	}
}

// ADR 0102 decision 2: a destination container that no longer reports the
// versioning statement the replacement was authorised under refuses the
// replacement at the fixed point on every publishing route, with the live
// object untouched and no replacement recorded. No identity refusal is
// recorded either: the identities still match, and the two statements are the
// app's hook to record.
func TestAcknowledgedOverwriteRefusesAChangedVersioningStatement(t *testing.T) {
	for _, c := range []struct {
		name  string
		route plan.Route
		link  bool
	}{{"staged", plan.RouteRclone, false}, {"native", plan.RouteNative, false}, {"symlink", plan.RouteNative, true}} {
		t.Run(c.name, func(t *testing.T) {
			w := newWorld(t)
			name := "doc"
			if c.name == "native" {
				name = "line\nname" // an unsafe name is what routes a plain object natively
			}
			source := []byte("NEW SOURCE CONTENT")
			if c.link {
				seed(w.srcMock, "media", "target", []byte("target bytes"), nil)
				w.srcMock.PutObject("media", name, swifttest.Object{
					ContentType: "application/octet-stream", SymlinkTarget: "media/target",
				})
			} else {
				seed(w.srcMock, "media", name, source, nil)
			}
			seed(w.dstMock, "backup", name, []byte("OLD"), nil)

			rec := newOverwriteRecorder()
			rec.acknowledge(t, w, name)
			// The hook sees the context the run was given: the marker proves
			// forwarding, not that a cancellation reaches the read, because
			// context.WithoutCancel keeps values.
			type liveKey struct{}
			rec.versioning = func(ctx context.Context) (string, error) {
				if ctx.Value(liveKey{}) == nil {
					return "", errors.New("the hook was asked with a context that is not the run's")
				}
				return "the container now reports versions_location; the question said none", nil
			}

			e := recordingExecutor(t, w, &fakeEngine{w: w, interruptAfter: -1}, rec.recorder())
			report, err := e.Run(context.WithValue(context.Background(), liveKey{}, true), []plan.Item{
				{Name: name, Verdict: plan.VerdictChanged, Route: c.route, SourceBytes: int64(len(source))},
			})
			if err != nil {
				t.Fatal(err)
			}
			if report.OverwritesRefused != 1 || report.Copied != 0 || report.Overwrites != 0 || len(report.Failures) != 0 {
				t.Fatalf("a changed versioning statement was not refused at the fixed point: %+v", report)
			}
			if d := headDst(t, w, name); d.ContentLength != 3 || d.ETag != etagOf([]byte("OLD")) {
				t.Errorf("the refused replacement touched the live object: len=%d etag=%s", d.ContentLength, d.ETag)
			}
			if _, ok := rec.replaced[name]; ok {
				t.Error("a refused replacement recorded a predecessor")
			}
			if _, ok := rec.refused[name]; ok {
				t.Error("a statement refusal was recorded as an identity refusal")
			}
		})
	}
}

// A cancel that lands in the versioning read is the job stopping, not a finding
// about the container: no overwrite is counted as refused, the live object is
// untouched and nothing is recorded as replaced.
func TestAcknowledgedOverwriteCancelledAtTheVersioningReadIsNotARefusal(t *testing.T) {
	w := newWorld(t)
	source := []byte("NEW SOURCE CONTENT")
	seed(w.srcMock, "media", "doc", source, nil)
	seed(w.dstMock, "backup", "doc", []byte("OLD"), nil)

	rec := newOverwriteRecorder()
	rec.acknowledge(t, w, "doc")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	rec.versioning = func(context.Context) (string, error) {
		cancel()
		return "the job was cancelled before the container's versioning was read again", nil
	}

	e := recordingExecutor(t, w, &fakeEngine{w: w, interruptAfter: -1}, rec.recorder())
	report, err := e.Run(ctx, []plan.Item{
		{Name: "doc", Verdict: plan.VerdictChanged, Route: plan.RouteRclone, SourceBytes: int64(len(source))},
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("run = %v, want the cancellation", err)
	}
	if report.OverwritesRefused != 0 || report.Overwrites != 0 || report.Copied != 0 {
		t.Fatalf("a cancelled read was counted as an outcome: %+v", report)
	}
	if d := headDst(t, w, "doc"); d.ContentLength != 3 || d.ETag != etagOf([]byte("OLD")) {
		t.Errorf("the cancelled replacement touched the live object: len=%d etag=%s", d.ContentLength, d.ETag)
	}
	if _, ok := rec.replaced["doc"]; ok {
		t.Error("a cancelled replacement recorded a predecessor")
	}
}

// A refusal that could not be recorded is lost whether or not the job was
// cancelled at the same moment: the run still stops on the lost record, and
// says so, rather than ending as an ordinary cancellation.
func TestAcknowledgedOverwriteKeepsALostRefusalRecordThroughACancel(t *testing.T) {
	w := newWorld(t)
	source := []byte("NEW SOURCE CONTENT")
	seed(w.srcMock, "media", "doc", source, nil)
	seed(w.dstMock, "backup", "doc", []byte("OLD"), nil)

	rec := newOverwriteRecorder()
	rec.acknowledge(t, w, "doc")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	rec.versioning = func(context.Context) (string, error) {
		cancel()
		return "", errors.New("the audit store is unavailable")
	}

	e := recordingExecutor(t, w, &fakeEngine{w: w, interruptAfter: -1}, rec.recorder())
	_, err := e.Run(ctx, []plan.Item{
		{Name: "doc", Verdict: plan.VerdictChanged, Route: plan.RouteRclone, SourceBytes: int64(len(source))},
	})
	if err == nil || !strings.Contains(err.Error(), "could not be recorded") {
		t.Fatalf("a cancel hid a refusal that could not be recorded: %v", err)
	}
	if d := headDst(t, w, "doc"); d.ContentLength != 3 || d.ETag != etagOf([]byte("OLD")) {
		t.Errorf("the refused replacement touched the live object: len=%d etag=%s", d.ContentLength, d.ETag)
	}
}

// A refusal on versioning that could not be recorded still refuses the name,
// and the run stops, as it does when an identity refusal has nowhere to go.
func TestAcknowledgedOverwriteStopsWhenAVersioningRefusalIsUnrecorded(t *testing.T) {
	w := newWorld(t)
	source := []byte("NEW SOURCE CONTENT")
	seed(w.srcMock, "media", "doc", source, nil)
	seed(w.dstMock, "backup", "doc", []byte("OLD"), nil)

	rec := newOverwriteRecorder()
	rec.acknowledge(t, w, "doc")
	rec.versioning = func(context.Context) (string, error) { return "", errors.New("the audit store is unavailable") }

	e := recordingExecutor(t, w, &fakeEngine{w: w, interruptAfter: -1}, rec.recorder())
	report, err := e.Run(context.Background(), []plan.Item{
		{Name: "doc", Verdict: plan.VerdictChanged, Route: plan.RouteRclone, SourceBytes: int64(len(source))},
	})
	if err == nil || !strings.Contains(err.Error(), "could not be recorded") {
		t.Fatalf("an unrecorded refusal did not stop the run: %v", err)
	}
	if report.OverwritesRefused != 1 || report.Copied != 0 {
		t.Fatalf("the name was not refused: %+v", report)
	}
	if d := headDst(t, w, "doc"); d.ContentLength != 3 {
		t.Errorf("the refused replacement touched the live object: len=%d", d.ContentLength)
	}
}

// A run with nobody to ask whether the observation still holds refuses the
// replacement rather than publishing under versioning nobody re-read, the
// direction every other missing overwrite hook takes.
func TestAcknowledgedOverwriteRefusesWithoutVersioningHook(t *testing.T) {
	w := newWorld(t)
	source := []byte("NEW SOURCE CONTENT")
	seed(w.srcMock, "media", "doc", source, nil)
	seed(w.dstMock, "backup", "doc", []byte("OLD"), nil)

	rec := newOverwriteRecorder()
	rec.acknowledge(t, w, "doc")
	recorder := rec.recorder()
	recorder.VersioningUnchanged = nil

	e := recordingExecutor(t, w, &fakeEngine{w: w, interruptAfter: -1}, recorder)
	report, err := e.Run(context.Background(), []plan.Item{
		{Name: "doc", Verdict: plan.VerdictChanged, Route: plan.RouteRclone, SourceBytes: int64(len(source))},
	})
	if err != nil {
		t.Fatal(err)
	}
	if report.OverwritesRefused != 1 || report.Copied != 0 || len(report.Failures) != 0 {
		t.Fatalf("a replacement with nobody to ask about versioning was not refused: %+v", report)
	}
	if d := headDst(t, w, "doc"); d.ContentLength != 3 {
		t.Errorf("the refused replacement touched the live object: len=%d", d.ContentLength)
	}
}

// A source symlink whose acknowledged destination has vanished by the fixed
// point is created as a symlink, with its observed absence recorded first, the
// same as the plain and native routes (db:937 finding 3).
func TestAcknowledgedSymlinkOverwriteRecordsVanishedAbsence(t *testing.T) {
	w := newWorld(t)
	name := "linkobj"
	seed(w.srcMock, "media", "target", []byte("target bytes"), nil)
	w.srcMock.PutObject("media", name, swifttest.Object{
		ContentType: "application/octet-stream", SymlinkTarget: "media/target",
	})
	seed(w.dstMock, "backup", name, []byte("OLD"), nil)

	rec := newOverwriteRecorder()
	rec.acknowledge(t, w, name)
	w.dstMock.RemoveObject("backup", name) // vanished after the acknowledgement

	e := recordingExecutor(t, w, &fakeEngine{w: w, interruptAfter: -1}, rec.recorder())
	report, err := e.Run(context.Background(), []plan.Item{
		{Name: name, Verdict: plan.VerdictChanged, Route: plan.RouteNative},
	})
	if err != nil {
		t.Fatal(err)
	}
	if report.Copied != 1 || report.Overwrites != 0 || report.OverwritesRefused != 0 {
		t.Fatalf("vanished symlink destination was not created cleanly: %+v", report)
	}
	observed, ok := rec.replaced[name]
	if !ok {
		t.Fatal("the vanished symlink destination's absence was not recorded")
	}
	if observed.Present {
		t.Errorf("absence recorded as present: %+v", observed)
	}
	if written, serr := w.dst.SymlinkGet(context.Background(), "backup", name); serr != nil || written.SymlinkTarget != "media/target" {
		t.Errorf("free name did not take the symlink: target=%q err=%v", written.SymlinkTarget, serr)
	}
}

// A staged symlink overwrite refused at the fixed point carries the engine's
// unresolved effect, not not-attempted: by the refusal the staged route has
// already written the symlink's bytes into the staging namespace (ADR 0030), so
// the item's recorded uncertainty must say the destination MIGHT hold staged
// residue. The native route, which stages nothing, keeps not-attempted
// (its existing tests cover that). The admitted-item validator must also accept
// refused+unresolved, which it did not before (db:944 finding 2).
func TestAcknowledgedStagedSymlinkOverwriteRefusalCarriesUnresolvedEffect(t *testing.T) {
	w := newWorld(t)
	name := "linkobj" // rclone-safe, so the copy routes staged
	seed(w.srcMock, "media", "target", []byte("target bytes"), nil)
	w.srcMock.PutObject("media", name, swifttest.Object{
		ContentType: "application/octet-stream", SymlinkTarget: "media/target",
	})
	seed(w.dstMock, "backup", name, []byte("OLD"), nil)

	rec := newOverwriteRecorder()
	rec.acknowledge(t, w, name) // baseline: the plain 3-byte object
	seed(w.dstMock, "backup", name, []byte("CHANGED"), nil)

	e := recordingExecutor(t, w, &fakeEngine{w: w, interruptAfter: -1}, rec.recorder())
	report, err := e.Run(context.Background(), []plan.Item{
		{Name: name, Verdict: plan.VerdictChanged, Route: plan.RouteRclone, SourceBytes: 3},
	})
	if err != nil {
		t.Fatal(err)
	}
	if report.OverwritesRefused != 1 || report.Overwrites != 0 {
		t.Fatalf("staged symlink overwrite of a changed destination was not refused: %+v", report)
	}
	observed, ok := rec.executed[name]
	if !ok || observed.Result != ResultRefused {
		t.Fatalf("the staged symlink refusal was not recorded as a refusal: %+v ok=%t", observed, ok)
	}
	if observed.Effect != EffectUnresolved {
		t.Errorf("staged symlink refusal lost the staged bytes' uncertainty: effect=%q, want %q", observed.Effect, EffectUnresolved)
	}
}

// ADR 0104 decision 1: an engine batch is a unit of its own. An ordinary name
// is only buffered when the plan hands it over, so a copy that then handles a
// native name has the batch still pending when the plan ends, and it is asked
// to pause once more before that batch's child starts. A cancel seen there
// starts no child and records the buffered name as not attempted; without one
// the batch is dispatched as before.
func TestExecutorAsksToPauseBeforeAPendingEngineBatch(t *testing.T) {
	for _, ending := range []string{"carries on", "cancelled"} {
		t.Run(ending, func(t *testing.T) {
			w := newWorld(t)
			seed(w.srcMock, "media", "a-engine", []byte("alpha"), nil)
			seed(w.srcMock, "media", "z\nnative", []byte("zulu"), nil)
			items := []plan.Item{
				{Name: "a-engine", Verdict: plan.VerdictNew, Route: plan.RouteRclone, SourceBytes: 5},
				{Name: "z\nnative", Verdict: plan.VerdictNew, Route: plan.RouteNative, SourceBytes: 4},
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			rec := newOverwriteRecorder()
			recorder := rec.recorder()
			asked := 0
			recorder.Pause = func(context.Context) error {
				asked++
				// Two item heads, then the head of the pending batch.
				if asked == 3 && ending == "cancelled" {
					cancel()
					return ctx.Err()
				}
				return nil
			}
			e := recordingExecutor(t, w, &fakeEngine{w: w, interruptAfter: -1}, recorder)
			report, err := e.Run(ctx, items)
			if asked != 3 {
				t.Fatalf("the run asked to pause %d times, want the two item heads and the batch head", asked)
			}
			_, headErr := w.dst.ObjectDetail(context.Background(), "backup", "a-engine")
			if ending == "carries on" {
				if err != nil || report.Copied != 2 || headErr != nil {
					t.Fatalf("report = %+v, %v, engine name read %v; want both names copied", report, err, headErr)
				}
				return
			}
			if !errors.Is(err, context.Canceled) || report.Copied != 1 || len(report.StagedResidue) != 0 || headErr == nil {
				t.Fatalf("report = %+v, %v, engine name read %v; want the native name copied and no child started", report, err, headErr)
			}
			if got := rec.executed["a-engine"]; got.Result != ResultNotAttempted || got.Effect != EffectNotAttempted {
				t.Errorf("the buffered name is recorded as %+v, want not attempted", got)
			}
		})
	}
}

// ADR 0104 decision 1: the executor asks to pause at the head of each item
// the plan hands it, and at the head of an engine batch, and nowhere else. A
// cancellation returned from that wait ends the run as a cancel seen there
// does: the item before it is copied, and nothing is done or recorded for the
// item it stopped at or any after.
func TestExecutorAsksToPauseAtItemHeadsOnly(t *testing.T) {
	w := newWorld(t)
	// A newline in a name is what routes a plain object natively, so each
	// item is copied as it is handled.
	names := []string{"a\none", "b\ntwo", "c\nthree"}
	var items []plan.Item
	for _, name := range names {
		seed(w.srcMock, "media", name, []byte("bytes of "+name), nil)
		items = append(items, plan.Item{Name: name, Verdict: plan.VerdictNew, Route: plan.RouteNative, SourceBytes: int64(len("bytes of " + name))})
	}
	rec := newOverwriteRecorder()
	recorder := rec.recorder()
	asked := 0
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	recorder.Pause = func(context.Context) error {
		asked++
		if asked == 2 {
			cancel()
			return context.Canceled
		}
		return nil
	}
	e := recordingExecutor(t, w, &fakeEngine{w: w, interruptAfter: -1}, recorder)
	report, err := e.Run(ctx, items)
	if !errors.Is(err, context.Canceled) || asked != 2 || report.Copied != 1 {
		t.Fatalf("report = %+v, %v after %d asks; want the first item copied and the cancel at the second head", report, err, asked)
	}
	if len(rec.executed) != 1 {
		t.Errorf("executions recorded = %v, want none for the items never started", rec.executed)
	}
}
