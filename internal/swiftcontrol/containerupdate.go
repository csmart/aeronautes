package swiftcontrol

import (
	"context"
	"errors"
	"fmt"
	"net/textproto"
	"sort"
	"strconv"
	"strings"

	"github.com/ncw/swift/v2"
)

// ErrVersioningUnestablished refuses a container edit whose preflight
// HEAD did not yield a versioning mode (ADR 0054).
var ErrVersioningUnestablished = errors.New("versioning mode could not be established")

// ErrModernVersioning refuses rewriting a container that uses modern
// object versioning, which v1 cannot edit safely (ADR 0054).
var ErrModernVersioning = errors.New("uses modern object versioning, which v1 cannot edit safely")

// Container mutation (§18, §19, §20, §21): the caller states the
// complete intended editable surface — the same working-copy contract as
// objects — and the update computes the additive POST delta (sets plus
// X-Remove-* headers) from the preflight state. CORS, staticweb, and
// TempURL container metadata are outside this surface and are never
// touched by the delta.

// ContainerEdit is the complete intended editable state of a container.
type ContainerEdit struct {
	// Meta is the complete intended plain user metadata set (the special
	// families — quota, TempURL, CORS, staticweb — are not part of it).
	Meta map[string]string
	// ReadACL / WriteACL "" means no ACL.
	ReadACL  string
	WriteACL string
	// Versioning per the §21 pin; Mode VersioningOff clears both headers.
	Versioning ContainerVersioning
	// QuotaBytes / QuotaCount 0 means no quota.
	QuotaBytes int64
	QuotaCount int64
}

// ContainerUpdateResult reports an update attempt.
type ContainerUpdateResult struct {
	Outcome UpdateOutcome
	Before  *ContainerDetail
	After   *ContainerDetail
	// Mismatch says what verify found different, for UpdateVerifyMismatch.
	Mismatch string
	// TransIDs: preflight, write, verify.
	TransIDs []string
}

// UpdateContainer applies edit to one container. baseFingerprint is the
// EditableFingerprint of the working copy the edit was built from; ""
// skips conflict detection.
func (s *Session) UpdateContainer(ctx context.Context, name, baseFingerprint string, edit ContainerEdit) (*ContainerUpdateResult, error) {
	if err := validateMetadata(edit.Meta, false); err != nil {
		return nil, err
	}
	before, err := s.ContainerDetail(ctx, name)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", name, errors.Join(ErrVersioningUnestablished, err))
	}
	if before.Versioning.Mode == VersioningEnabled && edit.Versioning.Mode != VersioningEnabled {
		return nil, fmt.Errorf("%s: %w — manage it with swift/CLI", name, ErrModernVersioning)
	}
	result := &ContainerUpdateResult{Before: before, TransIDs: []string{before.TransID}}

	if baseFingerprint != "" && before.EditableFingerprint != baseFingerprint {
		result.Outcome = UpdateConflict
		return result, nil
	}

	headers := buildContainerPostHeaders(before, edit)
	writeTransID, err := s.post(ctx, name, "", headers)
	if err != nil {
		return nil, err
	}
	result.TransIDs = append(result.TransIDs, writeTransID)

	after, err := s.ContainerDetail(ctx, name)
	if err != nil {
		return nil, fmt.Errorf("update written (trans %s) but verify read failed: %w", writeTransID, err)
	}
	result.After = after
	result.TransIDs = append(result.TransIDs, after.TransID)

	if mismatch := verifyContainerOutcome(edit, after); mismatch != "" {
		result.Outcome = UpdateVerifyMismatch
		result.Mismatch = mismatch
		return result, nil
	}
	result.Outcome = UpdateApplied
	return result, nil
}

// buildContainerPostHeaders computes the additive delta from the
// preflight state to the intended state: sets for new/changed values,
// X-Remove-* for everything the intent no longer carries.
func buildContainerPostHeaders(before *ContainerDetail, edit ContainerEdit) swift.Headers {
	headers := swift.Headers{}

	intended := map[string]string{}
	for key, value := range edit.Meta {
		intended[textproto.CanonicalMIMEHeaderKey(key)] = value
	}
	for key, value := range intended {
		if before.Meta[key] != value {
			headers[containerMetaPrefix+key] = value
		}
	}
	for key := range before.Meta {
		if _, keep := intended[key]; !keep {
			headers["X-Remove-Container-Meta-"+key] = "x"
		}
	}

	setOrRemove := func(setKey, removeKey, current, want string) {
		switch {
		case want != "" && want != current:
			headers[setKey] = want
		case want == "" && current != "":
			headers[removeKey] = "x"
		}
	}
	setOrRemove("X-Container-Read", "X-Remove-Container-Read", before.ReadACL, edit.ReadACL)
	setOrRemove("X-Container-Write", "X-Remove-Container-Write", before.WriteACL, edit.WriteACL)

	quota := func(name string, current, want int64) {
		switch {
		case want > 0 && want != current:
			headers[containerMetaPrefix+name] = strconv.FormatInt(want, 10)
		case want <= 0 && current > 0:
			headers["X-Remove-Container-Meta-"+name] = "x"
		}
	}
	quota("Quota-Bytes", before.QuotaBytes, edit.QuotaBytes)
	quota("Quota-Count", before.QuotaCount, edit.QuotaCount)

	wantVersions, wantHistory := "", ""
	switch edit.Versioning.Mode {
	case VersioningVersionsLocation:
		wantVersions = edit.Versioning.Archive
	case VersioningHistoryLocation:
		wantHistory = edit.Versioning.Archive
	}
	currentVersions, currentHistory := "", ""
	switch before.Versioning.Mode {
	case VersioningVersionsLocation:
		currentVersions = before.Versioning.Archive
	case VersioningHistoryLocation:
		currentHistory = before.Versioning.Archive
	}
	setOrRemove("X-Versions-Location", "X-Remove-Versions-Location", currentVersions, wantVersions)
	setOrRemove("X-History-Location", "X-Remove-History-Location", currentHistory, wantHistory)

	return headers
}

// verifyContainerOutcome compares the verify HEAD against the intent.
func verifyContainerOutcome(edit ContainerEdit, after *ContainerDetail) string {
	intended := map[string]string{}
	for key, value := range edit.Meta {
		intended[textproto.CanonicalMIMEHeaderKey(key)] = value
	}
	var problems []string
	for key, want := range intended {
		if got, ok := after.Meta[key]; !ok {
			problems = append(problems, fmt.Sprintf("metadata %q missing after write", key))
		} else if got != want {
			problems = append(problems, fmt.Sprintf("metadata %q = %q, want %q", key, got, want))
		}
	}
	for key := range after.Meta {
		if _, ok := intended[key]; !ok {
			problems = append(problems, fmt.Sprintf("unexpected metadata %q present after write", key))
		}
	}
	if after.ReadACL != edit.ReadACL {
		problems = append(problems, fmt.Sprintf("read ACL %q, want %q", after.ReadACL, edit.ReadACL))
	}
	if after.WriteACL != edit.WriteACL {
		problems = append(problems, fmt.Sprintf("write ACL %q, want %q", after.WriteACL, edit.WriteACL))
	}
	if after.Versioning != edit.Versioning && !(edit.Versioning.Mode == "" && after.Versioning.Mode == VersioningOff) {
		problems = append(problems, fmt.Sprintf("versioning %+v, want %+v", after.Versioning, edit.Versioning))
	}
	if after.QuotaBytes != max64(edit.QuotaBytes, 0) || after.QuotaCount != max64(edit.QuotaCount, 0) {
		problems = append(problems, fmt.Sprintf("quotas %d/%d, want %d/%d",
			after.QuotaBytes, after.QuotaCount, edit.QuotaBytes, edit.QuotaCount))
	}
	sort.Strings(problems)
	return strings.Join(problems, "; ")
}

func max64(v, floor int64) int64 {
	if v < floor {
		return floor
	}
	return v
}

// post POSTs headers at container level (object "") or object level and
// returns the write's transaction id.
func (s *Session) post(ctx context.Context, container, object string, headers swift.Headers) (string, error) {
	credentials, err := s.Credentials(ctx, 0)
	if err != nil {
		return "", err
	}
	resp, respHeaders, err := s.conn.Call(ctx, credentials.StorageURL, swift.RequestOpts{
		Container:  container,
		ObjectName: object,
		Operation:  "POST",
		Headers:    headers,
		NoResponse: true,
		OnReAuth:   s.auth.storageTarget,
	})
	if err != nil {
		return "", Normalize(err)
	}
	if resp != nil && resp.Body != nil {
		resp.Body.Close()
	}
	transID := respHeaders["X-Trans-Id"]
	if transID == "" {
		transID = respHeaders["X-Openstack-Request-Id"]
	}
	return transID, nil
}
