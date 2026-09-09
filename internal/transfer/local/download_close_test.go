package local

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type downloadCloseReader struct {
	io.Reader
	close func() error
}

func (r downloadCloseReader) Close() error { return r.close() }

type downloadCloseFile struct {
	io.Writer
	sync  func() error
	close func() error
}

func (f downloadCloseFile) Sync() error  { return f.sync() }
func (f downloadCloseFile) Close() error { return f.close() }

type downloadReadFailure struct{ err error }

func (r downloadReadFailure) Read(p []byte) (int, error) {
	return copy(p, "partial"), r.err
}

func TestNativeDownloadClosure(t *testing.T) {
	readErr, syncErr := errors.New("read refused"), errors.New("sync refused")
	fileErr, sourceErr := errors.New("file close refused"), errors.New("source close refused")
	for _, fault := range []string{"control", "read", "length", "sync", "file_close", "source_close", "all_closes", "read_and_closes", "target_exists"} {
		t.Run(fault, func(t *testing.T) {
			dir := t.TempDir()
			target := filepath.Join(dir, "final")
			if fault == "target_exists" {
				if err := os.WriteFile(target, []byte("other writer"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			temp, err := os.CreateTemp(dir, ".aeronautes-part-*")
			if err != nil {
				t.Fatal(err)
			}
			checkBeforeLink := func() {
				t.Helper()
				if fault != "target_exists" {
					if _, err := os.Lstat(target); !errors.Is(err, os.ErrNotExist) {
						t.Fatalf("final name appeared before closure: %v", err)
					}
				}
			}
			var expected []error
			var reader io.Reader = strings.NewReader("complete bytes")
			if fault == "read" || fault == "read_and_closes" {
				reader = downloadReadFailure{readErr}
				expected = append(expected, readErr)
			}
			syncs, fileCloses, sourceCloses := 0, 0, 0
			file := downloadCloseFile{Writer: temp, sync: func() error {
				syncs++
				checkBeforeLink()
				if fault == "sync" {
					expected = append(expected, syncErr)
					return syncErr
				}
				return temp.Sync()
			}, close: func() error {
				fileCloses++
				checkBeforeLink()
				err := temp.Close()
				if fault == "file_close" || fault == "all_closes" || fault == "read_and_closes" {
					expected = append(expected, fileErr)
					return errors.Join(err, fileErr)
				}
				return err
			}}
			body := downloadCloseReader{Reader: reader, close: func() error {
				sourceCloses++
				checkBeforeLink()
				if fault == "source_close" || fault == "all_closes" || fault == "read_and_closes" {
					expected = append(expected, sourceErr)
					return sourceErr
				}
				return nil
			}}
			wantSize := int64(len("complete bytes"))
			if fault == "length" {
				wantSize++
			}
			placed, err := finishNativeDownload(body, file, temp.Name(), target, wantSize)
			if placed != (fault == "control") || (err == nil) != (fault == "control") {
				t.Fatalf("placement outcome: placed=%v error=%v", placed, err)
			}
			for _, cause := range expected {
				if !errors.Is(err, cause) {
					t.Errorf("lost error %v: %v", cause, err)
				}
			}
			if fileCloses != 1 || sourceCloses != 1 {
				t.Errorf("handles not closed exactly once: file=%d source=%d", fileCloses, sourceCloses)
			}
			if fault == "read" || fault == "read_and_closes" || fault == "length" {
				if syncs != 0 {
					t.Errorf("synced an unsuccessful read: %d", syncs)
				}
			} else if syncs != 1 {
				t.Errorf("required sync missing: %d", syncs)
			}
			if fault == "control" || fault == "target_exists" {
				want := "complete bytes"
				if fault == "target_exists" {
					want = "other writer"
				}
				data, err := os.ReadFile(target)
				if err != nil || string(data) != want {
					t.Errorf("final bytes changed: %q %v", data, err)
				}
			} else {
				checkBeforeLink()
			}
			if _, err := os.Lstat(temp.Name()); !errors.Is(err, os.ErrNotExist) {
				t.Errorf("temporary file retained without cleanup refusal: %v", err)
			}
		})
	}
}

func TestNativeDownloadCleanupRefusal(t *testing.T) {
	for _, primary := range []string{"none", "source_close", "link"} {
		t.Run(primary, func(t *testing.T) {
			dir := t.TempDir()
			staging, final := filepath.Join(dir, "temporary"), filepath.Join(dir, "final")
			if err := os.Mkdir(staging, 0o700); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := os.Chmod(staging, 0o700); err != nil {
					t.Error(err)
				}
			})
			temp, err := os.CreateTemp(staging, ".aeronautes-part-*")
			if err != nil {
				t.Fatal(err)
			}
			if primary == "link" {
				if err := os.WriteFile(final, []byte("other writer"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			refusal := errors.New("source close refused")
			body := downloadCloseReader{Reader: strings.NewReader("complete bytes"), close: func() error {
				if err := os.Chmod(staging, 0o500); err != nil {
					t.Fatal(err)
				}
				if primary == "source_close" {
					return refusal
				}
				return nil
			}}
			placed, err := finishNativeDownload(body, temp, temp.Name(), final, int64(len("complete bytes")))
			if placed != (primary == "none") || !errors.Is(err, os.ErrPermission) {
				t.Fatalf("cleanup refusal lacks actual placement/permission facts: %v %v", placed, err)
			}
			if primary == "source_close" && !errors.Is(err, refusal) || primary == "link" && !errors.Is(err, os.ErrExist) {
				t.Errorf("cleanup erased primary error: %v", err)
			}
			report := &Report{}
			report.nativeDownloaded(DownloadItem{Name: "selected", Size: int64(len("complete bytes"))}, placed, err)
			if placed {
				if report.Done != 1 || report.Bytes != int64(len("complete bytes")) || len(report.Failures) != 0 || report.PostCommitIssues != 1 || len(report.PostCommitIssueSamples) != 1 {
					t.Fatalf("cleanup hid witnessed copied bytes: %+v", report)
				}
				issue := report.PostCommitIssueSamples[0]
				if issue.Name != "selected" || issue.Stage != "temporary-cleanup" || issue.Err != err.Error() {
					t.Errorf("cleanup issue lost its identity or reason: %+v", issue)
				}
			} else if report.Done != 0 || report.Bytes != 0 || report.PostCommitIssues != 0 || len(report.Failures) != 1 || report.Failures[0].Name != "selected" || report.Failures[0].Err != err.Error() {
				t.Errorf("pre-placement failures changed accounting: %+v", report)
			}
			data, readErr := os.ReadFile(temp.Name())
			if readErr != nil || string(data) != "complete bytes" {
				t.Fatalf("retained temporary bytes changed: %q %v", data, readErr)
			}
			if placed {
				staged, err := os.Stat(temp.Name())
				landed, finalErr := os.Stat(final)
				if err != nil || finalErr != nil || !os.SameFile(staged, landed) {
					t.Fatalf("successful link not witnessed: %v %v", err, finalErr)
				}
			} else if primary == "link" {
				data, err := os.ReadFile(final)
				if err != nil || string(data) != "other writer" {
					t.Errorf("link refusal changed original: %q %v", data, err)
				}
			} else if _, err := os.Lstat(final); !errors.Is(err, os.ErrNotExist) {
				t.Errorf("source close refusal placed final file: %v", err)
			}
		})
	}
}
