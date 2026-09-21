package local

import (
	"bytes"
	"context"
	"crypto/md5"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	transfercopy "github.com/csmart/aeronautes/internal/transfer/copy"
)

type downloadCloseReader struct {
	io.Reader
	close func() error
}

func (r downloadCloseReader) Close() error { return r.close() }

type downloadCloseFile struct {
	io.Writer
	sync  func() error
	stat  func() (os.FileInfo, error)
	close func() error
}

func (f downloadCloseFile) Sync() error                { return f.sync() }
func (f downloadCloseFile) Stat() (os.FileInfo, error) { return f.stat() }
func (f downloadCloseFile) Close() error               { return f.close() }

func closeDownloadPublication(t *testing.T, result downloadResult) {
	t.Helper()
	if result.published != nil {
		t.Cleanup(func() {
			if err := result.published.Close(); err != nil {
				t.Error(err)
			}
		})
	}
}

// nativePublication describes publishing the temporary beside its target
// under the held directory, both names relative to dir.
func nativePublication(t *testing.T, destination *DestinationRoot, stagedDir, temporary, targetDir, target string) publication {
	t.Helper()
	return publication{root: destination,
		stagedRoot: testDirectory(t, destination, stagedDir), stagedBase: temporary, stagedRel: filepath.Join(stagedDir, temporary), stagedDisplay: filepath.Join(destination.Path(), stagedDir, temporary),
		targetRoot: testDirectory(t, destination, targetDir), targetBase: target, targetRel: filepath.Join(targetDir, target), targetDisplay: filepath.Join(destination.Path(), targetDir, target)}
}

type downloadReadFailure struct{ err error }

func (r downloadReadFailure) Read(p []byte) (int, error) {
	return copy(p, "partial"), r.err
}

func TestNativeDownloadClosure(t *testing.T) {
	readErr, syncErr := errors.New("read refused"), errors.New("sync refused")
	fileErr, sourceErr := errors.New("file close refused"), errors.New("source close refused")
	for _, fault := range []string{"control", "read", "length", "over_length", "sync", "file_close", "source_close", "all_closes", "read_and_closes", "target_exists", "cancelled_before_link"} {
		t.Run(fault, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
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
			file := downloadCloseFile{Writer: temp, stat: temp.Stat, sync: func() error {
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
				if fault == "cancelled_before_link" {
					cancel()
					expected = append(expected, context.Canceled)
				}
				if fault == "source_close" || fault == "all_closes" || fault == "read_and_closes" {
					expected = append(expected, sourceErr)
					return sourceErr
				}
				return nil
			}}
			wantSize := int64(len("complete bytes"))
			if fault == "length" {
				wantSize++
			} else if fault == "over_length" {
				wantSize--
			}
			destination := testDestination(t, dir)
			outcome := finishNativeDownload(ctx, body, file, testDirectory(t, destination, "."), nativePublication(t, destination, ".", filepath.Base(temp.Name()), ".", "final"), wantSize, ladderFor(t, []byte("complete bytes")))
			closeDownloadPublication(t, outcome)
			placed, err := outcome.Result == transfercopy.ResultCopied, outcome.err()
			if placed != (fault == "control") || (err == nil) != (fault == "control") {
				t.Fatalf("placement outcome: placed=%v error=%v", placed, err)
			}
			wantVerification := transfercopy.ResultFailed
			if fault == "control" {
				wantVerification = transfercopy.ResultVerified
			} else if fault == "read" || fault == "target_exists" || fault == "cancelled_before_link" {
				wantVerification = ""
			}
			if wantVerification == "" && outcome.verification != nil || wantVerification != "" && (outcome.verification == nil || outcome.verification.Result != wantVerification) {
				t.Fatalf("native result invented or lost an attempted verification: %+v", outcome.verification)
			}
			for _, cause := range expected {
				if !errors.Is(err, cause) {
					t.Errorf("lost error %v: %v", cause, err)
				}
			}
			if fileCloses != 1 || sourceCloses != 1 {
				t.Errorf("handles not closed exactly once: file=%d source=%d", fileCloses, sourceCloses)
			}
			if fault == "read" || fault == "read_and_closes" || fault == "length" || fault == "over_length" {
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
			destination := testDestination(t, dir)
			outcome := finishNativeDownload(t.Context(), body, temp, testDirectory(t, destination, "temporary"), nativePublication(t, destination, "temporary", filepath.Base(temp.Name()), ".", "final"), int64(len("complete bytes")), ladderFor(t, []byte("complete bytes")))
			placed, err := outcome.Result == transfercopy.ResultCopied, outcome.err()
			if placed != (primary == "none") || !errors.Is(err, os.ErrPermission) {
				t.Fatalf("cleanup refusal lacks actual placement/permission facts: %v %v", placed, err)
			}
			if primary == "source_close" && !errors.Is(err, refusal) || primary == "link" && !errors.Is(err, os.ErrExist) {
				t.Errorf("cleanup erased primary error: %v", err)
			}
			report := &Report{}
			run := downloadRun{report: report}
			t.Cleanup(func() {
				if err := run.closeNativePublications(); err != nil {
					t.Error(err)
				}
			})
			if err := run.observe(DownloadItem{Name: "selected", Size: int64(len("complete bytes"))}, outcome); err != nil {
				t.Fatal(err)
			}
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

// ladderFor is the source content expectation these fixtures stand in
// for: the length and MD5 of the bytes the case is about. A nil body
// gives an empty expectation, which is what a case that never reaches
// the comparison needs.
func ladderFor(t *testing.T, body []byte) transfercopy.Ladder {
	t.Helper()
	if body == nil {
		return transfercopy.Ladder{}
	}
	sum := md5.Sum(body)
	ladder, err := transfercopy.PlainLadder(int64(len(body)), hex.EncodeToString(sum[:]))
	if err != nil {
		t.Fatal(err)
	}
	return ladder
}

// Every failing path must hand cleanup a real identity, so that cleanup
// refuses to unlink an entry another writer has put at the temporary's
// name. Protecting somebody else's file cannot depend on why this call
// failed, so the unequal-length and close-failure paths are checked
// alongside the same-size mismatch. Retention of our OWN bytes is the
// narrower promise: it applies to a comparison that failed on its own,
// and an actual close failure keeps the existing cleanup contract.
func TestMismatchedDownloadRetainsWhatItRejected(t *testing.T) {
	served := []byte("served bytes..")
	for _, c := range []struct {
		name        string
		expect      []byte
		substituted bool
		closeFails  bool
		retainsOwn  bool
	}{
		{name: "same_size_mismatch_retains_rejected_bytes", expect: bytes.Repeat([]byte("x"), len(served)), retainsOwn: true},
		{name: "same_size_mismatch_preserves_a_replacement", expect: bytes.Repeat([]byte("x"), len(served)), substituted: true},
		{name: "longer_expectation_preserves_a_replacement", expect: append(bytes.Repeat([]byte("x"), len(served)), 'x'), substituted: true},
		{name: "shorter_expectation_preserves_a_replacement", expect: bytes.Repeat([]byte("x"), len(served)-1), substituted: true},
		{name: "longer_expectation_with_close_failure_preserves_a_replacement", expect: append(bytes.Repeat([]byte("x"), len(served)), 'x'), substituted: true, closeFails: true},
	} {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			staging := filepath.Join(dir, "temporary")
			if err := os.MkdirAll(staging, 0o755); err != nil {
				t.Fatal(err)
			}
			temp, err := os.CreateTemp(staging, ".aeronautes-part-*")
			if err != nil {
				t.Fatal(err)
			}
			base := filepath.Base(temp.Name())
			replacement := []byte("another writer")
			closeRefusal := errors.New("source close refused")
			body := downloadCloseReader{Reader: bytes.NewReader(served), close: func() error {
				if c.substituted {
					// Another actor replaces the entry while the read ends.
					if err := os.Remove(temp.Name()); err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(temp.Name(), replacement, 0o600); err != nil {
						t.Fatal(err)
					}
				}
				if c.closeFails {
					return closeRefusal
				}
				return nil
			}}
			destination := testDestination(t, dir)
			outcome := finishNativeDownload(t.Context(), body, temp,
				testDirectory(t, destination, "temporary"),
				nativePublication(t, destination, "temporary", base, ".", "final"),
				int64(len(served)), ladderFor(t, c.expect))

			if outcome.Result == transfercopy.ResultCopied || outcome.err() == nil {
				t.Fatalf("a rejected download was accepted: %+v %v", outcome.Observation, outcome.err())
			}
			if _, err := os.Lstat(filepath.Join(dir, "final")); !os.IsNotExist(err) {
				t.Errorf("something was published despite the rejection: %v", err)
			}
			held, readErr := os.ReadFile(temp.Name())
			switch {
			case c.substituted:
				if readErr != nil || !bytes.Equal(held, replacement) {
					t.Fatalf("another writer's replacement was destroyed: err=%v content=%q", readErr, held)
				}
			case c.retainsOwn:
				if readErr != nil || !bytes.Equal(held, served) {
					t.Fatalf("the rejected bytes were not retained: err=%v content=%q", readErr, held)
				}
				if outcome.Effect != transfercopy.EffectUnresolved {
					t.Errorf("retained residue left effect %v, want unresolved", outcome.Effect)
				}
			}
		})
	}
}

// statFailingFile makes the identity acquisition fail while everything
// else about the temporary behaves normally. An identity that could not
// be established must retain, not fall back to "any regular file will
// do" — which is how another writer's replacement gets unlinked.
type statFailingFile struct {
	*os.File
	err error
}

func (f statFailingFile) Stat() (os.FileInfo, error) { return nil, f.err }

func TestUnknownTemporaryIdentityRetainsRatherThanUnlinks(t *testing.T) {
	for _, substituted := range []bool{false, true} {
		name := "retains_when_identity_is_unknown"
		if substituted {
			name = "preserves_a_replacement_when_identity_is_unknown"
		}
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			staging := filepath.Join(dir, "temporary")
			if err := os.MkdirAll(staging, 0o755); err != nil {
				t.Fatal(err)
			}
			temp, err := os.CreateTemp(staging, ".aeronautes-part-*")
			if err != nil {
				t.Fatal(err)
			}
			base := filepath.Base(temp.Name())
			served := []byte("served bytes..")
			replacement := []byte("another writer")
			statErr := errors.New("input/output error")
			body := downloadCloseReader{Reader: bytes.NewReader(served), close: func() error {
				if !substituted {
					return nil
				}
				if err := os.Remove(temp.Name()); err != nil {
					t.Fatal(err)
				}
				return os.WriteFile(temp.Name(), replacement, 0o600)
			}}
			destination := testDestination(t, dir)
			outcome := finishNativeDownload(t.Context(), body, statFailingFile{File: temp, err: statErr},
				testDirectory(t, destination, "temporary"),
				nativePublication(t, destination, "temporary", base, ".", "final"),
				int64(len(served)), ladderFor(t, served))

			if outcome.Result == transfercopy.ResultCopied || !errors.Is(outcome.err(), statErr) {
				t.Fatalf("a failed identity was not reported: %+v %v", outcome.Observation, outcome.err())
			}
			if outcome.Effect != transfercopy.EffectUnresolved {
				t.Errorf("unknown identity left effect %v, want unresolved residue", outcome.Effect)
			}
			if _, err := os.Lstat(filepath.Join(dir, "final")); !os.IsNotExist(err) {
				t.Errorf("something was published: %v", err)
			}
			held, readErr := os.ReadFile(temp.Name())
			want := served
			if substituted {
				want = replacement
			}
			if readErr != nil || !bytes.Equal(held, want) {
				t.Fatalf("the file at the temporary's name was destroyed: err=%v content=%q want %q", readErr, held, want)
			}
		})
	}
}
