package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// importFixture writes an rclone.conf naming accounts on authURL and
// returns its path. good signs in; wrong has a key the mock refuses; nokey
// has none; skipped takes its credentials from the environment.
func importFixture(t *testing.T, authURL string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "rclone.conf")
	conf := `
[good]
type = swift
user = test:tester
key = testing
auth = ` + authURL + `

[wrong]
type = swift
user = test:tester
key = not-the-key-41
auth = ` + authURL + `

[nokey]
type = swift
user = test:tester
auth = ` + authURL + `

[skipped]
type = swift
env_auth = true

[fresh]
type = swift
user = someone:else
key = testing
auth = ` + authURL + `
`
	if err := os.WriteFile(path, []byte(conf), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func newImportService(h *harness, file string) *BookmarkImportService {
	s := NewBookmarkImportService(h.bookmarks, h.bookmarks.store)
	WireImportFilePicker(s, restorePicker{file: file})
	return s
}

// waitForImport follows a save's verification until nothing is pending.
func waitForImport(t *testing.T, s *BookmarkImportService, token string) ImportResultDTO {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for {
		result, err := s.ImportStatus(token)
		if err != nil {
			t.Fatal(err)
		}
		if result.Done {
			return result
		}
		if time.Now().After(deadline) {
			t.Fatalf("verification still pending: %+v", result.Outcomes)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// A preview lists every account with no secret, marks the one an existing
// bookmark already has and the one that cannot be reproduced; a save
// stores each secret only in the secret store, saves a keyless account
// without a credential, and spends its token; verification flags the
// account whose key is refused and clears nothing it should not
// (ADR 0114).
func TestImportPreviewSaveAndVerify(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	if _, err := h.bookmarks.Save(ctx, BookmarkDTO{DisplayName: "Already", AuthURL: h.authURL, Username: "test:tester"}, "testing"); err != nil {
		t.Fatal(err)
	}
	s := newImportService(h, importFixture(t, h.authURL))

	preview, err := s.PreviewImport(ctx)
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(preview)
	for _, secret := range []string{"testing", "not-the-key-41"} {
		if bytes.Contains(encoded, []byte(secret)) {
			t.Fatalf("the preview carries a secret: %s", encoded)
		}
	}
	states := map[string]ImportRowDTO{}
	for _, row := range preview.Rows {
		states[row.Name] = row
	}
	for name, want := range map[string]string{"good": "duplicate", "wrong": "duplicate", "nokey": "duplicate", "skipped": "skipped", "fresh": "importable"} {
		if states[name].State != want {
			t.Errorf("%s state = %q, want %q", name, states[name].State, want)
		}
	}
	if !states["good"].HasSecret || states["nokey"].HasSecret {
		t.Errorf("HasSecret good=%t nokey=%t, want true and false", states["good"].HasSecret, states["nokey"].HasSecret)
	}
	if _, err := s.SaveImport(ctx, preview.Token, []int{states["skipped"].Index}, false); err == nil {
		t.Fatal("a skipped row was saved")
	}

	// The refused save spent nothing, so the same token saves.
	result, err := s.SaveImport(ctx, preview.Token, []int{states["good"].Index, states["wrong"].Index, states["nokey"].Index}, true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.SaveImport(ctx, preview.Token, []int{states["good"].Index}, true); !errors.Is(err, errImportTokenSpent) {
		t.Fatalf("a second save with the token = %v, want it refused", err)
	}
	result = waitForImport(t, s, result.Token)
	outcomes := map[string]ImportOutcomeDTO{}
	for _, o := range result.Outcomes {
		outcomes[o.Name] = o
	}
	if outcomes["good"].Verify != "verified" || outcomes["nokey"].Verify != "no_secret" {
		t.Fatalf("outcomes = %+v", result.Outcomes)
	}
	const refused = "the identity service refused these credentials"
	if outcomes["wrong"].Verify != "failed" || outcomes["wrong"].Reason != refused {
		t.Fatalf("wrong key outcome = %+v, want failed: %s", outcomes["wrong"], refused)
	}

	list, err := h.bookmarks.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	byID := map[string]BookmarkDTO{}
	for _, b := range list {
		byID[b.ID] = b
	}
	if got := byID[outcomes["wrong"].BookmarkID]; got.VerifyFailure != refused || !got.HasCredential {
		t.Errorf("the refused bookmark = %+v, want it saved with its key and flagged", got)
	}
	if got := byID[outcomes["good"].BookmarkID]; got.VerifyFailure != "" || got.DisplayName != "good" {
		t.Errorf("the verified bookmark = %+v, want it unflagged and named for its remote", got)
	}
	if got := byID[outcomes["nokey"].BookmarkID]; got.HasCredential {
		t.Error("the keyless account was saved with a credential")
	}
	if key, err := h.bookmarks.store.Credential(ctx, outcomes["good"].BookmarkID); err != nil || string(key) != "testing" {
		t.Errorf("the imported key is not in the secret store: %v", err)
	}
	for _, file := range []string{h.dbPath, h.dbPath + "-wal"} {
		raw, err := os.ReadFile(file)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Contains(raw, []byte("not-the-key-41")) {
			t.Fatalf("%s holds an imported key (K-SEC-003)", file)
		}
	}
}

// Imported bookmarks are verified at most two at a time (ADR 0114
// decision 8).
func TestImportVerifiesTwoAtATime(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	var inFlight, most atomic.Int32
	gauge := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/auth/v1.0" {
			n := inFlight.Add(1)
			defer inFlight.Add(-1)
			for {
				seen := most.Load()
				if n <= seen || most.CompareAndSwap(seen, n) {
					break
				}
			}
			time.Sleep(50 * time.Millisecond)
		}
		h.mock.ServeHTTP(w, r)
	}))
	t.Cleanup(gauge.Close)
	var conf strings.Builder
	for _, name := range []string{"a", "b", "c", "d", "e"} {
		conf.WriteString("[" + name + "]\ntype = swift\nuser = test:tester\nkey = testing\nauth = " + gauge.URL + "/auth/v1.0\n")
	}
	path := filepath.Join(t.TempDir(), "rclone.conf")
	if err := os.WriteFile(path, []byte(conf.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	s := newImportService(h, path)
	preview, err := s.PreviewImport(ctx)
	if err != nil {
		t.Fatal(err)
	}
	all := make([]int, len(preview.Rows))
	for i := range all {
		all[i] = i
	}
	result, err := s.SaveImport(ctx, preview.Token, all, true)
	if err != nil {
		t.Fatal(err)
	}
	result = waitForImport(t, s, result.Token)
	for _, o := range result.Outcomes {
		if o.Verify != "verified" {
			t.Fatalf("outcome %+v, want every account verified", o)
		}
	}
	// The ADR's number, not the constant, so a changed constant fails here.
	if got := most.Load(); got != 2 {
		t.Fatalf("at most %d sign-ins ran at once, want 2", got)
	}
}
