package swiftcontrol

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

const infoJSON = `{
	"swift": {
		"max_file_size": 5368709122,
		"policies": [
			{"name": "Policy-0", "default": true, "aliases": "gold"},
			{"name": "ec42", "aliases": "erasure"}
		]
	},
	"bulk_delete": {"max_deletes_per_request": 10000},
	"slo": {"min_segment_size": 1048576},
	"tempurl": {"methods": ["GET", "HEAD", "PUT"]}
}`

// newAuthedSession spins up a fake v1 cluster whose /info handler is the
// one given (nil for no /info route at all) and returns an authenticated
// session against it.
func newAuthedSession(t *testing.T, info http.HandlerFunc) *Session {
	t.Helper()
	auth := &fakeAuth{user: "acct:user", key: "sekrit", storagePath: "/v1/AUTH_acct"}
	mux := http.NewServeMux()
	mux.HandleFunc("/auth/v1.0", auth.handle)
	if info != nil {
		mux.HandleFunc("/info", info)
	}
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	auth.serverURL = srv.URL

	s, err := New(Config{AuthURL: srv.URL + "/auth/v1.0", Username: "acct:user", APIKey: "sekrit"})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Authenticate(context.Background()); err != nil {
		t.Fatal(err)
	}
	return s
}

func TestFetchCapabilitiesAdvertised(t *testing.T) {
	s := newAuthedSession(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(infoJSON))
	})

	caps, err := s.FetchCapabilities(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if caps.Source != SourceAdvertised {
		t.Fatalf("Source = %s, want advertised", caps.Source)
	}
	if caps.Unavailable != nil {
		t.Fatalf("Unavailable = %v, want nil", caps.Unavailable)
	}

	for feature, want := range map[string]CapabilityState{
		"slo":         CapabilitySupported,
		"bulk_delete": CapabilitySupported,
		"tempurl":     CapabilitySupported,
		"staticweb":   CapabilityUnsupported,
	} {
		if got := caps.State(feature); got != want {
			t.Errorf("State(%q) = %s, want %s", feature, got, want)
		}
	}

	if got := caps.MaxFileSize(); got != 5368709122 {
		t.Errorf("MaxFileSize = %d, want 5368709122", got)
	}
	if got := caps.BulkDeleteMaxDeletes(); got != 10000 {
		t.Errorf("BulkDeleteMaxDeletes = %d, want 10000", got)
	}

	policies := caps.StoragePolicies()
	if len(policies) != 2 {
		t.Fatalf("StoragePolicies = %v, want 2 entries", policies)
	}
	if policies[0].Name != "Policy-0" || !policies[0].Default || policies[0].Aliases != "gold" {
		t.Errorf("policy 0 = %+v, want Policy-0/default/gold", policies[0])
	}
	if policies[1].Name != "ec42" || policies[1].Default {
		t.Errorf("policy 1 = %+v, want ec42/non-default", policies[1])
	}
}

// TestFetchCapabilitiesDisabled: /info returning 404/403 (disabled or
// filtered) yields Unknown-capabilities mode, not an error (§10).
func TestFetchCapabilitiesDisabled(t *testing.T) {
	s := newAuthedSession(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})

	caps, err := s.FetchCapabilities(context.Background())
	if err != nil {
		t.Fatalf("disabled /info must not be an error, got %v", err)
	}
	if caps.Source != SourceUnknown {
		t.Fatalf("Source = %s, want unknown", caps.Source)
	}
	if caps.Unavailable == nil {
		t.Fatal("Unavailable should carry the cause")
	}
	if got := caps.State("slo"); got != CapabilityUnknown {
		t.Fatalf("State(slo) = %s, want unknown — unknown is not unsupported", got)
	}
	if caps.MaxFileSize() != 0 || caps.BulkDeleteMaxDeletes() != 0 || caps.StoragePolicies() != nil {
		t.Fatal("unknown mode must report zero values, not invented ones")
	}
}

// TestFetchCapabilitiesUnreachable: a storage endpoint whose /info cannot
// even be reached also yields Unknown mode.
func TestFetchCapabilitiesUnreachable(t *testing.T) {
	auth := &fakeAuth{user: "acct:user", key: "sekrit", storagePath: "/v1/AUTH_acct"}
	mux := http.NewServeMux()
	mux.HandleFunc("/auth/v1.0", auth.handle)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	auth.serverURL = srv.URL

	// A server that is already gone: connection refused for storage + info.
	dead := httptest.NewServer(http.NotFoundHandler())
	deadURL := dead.URL
	dead.Close()

	s, err := New(Config{
		AuthURL:            srv.URL + "/auth/v1.0",
		Username:           "acct:user",
		APIKey:             "sekrit",
		StorageURLOverride: deadURL + "/v1/AUTH_acct",
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := s.Authenticate(ctx); err != nil {
		t.Fatal(err)
	}

	caps, err := s.FetchCapabilities(ctx)
	if err != nil {
		t.Fatalf("unreachable /info must not be an error, got %v", err)
	}
	if caps.Source != SourceUnknown || caps.Unavailable == nil {
		t.Fatalf("got source=%s unavailable=%v, want unknown mode with a cause", caps.Source, caps.Unavailable)
	}
}

func TestFetchCapabilitiesCancelled(t *testing.T) {
	s := newAuthedSession(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(infoJSON))
	})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := s.FetchCapabilities(ctx); err == nil {
		t.Fatal("cancellation must surface as an error, not Unknown mode")
	}
}
