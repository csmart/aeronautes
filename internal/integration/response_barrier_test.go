package integration

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/csmart/aeronautes/internal/swiftcontrol"
	"github.com/csmart/aeronautes/internal/swifttest"
	"github.com/csmart/aeronautes/internal/systemtest/oracle"
)

func TestPutResponseBarrierRealSwift(t *testing.T) {
	h := harness(t)
	for _, lose := range []bool{false, true} {
		t.Run(map[bool]string{false: "deliver", true: "lose"}[lose], func(t *testing.T) {
			container := h.Container("response-" + map[bool]string{false: "deliver", true: "lose"}[lose])
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			if _, err := h.Session.CreateContainer(ctx, swiftcontrol.ContainerCreateRequest{Name: container}); err != nil {
				t.Fatal(err)
			}
			if _, err := h.Session.PutObject(ctx, container, "keep", strings.NewReader("untouched"), "text/plain", nil); err != nil {
				t.Fatal(err)
			}
			witness, err := oracle.Connect(os.Getenv("AERONAUTES_SAIO_AUTH_URL"), envOr("AERONAUTES_SAIO_USER", "test:tester"), envOr("AERONAUTES_SAIO_KEY", "testing"))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(witness.Close)
			snapshot := func() *oracle.AccountSnapshot {
				t.Helper()
				state, err := witness.SnapshotContainers(container)
				if err != nil {
					t.Fatal(err)
				}
				return state
			}
			before := snapshot()
			storage, err := url.Parse(h.Session.StorageURL())
			if err != nil {
				t.Fatal(err)
			}
			selector := swifttest.HTTPResponseSelector{Operation: t.Name(), Method: http.MethodPut, EscapedPath: storage.EscapedPath() + "/" + url.PathEscape(container) + "/object", Occurrence: 1}
			barrier, err := swifttest.NewHTTPResponseBarrier(selector, lose, 5*time.Second)
			if err != nil {
				t.Fatal(err)
			}
			transport := http.DefaultTransport.(*http.Transport).Clone()
			transport.DisableCompression = true
			transport.ResponseHeaderTimeout = 5 * time.Second
			t.Cleanup(transport.CloseIdleConnections)
			proxy := httputil.NewSingleHostReverseProxy(saioProxyTarget(t))
			proxy.Transport = transport
			var forwarded atomic.Int32
			front := httptest.NewUnstartedServer(barrier.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == selector.Method && r.URL.EscapedPath() == selector.EscapedPath && r.URL.RawQuery == selector.RawQuery {
					forwarded.Add(1)
				}
				proxy.ServeHTTP(w, r)
			})))
			front.Config.ReadHeaderTimeout = 5 * time.Second
			front.Config.WriteTimeout = 10 * time.Second
			front.Start()
			t.Cleanup(front.Close)
			t.Cleanup(barrier.Close)
			// This raw client has no Expect handshake or retry policy;
			// authentication and the witness stay outside the fault endpoint.
			requestTransport := http.DefaultTransport.(*http.Transport).Clone()
			requestTransport.DisableKeepAlives = true
			requestTransport.DisableCompression = true
			t.Cleanup(requestTransport.CloseIdleConnections)
			client := &http.Client{Transport: requestTransport, Timeout: 5 * time.Second,
				CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
			type putOutcome struct {
				status int
				err    error
			}
			done := make(chan putOutcome, 1)
			finished := make(chan struct{})
			t.Cleanup(func() {
				cancel()
				barrier.Close()
				select {
				case <-finished:
				case <-time.After(5 * time.Second):
					t.Error("raw PUT remained blocked after fixture cancellation")
				}
			})
			const payload = "landed real Swift data"
			go func() {
				defer close(finished)
				req, err := http.NewRequestWithContext(ctx, http.MethodPut, front.URL+selector.EscapedPath, strings.NewReader(payload))
				if err != nil {
					done <- putOutcome{err: err}
					return
				}
				req.Header.Set("X-Auth-Token", h.Session.AuthToken())
				req.Header.Set("Content-Type", "text/plain")
				resp, err := client.Do(req)
				result := putOutcome{err: err}
				if resp != nil {
					result.status = resp.StatusCode
					_, result.err = io.Copy(io.Discard, resp.Body)
					resp.Body.Close()
				}
				done <- result
			}()
			event, err := barrier.Wait(ctx)
			if err != nil || event.HTTPResponseSelector != selector || event.StatusCode != http.StatusCreated {
				t.Fatalf("real PUT event=%+v err=%v", event, err)
			}
			landed := snapshot()
			want := oracle.Delta{AddedObjects: []oracle.ObjectChange{{Container: container, Object: "object"}}}
			if delta := oracle.Diff(before, landed); !reflect.DeepEqual(delta, want) {
				t.Fatalf("real PUT collateral delta:\n%s", delta)
			}
			object := landed.Containers[container].Objects["object"]
			if object.Size != int64(len(payload)) || object.Digest != fmt.Sprintf("%x", sha256.Sum256([]byte(payload))) {
				t.Fatalf("real PUT stored representation=%+v", object)
			}
			if err := barrier.Release(); err != nil {
				t.Fatal(err)
			}
			select {
			case result := <-done:
				if (lose && (result.err == nil || result.status != 0)) || (!lose && (result.err != nil || result.status != http.StatusCreated)) || barrier.Err() != nil {
					t.Fatalf("real PUT lose=%v client=%+v fixture=%v", lose, result, barrier.Err())
				}
			case <-ctx.Done():
				t.Fatal("real PUT did not finish after barrier release")
			}
			if delta := oracle.Diff(landed, snapshot()); !delta.Empty() || forwarded.Load() != 1 {
				t.Fatalf("raw PUT forwarded=%d; delta after response:\n%s", forwarded.Load(), delta)
			}
		})
	}
}
