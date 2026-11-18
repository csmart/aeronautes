package local

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"

	"github.com/csmart/aeronautes/internal/swiftcontrol"
	transfercopy "github.com/csmart/aeronautes/internal/transfer/copy"
	"github.com/csmart/aeronautes/internal/transfer/rclone"
)

// batchCountingEngine records the names each child was given, can answer
// one call with a failed child that copied nothing, and runs a hook after
// each child that copied.
type batchCountingEngine struct {
	Engine
	calls  [][]string
	failOn int
	after  func(call int)
}

func (e *batchCountingEngine) Copy(ctx context.Context, src, dst string, names []string, event func(rclone.Event)) (*rclone.Result, error) {
	call := len(e.calls) + 1
	e.calls = append(e.calls, append([]string(nil), names...))
	if e.failOn == call {
		return &rclone.Result{ExitCode: 1}, nil
	}
	result, err := e.Engine.Copy(ctx, src, dst, names, event)
	if err == nil && e.after != nil {
		e.after(call)
	}
	return result, err
}

type batchFixture struct {
	root     string
	plan     *DownloadPreflight
	names    []string
	executed map[string]transfercopy.Observation
	verified map[string]transfercopy.Observation
	recorder *DownloadRecorder
}

// newBatchFixture seeds count engine-routed objects under scope/ in plan
// order and a recorder that keeps the last execute and verify
// observation of every item.
func newBatchFixture(t *testing.T, count int) (*batchFixture, *swiftcontrol.Session) {
	t.Helper()
	source, mock := session(t)
	f := &batchFixture{root: t.TempDir(), executed: map[string]transfercopy.Observation{}, verified: map[string]transfercopy.Observation{}}
	for i := range count {
		name := fmt.Sprintf("o-%04d", i)
		seed(mock, "media", "scope/"+name, "bytes "+name)
		f.names = append(f.names, name)
	}
	writeFile(t, f.root, "keep", "unrelated")
	plan, err := PreflightDownload(t.Context(), source, "media", "scope/", f.root)
	if err != nil || plan.Rejected() || len(plan.Items) != count {
		t.Fatalf("batch fixture preflight: %d items %v", len(plan.Items), err)
	}
	f.plan = plan
	f.recorder = &DownloadRecorder{
		Admit: func(DownloadAdmission) error { return nil },
		Execute: func(item DownloadItem, o transfercopy.Observation) error {
			f.executed[item.Name] = o
			return nil
		},
		Verify: func(item DownloadItem, o transfercopy.Observation) error {
			f.verified[item.Name] = o
			return nil
		},
	}
	return f, source
}

// lengths reports the names each child was given.
func lengths(calls [][]string) []int {
	var out []int
	for _, call := range calls {
		out = append(out, len(call))
	}
	return out
}

// expectExecute checks the execute observation of the items at [from, to).
func (f *batchFixture) expectExecute(t *testing.T, from, to int, effect transfercopy.Effect, stage transfercopy.Stage, verified bool) {
	t.Helper()
	for _, name := range f.names[from:to] {
		o, ok := f.executed["scope/"+name]
		// A copied item carries no diagnostic stage; a failed one names it.
		if !ok || o.Effect != effect || (stage != "" && o.Diagnostic.Stage != stage) {
			t.Fatalf("%s execute = %+v (recorded %v), want effect %s at stage %s", name, o, ok, effect, stage)
		}
		if _, ok := f.verified["scope/"+name]; ok != verified {
			t.Fatalf("%s verified = %v, want %v", name, ok, verified)
		}
	}
}

// ADR 0074 decision 3: the engine-routed names go to the child in
// batches of DefaultBatchNames in plan order, and every file of a batch has
// its outcome before the next child starts.
func TestDownloadBatchesEngineNames(t *testing.T) {
	count := 2*transfercopy.DefaultBatchNames + 1
	f, source := newBatchFixture(t, count)
	engine := &batchCountingEngine{Engine: &fsEngine{session: source}}
	recordedBeforeNext := true
	engine.after = func(call int) {
		if call == 2 {
			for _, name := range f.names[:transfercopy.DefaultBatchNames] {
				if f.executed["scope/"+name].Result != transfercopy.ResultCopied {
					recordedBeforeNext = false
				}
			}
		}
	}
	report, err := Download(t.Context(), source, engine, "remote:media", "media", "scope/", f.root, f.plan, nil, f.recorder)
	if err != nil || report.Done != int64(count) || len(report.Failures) != 0 {
		t.Fatalf("batched download: %+v %v", report, err)
	}
	if got := lengths(engine.calls); len(got) != 3 || got[0] != transfercopy.DefaultBatchNames || got[1] != transfercopy.DefaultBatchNames || got[2] != 1 {
		t.Fatalf("children were given %v names", got)
	}
	if !recordedBeforeNext {
		t.Fatal("the second child ran before the first batch's files had their outcome")
	}
	var given []string
	for _, call := range engine.calls {
		given = append(given, call...)
	}
	for i, name := range f.names {
		if given[i] != name {
			t.Fatalf("plan order lost at %d: %q, want %q", i, given[i], name)
		}
	}
	f.expectExecute(t, 0, count, transfercopy.EffectReportedApplied, "", true)
}

// A child that fails leaves its batch unknown and does not change what the
// children before it said; the next batch is given to a child of its own,
// as a copy's is, because there is no stage whose residue to bound.
func TestDownloadBatchFailureKeepsEachBatchsOutcome(t *testing.T) {
	n := transfercopy.DefaultBatchNames
	f, source := newBatchFixture(t, 2*n+1)
	engine := &batchCountingEngine{Engine: &fsEngine{session: source}, failOn: 2}
	report, err := Download(t.Context(), source, engine, "remote:media", "media", "scope/", f.root, f.plan, nil, f.recorder)
	// The report keeps a bounded sample of failures; the records carry
	// every item.
	if err != nil || report.Done != int64(n+1) || report.Verified != int64(n+1) || len(report.Failures) == 0 || len(engine.calls) != 3 || !report.EngineStopped {
		t.Fatalf("failed second batch: done=%d verified=%d failures=%d calls=%d stopped=%v %v", report.Done, report.Verified, len(report.Failures), len(engine.calls), report.EngineStopped, err)
	}
	f.expectExecute(t, 0, n, transfercopy.EffectReportedApplied, "", true)
	f.expectExecute(t, n, 2*n, transfercopy.EffectUnresolved, transfercopy.StageEngine, false)
	f.expectExecute(t, 2*n, 2*n+1, transfercopy.EffectReportedApplied, "", true)
}

// A cancel seen after a child returns keeps what that child said, and the
// batches after it were never given to a child.
func TestDownloadCancelBetweenBatchesLeavesLaterBatchesUnattempted(t *testing.T) {
	n := transfercopy.DefaultBatchNames
	f, source := newBatchFixture(t, 2*n+1)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	engine := &batchCountingEngine{Engine: &fsEngine{session: source}}
	engine.after = func(call int) {
		if call == 1 {
			cancel()
		}
	}
	report, err := Download(ctx, source, engine, "remote:media", "media", "scope/", f.root, f.plan, nil, f.recorder)
	if !errors.Is(err, context.Canceled) || report.Done != int64(n) || len(engine.calls) != 1 {
		t.Fatalf("cancel between batches: %+v calls=%d %v", report, len(engine.calls), err)
	}
	f.expectExecute(t, 0, n, transfercopy.EffectReportedApplied, "", true)
	f.expectExecute(t, n, 2*n+1, transfercopy.EffectNotAttempted, transfercopy.StageEngine, false)
}

// The batch closes on the name that reaches either bound, in plan order.
func TestDownloadBatchesByNamesAndPlannedBytes(t *testing.T) {
	const gib = int64(1 << 30)
	item := func(size int64) DownloadItem { return DownloadItem{Size: size} }
	for _, tc := range []struct {
		name  string
		sizes []int64
		want  [][]string
	}{
		{name: "empty"},
		{name: "one", sizes: []int64{1}, want: [][]string{{"n0"}}},
		{name: "bytes close a batch on the item that reaches the bound", sizes: []int64{40 * gib, 40 * gib, 40 * gib}, want: [][]string{{"n0", "n1"}, {"n2"}}},
		{name: "bytes under the bound stay together", sizes: []int64{30 * gib, 30 * gib, 3 * gib}, want: [][]string{{"n0", "n1", "n2"}}},
		{name: "an oversize item is its own batch", sizes: []int64{100 * gib, 1}, want: [][]string{{"n0"}, {"n1"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var names []string
			items := map[string]DownloadItem{}
			for i, size := range tc.sizes {
				name := fmt.Sprintf("n%d", i)
				names = append(names, name)
				items[name] = item(size)
			}
			got := downloadBatches(names, items)
			if fmt.Sprint(got) != fmt.Sprint(tc.want) {
				t.Fatalf("batches = %v, want %v", got, tc.want)
			}
		})
	}
	var names []string
	items := map[string]DownloadItem{}
	for i := range transfercopy.DefaultBatchNames + 1 {
		name := fmt.Sprintf("n%d", i)
		names = append(names, name)
		items[name] = item(1)
	}
	if got := lengths(downloadBatches(names, items)); len(got) != 2 || got[0] != transfercopy.DefaultBatchNames || got[1] != 1 {
		t.Fatalf("count bound: %v", got)
	}
}

// ADR 0104 decision 1: the download route asks to pause at the head of each
// engine batch and nowhere else. A cancellation returned from that wait
// leaves the batch before it downloaded and the batch after it never given
// to a child.
func TestDownloadAsksToPauseAtBatchHeadsOnly(t *testing.T) {
	n := transfercopy.DefaultBatchNames
	f, source := newBatchFixture(t, n+1)
	engine := &batchCountingEngine{Engine: &fsEngine{session: source}}
	asked := 0
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	f.recorder.Pause = func(context.Context) error {
		asked++
		if asked == 2 {
			cancel()
			return context.Canceled
		}
		return nil
	}
	report, err := Download(ctx, source, engine, "remote:media", "media", "scope/", f.root, f.plan, nil, f.recorder)
	if !errors.Is(err, context.Canceled) || asked != 2 || len(engine.calls) != 1 || report.Done != int64(n) {
		t.Fatalf("report = %+v, %v; asked %d times with %d children, want one batch downloaded and the cancel at the next head", report, err, asked, len(engine.calls))
	}
	f.expectExecute(t, 0, n, transfercopy.EffectReportedApplied, "", true)
	f.expectExecute(t, n, n+1, transfercopy.EffectNotAttempted, transfercopy.StageEngine, false)
}

// ADR 0122 decision 14 (finding F78): a file the engine reports written or
// the same is recorded and shown in the progress while the engine is still
// running, and a file with an error line or none is recorded after the run.
// A file the engine reports again, in the run repeated for an expired
// sign-in, is recorded once.
func TestDownloadRecordsEachFileWhenTheEngineReportsIt(t *testing.T) {
	f, source := newBatchFixture(t, 4)
	records := map[string]int{}
	execute := f.recorder.Execute
	f.recorder.Execute = func(item DownloadItem, o transfercopy.Observation) error {
		records[item.Name]++
		return execute(item, o)
	}
	var shownDone, shownSame int64
	recordedNow := func(t *testing.T, want map[string]int) {
		t.Helper()
		if !reflect.DeepEqual(records, want) {
			t.Fatalf("recorded while the engine runs = %v, want %v", records, want)
		}
	}
	runs := 0
	engine := &scriptedEngine{run: func(_ context.Context, _, _ string, _ []string, onEvent func(rclone.Event)) (*rclone.Result, error) {
		runs++
		onEvent(rclone.Event{Level: "info", Object: "o-0000", Msg: "Copied (new)"})
		if runs == 1 {
			recordedNow(t, map[string]int{"scope/o-0000": 1})
			return &rclone.Result{ExitCode: 1, Errors: []string{"401 Unauthorized"}}, nil
		}
		onEvent(rclone.Event{Level: "debug", Object: "o-0001", Msg: "Unchanged skipping"})
		onEvent(rclone.Event{Level: "error", Object: "o-0002", Msg: "Failed to copy: the store said no"})
		recordedNow(t, map[string]int{"scope/o-0000": 1, "scope/o-0001": 1})
		if shownDone != 1 || shownSame != 1 {
			t.Fatalf("progress while the engine runs = %d written and %d the same, want 1 and 1", shownDone, shownSame)
		}
		return &rclone.Result{}, nil
	}}
	report, err := Download(t.Context(), source, engine, "remote:media", "media", "scope/", f.root, f.plan,
		func(done, same, _, _ int64) { shownDone, shownSame = done, same }, f.recorder)
	if err != nil || runs != 2 || report.Done != 1 || report.Same != 1 || len(report.Failures) != 2 {
		t.Fatalf("runs = %d, report = %+v, %v", runs, report, err)
	}
	want := map[string]int{"scope/o-0000": 1, "scope/o-0001": 1, "scope/o-0002": 1, "scope/o-0003": 1}
	if !reflect.DeepEqual(records, want) {
		t.Fatalf("records = %v, want one for each file", records)
	}
}
