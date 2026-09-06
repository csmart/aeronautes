package swifttest

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"net"
	"net/http"
	"sync"
	"time"
)

const responseCaptureLimit = 64 << 10

// HTTPResponseSelector uses wire names; RequestLog uses decoded paths.
// Operation identifies this fixture, so actors with identical requests need
// separate controlled endpoints to keep their occurrences independent.
type HTTPResponseSelector struct {
	Operation   string
	Method      string
	EscapedPath string
	RawQuery    string
	Occurrence  int
}

// HTTPResponseEvent proves the selected handler returned, not that its
// reported effect is correct or durable. Witness effects outside the barrier.
type HTTPResponseEvent struct {
	HTTPResponseSelector
	StatusCode int
}

type HTTPResponseBarrier struct {
	selector HTTPResponseSelector
	lose     bool
	timeout  time.Duration
	ctx      context.Context
	cancel   context.CancelFunc

	mu       sync.Mutex
	seen     int
	request  context.Context
	event    HTTPResponseEvent
	err      error
	arrived  bool
	released bool
	ready    chan struct{}
	release  chan struct{}
	readyOne sync.Once
}

func NewHTTPResponseBarrier(selector HTTPResponseSelector, loseResponse bool, timeout time.Duration) (*HTTPResponseBarrier, error) {
	if selector.Operation == "" || selector.Method == "" || selector.EscapedPath == "" || selector.Occurrence < 1 || timeout <= 0 {
		return nil, fmt.Errorf("HTTP response barrier needs an operation, method, escaped path, positive occurrence and finite timeout")
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &HTTPResponseBarrier{
		selector: selector, lose: loseResponse, timeout: timeout,
		ctx: ctx, cancel: cancel, ready: make(chan struct{}), release: make(chan struct{}),
	}, nil
}

func (b *HTTPResponseBarrier) failure(err error) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.err == nil {
		b.err = fmt.Errorf("HTTP response barrier %+v: %w", b.selector, err)
	}
	b.readyOne.Do(func() { close(b.ready) })
	b.cancel()
	return b.err
}

// Wait aborts the fixture on a missing event, so a late request cannot
// quietly perform the selected effect after its test has already failed.
func (b *HTTPResponseBarrier) Wait(ctx context.Context) (HTTPResponseEvent, error) {
	ctx, cancel := context.WithTimeout(ctx, b.timeout)
	defer cancel()
	select {
	case <-b.ready:
		b.mu.Lock()
		defer b.mu.Unlock()
		return b.event, b.err
	case <-ctx.Done():
		return HTTPResponseEvent{}, b.failure(fmt.Errorf("waiting for response: %w", ctx.Err()))
	}
}

func (b *HTTPResponseBarrier) Release() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.released {
		return nil
	}
	if b.err != nil {
		return b.err
	}
	if !b.arrived {
		return fmt.Errorf("HTTP response barrier %+v: release before arrival", b.selector)
	}
	if err := b.request.Err(); err != nil {
		return fmt.Errorf("HTTP response barrier %+v: release after request ended: %w", b.selector, err)
	}
	if !b.released {
		b.released = true
		close(b.release)
	}
	return nil
}

// Err distinguishes deliberate loss from a fixture failure once the selected
// exchange has ended. Read it before Close records fixture teardown.
func (b *HTTPResponseBarrier) Err() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.err
}

// Close must run before the owning HTTP server's shutdown waits for handlers.
func (b *HTTPResponseBarrier) Close() {
	b.failure(fmt.Errorf("fixture closed"))
}

func (b *HTTPResponseBarrier) Wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s := b.selector
		if r.Method != s.Method || r.URL.EscapedPath() != s.EscapedPath || r.URL.RawQuery != s.RawQuery {
			next.ServeHTTP(w, r)
			return
		}
		b.mu.Lock()
		b.seen++
		occurrence := b.seen
		b.mu.Unlock()
		if occurrence != s.Occurrence {
			next.ServeHTTP(w, r)
			return
		}
		// Reading an Expect body can send 100 Continue through the original
		// server writer, bypassing the replacement response capture entirely.
		if len(r.Header.Values("Expect")) != 0 {
			b.failure(fmt.Errorf("Expect unsupported on selected request"))
			panic(http.ErrAbortHandler)
		}

		ctx, cancel := context.WithTimeout(r.Context(), b.timeout)
		stop := context.AfterFunc(b.ctx, cancel)
		defer cancel()
		defer stop()
		if b.ctx.Err() != nil || ctx.Err() != nil {
			b.failure(fmt.Errorf("request cancelled before handler"))
			panic(http.ErrAbortHandler)
		}
		capture := &capturedResponse{header: make(http.Header)}
		if err := capture.run(next, r.WithContext(ctx)); err != nil {
			b.failure(err)
			panic(http.ErrAbortHandler)
		}
		if ctx.Err() != nil {
			b.failure(fmt.Errorf("selected handler: %w", ctx.Err()))
			panic(http.ErrAbortHandler)
		}

		b.mu.Lock()
		b.request = ctx
		b.event = HTTPResponseEvent{HTTPResponseSelector: HTTPResponseSelector{
			Operation: s.Operation, Method: r.Method, EscapedPath: r.URL.EscapedPath(),
			RawQuery: r.URL.RawQuery, Occurrence: occurrence,
		}, StatusCode: capture.status}
		b.arrived = true
		b.readyOne.Do(func() { close(b.ready) })
		b.mu.Unlock()
		select {
		case <-b.release:
		case <-ctx.Done():
			b.failure(fmt.Errorf("holding response: %w", ctx.Err()))
			panic(http.ErrAbortHandler)
		}
		// Cancellation wins over an already queued release; a test must not
		// mistake its timeout or cleanup for the configured response loss.
		if ctx.Err() != nil || b.ctx.Err() != nil {
			b.failure(fmt.Errorf("request cancelled before response delivery"))
			panic(http.ErrAbortHandler)
		}
		if b.lose {
			panic(http.ErrAbortHandler)
		}
		for name, values := range capture.sentHeader {
			w.Header()[name] = append([]string(nil), values...)
		}
		w.WriteHeader(capture.status)
		if capture.body.Len() != 0 {
			if _, err := w.Write(capture.body.Bytes()); err != nil {
				b.failure(fmt.Errorf("delivering captured response: %w", err))
			}
		}
	})
}

type capturedResponse struct {
	header, sentHeader http.Header
	status             int
	body               bytes.Buffer
	err                error
}

func (c *capturedResponse) Header() http.Header { return c.header }

func (c *capturedResponse) WriteHeader(status int) {
	if status < 200 || status > 599 {
		c.err = fmt.Errorf("unsupported captured response status %d", status)
		return
	}
	if c.status == 0 {
		c.status = status
		c.sentHeader = c.header.Clone()
	}
}

func (c *capturedResponse) Write(data []byte) (int, error) {
	if c.status == 0 {
		c.WriteHeader(http.StatusOK)
	}
	if len(data) > responseCaptureLimit-c.body.Len() {
		c.err = fmt.Errorf("captured response exceeds %d bytes", responseCaptureLimit)
	}
	if c.err != nil {
		return 0, c.err
	}
	return c.body.Write(data)
}

func (c *capturedResponse) Flush() {
	c.err = fmt.Errorf("flush unsupported on captured response")
}

func (c *capturedResponse) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	c.err = fmt.Errorf("hijack unsupported on captured response")
	return nil, nil, c.err
}

func (c *capturedResponse) run(next http.Handler, r *http.Request) (err error) {
	defer func() {
		if cause := recover(); cause != nil {
			err = fmt.Errorf("selected handler panicked: %v", cause)
		}
	}()
	next.ServeHTTP(c, r)
	if c.status == 0 {
		c.WriteHeader(http.StatusOK)
	}
	if len(c.sentHeader.Values("Trailer")) != 0 {
		return fmt.Errorf("trailers unsupported on captured response")
	}
	return c.err
}
