package swift

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

var errOwnedReadClosed = errors.New("owned response closed")

func validateOwnedRead(target string, p RequestOpts) error {
	if target != "" || p.OnReAuth == nil || p.Container == "" || p.ObjectName == "" || p.Operation != http.MethodGet || p.Body != nil || p.NoResponse || p.Parameters != nil {
		return errors.New("owned read requires a full object GET, an empty initial target and a storage target callback")
	}
	for key := range p.Headers {
		switch strings.ToLower(key) {
		case "range", "if-range", "content-length", "transfer-encoding":
			return fmt.Errorf("owned read does not support %s", key)
		}
	}
	return nil
}

// The legacy helpers detach Do/Read or drain unused bodies. This opt-in owns
// each request separately so closing authentication cannot cancel the GET.
func (c *Connection) callOwnedRead(ctx context.Context, target string, p RequestOpts) (*http.Response, Headers, error) {
	if err := validateOwnedRead(target, p); err != nil {
		return nil, nil, err
	}
	operation, cancel := context.WithCancel(ctx)
	transferred := false
	defer func() {
		if !transferred {
			cancel()
		}
	}()
	c.authLock.Lock()
	c.setDefaults()
	retries := p.Retries
	if retries == 0 {
		retries = c.Retries
	}
	c.authLock.Unlock()
	var headers Headers
	for {
		target, token, err := c.ownedReadCredentials(operation, p.OnReAuth)
		if err != nil {
			return nil, headers, err
		}
		uri, err := url.Parse(target)
		if err != nil {
			return nil, headers, err
		}
		if uri.RawQuery != "" || uri.ForceQuery || uri.Fragment != "" {
			return nil, headers, errors.New("owned read storage target must not contain a query or fragment")
		}
		uri.Path += "/" + p.Container + "/" + p.ObjectName
		// RawPath belongs to the old target and cannot describe the appended name.
		uri.RawPath = ""
		attempt, stop := context.WithCancelCause(operation)
		req, err := http.NewRequestWithContext(attempt, http.MethodGet, uri.String(), nil)
		if err != nil {
			stop(err)
			return nil, headers, err
		}
		for key, value := range p.Headers {
			req.Header.Set(key, value)
		}
		req.Header.Set("Accept-Encoding", "identity")
		req.Header.Set("User-Agent", c.UserAgent)
		req.Header.Set("X-Auth-Token", token)
		resp, err, closeErr := c.doOwnedRead(operation, req, stop)
		if resp != nil {
			headers = ownedReadHeaders(resp)
			if resp.StatusCode == http.StatusUnauthorized {
				c.UnAuthenticate()
			}
		}
		if err != nil {
			if closeErr != nil || operation.Err() != nil || retries <= 0 {
				return nil, headers, errors.Join(err, closeErr)
			}
			retries--
			continue
		}
		if resp.StatusCode == http.StatusUnauthorized {
			closeErr = resp.Body.Close()
			if closeErr != nil || operation.Err() != nil || retries <= 0 {
				return nil, headers, errors.Join(AuthorizationFailed, closeErr, operation.Err())
			}
			retries--
			continue
		}
		if err = ownedReadStatus(resp, p.ErrorMap); err != nil {
			return nil, headers, errors.Join(err, resp.Body.Close())
		}
		body := resp.Body.(*ownedReadBody)
		body.finishOperation = cancel
		transferred = true
		return resp, headers, nil
	}
}

// Check cancellation on both sides of the legacy non-context-aware lock.
func (c *Connection) ownedReadCredentials(ctx context.Context, target func() (string, error)) (string, string, error) {
	if err := ctx.Err(); err != nil {
		return "", "", err
	}
	c.authLock.Lock()
	defer c.authLock.Unlock()
	if err := ctx.Err(); err != nil {
		return "", "", err
	}
	if !c.authenticated() {
		if err := c.authenticateOwnedRead(ctx); err != nil {
			return "", "", err
		}
	}
	if err := ctx.Err(); err != nil {
		return "", "", err
	}
	uri, err := target()
	if err != nil {
		return "", "", err
	}
	if uri == "" {
		return "", "", errors.New("storage target callback returned an empty URL")
	}
	return uri, c.AuthToken, nil
}

// Authentication publication deliberately precedes checked close, as in the
// legacy path: header authenticators may already have published their snapshot.
func (c *Connection) authenticateOwnedRead(ctx context.Context) error {
	flushKeepaliveConnections(c.Transport)
	defer flushKeepaliveConnections(c.Transport)
	if c.Auth == nil {
		var err error
		c.Auth, err = newAuth(c)
		if err != nil {
			return err
		}
	}
	for retries := 1; ; retries-- {
		attempt, stop := context.WithCancelCause(ctx)
		req, err := c.Auth.Request(attempt, c)
		if err != nil {
			stop(err)
			return errors.Join(err, closeOwnedReadRequest(req))
		}
		if req == nil {
			stop(errOwnedReadClosed)
			return c.publishOwnedReadAuthentication()
		}
		resp, err, closeErr := c.doOwnedRead(ctx, req.WithContext(attempt), stop)
		if err != nil {
			return errors.Join(err, closeErr)
		}
		if err = ownedReadStatus(resp, authErrorMap); err != nil {
			closeErr = resp.Body.Close()
			if closeErr == nil && ctx.Err() == nil && retries > 0 && (errors.Is(err, AuthorizationFailed) || errors.Is(err, BadRequest)) {
				continue
			}
			return errors.Join(err, closeErr, ctx.Err())
		}
		err = c.Auth.Response(attempt, resp)
		if err == nil {
			err = c.publishOwnedReadAuthentication()
		}
		return errors.Join(err, resp.Body.Close())
	}
}

func (c *Connection) publishOwnedReadAuthentication() error {
	if custom, ok := c.Auth.(CustomEndpointAuthenticator); ok && c.EndpointType != "" {
		c.StorageUrl = custom.StorageUrlForEndpoint(c.EndpointType)
	} else {
		c.StorageUrl = c.Auth.StorageUrl(c.Internal)
	}
	c.AuthToken = c.Auth.Token()
	if expiring, ok := c.Auth.(Expireser); ok {
		c.Expires = expiring.Expires()
	} else {
		c.Expires = time.Time{}
	}
	if !c.authenticated() {
		return newError(0, "Response didn't have storage url and auth token")
	}
	return nil
}

func ownedReadStatus(resp *http.Response, mapping errorMap) error {
	if err := mapping[resp.StatusCode]; err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return newErrorf(resp.StatusCode, "HTTP Error: %d: %s", resp.StatusCode, http.StatusText(resp.StatusCode))
	}
	return nil
}

func ownedReadHeaders(resp *http.Response) Headers {
	headers := Headers{}
	for key, values := range resp.Header {
		if len(values) != 0 {
			headers[key] = values[0]
		}
	}
	if tag, ok := headers["Etag"]; ok {
		headers["Etag"] = strings.Trim(tag, "\"")
	}
	return headers
}

// http.Client discards a RoundTripper response returned with an error. Retain
// that response in this attempt, so neither its body nor its close error is lost.
// Refusing redirects also avoids the client's implicit raw body drain.
type ownedReadTransport struct {
	inner           http.RoundTripper
	response        *http.Response
	requestCloseErr error
}

func (t *ownedReadTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if err := req.Context().Err(); err != nil {
		t.requestCloseErr = closeOwnedReadRequest(req)
		return nil, err
	}
	resp, err := t.inner.RoundTrip(req)
	t.response = resp
	if err != nil {
		return nil, err
	}
	return resp, nil
}

func (c *Connection) doOwnedRead(parent context.Context, req *http.Request, cancel context.CancelCauseFunc) (*http.Response, error, error) {
	client := *c.client
	transport := &ownedReadTransport{inner: client.Transport}
	client.Transport = transport
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	stopTimer := ownedReadWatchdog(cancel, c.ConnectTimeout)
	resp, err := client.Do(req)
	stopTimer()
	if resp == nil {
		resp = transport.response
	}
	err = ownedReadCause(req.Context(), err)
	if resp != nil {
		if resp.Body == nil {
			resp.Body = http.NoBody
		}
		resp.Body = &ownedReadBody{body: resp.Body, ctx: req.Context(), parent: parent, cancel: cancel, idle: c.Timeout}
	}
	if err != nil {
		if resp != nil {
			body := resp.Body.(*ownedReadBody)
			closeErr := body.Close()
			var cleanupFailure error
			if body.cleanupErr != nil {
				cleanupFailure = ownedReadCause(req.Context(), body.cleanupErr)
			}
			return resp, errors.Join(err, closeErr), cleanupFailure
		}
		cancel(err)
		return nil, err, transport.requestCloseErr
	}
	return resp, nil, nil
}

func closeOwnedReadRequest(req *http.Request) error {
	if req != nil && req.Body != nil {
		return req.Body.Close()
	}
	return nil
}

func ownedReadWatchdog(cancel context.CancelCauseFunc, timeout time.Duration) func() {
	done := make(chan struct{})
	timer := time.AfterFunc(timeout, func() {
		defer close(done)
		cancel(TimeoutError)
	})
	return func() {
		if !timer.Stop() {
			<-done
		}
	}
}

func ownedReadCause(ctx context.Context, err error) error {
	cause := context.Cause(ctx)
	if cause == nil {
		return err
	}
	if errors.Is(cause, errOwnedReadClosed) {
		return err
	}
	// A watchdog uses cancellation to interrupt I/O. Its transport cancellation
	// must not hide the timeout or discard other joined I/O/cleanup failures.
	if err == nil || err == io.EOF {
		return cause
	}
	if errors.Is(cause, TimeoutError) {
		err = ownedReadWithoutCancellation(err)
	}
	return errors.Join(cause, err)
}

func ownedReadWithoutCancellation(err error) error {
	if err == context.Canceled || err == context.DeadlineExceeded {
		return nil
	}
	if !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	switch wrapped := err.(type) {
	case interface{ Unwrap() []error }:
		var retained []error
		for _, child := range wrapped.Unwrap() {
			retained = append(retained, ownedReadWithoutCancellation(child))
		}
		return errors.Join(retained...)
	case interface{ Unwrap() error }:
		return ownedReadWithoutCancellation(wrapped.Unwrap())
	default:
		return err
	}
}

type ownedReadBody struct {
	body            io.ReadCloser
	ctx             context.Context
	parent          context.Context
	cancel          context.CancelCauseFunc
	idle            time.Duration
	finishOperation context.CancelFunc
	reading         sync.Mutex
	closed          atomic.Bool
	closeOnce       sync.Once
	closeErr        error
	cleanupErr      error
}

func (b *ownedReadBody) Read(p []byte) (int, error) {
	b.reading.Lock()
	defer b.reading.Unlock()
	if b.closed.Load() {
		return 0, io.ErrClosedPipe
	}
	if err := ownedReadCause(b.ctx, nil); err != nil {
		return 0, err
	}
	stopTimer := ownedReadWatchdog(b.cancel, b.idle)
	n, err := b.body.Read(p)
	stopTimer()
	return n, ownedReadCause(b.ctx, err)
}

func (b *ownedReadBody) Close() error {
	b.closeOnce.Do(func() {
		b.closed.Store(true)
		cause := ownedReadCause(b.ctx, nil)
		b.cancel(errOwnedReadClosed)
		b.cleanupErr = b.body.Close()
		// Cancel and Close precede the join because either can unblock Read.
		b.reading.Lock()
		b.reading.Unlock()
		cleanup := b.cleanupErr
		if errors.Is(cause, TimeoutError) {
			cleanup = ownedReadWithoutCancellation(cleanup)
		}
		b.closeErr = errors.Join(cause, cleanup, b.parent.Err())
		if b.finishOperation != nil {
			b.finishOperation()
		}
	})
	return b.closeErr
}
