package swiftcontrol

import (
	"context"
	"errors"
	"fmt"
	"net/textproto"
	"slices"
	"strings"

	"github.com/ncw/swift/v2"
)

// TempURL key slots (§22, ADR 0101). Swift reads two slots at the account
// and at each container, stored as metadata under the ordinary *-Meta-
// prefix, so the portraits report their presence and withhold their
// values. Setting or clearing one is its own operation rather than a
// metadata edit: an editor that shows every value cannot hold one it must
// never show, and a complete-set edit would remove a slot the operator
// did not see.

// ErrUnverifiedTempURLKeyWrite means the POST may have changed the slot,
// but the verify read could not confirm it (ADR 0062).
var ErrUnverifiedTempURLKeyWrite = errors.New("tempurl key write unverified")

// tempURLSlots are the two slot names Swift reads, in canonical form.
var tempURLSlots = []string{"Temp-Url-Key", "Temp-Url-Key-2"}

// maxTempURLSecretBytes is what a Swift metadata value may hold.
const maxTempURLSecretBytes = 256

// TempURLKeyEdit sets one slot to Secret, or clears it.
type TempURLKeyEdit struct {
	// Slot is Temp-URL-Key or Temp-URL-Key-2, in any letter case.
	Slot string
	// Secret is the new key. It must keep its literal identity over HTTP:
	// no control bytes and no leading or trailing space or tab, because an
	// empty header value deletes the slot, so a secret of spaces would
	// clear what it meant to set.
	Secret string
	// Clear removes the slot, and is the only way to ask for that: an
	// empty Secret without it is refused rather than read as a clear.
	Clear bool
}

// TempURLKeyResult reports a slot change. After a verified write the
// portrait of the target is set, AccountAfter for the account and
// ContainerAfter for a container; Mismatch names the slot and the kind of
// difference, never a value.
type TempURLKeyResult struct {
	Outcome        UpdateOutcome
	AccountAfter   *AccountPortrait
	ContainerAfter *ContainerDetail
	Mismatch       string
	transIDTrail
}

// UpdateTempURLKey sets or clears one TempURL key slot of the account
// (container "") or of one container. baseSlots is the slot presence the
// operator was shown, compared with the preflight's presence as observed
// lists: a portrait with no slots reports nil, and nil and empty are the
// same observation, so there is no value that skips the check. The secret
// is sent only in this operation's POST, which the library's authentication
// retry may repeat, and is kept out of every result, error and mismatch.
func (s *Session) UpdateTempURLKey(ctx context.Context, container string, baseSlots []string, edit TempURLKeyEdit) (*TempURLKeyResult, error) {
	slot, err := validateTempURLKeyEdit(edit)
	if err != nil {
		return nil, err
	}
	metaPrefix, removePrefix := accountMetaPrefix, "X-Remove-Account-Meta-"
	if container != "" {
		metaPrefix, removePrefix = containerMetaPrefix, "X-Remove-Container-Meta-"
	}

	result := &TempURLKeyResult{}
	before, err := s.tempURLTarget(ctx, container)
	if err != nil {
		result.observeError(err)
		return result, err
	}
	result.observeTransID(before.transID)
	if !sameSlots(baseSlots, before.slots) {
		result.Outcome = UpdateConflict
		return result, nil
	}

	headers := swift.Headers{}
	if edit.Clear {
		headers[removePrefix+slot] = "x"
	} else {
		headers[metaPrefix+slot] = edit.Secret
	}
	writeTransID, err := s.post(ctx, container, "", headers)
	result.observeTransID(writeTransID)
	if err != nil {
		if postUnproven(err) {
			result.Outcome = UpdateUnverified
			return result, fmt.Errorf("%w: %w", ErrUnverifiedTempURLKeyWrite, err)
		}
		return result, err
	}

	after, err := s.tempURLTarget(ctx, container)
	if err != nil {
		result.observeError(err)
		result.Outcome = UpdateUnverified
		return result, fmt.Errorf("%w: verify read failed: %w", ErrUnverifiedTempURLKeyWrite, err)
	}
	result.observeTransID(after.transID)
	result.AccountAfter, result.ContainerAfter = after.account, after.container

	value, present := after.headers[metaPrefix+slot]
	switch {
	case edit.Clear && present:
		result.Mismatch = fmt.Sprintf("slot %s is still present after the clear", slot)
	case !edit.Clear && !present:
		result.Mismatch = fmt.Sprintf("slot %s is absent after the set", slot)
	case !edit.Clear && value != edit.Secret:
		result.Mismatch = fmt.Sprintf("slot %s holds a different value after the set", slot)
	}
	if result.Mismatch != "" {
		result.Outcome = UpdateVerifyMismatch
		return result, nil
	}
	result.Outcome = UpdateApplied
	return result, nil
}

// validateTempURLKeyEdit refuses, before any request, a slot Swift does
// not read or a secret that would not survive the wire as typed. No
// message carries the secret.
func validateTempURLKeyEdit(edit TempURLKeyEdit) (string, error) {
	slot := textproto.CanonicalMIMEHeaderKey(edit.Slot)
	if !slices.Contains(tempURLSlots, slot) {
		return "", fmt.Errorf("tempurl key slot %q is not one of Temp-URL-Key or Temp-URL-Key-2", edit.Slot)
	}
	if edit.Clear {
		if edit.Secret != "" {
			return "", errors.New("a tempurl key clear carries no secret")
		}
		return slot, nil
	}
	secret := edit.Secret
	switch {
	case secret == "":
		return "", errors.New("tempurl key secret is empty; use clear to remove the slot")
	case len(secret) > maxTempURLSecretBytes:
		return "", fmt.Errorf("tempurl key secret is %d bytes; Swift keeps at most %d", len(secret), maxTempURLSecretBytes)
	case strings.Trim(secret, " \t") != secret:
		return "", errors.New("tempurl key secret has leading or trailing ASCII spaces or tabs, which HTTP does not preserve")
	}
	for i := 0; i < len(secret); i++ {
		if secret[i] < ' ' || secret[i] == 0x7f {
			return "", errors.New("tempurl key secret contains an HTTP header control byte")
		}
	}
	return slot, nil
}

// tempURLTarget is one HEAD of the account or a container: its portrait,
// the canonical headers the portrait was built from (the only place a
// slot's value is read, to compare and drop), and the slot presence.
type tempURLTarget struct {
	account   *AccountPortrait
	container *ContainerDetail
	headers   map[string]string
	slots     []string
	transID   string
}

func (s *Session) tempURLTarget(ctx context.Context, container string) (*tempURLTarget, error) {
	if container == "" {
		portrait, headers, err := s.accountHead(ctx)
		if err != nil {
			return nil, err
		}
		return &tempURLTarget{account: portrait, headers: headers, slots: portrait.TempURLKeySlots, transID: portrait.TransID}, nil
	}
	detail, headers, err := s.containerHead(ctx, container)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", container, err)
	}
	return &tempURLTarget{container: detail, headers: headers, slots: detail.TempURLKeySlots, transID: detail.TransID}, nil
}

// sameSlots compares two slot presence lists regardless of order and
// letter case.
func sameSlots(a, b []string) bool {
	canon := func(slots []string) []string {
		out := make([]string, 0, len(slots))
		for _, slot := range slots {
			out = append(out, textproto.CanonicalMIMEHeaderKey(slot))
		}
		slices.Sort(out)
		return out
	}
	return slices.Equal(canon(a), canon(b))
}
