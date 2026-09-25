package swiftcontrol

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"

	"github.com/ncw/swift/v2"
)

// ErrorKind classifies a Swift failure for callers that must decide
// between retry, reauth, reconcile, and surface-to-operator (§49).
type ErrorKind string

const (
	KindUnauthorized ErrorKind = "unauthorized"
	KindForbidden    ErrorKind = "forbidden"
	KindNotFound     ErrorKind = "not_found"
	KindConflict     ErrorKind = "conflict"
	KindRateLimited  ErrorKind = "rate_limited"
	KindTooLarge     ErrorKind = "too_large"
	KindTimeout      ErrorKind = "timeout"
	KindServer       ErrorKind = "server"
	KindNetwork      ErrorKind = "network"
	KindUnknown      ErrorKind = "unknown"
)

// SwiftError is the normalized error every swiftcontrol operation returns
// (DESIGN.md §49). Retryable describes read-operation retryability only;
// an ambiguous mutation failure is reconciled (HEAD, observe, then decide),
// never blindly retried.
type SwiftError struct {
	Kind       ErrorKind
	HTTPStatus int // 0 when the failure happened below HTTP
	Message    string
	TransID    string // Swift X-Trans-Id when known
	Retryable  bool
	err        error
}

func (e *SwiftError) Error() string {
	msg := fmt.Sprintf("swift: %s (%s", e.Message, e.Kind)
	if e.HTTPStatus != 0 {
		msg += fmt.Sprintf(", HTTP %d", e.HTTPStatus)
	}
	if e.TransID != "" {
		msg += ", txn " + e.TransID
	}
	return msg + ")"
}

func (e *SwiftError) Unwrap() error {
	return e.err
}

// IsNotFound reports whether an error is a normalized 404.
func IsNotFound(err error) bool {
	var se *SwiftError
	return errors.As(err, &se) && se.Kind == KindNotFound
}

// Normalize converts any error coming out of the wire library into a
// *SwiftError. nil stays nil; context cancellation passes through
// untouched so callers can keep using errors.Is(err, context.Canceled).
func Normalize(err error) error {
	if err == nil {
		return nil
	}
	var se *SwiftError
	if errors.As(err, &se) {
		return err
	}
	if errors.Is(err, context.Canceled) {
		return err
	}

	var we *swift.Error
	if errors.As(err, &we) {
		kind, retryable := classifyStatus(we.StatusCode)
		// Provider reason phrases can echo credentials into public errors.
		message := http.StatusText(we.StatusCode)
		if message == "" {
			message = "Swift request failed"
		}
		return &SwiftError{
			Kind:       kind,
			HTTPStatus: we.StatusCode,
			Message:    message,
			Retryable:  retryable,
			err:        err,
		}
	}

	if errors.Is(err, context.DeadlineExceeded) {
		return &SwiftError{Kind: KindTimeout, Message: err.Error(), Retryable: true, err: err}
	}
	var ne net.Error
	if errors.As(err, &ne) {
		kind := KindNetwork
		if ne.Timeout() {
			kind = KindTimeout
		}
		return &SwiftError{Kind: kind, Message: err.Error(), Retryable: true, err: err}
	}

	return &SwiftError{Kind: KindUnknown, Message: err.Error(), err: err}
}

// classifyStatus maps an HTTP status to a kind and read-retryability
// (§49: retry network resets, timeouts, 408, 429, and selected 5xx).
func classifyStatus(status int) (ErrorKind, bool) {
	switch status {
	case 401:
		return KindUnauthorized, false
	case 403:
		return KindForbidden, false
	case 404:
		return KindNotFound, false
	case 408:
		return KindTimeout, true
	case 409:
		return KindConflict, false
	case 413:
		return KindTooLarge, false
	case 429, 498:
		return KindRateLimited, true
	case 500, 502, 503, 504:
		return KindServer, true
	}
	if status >= 500 {
		return KindServer, false
	}
	return KindUnknown, false
}

// WithTransID attaches the Swift transaction id from response headers to a
// normalized error, so diagnostics can quote it (§49). Non-SwiftErrors are
// returned unchanged.
func WithTransID(err error, headers swift.Headers) error {
	var se *SwiftError
	if !errors.As(err, &se) {
		return err
	}
	if id := headers["X-Trans-Id"]; id != "" {
		se.TransID = id
	} else if id := headers["X-Openstack-Request-Id"]; id != "" {
		se.TransID = id
	}
	return err
}

// IsDeclaredDigestRefused reports a 422 — the store's verdict that the
// body did not match the entity tag the request declared, measured to
// leave no object at that name and to leave a prior object untouched
// (ADR 0080).
func IsDeclaredDigestRefused(err error) bool {
	var swiftErr *SwiftError
	if errors.As(err, &swiftErr) {
		return swiftErr.HTTPStatus == 422
	}
	return false
}

// IsPreconditionFailed reports a 412 — the conditional-create verdict
// (If-None-Match: * met an existing object).
func IsPreconditionFailed(err error) bool {
	var swiftErr *SwiftError
	if errors.As(err, &swiftErr) {
		return swiftErr.HTTPStatus == 412
	}
	return false
}
