package swift

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type ownedTestTransport func(*http.Request) (*http.Response, error)

func (f ownedTestTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func ownedTestConnection(transport http.RoundTripper) *Connection {
	return &Connection{Transport: transport, StorageUrl: "http://storage.invalid/v1/account", AuthToken: "token", Retries: -1, ConnectTimeout: 100 * time.Millisecond, Timeout: 100 * time.Millisecond}
}

func ownedTestOptions(c *Connection) RequestOpts {
	return RequestOpts{OwnedRead: true, Container: "container", ObjectName: "object", Operation: http.MethodGet, OnReAuth: func() (string, error) { return c.StorageUrl, nil }}
}

func ownedTestWait(t *testing.T, ch <-chan struct{}, what string) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(3 * time.Second):
		t.Fatalf("did not observe %s", what)
	}
}

func ownedTestPending(t *testing.T, ch <-chan struct{}, what string) {
	t.Helper()
	select {
	case <-ch:
		t.Errorf("%s returned before its underlying work joined", what)
	case <-time.After(25 * time.Millisecond):
	}
}

type ownedTestBody struct {
	reads    atomic.Int32
	closes   atomic.Int32
	closeErr error
}

func (b *ownedTestBody) Read([]byte) (int, error) {
	b.reads.Add(1)
	return 0, errors.New("unused body was read")
}
func (b *ownedTestBody) Close() error { b.closes.Add(1); return b.closeErr }

func TestOwnedReadRejectsOtherOperations(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*RequestOpts)
		target string
	}{
		{"method", func(p *RequestOpts) { p.Operation = http.MethodHead }, ""},
		{"body", func(p *RequestOpts) { p.Body = strings.NewReader("") }, ""},
		{"no_response", func(p *RequestOpts) { p.NoResponse = true }, ""},
		{"parameters", func(p *RequestOpts) { p.Parameters = url.Values{} }, ""},
		{"range", func(p *RequestOpts) { p.Headers = Headers{"rAnGe": ""} }, ""},
		{"if_range", func(p *RequestOpts) { p.Headers = Headers{"If-Range": "tag"} }, ""},
		{"callback", func(p *RequestOpts) { p.OnReAuth = nil }, ""},
		{"initial_target", func(*RequestOpts) {}, "http://storage.invalid/v1/old"},
		{"container", func(p *RequestOpts) { p.Container = "" }, ""},
		{"object", func(p *RequestOpts) { p.ObjectName = "" }, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls atomic.Int32
			c := ownedTestConnection(ownedTestTransport(func(*http.Request) (*http.Response, error) {
				calls.Add(1)
				return nil, errors.New("unexpected dispatch")
			}))
			p := ownedTestOptions(c)
			tc.change(&p)
			resp, _, err := c.Call(context.Background(), tc.target, p)
			if err == nil || resp != nil || calls.Load() != 0 {
				t.Fatalf("invalid option: response=%v err=%v calls=%d", resp, err, calls.Load())
			}
		})
	}
}

func TestOwnedReadJoinsHeaderWork(t *testing.T) {
	for _, mode := range []string{"timeout", "cancellation", "response_and_error"} {
		t.Run(mode, func(t *testing.T) {
			entered, cancelled, release, finished, returned := make(chan struct{}), make(chan struct{}), make(chan struct{}), make(chan struct{}), make(chan struct{})
			var once sync.Once
			releaseWork := func() { once.Do(func() { close(release) }) }
			defer releaseWork()
			closeErr := errors.New("late close failed")
			body := &ownedTestBody{closeErr: closeErr}
			transportErr := errors.New("late transport error")
			c := ownedTestConnection(ownedTestTransport(func(r *http.Request) (*http.Response, error) {
				close(entered)
				<-r.Context().Done()
				close(cancelled)
				<-release
				defer close(finished)
				resp := &http.Response{StatusCode: 200, Header: http.Header{"X-Trans-Id": {"late-id"}}, Body: body}
				if mode == "response_and_error" {
					return resp, transportErr
				}
				return resp, nil
			}))
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			var resp *http.Response
			var headers Headers
			var err error
			go func() { defer close(returned); resp, headers, err = c.Call(ctx, "", ownedTestOptions(c)) }()
			t.Cleanup(func() {
				cancel()
				releaseWork()
				ownedTestWait(t, returned, "Call cleanup")
				ownedTestWait(t, finished, "Do cleanup")
			})
			ownedTestWait(t, entered, "Do entry")
			if mode != "timeout" {
				cancel()
			}
			ownedTestWait(t, cancelled, "request cancellation")
			ownedTestPending(t, returned, "Call")
			releaseWork()
			ownedTestWait(t, returned, "Call completion")
			ownedTestWait(t, finished, "Do completion")
			want := error(context.Canceled)
			if mode == "timeout" {
				want = TimeoutError
			}
			if resp != nil || !errors.Is(err, want) || !errors.Is(err, closeErr) || headers["X-Trans-Id"] != "late-id" {
				t.Fatalf("late response=%v headers=%v err=%v", resp, headers, err)
			}
			if mode == "timeout" && errors.Is(err, context.Canceled) {
				t.Fatalf("timer reported as caller cancellation: %v", err)
			}
			if mode == "response_and_error" && !errors.Is(err, transportErr) {
				t.Fatalf("transport cause lost: %v", err)
			}
			if body.reads.Load() != 0 || body.closes.Load() != 1 {
				t.Fatalf("late body reads=%d closes=%d", body.reads.Load(), body.closes.Load())
			}
		})
	}
}

func TestOwnedReadRejectsWithoutDraining(t *testing.T) {
	for _, status := range []int{401, 403, 404, 503} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			closeErr := errors.New("rejected close failed")
			body := &ownedTestBody{closeErr: closeErr}
			c := ownedTestConnection(ownedTestTransport(func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": {"text/plain"}}, Body: body}, nil
			}))
			resp, _, err := c.Call(context.Background(), "", ownedTestOptions(c))
			var swiftErr *Error
			if resp != nil || !errors.As(err, &swiftErr) || swiftErr.StatusCode != status || !errors.Is(err, closeErr) {
				t.Fatalf("response=%v error=%v", resp, err)
			}
			if body.reads.Load() != 0 || body.closes.Load() != 1 {
				t.Fatalf("body reads=%d closes=%d", body.reads.Load(), body.closes.Load())
			}
			if status == 401 && c.Authenticated() {
				t.Fatal("401 left rejected native credentials authenticated")
			}
		})
	}
}

func TestOwnedReadHTTPControl(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.Header.Get("Accept-Encoding") != "identity" || r.Header.Get("X-Auth-Token") != "token" || r.URL.EscapedPath() != "/v1/account/container/a%25%3F%23/b" {
			t.Errorf("request headers=%v path=%q", r.Header, r.URL.EscapedPath())
		}
		w.Header().Set("Content-Encoding", "unknown")
		w.Header().Set("X-Trans-Id", "good-id")
		_, err := io.WriteString(w, "opaque bytes")
		if err != nil {
			t.Errorf("fixture write: %v", err)
		}
	}))
	defer server.Close()
	c := ownedTestConnection(server.Client().Transport)
	c.StorageUrl = server.URL + "/v1/account"
	p := ownedTestOptions(c)
	p.ObjectName = "a%?#/b"
	p.Headers = Headers{"accept-encoding": "gzip"}
	resp, headers, err := c.Call(context.Background(), "", p)
	if err != nil {
		t.Fatal(err)
	}
	got, readErr := io.ReadAll(resp.Body)
	closeErr := resp.Body.Close()
	if string(got) != "opaque bytes" || readErr != nil || closeErr != nil || headers["X-Trans-Id"] != "good-id" || resp.Header.Get("Content-Encoding") != "unknown" || resp.Uncompressed || requests.Load() != 1 {
		t.Fatalf("body=%q read=%v close=%v headers=%v requests=%d", got, readErr, closeErr, headers, requests.Load())
	}
	if err := resp.Body.Close(); err != nil {
		t.Fatalf("repeated close: %v", err)
	}
}

type ownedHeldBody struct {
	ctx                                   context.Context
	entered, cancelled, release, finished chan struct{}
	closes                                atomic.Int32
}

func (b *ownedHeldBody) Read(p []byte) (int, error) {
	close(b.entered)
	<-b.ctx.Done()
	close(b.cancelled)
	<-b.release
	p[0] = 'z'
	close(b.finished)
	return 1, b.ctx.Err()
}
func (b *ownedHeldBody) Close() error { b.closes.Add(1); return nil }

func TestOwnedReadJoinsBorrowedBuffer(t *testing.T) {
	for _, mode := range []string{"timeout", "cancellation", "close"} {
		t.Run(mode, func(t *testing.T) {
			body := &ownedHeldBody{entered: make(chan struct{}), cancelled: make(chan struct{}), release: make(chan struct{}), finished: make(chan struct{})}
			var once sync.Once
			release := func() { once.Do(func() { close(body.release) }) }
			defer release()
			c := ownedTestConnection(ownedTestTransport(func(r *http.Request) (*http.Response, error) {
				body.ctx = r.Context()
				return &http.Response{StatusCode: 200, Header: http.Header{}, Body: body}, nil
			}))
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			resp, _, err := c.Call(ctx, "", ownedTestOptions(c))
			if err != nil {
				t.Fatal(err)
			}
			buffer := []byte{'a'}
			returned := make(chan struct{})
			var n int
			var readErr error
			go func() { defer close(returned); n, readErr = resp.Body.Read(buffer) }()
			t.Cleanup(func() {
				cancel()
				release()
				ownedTestWait(t, returned, "Read cleanup")
				ownedTestWait(t, body.finished, "buffer writer cleanup")
				if err := resp.Body.Close(); err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, TimeoutError) {
					t.Errorf("unexpected body cleanup error: %v", err)
				}
			})
			ownedTestWait(t, body.entered, "body Read entry")
			closeReturned := make(chan struct{})
			var closeErr error
			switch mode {
			case "cancellation":
				cancel()
			case "close":
				go func() { defer close(closeReturned); closeErr = resp.Body.Close() }()
			}
			ownedTestWait(t, body.cancelled, "body context cancellation")
			ownedTestPending(t, returned, "Read")
			if mode == "close" {
				ownedTestPending(t, closeReturned, "Close")
			}
			release()
			ownedTestWait(t, returned, "Read return")
			ownedTestWait(t, body.finished, "buffer writer completion")
			if mode == "close" {
				ownedTestWait(t, closeReturned, "Close return")
			} else {
				closeErr = resp.Body.Close()
			}
			if n != 1 || buffer[0] != 'z' || readErr == nil || body.closes.Load() != 1 {
				t.Fatalf("read=%d/%q err=%v close=%v count=%d", n, buffer, readErr, closeErr, body.closes.Load())
			}
			if mode == "timeout" && (!errors.Is(readErr, TimeoutError) || errors.Is(readErr, context.Canceled)) {
				t.Fatalf("wrong idle timeout: %v", readErr)
			}
			if mode == "cancellation" && !errors.Is(readErr, context.Canceled) {
				t.Fatalf("wrong cancellation: %v", readErr)
			}
			buffer[0] = 'x'
			if _, err := resp.Body.Read(buffer); !errors.Is(err, io.ErrClosedPipe) {
				t.Fatalf("read after close=%v", err)
			}
		})
	}
}

func TestOwnedReadHTTPProgressAndCancellation(t *testing.T) {
	for _, protocol := range []string{"http1", "tls_http1", "http2"} {
		t.Run(protocol, func(t *testing.T) {
			for _, mode := range []string{"progress", "header_timeout", "body_timeout", "caller_cancel"} {
				t.Run(mode, func(t *testing.T) {
					entered, cancelled, finished := make(chan struct{}), make(chan struct{}), make(chan struct{})
					release := make(chan struct{})
					var releaseOnce sync.Once
					releaseAll := func() { releaseOnce.Do(func() { close(release) }) }
					defer releaseAll()
					var protocolMajor atomic.Int32
					server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						defer close(finished)
						protocolMajor.Store(int32(r.ProtoMajor))
						close(entered)
						if mode == "progress" {
							w.WriteHeader(200)
							w.(http.Flusher).Flush()
							for i := 0; i < 6; i++ {
								select {
								case <-release:
									return
								case <-time.After(35 * time.Millisecond):
								}
								if _, err := w.Write([]byte{'a' + byte(i)}); err != nil {
									t.Errorf("progress fixture write: %v", err)
									return
								}
								w.(http.Flusher).Flush()
							}
							return
						}
						if mode != "header_timeout" {
							w.WriteHeader(200)
							w.(http.Flusher).Flush()
						}
						select {
						case <-r.Context().Done():
							close(cancelled)
						case <-release:
						}
					}))
					if protocol == "http1" {
						server.Start()
					} else {
						server.EnableHTTP2 = protocol == "http2"
						server.StartTLS()
					}
					defer server.Close()
					c := ownedTestConnection(server.Client().Transport)
					c.StorageUrl = server.URL + "/v1/account"
					ctx, cancel := context.WithCancel(context.Background())
					defer cancel()
					start := time.Now()
					resp, _, err := c.Call(ctx, "", ownedTestOptions(c))
					ownedTestWait(t, entered, "HTTP handler")
					if mode == "header_timeout" {
						if resp != nil || !errors.Is(err, TimeoutError) || errors.Is(err, context.Canceled) {
							t.Fatalf("header timeout response=%v err=%v", resp, err)
						}
					} else {
						if err != nil {
							t.Fatal(err)
						}
						if mode == "caller_cancel" {
							cancel()
						}
						data, readErr := io.ReadAll(resp.Body)
						closeErr := resp.Body.Close()
						if mode == "progress" {
							if string(data) != "abcdef" || readErr != nil || closeErr != nil || time.Since(start) <= c.Timeout || time.Since(start) <= c.ConnectTimeout {
								t.Fatalf("progress=%q read=%v close=%v elapsed=%v", data, readErr, closeErr, time.Since(start))
							}
						} else {
							want := error(TimeoutError)
							if mode == "caller_cancel" {
								want = context.Canceled
							}
							if !errors.Is(readErr, want) {
								t.Fatalf("body failure=%v want=%v close=%v", readErr, want, closeErr)
							}
						}
					}
					if mode != "progress" {
						ownedTestWait(t, cancelled, "server cancellation")
					}
					ownedTestWait(t, finished, "HTTP handler completion")
					wantProtocol := int32(1)
					if protocol == "http2" {
						wantProtocol = 2
					}
					if protocolMajor.Load() != wantProtocol {
						t.Fatalf("negotiated HTTP/%d, want %d", protocolMajor.Load(), wantProtocol)
					}
					t.Logf("observed HTTP/%d mode=%s elapsed=%v; handler joined", protocolMajor.Load(), mode, time.Since(start))
				})
			}
		})
	}
}

func TestOwnedReadLeavesConcurrentLegacyCallUnchanged(t *testing.T) {
	entered, release, returned := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	releaseAll := func() { releaseOnce.Do(func() { close(release) }) }
	defer releaseAll()
	c := ownedTestConnection(ownedTestTransport(func(r *http.Request) (*http.Response, error) {
		if strings.HasSuffix(r.URL.Path, "/owned") {
			close(entered)
			<-r.Context().Done()
			<-release
			return nil, r.Context().Err()
		}
		if r.Header.Get("Accept-Encoding") != "" {
			t.Errorf("owned encoding option leaked to legacy request: %v", r.Header)
		}
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader("legacy"))}, nil
	}))
	c.Transport = &struct{ http.RoundTripper }{c.Transport}
	c.setDefaults()
	originalClient, originalTransport, originalTimeout := c.client, c.Transport, c.Timeout
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var ownedErr error
	p := ownedTestOptions(c)
	p.ObjectName = "owned"
	go func() { defer close(returned); _, _, ownedErr = c.Call(ctx, "", p) }()
	t.Cleanup(func() { cancel(); releaseAll(); ownedTestWait(t, returned, "owned Call cleanup") })
	ownedTestWait(t, entered, "owned request")
	resp, _, err := c.Call(context.Background(), c.StorageUrl, RequestOpts{Operation: http.MethodGet, Container: "container", ObjectName: "legacy"})
	if err != nil {
		t.Fatal(err)
	}
	got, readErr := io.ReadAll(resp.Body)
	closeErr := resp.Body.Close()
	cancel()
	releaseAll()
	ownedTestWait(t, returned, "owned Call completion")
	if string(got) != "legacy" || readErr != nil || closeErr != nil || !errors.Is(ownedErr, context.Canceled) || c.client != originalClient || c.Transport != originalTransport || c.Timeout != originalTimeout || c.client.CheckRedirect != nil {
		t.Fatalf("legacy=%q read=%v close=%v owned=%v shared client or policy changed", got, readErr, closeErr, ownedErr)
	}
}
