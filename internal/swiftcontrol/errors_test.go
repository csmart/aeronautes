package swiftcontrol

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/ncw/swift/v2"
)

func TestNormalize(t *testing.T) {
	cases := []struct {
		name          string
		in            error
		wantKind      ErrorKind
		wantStatus    int
		wantRetryable bool
	}{
		{"auth failed", swift.AuthorizationFailed, KindUnauthorized, 401, false},
		{"forbidden", swift.Forbidden, KindForbidden, 403, false},
		{"object not found", swift.ObjectNotFound, KindNotFound, 404, false},
		{"container not found", swift.ContainerNotFound, KindNotFound, 404, false},
		{"conflict", swift.ContainerNotEmpty, KindConflict, 409, false},
		{"too many requests", swift.TooManyRequests, KindRateLimited, 429, true},
		{"rate limit 498", swift.RateLimit, KindRateLimited, 498, true},
		{"timeout 408", swift.TimeoutError, KindTimeout, 408, true},
		{"server 503", &swift.Error{StatusCode: 503, Text: "unavailable"}, KindServer, 503, true},
		{"server 501 not retryable", &swift.Error{StatusCode: 501, Text: "not implemented"}, KindServer, 501, false},
		{"wrapped swift error", fmt.Errorf("op: %w", swift.ObjectNotFound), KindNotFound, 404, false},
		{"deadline", context.DeadlineExceeded, KindTimeout, 0, true},
		{"unknown", errors.New("mystery"), KindUnknown, 0, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := Normalize(tc.in)
			var se *SwiftError
			if !errors.As(err, &se) {
				t.Fatalf("Normalize(%v) = %T, want *SwiftError", tc.in, err)
			}
			if se.Kind != tc.wantKind || se.HTTPStatus != tc.wantStatus || se.Retryable != tc.wantRetryable {
				t.Fatalf("got kind=%s status=%d retryable=%v, want %s/%d/%v",
					se.Kind, se.HTTPStatus, se.Retryable, tc.wantKind, tc.wantStatus, tc.wantRetryable)
			}
			if !errors.Is(err, tc.in) {
				t.Fatalf("normalized error does not wrap the original %v", tc.in)
			}
		})
	}
}

func TestNormalizePassthrough(t *testing.T) {
	if Normalize(nil) != nil {
		t.Fatal("Normalize(nil) != nil")
	}
	if err := Normalize(context.Canceled); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation must pass through, got %v", err)
	}
	var se *SwiftError
	if errors.As(Normalize(context.Canceled), &se) {
		t.Fatal("cancellation must not be wrapped in SwiftError")
	}
	orig := Normalize(swift.ObjectNotFound)
	if again := Normalize(orig); again != orig {
		t.Fatal("Normalize must not re-wrap an already-normalized error")
	}
}

func TestWithTransID(t *testing.T) {
	err := Normalize(swift.ObjectNotFound)
	err = WithTransID(err, swift.Headers{"X-Trans-Id": "txabc123"})
	var se *SwiftError
	if !errors.As(err, &se) {
		t.Fatal("lost SwiftError type")
	}
	if se.TransID != "txabc123" {
		t.Fatalf("TransID = %q, want txabc123", se.TransID)
	}

	plain := errors.New("plain")
	if got := WithTransID(plain, swift.Headers{"X-Trans-Id": "t"}); got != plain {
		t.Fatal("WithTransID must return non-SwiftErrors unchanged")
	}
}
