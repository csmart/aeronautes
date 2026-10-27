package swiftcontrol

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
)

// ErrUnverifiedDelete marks a plain-object DELETE whose effect is not proven:
// the request left without a store answer, the store answered with a server
// error, or the literal read afterwards could not show the identity that was
// shown gone. The result beside it carries what was observed (ADR 0092).
var ErrUnverifiedDelete = errors.New("object delete unverified")

// ObjectDeleteResult reports one plain-object deletion attempt (ADR 0092).
// Refusals return an error and no result; an identity conflict returns the
// comparison HEAD beside its error so the record can cite it; an Outcome is
// set once a DELETE was issued.
type ObjectDeleteResult struct {
	// Outcome is UpdateApplied or UpdateUnverified.
	Outcome UpdateOutcome
	// Before is the final comparison HEAD, the identity the DELETE was issued
	// against.
	Before *ObjectDetail
	// After is what the literal read observed at the name afterwards: nil when
	// the read answered not found or did not answer (see AfterRead), otherwise
	// the successor with its class, length and entity tag taken from the
	// literal response, never followed or expanded.
	After *ObjectDetail
	// AfterComparable reports that the literal response carried a parseable
	// length and an entity tag, the fields a plain successor is compared by.
	// A found plain object without them is not compared and stays unverified.
	AfterComparable bool
	// AfterRead is the literal read's own failure when it neither found the
	// name nor reported it absent; nil means the read answered. With it set,
	// After is nil because nothing was observed, not because the name was
	// empty.
	AfterRead error
	// Accepted reports that the store answered the DELETE with success. A
	// lost reply, a server error or an unexpected status leaves it false.
	Accepted bool
	TransIDs []string
}

// DeletePlainObject deletes ONE plain object the caller was shown, and only
// that (ADR 0092). The guards are ADR 0085's three with the class guard turned
// the other way: a fresh HEAD is the final comparison, a manifest refuses
// towards the manifest deletion, a symlink in either representation refuses,
// and a class, length or entity tag that differs from baseContent refuses with
// both identities.
//
// The comparison HEAD, the DELETE and the verifying read share ONE credentials
// snapshot: one token and one storage URL. Nothing of ours, and no
// authentication, runs between the HEAD's reply and the DELETE, and the
// DELETE cannot land in a namespace the HEAD never read. A token the store
// refuses on the HEAD restarts the whole sequence once with fresh
// credentials; one it refuses on the DELETE is a refusal that destroyed
// nothing, and the caller's next attempt compares again.
//
// Success is observed, not assumed: a literal read of the name afterwards
// must show the identity that was shown gone. Absence, or a successor of
// another kind or another plain identity, is applied with the successor
// reported; the same identity, an incomparable plain successor, a lost
// reply, a server error, an unexpected status or a failed read is
// unverified. Applied says nothing about bytes being gone or about where a
// successor came from, and a match is never exclusive ownership (ADR 0020).
func (s *Session) DeletePlainObject(ctx context.Context, container, name, baseContent string) (*ObjectDeleteResult, error) {
	if baseContent == "" {
		return nil, fmt.Errorf("deleting %s/%s needs the content identity the caller was shown; a delete that cannot say what it meant to destroy is refused", container, name)
	}
	for attempt := 0; ; attempt++ {
		credentials, err := s.Credentials(ctx, 0)
		if err != nil {
			return nil, Normalize(err)
		}
		before, _, status, err := s.rawHead(ctx, credentials, container, name, "", "comparison HEAD")
		if status == http.StatusUnauthorized && attempt == 0 {
			// rawHead has invalidated the refused token. Fresh credentials may
			// name another storage URL, so the comparison is repeated under
			// them rather than carried over.
			continue
		}
		if err != nil {
			return nil, err
		}
		if before.Class != ClassPlain {
			return nil, fmt.Errorf("%s/%s is a %s manifest, not a plain object; the manifest deletion owns it and keeps its segments", container, name, before.Class)
		}
		if before.FollowedSymlink || before.SymlinkTarget != "" {
			// Either representation: a followed link describes its target, and a
			// cross-account link arrives unfollowed with its target header. The
			// identity shown is the target's, so a DELETE here would remove
			// something the operator was not shown (ADR 0092 decision 2).
			return nil, fmt.Errorf("%s/%s is a symlink: the identity shown is its target's and a delete at this name would remove the link instead; deleting a link is not offered here", container, name)
		}
		if found := before.ContentFingerprint(); found != baseContent {
			// The comparison HEAD travels with the refusal so the record can carry
			// its transaction id beside both identities, as the manifest deletion's
			// does; no outcome is set because nothing was issued.
			return &ObjectDeleteResult{Before: before, TransIDs: []string{before.TransID}}, &IdentityConflictError{
				Container: container, Name: name,
				Expected: baseContent, Found: found,
				FoundClass: string(before.Class), FoundLength: before.ContentLength,
				FoundETag: etagValue(before.ETag),
			}
		}
		return s.deleteCompared(ctx, credentials, container, name, before)
	}
}

// deleteCompared issues the DELETE under the credentials the comparison HEAD
// used and decides the outcome from the store's answer and the literal read.
func (s *Session) deleteCompared(ctx context.Context, credentials StorageCredentials, container, name string, before *ObjectDetail) (*ObjectDeleteResult, error) {
	result := &ObjectDeleteResult{Before: before, TransIDs: []string{before.TransID}}
	status, deleteTransID, err := s.rawDelete(ctx, credentials, container, name)
	if deleteTransID != "" {
		result.TransIDs = append(result.TransIDs, deleteTransID)
	}
	switch {
	case err != nil:
		// The request may have left and nothing came back: the effect is
		// unproven, so the name is observed and the outcome stays unverified.
		result.Outcome = UpdateUnverified
		s.observeAfterDelete(ctx, credentials, container, name, result)
		return result, fmt.Errorf("%w: the DELETE left without a store answer: %v", ErrUnverifiedDelete, err)
	case status >= 500:
		// A server error is a store answer that does not prove the effect
		// either way (a partial quorum can have removed replicas), so it is
		// unverified rather than failed.
		result.Outcome = UpdateUnverified
		s.observeAfterDelete(ctx, credentials, container, name, result)
		return result, fmt.Errorf("%w: the store answered HTTP %d", ErrUnverifiedDelete, status)
	case status == http.StatusUnauthorized:
		// The token expired between the HEAD and the DELETE. Re-authenticating
		// and deleting again would put an authentication between the final
		// comparison and the effect, which ADR 0085's adjacency forbids, so this
		// is a refusal: nothing was deleted, and the next attempt compares again
		// under the fresh credentials rawDelete's invalidation forces.
		return result, &SwiftError{Kind: KindUnauthorized, HTTPStatus: status, TransID: deleteTransID,
			Message: "the token was refused when the DELETE arrived; nothing was deleted, and the next attempt compares the object again"}
	case status >= 400:
		// A client-side status is a refusal: the store did this nothing. A 404
		// means the name emptied between the HEAD and the DELETE, by someone
		// else; it is reported as not found rather than claimed as ours.
		kind, retryable := classifyStatus(status)
		message := http.StatusText(status)
		if status == http.StatusNotFound {
			message = "the object was already gone when the DELETE arrived"
		}
		return result, &SwiftError{Kind: kind, HTTPStatus: status, Message: message, TransID: deleteTransID, Retryable: retryable}
	case status < 200 || status >= 300:
		// A redirect or any other status outside success is not an acceptance:
		// the store did not apply this request (the session's transport drops
		// redirects' Location, so a 307 arrives here as a plain status).
		return result, &SwiftError{Kind: KindUnknown, HTTPStatus: status, TransID: deleteTransID,
			Message: fmt.Sprintf("the store answered the DELETE with HTTP %d, which is not an acceptance; nothing is claimed deleted", status)}
	}
	result.Accepted = true

	if s.observeAfterDelete(ctx, credentials, container, name, result); result.AfterRead != nil {
		result.Outcome = UpdateUnverified
		return result, fmt.Errorf("%w: deleted (trans %s) but the literal read failed: %v", ErrUnverifiedDelete, deleteTransID, result.AfterRead)
	}
	if after := result.After; after != nil && literalPlain(after) && !result.AfterComparable {
		result.Outcome = UpdateUnverified
		return result, fmt.Errorf("%w: the literal read found a plain object but did not report a comparable identity (trans %s)", ErrUnverifiedDelete, deleteTransID)
	}
	if result.SameIdentityObserved() {
		result.Outcome = UpdateUnverified
		return result, fmt.Errorf("%w: the name still holds the identity that was shown (trans %s)", ErrUnverifiedDelete, deleteTransID)
	}
	result.Outcome = UpdateApplied
	return result, nil
}

// SameIdentityObserved reports that the literal read found a plain object of
// the identity the DELETE was issued against. Such a result is unverified:
// either the DELETE has not taken effect yet or the same content was written
// back, and the two cannot be told apart from here.
func (r *ObjectDeleteResult) SameIdentityObserved() bool {
	return r.Before != nil && r.After != nil && r.AfterComparable && literalPlain(r.After) && r.After.ContentFingerprint() == r.Before.ContentFingerprint()
}

// observeAfterDelete records what a literal read finds at the name under the
// same credentials the DELETE used. Not found leaves After nil; a failure is
// kept in AfterRead so a caller can tell an empty name from an unanswered
// question.
func (s *Session) observeAfterDelete(ctx context.Context, credentials StorageCredentials, container, name string, result *ObjectDeleteResult) {
	after, headers, _, err := s.rawHead(ctx, credentials, container, name, "symlink=get&multipart-manifest=get", "literal HEAD")
	if err != nil {
		if !IsNotFound(err) {
			result.AfterRead = err
		}
		return
	}
	result.After = after
	result.AfterComparable = identityComparable(headers)
	if after.TransID != "" {
		result.TransIDs = append(result.TransIDs, after.TransID)
	}
}

// literalPlain reports an observed object that is a plain object in every
// representation: classification comes before any tuple comparison, because a
// literal link and an empty plain object share the class, the zero length and
// the empty entity tag that ContentFingerprint hashes (ADR 0092 decision 3).
func literalPlain(d *ObjectDetail) bool {
	return d.Class == ClassPlain && d.SymlinkTarget == "" && !d.FollowedSymlink
}

// rawDelete issues the DELETE under the credentials given, through the raw
// client, so the store's own status and transaction id are observed rather
// than folded into one error: the outcome rule has to tell a refusal from a
// server error from a lost reply. It never repeats the request; a refused
// token is invalidated here and the caller decides what the refusal means.
func (s *Session) rawDelete(ctx context.Context, credentials StorageCredentials, container, name string) (int, string, error) {
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
		// no later Credentials call can hand it on; the caller still never
		// repeats the DELETE.
		s.ForceReauth()
	}
	transID := resp.Header.Get("X-Trans-Id")
	if transID == "" {
		transID = resp.Header.Get("X-Openstack-Request-Id")
	}
	return resp.StatusCode, transID, nil
}
