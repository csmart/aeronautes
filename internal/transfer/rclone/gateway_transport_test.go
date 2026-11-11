package rclone

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/pem"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/csmart/aeronautes/internal/applog"
)

func TestGatewayTLSAndEncodedResponse(t *testing.T) {
	var compressed bytes.Buffer
	writer := gzip.NewWriter(&compressed)
	if _, err := writer.Write([]byte("the stored gzip bytes must survive the gateway")); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Encoding", "gzip")
		_, _ = w.Write(compressed.Bytes())
	}))
	defer upstream.Close()
	caFile := filepath.Join(t.TempDir(), "ca.pem")
	ca := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: upstream.Certificate().Raw})
	if err := os.WriteFile(caFile, ca, 0o600); err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"system", "custom", "insecure"} {
		t.Run(mode, func(t *testing.T) {
			remote := Remote{Name: "aeronautes_src", StorageURL: upstream.URL + gatewayAccount, Token: "fixture-token"}
			if mode == "custom" {
				remote.CACertFile = caFile
			}
			remote.InsecureTLS = mode == "insecure"
			gateway, local, err := newStorageGateway(t.Context(), remote, nil, 64)
			if err != nil {
				t.Fatal(err)
			}
			defer gateway.Close()
			transport := &http.Transport{DisableCompression: true}
			defer transport.CloseIdleConnections()
			client := &http.Client{Transport: transport, Timeout: 3 * time.Second}
			req, err := http.NewRequest("GET", local.StorageURL+"/owned/object", nil)
			if err != nil {
				t.Fatal(err)
			}
			req.Header.Set("X-Auth-Token", local.Token)
			resp, err := client.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			body, err := io.ReadAll(resp.Body)
			if err != nil {
				t.Fatal(err)
			}
			if mode == "system" {
				if resp.StatusCode != http.StatusBadGateway {
					t.Fatal("system mode trusted a self-signed upstream")
				}
			} else if resp.StatusCode != http.StatusOK || resp.Header.Get("Content-Encoding") != "gzip" || !bytes.Equal(body, compressed.Bytes()) || resp.ContentLength != int64(compressed.Len()) {
				t.Fatal("upstream trust or stored content encoding was not preserved")
			}
		})
	}
	if err := os.WriteFile(caFile, []byte("invalid CA data"), 0o600); err != nil {
		t.Fatal(err)
	}
	if gateway, _, err := newStorageGateway(t.Context(), Remote{Name: "aeronautes_src", StorageURL: upstream.URL + gatewayAccount, Token: "fixture-token", CACertFile: caFile}, nil, 64); err == nil {
		gateway.Close()
		t.Fatal("invalid CA data accepted")
	}
}

func TestGatewayRejectsProxyAndForeignHost(t *testing.T) {
	f := newGatewayFixture(t, nil)
	proxyURL, err := url.Parse(f.root)
	if err != nil {
		t.Fatal(err)
	}
	for _, proxy := range []bool{false, true} {
		transport := &http.Transport{}
		if proxy {
			transport.Proxy = http.ProxyURL(proxyURL)
		}
		client := &http.Client{Transport: transport, Timeout: 3 * time.Second}
		req, err := http.NewRequest("GET", f.remote.StorageURL+"/owned/object", nil)
		if err != nil {
			t.Fatal(err)
		}
		if !proxy {
			req.Host = "unrelated.invalid"
		}
		req.Header.Set("X-Auth-Token", f.remote.Token)
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		transport.CloseIdleConnections()
		if resp.StatusCode != http.StatusForbidden || len(f.events) != 0 {
			t.Fatal("gateway accepted a proxy request or foreign Host")
		}
	}
}

func TestGatewayCancellationClosesActiveRequests(t *testing.T) {
	started, stopped := make(chan struct{}), make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		<-r.Context().Done()
		close(stopped)
	}))
	defer upstream.Close()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	gateway, local, err := newStorageGateway(ctx, Remote{Name: "aeronautes_src", StorageURL: upstream.URL + gatewayAccount, Token: "fixture-token"}, nil, 64)
	if err != nil {
		t.Fatal(err)
	}
	defer gateway.Close()
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		req, err := http.NewRequest("GET", local.StorageURL+"/owned/object", nil)
		if err != nil {
			t.Error(err)
			return
		}
		req.Header.Set("X-Auth-Token", local.Token)
		resp, err := http.DefaultClient.Do(req)
		if err == nil {
			resp.Body.Close()
		}
	}()
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("upstream request never started")
	}
	cancel()
	for _, done := range []<-chan struct{}{stopped, finished, gateway.done} {
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			t.Fatal("cancellation left an active request or listener")
		}
	}
	endpoint, err := url.Parse(local.StorageURL)
	if err != nil {
		t.Fatal(err)
	}
	if conn, err := net.DialTimeout("tcp", endpoint.Host, time.Second); err == nil {
		conn.Close()
		t.Fatal("cancelled gateway still accepts connections")
	}
}

func TestGatewayStreamsBeforeTheProducerFinishes(t *testing.T) {
	for _, operation := range []string{"upload", "download"} {
		t.Run(operation, func(t *testing.T) {
			const size = 16 << 20
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			firstChunk := bytes.Repeat([]byte("x"), 64<<10)
			observed := make(chan struct{})
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				if operation == "upload" {
					chunk := make([]byte, len(firstChunk))
					if _, err := io.ReadFull(req.Body, chunk); err != nil {
						t.Error(err)
						return
					}
					close(observed)
					n, err := io.Copy(io.Discard, req.Body)
					if err != nil || n+int64(len(chunk)) != size {
						t.Errorf("upload bytes=%d err=%v", n+int64(len(chunk)), err)
					}
				} else {
					_, _ = w.Write(firstChunk)
					w.(http.Flusher).Flush()
					select {
					case <-observed:
					case <-ctx.Done():
						return
					}
					_, _ = io.CopyN(w, zeroStream{}, size-int64(len(firstChunk)))
				}
			}))
			defer upstream.Close()
			scope := &gatewayScope{container: "owned", object: gatewayStage}
			gateway, local, err := newStorageGateway(ctx, Remote{Name: "aeronautes_dst", StorageURL: upstream.URL + gatewayAccount, Token: "fixture-token"}, scope, 64)
			if err != nil {
				t.Fatal(err)
			}
			defer gateway.Close()
			method := "GET"
			var body io.Reader
			if operation == "upload" {
				method = "PUT"
				reader, writer := io.Pipe()
				defer reader.Close()
				body = reader
				go func() {
					defer writer.Close()
					if _, err := writer.Write(firstChunk); err != nil {
						return
					}
					select {
					case <-observed:
					case <-ctx.Done():
						return
					}
					_, _ = io.CopyN(writer, zeroStream{}, size-int64(len(firstChunk)))
				}()
			}
			req, err := http.NewRequestWithContext(ctx, method, local.StorageURL+"/owned/"+gatewayStage+"/one", body)
			if err != nil {
				t.Fatal(err)
			}
			req.Header.Set("X-Auth-Token", local.Token)
			if operation == "upload" {
				req.ContentLength = size
			}
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("stream refused: %d", resp.StatusCode)
			}
			if operation == "download" {
				chunk := make([]byte, len(firstChunk))
				if _, err := io.ReadFull(resp.Body, chunk); err != nil {
					t.Fatal(err)
				}
				if !bytes.Equal(chunk, firstChunk) {
					t.Fatal("first streamed bytes changed")
				}
				close(observed)
				n, err := io.Copy(io.Discard, resp.Body)
				if err != nil || n+int64(len(chunk)) != size {
					t.Fatalf("download bytes=%d err=%v", n+int64(len(chunk)), err)
				}
			}
		})
	}
}

type zeroStream struct{}

func (zeroStream) Read(p []byte) (int, error) {
	clear(p)
	return len(p), nil
}

// When the upstream cannot be reached the engine only sees the gateway's 502,
// so the application log must say why, without the token, the account path
// or the object name.
func TestGatewayLogsWhyTheUpstreamFailed(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "application.log")
	t.Cleanup(func() {
		if _, _, err := applog.Configure(); err != nil {
			t.Error(err)
		}
	})
	t.Setenv(applog.EnvLevel, "warn")
	t.Setenv(applog.EnvFile, logPath)
	_, closeLog, err := applog.Configure()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = closeLog() })

	upstream := httptest.NewServer(http.NotFoundHandler())
	host := upstream.Listener.Addr().String()
	upstream.Close()
	gateway, local, err := newStorageGateway(t.Context(), Remote{Name: "aeronautes_src", StorageURL: "http://" + host + gatewayAccount, Token: "fixture-token"}, nil, 64)
	if err != nil {
		t.Fatal(err)
	}
	defer gateway.Close()
	req, err := http.NewRequest("GET", local.StorageURL+"/owned/secret-object-name", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("X-Auth-Token", local.Token)
	resp, err := (&http.Client{Timeout: 3 * time.Second}).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadGateway {
		t.Fatalf("status %d, want 502", resp.StatusCode)
	}
	logged, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	text := string(logged)
	if !strings.Contains(text, "storage gateway: GET to "+host+" failed") || !strings.Contains(text, "refused") {
		t.Errorf("the log does not say why the upstream failed: %q", text)
	}
	for _, private := range []string{"fixture-token", local.Token, "secret-object-name", strings.Trim(gatewayAccount, "/")} {
		if strings.Contains(text, private) {
			t.Errorf("the log carries %q: %q", private, text)
		}
	}
}
