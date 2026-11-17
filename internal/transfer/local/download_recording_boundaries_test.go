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
			report, err := Download(t.Context(), nil, nil, "", "media", "scope/", root, plan, nil, recorders...)
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
			report, err := Download(ctx, source, nil, "", "media", "scope\n/", root, plan, nil, recorder)
			want := 2
			if cancelAfterFirst {
				want = 1
			}
			if errors.Is(err, context.Canceled) != cancelAfterFirst || !cancelAfterFirst && err != nil ||
				admitted != want || executed != want || verified != want || report.Done != int64(want) ||
				report.Bytes != int64(want*7) || mock.Requests() != beforeRequests+want {
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

// A child that fails part way does not taint the files it reported: each is
// downloaded, and a file it said nothing about is unknown (ADR 0122 decision
// 5).
func TestDownloadRecordsPartialChildEffects(t *testing.T) {
	source, mock := session(t)
	for _, name := range []string{"a", "b"} {
		seed(mock, "media", "scope/"+name, "bytes "+name)
	}
	root := t.TempDir()
	plan, err := PreflightDownload(t.Context(), source, "media", "scope/", root)
	if err != nil || plan.Rejected() || len(plan.Items) != 2 {
		t.Fatalf("partial child fixture preflight: %+v %v", plan, err)
	}
	engine := &scriptedEngine{run: func(ctx context.Context, src, dst string, names []string, onEvent func(rclone.Event)) (*rclone.Result, error) {
		if _, err := (&fsEngine{source}).Copy(ctx, src, dst, names[:1], onEvent); err != nil {
			return nil, err
		}
		return &rclone.Result{ExitCode: 1}, nil
	}}
	var kept downloadRecords
	report, err := Download(t.Context(), source, engine, "remote:media", "media", "scope/", root, plan, nil, kept.recorder())
	if err != nil || report.Done != 1 || len(report.Failures) != 1 || !report.EngineStopped {
		t.Fatalf("report = %+v, %v", report, err)
	}
	if got := kept.executed["scope/a"]; got.Result != transfercopy.ResultCopied || !kept.verified["scope/a"] || readLocal(t, filepath.Join(root, "a")) != "bytes a" {
		t.Fatalf("the reported file was recorded %+v", got)
	}
	if got := kept.executed["scope/b"]; got.Result != transfercopy.ResultFailed || got.Effect != transfercopy.EffectUnresolved || got.Diagnostic.Code != transfercopy.CodeUnknown {
		t.Fatalf("the unreported file was recorded %+v, want unknown", got)
	}
}
