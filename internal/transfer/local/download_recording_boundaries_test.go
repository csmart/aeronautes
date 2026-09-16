package local

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	transfercopy "github.com/csmart/aeronautes/internal/transfer/copy"
	"github.com/csmart/aeronautes/internal/transfer/rclone"
)

func TestDownloadRequiresCompleteRecorder(t *testing.T) {
	for _, missing := range []string{"nil", "admit", "execute", "verify", "extra"} {
		t.Run(missing, func(t *testing.T) {
			root := t.TempDir()
			calls := 0
			recorder := &DownloadRecorder{
				Admit:   func(DownloadAdmission) error { calls++; return nil },
				Execute: func(DownloadItem, transfercopy.Observation) error { calls++; return nil },
				Verify:  func(DownloadItem, transfercopy.Observation) error { calls++; return nil },
			}
			switch missing {
			case "nil":
				recorder = nil
			case "admit":
				recorder.Admit = nil
			case "execute":
				recorder.Execute = nil
			case "verify":
				recorder.Verify = nil
			}
			recorders := []*DownloadRecorder{recorder}
			if missing == "extra" {
				recorders = append(recorders, recorder)
			}
			plan := &DownloadPreflight{Items: []DownloadItem{{Name: "scope/marker/", RelPath: "marker", Dir: true}}}
			report, err := Download(t.Context(), nil, nil, "", "media", "scope/", root, "fixture", plan, nil, recorders...)
			if err == nil || report != nil || calls != 0 {
				t.Fatalf("partial recorder admitted dependent work: report=%+v calls=%d err=%v", report, calls, err)
			}
			if entries, err := os.ReadDir(root); err != nil || len(entries) != 0 {
				t.Fatalf("partial recorder changed the destination: %v %v", entries, err)
			}
		})
	}
}

func TestDownloadNativeCancellationBetweenItems(t *testing.T) {
	for _, cancelAfterFirst := range []bool{false, true} {
		t.Run(fmt.Sprint(cancelAfterFirst), func(t *testing.T) {
			source, mock := session(t)
			for _, name := range []string{"a", "b"} {
				seed(mock, "media", "scope\n/"+name, "bytes "+name)
			}
			root := t.TempDir()
			writeFile(t, root, "keep", "unrelated")
			plan, err := PreflightDownload(t.Context(), source, "media", "scope\n/", root)
			if err != nil || plan.Rejected() || len(plan.Items) != 2 {
				t.Fatalf("cancellation fixture preflight: %+v %v", plan, err)
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			admitted, executed, verified := 0, 0, 0
			recorder := &DownloadRecorder{
				Admit: func(DownloadAdmission) error { admitted++; return nil },
				Execute: func(item DownloadItem, observation transfercopy.Observation) error {
					executed++
					if observation.Result != transfercopy.ResultCopied || observation.CopiedBytes != 7 {
						t.Fatalf("native cancellation lost placed content: %+v", observation)
					}
					if cancelAfterFirst {
						cancel()
					}
					return nil
				},
				Verify: func(DownloadItem, transfercopy.Observation) error {
					verified++
					return nil
				},
			}
			beforeRequests := mock.Requests()
			report, err := Download(ctx, source, nil, "", "media", "scope\n/", root, "fixture", plan, nil, recorder)
			want := 2
			if cancelAfterFirst {
				want = 1
			}
			if errors.Is(err, context.Canceled) != cancelAfterFirst || !cancelAfterFirst && err != nil ||
				admitted != want || executed != want || verified != want || report.Done != int64(want) ||
				report.Bytes != int64(want*7) || report.StageCleanup != nil || mock.Requests() != beforeRequests+want {
				t.Fatalf("known cancellation allowed later native work or erased earlier facts: %+v admit=%d execute=%d verify=%d requests=%d err=%v", report, admitted, executed, verified, mock.Requests()-beforeRequests, err)
			}
			for index, name := range []string{"a", "b"} {
				data, err := os.ReadFile(filepath.Join(root, name))
				if index < want && (err != nil || string(data) != "bytes "+name) || index >= want && !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("native cancellation changed its permitted final delta: %q %q %v", name, data, err)
				}
			}
			entries, err := os.ReadDir(root)
			if err != nil || len(entries) != want+1 {
				t.Fatalf("native cancellation retained an unreported temporary: %v %v", entries, err)
			}
			if data, err := os.ReadFile(filepath.Join(root, "keep")); err != nil || string(data) != "unrelated" {
				t.Fatal("native cancellation changed unrelated bytes")
			}
		})
	}
}

type partialDownloadEngine struct{ Engine }

func (e partialDownloadEngine) Copy(ctx context.Context, source, destination string, names []string, event func(rclone.Event)) (*rclone.Result, error) {
	if _, err := e.Engine.Copy(ctx, source, destination, names[:1], event); err != nil {
		return nil, err
	}
	if err := os.Truncate(filepath.Join(destination, names[0]), 3); err != nil {
		return nil, err
	}
	return &rclone.Result{ExitCode: 1}, nil
}

func TestDownloadRecordsPartialChildEffects(t *testing.T) {
	source, mock := session(t)
	for _, name := range []string{"a", "b"} {
		seed(mock, "media", "scope/"+name, "bytes "+name)
	}
	root := t.TempDir()
	writeFile(t, root, "keep", "unrelated")
	plan, err := PreflightDownload(t.Context(), source, "media", "scope/", root)
	if err != nil || plan.Rejected() || len(plan.Items) != 2 {
		t.Fatalf("partial child fixture preflight: %+v %v", plan, err)
	}
	admitted, executed, verified := 0, 0, 0
	recorder := &DownloadRecorder{
		Admit: func(DownloadAdmission) error { admitted++; return nil },
		Execute: func(_ DownloadItem, observation transfercopy.Observation) error {
			executed++
			if observation.Result != transfercopy.ResultFailed || observation.Effect != transfercopy.EffectUnresolved || observation.CopiedBytes != 0 {
				t.Fatalf("partial child invented placed or absent effects: %+v", observation)
			}
			return nil
		},
		Verify: func(DownloadItem, transfercopy.Observation) error { verified++; return nil },
	}
	beforeRequests := mock.Requests()
	report, err := Download(t.Context(), source, partialDownloadEngine{&fsEngine{session: source}}, "remote:media", "media", "scope/", root, "fixture", plan, nil, recorder)
	if err != nil || admitted != 2 || executed != 2 || verified != 0 || report.Done != 0 || report.Bytes != 0 || len(report.Failures) != 2 ||
		report.StageCleanup == nil || report.StageCleanup.Retained || report.StageCleanup.Issues != 0 || mock.Requests() != beforeRequests+1 {
		t.Fatalf("partial child lost uncertainty or invented verification: %+v admit=%d execute=%d verify=%d err=%v", report, admitted, executed, verified, err)
	}
	stage := filepath.Join(root, ".aeronautes-stage-fixture")
	// The partial bytes the child wrote are this job's own scratch and go
	// with the stage when the job ends (ADR 0048). The uncertainty they
	// carried is preserved in the records, which is where it belongs.
	for _, path := range []string{stage, filepath.Join(stage, "a"), filepath.Join(stage, "b"), filepath.Join(root, "a"), filepath.Join(root, "b")} {
		if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("partial child created an unwitnessed final or staged file: %q %v", path, err)
		}
	}
	if data, err := os.ReadFile(filepath.Join(root, "keep")); err != nil || string(data) != "unrelated" {
		t.Fatal("partial child changed unrelated bytes")
	}
}
