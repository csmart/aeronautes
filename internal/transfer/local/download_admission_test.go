package local

import (
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

type downloadAdmissionTransport func(*http.Request) (*http.Response, error)

func (f downloadAdmissionTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

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
				if admission == "mkdir" {
					if err := os.WriteFile(dest, []byte("existing destination file"), 0o600); err != nil {
						t.Fatal(err)
					}
				} else {
					if err := os.Mkdir(dest, 0o700); err != nil {
						t.Fatal(err)
					}
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
				placed, err := nativeDownload(t.Context(), source, "media", DownloadItem{Name: "selected", RelPath: "final", Size: 21}, dest)
				if placed || err == nil || gets.Load() != 1 || closes.Load() != 1 {
					t.Fatalf("admission did not close the actual GET exactly once: placed=%v gets=%d closes=%d error=%v", placed, gets.Load(), closes.Load(), err)
				}
				if errors.Is(err, closeErr) != closeFails {
					t.Errorf("admission lost or invented the close error: %v", err)
				}
				var pathErr *os.PathError
				if !errors.As(err, &pathErr) {
					t.Fatalf("admission lost filesystem cause: %v", err)
				}
				if admission == "mkdir" {
					if pathErr.Op != "mkdir" || pathErr.Path != dest {
						t.Errorf("wrong admission failure: %v", pathErr)
					}
					data, err := os.ReadFile(dest)
					if err != nil || string(data) != "existing destination file" {
						t.Errorf("admission changed destination bytes: %q %v", data, err)
					}
				} else {
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
