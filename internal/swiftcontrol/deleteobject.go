package swiftcontrol

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
)

// ErrUnverifiedDelete marks a DELETE whose effect the application does not
// know: the request left without a store answer, or the store answered with
// a server error, which a partial quorum can follow with some replicas
// removed (ADR 0122 decision 6). It is the only unknown: nothing is read
// afterwards to find out.
var ErrUnverifiedDelete = errors.New("object delete unverified")

// ObjectDeleteResult is the store's answer to one object's DELETE. It is nil
// only when no request was sent.
type ObjectDeleteResult struct {
	// Outcome is UpdateApplied when the store accepted the DELETE or answered
	// that the name was already gone, and UpdateUnverified when the effect is
	// not known. It is empty when the store refused the request.
	Outcome UpdateOutcome
	// Gone reports that the store answered not found: the name was already
	// empty, which is the outcome asked for and not a failure.
	Gone bool
	// Status is the store's HTTP status, 0 when no answer came.
	Status   int
	TransIDs []string
}

// DeleteName sends one object's DELETE and takes the store's answer as the
// result (ADR 0122 decision 6). The name is not read before the request, to
// compare it with anything, or after it, to see whether it is gone.
//
// Accepted is deleted. Not found is already gone. No answer, or a server
// error, is ErrUnverifiedDelete with the result beside it. Any other status
// is a refusal that deleted nothing, returned as a SwiftError with the
// status.
//
// The DELETE is always plain, never the form that expands a static manifest,
// so deleting a manifest leaves its segments, and a link is deleted as the
// link it is. A token the store refuses is renewed and the request sent once
// more, because a refused token deleted nothing.
func (s *Session) DeleteName(ctx context.Context, container, name string) (*ObjectDeleteResult, error) {
	if container == "" || name == "" {
		// An empty object name would address the container itself: Swift routes
		// a DELETE of "container/" to its container controller.
		return nil, errors.New("deleting an object needs both its container and its name; an empty name would address the container itself")
	}
	result := &ObjectDeleteResult{}
	for attempt := 0; ; attempt++ {
		credentials, err := s.Credentials(ctx, 0)
		if err != nil {
			if attempt == 0 {
				return nil, Normalize(err)
			}
			return result, Normalize(err)
		}
		status, transID, err := s.rawDelete(ctx, credentials, container, name)
		if transID != "" {
			result.TransIDs = append(result.TransIDs, transID)
		}
		result.Status = status
		switch {
		case err != nil:
			result.Outcome = UpdateUnverified
			return result, fmt.Errorf("%w: the DELETE left without a store answer: %v", ErrUnverifiedDelete, err)
		case status >= 500:
			result.Outcome = UpdateUnverified
			return result, fmt.Errorf("%w: the store answered HTTP %d", ErrUnverifiedDelete, status)
		case status == http.StatusUnauthorized && attempt == 0:
			// rawDelete has invalidated the refused token.
			continue
		case status == http.StatusNotFound:
			result.Outcome, result.Gone = UpdateApplied, true
			return result, nil
		case status >= 200 && status < 300:
			result.Outcome = UpdateApplied
			return result, nil
		}
		// A client-side status, a redirect or anything else outside success:
		// the store did not apply this request.
		kind, retryable := classifyStatus(status)
		message := http.StatusText(status)
		if message == "" {
			message = fmt.Sprintf("the store answered the DELETE with HTTP %d", status)
		}
		return result, &SwiftError{Kind: kind, HTTPStatus: status, Message: message, TransID: transID, Retryable: retryable}
	}
}

// rawDelete issues the DELETE under the credentials given, through the raw
// client, so the store's own status and transaction id are observed rather
// than folded into one error: the outcome rule has to tell a refusal from a
// server error from a lost reply. It never repeats the request; a refused
// token is invalidated here and the caller decides what the refusal means.
func (s *Session) rawDelete(ctx context.Context, credentials StorageCredentials, container, name string) (int, string, error) {
	if container == "" || name == "" {
		// The I/O boundary refuses too: "container/" is the container's own
		// path, and no caller may reach it through an object delete.
		return 0, "", errors.New("object DELETE: the container and object name must both be present")
	}
	target := credentials.StorageURL + "/" + escapeObjectPath(container) + "/" + escapeObjectPath(name)
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, target, nil)
	if err != nil {
		return 0, "", Normalize(err)
	}
	req.Header.Set("X-Auth-Token", credentials.Token)
	resp, err := s.raw.Do(req)
	if err != nil {
		return 0, "", Normalize(err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	if resp.StatusCode == http.StatusUnauthorized {
		// A refused token is invalidated where it is seen, as rawHead does, so
		// no later Credentials call can hand it on.
		s.ForceReauth()
	}
	return resp.StatusCode, transIDOf(resp.Header), nil
}
