package oracle

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestSnapshotFailsClosedOnTruncatedListing: a listing body that is
// shorter than its declared Content-Length produces a read error, and
// the oracle must fail closed rather than accept a truncated (or empty)
// view — a silently short listing would let accidental deletion of the
// unseen tail pass (codex war-room F-02).
func TestSnapshotFailsClosedOnTruncatedListing(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/c" {
			// Valid, COMPLETE-looking JSON (empty array) but the declared
			// length promises far more, so the read errors after it. Without
			// the read-error check this parses as "empty container" and the
			// snapshot succeeds — exactly the silent-tail-loss Codex found.
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Content-Length", "4096")
			w.Write([]byte(`[]`))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()
	o := &Oracle{client: srv.Client(), storageURL: srv.URL, token: "t"}
	if _, err := o.SnapshotContainers("c"); err == nil {
		t.Fatal("snapshot accepted a truncated listing body instead of failing closed")
	}
}

// TestSnapshotFailsClosedOnOutOfOrderPage: names that are not strictly
// increasing (a duplicate or a reversal) are a protocol violation the
// oracle must reject, not silently accept — otherwise a provider could
// hide or double-count objects (codex war-room F-02).
func TestSnapshotFailsClosedOnOutOfOrderPage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/c" {
			_ = json.NewEncoder(w).Encode([]map[string]string{{"name": "b"}, {"name": "a"}})
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()
	o := &Oracle{client: srv.Client(), storageURL: srv.URL, token: "t"}
	if _, err := o.SnapshotContainers("c"); err == nil {
		t.Fatal("snapshot accepted an out-of-order listing page instead of failing closed")
	}
}

// TestSnapshotFailsClosedOnMalformedSLOEntry: an SLO manifest entry whose
// name has no container separator cannot be witnessed; the oracle must
// refuse the whole snapshot rather than silently drop the segment and
// report a smaller inventory (codex war-room F-02).
func TestSnapshotFailsClosedOnMalformedSLOEntry(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		switch {
		case r.URL.Path == "/c":
			_ = json.NewEncoder(w).Encode([]map[string]string{{"name": "big.slo"}})
		case r.URL.Path == "/c/big.slo" && q.Get("multipart-manifest") == "get":
			w.Write([]byte(`[{"name":"no-container-separator"}]`))
		case r.URL.Path == "/c/big.slo" && q.Get("symlink") == "get":
			w.WriteHeader(http.StatusOK) // not a symlink
		case r.URL.Path == "/c/big.slo":
			w.Header().Set("X-Static-Large-Object", "true")
			w.Write([]byte("logical bytes"))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()
	o := &Oracle{client: srv.Client(), storageURL: srv.URL, token: "t"}
	if _, err := o.SnapshotContainers("c"); err == nil {
		t.Fatal("snapshot silently omitted a malformed SLO entry instead of failing closed")
	}
}
