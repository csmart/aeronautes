package swiftcontrol

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/csmart/aeronautes/internal/swifttest"
	"github.com/ncw/swift/v2"
)

// TestBuildAccountPostHeaders is the safety test: the additive delta never
// names a TempURL key slot, so editing user metadata can neither change nor
// remove X-Account-Meta-Temp-URL-Key (K-SEC-006). The portrait keeps the
// secret out of the editable Meta, which is exactly what protects it here.
func TestBuildAccountPostHeaders(t *testing.T) {
	before := &AccountPortrait{
		Meta:            map[string]string{"Keep": "old", "Drop": "gone"},
		TempURLKeySlots: []string{"Temp-URL-Key"},
		QuotaBytes:      500,
	}

	headers := buildAccountPostHeaders(before, AccountEdit{
		Meta:       map[string]string{"Keep": "new", "Added": "yes"},
		QuotaBytes: 500,
	})
	want := map[string]string{
		"X-Account-Meta-Keep":        "new",
		"X-Account-Meta-Added":       "yes",
		"X-Remove-Account-Meta-Drop": "x",
	}
	assertHeaders(t, headers, want)

	// Clearing the quota and every user key still leaves the secret alone.
	cleared := buildAccountPostHeaders(before, AccountEdit{})
	assertHeaders(t, cleared, map[string]string{
		"X-Remove-Account-Meta-Keep":        "x",
		"X-Remove-Account-Meta-Drop":        "x",
		"X-Remove-Account-Meta-Quota-Bytes": "x",
	})
}

func assertHeaders(t *testing.T, got swift.Headers, want map[string]string) {
	t.Helper()
	for k := range got {
		if strings.Contains(k, "Temp-URL-Key") {
			t.Errorf("delta touches a TempURL secret slot: %q", k)
		}
	}
	if len(got) != len(want) {
		t.Errorf("headers = %v, want %v", map[string]string(got), want)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("header %q = %q, want %q", k, got[k], v)
		}
	}
}

func TestVerifyAccountOutcome(t *testing.T) {
	edit := AccountEdit{Meta: map[string]string{"Keep": "new"}, QuotaBytes: 100}
	if m := verifyAccountOutcome(edit, &AccountPortrait{Meta: map[string]string{"Keep": "new"}, QuotaBytes: 100}); m != "" {
		t.Errorf("match reported a mismatch: %q", m)
	}
	for name, after := range map[string]*AccountPortrait{
		"missing":    {Meta: map[string]string{}, QuotaBytes: 100},
		"changed":    {Meta: map[string]string{"Keep": "stale"}, QuotaBytes: 100},
		"unexpected": {Meta: map[string]string{"Keep": "new", "Extra": "x"}, QuotaBytes: 100},
		"quota":      {Meta: map[string]string{"Keep": "new"}, QuotaBytes: 999},
	} {
		if verifyAccountOutcome(edit, after) == "" {
			t.Errorf("%s: expected a reported mismatch", name)
		}
	}
}

func TestUpdateAccountAppliesDelta(t *testing.T) {
	s, conn := newWritableSession(t)
	ctx := context.Background()

	// Seed account state including a secret the edit surface must never touch.
	if err := conn.AccountUpdate(ctx, swift.Headers{
		"X-Account-Meta-Keep":         "old",
		"X-Account-Meta-Drop":         "gone-soon",
		"X-Account-Meta-Temp-URL-Key": "sekrit",
		"X-Account-Meta-Quota-Bytes":  "500",
	}); err != nil {
		t.Fatal(err)
	}

	before, err := s.AccountPortrait(ctx)
	if err != nil {
		t.Fatal(err)
	}

	result, err := s.UpdateAccount(ctx, before.EditableFingerprint, AccountEdit{
		Meta:       map[string]string{"Keep": "new", "Added": "yes"},
		QuotaBytes: 1000,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Outcome != UpdateApplied {
		t.Fatalf("Outcome = %q (%s)", result.Outcome, result.Mismatch)
	}
	after := result.After
	if after.Meta["Keep"] != "new" || after.Meta["Added"] != "yes" {
		t.Errorf("Meta = %v", after.Meta)
	}
	if _, still := after.Meta["Drop"]; still {
		t.Error("removed key still present")
	}
	if after.QuotaBytes != 1000 {
		t.Errorf("QuotaBytes = %d", after.QuotaBytes)
	}
	// The secret survived the edit untouched.
	if len(after.TempURLKeySlots) != 1 {
		t.Errorf("TempURL slots = %v, must be untouched", after.TempURLKeySlots)
	}
	if len(result.TransIDs) != 3 {
		t.Errorf("TransIDs = %v (want preflight, write, verify)", result.TransIDs)
	}

	// A second edit clears the quota; the secret still survives.
	result2, err := s.UpdateAccount(ctx, after.EditableFingerprint, AccountEdit{
		Meta: map[string]string{"Keep": "new", "Added": "yes"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result2.Outcome != UpdateApplied || result2.After.QuotaBytes != 0 {
		t.Fatalf("Outcome = %q quota = %d, want applied with cleared quota", result2.Outcome, result2.After.QuotaBytes)
	}
	if len(result2.After.TempURLKeySlots) != 1 {
		t.Error("TempURL slot was destroyed by the second edit")
	}
}

func TestUpdateAccountConflict(t *testing.T) {
	s, conn := newWritableSession(t)
	ctx := context.Background()

	before, err := s.AccountPortrait(ctx)
	if err != nil {
		t.Fatal(err)
	}
	// An external writer changes the account after the working copy was read.
	if err := conn.AccountUpdate(ctx, swift.Headers{"X-Account-Meta-Interloper": "yes"}); err != nil {
		t.Fatal(err)
	}
	result, err := s.UpdateAccount(ctx, before.EditableFingerprint, AccountEdit{
		Meta: map[string]string{"Added": "yes"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Outcome != UpdateConflict || result.After != nil {
		t.Fatalf("result = %+v, want conflict without write", result)
	}
	current, err := s.AccountPortrait(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if current.Meta["Interloper"] != "yes" {
		t.Error("conflicted update clobbered the interloper's change")
	}
	if _, wrote := current.Meta["Added"]; wrote {
		t.Error("conflicted update wrote its own change anyway")
	}
}

func TestUpdateAccountRejectsReservedKeys(t *testing.T) {
	mock := swifttest.New("test:tester", "testing")
	srv := httptest.NewServer(mock)
	t.Cleanup(srv.Close)
	s, err := New(Config{AuthURL: srv.URL + "/auth/v1.0", Username: "test:tester", APIKey: "testing"})
	if err != nil {
		t.Fatal(err)
	}
	conn := &swift.Connection{AuthUrl: srv.URL + "/auth/v1.0", UserName: "test:tester", ApiKey: "testing", AuthVersion: 1}
	ctx := context.Background()

	// Seed a secret TempURL key and a quota through the real POST path.
	if err := conn.AccountUpdate(ctx, swift.Headers{
		"X-Account-Meta-Temp-URL-Key": "sekrit",
		"X-Account-Meta-Quota-Bytes":  "500",
	}); err != nil {
		t.Fatal(err)
	}

	// A reserved key in the metadata map is refused before any I/O, so it can
	// neither overwrite the secret nor change the quota behind the typed field.
	for _, key := range []string{"Temp-URL-Key", "temp-url-key", "Temp-URL-Key-2", "Quota-Bytes", "quota-bytes"} {
		before := mock.Requests()
		if _, err := s.UpdateAccount(ctx, "", AccountEdit{Meta: map[string]string{key: "intruder"}}); err == nil {
			t.Errorf("reserved key %q was admitted", key)
		}
		if got := mock.Requests(); got != before {
			t.Errorf("reserved key %q issued %d storage request(s) before refusal", key, got-before)
		}
	}

	// A normal key still round-trips, so the guard refuses rather than blocks.
	base, err := s.AccountPortrait(ctx)
	if err != nil {
		t.Fatal(err)
	}
	result, err := s.UpdateAccount(ctx, base.EditableFingerprint, AccountEdit{
		Meta: map[string]string{"Project": "atlas"}, QuotaBytes: 500,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Outcome != UpdateApplied || result.After.Meta["Project"] != "atlas" {
		t.Fatalf("normal edit outcome = %q meta = %v", result.Outcome, result.After.Meta)
	}
	if len(result.After.TempURLKeySlots) != 1 {
		t.Error("the secret slot did not survive the normal edit")
	}
}

// accountReadWire fails a chosen account read with a transaction id, so a
// failed preflight or verify read can be shown to carry its id (DESIGN §49).
type accountReadWire struct {
	reads, posts atomic.Int32
	failRead     func(n int32) bool
}

func newAccountReadSession(t *testing.T, wire *accountReadWire) *Session {
	t.Helper()
	auth := &fakeAuth{user: "acct:user", key: "sekrit", storagePath: "/v1/AUTH_acct"}
	mux := http.NewServeMux()
	mux.HandleFunc("/auth/v1.0", auth.handle)
	mux.HandleFunc("/v1/AUTH_acct", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			wire.posts.Add(1)
			w.Header().Set("X-Trans-Id", "tx-write")
			w.WriteHeader(http.StatusNoContent)
			return
		}
		n := wire.reads.Add(1)
		h := w.Header()
		h.Set("X-Account-Container-Count", "0")
		h.Set("X-Account-Object-Count", "0")
		h.Set("X-Account-Bytes-Used", "0")
		h.Set("X-Trans-Id", fmt.Sprintf("tx-read-%d", n))
		if wire.failRead != nil && wire.failRead(n) {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		if r.Method == http.MethodHead {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		h.Set("Content-Type", "application/json")
		_, _ = w.Write([]byte("[]"))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	auth.serverURL = srv.URL
	s, err := New(Config{AuthURL: srv.URL + "/auth/v1.0", Username: "acct:user", APIKey: "sekrit"})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestUpdateAccountReadErrorCarriesTransID(t *testing.T) {
	ctx := context.Background()

	// A verify read failing after a good POST still reports preflight, write
	// and verify ids, and the write is reported unverified.
	verify := &accountReadWire{failRead: func(n int32) bool { return n == 2 }}
	s := newAccountReadSession(t, verify)
	result, err := s.UpdateAccount(ctx, "", AccountEdit{Meta: map[string]string{"Project": "atlas"}})
	if !errors.Is(err, ErrUnverifiedAccountWrite) {
		t.Fatalf("err = %v, want ErrUnverifiedAccountWrite", err)
	}
	if result.Outcome != UpdateUnverified {
		t.Errorf("Outcome = %q", result.Outcome)
	}
	assertTransIDs(t, result.TransIDs, "tx-read-1", "tx-write", "tx-read-2")
	if verify.posts.Load() != 1 {
		t.Errorf("posts = %d, want 1", verify.posts.Load())
	}

	// A preflight read failing issues no POST but still reports its id.
	preflight := &accountReadWire{failRead: func(n int32) bool { return n == 1 }}
	s2 := newAccountReadSession(t, preflight)
	result2, err := s2.UpdateAccount(ctx, "", AccountEdit{Meta: map[string]string{"Project": "atlas"}})
	if err == nil {
		t.Fatal("preflight failure was not reported")
	}
	assertTransIDs(t, result2.TransIDs, "tx-read-1")
	if preflight.posts.Load() != 0 {
		t.Errorf("posts = %d, want 0 (no write after a failed preflight)", preflight.posts.Load())
	}
}

func assertTransIDs(t *testing.T, got []string, want ...string) {
	t.Helper()
	have := map[string]bool{}
	for _, g := range got {
		have[g] = true
	}
	for _, w := range want {
		if !have[w] {
			t.Errorf("TransIDs %v missing %q", got, w)
		}
	}
}
