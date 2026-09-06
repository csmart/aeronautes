package copy

import (
	"context"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"

	"github.com/csmart/aeronautes/internal/swiftcontrol"
	"github.com/csmart/aeronautes/internal/swifttest"
	"github.com/csmart/aeronautes/internal/systemtest/oracle"
	"github.com/csmart/aeronautes/internal/transfer/plan"
)

func TestConditionalCreationPreservesRacers(t *testing.T) {
	for _, kind := range []string{"dlo", "staged-symlink", "native", "native-symlink"} {
		t.Run(kind, func(t *testing.T) {
			selector := swifttest.HTTPResponseSelector{Operation: t.Name(), Method: http.MethodHead, EscapedPath: "/v1/AUTH_test/backup/object", Occurrence: 1}
			status := http.StatusNotFound
			if kind == "native" {
				// The direct native path has no final HEAD. Holding its
				// source GET acknowledges the boundary before the live PUT.
				selector.Method, selector.EscapedPath, status = http.MethodGet, "/v1/AUTH_test/media/object", http.StatusOK
			}
			barrier, err := swifttest.NewHTTPResponseBarrier(selector, false, 5*time.Second)
			if err != nil {
				t.Fatal(err)
			}
			var w *world
			if kind == "native" {
				w = newWorld(t)
				server := httptest.NewServer(barrier.Wrap(w.srcMock))
				t.Cleanup(server.Close)
				t.Cleanup(barrier.Close)
				w.src, err = swiftcontrol.New(swiftcontrol.Config{AuthURL: server.URL + "/auth/v1.0", Username: "test:tester", APIKey: "testing"})
				if err != nil {
					t.Fatal(err)
				}
			} else {
				w = newWorldWithResponseBarrier(t, barrier)
			}
			seed(w.srcMock, "media", "object", []byte("ours"), nil)
			seed(w.srcMock, "media", "target", []byte("ours"), nil)
			if kind == "staged-symlink" || kind == "native-symlink" {
				w.srcMock.PutObject("media", "object", swifttest.Object{SymlinkTarget: "media/target"})
			}
			seed(w.dstMock, "unrelated", "keep", []byte("outside"), nil)
			witness := copyOracle(t, w.dstMock)
			srcWitness := copyOracle(t, w.srcMock)
			srcBefore := refusalSnapshot(t, srcWitness, "media")
			engine := &fakeEngine{w: w, interruptAfter: -1}
			if kind == "dlo" {
				engine.chunk = 2
			}
			route := plan.RouteRclone
			if kind == "native" || kind == "native-symlink" {
				route = plan.RouteNative
			}
			executor := newExecutor(t, w, engine)
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			type result struct {
				report *Report
				err    error
			}
			done := make(chan result, 1)
			finished := make(chan struct{})
			t.Cleanup(func() {
				cancel()
				barrier.Close()
				select {
				case <-finished:
				case <-time.After(5 * time.Second):
					t.Error("copy did not stop after fixture cancellation")
				}
			})
			go func() {
				defer close(finished)
				report, err := executor.Run(ctx, []plan.Item{{Name: "object", Verdict: plan.VerdictNew, Route: route, SourceBytes: 4}})
				done <- result{report, err}
			}()
			event, err := barrier.Wait(ctx)
			if err != nil || event.HTTPResponseSelector != selector || event.StatusCode != status {
				t.Fatalf("write boundary: %+v, %v", event, err)
			}
			seed(w.dstMock, "backup", "object", []byte("concurrent writer"), nil)
			expected := refusalSnapshot(t, witness, "backup", "backup_segments", "unrelated")
			// Refused conditional commits may remove their exact staged
			// manifest, but must preserve the racer and all backing bytes.
			delete(expected.Containers["backup"].Objects, executor.StagingPrefix()+"batch/object")
			if err := barrier.Release(); err != nil {
				t.Fatal(err)
			}
			select {
			case result := <-done:
				if result.err != nil || result.report.Copied != 0 || result.report.OverwritesRefused != 0 || !reflect.DeepEqual(result.report.Conflicts, []string{"object"}) || len(result.report.Failures) != 0 {
					t.Errorf("conditional creation did not report conflict: %+v, %v", result.report, result.err)
				}
			case <-ctx.Done():
				t.Fatal("copy did not finish after releasing its write boundary")
			}
			if err := barrier.Err(); err != nil {
				t.Fatal(err)
			}
			if diff := oracle.Diff(expected, refusalSnapshot(t, witness, "backup", "backup_segments", "unrelated")); !diff.Empty() {
				t.Errorf("conditional write harmed a competing writer or backing data: %s", diff)
			}
			if diff := oracle.Diff(srcBefore, refusalSnapshot(t, srcWitness, "media")); !diff.Empty() {
				t.Errorf("conditional copy changed source data: %s", diff)
			}
		})
	}
}
