package copy

import (
	"context"
	"crypto/sha256"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/csmart/aeronautes/internal/swifttest"
	"github.com/csmart/aeronautes/internal/systemtest/oracle"
	"github.com/csmart/aeronautes/internal/transfer/plan"
)

func copyOracle(t *testing.T, mock *swifttest.Server) *oracle.Oracle {
	t.Helper()
	// The witness has its own listener over the same storage, so faults
	// aimed at the product cannot alter the evidence used to judge it.
	server := httptest.NewServer(mock)
	t.Cleanup(server.Close)
	witness, err := oracle.Connect(server.URL+"/auth/v1.0", "test:tester", "testing")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(witness.Close)
	return witness
}

func copySnapshot(t *testing.T, witness *oracle.Oracle) *oracle.AccountSnapshot {
	t.Helper()
	snapshot, err := witness.SnapshotContainers("backup", "unrelated")
	if err != nil {
		t.Fatal(err)
	}
	return snapshot
}

func onlyAddedObject(t *testing.T, before, after *oracle.AccountSnapshot, name, body string) {
	t.Helper()
	delta := oracle.Diff(before, after)
	want := oracle.Delta{AddedObjects: []oracle.ObjectChange{{Container: "backup", Object: name}}}
	if !reflect.DeepEqual(delta, want) {
		t.Fatalf("unexpected storage delta:\n%s", delta)
	}
	object := after.Containers["backup"].Objects[name]
	if object.Size != int64(len(body)) || object.Digest != fmt.Sprintf("%x", sha256.Sum256([]byte(body))) {
		t.Fatalf("unexpected stored representation: %+v", object)
	}
}

func TestPlainCommitRacerSurvives(t *testing.T) {
	selector := swifttest.HTTPResponseSelector{Operation: t.Name(), Method: http.MethodHead, EscapedPath: "/v1/AUTH_test/backup/notes.txt", Occurrence: 1}
	barrier, err := swifttest.NewHTTPResponseBarrier(selector, false, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	w := newWorldWithResponseBarrier(t, barrier)
	seed(w.srcMock, "media", "notes.txt", []byte("ours"), nil)
	seed(w.dstMock, "unrelated", "keep", []byte("untouched"), nil)
	witness := copyOracle(t, w.dstMock)
	before := copySnapshot(t, witness)
	e := newExecutor(t, w, &fakeEngine{w: w, interruptAfter: -1})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	type outcome struct {
		report *Report
		err    error
	}
	done := make(chan outcome, 1)
	finished := make(chan struct{})
	t.Cleanup(func() {
		cancel()
		barrier.Close()
		select {
		case <-finished:
		case <-time.After(5 * time.Second):
			t.Error("copy remained blocked after fixture cancellation")
		}
	})
	go func() {
		defer close(finished)
		report, err := e.Run(ctx, []plan.Item{{Name: "notes.txt", Verdict: plan.VerdictNew, Route: plan.RouteRclone, SourceBytes: 4}})
		done <- outcome{report, err}
	}()
	event, err := barrier.Wait(ctx)
	if err != nil || event.HTTPResponseSelector != selector || event.StatusCode != http.StatusNotFound {
		t.Fatalf("final HEAD event=%+v err=%v", event, err)
	}
	const raced = "racer's data, longer"
	seed(w.dstMock, "backup", "notes.txt", []byte(raced), nil)
	if err := barrier.Release(); err != nil {
		t.Fatal(err)
	}
	var result outcome
	select {
	case result = <-done:
	case <-ctx.Done():
		t.Fatal("copy did not finish after barrier release")
	}
	if result.report == nil {
		t.Fatalf("copy returned no report: %v", result.err)
	}
	if result.err != nil || result.report.Copied != 0 || !reflect.DeepEqual(result.report.Conflicts, []string{"notes.txt"}) || len(result.report.Failures) != 0 {
		t.Errorf("copy result=%+v err=%v", result.report, result.err)
	}
	if barrier.Err() != nil {
		t.Fatal(barrier.Err())
	}
	onlyAddedObject(t, before, copySnapshot(t, witness), "notes.txt", raced)
}

func TestLandedPutWithLostResponse(t *testing.T) {
	selector := swifttest.HTTPResponseSelector{Operation: t.Name(), Method: http.MethodPut, EscapedPath: "/v1/AUTH_test/backup/object", Occurrence: 1}
	barrier, err := swifttest.NewHTTPResponseBarrier(selector, true, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	w := newWorldWithResponseBarrier(t, barrier)
	seed(w.dstMock, "unrelated", "keep", []byte("untouched"), nil)
	witness := copyOracle(t, w.dstMock)
	before := copySnapshot(t, witness)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	done := make(chan error, 1)
	finished := make(chan struct{})
	t.Cleanup(func() {
		cancel()
		barrier.Close()
		select {
		case <-finished:
		case <-time.After(5 * time.Second):
			t.Error("PUT remained blocked after fixture cancellation")
		}
	})
	go func() {
		defer close(finished)
		client := &http.Client{Timeout: 5 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
		req, err := http.NewRequestWithContext(ctx, http.MethodPut, w.dst.StorageURL()+"/backup/object", strings.NewReader("landed data"))
		if err != nil {
			done <- err
			return
		}
		req.Header.Set("X-Auth-Token", w.dst.AuthToken())
		req.Header.Set("Content-Type", "text/plain")
		resp, err := client.Do(req)
		if resp != nil {
			resp.Body.Close()
		}
		done <- err
	}()
	if event, err := barrier.Wait(ctx); err != nil || event.StatusCode != http.StatusCreated {
		t.Fatalf("PUT event=%+v err=%v", event, err)
	}
	landed := copySnapshot(t, witness)
	onlyAddedObject(t, before, landed, "object", "landed data")
	if err := barrier.Release(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err == nil || barrier.Err() != nil {
			t.Fatalf("lost response: client=%v fixture=%v", err, barrier.Err())
		}
	case <-ctx.Done():
		t.Fatal("PUT did not finish after response loss")
	}
	if delta := oracle.Diff(landed, copySnapshot(t, witness)); !delta.Empty() {
		t.Fatalf("response loss changed storage:\n%s", delta)
	}
	mutations := 0
	for _, line := range w.dstMock.RequestLog() {
		if line == "PUT /v1/AUTH_test/backup/object" {
			mutations++
		}
	}
	if mutations != 1 {
		t.Fatalf("raw PUT attempted %d mutations, want exactly one", mutations)
	}
}
