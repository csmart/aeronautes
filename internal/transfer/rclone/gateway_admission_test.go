package rclone

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestGatewayRefusesRevokedRequests(t *testing.T) {
	for _, call := range []struct{ name, method, target, body string }{
		{"get", "GET", gatewayAccount + "/owned/file", ""},
		{"head", "HEAD", gatewayAccount + "/owned/file", ""},
		{"put", "PUT", gatewayAccount + "/owned/" + gatewayFolder + "/file", "contents"},
		{"sidecar", "PUT", gatewayAccount + "/owned_segments", ""},
		{"info", "GET", "/info", ""},
		{"bulk", "DELETE", gatewayAccount + "?bulk-delete=1", "/owned/" + gatewayFolder + "/file\n"},
	} {
		t.Run(call.name, func(t *testing.T) {
			var requests atomic.Int64
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				_, _ = io.Copy(io.Discard, r.Body)
				w.WriteHeader(http.StatusNoContent)
			}))
			t.Cleanup(upstream.Close)
			var revoked atomic.Bool
			refused := errors.New("private generation lookup detail")
			remote := Remote{Name: "aeronautes_dst", StorageURL: upstream.URL + gatewayAccount, Token: "fixture-token",
				AdmitRequest: func(context.Context) error {
					if revoked.Load() {
						return refused
					}
					return nil
				},
			}
			scope := &gatewayScope{container: "owned", object: gatewayFolder}
			revoked.Store(true)
			if gateway, _, err := newStorageGateway(t.Context(), remote, scope, 64); gateway != nil || !errors.Is(err, refused) {
				if gateway != nil {
					gateway.Close()
				}
				t.Fatalf("revoked remote created a gateway: %v", err)
			}
			revoked.Store(false)
			gateway, local, err := newStorageGateway(t.Context(), remote, scope, 64)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(gateway.Close)
			if local.AdmitRequest != nil {
				t.Fatal("parent admission callback crossed into child configuration")
			}
			fixture := gatewayFixture{root: strings.TrimSuffix(local.StorageURL, gatewayAccount), remote: local}
			headers := http.Header{"Content-Type": {"text/plain"}}
			status, _ := fixture.request(t, call.method, call.target, local.Token, call.body, headers)
			if status != http.StatusNoContent || requests.Load() == 0 {
				t.Fatalf("valid request did not reach upstream: status=%d", status)
			}
			before := requests.Load()
			revoked.Store(true)
			status, body := fixture.request(t, call.method, call.target, local.Token, call.body, headers)
			if status/100 == 2 || requests.Load() != before || strings.Contains(body, "private") {
				t.Fatalf("revoked gateway: status=%d, extra requests=%d, disclosed detail=%v", status, requests.Load()-before, strings.Contains(body, "private"))
			}
		})
	}
}

func TestGatewayRechecksAdmissionAfterSidecarHead(t *testing.T) {
	entered, gate := make(chan struct{}), make(chan struct{})
	var once sync.Once
	release := func() { once.Do(func() { close(gate) }) }
	var writes atomic.Int64
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodHead {
			close(entered)
			<-gate
		} else {
			writes.Add(1)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(func() { release(); upstream.Close() })
	var revoked atomic.Bool
	remote := Remote{Name: "aeronautes_dst", StorageURL: upstream.URL + gatewayAccount, Token: "fixture-token",
		AdmitRequest: func(context.Context) error {
			if revoked.Load() {
				return errors.New("generation revoked")
			}
			return nil
		},
	}
	gateway, local, err := newStorageGateway(t.Context(), remote, &gatewayScope{container: "owned", object: gatewayFolder}, 64)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(gateway.Close)
	result := make(chan error, 1)
	go func() {
		req, err := http.NewRequestWithContext(t.Context(), http.MethodPut, local.StorageURL+"/owned_segments", nil)
		if err != nil {
			result <- err
			return
		}
		req.Header.Set("X-Auth-Token", local.Token)
		response, err := (&http.Client{Timeout: 5 * time.Second}).Do(req)
		if err == nil {
			response.Body.Close()
			if response.StatusCode != http.StatusBadGateway {
				err = fmt.Errorf("sidecar request status=%d", response.StatusCode)
			}
		}
		result <- err
	}()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("sidecar HEAD was not acknowledged")
	}
	revoked.Store(true)
	release()
	select {
	case err := <-result:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("sidecar request did not finish")
	}
	if writes.Load() != 0 {
		t.Fatal("gateway reused HEAD admission for the later PUT")
	}
}

func TestGatewayRechecksAdmissionAfterBulkBody(t *testing.T) {
	var requests atomic.Int64
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(upstream.Close)
	var revoked atomic.Bool
	remote := Remote{Name: "aeronautes_dst", StorageURL: upstream.URL + gatewayAccount, Token: "fixture-token",
		AdmitRequest: func(context.Context) error {
			if revoked.Load() {
				return errors.New("generation revoked")
			}
			return nil
		},
	}
	gateway, local, err := newStorageGateway(t.Context(), remote, &gatewayScope{container: "owned", object: gatewayFolder}, 64)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(gateway.Close)
	endpoint, err := url.Parse(local.StorageURL)
	if err != nil {
		t.Fatal(err)
	}
	conn, err := net.DialTimeout("tcp", endpoint.Host, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if err := conn.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatal(err)
	}
	body := "/owned/" + gatewayFolder + "/file\n"
	_, err = fmt.Fprintf(conn, "DELETE %s?bulk-delete=1 HTTP/1.1\r\nHost: %s\r\nX-Auth-Token: %s\r\nContent-Type: text/plain\r\nContent-Length: %d\r\nExpect: 100-continue\r\n\r\n", gatewayAccount, endpoint.Host, local.Token, len(body))
	if err != nil {
		t.Fatal(err)
	}
	reader := bufio.NewReader(conn)
	response, err := http.ReadResponse(reader, nil)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusContinue {
		t.Fatalf("body read was not acknowledged: %d", response.StatusCode)
	}
	revoked.Store(true)
	if _, err := io.WriteString(conn, body); err != nil {
		t.Fatal(err)
	}
	response, err = http.ReadResponse(reader, nil)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusBadGateway || requests.Load() != 0 {
		t.Fatalf("gateway forwarded after blocked body read: status=%d, upstream requests=%d", response.StatusCode, requests.Load())
	}
}
