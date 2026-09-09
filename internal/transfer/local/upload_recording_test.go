package local

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"reflect"
	"testing"

	"github.com/csmart/aeronautes/internal/systemtest/oracle"
	transfercopy "github.com/csmart/aeronautes/internal/transfer/copy"
	"github.com/csmart/aeronautes/internal/transfer/rclone"
)

type uploadRecordingEngine struct {
	calls int
	run   func(context.Context, string, string, []string, func(rclone.Event)) (*rclone.Result, error)
}

func (e *uploadRecordingEngine) Copy(ctx context.Context, src, dst string, names []string, events func(rclone.Event)) (*rclone.Result, error) {
	e.calls++
	return e.run(ctx, src, dst, names, events)
}

func TestUploadRecorderRequiresEveryAcknowledgement(t *testing.T) {
	for mask := 0; mask < 9; mask++ {
		t.Run(fmt.Sprint(mask), func(t *testing.T) {
			s, mock := session(t)
			path := writeFile(t, t.TempDir(), "source", "bytes")
			preflight, err := PreflightUpload(t.Context(), s, "media", "native\n/", []string{path})
			if err != nil || preflight.Rejected() {
				t.Fatalf("preflight: %+v %v", preflight, err)
			}
			recorder := &UploadRecorder{}
			if mask&1 != 0 {
				recorder.Admit = func(UploadAdmission) error { return nil }
			}
			if mask&2 != 0 {
				recorder.Execute = func(UploadItem, transfercopy.Observation) error { return nil }
			}
			if mask&4 != 0 {
				recorder.Verify = func(UploadItem, transfercopy.Observation) error { return nil }
			}
			recorders := []*UploadRecorder{recorder}
			if mask == 7 {
				recorders[0] = nil
			} else if mask == 8 {
				recorders = append(recorders, recorder)
			}
			witness := uploadWitness(t, mock)
			before := uploadSnapshot(t, witness)
			result, err := Upload(t.Context(), s, nil, "aeronautes_dst:media", "media", "native\n/", "partial-recorder", preflight, nil, nil, recorders...)
			if err == nil || result != nil || !reflect.DeepEqual(before, uploadSnapshot(t, witness)) {
				t.Fatalf("partial recorder admitted upload effects: %+v %v", result, err)
			}
		})
	}
}

type uploadCloseRefusal struct {
	io.ReadCloser
	err error
}

func (r uploadCloseRefusal) Close() error { return errors.Join(r.ReadCloser.Close(), r.err) }

func TestNativeUploadRetainsCopiedFactAfterCloseFailure(t *testing.T) {
	s, mock := session(t)
	seed(mock, "media", "keep", "remote sentinel")
	seed(mock, "outside", "keep", "outside sentinel")
	witness := uploadWitness(t, mock)
	before := uploadSnapshot(t, witness)
	path := writeFile(t, t.TempDir(), "source", "bytes")
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	refusal := errors.New("fixture source close refused")
	item := UploadItem{Path: path, Name: "native\n/source", Size: 5, Route: "native"}
	result := nativeUpload(t.Context(), s, "media", item, uploadCloseRefusal{ReadCloser: file, err: refusal})
	want := transfercopy.Observation{Result: transfercopy.ResultCopied, Effect: transfercopy.EffectUnresolved, CopiedBytes: 5,
		Diagnostic: transfercopy.Diagnostic{Stage: transfercopy.StageSourceRead, Code: transfercopy.CodeIO}}
	if result.Observation != want || result.failure != nil || len(result.issues) != 1 || !errors.Is(result.issues[0].err, refusal) ||
		result.verification == nil || result.verification.Result != transfercopy.ResultVerified {
		t.Fatalf("close failure erased copied bytes or its problem: %+v", result)
	}
	if _, err := file.Read(make([]byte, 1)); !errors.Is(err, os.ErrClosed) {
		t.Fatalf("native helper left its actual source file open: %v", err)
	}
	after := uploadSnapshot(t, witness)
	observed := after.Containers["media"].Objects[item.Name]
	if observed.Size != 5 || observed.Digest != fmt.Sprintf("%x", sha256.Sum256([]byte("bytes"))) || observed.Class != "plain" {
		t.Fatalf("copied fact has no owned effect: %+v", observed)
	}
	delete(after.Containers["media"].Objects, item.Name)
	if delta := oracle.Diff(before, after); !delta.Empty() {
		t.Fatalf("native close fault changed collateral: %s", delta)
	}
	if bytes, err := os.ReadFile(path); err != nil || string(bytes) != "bytes" {
		t.Fatalf("native close fault changed its source: %q %v", bytes, err)
	}
}

func TestUploadObservationFailureStopsNextSource(t *testing.T) {
	for _, phase := range []string{"execution", "verification", "control"} {
		t.Run(phase, func(t *testing.T) {
			s, mock := session(t)
			seed(mock, "media", "keep", "remote sentinel")
			seed(mock, "outside", "keep", "outside sentinel")
			witness := uploadWitness(t, mock)
			before := uploadSnapshot(t, witness)
			dir := t.TempDir()
			paths := []string{writeFile(t, dir, "first", "alpha"), writeFile(t, dir, "second", "bravo")}
			preflight, err := PreflightUpload(t.Context(), s, "media", "native\n/", paths)
			if err != nil || preflight.Rejected() {
				t.Fatalf("preflight: %+v %v", preflight, err)
			}
			refusal := errors.New("fixture observation refused")
			var admitted, executed, verified []string
			recorder := &UploadRecorder{Admit: func(a UploadAdmission) error {
				if a.StagingObject != "" {
					t.Error("native admission invented staging")
				}
				admitted = append(admitted, a.Item.Path)
				return nil
			}, Execute: func(item UploadItem, observation transfercopy.Observation) error {
				executed = append(executed, item.Path)
				if observation != (transfercopy.Observation{Result: transfercopy.ResultCopied, Effect: transfercopy.EffectReportedApplied, CopiedBytes: 5}) {
					t.Errorf("successful live HEAD lost: %+v", observation)
				}
				if phase == "execution" {
					return refusal
				}
				return nil
			}, Verify: func(item UploadItem, observation transfercopy.Observation) error {
				verified = append(verified, item.Path)
				if observation != (transfercopy.Observation{Result: transfercopy.ResultVerified, Effect: transfercopy.EffectNotAttempted}) {
					t.Errorf("verification fact changed: %+v", observation)
				}
				if phase == "verification" {
					return refusal
				}
				return nil
			}}
			report, err := Upload(t.Context(), s, nil, "aeronautes_dst:media", "media", "native\n/", "record-stop", preflight, nil, nil, recorder)
			want, wantVerified := 1, 1
			if phase == "control" {
				want, wantVerified = 2, 2
			} else if phase == "execution" {
				wantVerified = 0
			}
			if (phase == "control" && err != nil) || (phase != "control" && !errors.Is(err, refusal)) || report.Done != int64(want) || report.Bytes != int64(want*5) ||
				!reflect.DeepEqual(admitted, paths[:want]) || !reflect.DeepEqual(executed, paths[:want]) || len(verified) != wantVerified {
				t.Fatalf("observation refusal crossed a dependent boundary: report=%+v error=%v admissions=%v execution=%v verification=%v", report, err, admitted, executed, verified)
			}
			after := uploadSnapshot(t, witness)
			for i, name := range []string{"first", "second"}[:want] {
				object := after.Containers["media"].Objects["native\n/"+name]
				body := []string{"alpha", "bravo"}[i]
				if object.Size != 5 || object.Digest != fmt.Sprintf("%x", sha256.Sum256([]byte(body))) || object.Class != "plain" {
					t.Fatalf("observations differ from owned bytes: %+v", object)
				}
				delete(after.Containers["media"].Objects, "native\n/"+name)
			}
			if delta := oracle.Diff(before, after); !delta.Empty() {
				t.Fatalf("observation refusal changed collateral: %s", delta)
			}
			for i, path := range paths {
				if bytes, err := os.ReadFile(path); err != nil || string(bytes) != []string{"alpha", "bravo"}[i] {
					t.Fatalf("observation refusal changed local bytes: %q %v", bytes, err)
				}
			}
		})
	}
}
