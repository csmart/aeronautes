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
// error, or the literal read afterwards still showed the identity that was
// shown. The result beside it carries what was observed (ADR 0092).
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
	// it found nothing, otherwise the successor with its class, length and
	// entity tag taken from the literal response, never followed or expanded.
	After *ObjectDetail
	// Accepted reports that the store answered the DELETE with success. A
	// lost reply or a server error leaves it false even when After is nil.
	Accepted bool
	TransIDs []string
}

// DeletePlainObject deletes ONE plain object the caller was shown, and only
// that (ADR 0092). The guards are ADR 0085's three with the class guard turned
// the other way: a fresh HEAD is the final comparison, a manifest refuses
// towards the manifest deletion, a symlink in either representation refuses,
// and a class, length or entity tag that differs from baseContent refuses with
// both identities. Nothing of ours runs between the HEAD's reply and the DELETE.
//
// Success is observed, not assumed: a literal read of the name afterwards must
// show the identity that was shown gone. Absence, or a successor of another
// kind or another plain identity, is applied with the successor reported; the
// same identity, a lost reply, a server error or a failed read is unverified.
// Applied says nothing about bytes being gone or about where a successor came
// from, and a match is never exclusive ownership (ADR 0020).
func (s *Session) DeletePlainObject(ctx context.Context, container, name, baseContent string) (*ObjectDeleteResult, error) {
	if baseContent == "" {
		return nil, fmt.Errorf("deleting %s/%s needs the content identity the caller was shown; a delete that cannot say what it meant to destroy is refused", container, name)
	}
	before, err := s.ObjectDetail(ctx, container, name)
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

	result := &ObjectDeleteResult{Before: before, TransIDs: []string{before.TransID}}
	status, deleteTransID, err := s.rawDelete(ctx, container, name)
	if deleteTransID != "" {
		result.TransIDs = append(result.TransIDs, deleteTransID)
	}
	switch {
	case err != nil:
		// The request may have left and nothing came back: the effect is
		// unproven, so the name is observed and the outcome stays unverified.
		result.Outcome = UpdateUnverified
		_ = s.observeAfterDelete(ctx, container, name, result)
		return result, fmt.Errorf("%w: the DELETE left without a store answer: %v", ErrUnverifiedDelete, err)
	case status >= 500:
		// A server error is a store answer that does not prove the effect
		// either way (a partial quorum can have removed replicas), so it is
		// unverified rather than failed.
		result.Outcome = UpdateUnverified
		_ = s.observeAfterDelete(ctx, container, name, result)
		return result, fmt.Errorf("%w: the store answered HTTP %d", ErrUnverifiedDelete, status)
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
	}
	result.Accepted = true

	if err := s.observeAfterDelete(ctx, container, name, result); err != nil {
		result.Outcome = UpdateUnverified
		return result, fmt.Errorf("%w: deleted (trans %s) but the literal read failed: %v", ErrUnverifiedDelete, deleteTransID, err)
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
	return r.Before != nil && r.After != nil && literalPlain(r.After) && r.After.ContentFingerprint() == r.Before.ContentFingerprint()
}

// observeAfterDelete records what a literal read finds at the name. Not found
// leaves After nil and returns nil; any other failure is returned for the
// caller to classify as unverified.
func (s *Session) observeAfterDelete(ctx context.Context, container, name string, result *ObjectDeleteResult) error {
	after, err := s.LiteralObjectHead(ctx, container, name)
	if err != nil {
		if IsNotFound(err) {
			return nil
		}
		return err
	}
	result.After = after
	if after.TransID != "" {
		result.TransIDs = append(result.TransIDs, after.TransID)
	}
	return nil
}

// literalPlain reports an observed object that is a plain object in every
// representation: classification comes before any tuple comparison, because a
// literal link and an empty plain object share the class, the zero length and
// the empty entity tag that ContentFingerprint hashes (ADR 0092 decision 3).
func literalPlain(d *ObjectDetail) bool {
	return d.Class == ClassPlain && d.SymlinkTarget == "" && !d.FollowedSymlink
}

// rawDelete issues the DELETE through the raw client so that the store's own
// status and transaction id are observed rather than folded into one error:
// the outcome rule needs to tell a refusal from a server error from a lost
// reply. A 401 is answered once with a fresh token; it refused the request, so
// nothing was repeated.
func (s *Session) rawDelete(ctx context.Context, container, name string) (int, string, error) {
	send := func() (*http.Response, error) {
		credentials, err := s.Credentials(ctx, 0)
		if err != nil {
			return nil, err
		}
		target := credentials.StorageURL + "/" + escapeObjectPath(container) + "/" + escapeObjectPath(name)
		req, err := http.NewRequestWithContext(ctx, http.MethodDelete, target, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("X-Auth-Token", credentials.Token)
		return s.raw.Do(req)
	}
	resp, err := send()
	if err == nil && resp.StatusCode == http.StatusUnauthorized {
		resp.Body.Close()
		s.ForceReauth()
		resp, err = send()
	}
	if err != nil {
		return 0, "", Normalize(err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	transID := resp.Header.Get("X-Trans-Id")
	if transID == "" {
		transID = resp.Header.Get("X-Openstack-Request-Id")
	}
	return resp.StatusCode, transID, nil
}
