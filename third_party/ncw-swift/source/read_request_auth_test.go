package swift

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type ownedTestAuth struct {
	request    func(context.Context, *Connection) (*http.Request, error)
	response   func(context.Context, *http.Response) error
	url, token string
	expires    time.Time
}

func (a *ownedTestAuth) Request(ctx context.Context, c *Connection) (*http.Request, error) {
	return a.request(ctx, c)
}
func (a *ownedTestAuth) Response(ctx context.Context, r *http.Response) error {
	return a.response(ctx, r)
}
func (a *ownedTestAuth) StorageUrl(bool) string { return a.url }
func (a *ownedTestAuth) Token() string          { return a.token }
func (a *ownedTestAuth) CdnUrl() string         { return "" }
func (a *ownedTestAuth) Expires() time.Time     { return a.expires }

func TestOwnedReadAuthBodiesAndPublication(t *testing.T) {
	for _, mode := range []string{"unused", "generic_body", "close_failure", "auth_rejected"} {
		t.Run(mode, func(t *testing.T) {
			closeErr := errors.New("auth close failure")
			body := &ownedTestBody{}
			if mode == "close_failure" {
				body.closeErr = closeErr
			}
			var authRequests, storageRequests, callbacks, responses int
			auth := &ownedTestAuth{}
			auth.request = func(ctx context.Context, c *Connection) (*http.Request, error) {
				return http.NewRequestWithContext(ctx, http.MethodPost, "http://auth.invalid/v1", strings.NewReader("auth request"))
			}
			auth.response = func(ctx context.Context, r *http.Response) error {
				responses++
				if mode == "close_failure" && auth.token != "token" {
					return errors.New("old accepted auth snapshot was lost before Response")
				}
				if body.closes.Load() != 0 {
					return errors.New("auth received a closed body")
				}
				if mode == "generic_body" {
					data, err := io.ReadAll(r.Body)
					if err != nil {
						return err
					}
					if string(data) != "auth response" {
						return errors.New("wrong generic auth body")
					}
				}
				auth.url = r.Header.Get("X-Storage-Url")
				auth.token = r.Header.Get("X-Auth-Token")
				return nil
			}
			c := ownedTestConnection(ownedTestTransport(func(r *http.Request) (*http.Response, error) {
				if r.URL.Host == "auth.invalid" {
					authRequests++
					if r.Body == nil {
						t.Error("generic auth lost its request body")
					} else {
						data, err := io.ReadAll(r.Body)
						closeErr := r.Body.Close()
						if string(data) != "auth request" || err != nil || closeErr != nil {
							t.Errorf("auth request=%q read=%v close=%v", data, err, closeErr)
						}
					}
					status := 200
					if mode == "auth_rejected" {
						status = 403
					}
					var responseBody io.ReadCloser = body
					if mode == "generic_body" {
						responseBody = io.NopCloser(strings.NewReader("auth response"))
					}
					return &http.Response{StatusCode: status, Header: http.Header{"X-Storage-Url": {"http://storage.invalid/v1/new"}, "X-Auth-Token": {"new-token"}}, Body: responseBody}, nil
				}
				storageRequests++
				if r.Context().Err() != nil || r.URL.Path != "/v1/new/container/object" || r.Header.Get("X-Auth-Token") != "new-token" {
					t.Errorf("storage request context=%v path=%q token=%q", r.Context().Err(), r.URL.Path, r.Header.Get("X-Auth-Token"))
				}
				return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader("good"))}, nil
			}))
			c.Auth = auth
			if mode == "close_failure" {
				auth.url, auth.token = c.StorageUrl, c.AuthToken
				if !c.Authenticated() {
					t.Fatal("control requires an accepted old native tuple")
				}
				c.Expires = time.Now().Add(30 * time.Second)
			} else {
				c.UnAuthenticate()
			}
			p := ownedTestOptions(c)
			p.OnReAuth = func() (string, error) {
				callbacks++
				if c.StorageUrl != auth.url || c.AuthToken != auth.token {
					return "", errors.New("mixed authentication tuple")
				}
				return auth.url, nil
			}
			resp, _, err := c.Call(context.Background(), "", p)
			if mode == "close_failure" || mode == "auth_rejected" {
				if resp != nil || err == nil || callbacks != 0 || storageRequests != 0 {
					t.Fatalf("auth failure response=%v err=%v callback=%d storage=%d", resp, err, callbacks, storageRequests)
				}
				if mode == "close_failure" && (!errors.Is(err, closeErr) || c.StorageUrl != auth.url || c.AuthToken != auth.token || c.AuthToken != "new-token" || responses != 1) {
					t.Fatalf("accepted tuple lost: url=%q token=%q responses=%d err=%v", c.StorageUrl, c.AuthToken, responses, err)
				}
				if mode == "auth_rejected" && (c.Authenticated() || responses != 0 || auth.token != "") {
					t.Fatalf("rejected auth was published: native=%v responses=%d snapshot=%q", c.Authenticated(), responses, auth.token)
				}
			} else {
				if err != nil {
					t.Fatal(err)
				}
				data, readErr := io.ReadAll(resp.Body)
				if err := resp.Body.Close(); err != nil {
					t.Fatal(err)
				}
				if string(data) != "good" || readErr != nil || storageRequests != 1 || callbacks != 1 {
					t.Fatalf("storage=%q err=%v requests=%d callbacks=%d", data, readErr, storageRequests, callbacks)
				}
			}
			if authRequests != 1 {
				t.Fatalf("auth requests=%d", authRequests)
			}
			if mode != "generic_body" && (body.reads.Load() != 0 || body.closes.Load() != 1) {
				t.Fatalf("unused auth body read=%d close=%d", body.reads.Load(), body.closes.Load())
			}
		})
	}
}

func TestOwnedRead401RetryAndCloseFailure(t *testing.T) {
	for _, mode := range []string{"success", "close_failure", "auth_failure", "revoked"} {
		t.Run(mode, func(t *testing.T) {
			closeErr := errors.New("401 close failure")
			revoked := errors.New("request no longer admitted")
			body := &ownedTestBody{}
			if mode == "close_failure" {
				body.closeErr = closeErr
			}
			var authRequests, storageRequests, attempts int
			c := ownedTestConnection(ownedTestTransport(func(r *http.Request) (*http.Response, error) {
				attempts++
				if mode == "revoked" && attempts > 1 {
					return nil, revoked
				}
				if r.URL.Host == "auth.invalid" {
					authRequests++
					status := 200
					if mode == "auth_failure" {
						status = 403
					}
					return &http.Response{StatusCode: status, Header: http.Header{"X-Storage-Url": {"http://storage.invalid/v1/new"}, "X-Auth-Token": {"new-token"}}, Body: &ownedTestBody{}}, nil
				}
				storageRequests++
				if storageRequests == 1 {
					if r.Header.Get("X-Auth-Token") != "token" {
						t.Error("missing initial token")
					}
					return &http.Response{StatusCode: 401, Header: http.Header{}, Body: body}, nil
				}
				if r.URL.Path != "/v1/new/container/object" || r.Header.Get("X-Auth-Token") != "new-token" || r.Context().Err() != nil {
					t.Errorf("retry used wrong pair or cancelled context: %v", r)
				}
				return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader("fresh"))}, nil
			}))
			c.Retries = 2
			c.AuthUrl = "http://auth.invalid/v1"
			c.AuthVersion = 1
			resp, _, err := c.Call(context.Background(), "", ownedTestOptions(c))
			if mode == "success" {
				if err != nil {
					t.Fatal(err)
				}
				data, readErr := io.ReadAll(resp.Body)
				closeErr := resp.Body.Close()
				if string(data) != "fresh" || readErr != nil || closeErr != nil || authRequests != 1 || storageRequests != 2 {
					t.Fatalf("retry=%q read=%v close=%v auth=%d storage=%d", data, readErr, closeErr, authRequests, storageRequests)
				}
			} else {
				if resp != nil || err == nil || storageRequests != 1 || c.Authenticated() {
					t.Fatalf("failure response=%v error=%v storage=%d native=%v", resp, err, storageRequests, c.Authenticated())
				}
				if mode == "close_failure" && (!errors.Is(err, AuthorizationFailed) || !errors.Is(err, closeErr) || authRequests != 0) {
					t.Fatalf("close refusal err=%v auth=%d", err, authRequests)
				}
				if mode == "revoked" && (!errors.Is(err, revoked) || authRequests != 0) {
					t.Fatalf("revoked err=%v auth=%d", err, authRequests)
				}
			}
			if body.reads.Load() != 0 || body.closes.Load() != 1 {
				t.Fatalf("401 body read=%d close=%d", body.reads.Load(), body.closes.Load())
			}
		})
	}
}

func TestOwnedReadRedirectDoesNotFollow(t *testing.T) {
	var targetRequests atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { targetRequests.Add(1) }))
	defer target.Close()
	body := &ownedTestBody{closeErr: errors.New("redirect close")}
	c := ownedTestConnection(ownedTestTransport(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 302, Header: http.Header{"Location": {target.URL}}, Body: body}, nil
	}))
	resp, headers, err := c.Call(context.Background(), "", ownedTestOptions(c))
	var swiftErr *Error
	if resp != nil || !errors.As(err, &swiftErr) || swiftErr.StatusCode != 302 || !errors.Is(err, body.closeErr) || headers["Location"] != target.URL || targetRequests.Load() != 0 || body.reads.Load() != 0 || body.closes.Load() != 1 {
		t.Fatalf("redirect response=%v headers=%v err=%v target=%d reads=%d closes=%d", resp, headers, err, targetRequests.Load(), body.reads.Load(), body.closes.Load())
	}
}

type ownedCountedBody struct {
	io.ReadCloser
	reads, closes atomic.Int32
}

func (b *ownedCountedBody) Read(p []byte) (int, error) { b.reads.Add(1); return b.ReadCloser.Read(p) }
func (b *ownedCountedBody) Close() error               { b.closes.Add(1); return b.ReadCloser.Close() }

func TestOwnedReadRawHTTPBodies(t *testing.T) {
	for _, kind := range []string{"v1_auth", "storage_401", "text_403"} {
		for _, stalled := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/stalled_%t", kind, stalled), func(t *testing.T) {
				selected, finished, cancelled := make(chan struct{}), make(chan struct{}), make(chan struct{})
				release := make(chan struct{})
				var releaseOnce sync.Once
				releaseAll := func() { releaseOnce.Do(func() { close(release) }) }
				defer releaseAll()
				var storageRequests, authRequests atomic.Int32
				var serverURL string
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					isAuth := r.URL.Path == "/v1.0"
					target := false
					status := 200
					if isAuth {
						authRequests.Add(1)
						w.Header().Set("X-Storage-Url", serverURL+"/v1/new")
						w.Header().Set("X-Auth-Token", "fresh")
						target = kind == "v1_auth"
					} else {
						n := storageRequests.Add(1)
						if n == 1 && kind == "storage_401" {
							target = true
							status = 401
						}
						if kind == "text_403" {
							target = true
							status = 403
							w.Header().Set("Content-Type", "text/plain")
						}
					}
					if !target {
						_, err := io.WriteString(w, "ok")
						if err != nil {
							t.Errorf("normal fixture write: %v", err)
						}
						return
					}
					defer close(finished)
					w.Header().Set("Content-Length", "100")
					w.WriteHeader(status)
					if !stalled {
						_, err := w.Write([]byte(strings.Repeat("x", 100)))
						if err != nil {
							t.Errorf("normal raw fixture write: %v", err)
						}
					}
					w.(http.Flusher).Flush()
					close(selected)
					if stalled {
						select {
						case <-r.Context().Done():
							close(cancelled)
						case <-release:
						}
					}
				}))
				serverURL = server.URL
				defer func() { releaseAll(); server.Close() }()
				var selectedBody *ownedCountedBody
				transport := server.Client().Transport
				c := ownedTestConnection(ownedTestTransport(func(r *http.Request) (*http.Response, error) {
					resp, err := transport.RoundTrip(r)
					if err == nil && ((kind == "v1_auth" && r.URL.Path == "/v1.0") || (kind == "storage_401" && resp.StatusCode == 401) || (kind == "text_403" && resp.StatusCode == 403)) {
						selectedBody = &ownedCountedBody{ReadCloser: resp.Body}
						resp.Body = selectedBody
					}
					return resp, err
				}))
				c.StorageUrl = serverURL + "/v1/old"
				c.AuthUrl = serverURL + "/v1.0"
				c.AuthVersion = 1
				c.Retries = 1
				if kind == "v1_auth" {
					c.UnAuthenticate()
				}
				ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
				defer cancel()
				resp, _, err := c.Call(ctx, "", ownedTestOptions(c))
				ownedTestWait(t, selected, "selected raw response headers")
				if kind == "text_403" {
					var statusErr *Error
					if resp != nil || !errors.As(err, &statusErr) || statusErr.StatusCode != 403 || errors.Is(err, context.Canceled) || errors.Is(err, TimeoutError) {
						t.Fatalf("text refusal response=%v err=%v", resp, err)
					}
				} else {
					if err != nil {
						t.Fatal(err)
					}
					data, readErr := io.ReadAll(resp.Body)
					closeErr := resp.Body.Close()
					if string(data) != "ok" || readErr != nil || closeErr != nil {
						t.Fatalf("storage control=%q read=%v close=%v", data, readErr, closeErr)
					}
				}
				if selectedBody == nil || selectedBody.reads.Load() != 0 || selectedBody.closes.Load() != 1 {
					t.Fatalf("raw body=%+v", selectedBody)
				}
				if stalled {
					ownedTestWait(t, cancelled, "raw response server cancellation")
				}
				ownedTestWait(t, finished, "raw response handler completion")
				wantStorage, wantAuth := int32(1), int32(1)
				if kind == "storage_401" {
					wantStorage = 2
				}
				if kind == "text_403" {
					wantAuth = 0
				}
				if storageRequests.Load() != wantStorage || authRequests.Load() != wantAuth {
					t.Fatalf("auth=%d storage=%d", authRequests.Load(), storageRequests.Load())
				}
			})
		}
	}
}

func TestOwnedReadCredentialBoundaries(t *testing.T) {
	for _, mode := range []string{"warm", "cold", "expiry", "unknown_expiry", "force_reauth_failure", "override", "callback_error", "empty_callback", "missing_native", "cancelled", "callback_cancels", "query_target"} {
		t.Run(mode, func(t *testing.T) {
			var authRequests, storageRequests, callbacks int
			callbackErr := errors.New("callback refused")
			c := ownedTestConnection(ownedTestTransport(func(r *http.Request) (*http.Response, error) {
				if r.URL.Host == "auth.invalid" {
					authRequests++
					status := 200
					if mode == "force_reauth_failure" {
						status = 403
					}
					header := http.Header{"X-Storage-Url": {"http://storage.invalid/v1/new"}, "X-Auth-Token": {"new-token"}}
					if mode == "missing_native" {
						header.Del("X-Auth-Token")
					}
					return &http.Response{StatusCode: status, Header: header, Body: &ownedTestBody{}}, nil
				}
				storageRequests++
				wantToken, wantHost, wantPath := "token", "storage.invalid", "/v1/account/container/object"
				if mode == "cold" || mode == "expiry" {
					wantToken = "new-token"
					wantPath = "/v1/new/container/object"
				}
				if mode == "override" {
					wantHost = "override.invalid"
					wantPath = "/v1/override/container/object"
				}
				if r.Header.Get("X-Auth-Token") != wantToken || r.URL.Host != wantHost || r.URL.Path != wantPath {
					t.Errorf("credential pair request=%s token=%q", r.URL, r.Header.Get("X-Auth-Token"))
				}
				return &http.Response{StatusCode: 200, Header: http.Header{}, Body: http.NoBody}, nil
			}))
			c.AuthUrl = "http://auth.invalid/v1"
			c.AuthVersion = 1
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			switch mode {
			case "cold", "force_reauth_failure", "missing_native":
				c.UnAuthenticate()
			case "expiry":
				c.Expires = time.Now().Add(30 * time.Second)
			case "unknown_expiry":
				c.Expires = time.Time{}
			case "cancelled":
				cancel()
			}
			p := ownedTestOptions(c)
			p.OnReAuth = func() (string, error) {
				callbacks++
				switch mode {
				case "override":
					return "http://override.invalid/v1/override", nil
				case "callback_error":
					return "", callbackErr
				case "empty_callback":
					return "", nil
				case "callback_cancels":
					cancel()
				case "query_target":
					return c.StorageUrl + "?multipart-manifest=get", nil
				}
				return c.StorageUrl, nil
			}
			resp, _, err := c.Call(ctx, "", p)
			failure := mode == "force_reauth_failure" || mode == "callback_error" || mode == "empty_callback" || mode == "missing_native" || mode == "cancelled" || mode == "callback_cancels" || mode == "query_target"
			if failure {
				if resp != nil || err == nil || storageRequests != 0 {
					t.Fatalf("refusal response=%v err=%v storage=%d", resp, err, storageRequests)
				}
			} else {
				if err != nil {
					t.Fatal(err)
				}
				if err := resp.Body.Close(); err != nil {
					t.Fatal(err)
				}
				if storageRequests != 1 {
					t.Fatalf("storage requests=%d", storageRequests)
				}
			}
			wantAuth := 0
			if mode == "cold" || mode == "expiry" || mode == "force_reauth_failure" || mode == "missing_native" {
				wantAuth = 1
			}
			if authRequests != wantAuth {
				t.Fatalf("auth requests=%d want=%d", authRequests, wantAuth)
			}
			if (mode == "force_reauth_failure" || mode == "missing_native" || mode == "cancelled") && callbacks != 0 {
				t.Fatalf("callback after refused authentication/cancellation=%d", callbacks)
			}
			if mode == "callback_error" && !errors.Is(err, callbackErr) {
				t.Fatalf("callback error lost: %v", err)
			}
			if (mode == "cancelled" || mode == "callback_cancels") && !errors.Is(err, context.Canceled) {
				t.Fatalf("caller cancellation lost: %v", err)
			}
		})
	}
}

func TestOwnedReadRetryBudget(t *testing.T) {
	for _, mode := range []string{"transport", "header_timeout", "auth_bad_request", "auth_unauthorized", "auth_close_failure"} {
		t.Run(mode, func(t *testing.T) {
			var requests int
			closeErr := errors.New("auth retry close failed")
			var firstBody *ownedTestBody
			c := ownedTestConnection(ownedTestTransport(func(r *http.Request) (*http.Response, error) {
				requests++
				if r.URL.Host == "auth.invalid" {
					if requests == 1 {
						status := 400
						if mode == "auth_unauthorized" {
							status = 401
						}
						firstBody = &ownedTestBody{}
						if mode == "auth_close_failure" {
							firstBody.closeErr = closeErr
						}
						return &http.Response{StatusCode: status, Header: http.Header{}, Body: firstBody}, nil
					}
					return &http.Response{StatusCode: 200, Header: http.Header{"X-Storage-Url": {"http://storage.invalid/v1/new"}, "X-Auth-Token": {"new-token"}}, Body: &ownedTestBody{}}, nil
				}
				if requests == 1 {
					if mode == "header_timeout" {
						<-r.Context().Done()
						firstBody = &ownedTestBody{}
						return &http.Response{StatusCode: 200, Header: http.Header{}, Body: firstBody}, nil
					}
					return nil, errors.New("temporary transport failure")
				}
				return &http.Response{StatusCode: 200, Header: http.Header{}, Body: http.NoBody}, nil
			}))
			c.Retries = 4
			p := ownedTestOptions(c)
			p.Retries = 1
			if strings.HasPrefix(mode, "auth_") {
				c.UnAuthenticate()
				c.AuthUrl = "http://auth.invalid/v1"
				c.AuthVersion = 1
			}
			resp, _, err := c.Call(context.Background(), "", p)
			if mode == "auth_close_failure" {
				if resp != nil || !errors.Is(err, closeErr) || requests != 1 {
					t.Fatalf("close failure response=%v err=%v requests=%d", resp, err, requests)
				}
			} else {
				if err != nil {
					t.Fatal(err)
				}
				if err := resp.Body.Close(); err != nil {
					t.Fatal(err)
				}
				want := 2
				if strings.HasPrefix(mode, "auth_") {
					want = 3
				}
				if requests != want {
					t.Fatalf("requests=%d want=%d", requests, want)
				}
			}
			if firstBody != nil && (firstBody.reads.Load() != 0 || firstBody.closes.Load() != 1) {
				t.Fatalf("first body reads=%d closes=%d", firstBody.reads.Load(), firstBody.closes.Load())
			}
			if c.Retries != 4 {
				t.Fatalf("per-call retry changed shared retries=%d", c.Retries)
			}
		})
	}
}

type ownedClosingBody struct {
	entered, release chan struct{}
	err              error
}

func (b *ownedClosingBody) Read([]byte) (int, error) { return 0, io.EOF }
func (b *ownedClosingBody) Close() error             { close(b.entered); <-b.release; return b.err }

func TestOwnedReadCancellationDuringClose(t *testing.T) {
	body := &ownedClosingBody{entered: make(chan struct{}), release: make(chan struct{})}
	var once sync.Once
	release := func() { once.Do(func() { close(body.release) }) }
	defer release()
	c := ownedTestConnection(ownedTestTransport(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: body}, nil
	}))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	resp, _, err := c.Call(ctx, "", ownedTestOptions(c))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.ReadAll(resp.Body); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	var closeErr error
	go func() { defer close(done); closeErr = resp.Body.Close() }()
	ownedTestWait(t, body.entered, "underlying Close entry")
	cancel()
	ownedTestPending(t, done, "Close")
	release()
	ownedTestWait(t, done, "Close return")
	if !errors.Is(closeErr, context.Canceled) {
		t.Fatalf("cancellation while Close joined was lost: %v", closeErr)
	}
}

func TestOwnedRead401WithTransportErrorInvalidates(t *testing.T) {
	transportErr := errors.New("response plus transport failure")
	closeErr := errors.New("response close failure")
	body := &ownedTestBody{closeErr: closeErr}
	c := ownedTestConnection(ownedTestTransport(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 401, Header: http.Header{}, Body: body}, transportErr
	}))
	resp, _, err := c.Call(context.Background(), "", ownedTestOptions(c))
	if resp != nil || !errors.Is(err, transportErr) || !errors.Is(err, closeErr) || c.Authenticated() || body.closes.Load() != 1 {
		t.Fatalf("rejected response=%v err=%v native=%v closes=%d", resp, err, c.Authenticated(), body.closes.Load())
	}
}

func TestOwnedReadRejectedAuthRequestClosesBody(t *testing.T) {
	for _, mode := range []string{"request_error", "cancel_before_dispatch"} {
		t.Run(mode, func(t *testing.T) {
			requestErr := errors.New("auth request refused")
			closeErr := errors.New("auth request close failed")
			body := &ownedTestBody{closeErr: closeErr}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			var dispatched atomic.Int32
			c := ownedTestConnection(ownedTestTransport(func(*http.Request) (*http.Response, error) {
				dispatched.Add(1)
				return nil, errors.New("must not dispatch")
			}))
			c.UnAuthenticate()
			c.Auth = &ownedTestAuth{request: func(ctx context.Context, _ *Connection) (*http.Request, error) {
				req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://auth.invalid/v1", body)
				if err != nil {
					return nil, err
				}
				if mode == "request_error" {
					return req, requestErr
				}
				cancel()
				return req, nil
			}}
			resp, _, err := c.Call(ctx, "", ownedTestOptions(c))
			want := error(requestErr)
			if mode == "cancel_before_dispatch" {
				want = context.Canceled
			}
			if resp != nil || !errors.Is(err, want) || !errors.Is(err, closeErr) || body.closes.Load() != 1 || dispatched.Load() != 0 {
				t.Fatalf("rejected request response=%v error=%v closes=%d dispatch=%d", resp, err, body.closes.Load(), dispatched.Load())
			}
		})
	}
}

type ownedErrorBody struct {
	ctx   context.Context
	extra error
}

func (b *ownedErrorBody) Read([]byte) (int, error) {
	<-b.ctx.Done()
	return 0, errors.Join(b.ctx.Err(), b.extra)
}
func (b *ownedErrorBody) Close() error { return nil }

func TestOwnedReadTimeoutKeepsOtherIOErrors(t *testing.T) {
	extra := errors.New("distinct I/O failure")
	body := &ownedErrorBody{extra: extra}
	c := ownedTestConnection(ownedTestTransport(func(r *http.Request) (*http.Response, error) {
		body.ctx = r.Context()
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: body}, nil
	}))
	resp, _, err := c.Call(context.Background(), "", ownedTestOptions(c))
	if err != nil {
		t.Fatal(err)
	}
	_, readErr := resp.Body.Read(make([]byte, 1))
	closeErr := resp.Body.Close()
	if !errors.Is(readErr, TimeoutError) || !errors.Is(readErr, extra) || errors.Is(readErr, context.Canceled) || !errors.Is(closeErr, TimeoutError) {
		t.Fatalf("timeout lost or misclassified causes: read=%v close=%v", readErr, closeErr)
	}
}

func TestOwnedReadExhaustedRetryBudgets(t *testing.T) {
	for _, authFailure := range []bool{false, true} {
		t.Run(fmt.Sprintf("auth_%t", authFailure), func(t *testing.T) {
			var attempts int
			var bodies []*ownedTestBody
			transportErr := errors.New("persistent transport failure")
			c := ownedTestConnection(ownedTestTransport(func(*http.Request) (*http.Response, error) {
				attempts++
				if !authFailure {
					return nil, transportErr
				}
				body := &ownedTestBody{}
				bodies = append(bodies, body)
				return &http.Response{StatusCode: 400, Header: http.Header{}, Body: body}, nil
			}))
			c.Retries = 4
			p := ownedTestOptions(c)
			p.Retries = 1
			if authFailure {
				c.UnAuthenticate()
				c.AuthVersion = 1
				c.AuthUrl = "http://auth.invalid/v1"
			}
			resp, _, err := c.Call(context.Background(), "", p)
			want := transportErr
			if authFailure {
				want = BadRequest
			}
			if resp != nil || !errors.Is(err, want) || attempts != 2 || c.Retries != 4 {
				t.Fatalf("bounded failure response=%v err=%v attempts=%d shared_retries=%d", resp, err, attempts, c.Retries)
			}
			for _, body := range bodies {
				if body.reads.Load() != 0 || body.closes.Load() != 1 {
					t.Fatalf("rejected auth body reads=%d closes=%d", body.reads.Load(), body.closes.Load())
				}
			}
		})
	}
}

type ownedCancelledCloseBody struct {
	ctx   context.Context
	extra error
}

func (b *ownedCancelledCloseBody) Read([]byte) (int, error) { <-b.ctx.Done(); return 0, b.ctx.Err() }
func (b *ownedCancelledCloseBody) Close() error             { return errors.Join(b.ctx.Err(), b.extra) }

func TestOwnedReadTimeoutCloseCause(t *testing.T) {
	for _, mode := range []string{"headers", "body", "caller"} {
		for _, distinct := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/distinct_%t", mode, distinct), func(t *testing.T) {
				body := &ownedCancelledCloseBody{}
				if distinct {
					body.extra = errors.New("distinct close failure")
				}
				var attempts int
				c := ownedTestConnection(ownedTestTransport(func(r *http.Request) (*http.Response, error) {
					attempts++
					body.ctx = r.Context()
					if mode == "headers" {
						<-r.Context().Done()
					}
					return &http.Response{StatusCode: 200, Header: http.Header{}, Body: body}, nil
				}))
				c.Retries = 2
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				resp, _, err := c.Call(ctx, "", ownedTestOptions(c))
				if mode == "headers" {
					if resp != nil {
						t.Fatal("timed out request returned a body")
					}
				} else {
					if err != nil {
						t.Fatal(err)
					}
					if mode == "caller" {
						cancel()
					}
					_, readErr := resp.Body.Read(make([]byte, 1))
					closeErr := resp.Body.Close()
					err = errors.Join(readErr, closeErr)
					if mode != "caller" && (errors.Is(closeErr, context.Canceled) || !errors.Is(closeErr, TimeoutError)) {
						t.Errorf("owned timeout Close misclassified: %v", closeErr)
					}
					if distinct && !errors.Is(closeErr, body.extra) {
						t.Errorf("distinct Close error lost: %v", closeErr)
					}
				}
				want := error(TimeoutError)
				if mode == "caller" {
					want = context.Canceled
				}
				if !errors.Is(err, want) || (mode != "caller" && errors.Is(err, context.Canceled)) || (distinct && !errors.Is(err, body.extra)) || attempts != 1 {
					t.Fatalf("wrong cause or retry after failed Close: err=%v attempts=%d", err, attempts)
				}
			})
		}
	}
}
