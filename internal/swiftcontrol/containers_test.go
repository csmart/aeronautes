package swiftcontrol

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
)

// fakeContainerList serves the account container listing with real
// limit/marker/prefix pagination, counting requests.
type fakeContainerList struct {
	mu         sync.Mutex
	names      []string
	requests   int
	failOnPage int // 1-based; 0 = never fail
}

func (f *fakeContainerList) handle(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	f.requests++
	n := f.requests
	f.mu.Unlock()

	if f.failOnPage != 0 && n == f.failOnPage {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 {
		limit = 10000
	}
	marker := r.URL.Query().Get("marker")
	prefix := r.URL.Query().Get("prefix")

	type row struct {
		Name  string `json:"name"`
		Count int64  `json:"count"`
		Bytes int64  `json:"bytes"`
	}
	out := make([]row, 0)
	for i, name := range f.names {
		if name <= marker || !strings.HasPrefix(name, prefix) {
			continue
		}
		if len(out) == limit {
			break
		}
		out = append(out, row{Name: name, Count: int64(i + 1), Bytes: int64((i + 1) * 100)})
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(out)
}

func newContainerSession(t *testing.T, list *fakeContainerList) *Session {
	t.Helper()
	auth := &fakeAuth{user: "acct:user", key: "sekrit", storagePath: "/v1/AUTH_acct"}
	mux := http.NewServeMux()
	mux.HandleFunc("/auth/v1.0", auth.handle)
	mux.HandleFunc("/v1/AUTH_acct", list.handle)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	auth.serverURL = srv.URL

	s, err := New(Config{AuthURL: srv.URL + "/auth/v1.0", Username: "acct:user", APIKey: "sekrit"})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestContainersPaginates(t *testing.T) {
	list := &fakeContainerList{names: []string{"analytics", "backups", "logs", "photos", "photos_segments"}}
	s := newContainerSession(t, list)

	var got []ContainerEntry
	for c, err := range s.Containers(context.Background(), ListOptions{PageSize: 2}) {
		if err != nil {
			t.Fatal(err)
		}
		got = append(got, c)
	}

	if len(got) != 5 {
		t.Fatalf("streamed %d containers, want 5", len(got))
	}
	for i, want := range []string{"analytics", "backups", "logs", "photos", "photos_segments"} {
		if got[i].Name != want {
			t.Errorf("entry %d = %q, want %q", i, got[i].Name, want)
		}
	}
	if got[0].Count != 1 || got[0].Bytes != 100 {
		t.Errorf("entry 0 = %+v, want count 1 / bytes 100", got[0])
	}
	if list.requests != 4 {
		t.Errorf("server saw %d listing requests, want 4 including the empty terminator", list.requests)
	}
}

// TestContainersEarlyBreakStopsFetching is the streaming contract (§15):
// a consumer that stops must not cause further page fetches.
func TestContainersEarlyBreakStopsFetching(t *testing.T) {
	names := make([]string, 10)
	for i := range names {
		names[i] = fmt.Sprintf("c%02d", i)
	}
	list := &fakeContainerList{names: names}
	s := newContainerSession(t, list)

	for c, err := range s.Containers(context.Background(), ListOptions{PageSize: 2}) {
		if err != nil {
			t.Fatal(err)
		}
		if c.Name == "c00" {
			break
		}
	}
	if list.requests != 1 {
		t.Fatalf("server saw %d requests after early break, want 1", list.requests)
	}
}

func TestContainersPrefix(t *testing.T) {
	list := &fakeContainerList{names: []string{"logs", "photos", "photos_segments"}}
	s := newContainerSession(t, list)

	var got []string
	for c, err := range s.Containers(context.Background(), ListOptions{Prefix: "photos"}) {
		if err != nil {
			t.Fatal(err)
		}
		got = append(got, c.Name)
	}
	if len(got) != 2 || got[0] != "photos" || got[1] != "photos_segments" {
		t.Fatalf("prefix listing = %v, want [photos photos_segments]", got)
	}
}

func TestContainersEmpty(t *testing.T) {
	s := newContainerSession(t, &fakeContainerList{})
	for _, err := range s.Containers(context.Background(), ListOptions{}) {
		if err != nil {
			t.Fatal(err)
		}
		t.Fatal("yielded an entry from an empty account")
	}
}

func TestContainersErrorMidStream(t *testing.T) {
	names := make([]string, 4)
	for i := range names {
		names[i] = fmt.Sprintf("c%02d", i)
	}
	list := &fakeContainerList{names: names, failOnPage: 2}
	s := newContainerSession(t, list)

	var streamed int
	var lastErr error
	for c, err := range s.Containers(context.Background(), ListOptions{PageSize: 2}) {
		if err != nil {
			lastErr = err
			continue
		}
		streamed++
		_ = c
	}
	if streamed != 2 {
		t.Fatalf("streamed %d entries before the failure, want 2", streamed)
	}
	var se *SwiftError
	if !errors.As(lastErr, &se) || se.Kind != KindServer {
		t.Fatalf("stream error = %v, want normalized server error", lastErr)
	}
}
