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

// ObjectDeleteResult reports one deletion attempt on an object the caller was
// shown: a plain object (ADR 0092), a directory marker or a manifest (ADR
// 0094). Refusals return an error and no result; an identity conflict returns
// the comparison HEAD beside its error so the record can cite it; an Outcome
// is set once a DELETE was issued.
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
	// Requested reports that the DELETE left: the store was asked, whatever
	// it answered or failed to answer. A refusal or a conflict at the
	// comparison leaves it false, because nothing was sent.
	Requested bool
	// Baseline is the identity a selected member's comparison was made
	// against (ADR 0093 decision 3): the listed row as a plain object, or the
	// HEAD taken first for a row that could not say what it held. Nil on the
	// shown-object paths, whose caller holds the identity it was shown.
	Baseline *ObjectDetail
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
	return s.deleteShown(ctx, container, name, baseContent, targetPlain)
}

// DeleteMarkerObject deletes ONE directory marker the caller was shown, by
// the plain-object protocol plus the marker rule (ADR 0094 decision 2): the
// comparison HEAD must show a plain object of zero bytes whose content type
// is application/directory, because every empty object has the same content
// fingerprint and the rule is what keeps a real object in a folder's clothing
// out of this path. The objects under the prefix are never read or touched.
func (s *Session) DeleteMarkerObject(ctx context.Context, container, prefix, baseContent string) (*ObjectDeleteResult, error) {
	return s.deleteShown(ctx, container, prefix, baseContent, targetMarker)
}

// DeleteManifestObject deletes ONE dynamic or static large-object manifest
// the caller was shown, manifest-only: the DELETE is always plain and never
// the segment-expanding form, so every segment stays by construction (§60
// Phase 4). It is the plain-object protocol with the class inverted (ADR 0094
// decision 3): a plain object refuses towards the object deletion, a link in
// either representation refuses, and the identity compared is the one the
// inspector's read shows, the assembled length and entity tag.
//
// The literal read afterwards describes the stored manifest, not the
// assembled content the comparison described, so the two are not compared
// as identities: absence, or a successor that is not a manifest of the same
// kind, is applied with the successor reported; a manifest of the same kind
// still observed is unverified, as is a lost reply, a server error or a
// failed read.
func (s *Session) DeleteManifestObject(ctx context.Context, container, name, baseContent string) (*ObjectDeleteResult, error) {
	return s.deleteShown(ctx, container, name, baseContent, targetManifest)
}

// ShownRefusalError is a destructive request refused at the comparison HEAD
// because what the name holds is not of the kind this path deletes: a link in
// either representation, a manifest on the object or marker path, a plain
// object on the manifest path, or an object in a folder's clothing on the
// marker path. It carries the observation so the record can say what was
// found, as an identity conflict does (ADR 0094 decisions 1 and 2): the
// identities shown and found, the class, the length, the entity tag and the
// content type, with the comparison's transaction id on the result beside it.
// Link reports a link in either representation, a followed one as much as a
// cross-account one that arrives with its target named; the target itself is
// not carried, because it is a name this code does not control and the
// record withholds it either way.
type ShownRefusalError struct {
	Container, Name string
	Reason          string
	Expected, Found string
	FoundClass      string
	FoundLength     int64
	FoundETag       string
	FoundType       string
	Link            bool
}

func (e *ShownRefusalError) Error() string { return e.Reason }

func shownRefusal(container, name, baseContent, reason string, before *ObjectDetail) *ShownRefusalError {
	return &ShownRefusalError{
		Container: container, Name: name, Reason: reason,
		Expected: baseContent, Found: before.ContentFingerprint(),
		FoundClass: string(before.Class), FoundLength: before.ContentLength,
		FoundETag: etagValue(before.ETag), FoundType: before.ContentType,
		Link: before.FollowedSymlink || before.SymlinkTarget != "",
	}
}

// MemberRow is the listing row a selected-set deletion carries for one
// member (ADR 0093 decision 2): the facts the baseline rule reads.
type MemberRow struct {
	Name      string
	Bytes     int64
	ETag      string
	SLOListed bool
}

// DeleteMember deletes ONE member of a selected set by the verified per-name
// protocol (ADR 0093 decision 3). A member listed with a non-zero size and
// not marked as a static large object has its listing row as the baseline,
// a plain object of those bytes and that entity tag; a member listed as zero
// bytes or as a static large object takes a HEAD of its own as a later
// baseline, because such a row may be a plain object, a marker, a manifest
// of either kind or a link, and the row cannot say which. A link found at
// the later baseline is refused at once.
//
// The comparison HEAD, the DELETE and the literal read then run as for any
// shown object, under one credentials snapshot. Any difference between the
// baseline and the comparison, class included, refuses the member as changed
// since listed with both identities, so an ordinary row whose name now holds
// a manifest or a link is a conflict and never a replacement baseline; a
// member whose baseline was a manifest is deleted manifest-only and judged by
// the manifest words afterwards, one whose baseline was plain by the plain
// words. No bulk request is sent and no DELETE is repeated.
func (s *Session) DeleteMember(ctx context.Context, container string, row MemberRow) (*ObjectDeleteResult, error) {
	if container == "" || row.Name == "" {
		return nil, errors.New("deleting a member needs both its container and its name; an empty name would address the container itself")
	}
	target := targetListedPlain
	var baseline *ObjectDetail
	if row.Bytes != 0 && !row.SLOListed {
		baseline = &ObjectDetail{Class: ClassPlain, ContentLength: row.Bytes, ETag: row.ETag}
	} else {
		head, err := s.ObjectDetail(ctx, container, row.Name)
		if err != nil {
			return nil, err
		}
		if head.FollowedSymlink || head.SymlinkTarget != "" {
			// Refused with its kind before any baseline is fixed: the typed
			// refusal says a link was found, and the read's transaction id
			// travels with it, so the member's record can say why.
			return &ObjectDeleteResult{TransIDs: []string{head.TransID}}, shownRefusal(container, row.Name, "",
				fmt.Sprintf("%s/%s is a symlink: the identity listed is its target's and a delete at this name would remove the link instead; deleting a link is not offered here", container, row.Name), head)
		}
		baseline = head
		if head.Class != ClassPlain {
			target = targetListedManifest
		}
	}
	result, err := s.deleteShown(ctx, container, row.Name, baseline.ContentFingerprint(), target)
	if result != nil {
		result.Baseline = baseline
	}
	return result, err
}

// deleteTarget is what a deletion's comparison HEAD must show for the DELETE
// to be issued, and how what the literal read finds afterwards is judged.
type deleteTarget int

const (
	targetPlain deleteTarget = iota
	targetMarker
	targetManifest
	// The listed targets are a selected set's members (ADR 0093 decision 3):
	// no class is refused at admission, because the baseline carries the
	// class and a change of class is a conflict with both identities; the
	// literal read afterwards is judged by the baseline's kind.
	targetListedPlain
	targetListedManifest
)

// manifestWords says the literal read after the DELETE is judged by the
// manifest words: no identity comparison, a manifest of the same kind still
// observed is unverified (ADR 0094 decision 3, ADR 0093 decision 3).
func (t deleteTarget) manifestWords() bool {
	return t == targetManifest || t == targetListedManifest
}

// listed says the target is a selected member whose baseline is already
// established, by its listing or by its own earlier HEAD.
func (t deleteTarget) listed() bool {
	return t == targetListedPlain || t == targetListedManifest
}

// admit refuses, before any DELETE, what the comparison HEAD shows when it is
// not of the kind this target deletes. Classification comes before the
// identity comparison, so a link or a manifest is never compared by tuple
// and a refusal names the class found rather than a mismatch (ADR 0092
// decision 3). The marker rule is not a classification and comes after the
// comparison: a real object at a marker's name is a changed identity, a
// conflict with both identities recorded, while an empty object of another
// content type shares every marker's fingerprint and is refused by the rule.
func (t deleteTarget) admit(container, name string, before *ObjectDetail) error {
	if before.FollowedSymlink || before.SymlinkTarget != "" {
		// Either representation: a followed link describes its target, and a
		// cross-account link arrives unfollowed with its target header. The
		// identity shown is the target's, so a DELETE here would remove
		// something the operator was not shown (ADR 0092 decision 2).
		return fmt.Errorf("%s/%s is a symlink: the identity shown is its target's and a delete at this name would remove the link instead; deleting a link is not offered here", container, name)
	}
	switch t {
	case targetPlain:
		if before.Class != ClassPlain {
			return fmt.Errorf("%s/%s is a %s manifest, not a plain object; the manifest deletion owns it and keeps its segments", container, name, before.Class)
		}
	case targetMarker:
		if before.Class != ClassPlain {
			return fmt.Errorf("%s/%s is a %s manifest, not a directory marker; the manifest deletion owns it", container, name, before.Class)
		}
	case targetManifest:
		if before.Class == ClassPlain {
			return fmt.Errorf("%s/%s is a plain object, not a manifest; the manifest-only delete refuses it", container, name)
		}
	}
	return nil
}

// markerShaped is the marker rule the prefix classification uses (§14.1): an
// explicit zero-byte object of the directory content type.
func markerShaped(d *ObjectDetail) bool {
	return d.ContentLength == 0 && d.ContentType == "application/directory"
}

// deleteShown is the protocol every shown-object deletion runs: one
// credentials snapshot, the comparison HEAD, the target's admission, the
// identity comparison, the DELETE immediately after, and the literal read.
func (s *Session) deleteShown(ctx context.Context, container, name, baseContent string, target deleteTarget) (*ObjectDeleteResult, error) {
	return s.deleteShownBound(ctx, container, name, baseContent, target, nil)
}

// deleteShownBound is deleteShown with an optional reviewed-graph binding for
// a segment job's manifest member (ADR 0095 decision 8): under the one
// credentials snapshot, a static manifest's entries are read immediately
// before the comparison HEAD and a dynamic manifest's namespace is compared
// against the one the HEAD reports, and any difference refuses the manifest as
// changed since reviewed with nothing deleted. The binding is nil on every
// other path, which then behaves exactly as before.
func (s *Session) deleteShownBound(ctx context.Context, container, name, baseContent string, target deleteTarget, graph *reviewedGraph) (*ObjectDeleteResult, error) {
	if container == "" || name == "" {
		// An empty object name would address the container itself: Swift routes
		// a DELETE of "container/" to its container controller. Refused before
		// any request, whatever identity the caller supplies.
		return nil, errors.New("deleting an object needs both its container and its name; an empty name would address the container itself")
	}
	if baseContent == "" {
		return nil, fmt.Errorf("deleting %s/%s needs the content identity the caller was shown; a delete that cannot say what it meant to destroy is refused", container, name)
	}
	for attempt := 0; ; attempt++ {
		credentials, err := s.Credentials(ctx, 0)
		if err != nil {
			return nil, Normalize(err)
		}
		var liveEntries []ReviewedEntry
		liveGraphWhole := true
		if graph != nil && graph.isStatic {
			live, err := s.sloManifestWith(ctx, credentials, container, name)
			var se *SwiftError
			if errors.As(err, &se) && se.Kind == KindUnauthorized && attempt == 0 {
				// The token was refused reading the graph. Fresh credentials may
				// name another storage URL, so the whole comparison restarts
				// under them rather than reading the graph at one authority and
				// deleting at another (ADR 0095 decision 8).
				continue
			}
			if err != nil {
				return nil, err
			}
			liveEntries, liveGraphWhole = live.WholeObjectEntries()
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
		if target.listed() && (before.FollowedSymlink || before.SymlinkTarget != "") {
			// A selected member's baseline is established, so a link found at
			// the comparison is a change since then: refused as a conflict with
			// both identities, never compared by tuple, and never deleted (ADR
			// 0093 decision 3). The shown-object paths keep their link refusal.
			return &ObjectDeleteResult{Before: before, TransIDs: []string{before.TransID}}, &IdentityConflictError{
				Container: container, Name: name,
				Expected: baseContent, Found: before.ContentFingerprint(),
				FoundClass: "symlink", FoundLength: before.ContentLength, FoundETag: etagValue(before.ETag),
			}
		}
		if err := target.admit(container, name, before); err != nil {
			// The comparison HEAD travels with the refusal, as with a conflict,
			// so the record can cite what was found and the transaction that
			// found it; no outcome is set because nothing was issued.
			return &ObjectDeleteResult{Before: before, TransIDs: []string{before.TransID}},
				shownRefusal(container, name, baseContent, err.Error(), before)
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
		if graph != nil {
			if changed, expected, found := graph.differs(before, liveEntries, liveGraphWhole); changed {
				// The identity matched but the graph the operator reviewed did
				// not: an equal-tag segment swap or a moved namespace. Refused
				// as changed since reviewed, with nothing deleted and the
				// manifest's current identity carried for the record.
				return &ObjectDeleteResult{Before: before, TransIDs: []string{before.TransID}}, &IdentityConflictError{
					Container: container, Name: name, Expected: expected, Found: found,
					FoundClass: string(before.Class), FoundLength: before.ContentLength, FoundETag: etagValue(before.ETag),
				}
			}
		}
		if target == targetMarker && !markerShaped(before) {
			// Classification never authorises destruction of data it does not
			// prove (§24.4 spirit): an empty object in a folder's clothing has
			// every marker's fingerprint and is still not a marker, and its
			// content type is what the record has to say.
			return &ObjectDeleteResult{Before: before, TransIDs: []string{before.TransID}},
				shownRefusal(container, name, baseContent, fmt.Sprintf("the object at %s/%s is not a directory marker (%s, %d bytes); refusing to delete it through the marker path",
					container, name, before.ContentType, before.ContentLength), before)
		}
		return s.deleteCompared(ctx, credentials, container, name, before, target)
	}
}

// deleteCompared issues the DELETE under the credentials the comparison HEAD
// used and decides the outcome from the store's answer and the literal read.
func (s *Session) deleteCompared(ctx context.Context, credentials StorageCredentials, container, name string, before *ObjectDetail, target deleteTarget) (*ObjectDeleteResult, error) {
	result := &ObjectDeleteResult{Before: before, TransIDs: []string{before.TransID}, Requested: true}
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
	if target.manifestWords() {
		// The comparison described the assembled content and the literal read
		// describes the stored manifest, so no identity is compared: a manifest
		// of the same kind still at the name is the uncertainty itself.
		if result.SameKindManifestObserved() {
			result.Outcome = UpdateUnverified
			return result, fmt.Errorf("%w: the name still holds a %s manifest (trans %s)", ErrUnverifiedDelete, result.Before.Class, deleteTransID)
		}
		result.Outcome = UpdateApplied
		return result, nil
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

// SameKindManifestObserved reports that the literal read found a manifest of
// the class the DELETE was issued against. Nothing says whether it is the
// manifest that was shown, so a manifest deletion that observes one is
// unverified (ADR 0094 decision 3).
func (r *ObjectDeleteResult) SameKindManifestObserved() bool {
	return r.Before != nil && r.After != nil && r.Before.Class != ClassPlain && r.After.Class == r.Before.Class
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
