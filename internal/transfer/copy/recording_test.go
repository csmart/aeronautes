package copy

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/csmart/aeronautes/internal/swiftcontrol"
	"github.com/csmart/aeronautes/internal/swifttest"
	"github.com/csmart/aeronautes/internal/transfer/plan"
	"github.com/csmart/aeronautes/internal/transfer/rclone"
)

func recordingExecutor(t *testing.T, w *world, engine Engine, recorder *Recorder) *Executor {
	t.Helper()
	executor, err := New(Config{Source: w.src, Dest: w.dst, Engine: engine,
		SrcSpec: "source:media", DstSpec: "dest:backup", SrcContainer: "media", DstContainer: "backup",
		JobID: "recorded", Recorder: recorder})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := executor.Close(); err != nil {
			t.Error(err)
		}
	})
	return executor
}

func TestRecorderRequiresEveryAcknowledgement(t *testing.T) {
	w := newWorld(t)
	for mask := 0; mask < 7; mask++ {
		t.Run(fmt.Sprint(mask), func(t *testing.T) {
			recorder := &Recorder{}
			if mask&1 != 0 {
				recorder.Admit = func(Admission) error { return nil }
			}
			if mask&2 != 0 {
				recorder.Execute = func(string, Observation) error { return nil }
			}
			if mask&4 != 0 {
				recorder.Verify = func(string, Observation) error { return nil }
			}
			if executor, err := New(Config{Source: w.src, Dest: w.dst, SrcContainer: "media", DstContainer: "backup", JobID: "invalid", Recorder: recorder}); err == nil || executor != nil {
				t.Fatalf("partial recording contract admitted an executor: %+v %v", executor, err)
			}
		})
	}
}

func TestRecordAdmissionStopsBufferedAndNativeWork(t *testing.T) {
	w := newWorld(t)
	seed(w.srcMock, "media", "a", []byte("buffered"), nil)
	seed(w.srcMock, "media", "b\nnative", []byte("native"), nil)
	engine := &recordingEngine{inner: &fakeEngine{w: w, interruptAfter: -1}}
	refusal := errors.New("fixture admission refused")
	var admitted []Admission
	observed := map[string]Observation{}
	recorder := &Recorder{Admit: func(a Admission) error {
		if a.Item.Name == "b\nnative" {
			return refusal
		}
		admitted = append(admitted, a)
		return nil
	}, Execute: func(key string, observation Observation) error {
		observed[key] = observation
		return nil
	}, Verify: func(string, Observation) error { t.Error("verification crossed recording failure"); return nil }}
	e := recordingExecutor(t, w, engine, recorder)
	if err := e.Handle(t.Context(), plan.Item{Name: "a", SourceBytes: 8, Verdict: plan.VerdictNew, Route: plan.RouteRclone}); err != nil {
		t.Fatal(err)
	}
	beforeSrc, beforeDst := len(w.srcMock.RequestLog()), len(w.dstMock.RequestLog())
	if err := e.Handle(t.Context(), plan.Item{Name: "b\nnative", SourceBytes: 6, Verdict: plan.VerdictNew, Route: plan.RouteNative}); !errors.Is(err, refusal) {
		t.Fatalf("admission refusal lost: %v", err)
	}
	for i := 0; i < 2; i++ {
		if _, err := e.Finish(t.Context()); !errors.Is(err, refusal) {
			t.Fatalf("Finish forgot failed admission: %v", err)
		}
	}
	if len(engine.dsts) != 0 || len(w.srcMock.RequestLog()) != beforeSrc || len(w.dstMock.RequestLog()) != beforeDst ||
		len(admitted) != 1 || admitted[0].StagingObject != ".aeronautes/stage/recorded/batch/a" || len(observed) != 1 ||
		observed["a"].Result != ResultNotAttempted || observed["a"].Effect != EffectNotAttempted {
		t.Fatalf("record refusal dispatched work or lost buffered intent: admissions=%+v observations=%+v engine=%v", admitted, observed, engine.dsts)
	}
	if _, err := e.Verify(t.Context(), nil, nil); !errors.Is(err, refusal) {
		t.Fatalf("verification forgot failed admission: %v", err)
	}
}

func TestRecordExecutionFailureStopsAfterNativeEffect(t *testing.T) {
	w := newWorld(t)
	seed(w.srcMock, "media", "a\nnative", []byte("first bytes"), nil)
	seed(w.srcMock, "media", "b\nnative", []byte("later bytes"), nil)
	refusal := errors.New("fixture observation refused")
	var admissions, observations int
	recorder := &Recorder{Admit: func(a Admission) error {
		admissions++
		if a.StagingObject != "" {
			t.Error("native admission invented a staging object")
		}
		return nil
	}, Execute: func(key string, observation Observation) error {
		observations++
		written := headDst(t, w, key)
		if written.ContentLength != 11 || observation != (Observation{Result: ResultCopied, Effect: EffectReportedApplied, CopiedBytes: 11}) {
			t.Fatalf("observation preceded the independently visible copy: %+v %+v", written, observation)
		}
		return refusal
	}, Verify: func(string, Observation) error { t.Error("verification crossed a failed execution record"); return nil }}
	e := recordingExecutor(t, w, nil, recorder)
	if err := e.Handle(t.Context(), plan.Item{Name: "a\nnative", SourceBytes: 11, Verdict: plan.VerdictNew, Route: plan.RouteNative}); !errors.Is(err, refusal) {
		t.Fatalf("post-effect recording error lost: %v", err)
	}
	beforeSrc, beforeDst := len(w.srcMock.RequestLog()), len(w.dstMock.RequestLog())
	if err := e.Handle(t.Context(), plan.Item{Name: "b\nnative", SourceBytes: 11, Verdict: plan.VerdictNew, Route: plan.RouteNative}); !errors.Is(err, refusal) {
		t.Fatalf("later native work ignored recording failure: %v", err)
	}
	if report, err := e.Finish(t.Context()); !errors.Is(err, refusal) || report.Copied != 1 || report.BytesCopied != 11 {
		t.Fatalf("Finish lost the witnessed copy or refusal: %+v %v", report, err)
	}
	if admissions != 1 || observations != 1 || len(w.srcMock.RequestLog()) != beforeSrc || len(w.dstMock.RequestLog()) != beforeDst {
		t.Fatal("post-effect recording refusal allowed another dispatch")
	}
}

func TestBufferedRecordingCancellationHasNoEffects(t *testing.T) {
	w := newWorld(t)
	engine := &recordingEngine{inner: &fakeEngine{w: w, interruptAfter: -1}}
	observations := map[string]Observation{}
	e := recordingExecutor(t, w, engine, &Recorder{Admit: func(Admission) error { return nil },
		Execute: func(key string, observation Observation) error { observations[key] = observation; return nil },
		Verify:  func(string, Observation) error { return nil }})
	ctx, cancel := context.WithCancel(t.Context())
	for _, name := range []string{"a", "b"} {
		if err := e.Handle(ctx, plan.Item{Name: name, Verdict: plan.VerdictNew, Route: plan.RouteRclone}); err != nil {
			t.Fatal(err)
		}
	}
	beforeSrc, beforeDst := len(w.srcMock.RequestLog()), len(w.dstMock.RequestLog())
	cancel()
	if _, err := e.Finish(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("buffer cancellation lost: %v", err)
	}
	if len(observations) != 2 || len(engine.dsts) != 0 || len(w.srcMock.RequestLog()) != beforeSrc || len(w.dstMock.RequestLog()) != beforeDst {
		t.Fatalf("cancelled buffer reached an effect or lost outcomes: %+v", observations)
	}
	for _, observation := range observations {
		if observation.Result != ResultNotAttempted || observation.Effect != EffectNotAttempted || observation.Diagnostic.Code != CodeCancelled {
			t.Fatalf("cancelled buffer was misrepresented: %+v", observation)
		}
	}
}

func TestCommittedRecordFailureRetainsCopiedBytes(t *testing.T) {
	w := newWorld(t)
	for _, name := range []string{"a", "b"} {
		seed(w.srcMock, "media", name, []byte("abcdefgh"), nil)
	}
	observations := map[string]Observation{}
	e := recordingExecutor(t, w, &fakeEngine{w: w, chunk: 3, interruptAfter: -1}, &Recorder{
		Admit:   func(Admission) error { return nil },
		Execute: func(key string, observation Observation) error { observations[key] = observation; return nil },
		Verify:  func(string, Observation) error { return nil }})
	refusal := errors.New("fixture committed record refused")
	var intents int
	e.cfg.OnRecovery = func(kind, container, prefix, object string) error {
		if kind == "committing" {
			intents++
		}
		if kind == "committed" {
			live := headDst(t, w, "a")
			if live.Class != swiftcontrol.ClassDLO || live.ContentLength != 8 {
				t.Fatalf("committed record did not follow the live manifest: %+v", live)
			}
			return refusal
		}
		return nil
	}
	for _, name := range []string{"a", "b"} {
		if err := e.Handle(t.Context(), plan.Item{Name: name, SourceBytes: 8, Verdict: plan.VerdictNew, Route: plan.RouteRclone}); err != nil {
			t.Fatal(err)
		}
	}
	report, err := e.Finish(t.Context())
	want := Observation{Result: ResultCopied, Effect: EffectUnresolved, CopiedBytes: 8,
		Diagnostic: Diagnostic{Stage: StageRecording, Code: CodeUnknown}}
	if !errors.Is(err, refusal) || report.Copied != 1 || report.BytesCopied != 8 || len(report.Failures) != 0 ||
		len(report.StagedResidue) != 2 || intents != 1 || len(observations) != 1 || observations["a"] != want {
		t.Fatalf("post-PUT record failure lost copied bytes, retained residue or the stop: report=%+v observed=%+v err=%v", report, observations, err)
	}
	for _, name := range []string{"a", "b"} {
		if headDst(t, w, e.StagingPrefix()+"batch/"+name).Class != swiftcontrol.ClassDLO {
			t.Fatal("unacknowledged committed record allowed staging cleanup")
		}
	}
	if _, err := w.dst.ObjectDetail(t.Context(), "backup", "b"); !swiftcontrol.IsNotFound(err) {
		t.Fatalf("later live commit crossed failed recording: %v", err)
	}
	before := len(w.dstMock.RequestLog())
	if _, err := e.Finish(t.Context()); !errors.Is(err, refusal) || len(w.dstMock.RequestLog()) != before {
		t.Fatalf("later Finish lost its recording stop: %v", err)
	}
}

func TestRecordingStopBetweenStagedCommits(t *testing.T) {
	for _, mode := range []string{"record_refusal", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			w := newWorld(t)
			for _, key := range []string{"a", "b"} {
				seed(w.srcMock, "media", key, []byte("copy bytes"), nil)
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			refusal := errors.New("fixture staged observation refusal")
			admitted := map[string]Admission{}
			observed := map[string]Observation{}
			e := recordingExecutor(t, w, &fakeEngine{w: w, interruptAfter: -1}, &Recorder{
				Admit: func(a Admission) error { admitted[a.Item.Name] = a; return nil },
				Execute: func(key string, observation Observation) error {
					if key != "a" || len(admitted) != 2 || headDst(t, w, "a").ContentLength != 10 ||
						headDst(t, w, admitted["b"].StagingObject).ContentLength != 10 {
						t.Fatalf("callback crossed admission or live/staging boundaries: %q %+v", key, admitted)
					}
					if _, err := w.dst.ObjectDetail(t.Context(), "backup", admitted["a"].StagingObject); !swiftcontrol.IsNotFound(err) {
						t.Fatalf("final observation preceded staging cleanup: %v", err)
					}
					if mode == "record_refusal" {
						return refusal
					}
					observed[key] = observation
					cancel()
					return nil
				}, Verify: func(string, Observation) error { t.Error("unexpected verification"); return nil }})
			for _, key := range []string{"a", "b"} {
				if err := e.Handle(ctx, plan.Item{Name: key, SourceBytes: 10, Verdict: plan.VerdictNew, Route: plan.RouteRclone}); err != nil {
					t.Fatal(err)
				}
			}
			report, err := e.Finish(ctx)
			wantErr, count := refusal, 0
			if mode == "cancel" {
				wantErr, count = context.Canceled, 1
			}
			if !errors.Is(err, wantErr) || report.Copied != 1 || report.BytesCopied != 10 || len(observed) != count || len(report.StagedResidue) != 1 {
				t.Fatalf("staged stop lost applied progress or invented later outcomes: %+v %+v %v", report, observed, err)
			}
			if _, err := w.dst.ObjectDetail(t.Context(), "backup", "b"); !swiftcontrol.IsNotFound(err) {
				t.Fatalf("second live commit crossed the stop: %v", err)
			}
			if headDst(t, w, admitted["b"].StagingObject).ContentLength != 10 {
				t.Fatal("engine-started unresolved data was reclaimed")
			}
		})
	}
}

func TestVerificationRecordingPrecedesStreamAdvance(t *testing.T) {
	for _, fault := range []string{"record", "listing"} {
		t.Run(fault, func(t *testing.T) {
			w := newWorld(t)
			refusal := errors.New("fixture verification refusal")
			var observed []string
			sourceReads, destReads := 0, 0
			source := func(yield func(swiftcontrol.ListingEntry, error) bool) {
				for i, key := range []string{"a", "b", "c", "d"} {
					if fault == "listing" && i == 2 {
						yield(nil, refusal)
						return
					}
					sourceReads++
					if !yield(swiftcontrol.ObjectEntry{Name: key, Bytes: 10, ETag: "source"}, nil) {
						return
					}
				}
			}
			dest := func(yield func(swiftcontrol.ListingEntry, error) bool) {
				for _, key := range []string{"a", "b", "c", "d"} {
					destReads++
					if !yield(swiftcontrol.ObjectEntry{Name: key, Bytes: 10, ETag: "destination"}, nil) {
						return
					}
				}
			}
			e := recordingExecutor(t, w, nil, &Recorder{
				Admit:   func(Admission) error { t.Error("verification invented admission"); return nil },
				Execute: func(string, Observation) error { t.Error("verification invented execution"); return nil },
				Verify: func(key string, o Observation) error {
					if sourceReads != len(observed)+1 || destReads != len(observed)+1 ||
						o.Result != ResultFailed || o.Effect != EffectNotAttempted || o.Diagnostic != (Diagnostic{Stage: StageVerification, Code: CodeUnverified}) {
						t.Errorf("stream advanced before checked comparison: key=%q source=%d dest=%d prior=%v observation=%+v", key, sourceReads, destReads, observed, o)
					}
					if fault == "record" && key == "b" {
						return refusal
					}
					observed = append(observed, key)
					return nil
				}})
			report, err := e.Verify(t.Context(), source, dest)
			want := []string{"a", "b"}
			if fault == "record" {
				want = want[:1]
			}
			if !errors.Is(err, refusal) || report == nil || report.Verified != 0 || len(report.Failures) != 2 ||
				report.Failures[0].Name != "a" || report.Failures[1].Name != "b" || !reflect.DeepEqual(observed, want) || sourceReads != 2 || destReads != 2 {
				t.Fatalf("verification lost prior facts or crossed refusal: report=%+v observed=%v source=%d dest=%d err=%v", report, observed, sourceReads, destReads, err)
			}
		})
	}
}

func TestEngineRetryRequiresSavedRecoveryFacts(t *testing.T) {
	w := newWorld(t)
	seed(w.srcMock, "media", "object", []byte("copy bytes"), nil)
	engine := &statusEngine{inner: &fakeEngine{w: w, interruptAfter: -1}, canned: []*rclone.Result{{
		ExitCode: 1, Errors: []string{"401 Unauthorized"},
		SegmentPrefixes: []rclone.SegmentPrefix{{Container: "backup_segments", Prefix: "captured/"}},
	}}}
	e := recordingExecutor(t, w, engine, &Recorder{
		Admit: func(Admission) error { return nil }, Execute: func(string, Observation) error { return nil }, Verify: func(string, Observation) error { return nil },
	})
	refusal := errors.New("fixture captured-prefix record refusal")
	e.cfg.OnRecovery = func(kind, _, _, _ string) error {
		if kind != "captured" {
			t.Errorf("later recovery action crossed failed capture: %q", kind)
		}
		return refusal
	}
	requests := len(w.dstMock.RequestLog())
	report, err := e.Run(t.Context(), []plan.Item{{Name: "object", Verdict: plan.VerdictNew, Route: plan.RouteRclone, SourceBytes: 10}})
	if !errors.Is(err, refusal) || report.Copied != 0 || engine.calls != 1 || len(w.dstMock.RequestLog()) != requests {
		t.Fatalf("failed capture permitted a fresh dependent child: calls=%d report=%+v err=%v", engine.calls, report, err)
	}
	if err := e.Handle(t.Context(), plan.Item{Name: "later", Verdict: plan.VerdictNew, Route: plan.RouteNative}); !errors.Is(err, refusal) {
		t.Fatalf("failed capture allowed later native work: %v", err)
	}
	if _, err := e.Finish(t.Context()); !errors.Is(err, refusal) || engine.calls != 1 || len(w.dstMock.RequestLog()) != requests {
		t.Fatalf("later Finish forgot failed recovery recording: %v", err)
	}
}

func TestNativeSymlinkConflictHasNoInvokedMutation(t *testing.T) {
	w := newWorld(t)
	seed(w.srcMock, "media", "target", []byte("copy bytes"), nil)
	w.srcMock.PutObject("media", "link", swifttest.Object{SymlinkTarget: "media/target"})
	seed(w.dstMock, "backup", "link", []byte("existing destination"), nil)
	var observations []Observation
	e := recordingExecutor(t, w, nil, &Recorder{Admit: func(Admission) error { return nil },
		Execute: func(_ string, o Observation) error { observations = append(observations, o); return nil }, Verify: func(string, Observation) error { return nil }})
	report, err := e.Run(t.Context(), []plan.Item{{Name: "link", Verdict: plan.VerdictNew, Route: plan.RouteNative}})
	want := Observation{Result: ResultConflict, Effect: EffectNotAttempted, Diagnostic: Diagnostic{Stage: StageCommit, Code: CodeConflict}}
	if err != nil || report.Copied != 0 || !reflect.DeepEqual(observations, []Observation{want}) || headDst(t, w, "link").ContentLength != 20 {
		t.Fatalf("pre-PUT symlink conflict lost effect boundary: %+v %+v %v", report, observations, err)
	}
	for _, request := range w.dstMock.RequestLog() {
		if strings.HasPrefix(request, "PUT ") || strings.HasPrefix(request, "POST ") || strings.HasPrefix(request, "DELETE ") {
			t.Fatalf("pre-PUT conflict invoked a mutation: %+v", request)
		}
	}
}

func TestCheckedVerificationVerdicts(t *testing.T) {
	w := newWorld(t)
	seed(w.srcMock, "media", "equal", []byte("equal"), nil)
	seed(w.dstMock, "backup", "equal", []byte("equal"), nil)
	seed(w.srcMock, "media", "unproven", []byte("new"), nil)
	seed(w.dstMock, "backup", "unproven", []byte("old"), nil)
	e := newExecutor(t, w, nil)
	for _, tc := range []struct {
		name    string
		verdict plan.Verdict
		code    Code
		status  int
	}{
		{"equal", plan.VerdictSame, "", 0}, {"equal", plan.VerdictUnknown, "", 0},
		{"unproven", plan.VerdictUnknown, CodeUnverified, 0}, {"missing", plan.VerdictUnknown, CodeMissing, 404},
		{"new", plan.VerdictNew, CodeUnverified, 0}, {"changed", plan.VerdictChanged, CodeUnverified, 0},
	} {
		t.Run(tc.name+"/"+string(tc.verdict), func(t *testing.T) {
			o, err := e.verifyItem(t.Context(), plan.Item{Name: tc.name, Verdict: tc.verdict})
			want := Observation{Result: ResultVerified, Effect: EffectNotAttempted}
			if tc.code != "" {
				want.Result = ResultFailed
				want.Diagnostic = Diagnostic{Stage: StageVerification, Code: tc.code, HTTPStatus: tc.status}
			}
			if o != want || (err != nil) != (tc.code != "") {
				t.Fatalf("verification misrepresented equality proof: %+v want=%+v %v", o, want, err)
			}
		})
	}
}

func TestPlanningStopPreservesFinishFailure(t *testing.T) {
	w := newWorld(t)
	engine := &recordingEngine{inner: &fakeEngine{w: w, interruptAfter: -1}}
	planningFailure, recordingFailure := errors.New("fixture listing refused"), errors.New("fixture buffered outcome refused")
	e := recordingExecutor(t, w, engine, &Recorder{Admit: func(Admission) error { return nil },
		Execute: func(key string, o Observation) error {
			if key != "buffered" || o.Result != ResultNotAttempted || o.Effect != EffectNotAttempted {
				t.Fatalf("stopped plan invented an attempted effect: %q %+v", key, o)
			}
			return recordingFailure
		}, Verify: func(string, Observation) error { return nil }})
	if err := e.Handle(t.Context(), plan.Item{Name: "buffered", Verdict: plan.VerdictNew, Route: plan.RouteRclone}); err != nil {
		t.Fatal(err)
	}
	e.Stop(planningFailure)
	beforeSrc, beforeDst := len(w.srcMock.RequestLog()), len(w.dstMock.RequestLog())
	for range 2 {
		report, err := e.Finish(t.Context())
		if !errors.Is(err, planningFailure) || !errors.Is(err, recordingFailure) || report.Copied != 0 || len(engine.dsts) != 0 ||
			len(w.srcMock.RequestLog()) != beforeSrc || len(w.dstMock.RequestLog()) != beforeDst {
			t.Fatalf("Finish hid a simultaneous planning/recording error or dispatched work: %+v %v", report, err)
		}
	}
}
