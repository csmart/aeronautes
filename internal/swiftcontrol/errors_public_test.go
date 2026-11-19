package swiftcontrol

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ncw/swift/v2"
)

func TestBulkDeletePublicDiagnosticsWithholdProviderText(t *testing.T) {
	const secret = "1234567890123456789"
	for _, test := range []struct {
		name, status string
		deleted      int64
		rows         [][]string
		valid        bool
	}{
		{"failure_reason", "400 " + secret, 0, [][]string{{"/media/a", "403 " + secret}}, true},
		{"invalid_status", "200 OK", 0, [][]string{{"/media/a", secret}}, false},
		{"duplicate", "400 Bad Request", 0, [][]string{{"/media/a", "403 " + secret}, {"/media/a", "503 " + secret}}, false},
		{"unmatched_path", "400 Bad Request", 0, [][]string{{"/media/" + secret, "403 Forbidden"}}, false},
		{"invalid_overall", secret, 1, nil, false},
		{"unsupported_overall", "201 " + secret, 1, nil, false},
		{"unexplained_failure", "500 " + secret, 1, nil, false},
		{"excessive_count", "200 OK", 1234567890123456789, nil, false},
		{"negative_count", "200 OK", -1234567890123456789, nil, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if strings.HasPrefix(r.URL.Path, "/auth/") {
					w.Header().Set("X-Storage-Url", "http://"+r.Host+"/v1/AUTH_test")
					w.Header().Set("X-Auth-Token", secret)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				if err := json.NewEncoder(w).Encode(map[string]any{
					"Number Deleted": test.deleted, "Number Not Found": 0,
					"Response Status": test.status, "Errors": test.rows,
				}); err != nil {
					t.Error(err)
				}
			}))
			defer server.Close()
			result, err := mustSession(t, server.URL).BulkDelete(t.Context(), "media", []string{"a"})
			if result == nil || result.Validated != test.valid || (err == nil) != test.valid {
				t.Fatalf("reply validation changed: %+v %v", result, err)
			}
			if strings.Contains(fmt.Sprint(err, result.Anomalies, result.Failures), secret) {
				t.Fatalf("public diagnostics exposed provider text: %v %+v %+v", err, result.Anomalies, result.Failures)
			}
			if test.valid && (result.Deleted != 0 || len(result.Failures) != 1 || result.Failures[0] != (BulkDeleteFailure{Name: "a", Status: "403 Forbidden"})) {
				t.Fatalf("the reported failure lost its identity or status: %+v", result)
			}
		})
	}
}

func TestNormalizePublicHTTPMessages(t *testing.T) {
	cases := []struct {
		name      string
		wire      *swift.Error
		message   string
		kind      ErrorKind
		retryable bool
	}{
		{"not_modified", swift.NotModified, "Not Modified", KindUnknown, false},
		{"bad_request", swift.BadRequest, "Bad Request", KindUnknown, false},
		{"authorization", swift.AuthorizationFailed, "Unauthorized", KindUnauthorized, false},
		{"forbidden", swift.Forbidden, "Forbidden", KindForbidden, false},
		{"object_missing", swift.ObjectNotFound, "Not Found", KindNotFound, false},
		{"container_missing", swift.ContainerNotFound, "Not Found", KindNotFound, false},
		{"timeout", swift.TimeoutError, "Request Timeout", KindTimeout, true},
		{"conflict", swift.ContainerNotEmpty, "Conflict", KindConflict, false},
		{"too_large", swift.TooLargeObject, "Request Entity Too Large", KindTooLarge, false},
		{"corrupted", swift.ObjectCorrupted, "Unprocessable Entity", KindUnknown, false},
		{"rate_limit", swift.TooManyRequests, "Too Many Requests", KindRateLimited, true},
		{"native_rate_limit", swift.RateLimit, "Swift request failed", KindRateLimited, true},
		{"provider_reason", &swift.Error{StatusCode: 500, Text: "synthetic-provider-key"}, "Internal Server Error", KindServer, true},
		{"not_implemented", &swift.Error{StatusCode: 501, Text: "synthetic-provider-key"}, "Not Implemented", KindServer, false},
		{"precondition", &swift.Error{StatusCode: 412, Text: "synthetic-provider-key"}, "Precondition Failed", KindUnknown, false},
		{"unknown_client", &swift.Error{StatusCode: 499, Text: "synthetic-provider-key"}, "Swift request failed", KindUnknown, false},
		{"unknown_server", &swift.Error{StatusCode: 599, Text: "synthetic-provider-key"}, "Swift request failed", KindServer, false},
		{"zero", &swift.Error{Text: "synthetic-provider-key"}, "Swift request failed", KindUnknown, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for _, wrapped := range []bool{false, true} {
				t.Run(fmt.Sprintf("wrapped_%t", wrapped), func(t *testing.T) {
					var original error = tc.wire
					if wrapped {
						original = fmt.Errorf("synthetic-provider-prefix: %w: synthetic-body-excerpt", original)
					}
					got := Normalize(original)
					var normalized *SwiftError
					if !errors.As(got, &normalized) {
						t.Fatalf("normalized type = %T, want SwiftError", got)
					}
					if normalized.Message != tc.message {
						t.Errorf("public Message = %q, want local %q", normalized.Message, tc.message)
					}
					if normalized.HTTPStatus != tc.wire.StatusCode || normalized.Kind != tc.kind || normalized.Retryable != tc.retryable {
						t.Errorf("classification = %d/%s/%t, want %d/%s/%t", normalized.HTTPStatus, normalized.Kind, normalized.Retryable, tc.wire.StatusCode, tc.kind, tc.retryable)
					}
					want := fmt.Sprintf("swift: %s (%s", tc.message, tc.kind)
					if tc.wire.StatusCode != 0 {
						want += fmt.Sprintf(", HTTP %d", tc.wire.StatusCode)
					}
					if got.Error() != want+")" {
						t.Errorf("public error = %q, want %q", got.Error(), want+")")
					}
					var wire *swift.Error
					if errors.Unwrap(got) != original || !errors.Is(got, original) || !errors.Is(got, tc.wire) || !errors.As(got, &wire) || wire != tc.wire {
						t.Error("normalization changed the original/native cause identity")
					}
					if IsNotFound(got) != (tc.wire.StatusCode == 404) {
						t.Error("typed missing predicate changed")
					}
				})
			}
		})
	}
}

func TestNormalizePublicHTTPPreservesPassthrough(t *testing.T) {
	wire := &swift.Error{StatusCode: 500, Text: "synthetic-private-reason"}
	existing := &SwiftError{Kind: KindConflict, HTTPStatus: 409, Message: "existing message", TransID: "txexisting", err: wire}
	cases := []struct {
		name string
		err  error
	}{
		{"nil", nil},
		{"cancelled", context.Canceled},
		{"wrapped_cancellation", fmt.Errorf("cancel: %w", context.Canceled)},
		{"joined_cancellation", errors.Join(wire, context.Canceled)},
		{"already_normalized", existing},
		{"wrapped_normalized", fmt.Errorf("existing: %w", existing)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := Normalize(tc.err); got != tc.err {
				t.Fatalf("passthrough identity changed: got %v, want %v", got, tc.err)
			}
		})
	}
}

func TestNormalizePublicHTTPTransactionID(t *testing.T) {
	cases := []struct {
		name    string
		headers swift.Headers
		want    string
	}{
		{"primary", swift.Headers{"X-Trans-Id": "txprimary", "X-Openstack-Request-Id": "txfallback"}, "txprimary"},
		{"fallback", swift.Headers{"X-Openstack-Request-Id": "txfallback"}, "txfallback"},
		{"absent", nil, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			wire := &swift.Error{StatusCode: 500, Text: "synthetic-private-reason"}
			original := Normalize(wire)
			got := WithTransID(original, tc.headers)
			var normalized *SwiftError
			if got != original || !errors.As(got, &normalized) || normalized.TransID != tc.want || !errors.Is(got, wire) {
				t.Fatalf("transaction attachment changed: %v", got)
			}
			if normalized.Message != "Internal Server Error" {
				t.Errorf("transaction attachment exposed native text: %q", normalized.Message)
			}
		})
	}
}

func TestBulkDeleteMalformedCounterWithholdsProviderText(t *testing.T) {
	const secret = "123456789012345678901234567890"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/auth/") {
			w.Header().Set("X-Storage-Url", "http://"+r.Host+"/v1/AUTH_test")
			w.Header().Set("X-Auth-Token", secret)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(map[string]any{
			"Number Deleted":   json.Number(r.Header.Get("X-Auth-Token")),
			"Number Not Found": 0, "Response Status": "200 OK", "Errors": [][]string{},
		}); err != nil {
			t.Error(err)
		}
	}))
	defer server.Close()
	result, err := mustSession(t, server.URL).BulkDelete(t.Context(), "media", []string{"a"})
	if result != nil || err == nil {
		t.Fatalf("malformed counter was accepted: %+v %v", result, err)
	}
	if strings.Contains(err.Error(), secret) {
		t.Fatalf("decoder exposed provider text: %v", err)
	}
}
