package copy

import (
	"fmt"
	"os"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/csmart/aeronautes/internal/swiftcontrol"
	"github.com/csmart/aeronautes/internal/swifttest"
	"github.com/csmart/aeronautes/internal/systemtest/oracle"
	"github.com/csmart/aeronautes/internal/transfer/plan"
)

func refusalSnapshot(t *testing.T, witness *oracle.Oracle, containers ...string) *oracle.AccountSnapshot {
	t.Helper()
	snapshot, err := witness.SnapshotContainers(containers...)
	if err != nil {
		t.Fatal(err)
	}
	return snapshot
}

func TestCopyRefusesExistingDestinationsBeforeStaging(t *testing.T) {
	for _, route := range []plan.Route{plan.RouteRclone, plan.RouteNative} {
		for _, class := range []string{"plain", "dlo", "slo", "symlink"} {
			for _, verdict := range []plan.Verdict{plan.VerdictChanged, plan.VerdictUnknown} {
				t.Run(fmt.Sprintf("%s/%s/%s", route, class, verdict), func(t *testing.T) {
					w := newWorld(t)
					name := "doc"
					if route == plan.RouteNative {
						name = "line\nname"
					}
					seed(w.srcMock, "media", name, []byte("NEW SOURCE CONTENT"), nil)
					seed(w.srcMock, "unrelated", "sentinel", []byte("source sentinel"), nil)
					seedRecoveryAliases(t, w.dstMock, "backup_segments", "shared/")
					switch class {
					case "plain":
						seed(w.dstMock, "backup", name, []byte("OLD"), nil)
					case "dlo":
						w.dstMock.PutObject("backup", name, swifttest.Object{Manifest: "backup_segments/shared/"})
					case "slo":
						w.dstMock.PutObject("backup", name, swifttest.Object{SLO: true, Bytes: 8, Data: []byte("LIVEDATA"),
							SLOManifestJSON: `[{"name":"/backup_segments/shared/00000000","bytes":8}]`})
					case "symlink":
						w.dstMock.PutObject("backup", name, swifttest.Object{SymlinkTarget: "unrelated/keep"})
					}
					srcWitness, dstWitness := copyOracle(t, w.srcMock), copyOracle(t, w.dstMock)
					srcBefore := refusalSnapshot(t, srcWitness, "media", "unrelated")
					dstBefore := recoverySnapshot(t, dstWitness)
					engine := &recordingEngine{inner: &fakeEngine{w: w, interruptAfter: -1}}
					executor := newExecutor(t, w, engine)
					var outcomes []ItemOutcome
					executor.cfg.OnItem = func(outcome ItemOutcome) { outcomes = append(outcomes, outcome) }
					requests := []int{len(w.srcMock.RequestLog()), len(w.dstMock.RequestLog())}
					report, err := executor.Run(t.Context(), []plan.Item{{Name: name, Verdict: verdict, Route: route}})
					if err != nil || report.Copied != 0 || report.Overwrites != 0 || report.OverwritesRefused != 1 || report.BytesCopied != 0 || len(report.Failures) != 0 || len(report.Conflicts) != 0 {
						t.Errorf("refused item counted as execution: %+v, %v", report, err)
					}
					if len(outcomes) != 1 || outcomes[0].Name != name || outcomes[0].Outcome != "overwrite-refused" || outcomes[0].Detail == "" {
						t.Errorf("missing explicit overwrite refusal: %+v", outcomes)
					}
					if len(engine.dsts) != 0 || len(report.CapturedPrefixes) != 0 || len(report.StagedResidue) != 0 {
						t.Errorf("refused overwrite staged data: engine=%v report=%+v", engine.dsts, report)
					}
					if got := []int{len(w.srcMock.RequestLog()), len(w.dstMock.RequestLog())}; verdict == plan.VerdictChanged && !reflect.DeepEqual(got, requests) {
						t.Errorf("changed item contacted Swift before refusal: before=%v after=%v", requests, got)
					}
					if diff := oracle.Diff(srcBefore, refusalSnapshot(t, srcWitness, "media", "unrelated")); !diff.Empty() {
						t.Errorf("refused overwrite changed source data: %s", diff)
					}
					if diff := oracle.Diff(dstBefore, recoverySnapshot(t, dstWitness)); !diff.Empty() {
						t.Errorf("refused overwrite changed destination or outside data: %s", diff)
					}
					verified, err := executor.Verify(t.Context(),
						w.src.Objects(t.Context(), "media", swiftcontrol.ObjectListOptions{Deep: true}),
						w.dst.Objects(t.Context(), "backup", swiftcontrol.ObjectListOptions{Deep: true}))
					if err != nil || verified == nil || verified.Verified != 0 || len(verified.Failures) != 0 {
						t.Errorf("refused name entered copy verification: %+v, %v", verified, err)
					}
				})
			}
		}
	}
}

func verifyRefusalFixture(t *testing.T, w *world, executor *Executor) (*VerifyReport, error) {
	t.Helper()
	return executor.Verify(t.Context(),
		w.src.Objects(t.Context(), "media", swiftcontrol.ObjectListOptions{Deep: true, Prefix: executor.cfg.SrcPrefix}),
		w.dst.Objects(t.Context(), "backup", swiftcontrol.ObjectListOptions{Deep: true, Prefix: executor.cfg.DstPrefix}))
}

func TestRefusalVolumeCannotHideCopiedMismatch(t *testing.T) {
	w := newWorld(t)
	executor := newExecutor(t, w, &fakeEngine{w: w, interruptAfter: -1})
	executor.cfg.SrcPrefix, executor.cfg.DstPrefix = "source/", "destination/"
	names := []string{"", "a\nname", "a%2Fname", "a/name"}
	for n := range maxFailures + 57 {
		names = append(names, fmt.Sprintf("refused-%04d", n))
	}
	for _, name := range names {
		seed(w.srcMock, "media", "source/"+name, []byte("new source"), nil)
		seed(w.dstMock, "backup", "destination/"+name, []byte("old destination"), nil)
	}
	seed(w.srcMock, "media", "source/equal", []byte("equal"), nil)
	seed(w.dstMock, "backup", "destination/equal", []byte("equal"), nil)
	for _, name := range []string{"z-good", "z-mismatch"} {
		seed(w.srcMock, "media", "source/"+name, []byte("copy me"), nil)
	}
	_, err := plan.Copy(t.Context(),
		w.src.Objects(t.Context(), "media", swiftcontrol.ObjectListOptions{Deep: true, Prefix: "source/"}),
		w.dst.Objects(t.Context(), "backup", swiftcontrol.ObjectListOptions{Deep: true, Prefix: "destination/"}),
		"source/", "destination/", func(item plan.Item) error { return executor.Handle(t.Context(), item) })
	if err != nil {
		t.Fatal(err)
	}
	report, err := executor.Finish(t.Context())
	if err != nil || report.OverwritesRefused != int64(len(names)) || report.Overwrites != 0 || report.Copied != 2 || report.BytesCopied != 14 || len(report.Failures) != 0 {
		t.Fatalf("mixed execution report: %+v, %v", report, err)
	}
	// A fresh Changed row is not necessarily a refusal: this name really
	// copied. Conversely, later equality cannot turn a refusal into a copy.
	seed(w.dstMock, "backup", "destination/z-mismatch", []byte("outside writer"), nil)
	seed(w.srcMock, "media", "source/refused-0000", []byte("old destination"), nil)
	verified, err := verifyRefusalFixture(t, w, executor)
	if err != nil || verified == nil || verified.Verified != 2 || len(verified.Failures) != 1 || verified.Failures[0].Name != "z-mismatch" {
		t.Fatalf("refusals hid the copied mismatch or entered verification: %+v, %v", verified, err)
	}
	info, err := executor.refusals.file.Stat()
	if err != nil || info.Size() == 0 || (runtime.GOOS != "windows" && info.Mode().Perm() != 0o600) {
		t.Fatalf("refusal record is not private: %+v, %v", info, err)
	}
}

func TestVerificationRefusesDamagedRefusalRecords(t *testing.T) {
	for _, damage := range []string{"missing-tail", "extra-bytes", "substituted-name", "oversized-length", "closed", "count-mismatch", "write-failed", "out-of-order"} {
		t.Run(damage, func(t *testing.T) {
			w := newWorld(t)
			executor := newExecutor(t, w, nil)
			for _, name := range []string{"a", "b"} {
				seed(w.srcMock, "media", name, []byte("new"), nil)
				seed(w.dstMock, "backup", name, []byte("old"), nil)
				if err := executor.Handle(t.Context(), plan.Item{Name: name, Verdict: plan.VerdictChanged}); err != nil {
					t.Fatal(err)
				}
			}
			file := executor.refusals.file
			switch damage {
			case "missing-tail":
				if err := file.Truncate(5); err != nil {
					t.Fatal(err)
				}
			case "extra-bytes":
				if _, err := file.Write([]byte{0}); err != nil {
					t.Fatal(err)
				}
			case "substituted-name":
				if _, err := file.WriteAt([]byte("c"), 9); err != nil {
					t.Fatal(err)
				}
			case "oversized-length":
				if _, err := file.WriteAt([]byte{0xff, 0xff, 0xff, 0xff}, 0); err != nil {
					t.Fatal(err)
				}
			case "closed", "write-failed":
				if err := file.Close(); err != nil {
					t.Fatal(err)
				}
				if damage == "write-failed" {
					if err := executor.Handle(t.Context(), plan.Item{Name: "c", Verdict: plan.VerdictChanged}); err == nil {
						t.Fatal("lost refusal write allowed execution to continue")
					}
				}
				// Repair only the descriptor after the assertion below so
				// normal fixture cleanup can close and remove its own file.
				defer func() {
					reopened, err := os.OpenFile(file.Name(), os.O_RDWR, 0)
					if err != nil {
						t.Fatal(err)
					}
					executor.refusals.file = reopened
				}()
			case "count-mismatch":
				executor.report.OverwritesRefused++
			case "out-of-order":
				if err := executor.Handle(t.Context(), plan.Item{Name: "a", Verdict: plan.VerdictChanged}); err == nil {
					t.Fatal("unordered refusal decisions were accepted")
				}
			}
			verified, err := verifyRefusalFixture(t, w, executor)
			wantFailures := 0
			if damage == "substituted-name" {
				wantFailures = 1
			}
			if err == nil || verified == nil || verified.Verified != 0 || len(verified.Failures) != wantFailures {
				t.Fatalf("damaged refusal record lost its error or partial observations: %+v, %v", verified, err)
			}
			if wantFailures == 1 && verified.Failures[0].Name != "b" {
				t.Fatalf("partial verification changed the observed identity: %+v", verified)
			}
		})
	}
}

func TestRefusalRecordErrorsStayVisible(t *testing.T) {
	w := newWorld(t)
	executor := newExecutor(t, w, nil)
	if err := executor.Handle(t.Context(), plan.Item{Name: strings.Repeat("n", refusalNameLimit+1), Verdict: plan.VerdictChanged}); err == nil {
		t.Fatal("unbounded refusal name was accepted")
	}
	if report, err := executor.Finish(t.Context()); err == nil || report.OverwritesRefused != 1 || len(report.Failures) != 0 {
		t.Fatalf("record failure lost the refused item: %+v, %v", report, err)
	}
	if verified, err := verifyRefusalFixture(t, w, executor); err == nil || verified == nil || verified.Verified != 0 || len(verified.Failures) != 0 {
		t.Fatalf("failed refusal persistence was hidden: %+v, %v", verified, err)
	}
}

func TestVerificationChecksRefusalsAbsentFromFreshScope(t *testing.T) {
	for _, corrupt := range []bool{false, true} {
		t.Run(fmt.Sprintf("corrupt=%t", corrupt), func(t *testing.T) {
			w := newWorld(t)
			executor := newExecutor(t, w, nil)
			for _, name := range []string{"a", "b"} {
				if err := executor.Handle(t.Context(), plan.Item{Name: name, Verdict: plan.VerdictChanged}); err != nil {
					t.Fatal(err)
				}
			}
			if corrupt {
				if _, err := executor.refusals.file.WriteAt([]byte("c"), 9); err != nil {
					t.Fatal(err)
				}
			}
			// Neither fresh scope contains the original names. Verification
			// must still validate their complete execution-time record.
			report, err := verifyRefusalFixture(t, w, executor)
			if corrupt {
				if err == nil || report == nil || report.Verified != 0 || len(report.Failures) != 0 {
					t.Fatalf("absent names concealed a corrupt refusal record: %+v, %v", report, err)
				}
			} else if err != nil || report == nil || report.Verified != 0 || len(report.Failures) != 0 {
				t.Fatalf("absent names counted as verification: %+v, %v", report, err)
			}
		})
	}
}

func TestRefusalRecordClosesAfterVerification(t *testing.T) {
	w := newWorld(t)
	executor := newExecutor(t, w, nil)
	if err := executor.Handle(t.Context(), plan.Item{Name: "a", Verdict: plan.VerdictChanged}); err != nil {
		t.Fatal(err)
	}
	name := executor.refusals.file.Name()
	if err := executor.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(name); !os.IsNotExist(err) {
		t.Fatalf("closed executor left its local refusal record: %v", err)
	}
	if report, err := verifyRefusalFixture(t, w, executor); err == nil || report == nil || report.Verified != 0 || len(report.Failures) != 0 {
		t.Fatalf("unavailable refusal record produced verification: %+v, %v", report, err)
	}
}
