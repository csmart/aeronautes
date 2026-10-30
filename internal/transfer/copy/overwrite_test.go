package copy

import (
	"context"
	"crypto/md5"
	"encoding/hex"
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
}

func newOverwriteRecorder() *overwriteRecorder {
	return &overwriteRecorder{
		baselines: map[string]ShownObject{},
		replaced:  map[string]ObservedPredecessor{},
		refused:   map[string][2]ShownObject{},
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
		Execute: func(string, Observation) error { return nil },
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

// An unsafe-named object copies through the native route, which has no
// fixed-point comparison yet, so an acknowledged overwrite of one still refuses.
func TestAcknowledgedNativeOverwriteRefuses(t *testing.T) {
	w := newWorld(t)
	name := "line\nname"
	seed(w.srcMock, "media", name, []byte("NEW"), nil)
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
	if report.OverwritesRefused != 1 || report.Copied != 0 {
		t.Fatalf("acknowledged native overwrite was not refused: %+v", report)
	}
	if d := headDst(t, w, name); d.ContentLength != 3 {
		t.Errorf("refused native overwrite altered the destination: len=%d", d.ContentLength)
	}
	if _, ok := rec.replaced[name]; ok {
		t.Error("refused native overwrite recorded a replacement")
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
