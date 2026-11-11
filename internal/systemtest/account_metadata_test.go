package systemtest

import (
	"context"
	"net/textproto"
	"strings"
	"testing"

	"github.com/csmart/aeronautes/internal/app"
)

// TestAccountMetadataEdit* certify the three war-room scenarios for
// MutateService.UpdateAccountMetadata (ADR 0052 §65.10): a plain metadata
// edit applies on the authoritative working-copy fingerprint, a TempURL
// secret-slot key is refused before any I/O (K-SEC-006), and an edit built
// on a stale fingerprint is a conflict that changes nothing.
//
// Account metadata is account-global and the real-Swift fixture is shared
// across overlapping suite processes, so the test owns only its own
// process-unique keys and makes every edit a guarded, additive change: it
// either applies against a current base or conflicts and retries, and so can
// never remove a key a concurrent run added.
func TestAccountMetadataEditMock(t *testing.T)  { testAccountMetadataEdit(t, findMockHarness(t)) }
func TestAccountMetadataEditSwift(t *testing.T) { testAccountMetadataEdit(t, New(t)) }

func testAccountMetadataEdit(t *testing.T, h *Harness) {
	t.Helper()
	ctx := context.Background()
	if _, err := h.Browse.Connect(ctx, h.Primary.BookmarkID); err != nil {
		t.Fatal(err)
	}

	teamKey := textproto.CanonicalMIMEHeaderKey("Aero-" + h.ns + "-Team")
	peerKey := textproto.CanonicalMIMEHeaderKey("Aero-" + h.ns + "-Peer")
	mine := map[string]bool{teamKey: true, peerKey: true}
	t.Cleanup(func() { restoreAccountKeys(t, h, mine) })

	// A seeded container and object give "the account edit touched nothing
	// in the namespace" something to protect.
	c := h.Container(h.Primary, "acct-meta")
	h.Seed(h.Primary, c, "sentinel", []byte("do not touch"), map[string]string{"X-Object-Meta-Keep": "yes"})
	before := h.SnapshotNamespace(h.Primary)

	// HAPPY: add this test's key on the authoritative working-copy
	// fingerprint. An empty base would skip the guard entirely, so the base
	// is the fingerprint the read reported; a concurrent change makes it
	// stale, so re-read and retry.
	var dto app.MutationResultDTO
	var rec *Receipt
	for attempt := 0; ; attempt++ {
		base, meta := accountEditable(t, h, ctx)
		meta[teamKey] = "storage"
		dto, rec = h.UpdateAccountMetadata(ctx, base.EditableFingerprint, app.AccountEditDTO{Meta: meta, QuotaBytes: base.QuotaBytes})
		if rec.Err() != nil {
			t.Fatal(rec.Err())
		}
		if rec.Outcome() == "applied" {
			break
		}
		if attempt == 7 {
			t.Fatalf("happy edit never applied: last outcome %q", rec.Outcome())
		}
	}
	if dto.AccountAfter == nil || dto.AccountAfter.Meta[teamKey] != "storage" {
		t.Fatalf("after-state = %+v; want %s set", dto.AccountAfter, teamKey)
	}
	readBack, err := h.Browse.Account(ctx, h.Primary.BookmarkID)
	if err != nil || readBack.Meta[teamKey] != "storage" {
		t.Fatalf("read-back meta = %+v, %v", readBack.Meta, err)
	}
	h.AssertNoCollateral(before, h.SnapshotNamespace(h.Primary))
	rec.CreditApplied()

	// REFUSAL: a TempURL secret-slot key is refused before any I/O; the
	// metadata editor can never overwrite the hidden signing key (K-SEC-006).
	_, refuseRec := h.UpdateAccountMetadata(ctx, readBack.EditableFingerprint, app.AccountEditDTO{
		Meta: map[string]string{teamKey: "storage", "Temp-URL-Key": "attacker-controlled"}, QuotaBytes: readBack.QuotaBytes,
	})
	if refuseRec.Err() == nil {
		t.Fatal("a TempURL-key metadata entry was not refused")
	}
	if !strings.Contains(refuseRec.Err().Error(), "TempURL") {
		t.Fatalf("refusal error = %v; want it to name the TempURL secret slot", refuseRec.Err())
	}
	if after, err := h.Browse.Account(ctx, h.Primary.BookmarkID); err != nil || after.Meta[teamKey] != "storage" || after.Meta["Temp-URL-Key"] != "" {
		t.Fatalf("meta after refusal = %+v, %v; want unchanged and no TempURL key", after, err)
	}
	refuseRec.CreditRefusal()

	// CONFLICT: capture the working copy, let a competing save add a peer
	// key so the captured fingerprint goes stale, then edit on that stale
	// base. The guard must refuse rather than apply and drop the peer — the
	// boundary an invented stale string never reached.
	stale, working := accountEditable(t, h, ctx)
	applyAccountAdditions(t, h, ctx, map[string]string{peerKey: "keep"})
	working[teamKey] = "must-not-apply"
	_, conflictRec := h.UpdateAccountMetadata(ctx, stale.EditableFingerprint, app.AccountEditDTO{Meta: working, QuotaBytes: stale.QuotaBytes})
	if conflictRec.Err() != nil {
		t.Fatal(conflictRec.Err())
	}
	if conflictRec.Outcome() != "conflict" {
		t.Fatalf("stale-fingerprint outcome = %q, want conflict", conflictRec.Outcome())
	}
	if after, err := h.Browse.Account(ctx, h.Primary.BookmarkID); err != nil || after.Meta[teamKey] != "storage" || after.Meta[peerKey] != "keep" {
		t.Fatalf("meta after conflict = %+v, %v; want the applied value and the competing peer both intact", after, err)
	}
	conflictRec.CreditConflict()
}

// accountEditable reads the connected account and a fresh copy of its plain
// metadata to edit on top of.
func accountEditable(t *testing.T, h *Harness, ctx context.Context) (app.AccountDTO, map[string]string) {
	t.Helper()
	account, err := h.Browse.Account(ctx, h.Primary.BookmarkID)
	if err != nil {
		t.Fatal(err)
	}
	meta := map[string]string{}
	for k, v := range account.Meta {
		meta[k] = v
	}
	return account, meta
}

// applyAccountAdditions adds keys with a guarded, additive edit, retrying
// past a concurrent change. Because it only ever applies against a current
// base, it never sets or removes a key it did not add, so overlapping suite
// runs cannot corrupt each other.
func applyAccountAdditions(t *testing.T, h *Harness, ctx context.Context, additions map[string]string) {
	t.Helper()
	for attempt := 0; ; attempt++ {
		base, meta := accountEditable(t, h, ctx)
		for k, v := range additions {
			meta[k] = v
		}
		result, err := h.Mutate.UpdateAccountMetadata(ctx, h.ActiveScope(ctx), base.EditableFingerprint, app.AccountEditDTO{Meta: meta, QuotaBytes: base.QuotaBytes})
		if err != nil {
			t.Fatal(err)
		}
		if result.Outcome == "applied" {
			return
		}
		if attempt == 7 {
			t.Fatalf("account edit never applied: last outcome %q", result.Outcome)
		}
	}
}

// restoreAccountKeys removes only this test's own keys with a guarded,
// retried edit and requires it to apply and verify: a cleanup that is
// refused, unverified or mismatched must invalidate the scenario's claims,
// so it reports a test error rather than returning
// quietly.
func restoreAccountKeys(t *testing.T, h *Harness, mine map[string]bool) {
	bg := context.Background()
	for attempt := 0; ; attempt++ {
		current, err := h.Browse.Account(bg, h.Primary.BookmarkID)
		if err != nil {
			t.Errorf("cleanup read: %v", err)
			return
		}
		intended := map[string]string{}
		for k, v := range current.Meta {
			if !mine[k] {
				intended[k] = v
			}
		}
		result, err := h.Mutate.UpdateAccountMetadata(bg, h.ActiveScope(bg), current.EditableFingerprint, app.AccountEditDTO{Meta: intended, QuotaBytes: current.QuotaBytes})
		if err != nil {
			t.Errorf("cleanup edit: %v", err)
			return
		}
		if result.Outcome == "applied" {
			break
		}
		if attempt == 7 {
			t.Errorf("cleanup never applied: last outcome %q", result.Outcome)
			return
		}
	}
	restored, err := h.Browse.Account(bg, h.Primary.BookmarkID)
	if err != nil {
		t.Errorf("cleanup verify read: %v", err)
		return
	}
	for k := range mine {
		if _, left := restored.Meta[k]; left {
			t.Errorf("cleanup left this test's key %q behind", k)
		}
	}
}
