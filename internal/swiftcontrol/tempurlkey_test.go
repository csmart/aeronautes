package swiftcontrol

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/csmart/aeronautes/internal/swifttest"
	"github.com/ncw/swift/v2"
)

// newTempURLSession runs the writable mock and returns the session, the
// mock for its request count, and a raw connection that reads what the
// mock stored, which no portrait will show.
func newTempURLSession(t *testing.T) (*Session, *swifttest.Server, *swift.Connection) {
	t.Helper()
	mock := swifttest.New("test:tester", "testing")
	srv := httptest.NewServer(mock)
	t.Cleanup(srv.Close)
	s, err := New(Config{AuthURL: srv.URL + "/auth/v1.0", Username: "test:tester", APIKey: "testing"})
	if err != nil {
		t.Fatal(err)
	}
	conn := &swift.Connection{AuthUrl: srv.URL + "/auth/v1.0", UserName: "test:tester", ApiKey: "testing", AuthVersion: 1}
	if err := conn.ContainerCreate(context.Background(), "media", nil); err != nil {
		t.Fatal(err)
	}
	return s, mock, conn
}

// storedSlots reads the raw slot values the mock holds for the account
// (container "") or a container: the test's view behind the redaction.
func storedSlots(t *testing.T, conn *swift.Connection, container string) map[string]string {
	t.Helper()
	ctx := context.Background()
	var headers swift.Headers
	var err error
	if container == "" {
		_, headers, err = conn.Account(ctx)
	} else {
		_, headers, err = conn.Container(ctx, container)
	}
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for k, v := range canonicalHeaders(headers) {
		for _, prefix := range []string{accountMetaPrefix, containerMetaPrefix} {
			if name := metaSuffix(k, prefix); name != "" && isTempURLKey(name) {
				out[name] = v
			}
		}
	}
	return out
}

// listedSlots is the slot presence the portrait of the account or a
// container reports, which is what an inspector shows and a request carries.
func listedSlots(t *testing.T, s *Session, container string) []string {
	t.Helper()
	ctx := context.Background()
	if container == "" {
		portrait, err := s.AccountPortrait(ctx)
		if err != nil {
			t.Fatal(err)
		}
		return portrait.TempURLKeySlots
	}
	detail, err := s.ContainerDetail(ctx, container)
	if err != nil {
		t.Fatal(err)
	}
	return detail.TempURLKeySlots
}

// carries reports whether the complete returned result, portraits included,
// holds needle anywhere. The JSON form walks every exported field, where a
// %+v print stops at the portrait pointers and would miss a leaking header.
func carries(t *testing.T, result any, needle string) bool {
	t.Helper()
	encoded, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	return strings.Contains(string(encoded), needle)
}

func targetName(container string) string {
	if container == "" {
		return "account"
	}
	return "container"
}

// assertTempURLApplied checks a verified write: the outcome, the three
// transaction ids, and the target's portrait with the slots it now lists.
func assertTempURLApplied(t *testing.T, result *TempURLKeyResult, container string, wantSlots []string) {
	t.Helper()
	if result.Outcome != UpdateApplied {
		t.Fatalf("Outcome = %q (%s)", result.Outcome, result.Mismatch)
	}
	if len(result.TransIDs) != 3 || slices.Contains(result.TransIDs, "") {
		t.Errorf("TransIDs = %v, want preflight, write and verify", result.TransIDs)
	}
	var slots []string
	switch {
	case container == "" && result.AccountAfter != nil && result.ContainerAfter == nil:
		slots = result.AccountAfter.TempURLKeySlots
	case container != "" && result.ContainerAfter != nil && result.AccountAfter == nil:
		slots = result.ContainerAfter.TempURLKeySlots
	default:
		t.Fatalf("after-state for %s = account %v container %v", targetName(container), result.AccountAfter != nil, result.ContainerAfter != nil)
	}
	if !slices.Equal(slots, wantSlots) {
		t.Errorf("slots after = %v, want %v", slots, wantSlots)
	}
}

func TestUpdateTempURLKeySetsAndClearsSlots(t *testing.T) {
	for _, target := range []string{"", "media"} {
		t.Run(targetName(target), func(t *testing.T) {
			s, _, conn := newTempURLSession(t)
			ctx := context.Background()

			// The first slot, against the presence the portrait lists, which is
			// none and reported as nil.
			first, err := s.UpdateTempURLKey(ctx, target, listedSlots(t, s, target), TempURLKeyEdit{Slot: "Temp-URL-Key", Secret: "first-secret-9f3a"})
			if err != nil {
				t.Fatal(err)
			}
			assertTempURLApplied(t, first, target, []string{"Temp-Url-Key"})
			if got := storedSlots(t, conn, target); got["Temp-Url-Key"] != "first-secret-9f3a" {
				t.Fatalf("stored slots = %v, want the first secret in slot 1", got)
			}

			// Rotation: the second slot, against the presence the first left,
			// with the slot spelled in another case.
			second, err := s.UpdateTempURLKey(ctx, target, []string{"Temp-Url-Key"}, TempURLKeyEdit{Slot: "temp-url-key-2", Secret: "second-secret-7c1d"})
			if err != nil {
				t.Fatal(err)
			}
			assertTempURLApplied(t, second, target, []string{"Temp-Url-Key", "Temp-Url-Key-2"})
			if got := storedSlots(t, conn, target); got["Temp-Url-Key"] != "first-secret-9f3a" || got["Temp-Url-Key-2"] != "second-secret-7c1d" {
				t.Fatalf("stored slots = %v, want both secrets", got)
			}

			// Then the first slot is cleared, against the presence of both.
			cleared, err := s.UpdateTempURLKey(ctx, target, []string{"Temp-Url-Key-2", "Temp-Url-Key"}, TempURLKeyEdit{Slot: "Temp-URL-Key", Clear: true})
			if err != nil {
				t.Fatal(err)
			}
			assertTempURLApplied(t, cleared, target, []string{"Temp-Url-Key-2"})
			if got := storedSlots(t, conn, target); len(got) != 1 || got["Temp-Url-Key-2"] != "second-secret-7c1d" {
				t.Fatalf("stored slots = %v, want only the second secret", got)
			}

			// A set over a present slot is a replacement by intent.
			replaced, err := s.UpdateTempURLKey(ctx, target, []string{"Temp-Url-Key-2"}, TempURLKeyEdit{Slot: "Temp-URL-Key-2", Secret: "third-secret-2b8e"})
			if err != nil {
				t.Fatal(err)
			}
			assertTempURLApplied(t, replaced, target, []string{"Temp-Url-Key-2"})
			if got := storedSlots(t, conn, target); got["Temp-Url-Key-2"] != "third-secret-2b8e" {
				t.Fatalf("stored slots = %v, want the replacement", got)
			}

			// No result carries a secret anywhere, the returned portrait's
			// metadata and redacted raw view included.
			for _, result := range []*TempURLKeyResult{first, second, cleared, replaced} {
				if carries(t, result, "-secret-") {
					t.Error("a result carries a secret")
				}
			}
		})
	}
}

// Every refusal happens before any request and names no secret.
func TestUpdateTempURLKeyRefusesBeforeAnyRequest(t *testing.T) {
	s, mock, _ := newTempURLSession(t)
	ctx := context.Background()
	cases := []struct {
		name string
		edit TempURLKeyEdit
		leak string
	}{
		{"unknown slot", TempURLKeyEdit{Slot: "Temp-URL-Key-3", Secret: "zq9-unknown"}, "zq9-unknown"},
		{"empty secret", TempURLKeyEdit{Slot: "Temp-URL-Key"}, ""},
		{"clear with a secret", TempURLKeyEdit{Slot: "Temp-URL-Key", Secret: "zq9-cleared", Clear: true}, "zq9-cleared"},
		{"only spaces", TempURLKeyEdit{Slot: "Temp-URL-Key", Secret: "  "}, ""},
		{"leading space", TempURLKeyEdit{Slot: "Temp-URL-Key", Secret: " zq9-leading"}, "zq9-leading"},
		{"trailing tab", TempURLKeyEdit{Slot: "Temp-URL-Key", Secret: "zq9-trailing\t"}, "zq9-trailing"},
		{"control byte", TempURLKeyEdit{Slot: "Temp-URL-Key", Secret: "zq9-con\x01trol"}, "zq9-con"},
		{"inner tab", TempURLKeyEdit{Slot: "Temp-URL-Key", Secret: "zq9\ttab"}, "zq9"},
		{"over long", TempURLKeyEdit{Slot: "Temp-URL-Key-2", Secret: strings.Repeat("k", maxTempURLSecretBytes+1)}, "kkkkkkkk"},
	}
	for _, c := range cases {
		for _, target := range []string{"", "media"} {
			before := mock.Requests()
			result, err := s.UpdateTempURLKey(ctx, target, nil, c.edit)
			if err == nil {
				t.Errorf("%s on the %s was admitted", c.name, targetName(target))
				continue
			}
			if result != nil {
				t.Errorf("%s: a refusal returned a result", c.name)
			}
			if got := mock.Requests(); got != before {
				t.Errorf("%s: %d request(s) issued before refusal", c.name, got-before)
			}
			if c.leak != "" && strings.Contains(err.Error(), c.leak) {
				t.Errorf("%s: the refusal carries the secret", c.name)
			}
		}
	}
}

// A presence the operator did not see is a conflict with no POST, including
// the presence a portrait reports for no slots at all, which is nil; the
// same presence in another order or case is not a conflict.
func TestUpdateTempURLKeyConflictWritesNothing(t *testing.T) {
	s, mock, conn := newTempURLSession(t)
	ctx := context.Background()
	shown := listedSlots(t, s, "")
	if shown != nil {
		t.Fatalf("a fresh account lists %v, want no slots", shown)
	}
	if err := conn.AccountUpdate(ctx, swift.Headers{"X-Account-Meta-Temp-URL-Key": "interloper-secret"}); err != nil {
		t.Fatal(err)
	}

	before := mock.Requests()
	result, err := s.UpdateTempURLKey(ctx, "", shown, TempURLKeyEdit{Slot: "Temp-URL-Key", Secret: "mine-secret"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Outcome != UpdateConflict || result.AccountAfter != nil {
		t.Fatalf("result = %+v, want a conflict without a write", result)
	}
	if got := mock.Requests() - before; got != 1 {
		t.Errorf("requests = %d, want the preflight HEAD only", got)
	}
	if got := storedSlots(t, conn, ""); got["Temp-Url-Key"] != "interloper-secret" {
		t.Errorf("stored slots = %v, want the other writer's key untouched", got)
	}
	assertTransIDs(t, result.TransIDs, "txswifttest-account")

	rotated, err := s.UpdateTempURLKey(ctx, "", []string{"temp-url-key"}, TempURLKeyEdit{Slot: "Temp-URL-Key-2", Secret: "mine-secret"})
	if err != nil {
		t.Fatal(err)
	}
	assertTempURLApplied(t, rotated, "", []string{"Temp-Url-Key", "Temp-Url-Key-2"})
}

// tempURLWire serves the account and one container with a fixed slot value
// whatever is posted, so a verify read can disagree with the write, and can
// drop a POST's response to leave the write unproven.
type tempURLWire struct {
	slotValue string
	dropPosts bool
	posts     atomic.Int32
	reads     atomic.Int32
}

func newTempURLWireSession(t *testing.T, wire *tempURLWire) *Session {
	t.Helper()
	auth := &fakeAuth{user: "acct:user", key: "sekrit", storagePath: "/v1/AUTH_acct"}
	mux := http.NewServeMux()
	mux.HandleFunc("/auth/v1.0", auth.handle)
	serve := func(metaPrefix string) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodPost {
				wire.posts.Add(1)
				if wire.dropPosts {
					if conn, _, err := w.(http.Hijacker).Hijack(); err == nil {
						conn.Close()
					}
					return
				}
				w.Header().Set("X-Trans-Id", "tx-write")
				w.WriteHeader(http.StatusNoContent)
				return
			}
			n := wire.reads.Add(1)
			h := w.Header()
			if metaPrefix == accountMetaPrefix {
				h.Set("X-Account-Container-Count", "1")
				h.Set("X-Account-Object-Count", "0")
				h.Set("X-Account-Bytes-Used", "0")
			} else {
				h.Set("X-Container-Object-Count", "0")
				h.Set("X-Container-Bytes-Used", "0")
			}
			if wire.slotValue != "" {
				h.Set(metaPrefix+"Temp-Url-Key", wire.slotValue)
			}
			h.Set("X-Trans-Id", fmt.Sprintf("tx-read-%d", n))
			w.WriteHeader(http.StatusNoContent)
		}
	}
	mux.HandleFunc("/v1/AUTH_acct", serve(accountMetaPrefix))
	mux.HandleFunc("/v1/AUTH_acct/media", serve(containerMetaPrefix))
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	auth.serverURL = srv.URL
	s, err := New(Config{AuthURL: srv.URL + "/auth/v1.0", Username: "acct:user", APIKey: "sekrit"})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// A verify read that disagrees with the write is a mismatch that names the
// slot and the kind of difference, and neither value.
func TestUpdateTempURLKeyVerifyMismatchNamesNoValue(t *testing.T) {
	ctx := context.Background()
	// The wire always serves slot 1, so that presence is the base; the
	// disagreement is in the value, not the presence.
	wire := &tempURLWire{slotValue: "served-other-value"}
	s := newTempURLWireSession(t, wire)
	for _, target := range []string{"", "media"} {
		set, err := s.UpdateTempURLKey(ctx, target, []string{"Temp-Url-Key"}, TempURLKeyEdit{Slot: "Temp-URL-Key", Secret: "sent-value-55"})
		if err != nil {
			t.Fatal(err)
		}
		if set.Outcome != UpdateVerifyMismatch || !strings.Contains(set.Mismatch, "different value") {
			t.Errorf("%s set = %q (%s), want a different-value mismatch", targetName(target), set.Outcome, set.Mismatch)
		}
		cleared, err := s.UpdateTempURLKey(ctx, target, []string{"Temp-Url-Key"}, TempURLKeyEdit{Slot: "Temp-URL-Key", Clear: true})
		if err != nil {
			t.Fatal(err)
		}
		if cleared.Outcome != UpdateVerifyMismatch || !strings.Contains(cleared.Mismatch, "still present") {
			t.Errorf("%s clear = %q (%s), want a still-present mismatch", targetName(target), cleared.Outcome, cleared.Mismatch)
		}
		for _, result := range []*TempURLKeyResult{set, cleared} {
			if carries(t, result, "sent-value") || carries(t, result, "served-other-value") {
				t.Errorf("%s result carries a value: %s", targetName(target), result.Mismatch)
			}
		}
	}

	absent := newTempURLWireSession(t, &tempURLWire{})
	result, err := absent.UpdateTempURLKey(ctx, "", nil, TempURLKeyEdit{Slot: "Temp-URL-Key-2", Secret: "sent-value-66"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Outcome != UpdateVerifyMismatch || !strings.Contains(result.Mismatch, "absent") {
		t.Errorf("set the store did not keep = %q (%s), want an absent mismatch", result.Outcome, result.Mismatch)
	}
}

// A POST whose reply never arrives, and a verify read that fails after a
// good POST, are unverified writes that keep their transaction ids; a
// failed preflight issues no POST.
func TestUpdateTempURLKeyUnverifiedAndRefusedReads(t *testing.T) {
	ctx := context.Background()

	dropped := &tempURLWire{dropPosts: true}
	s := newTempURLWireSession(t, dropped)
	result, err := s.UpdateTempURLKey(ctx, "media", nil, TempURLKeyEdit{Slot: "Temp-URL-Key", Secret: "sent-value-77"})
	if !errors.Is(err, ErrUnverifiedTempURLKeyWrite) || result.Outcome != UpdateUnverified {
		t.Fatalf("dropped POST: err = %v, outcome = %q; want unverified", err, result.Outcome)
	}
	assertTransIDs(t, result.TransIDs, "tx-read-1")
	if strings.Contains(err.Error(), "sent-value") {
		t.Error("the unverified error carries the secret")
	}

	verify := &accountReadWire{failRead: func(n int32) bool { return n == 2 }}
	s2 := newAccountReadSession(t, verify)
	result2, err := s2.UpdateTempURLKey(ctx, "", nil, TempURLKeyEdit{Slot: "Temp-URL-Key", Secret: "sent-value-88"})
	if !errors.Is(err, ErrUnverifiedTempURLKeyWrite) || result2.Outcome != UpdateUnverified {
		t.Fatalf("failed verify: err = %v, outcome = %q; want unverified", err, result2.Outcome)
	}
	assertTransIDs(t, result2.TransIDs, "tx-read-1", "tx-write", "tx-read-2")
	if verify.posts.Load() != 1 {
		t.Errorf("posts = %d, want exactly one", verify.posts.Load())
	}

	preflight := &accountReadWire{failRead: func(n int32) bool { return n == 1 }}
	s3 := newAccountReadSession(t, preflight)
	result3, err := s3.UpdateTempURLKey(ctx, "", nil, TempURLKeyEdit{Slot: "Temp-URL-Key", Secret: "sent-value-99"})
	if err == nil || result3.Outcome != "" {
		t.Fatalf("failed preflight: err = %v, outcome = %q; want an error and no outcome", err, result3.Outcome)
	}
	assertTransIDs(t, result3.TransIDs, "tx-read-1")
	if preflight.posts.Load() != 0 {
		t.Errorf("posts = %d, want none after a failed preflight", preflight.posts.Load())
	}
}
