package rclone

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

func TestGatewaySetupFailureClosesEarlierListeners(t *testing.T) {
	for _, failure := range []string{"constructor", "mixed trust"} {
		t.Run(failure, func(t *testing.T) {
			var opened []*storageGateway
			create := func(ctx context.Context, remote Remote, scope *gatewayScope, capacity int) (*storageGateway, Remote, error) {
				if len(opened) == 1 && failure == "constructor" {
					return nil, Remote{}, errors.New("injected listener setup failure")
				}
				gateway, local, err := newStorageGateway(ctx, remote, scope, capacity)
				if err == nil {
					opened = append(opened, gateway)
					t.Cleanup(gateway.Close)
				}
				return gateway, local, err
			}
			pair := remotes()
			if failure == "mixed trust" {
				pair[1].InsecureTLS = true
			}
			local, cleanup, err := startGatewaysWith(t.Context(), pair, "aeronautes_dst:owned/"+gatewayStage, false, 64, create)
			if err == nil || local != nil || cleanup != nil || len(opened) == 0 {
				t.Fatal("setup failure did not return an empty result")
			}
			for _, gateway := range opened {
				select {
				case <-gateway.done:
				case <-time.After(time.Second):
					t.Fatal("setup failure left an earlier listener running")
				}
				if conn, err := net.DialTimeout("tcp", gateway.localHost, time.Second); err == nil {
					conn.Close()
					t.Fatal("closed gateway still accepts requests")
				}
			}
		})
	}
}

func TestGatewayCancelDeadlineRevokesWithoutChildExit(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	started := make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		close(started)
		<-req.Context().Done()
	}))
	defer upstream.Close()
	remote := Remote{Name: "aeronautes_src", StorageURL: upstream.URL + gatewayAccount, Token: "parent-token"}
	local, closeAll, err := startGateways(ctx, []Remote{remote}, "/unused/download", true, 64)
	if err != nil {
		t.Fatal(err)
	}
	defer closeAll()
	endpoint, err := url.Parse(local[0].StorageURL)
	if err != nil {
		t.Fatal(err)
	}
	conn, err := net.DialTimeout("tcp", endpoint.Host, time.Second)
	if err != nil {
		t.Fatal("gateway was not open before cancellation")
	}
	defer conn.Close()
	if _, err := fmt.Fprintf(conn, "GET %s/owned/one HTTP/1.1\r\nHost: %s\r\nX-Auth-Token: %s\r\n\r\n", gatewayAccount, endpoint.Host, local[0].Token); err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("request did not pass the header-read phase before cancellation")
	}
	deadline := time.Now().Add(childCancelGrace + 2*time.Second)
	cancel()
	// No child exits and the invocation cleanup is deliberately withheld.
	// A cancelled upstream request can flush an error before Close reaches
	// the socket. Drain that bounded response, then require closure at the
	// kill deadline independently of child exit.
	if err := conn.SetReadDeadline(deadline); err != nil {
		t.Fatal(err)
	}
	if _, err := io.CopyN(io.Discard, conn, 32<<10); err == nil {
		t.Fatal("cancelled gateway sent 32 KiB without closing")
	} else if timeout, ok := err.(net.Error); ok && timeout.Timeout() {
		t.Fatal("gateway relied on child exit instead of its cancellation deadline")
	} else if !errors.Is(err, io.EOF) && !errors.Is(err, syscall.ECONNRESET) {
		t.Fatalf("checking revoked gateway socket: %v", err)
	}
	// Server.Close disables keep-alives before closing the listener. A
	// cancelled handler can therefore close this socket while the listener
	// is still shutting down; both must finish within the original bound.
	wait, stop := context.WithDeadline(t.Context(), deadline)
	defer stop()
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	for {
		next, err := (&net.Dialer{Timeout: time.Second}).DialContext(wait, "tcp", endpoint.Host)
		if errors.Is(err, syscall.ECONNREFUSED) {
			break
		}
		if err != nil {
			t.Fatalf("checking revoked gateway listener: %v", err)
		}
		if err := next.Close(); err != nil {
			t.Fatal(err)
		}
		select {
		case <-wait.Done():
			t.Fatal("cancel deadline left the listener usable")
		case <-tick.C:
		}
	}
}

func TestRunnerAllowsCleanupDuringCancelGrace(t *testing.T) {
	var deletes atomic.Int64
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.Method != "DELETE" || req.URL.Path != gatewayAccount+"/owned/"+gatewayStage+"/one" || req.Header.Get("X-Auth-Token") != "parent-token" {
			t.Error("unexpected cancellation cleanup request")
			w.WriteHeader(403)
			return
		}
		deletes.Add(1)
		w.WriteHeader(204)
	}))
	defer upstream.Close()
	ready := filepath.Join(t.TempDir(), "ready")
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	bin := stub(t, PinnedVersion, fmt.Sprintf("exec %q -test.run='^TestGatewayCleanupChild$' -- %q", binary, ready))
	runner, err := NewRunner(Options{Binary: bin})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	done := make(chan struct{})
	var result *Result
	go func() {
		defer close(done)
		result, err = runner.CopyTo(ctx, []Remote{{Name: "aeronautes_dst", StorageURL: upstream.URL + gatewayAccount, Token: "parent-token"}}, "/unused/source", "aeronautes_dst:owned/"+gatewayStage+"/one", nil)
	}()
	for {
		if _, err := os.Stat(ready); err == nil {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("cleanup child did not become ready")
		case <-time.After(10 * time.Millisecond):
		}
	}
	cancel()
	select {
	case <-done:
		if err != nil || result == nil || result.ExitCode != 0 || !result.Cancelled || deletes.Load() != 1 {
			t.Fatalf("graceful cleanup: result=%+v error=%v deletes=%d", result, err, deletes.Load())
		}
	case <-time.After(15 * time.Second):
		t.Fatal("cleanup child did not exit")
	}
}

func TestGatewayCleanupChild(t *testing.T) {
	endpoint := os.Getenv("RCLONE_CONFIG_AERONAUTES_DST_STORAGE_URL")
	if endpoint == "" {
		return
	}
	interrupt := make(chan os.Signal, 1)
	signal.Notify(interrupt, syscall.SIGINT)
	defer signal.Stop(interrupt)
	if err := os.WriteFile(os.Args[len(os.Args)-1], nil, 0o600); err != nil {
		t.Fatal(err)
	}
	<-interrupt
	req, err := http.NewRequest("DELETE", endpoint+"/owned/"+gatewayStage+"/one", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("X-Auth-Token", os.Getenv("RCLONE_CONFIG_AERONAUTES_DST_AUTH_TOKEN"))
	resp, err := (&http.Client{Timeout: time.Second}).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 204 {
		t.Fatalf("cleanup refused: %d", resp.StatusCode)
	}
}

func TestRunnerClosesGatewaysAfterChildExit(t *testing.T) {
	for _, method := range []string{"Copy", "CopyTo"} {
		for _, exit := range []int{0, 7} {
			t.Run(fmt.Sprintf("%s/exit%d", method, exit), func(t *testing.T) {
				bin := stub(t, PinnedVersion, fmt.Sprintf("exit %d", exit))
				runner, err := NewRunner(Options{Binary: bin})
				if err != nil {
					t.Fatal(err)
				}
				var result *Result
				if method == "Copy" {
					result, err = runner.Copy(t.Context(), remotes(), "aeronautes_src:owned", "aeronautes_dst:owned/"+gatewayStage, []string{"one"}, nil)
				} else {
					result, err = runner.CopyTo(t.Context(), remotes(), "aeronautes_src:owned/one", filepath.Join(t.TempDir(), "download"), nil)
				}
				if err != nil || result == nil || result.ExitCode != exit {
					t.Fatalf("child exit: result=%+v error=%v", result, err)
				}
				data, err := os.ReadFile(filepath.Join(dumpDir(bin), "env"))
				if err != nil {
					t.Fatal(err)
				}
				checked := 0
				for line := range strings.SplitSeq(string(data), "\n") {
					name, value, _ := strings.Cut(line, "=")
					if !strings.HasPrefix(name, "RCLONE_CONFIG_AERONAUTES_") || !strings.HasSuffix(name, "_STORAGE_URL") {
						continue
					}
					endpoint, err := url.Parse(value)
					if err != nil {
						t.Fatal(err)
					}
					if conn, err := net.DialTimeout("tcp", endpoint.Host, time.Second); err == nil {
						conn.Close()
						t.Fatal("returned child left a usable gateway listener")
					}
					checked++
				}
				if checked != 2 {
					t.Fatalf("checked %d gateways, want 2", checked)
				}
			})
		}
	}
}
