package swifttest_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/csmart/aeronautes/internal/swifttest"
)

type httpResult struct {
	status int
	header http.Header
	body   string
	err    error
}

func barrier(t *testing.T, selector swifttest.HTTPResponseSelector, lose bool, timeout time.Duration) *swifttest.HTTPResponseBarrier {
	t.Helper()
	b, err := swifttest.NewHTTPResponseBarrier(selector, lose, timeout)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func responseServer(t *testing.T, b *swifttest.HTTPResponseBarrier, next http.Handler) (string, *http.Client, <-chan struct{}) {
	t.Helper()
	firstFinished := make(chan struct{})
	var once sync.Once
	wrapped := b.Wrap(next)
	s := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer once.Do(func() { close(firstFinished) })
		wrapped.ServeHTTP(w, r)
	}))
	s.Config.ReadHeaderTimeout = 5 * time.Second
	s.Config.WriteTimeout = 10 * time.Second
	s.Start()
	t.Cleanup(s.Close)
	t.Cleanup(b.Close)
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.DisableKeepAlives = true
	transport.DisableCompression = true
	t.Cleanup(transport.CloseIdleConnections)
	client := &http.Client{
		Transport: transport, Timeout: 5 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	return s.URL, client, firstFinished
}

func request(t *testing.T, client *http.Client, ctx context.Context, method, url string) <-chan httpResult {
	t.Helper()
	ctx, cancel := context.WithCancel(ctx)
	done := make(chan httpResult, 1)
	finished := make(chan struct{})
	t.Cleanup(func() {
		cancel()
		select {
		case <-finished:
		case <-time.After(6 * time.Second):
			t.Error("client remained blocked after cancellation")
		}
	})
	go func() {
		defer close(finished)
		req, err := http.NewRequestWithContext(ctx, method, url, nil)
		if err != nil {
			done <- httpResult{err: err}
			return
		}
		resp, err := client.Do(req)
		if err != nil {
			done <- httpResult{err: err}
			return
		}
		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		done <- httpResult{status: resp.StatusCode, header: resp.Header.Clone(), body: string(body), err: err}
	}()
	return done
}

func TestHTTPResponseBarrierCountsArrivalBeforeCompletion(t *testing.T) {
	selector := swifttest.HTTPResponseSelector{Operation: t.Name(), Method: "PUT", EscapedPath: "/object", Occurrence: 2}
	b := barrier(t, selector, false, 5*time.Second)
	firstEntered, releaseFirst := make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	base, client, _ := responseServer(t, b, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			close(firstEntered)
			select {
			case <-releaseFirst:
			case <-r.Context().Done():
				return
			}
			w.WriteHeader(http.StatusCreated)
			return
		}
		w.WriteHeader(http.StatusAccepted)
	}))
	var once sync.Once
	t.Cleanup(func() { once.Do(func() { close(releaseFirst) }) })
	first := request(t, client, context.Background(), "PUT", base+"/object")
	signal(t, firstEntered)
	second := request(t, client, context.Background(), "PUT", base+"/object")
	event, err := b.Wait(context.Background())
	if err != nil || event.Occurrence != 2 || event.StatusCode != http.StatusAccepted {
		t.Fatalf("out-of-order event=%+v err=%v", event, err)
	}
	if err := b.Release(); err != nil {
		t.Fatal(err)
	}
	if result := receive(t, second); result.err != nil || result.status != http.StatusAccepted {
		t.Fatalf("second response=%+v", result)
	}
	once.Do(func() { close(releaseFirst) })
	if result := receive(t, first); result.err != nil || result.status != http.StatusCreated {
		t.Fatalf("first response=%+v", result)
	}
}

func TestHTTPResponseBarrierMissingArrivalAbortsLateEffect(t *testing.T) {
	for _, cancelWait := range []bool{false, true} {
		t.Run(map[bool]string{false: "timeout", true: "cancel"}[cancelWait], func(t *testing.T) {
			b := barrier(t, swifttest.HTTPResponseSelector{Operation: t.Name(), Method: "PUT", EscapedPath: "/object", Occurrence: 1}, false, 100*time.Millisecond)
			var effects atomic.Int32
			base, client, finished := responseServer(t, b, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				effects.Add(1)
			}))
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			want := context.DeadlineExceeded
			if cancelWait {
				cancel()
				want = context.Canceled
			}
			event, err := b.Wait(ctx)
			if !errors.Is(err, want) || event.StatusCode != 0 || !strings.Contains(err.Error(), t.Name()) {
				t.Fatalf("missing arrival event=%+v err=%v", event, err)
			}
			if !cancelWait && ctx.Err() != nil {
				t.Fatal("arrival wait used the caller fallback instead of its own bound")
			}
			result := receive(t, request(t, client, context.Background(), "PUT", base+"/object"))
			signal(t, finished)
			if result.err == nil || effects.Load() != 0 {
				t.Fatalf("late request performed effect: %+v, effects=%d", result, effects.Load())
			}
			if err := b.Release(); err == nil {
				t.Fatal("aborted barrier accepted a release")
			}
			b.Close()
			b.Close()
		})
	}
}

func TestHTTPResponseBarrierAbortsHeldExchange(t *testing.T) {
	for _, mode := range []string{"close", "cancel", "timeout"} {
		t.Run(mode, func(t *testing.T) {
			timeout := 5 * time.Second
			if mode == "timeout" {
				timeout = 250 * time.Millisecond
			}
			b := barrier(t, swifttest.HTTPResponseSelector{Operation: t.Name(), Method: "PUT", EscapedPath: "/object", Occurrence: 1}, true, timeout)
			base, client, finished := responseServer(t, b, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusCreated)
			}))
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			done := request(t, client, ctx, "PUT", base+"/object")
			if event, err := b.Wait(context.Background()); err != nil || event.StatusCode != http.StatusCreated {
				t.Fatalf("arrival=%+v %v", event, err)
			}
			switch mode {
			case "close":
				b.Close()
				b.Close()
			case "cancel":
				cancel()
			}
			var result httpResult
			select {
			case result = <-done:
			case <-time.After(time.Second):
				t.Fatal("abort did not release the held exchange")
			}
			signal(t, finished)
			if result.err == nil || result.status != 0 || b.Err() == nil {
				t.Fatalf("abort mistaken for deliberate loss: result=%+v barrier=%v", result, b.Err())
			}
			if err := b.Release(); err == nil {
				t.Fatal("release after abort succeeded")
			}
			if mode == "timeout" && !errors.Is(b.Err(), context.DeadlineExceeded) {
				t.Fatalf("held request did not reach its own deadline: %v", b.Err())
			}
		})
	}
}

func TestHTTPResponseBarrierWaitsForHandlerReturn(t *testing.T) {
	b := barrier(t, swifttest.HTTPResponseSelector{Operation: t.Name(), Method: "PUT", EscapedPath: "/object", Occurrence: 1}, false, 5*time.Second)
	entered, allowReturn := make(chan struct{}), make(chan struct{})
	base, client, _ := responseServer(t, b, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
		close(entered)
		select {
		case <-allowReturn:
		case <-r.Context().Done():
		}
	}))
	var once sync.Once
	t.Cleanup(func() { once.Do(func() { close(allowReturn) }) })
	done := request(t, client, context.Background(), "PUT", base+"/object")
	signal(t, entered)
	type arrival struct {
		event swifttest.HTTPResponseEvent
		err   error
	}
	arrived := make(chan arrival, 1)
	waitFinished := make(chan struct{})
	t.Cleanup(func() { b.Close(); signal(t, waitFinished) })
	go func() {
		defer close(waitFinished)
		event, err := b.Wait(context.Background())
		arrived <- arrival{event, err}
	}()
	select {
	case result := <-arrived:
		t.Fatalf("handler still running at acknowledgement: %+v", result)
	case <-time.After(50 * time.Millisecond):
	}
	once.Do(func() { close(allowReturn) })
	result := <-arrived
	if result.err != nil || result.event.StatusCode != http.StatusCreated {
		t.Fatalf("completed handler arrival=%+v", result)
	}
	if err := b.Release(); err != nil {
		t.Fatal(err)
	}
	if result := receive(t, done); result.err != nil || result.status != http.StatusCreated {
		t.Fatalf("delivered response=%+v", result)
	}
	if err := b.Release(); err != nil {
		t.Fatalf("repeated release=%v", err)
	}
}

func TestHTTPResponseBarrierRefusesExpectBeforeEffect(t *testing.T) {
	b := barrier(t, swifttest.HTTPResponseSelector{Operation: t.Name(), Method: "PUT", EscapedPath: "/object", Occurrence: 1}, false, 5*time.Second)
	var effects atomic.Int32
	base, client, finished := responseServer(t, b, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		effects.Add(1)
		_, _ = io.Copy(io.Discard, r.Body)
		w.WriteHeader(http.StatusCreated)
	}))
	result := make(chan error, 1)
	requestFinished := make(chan struct{})
	t.Cleanup(func() { b.Close(); signal(t, requestFinished) })
	go func() {
		defer close(requestFinished)
		req, err := http.NewRequest(http.MethodPut, base+"/object", strings.NewReader("payload"))
		if err != nil {
			result <- err
			return
		}
		req.Header.Set("Expect", "100-continue")
		resp, err := client.Do(req)
		if resp != nil {
			resp.Body.Close()
		}
		result <- err
	}()
	event, err := b.Wait(context.Background())
	if err == nil || !strings.Contains(err.Error(), "Expect unsupported") || event.StatusCode != 0 {
		t.Fatalf("Expect event=%+v err=%v", event, err)
	}
	if err := <-result; err == nil {
		t.Fatal("unsupported Expect request received a response")
	}
	signal(t, finished)
	if effects.Load() != 0 {
		t.Fatalf("unsupported Expect request reached handler %d times", effects.Load())
	}
}

func TestHTTPResponseBarrierLeavesOtherTrafficStreaming(t *testing.T) {
	b := barrier(t, swifttest.HTTPResponseSelector{Operation: t.Name(), Method: "PUT", EscapedPath: "/object", Occurrence: 1}, false, 5*time.Second)
	release := make(chan struct{})
	base, client, _ := responseServer(t, b, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "one")
		w.(http.Flusher).Flush()
		select {
		case <-release:
		case <-r.Context().Done():
			return
		}
		_, _ = io.WriteString(w, "two")
	}))
	var once sync.Once
	t.Cleanup(func() { once.Do(func() { close(release) }) })
	resp, err := client.Get(base + "/other")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	first := make([]byte, 3)
	if _, err := io.ReadFull(resp.Body, first); err != nil || string(first) != "one" {
		t.Fatalf("unselected first chunk=%q err=%v", first, err)
	}
	once.Do(func() { close(release) })
	rest, err := io.ReadAll(resp.Body)
	if err != nil || string(rest) != "two" {
		t.Fatalf("unselected later chunk=%q err=%v", rest, err)
	}
}

func TestHTTPResponseBarrierRefusesUnsupportedResponses(t *testing.T) {
	cases := []struct {
		name, errorText string
		handler         http.HandlerFunc
	}{
		{"flush", "flush unsupported", func(w http.ResponseWriter, r *http.Request) { w.(http.Flusher).Flush() }},
		{"hijack", "hijack unsupported", func(w http.ResponseWriter, r *http.Request) { _, _, _ = w.(http.Hijacker).Hijack() }},
		{"informational", "unsupported captured response status", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusEarlyHints) }},
		{"trailers", "trailers unsupported", func(w http.ResponseWriter, r *http.Request) { w.Header().Set("Trailer", "X-Receipt") }},
		{"overflow", "exceeds 65536 bytes", func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, strings.Repeat("x", 65537)) }},
		{"panic", "selected handler panicked", func(w http.ResponseWriter, r *http.Request) { panic("fixture failure") }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			b := barrier(t, swifttest.HTTPResponseSelector{Operation: t.Name(), Method: "PUT", EscapedPath: "/object", Occurrence: 1}, false, 5*time.Second)
			base, client, finished := responseServer(t, b, tc.handler)
			done := request(t, client, context.Background(), "PUT", base+"/object")
			event, err := b.Wait(context.Background())
			if err == nil || !strings.Contains(err.Error(), tc.errorText) || event.StatusCode != 0 {
				t.Fatalf("unsupported response event=%+v err=%v", event, err)
			}
			if result := receive(t, done); result.err == nil || result.status != 0 {
				t.Fatalf("unsupported response reached client: %+v", result)
			}
			signal(t, finished)
		})
	}
}

func receive(t *testing.T, done <-chan httpResult) httpResult {
	t.Helper()
	select {
	case result := <-done:
		return result
	case <-time.After(6 * time.Second):
		t.Fatal("client did not finish within its deadline")
		return httpResult{}
	}
}

func signal(t *testing.T, done <-chan struct{}) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("handler did not finish within its deadline")
	}
}

func TestHTTPResponseBarrierHoldsCompletedResponse(t *testing.T) {
	for _, lose := range []bool{false, true} {
		t.Run(map[bool]string{false: "deliver", true: "lose"}[lose], func(t *testing.T) {
			selector := swifttest.HTTPResponseSelector{Operation: t.Name(), Method: "PUT", EscapedPath: "/object", Occurrence: 1}
			b := barrier(t, selector, lose, 5*time.Second)
			var effects atomic.Int32
			base, client, finished := responseServer(t, b, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				effects.Add(1)
				w.Header().Set("X-Receipt", "original")
				w.WriteHeader(http.StatusCreated)
				_, _ = io.WriteString(w, "receipt")
				w.Header().Set("X-Receipt", "too late")
			}))
			if err := b.Release(); err == nil {
				t.Fatal("release before arrival succeeded")
			}
			done := request(t, client, context.Background(), "PUT", base+"/object")
			event, err := b.Wait(context.Background())
			if err != nil || event.StatusCode != http.StatusCreated || event.HTTPResponseSelector != selector || effects.Load() != 1 {
				t.Fatalf("event=%+v err=%v effects=%d", event, err, effects.Load())
			}
			// This interval observes non-delivery after the acknowledged
			// boundary; it does not choose when the server effect occurs.
			select {
			case result := <-done:
				t.Fatalf("response escaped while held: %+v", result)
			case <-time.After(50 * time.Millisecond):
			}
			if err := b.Release(); err != nil {
				t.Fatal(err)
			}
			result := receive(t, done)
			signal(t, finished)
			if lose {
				if result.err == nil || result.status != 0 {
					t.Fatalf("lost response returned an HTTP result: %+v", result)
				}
			} else if result.err != nil || result.status != http.StatusCreated || result.body != "receipt" || result.header.Get("X-Receipt") != "original" {
				t.Fatalf("delivered response changed: %+v", result)
			}
			if b.Err() != nil || effects.Load() != 1 {
				t.Fatalf("fault=%v effects=%d", b.Err(), effects.Load())
			}
		})
	}
}

func TestHTTPResponseBarrierSelectsWireOccurrence(t *testing.T) {
	selector := swifttest.HTTPResponseSelector{Operation: t.Name(), Method: "PUT", EscapedPath: "/a%2Fb", RawQuery: "x=1&x=2", Occurrence: 2}
	b := barrier(t, selector, false, 5*time.Second)
	var requests atomic.Int32
	base, client, _ := responseServer(t, b, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.WriteHeader(http.StatusNoContent)
	}))
	for _, input := range []struct{ method, path string }{
		{"GET", "/a%2Fb?x=1&x=2"},
		{"PUT", "/a/b?x=1&x=2"},
		{"PUT", "/a%2Fb?x=2&x=1"},
		{"PUT", "/a%2Fb?x=1&x=2"},
	} {
		result := receive(t, request(t, client, context.Background(), input.method, base+input.path))
		if result.err != nil || result.status != http.StatusNoContent {
			t.Fatalf("unselected request %+v was intercepted: %+v", input, result)
		}
	}
	done := request(t, client, context.Background(), "PUT", base+"/a%2Fb?x=1&x=2")
	event, err := b.Wait(context.Background())
	if err != nil || !reflect.DeepEqual(event.HTTPResponseSelector, selector) || event.StatusCode != http.StatusNoContent || requests.Load() != 5 {
		t.Fatalf("event=%+v err=%v requests=%d", event, err, requests.Load())
	}
	if err := b.Release(); err != nil {
		t.Fatal(err)
	}
	if result := receive(t, done); result.err != nil || result.status != http.StatusNoContent {
		t.Fatalf("selected response=%+v", result)
	}
	if result := receive(t, request(t, client, context.Background(), "PUT", base+"/a%2Fb?x=1&x=2")); result.err != nil {
		t.Fatalf("later occurrence intercepted: %+v", result)
	}
}
