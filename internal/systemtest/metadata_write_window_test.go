package systemtest

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/csmart/aeronautes/internal/app"
	"github.com/csmart/aeronautes/internal/swifttest"
)

// Maintained regressions for ADR 0046. Swift offers no conditional
// metadata write, so a save that lands in the interval between the
// preflight read and the replace-all POST cannot be prevented or
// detected. What the product owes the operator is an honest statement of
// that, on every completed save, whatever the outcome. These assert the
// REPORTED shape; they do not assert exclusivity, which the product
// cannot provide.

// metadataWindowProxy fronts the account's storage URL so a barrier can
// hold one selected response while the test writes concurrently DIRECTLY
// to the store, bypassing the proxy.
func metadataWindowProxy(t *testing.T, h *Harness, account *Account, barrier *swifttest.HTTPResponseBarrier) {
	t.Helper()
	storage, err := url.Parse(account.storageURL)
	if err != nil {
		t.Fatal(err)
	}
	target := *storage
	target.Path, target.RawPath, target.RawQuery = "", "", ""
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.DisableCompression = true
	transport.ResponseHeaderTimeout = 20 * time.Second
	t.Cleanup(transport.CloseIdleConnections)
	proxy := httputil.NewSingleHostReverseProxy(&target)
	proxy.Transport = transport
	proxy.ErrorHandler = func(w http.ResponseWriter, _ *http.Request, err error) {
		t.Errorf("metadata window proxy: %v", err)
		http.Error(w, "fixture proxy failed", http.StatusBadGateway)
	}
	front := httptest.NewServer(barrier.Wrap(proxy))
	t.Cleanup(front.Close)
	t.Cleanup(barrier.Close)

	list, err := h.Bookmarks.List(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for _, bookmark := range list {
		if bookmark.ID != account.BookmarkID {
			continue
		}
		bookmark.StorageURLOverride = front.URL + storage.EscapedPath()
		if _, err := h.Bookmarks.Save(t.Context(), bookmark, account.key); err != nil {
			t.Fatal(err)
		}
		if _, err := h.Browse.Connect(t.Context(), account.BookmarkID); err != nil {
			t.Fatal(err)
		}
		return
	}
	t.Fatalf("bookmark %s not found", account.BookmarkID)
}

// competingSave is a second editor's complete replace-all metadata POST,
// issued directly to the store as that editor's own save would be.
func competingSave(t *testing.T, a *Account, path string, headers map[string]string) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, a.storageURL+path, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("X-Auth-Token", a.token)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := a.rawClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		t.Fatalf("competing POST %s: HTTP %d", path, resp.StatusCode)
	}
}

// TestMetadataSaveStatesItsLimit covers the three ways a save ends. A
// save that WROTE says what it does not establish; a save refused before
// writing has nothing to qualify and must stay silent.
func TestMetadataSaveStatesItsLimit(t *testing.T) {
	const object = "target.txt"

	t.Run("an_undisturbed_save_still_states_its_limit", func(t *testing.T) {
		h := New(t)
		ctx := context.Background()
		c := h.Container(h.Primary, "meta-limit")
		h.Seed(h.Primary, c, object, []byte("payload"), map[string]string{
			"Content-Type": "text/plain", "X-Object-Meta-Alpha": "1",
		})
		detail, err := h.Inspector.Object(ctx, h.Primary.BookmarkID, c, object)
		if err != nil {
			t.Fatal(err)
		}
		result, err := h.Mutate.UpdateObjectMetadata(ctx, c, object, detail.EditableFingerprint,
			app.ObjectMetadataEditDTO{Meta: map[string]string{"Alpha": "1", "Beta": "2"}, ContentType: "text/plain"})
		if err != nil {
			t.Fatal(err)
		}
		// The qualification is unconditional: this path cannot tell an
		// undisturbed save from a concurrent one, so it must not claim to.
		if result.Outcome != "applied" {
			t.Fatalf("outcome = %s, want applied", result.Outcome)
		}
		if result.Limitation == "" {
			t.Error("a completed save reported no limitation, so it presents itself as exclusive")
		}
		if strings.Contains(strings.ToLower(result.Limitation), "conflict was detected") {
			t.Errorf("the limitation claims detection it does not have: %q", result.Limitation)
		}
	})

	t.Run("a_refused_save_states_no_limit", func(t *testing.T) {
		h := New(t)
		ctx := context.Background()
		c := h.Container(h.Primary, "meta-limit-refused")
		h.Seed(h.Primary, c, object, []byte("payload"), map[string]string{
			"Content-Type": "text/plain", "X-Object-Meta-Alpha": "1",
		})
		result, err := h.Mutate.UpdateObjectMetadata(ctx, c, object, "a-fingerprint-that-cannot-match",
			app.ObjectMetadataEditDTO{Meta: map[string]string{"Alpha": "9"}, ContentType: "text/plain"})
		if err != nil {
			t.Fatal(err)
		}
		if result.Outcome != "conflict" {
			t.Fatalf("outcome = %s, want conflict", result.Outcome)
		}
		// Nothing was written, so there is no completed save to qualify.
		// A limitation here would invent an effect that never happened.
		if result.Limitation != "" {
			t.Errorf("a refused save reported a limitation %q; nothing was written", result.Limitation)
		}
		final := h.Snapshot(h.Primary, c).Containers[c].Objects[object]
		if final.Meta["Alpha"] != "1" {
			t.Errorf("a refused save changed metadata: %+v", final.Meta)
		}
	})

	t.Run("a_save_that_overwrites_a_concurrent_edit_states_its_limit", func(t *testing.T) {
		h := New(t)
		ctx := context.Background()
		c := h.Container(h.Primary, "meta-limit-race")
		h.Seed(h.Primary, c, object, []byte("payload"), map[string]string{
			"Content-Type": "text/plain", "X-Object-Meta-Alpha": "1",
		})
		detail, err := h.Inspector.Object(ctx, h.Primary.BookmarkID, c, object)
		if err != nil {
			t.Fatal(err)
		}
		before := h.Snapshot(h.Primary, c).Containers[c].Objects[object]

		storage, err := url.Parse(h.Primary.storageURL)
		if err != nil {
			t.Fatal(err)
		}
		barrier, err := swifttest.NewHTTPResponseBarrier(swifttest.HTTPResponseSelector{
			Operation: t.Name(), Method: http.MethodHead,
			EscapedPath: storage.EscapedPath() + "/" + c + "/" + object, Occurrence: 1,
		}, false, 30*time.Second)
		if err != nil {
			t.Fatal(err)
		}
		metadataWindowProxy(t, h, h.Primary, barrier)

		type saved struct {
			result app.MutationResultDTO
			err    error
		}
		done := make(chan saved, 1)
		go func() {
			result, err := h.Mutate.UpdateObjectMetadata(ctx, c, object, detail.EditableFingerprint,
				app.ObjectMetadataEditDTO{Meta: map[string]string{"Alpha": "1", "Beta": "2"}, ContentType: "text/plain"})
			done <- saved{result, err}
		}()
		if _, err := barrier.Wait(ctx); err != nil {
			t.Fatal(err)
		}
		competingSave(t, h.Primary, "/"+c+"/"+object, map[string]string{
			"Content-Type": "text/plain", "X-Object-Meta-Alpha": "1", "X-Object-Meta-Gamma": "3",
		})
		if err := barrier.Release(); err != nil {
			t.Fatal(err)
		}
		got := <-done
		if got.err != nil {
			t.Fatalf("save errored: %v", got.err)
		}

		// The overwrite is NOT prevented and NOT detected — no conditional
		// write exists to prevent it, and the verify read asserts the
		// intended post-state, which an overwrite satisfies exactly. What
		// the product owes is the honest statement.
		if got.result.Limitation == "" {
			t.Error("a save that destroyed a concurrent edit reported no limitation")
		}
		final := h.Snapshot(h.Primary, c).Containers[c].Objects[object]
		if final.Meta["Gamma"] != "" {
			t.Skip("the competing save survived; this run did not exercise the window")
		}
		// Content must be untouched by a metadata save. Compare the digest
		// rather than assuming it: a POST that altered bytes would be a
		// different and much worse defect.
		if final.Digest != before.Digest || final.Size != before.Size {
			t.Errorf("a metadata save changed CONTENT: %s/%d -> %s/%d",
				before.Digest, before.Size, final.Digest, final.Size)
		}
	})
}

// TestMetadataSaveLimitReachesTheAuditTrail keeps the qualification with
// the record that outlives the editor. A completed save the operator can
// later read as a plain success is the defect this slice exists for.
func TestMetadataSaveLimitReachesTheAuditTrail(t *testing.T) {
	h := New(t)
	ctx := context.Background()
	c := h.Container(h.Primary, "meta-limit-audit")
	const object = "target.txt"
	h.Seed(h.Primary, c, object, []byte("payload"), map[string]string{
		"Content-Type": "text/plain", "X-Object-Meta-Alpha": "1",
	})
	detail, err := h.Inspector.Object(ctx, h.Primary.BookmarkID, c, object)
	if err != nil {
		t.Fatal(err)
	}
	result, err := h.Mutate.UpdateObjectMetadata(ctx, c, object, detail.EditableFingerprint,
		app.ObjectMetadataEditDTO{Meta: map[string]string{"Alpha": "2"}, ContentType: "text/plain"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Limitation == "" {
		t.Fatal("no limitation on the result, so the audit row cannot carry one")
	}
	events, err := h.Audit.RecentFor(ctx, h.Primary.BookmarkID, 20)
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range events {
		if event.Operation != "object.metadata.update" {
			continue
		}
		if !strings.Contains(event.Summary, result.Limitation) {
			t.Errorf("audit summary %q does not carry the limitation %q", event.Summary, result.Limitation)
		}
		return
	}
	t.Error("no object.metadata.update audit row was written")
}
