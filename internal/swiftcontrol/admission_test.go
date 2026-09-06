package swiftcontrol

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
)

type admissionRoundTrip func(*http.Request) (*http.Response, error)

func (f admissionRoundTrip) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

type admissionBody struct {
	io.Reader
	closes int
}

func (b *admissionBody) Close() error { b.closes++; return nil }

func TestRequestAdmissionClosesRefusedBody(t *testing.T) {
	body := &admissionBody{Reader: strings.NewReader("owned fixture")}
	req, err := http.NewRequestWithContext(t.Context(), http.MethodPut, "https://swift.example/v1/AUTH_test/owned/new", body)
	if err != nil {
		t.Fatal(err)
	}
	refused := errors.New("generation revoked")
	dispatched := false
	transport := requestAdmissionTransport{
		admit: func(ctx context.Context) error {
			if ctx != req.Context() {
				t.Fatal("admission lost the request context")
			}
			return refused
		},
		inner: admissionRoundTrip(func(*http.Request) (*http.Response, error) {
			dispatched = true
			return nil, errors.New("unexpected dispatch")
		}),
	}
	response, err := transport.RoundTrip(req)
	if response != nil || !errors.Is(err, refused) || dispatched || body.closes != 1 {
		t.Fatalf("refused request: response=%v, err=%v, dispatched=%v, body closes=%d", response, err, dispatched, body.closes)
	}
}

func TestRequestAdmissionPrecedesEveryDispatch(t *testing.T) {
	allowed := true
	dispatched := 0
	refused := errors.New("generation revoked")
	transport := requestAdmissionTransport{
		admit: func(context.Context) error {
			if !allowed {
				return refused
			}
			return nil
		},
		inner: admissionRoundTrip(func(req *http.Request) (*http.Response, error) {
			dispatched++
			// Revocation after admission cannot undo this operation; a
			// later dispatch still needs its own successful admission.
			allowed = false
			return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader("ok")), Request: req}, nil
		}),
	}
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "https://swift.example/v1/AUTH_test/owned/file", nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := transport.RoundTrip(req)
	if err != nil || response == nil {
		t.Fatalf("admitted request failed: %v", err)
	}
	response.Body.Close()
	if response, err := transport.RoundTrip(req); response != nil || !errors.Is(err, refused) || dispatched != 1 {
		t.Fatalf("second request reused admission: response=%v, err=%v, dispatches=%d", response, err, dispatched)
	}
}
