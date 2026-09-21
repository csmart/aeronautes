package local

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	transfercopy "github.com/csmart/aeronautes/internal/transfer/copy"
)

func hasOpenDownloadIdentity(t *testing.T, identity os.FileInfo) bool {
	t.Helper()
	entries, err := os.ReadDir("/proc/self/fd")
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		info, err := os.Stat(filepath.Join("/proc/self/fd", entry.Name()))
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			t.Fatal(err)
		}
		if err == nil && os.SameFile(info, identity) {
			return true
		}
	}
	return false
}

func TestNativeDownloadPublicationPinsIdentity(t *testing.T) {
	dir := t.TempDir()
	temp, err := os.CreateTemp(dir, ".aeronautes-part-*")
	if err != nil {
		t.Fatal(err)
	}
	identity, err := temp.Stat()
	if err != nil {
		t.Fatal(err)
	}
	destination := testDestination(t, dir)
	item := DownloadItem{Name: "scope/file", RelPath: "final", Size: 5, Route: "native"}
	outcome := finishNativeDownload(t.Context(), io.NopCloser(strings.NewReader("bytes")), temp,
		testDirectory(t, destination, "."),
		nativePublication(t, destination, ".", filepath.Base(temp.Name()), ".", "final"),
		5, ladderFor(t, []byte("bytes")))
	if outcome.Result != transfercopy.ResultCopied {
		t.Fatalf("matching download failed: %+v", outcome)
	}
	run := &downloadRun{report: &Report{}}
	t.Cleanup(func() {
		if err := run.closeNativePublications(); err != nil {
			t.Error(err)
		}
	})
	if err := run.observe(item, outcome); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(dir, "final")); err != nil {
		t.Fatal(err)
	}
	// A reference must survive both unlinks; otherwise a recycled inode
	// number can make the final identity comparison accept a substitute.
	if !hasOpenDownloadIdentity(t, identity) {
		t.Error("verified inode has no live descriptor before publication confirmation")
	}
	if err := run.confirmNativePublications(destination); err != nil {
		t.Fatal(err)
	}
	if hasOpenDownloadIdentity(t, identity) {
		t.Error("publication confirmation leaked the inode descriptor")
	}
	if run.report.Done != 0 || len(run.report.Failures) != 1 {
		t.Fatalf("removed publication stayed verified: %+v", run.report)
	}
}

func TestNativeDownloadPublicationPinsCloseOnExit(t *testing.T) {
	for _, phase := range []string{"control", "admission", "execution", "verification", "cancellation"} {
		t.Run(phase, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			s, mock := session(t)
			seed(mock, "media", "scope\n/first", "first")
			seed(mock, "media", "scope\n/second", "later")
			root := t.TempDir()
			preflight, err := PreflightDownload(t.Context(), s, "media", "scope\n/", root)
			if err != nil || preflight.Rejected() {
				t.Fatalf("preflight: %+v %v", preflight, err)
			}
			refused := errors.New("required recording refused")
			var published []os.FileInfo
			recorder := &DownloadRecorder{
				Admit: func(a DownloadAdmission) error {
					if phase == "admission" && strings.HasSuffix(a.Item.Name, "/second") {
						return refused
					}
					return nil
				},
				Execute: func(item DownloadItem, _ transfercopy.Observation) error {
					info, err := os.Stat(filepath.Join(root, item.RelPath))
					if err != nil {
						t.Fatal(err)
					}
					published = append(published, info)
					if phase == "execution" && len(published) == 2 {
						return refused
					}
					if phase == "cancellation" {
						cancel()
					}
					return nil
				},
				Verify: func(DownloadItem, transfercopy.Observation) error {
					if phase == "verification" {
						return refused
					}
					return nil
				},
			}
			_, err = Download(ctx, s, nil, "", "media", "scope\n/", root, "fixture", preflight, nil, recorder)
			wantErr := refused
			if phase == "control" {
				wantErr = nil
			} else if phase == "cancellation" {
				wantErr = context.Canceled
			}
			if !errors.Is(err, wantErr) || len(published) == 0 {
				t.Fatalf("fixture missed exit after publication: %v %d", err, len(published))
			}
			for _, identity := range published {
				if hasOpenDownloadIdentity(t, identity) {
					t.Error("job exit leaked a publication descriptor")
				}
			}
		})
	}
}

func TestNativeDownloadPublicationPinsCloseWithoutPublication(t *testing.T) {
	for _, fault := range []string{"source_close", "writer_close", "cancelled", "target_exists"} {
		t.Run(fault, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			dir := t.TempDir()
			temp, err := os.CreateTemp(dir, ".aeronautes-part-*")
			if err != nil {
				t.Fatal(err)
			}
			identity, err := temp.Stat()
			if err != nil {
				t.Fatal(err)
			}
			refused := errors.New("close refused")
			file := downloadCloseFile{Writer: temp, sync: temp.Sync, stat: temp.Stat, close: func() error {
				err := temp.Close()
				if fault == "writer_close" {
					return errors.Join(err, refused)
				}
				return err
			}}
			body := downloadCloseReader{Reader: strings.NewReader("bytes"), close: func() error {
				if !hasOpenDownloadIdentity(t, identity) {
					t.Error("identity was not pinned before the writer closed")
				}
				if fault == "cancelled" {
					cancel()
				}
				if fault == "source_close" {
					return refused
				}
				return nil
			}}
			if fault == "target_exists" {
				if err := os.WriteFile(filepath.Join(dir, "final"), []byte("occupant"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			destination := testDestination(t, dir)
			outcome := finishNativeDownload(ctx, body, file, testDirectory(t, destination, "."),
				nativePublication(t, destination, ".", filepath.Base(temp.Name()), ".", "final"),
				5, ladderFor(t, []byte("bytes")))
			closeDownloadPublication(t, outcome)
			if outcome.Result == transfercopy.ResultCopied || outcome.err() == nil {
				t.Fatalf("fixture did not refuse publication: %+v", outcome)
			}
			if hasOpenDownloadIdentity(t, identity) {
				t.Error("failed publication leaked its inode descriptor")
			}
			if _, err := os.Stat(temp.Name()); !errors.Is(err, os.ErrNotExist) {
				t.Errorf("failed publication changed temporary cleanup: %v", err)
			}
		})
	}
}

func TestNativeDownloadPublicationPinRefusesSubstitution(t *testing.T) {
	dir := t.TempDir()
	temp, err := os.CreateTemp(dir, ".aeronautes-part-*")
	if err != nil {
		t.Fatal(err)
	}
	identity, err := temp.Stat()
	if err != nil {
		t.Fatal(err)
	}
	file := downloadCloseFile{Writer: temp, sync: temp.Sync, close: temp.Close, stat: func() (os.FileInfo, error) {
		info, err := temp.Stat()
		if err != nil {
			return nil, err
		}
		if err := os.Remove(temp.Name()); err != nil {
			return nil, err
		}
		if err := os.WriteFile(temp.Name(), []byte("occupant"), 0o600); err != nil {
			return nil, err
		}
		return info, nil
	}}
	destination := testDestination(t, dir)
	outcome := finishNativeDownload(t.Context(), io.NopCloser(strings.NewReader("bytes")), file,
		testDirectory(t, destination, "."),
		nativePublication(t, destination, ".", filepath.Base(temp.Name()), ".", "final"),
		5, ladderFor(t, []byte("bytes")))
	closeDownloadPublication(t, outcome)
	if outcome.Result == transfercopy.ResultCopied || outcome.Effect != transfercopy.EffectUnresolved || outcome.err() == nil {
		t.Fatalf("substituted temporary was not refused: %+v", outcome)
	}
	if hasOpenDownloadIdentity(t, identity) {
		t.Error("refused pin leaked the writer descriptor")
	}
	occupant, err := os.Stat(temp.Name())
	if err != nil {
		t.Fatal(err)
	}
	if hasOpenDownloadIdentity(t, occupant) {
		t.Error("refused pin leaked the substitute descriptor")
	}
	data, err := os.ReadFile(temp.Name())
	if err != nil || string(data) != "occupant" {
		t.Fatalf("substitution lost its bytes: %q %v", data, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "final")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("substitute reached publication: %v", err)
	}
}
