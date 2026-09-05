package rclone

import (
	"context"
	"crypto/md5"
	"encoding/pem"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func officialGatewayRunner(t *testing.T) *Runner {
	t.Helper()
	dir := os.Getenv("AERONAUTES_OFFICIAL_RCLONE_DIR")
	if dir == "" {
		t.Skip("AERONAUTES_OFFICIAL_RCLONE_DIR not set")
	}
	runner, err := NewRunner(Options{Binary: filepath.Join(dir, "rclone"), Retries: 1, LowLevelRetries: 1})
	if err != nil {
		t.Fatal(err)
	}
	return runner
}

func TestRunnerUsesGatewayTrustForDownloads(t *testing.T) {
	runner := officialGatewayRunner(t)
	data := []byte(strings.Repeat("verified child bytes\n", 1024))
	for _, mode := range []string{"loopback HTTP", "custom CA", "insecure", "untrusted TLS"} {
		t.Run(mode, func(t *testing.T) {
			var requests atomic.Int64
			handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				if r.Header.Get("X-Auth-Token") != "real-upstream-fixture" {
					t.Error("upstream did not receive its captured token")
				}
				w.Header().Set("X-Container-Bytes-Used", fmt.Sprint(len(data)))
				w.Header().Set("X-Container-Object-Count", "1")
				w.Header().Set("Content-Length", fmt.Sprint(len(data)))
				w.Header().Set("Content-Type", "application/octet-stream")
				w.Header().Set("ETag", fmt.Sprintf("%x", md5.Sum(data)))
				w.Header().Set("Last-Modified", time.Unix(1700000000, 0).UTC().Format(http.TimeFormat))
				if r.Method == "GET" {
					_, _ = w.Write(data)
				}
			})
			server := httptest.NewUnstartedServer(handler)
			if mode == "loopback HTTP" {
				server.Start()
			} else {
				server.StartTLS()
			}
			defer server.Close()
			remote := Remote{Name: "aeronautes_src", StorageURL: server.URL + gatewayAccount, Token: "real-upstream-fixture", InsecureTLS: mode == "insecure"}
			if mode == "custom CA" {
				remote.CACertFile = filepath.Join(t.TempDir(), "ca.pem")
				ca := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw})
				if err := os.WriteFile(remote.CACertFile, ca, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			destination := filepath.Join(t.TempDir(), "download")
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			result, err := runner.CopyTo(ctx, []Remote{remote}, "aeronautes_src:owned/object", destination, nil)
			if mode == "untrusted TLS" {
				if err == nil && (result == nil || result.ExitCode == 0 || result.Cancelled) {
					t.Fatal("untrusted upstream was not refused")
				}
				if requests.Load() != 0 {
					t.Fatal("credentials reached an untrusted TLS endpoint")
				}
				return
			}
			if err != nil || result.ExitCode != 0 || result.Cancelled {
				t.Fatalf("trusted download failed: result=%+v err=%v", result, err)
			}
			copied, err := os.ReadFile(destination)
			if err != nil || string(copied) != string(data) {
				t.Fatalf("trusted download bytes changed: %v", err)
			}
		})
	}
}

func TestRunnerCancellationStopsGatewayTraffic(t *testing.T) {
	runner := officialGatewayRunner(t)
	started, stopped := make(chan struct{}), make(chan struct{})
	var startOnce, stopOnce sync.Once
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Container-Bytes-Used", "67108864")
		w.Header().Set("X-Container-Object-Count", "1")
		w.Header().Set("Content-Length", "67108864")
		w.Header().Set("ETag", strings.Repeat("0", 32))
		w.Header().Set("Last-Modified", time.Unix(1700000000, 0).UTC().Format(http.TimeFormat))
		if r.Method == "GET" {
			startOnce.Do(func() { close(started) })
			_, _ = w.Write(make([]byte, 64<<10))
			w.(http.Flusher).Flush()
			<-r.Context().Done()
			stopOnce.Do(func() { close(stopped) })
		}
	}))
	defer upstream.Close()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	type outcome struct {
		result *Result
		err    error
	}
	finished := make(chan outcome, 1)
	destination := filepath.Join(t.TempDir(), "download")
	go func() {
		result, err := runner.CopyTo(ctx, []Remote{{Name: "aeronautes_src", StorageURL: upstream.URL + gatewayAccount, Token: "upstream-fixture"}}, "aeronautes_src:owned/object", destination, nil)
		finished <- outcome{result, err}
	}()
	select {
	case <-started:
	case <-time.After(10 * time.Second):
		t.Fatal("real child never started the upstream read")
	}
	cancel()
	select {
	case outcome := <-finished:
		if outcome.err != nil || outcome.result == nil || !outcome.result.Cancelled {
			t.Fatalf("child did not report cancellation: %+v %v", outcome.result, outcome.err)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("cancelled child did not exit")
	}
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("child cancellation left upstream traffic active")
	}
	if _, err := os.Stat(destination); !os.IsNotExist(err) {
		t.Fatal("cancelled download published a final object")
	}
}

func TestRunnerRefusesStorageRedirects(t *testing.T) {
	runner := officialGatewayRunner(t)
	for _, status := range []int{301, 302, 303, 307, 308} {
		for _, targetKind := range []string{"same origin", "cross port", "cross host", "HTTPS downgrade"} {
			for _, operation := range []string{"read", "write"} {
				t.Run(fmt.Sprintf("%d/%s/%s", status, targetKind, operation), func(t *testing.T) {
					const token = "synthetic-upstream-token"
					var targetRequests, targetTokens, targetWrites atomic.Int64
					var redirects atomic.Int64
					sink := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						targetRequests.Add(1)
						if r.Header.Get("X-Auth-Token") != "" {
							targetTokens.Add(1)
						}
						if r.Method != http.MethodGet && r.Method != http.MethodHead {
							targetWrites.Add(1)
						}
						w.WriteHeader(http.StatusForbidden)
					})
					target := httptest.NewServer(sink)
					defer target.Close()
					targetURL := target.URL + "/redirected"
					if targetKind == "cross host" {
						targetURL = strings.Replace(targetURL, "127.0.0.1", "localhost", 1)
					}
					var location atomic.Value
					location.Store(targetURL)
					upstreamHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						if r.URL.Path == "/redirected" {
							sink.ServeHTTP(w, r)
							return
						}
						if r.URL.Path == "/info" {
							fmt.Fprint(w, `{}`)
							return
						}
						if operation == "write" && r.Method == http.MethodHead {
							if strings.Contains(r.URL.Path, "/.aeronautes/") {
								w.WriteHeader(http.StatusNotFound)
							} else {
								w.Header().Set("X-Container-Bytes-Used", "0")
								w.Header().Set("X-Container-Object-Count", "0")
								w.WriteHeader(http.StatusNoContent)
							}
							return
						}
						if operation == "read" || r.Method == http.MethodPut {
							redirects.Add(1)
						}
						w.Header().Set("Location", location.Load().(string))
						w.WriteHeader(status)
					})
					var upstream *httptest.Server
					if targetKind == "HTTPS downgrade" {
						upstream = httptest.NewTLSServer(upstreamHandler)
					} else {
						upstream = httptest.NewServer(upstreamHandler)
					}
					defer upstream.Close()
					if targetKind == "same origin" {
						location.Store(upstream.URL + "/redirected")
					}
					remote := Remote{Name: "aeronautes_src", StorageURL: upstream.URL + "/v1/AUTH_test", Token: token, InsecureTLS: targetKind == "HTTPS downgrade"}
					local := filepath.Join(t.TempDir(), "object")
					src, dst := "aeronautes_src:owned/object", local
					if operation == "write" {
						if err := os.WriteFile(local, []byte("owned fixture"), 0o600); err != nil {
							t.Fatal(err)
						}
						remote.Name = "aeronautes_dst"
						src, dst = local, "aeronautes_dst:owned/.aeronautes/stage/redirect/object"
					}
					ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
					defer cancel()
					result, err := runner.CopyTo(ctx, []Remote{remote}, src, dst, nil)
					if redirects.Load() == 0 {
						t.Errorf("test did not reach the upstream %s redirect: result=%+v err=%v", operation, result, err)
					}
					if targetRequests.Load() != 0 || targetTokens.Load() != 0 || targetWrites.Load() != 0 {
						t.Errorf("redirect target reached: requests=%d tokens=%d mutations=%d", targetRequests.Load(), targetTokens.Load(), targetWrites.Load())
					}
					if err == nil && (result == nil || result.ExitCode == 0 || result.Cancelled) {
						t.Errorf("redirect did not produce a transfer refusal: %+v", result)
					}
					if operation == "read" {
						if _, err := os.Stat(local); !os.IsNotExist(err) {
							t.Errorf("refused download left a local object: %v", err)
						}
					}
				})
			}
		}
	}
}
