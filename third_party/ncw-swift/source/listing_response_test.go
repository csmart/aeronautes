package swift

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"testing/iotest"
)

type listingResponseTransport func(*http.Request) (*http.Response, error)

func (f listingResponseTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

type listingResponseBody struct {
	reader   io.Reader
	closeErr error
	onClose  func()
	closed   int
}

func (b *listingResponseBody) Read(p []byte) (int, error) { return b.reader.Read(p) }

func (b *listingResponseBody) Close() error {
	b.closed++
	if b.onClose != nil {
		b.onClose()
	}
	return b.closeErr
}

type listingResponseFailure struct {
	err   error
	reads int
}

func (r *listingResponseFailure) Read([]byte) (int, error) {
	r.reads++
	return 0, r.err
}

func TestListingResponseLifecycle(t *testing.T) {
	const good = `[{"name":"one","bytes":4,"last_modified":"2026-08-28T10:00:00.123456Z"}]`
	for _, kind := range []string{"containers", "objects"} {
		t.Run(kind, func(t *testing.T) {
			for _, fault := range []string{
				"valid", "empty", "read-error", "close-error", "cancel-at-close",
				"decode-and-close-error", "partial-status", "content-range",
				"invalid-shape", "invalid-suffix",
				"split-valid", "split-empty", "cancel-before-body",
			} {
				t.Run(fault, func(t *testing.T) {
					ctx, cancel := context.WithCancel(context.Background())
					defer cancel()
					closeFailure := errors.New("listing close failed")
					later := &listingResponseFailure{err: io.ErrUnexpectedEOF}
					body := &listingResponseBody{reader: strings.NewReader(good)}
					headers := make(http.Header)
					status, wantRows := http.StatusOK, 0
					var wantError error
					decodeError, prompt, valid := false, false, false
					switch fault {
					case "valid":
						valid, wantRows = true, 1
					case "empty":
						valid = true
						body.reader = strings.NewReader("[]")
					case "split-valid":
						valid, wantRows = true, 1
						body.reader = iotest.OneByteReader(strings.NewReader(" \t" + good + "\r\n"))
					case "split-empty":
						valid = true
						body.reader = iotest.OneByteReader(strings.NewReader(" \t[]\r\n"))
					case "cancel-before-body":
						body.reader, prompt, wantError = later, true, context.Canceled
					case "read-error":
						body.reader = io.MultiReader(strings.NewReader(good), later)
						wantError = io.ErrUnexpectedEOF
					case "close-error":
						body.closeErr, wantError = closeFailure, closeFailure
					case "cancel-at-close":
						body.onClose, wantError = cancel, context.Canceled
					case "decode-and-close-error":
						body.reader = strings.NewReader(strings.Replace(good, `"bytes":4`, `"bytes":"invalid"`, 1))
						body.closeErr, decodeError = closeFailure, true
					case "partial-status":
						status, body.reader, prompt = http.StatusPartialContent, later, true
					case "content-range":
						headers.Set("Content-Range", "")
						body.reader, prompt = later, true
					case "invalid-shape":
						body.reader, prompt = io.MultiReader(strings.NewReader(`"`), later), true
					case "invalid-suffix":
						body.reader, prompt = io.MultiReader(strings.NewReader(good+" ["), later), true
					}
					calls := 0
					connection := &Connection{
						StorageUrl: "http://storage.invalid/v1/AUTH_fixture",
						AuthToken:  "fixture-token",
						Transport: listingResponseTransport(func(request *http.Request) (*http.Response, error) {
							calls++
							if fault == "cancel-before-body" {
								cancel()
							}
							if request.Method != http.MethodGet || request.URL.Query().Get("format") != "json" {
								t.Errorf("unexpected listing request: %s %s", request.Method, request.URL)
							}
							return &http.Response{StatusCode: status, Header: headers, Body: body, Request: request}, nil
						}),
					}
					var rows int
					var err error
					if kind == "containers" {
						var page []Container
						page, err = connection.Containers(ctx, nil)
						rows = len(page)
						if err != nil && page != nil {
							t.Error("failed container page returned rows")
						}
					} else {
						var page []Object
						page, err = connection.Objects(ctx, "owned", nil)
						rows = len(page)
						if err != nil && page != nil {
							t.Error("failed object page returned rows")
						}
					}
					if calls != 1 || body.closed != 1 {
						t.Fatalf("requests=%d body closes=%d, want one each", calls, body.closed)
					}
					if prompt && later.reads != 0 {
						t.Fatal("known invalid response read its remaining body")
					}
					if valid {
						if err != nil || rows != wantRows {
							t.Fatalf("valid listing: rows=%d err=%v", rows, err)
						}
						return
					}
					if err == nil || rows != 0 {
						t.Fatalf("invalid listing accepted: rows=%d err=%v", rows, err)
					}
					if wantError != nil && !errors.Is(err, wantError) {
						t.Fatalf("listing error=%v, want %v", err, wantError)
					}
					var typeError *json.UnmarshalTypeError
					if decodeError && !errors.As(err, &typeError) {
						t.Fatalf("primary decode error was lost: %v", err)
					}
				})
			}
		})
	}
}
