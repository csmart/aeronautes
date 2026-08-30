package oracle

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"
)

// TestSnapshotPagesAndHostileNames drives the oracle against a fake
// Swift that serves more objects than one page and whose names contain a
// tab, a newline, and a space. It proves (grok war-room #3/#4, no SAIO):
//   - marker pagination walks every page (a single GET would truncate);
//   - Escape round-trips control bytes so each object is witnessed.
func TestSnapshotPagesAndHostileNames(t *testing.T) {
	// Sorted byte order: "a", "nl\nhere", "tab\there", "z space".
	want := []string{"a", "nl\nhere", "tab\there", "z space"}
	const pageSize = 2

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Object read (plain GET or symlink probe): 200 + a body, no
		// large-object or symlink headers => witnessed as plain.
		if name := strings.TrimPrefix(r.URL.Path, "/c/"); name != r.URL.Path && name != "" {
			w.WriteHeader(http.StatusOK)
			w.Write([]byte("body-of-" + name))
			return
		}
		// Container listing with exclusive marker pagination.
		if r.URL.Path == "/c" {
			marker := r.URL.Query().Get("marker")
			sorted := append([]string(nil), want...)
			sort.Strings(sorted)
			var page []map[string]string
			for _, n := range sorted {
				if marker != "" && n <= marker {
					continue
				}
				page = append(page, map[string]string{"name": n})
				if len(page) == pageSize {
					break
				}
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(page)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	o := &Oracle{client: srv.Client(), storageURL: srv.URL, token: "t"}
	snap, err := o.SnapshotContainers("c")
	if err != nil {
		t.Fatal(err)
	}
	got := snap.Containers["c"].Objects
	if len(got) != len(want) {
		t.Fatalf("witnessed %d objects across pages, want %d: %v", len(got), len(want), keysOf(got))
	}
	for _, n := range want {
		os, ok := got[n]
		if !ok {
			t.Fatalf("object %q not witnessed — pagination or escaping dropped it", n)
		}
		if os.Class != "plain" || os.Digest == "" {
			t.Fatalf("object %q witnessed as %+v", n, os)
		}
	}
}

func keysOf(m map[string]ObjectState) []string {
	var k []string
	for n := range m {
		k = append(k, n)
	}
	return k
}
