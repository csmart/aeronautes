package app

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"syscall"
	"testing"

	"github.com/csmart/aeronautes/internal/deletion"
)

func deletionAttemptTestCache(t *testing.T) (string, string) {
	t.Helper()
	root := t.TempDir()
	cache := root
	switch runtime.GOOS {
	case "windows":
		t.Setenv("LocalAppData", root)
	case "darwin":
		t.Setenv("HOME", root)
		cache = filepath.Join(root, "Library", "Caches")
	default:
		t.Setenv("XDG_CACHE_HOME", root)
	}
	return cache, filepath.Join(cache, "aeronautes", "deletion-attempts")
}

func readDeletionAttempts(t *testing.T, path string) []deletion.UnindexedAttempt {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasSuffix(data, []byte("\n")) {
		t.Fatal("evidence is not terminated JSONL")
	}
	var got []deletion.UnindexedAttempt
	for _, line := range bytes.Split(bytes.TrimSuffix(data, []byte("\n")), []byte("\n")) {
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(line, &fields); err != nil || len(fields) != 2 || fields["name"] == nil || fields["detail"] == nil {
			t.Fatalf("attempt fields=%v err=%v", fields, err)
		}
		var attempt deletion.UnindexedAttempt
		if err := json.Unmarshal(line, &attempt); err != nil {
			t.Fatal(err)
		}
		got = append(got, attempt)
	}
	return got
}

func TestRetainDeletionAttemptsExactJSONL(t *testing.T) {
	_, directory := deletionAttemptTestCache(t)
	want := []deletion.UnindexedAttempt{
		{Name: "", Detail: ""},
		{Name: "../outside/provider-name", Detail: "cannot establish this attempt"},
		{Name: "a/b%2Fc+ d", Detail: "500 quoted \"status\"\nsecond line\r\ttab\\slash"},
		{Name: "a\x00b\n\r", Detail: "detail\x00end"},
		{Name: "é", Detail: "composed"},
		{Name: "e\u0301", Detail: "decomposed"},
		{Name: "literal \ufffd", Detail: "valid Unicode replacement character"},
		{Name: "same", Detail: "first admitted attempt"},
		{Name: "same", Detail: "second admitted attempt"},
	}
	path, err := retainDeletionAttempts(want)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Dir(path) != directory || !strings.HasPrefix(filepath.Base(path), "attempts-") || !strings.HasSuffix(path, ".jsonl") {
		t.Fatalf("unexpected evidence path %q", path)
	}
	if got := readDeletionAttempts(t, path); !slices.Equal(got, want) {
		t.Fatalf("attempts changed: got=%q want=%q", got, want)
	}
	if runtime.GOOS != "windows" {
		for name, wantMode := range map[string]os.FileMode{directory: 0o700, path: 0o600} {
			info, err := os.Stat(name)
			if err != nil || info.Mode().Perm() != wantMode {
				t.Fatalf("private path %q: info=%v err=%v want mode=%o", name, info, err, wantMode)
			}
		}
	}
	second, err := retainDeletionAttempts(want)
	if err != nil || second == path {
		t.Fatalf("second evidence path=%q first=%q err=%v", second, path, err)
	}
	if got := readDeletionAttempts(t, path); !slices.Equal(got, want) {
		t.Fatal("a later retention changed an earlier artifact")
	}
}

func TestRetainDeletionAttemptsEmptyCreatesNothing(t *testing.T) {
	cache, directory := deletionAttemptTestCache(t)
	for _, attempts := range [][]deletion.UnindexedAttempt{nil, {}} {
		if path, err := retainDeletionAttempts(attempts); path != "" || err != nil {
			t.Fatalf("empty retention: path=%q err=%v", path, err)
		}
	}
	if _, err := os.Stat(directory); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("empty retention created an artifact directory: %v", err)
	}
	if entries, err := os.ReadDir(cache); err != nil && !errors.Is(err, os.ErrNotExist) || len(entries) != 0 {
		t.Fatalf("empty retention changed cache: entries=%v err=%v", entries, err)
	}
}

func TestRetainDeletionAttemptsRefusesUnavailableCache(t *testing.T) {
	deletionAttemptTestCache(t)
	switch runtime.GOOS {
	case "windows":
		t.Setenv("LocalAppData", "")
	case "darwin":
		t.Setenv("HOME", "")
	default:
		t.Setenv("XDG_CACHE_HOME", "relative-cache")
	}
	path, err := retainDeletionAttempts([]deletion.UnindexedAttempt{{Name: "issued"}})
	if path != "" || err == nil || !strings.Contains(err.Error(), "resolve deletion attempt cache") {
		t.Fatalf("unavailable cache: path=%q err=%v", path, err)
	}
	if path, err := retainDeletionAttempts(nil); path != "" || err != nil {
		t.Fatalf("empty retention consulted unavailable cache: path=%q err=%v", path, err)
	}
}

func TestRetainDeletionAttemptsReportsBlockedDirectory(t *testing.T) {
	_, directory := deletionAttemptTestCache(t)
	if err := os.MkdirAll(filepath.Dir(directory), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(directory, []byte("sentinel"), 0o600); err != nil {
		t.Fatal(err)
	}
	path, err := retainDeletionAttempts([]deletion.UnindexedAttempt{{Name: "issued"}})
	if path != "" || err == nil || !strings.Contains(err.Error(), "create deletion attempt directory") {
		t.Fatalf("blocked directory: path=%q err=%v", path, err)
	}
	if data, err := os.ReadFile(directory); err != nil || string(data) != "sentinel" {
		t.Fatalf("directory sentinel changed: %q %v", data, err)
	}
}

func TestRetainDeletionAttemptsRefusesRelativeCache(t *testing.T) {
	deletionAttemptTestCache(t)
	switch runtime.GOOS {
	case "windows":
		t.Setenv("LocalAppData", "relative-cache")
	case "darwin":
		t.Setenv("HOME", "relative-home")
	default:
		t.Setenv("XDG_CACHE_HOME", "")
		t.Setenv("HOME", "relative-home")
	}
	path, err := retainDeletionAttempts([]deletion.UnindexedAttempt{{Name: "issued"}})
	if path != "" || err == nil || !strings.Contains(err.Error(), "location is not absolute") {
		t.Fatalf("relative cache: path=%q err=%v", path, err)
	}
}

func TestRetainDeletionAttemptsReportsFileCreationFailure(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("owned permission fixture requires Unix modes without root bypass")
	}
	_, directory := deletionAttemptTestCache(t)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(directory, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chmod(directory, 0o700); err != nil {
			t.Error(err)
		}
	})
	path, err := retainDeletionAttempts([]deletion.UnindexedAttempt{{Name: "issued"}})
	if path != "" || !errors.Is(err, os.ErrPermission) || !strings.Contains(err.Error(), "create deletion attempt evidence") {
		t.Fatalf("file creation failure: path=%q err=%v", path, err)
	}
}

func TestRetainDeletionAttemptsRefusesLossyJSON(t *testing.T) {
	for _, test := range []struct {
		name    string
		attempt deletion.UnindexedAttempt
	}{
		{"invalid name", deletion.UnindexedAttempt{Name: "issued\xff", Detail: "available"}},
		{"invalid detail", deletion.UnindexedAttempt{Name: "issued", Detail: "detail\xff"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			deletionAttemptTestCache(t)
			attempts := []deletion.UnindexedAttempt{{Name: "valid first", Detail: "available"}, test.attempt}
			path, err := retainDeletionAttempts(attempts)
			if path != "" || err == nil || !strings.Contains(err.Error(), "invalid UTF-8") {
				t.Fatalf("lossy record advertised: path=%q err=%v", path, err)
			}
		})
	}
}

func TestWriteDeletionAttemptsChecksClosedFile(t *testing.T) {
	file, err := os.CreateTemp(t.TempDir(), "owned-")
	if err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	err = writeDeletionAttempts(file, []deletion.UnindexedAttempt{{Name: "issued", Detail: "small buffered record"}})
	if !errors.Is(err, os.ErrClosed) || !strings.Contains(err.Error(), "flush deletion attempts") || !strings.Contains(err.Error(), "close deletion attempt evidence") {
		t.Fatalf("closed owned file: %v", err)
	}
}

func TestWriteDeletionAttemptsBufferedIO(t *testing.T) {
	for _, large := range []bool{false, true} {
		for _, full := range []bool{false, true} {
			name := "small"
			if large {
				name = "large"
			}
			if full {
				name += " full device"
			} else {
				name += " regular file control"
			}
			t.Run(name, func(t *testing.T) {
				path := filepath.Join(t.TempDir(), "owned-writer")
				if full {
					if runtime.GOOS != "linux" {
						t.Skip("this concrete ENOSPC fixture uses Linux /dev/full")
					}
					if _, err := os.Stat("/dev/full"); err != nil {
						t.Skipf("/dev/full unavailable: %v", err)
					}
					if err := os.Symlink("/dev/full", path); err != nil {
						t.Skipf("cannot make owned /dev/full link: %v", err)
					}
				}
				file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE, 0o600)
				if err != nil {
					t.Fatal(err)
				}
				detail := "small buffered record"
				if large {
					detail = strings.Repeat("x", 16*1024)
				}
				attempts := []deletion.UnindexedAttempt{{Name: "issued", Detail: detail}}
				err = writeDeletionAttempts(file, attempts)
				if _, closeCheck := file.WriteString("must be closed"); !errors.Is(closeCheck, os.ErrClosed) {
					t.Fatalf("writer left its owned handle usable: %v", closeCheck)
				}
				if !full {
					if err != nil {
						t.Fatal(err)
					}
					if got := readDeletionAttempts(t, path); !slices.Equal(got, attempts) {
						t.Fatal("regular-file control changed evidence")
					}
					return
				}
				if !errors.Is(err, syscall.ENOSPC) || !strings.Contains(err.Error(), "flush deletion attempts") {
					t.Fatalf("expected concrete buffered I/O failure: %v", err)
				}
				if strings.Contains(err.Error(), "encode deletion attempt") != large || strings.Contains(err.Error(), "close deletion attempt evidence") {
					t.Fatalf("wrong failure stage for large=%t: %v", large, err)
				}
			})
		}
	}
}
