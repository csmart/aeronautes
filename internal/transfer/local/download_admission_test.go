package local

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	transfercopy "github.com/csmart/aeronautes/internal/transfer/copy"
)

type downloadAdmissionTransport func(*http.Request) (*http.Response, error)

func (f downloadAdmissionTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

type downloadCloseReader struct {
	io.Reader
	close func() error
}

func (r downloadCloseReader) Close() error { return r.close() }

func TestNativeDownloadAdmissionClosesSource(t *testing.T) {
	for _, admission := range []string{"mkdir", "temporary"} {
		for _, closeFails := range []bool{false, true} {
			name := admission + "/close_ok"
			if closeFails {
				name = admission + "/close_refused"
			}
			t.Run(name, func(t *testing.T) {
				var gets, closes atomic.Int32
				closeErr := errors.New("source transport close refused")
				original := http.DefaultTransport
				t.Cleanup(func() { http.DefaultTransport = original })
				// This sequential test captures a counting transport in the
				// session without adding a production-only test hook.
				http.DefaultTransport = downloadAdmissionTransport(func(r *http.Request) (*http.Response, error) {
					response, err := original.RoundTrip(r)
					if err == nil && r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/media/selected") {
						gets.Add(1)
						body := response.Body
						response.Body = downloadCloseReader{Reader: body, close: func() error {
							closes.Add(1)
							err := body.Close()
							if closeFails {
								return errors.Join(err, closeErr)
							}
							return err
						}}
					}
					return response, err
				})
				source, mock := session(t)
				http.DefaultTransport = original
				seed(mock, "media", "selected", "complete source bytes")
				dest := filepath.Join(t.TempDir(), "selected")
				if err := os.Mkdir(dest, 0o700); err != nil {
					t.Fatal(err)
				}
				// The selected directory is always a directory the handle can
				// hold; the parent that cannot be created is a file beneath it.
				item := DownloadItem{Name: "selected", RelPath: "final", Size: 21}
				if admission == "mkdir" {
					if err := os.WriteFile(filepath.Join(dest, "parent"), []byte("existing destination file"), 0o600); err != nil {
						t.Fatal(err)
					}
					item.RelPath = filepath.Join("parent", "final")
				} else {
					if err := os.WriteFile(filepath.Join(dest, "keep"), []byte("local sentinel"), 0o600); err != nil {
						t.Fatal(err)
					}
					t.Cleanup(func() {
						if err := os.Chmod(dest, 0o700); err != nil {
							t.Error(err)
						}
					})
					if err := os.Chmod(dest, 0o500); err != nil {
						t.Fatal(err)
					}
				}
				outcome := nativeDownload(t.Context(), source, "media", item, testDestination(t, dest))
				placed, err := outcome.Result == transfercopy.ResultCopied, outcome.err()
				if placed || err == nil || gets.Load() != 1 || closes.Load() != 1 {
					t.Fatalf("admission did not close the actual GET exactly once: placed=%v gets=%d closes=%d error=%v", placed, gets.Load(), closes.Load(), err)
				}
				if errors.Is(err, closeErr) != closeFails {
					t.Errorf("admission lost or invented the close error: %v", err)
				}
				if admission == "mkdir" {
					// A file at a parent's name is a refusal of that component
					// through the held handle, never a pathname mkdir.
					if !strings.Contains(err.Error(), fmt.Sprintf("destination component %q is not an inspectable ordinary directory", filepath.Join(dest, "parent"))) {
						t.Errorf("wrong admission failure: %v", err)
					}
					data, err := os.ReadFile(filepath.Join(dest, "parent"))
					if err != nil || string(data) != "existing destination file" {
						t.Errorf("admission changed destination bytes: %q %v", data, err)
					}
				} else {
					var pathErr *os.PathError
					if !errors.As(err, &pathErr) {
						t.Fatalf("admission lost filesystem cause: %v", err)
					}
					if pathErr.Op != "open" || filepath.Dir(pathErr.Path) != dest || !errors.Is(err, os.ErrPermission) {
						t.Errorf("temporary admission was not denied: %v", pathErr)
					}
					entries, err := os.ReadDir(dest)
					if err != nil || len(entries) != 1 || entries[0].Name() != "keep" {
						t.Fatalf("admission left a temporary or final name: %+v %v", entries, err)
					}
					data, err := os.ReadFile(filepath.Join(dest, "keep"))
					if err != nil || string(data) != "local sentinel" {
						t.Errorf("admission changed sentinel bytes: %q %v", data, err)
					}
				}
			})
		}
	}
}
