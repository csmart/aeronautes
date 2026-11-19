package swiftcontrol

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/md5"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ncw/swift/v2"
)

func TestObjectReadsRequireCompleteOpaqueResponse(t *testing.T) {
	var encoded bytes.Buffer
	compressor := gzip.NewWriter(&encoded)
	if _, err := compressor.Write([]byte("stored representation")); err != nil {
		t.Fatal(err)
	}
	if err := compressor.Close(); err != nil {
		t.Fatal(err)
	}
	rangeValue, emptyRange := "bytes 0-3/4", ""
	cases := []struct {
		name       string
		status     int
		body       []byte
		encoding   string
		rangeValue *string
		chunked    bool
		valid      bool
	}{
		{name: "plain", body: []byte("data"), valid: true},
		{name: "zero bytes", valid: true},
		{name: "chunked", body: []byte("data"), chunked: true, valid: true},
		{name: "opaque gzip", body: encoded.Bytes(), encoding: "gzip", valid: true},
		{name: "malformed gzip", body: []byte("not gzip"), encoding: "gzip", valid: true},
		{name: "unknown coding", body: []byte("opaque"), encoding: "unknown-fixture", valid: true},
		{name: "partial status with matching bytes", status: http.StatusPartialContent, body: []byte("data")},
		{name: "created status with matching bytes", status: http.StatusCreated, body: []byte("data")},
		{name: "no content status", status: http.StatusNoContent},
		{name: "content range with matching bytes", body: []byte("data"), rangeValue: &rangeValue},
		{name: "empty content range", body: []byte("data"), rangeValue: &emptyRange},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var requests atomic.Int32
			session := listingResponseSession(t, func(w http.ResponseWriter, request *http.Request) {
				requests.Add(1)
				if request.Method != http.MethodGet || request.URL.Path != "/v1/AUTH_acct/owned/a%2F +日本語" || request.URL.RawQuery != "" {
					t.Errorf("unexpected object request: %s %s", request.Method, request.URL)
				}
				if request.Header.Get("Accept-Encoding") != "identity" || request.Header.Get("Range") != "" || request.Header.Get("If-Range") != "" {
					t.Error("object content request lost its full opaque representation")
				}
				w.Header().Set("X-Trans-Id", "tx-content")
				w.Header().Set("Etag", fmt.Sprintf("%x", md5.Sum(tc.body)))
				if tc.encoding != "" {
					w.Header().Set("Content-Encoding", tc.encoding)
				}
				if tc.rangeValue != nil {
					w.Header().Set("Content-Range", *tc.rangeValue)
				}
				if !tc.chunked {
					w.Header().Set("Content-Length", strconv.Itoa(len(tc.body)))
				}
				status := tc.status
				if status == 0 {
					status = http.StatusOK
				}
				w.WriteHeader(status)
				if tc.chunked {
					w.(http.Flusher).Flush()
				}
				if len(tc.body) != 0 {
					if _, err := w.Write(tc.body); err != nil {
						t.Errorf("fixture response: %v", err)
					}
				}
			})
			reader, _, err := session.OpenObjectChecked(t.Context(), "owned", "a%2F +日本語")
			if !tc.valid {
				if reader != nil || err == nil {
					if reader != nil {
						if closeErr := reader.Close(); closeErr != nil {
							t.Error(closeErr)
						}
					}
					t.Fatalf("incomplete response accepted: reader=%v error=%v", reader, err)
				}
				var swiftErr *SwiftError
				if !errors.As(err, &swiftErr) || swiftErr.TransID != "tx-content" {
					t.Fatalf("refusal lost its transaction identity: %v", err)
				}
			} else {
				if err != nil {
					t.Fatal(err)
				}
				content, readErr := io.ReadAll(reader)
				closeErr := reader.Close()
				if readErr != nil || closeErr != nil || !bytes.Equal(content, tc.body) {
					t.Fatalf("stored bytes changed: received=%x want=%x read=%v close=%v", content, tc.body, readErr, closeErr)
				}
			}
			if requests.Load() != 1 {
				t.Fatalf("object requests=%d, want 1", requests.Load())
			}
		})
	}
}

type contentResultBody struct {
	data     []byte
	readErr  error
	closeErr error
	reads    atomic.Int32
	closes   atomic.Int32
}

func (b *contentResultBody) Read(buffer []byte) (int, error) {
	b.reads.Add(1)
	n := copy(buffer, b.data)
	b.data = b.data[n:]
	if len(b.data) == 0 {
		return n, b.readErr
	}
	return n, nil
}

func (b *contentResultBody) Close() error {
	b.closes.Add(1)
	return b.closeErr
}

func TestObjectContentPreservesReadAndCloseErrors(t *testing.T) {
	readFailure := errors.New("fixture read failure")
	closeFailure := errors.New("fixture close failure")
	for _, tc := range []struct {
		name     string
		status   int
		readErr  error
		closeErr error
	}{
		{name: "bytes plus EOF", status: 200, readErr: io.EOF},
		{name: "read failure", status: 200, readErr: readFailure},
		{name: "close failure after EOF", status: 200, readErr: io.EOF, closeErr: closeFailure},
		{name: "read and close failures", status: 200, readErr: readFailure, closeErr: closeFailure},
		{name: "incomplete status and close failure", status: 206, readErr: io.EOF, closeErr: closeFailure},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := &contentResultBody{data: []byte("data"), readErr: tc.readErr, closeErr: tc.closeErr}
			session, err := New(Config{AuthURL: "http://127.0.0.1/auth", Username: "fixture", APIKey: "fixture"})
			if err != nil {
				t.Fatal(err)
			}
			session.conn.Transport = redirectRefusingTransport{inner: admissionRoundTrip(func(request *http.Request) (*http.Response, error) {
				if request.URL.Path == "/auth" {
					return &http.Response{StatusCode: 200, Header: http.Header{
						"X-Storage-Url": {"http://127.0.0.1/v1/AUTH_fixture"}, "X-Auth-Token": {"fixture"},
					}, Body: io.NopCloser(strings.NewReader("")), Request: request}, nil
				}
				return &http.Response{StatusCode: tc.status, Header: http.Header{"X-Trans-Id": {"tx-content"}}, Body: body, Request: request}, nil
			})}
			reader, openErr := session.OpenObjectContent(t.Context(), "owned", "object")
			if tc.status != 200 {
				if reader != nil {
					if err := reader.Close(); err != nil {
						t.Error(err)
					}
				}
				if reader != nil || !errors.Is(openErr, closeFailure) || body.reads.Load() != 0 || body.closes.Load() != 1 {
					t.Fatalf("rejected body ownership: reader=%v err=%v reads=%d closes=%d", reader, openErr, body.reads.Load(), body.closes.Load())
				}
				return
			}
			if openErr != nil {
				t.Fatal(openErr)
			}
			buffer := make([]byte, 16)
			n, readErr := reader.Read(buffer)
			closeErr := reader.Close()
			if n != 4 || string(buffer[:n]) != "data" || !errors.Is(readErr, tc.readErr) || !errors.Is(closeErr, tc.closeErr) || body.closes.Load() != 1 {
				t.Fatalf("body outcome: n=%d read=%v close=%v closes=%d", n, readErr, closeErr, body.closes.Load())
			}
			for _, failure := range []error{readErr, closeErr} {
				if failure == nil || failure == io.EOF {
					continue
				}
				var swiftErr *SwiftError
				if !errors.As(failure, &swiftErr) || swiftErr.TransID != "tx-content" {
					t.Fatalf("body error lost transaction identity: %v", failure)
				}
			}
		})
	}
}

func TestObjectContentRefusesRevokedOrCancelledSession(t *testing.T) {
	refused := errors.New("captured generation revoked")
	for _, cancelled := range []bool{false, true} {
		t.Run(strconv.FormatBool(cancelled), func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			if cancelled {
				cancel()
			}
			var admissions int
			session, err := New(Config{
				AuthURL: "http://127.0.0.1/auth", Username: "fixture", APIKey: "fixture",
				AdmitRequest: func(context.Context) error {
					admissions++
					return refused
				},
			})
			if err != nil {
				t.Fatal(err)
			}
			auth := &contentRefusedAuth{v1Auth: session.auth.(*v1Auth)}
			session.conn.Auth = auth
			reader, err := session.OpenObjectContent(ctx, "owned", "object")
			if reader != nil {
				if closeErr := reader.Close(); closeErr != nil {
					t.Error(closeErr)
				}
			}
			want, wantAdmissions := refused, 1
			if cancelled {
				want, wantAdmissions = context.Canceled, 0
			}
			if reader != nil || !errors.Is(err, want) || admissions != wantAdmissions || auth.requests != 0 {
				t.Fatalf("refused read: reader=%v error=%v admissions=%d auth=%d", reader, err, admissions, auth.requests)
			}
		})
	}
}

type contentRefusedAuth struct {
	*v1Auth
	requests int
}

func (a *contentRefusedAuth) Request(context.Context, *swift.Connection) (*http.Request, error) {
	a.requests++
	return nil, errors.New("refused content read started authentication")
}

func TestObjectContentNativeCredentialLifetime(t *testing.T) {
	for _, force := range []bool{false, true} {
		t.Run(strconv.FormatBool(force), func(t *testing.T) {
			fixture := newPairingFixture(t, "origin", func(w http.ResponseWriter, request *http.Request) {
				if _, err := io.WriteString(w, "data"); err != nil {
					t.Errorf("fixture response: %v", err)
				}
			})
			session := fixture.session
			if err := session.Authenticate(t.Context()); err != nil {
				t.Fatal(err)
			}
			old := time.Now().Add(-20 * time.Minute)
			session.authedAt = old
			wantAuths := int64(1)
			if force {
				session.ForceReauth()
				old = time.Time{}
				wantAuths = 2
			}
			reader, err := session.OpenObjectContent(t.Context(), "owned", "object")
			if err != nil {
				t.Fatal(err)
			}
			content, readErr := io.ReadAll(reader)
			closeErr := reader.Close()
			if string(content) != "data" || readErr != nil || closeErr != nil {
				t.Fatalf("native credential read: content=%q read=%v close=%v", content, readErr, closeErr)
			}
			fixture.check(t, 1, wantAuths)
			if !session.authedAt.Equal(old) {
				t.Fatal("native read changed the explicit credential refresh age")
			}
		})
	}
}

func TestObjectContentKeepsPairAcrossUnauthorizedRetry(t *testing.T) {
	for _, change := range []string{"origin", "path", "override"} {
		t.Run(change, func(t *testing.T) {
			var requests atomic.Int32
			fixture := newPairingFixture(t, change, func(w http.ResponseWriter, request *http.Request) {
				if requests.Add(1) == 1 {
					w.WriteHeader(http.StatusUnauthorized)
					return
				}
				if _, err := io.WriteString(w, "data"); err != nil {
					t.Errorf("fixture response: %v", err)
				}
			})
			reader, err := fixture.session.OpenObjectContent(t.Context(), "owned", "object")
			if err != nil {
				t.Fatal(err)
			}
			content, readErr := io.ReadAll(reader)
			closeErr := reader.Close()
			if string(content) != "data" || readErr != nil || closeErr != nil {
				t.Fatalf("authenticated retry: content=%q read=%v close=%v", content, readErr, closeErr)
			}
			fixture.check(t, 2, 2)
			if !fixture.session.authedAt.IsZero() {
				t.Fatal("implicit retry changed explicit refresh state")
			}
		})
	}
}

func TestObjectContentAuthCloseFailureKeepsCoherentTuple(t *testing.T) {
	for _, primed := range []bool{false, true} {
		t.Run(strconv.FormatBool(primed), func(t *testing.T) {
			closeFailure := errors.New("fixture auth close failure")
			authBody := &contentResultBody{data: []byte("unused"), readErr: io.EOF, closeErr: closeFailure}
			session, err := New(Config{AuthURL: "http://127.0.0.1/auth", Username: "fixture", APIKey: "fixture"})
			if err != nil {
				t.Fatal(err)
			}
			var storageRequests, authRequests atomic.Int32
			const target, token = "http://127.0.0.1/new/v1/AUTH_fixture", "new-fixture-token"
			session.conn.Transport = redirectRefusingTransport{inner: admissionRoundTrip(func(request *http.Request) (*http.Response, error) {
				if request.URL.Path != "/auth" {
					storageRequests.Add(1)
					return nil, errors.New("unexpected storage request")
				}
				if authRequests.Add(1) == 1 && primed {
					return &http.Response{StatusCode: 200, Header: http.Header{
						"X-Storage-Url": {"http://127.0.0.1/old/v1/AUTH_fixture"}, "X-Auth-Token": {"old-fixture-token"}, "X-Auth-Token-Expires": {"3600"},
					}, Body: io.NopCloser(strings.NewReader("")), Request: request}, nil
				}
				return &http.Response{StatusCode: 200, Header: http.Header{
					"X-Storage-Url": {target}, "X-Auth-Token": {token}, "X-Auth-Token-Expires": {"3600"},
				}, Body: authBody, Request: request}, nil
			})}
			wantAuth := int32(1)
			if primed {
				if err := session.Authenticate(t.Context()); err != nil {
					t.Fatal(err)
				}
				if session.conn.AuthToken != "old-fixture-token" || session.auth.snapshot().token != "old-fixture-token" {
					t.Fatal("fixture did not establish the old native and snapshot credentials")
				}
				nearExpiryObjectContentCredentials(session)
				wantAuth = 2
			}
			priorAge := session.authedAt
			reader, err := session.OpenObjectContent(t.Context(), "owned", "object")
			if reader != nil {
				if closeErr := reader.Close(); closeErr != nil {
					t.Error(closeErr)
				}
			}
			if reader != nil || !errors.Is(err, closeFailure) || storageRequests.Load() != 0 || authBody.reads.Load() != 0 || authBody.closes.Load() != 1 || authRequests.Load() != wantAuth {
				t.Fatalf("auth-close refusal: reader=%v error=%v storage=%d reads=%d closes=%d auth=%d", reader, err, storageRequests.Load(), authBody.reads.Load(), authBody.closes.Load(), authRequests.Load())
			}
			snapshot := session.auth.snapshot()
			if snapshot.storageURL != target || snapshot.token != token || snapshot.expires.IsZero() || session.conn.StorageUrl != target || session.conn.AuthToken != token || !session.conn.Expires.Equal(snapshot.expires) {
				t.Fatal("accepted auth close failure left incoherent native and snapshot credentials")
			}
			if !session.authedAt.Equal(priorAge) {
				t.Fatal("failed native read advanced explicit refresh state")
			}
		})
	}
}

func nearExpiryObjectContentCredentials(session *Session) {
	// Advance the accepted tuple together before concurrency, without a minute-long wait.
	expiry := time.Now().Add(30 * time.Second)
	session.conn.Expires = expiry
	v1 := session.auth.(*v1Auth)
	v1.expires = expiry
	snapshot := v1.snapshot()
	snapshot.expires = expiry
	v1.snap.Store(&snapshot)
}
