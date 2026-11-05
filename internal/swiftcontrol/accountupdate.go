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

// ErrUnverifiedAccountWrite means the account POST may have changed the
// store, but the verify read could not confirm the intended state.
var ErrUnverifiedAccountWrite = errors.New("account metadata write unverified")

// AccountEdit is the complete intended editable account state: the plain
// user metadata and the account quota. TempURL key slots are secrets
// (§22, K-SEC-006) and are deliberately NOT part of it — they are
// preserved untouched across an edit.
type AccountEdit struct {
	// Meta is the complete intended plain user metadata set; the quota and
	// any TempURL key are handled apart and never belong to it.
	Meta map[string]string
	// QuotaBytes is the account quota, 0 means no quota.
	QuotaBytes int64
}

// AccountUpdateResult reports an account update attempt.
type AccountUpdateResult struct {
	Outcome UpdateOutcome
	Before  *AccountPortrait
	After   *AccountPortrait
	// Mismatch says what verify found different, for UpdateVerifyMismatch.
	Mismatch string
	// TransIDs: preflight, write, verify.
	TransIDs []string
}

// UpdateAccount applies edit to the connected account. baseFingerprint is
// the EditableFingerprint of the working copy the edit was built from; ""
// skips conflict detection.
//
// The TempURL key slots are never touched: AccountPortrait keeps them out
// of the editable Meta, so the additive delta below can neither change nor
// remove X-Account-Meta-Temp-URL-Key (K-SEC-006). Editing account metadata
// must not be a way to wipe a secret the operator cannot see.
func (s *Session) UpdateAccount(ctx context.Context, baseFingerprint string, edit AccountEdit) (*AccountUpdateResult, error) {
	if err := validateMetadata(edit.Meta, false); err != nil {
		return nil, err
	}
	// Reserved keys are refused before any I/O, not silently filtered: a
	// TempURL key slot would otherwise be SET by the delta below to a
	// caller-supplied value, overwriting the secret the portrait hides, and
	// Quota-Bytes in the map would change the quota behind the typed field.
	for key := range edit.Meta {
		switch {
		case isTempURLKey(key):
			return nil, fmt.Errorf("account metadata key %q is a TempURL secret slot and cannot be set through the metadata editor", key)
		case strings.EqualFold(key, "Quota-Bytes"):
			return nil, fmt.Errorf("account metadata key %q is the account quota; set QuotaBytes rather than a metadata entry", key)
		}
	}
	result := &AccountUpdateResult{}
	before, err := s.AccountPortrait(ctx)
	if err != nil {
		result.observeError(err)
		return result, err
	}
	result.Before = before
	result.observeTransID(before.TransID)

	if baseFingerprint != "" && before.EditableFingerprint != baseFingerprint {
		result.Outcome = UpdateConflict
		return result, nil
	}

	headers := buildAccountPostHeaders(before, edit)
	writeTransID, err := s.post(ctx, "", "", headers)
	result.observeTransID(writeTransID)
	if err != nil {
		if postUnproven(err) {
			result.Outcome = UpdateUnverified
			return result, fmt.Errorf("%w: %w", ErrUnverifiedAccountWrite, err)
		}
		return result, err
	}

	after, err := s.AccountPortrait(ctx)
	if err != nil {
		result.observeError(err)
		result.Outcome = UpdateUnverified
		return result, fmt.Errorf("%w: verify read failed: %w", ErrUnverifiedAccountWrite, err)
	}
	result.After = after
	result.observeTransID(after.TransID)

	if mismatch := verifyAccountOutcome(edit, after); mismatch != "" {
		result.Outcome = UpdateVerifyMismatch
		result.Mismatch = mismatch
		return result, nil
	}
	result.Outcome = UpdateApplied
	return result, nil
}

func (r *AccountUpdateResult) observeTransID(id string) {
	if id != "" {
		r.TransIDs = append(r.TransIDs, id)
	}
}

func (r *AccountUpdateResult) observeError(err error) {
	var se *SwiftError
	if errors.As(err, &se) {
		r.observeTransID(se.TransID)
	}
}

// buildAccountPostHeaders computes the additive delta from the portrait to
// the intended state: sets for new or changed user metadata, X-Remove-* for
// user metadata the intent no longer carries, and the quota set or removed.
// The portrait's Meta excludes the TempURL key slots and the quota, so
// neither a secret key nor the quota is ever removed by the metadata loop.
func buildAccountPostHeaders(before *AccountPortrait, edit AccountEdit) swift.Headers {
	headers := swift.Headers{}

	intended := map[string]string{}
	for key, value := range edit.Meta {
		intended[textproto.CanonicalMIMEHeaderKey(key)] = value
	}
	for key, value := range intended {
		if before.Meta[key] != value {
			headers[accountMetaPrefix+key] = value
		}
	}
	for key := range before.Meta {
		if _, keep := intended[key]; !keep {
			headers["X-Remove-Account-Meta-"+key] = "x"
		}
	}

	switch {
	case edit.QuotaBytes > 0 && edit.QuotaBytes != before.QuotaBytes:
		headers[accountMetaPrefix+"Quota-Bytes"] = strconv.FormatInt(edit.QuotaBytes, 10)
	case edit.QuotaBytes <= 0 && before.QuotaBytes > 0:
		headers["X-Remove-Account-Meta-Quota-Bytes"] = "x"
	}

	return headers
}

// verifyAccountOutcome compares the verify HEAD against the intent.
func verifyAccountOutcome(edit AccountEdit, after *AccountPortrait) string {
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
	if after.QuotaBytes != max64(edit.QuotaBytes, 0) {
		problems = append(problems, fmt.Sprintf("quota %d, want %d", after.QuotaBytes, max64(edit.QuotaBytes, 0)))
	}
	sort.Strings(problems)
	return strings.Join(problems, "; ")
}
