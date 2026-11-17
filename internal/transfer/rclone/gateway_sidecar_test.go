package rclone

import (
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
)

func TestGatewayBindsSidecarPolicyToFreshDestinationHead(t *testing.T) {
	for _, test := range []struct {
		name       string
		observed   []string
		requested  []string
		headStatus int
		extraACL   bool
		wantStatus int
		wantHeads  int64
	}{
		{"same policy", []string{"tier-a"}, []string{"tier-a"}, 204, false, 204, 1},
		{"no policy", nil, nil, 204, false, 204, 1},
		{"wrong policy", []string{"tier-a"}, []string{"tier-b"}, 204, false, 403, 1},
		{"case differs", []string{"Tier-A"}, []string{"tier-a"}, 204, false, 403, 1},
		{"expected policy omitted", []string{"tier-a"}, nil, 204, false, 403, 1},
		{"unexpected policy", nil, []string{"tier-a"}, 204, false, 403, 1},
		{"ambiguous evidence", []string{"tier-a", "tier-a"}, []string{"tier-a"}, 204, false, 403, 1},
		{"empty evidence", []string{""}, nil, 204, false, 403, 1},
		{"missing destination", nil, nil, 404, false, 403, 1},
		{"failed head", []string{"tier-a"}, []string{"tier-a"}, 503, false, 403, 1},
		{"redirected head", []string{"tier-a"}, []string{"tier-a"}, 302, false, 403, 1},
		{"duplicate requested policy", []string{"tier-a"}, []string{"tier-a", "tier-a"}, 204, false, 403, 0},
		{"empty requested policy", nil, []string{""}, 204, false, 403, 0},
		{"policy with ACL", []string{"tier-a"}, []string{"tier-a"}, 204, true, 403, 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			var heads, puts atomic.Int64
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				if req.Header.Get("X-Auth-Token") != "parent-token" {
					t.Error("policy check or creation did not use the parent credential")
				}
				if req.Method == "HEAD" && req.URL.Path == gatewayAccount+"/owned" && req.URL.RawQuery == "" {
					heads.Add(1)
					for _, policy := range test.observed {
						w.Header().Add("X-Storage-Policy", policy)
					}
					w.Header().Set("Location", "http://unrelated.invalid/")
					w.WriteHeader(test.headStatus)
					return
				}
				if req.Method == "PUT" && req.URL.Path == gatewayAccount+"/owned_segments" {
					if !slices.Equal(req.Header.Values("X-Storage-Policy"), test.requested) || req.ContentLength != 0 {
						t.Error("creation changed the validated policy or empty body")
					}
					puts.Add(1)
					w.WriteHeader(http.StatusNoContent)
					return
				}
				t.Errorf("unexpected policy request: %s %s", req.Method, req.URL)
				w.WriteHeader(http.StatusForbidden)
			}))
			defer upstream.Close()
			gateway, remote, err := newStorageGateway(t.Context(), Remote{Name: "aeronautes_dst", StorageURL: upstream.URL + gatewayAccount, Token: "parent-token"}, &gatewayScope{container: "owned", object: gatewayFolder}, 64)
			if err != nil {
				t.Fatal(err)
			}
			defer gateway.Close()
			f := gatewayFixture{root: strings.TrimSuffix(remote.StorageURL, gatewayAccount), remote: remote}
			headers := http.Header{}
			for _, policy := range test.requested {
				headers.Add("X-Storage-Policy", policy)
			}
			if test.extraACL {
				headers.Set("X-Container-Read", ".r:* ")
			}
			status, _ := f.request(t, "PUT", gatewayAccount+"/owned_segments", remote.Token, "", headers)
			wantPuts := int64(0)
			if test.wantStatus == 204 {
				wantPuts = 1
			}
			if status != test.wantStatus || heads.Load() != test.wantHeads || puts.Load() != wantPuts {
				t.Fatalf("status/heads/puts = %d/%d/%d, want %d/%d/%d", status, heads.Load(), puts.Load(), test.wantStatus, test.wantHeads, wantPuts)
			}
		})
	}
}

func TestGatewayIgnoresEarlierOrUnrelatedPolicyEvidence(t *testing.T) {
	var heads, puts atomic.Int64
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.Method == "HEAD" {
			policy := "current"
			if heads.Add(1) == 1 || req.URL.Path != gatewayAccount+"/owned" {
				policy = "old"
			}
			w.Header().Set("X-Storage-Policy", policy)
			w.WriteHeader(204)
			return
		}
		puts.Add(1)
		w.WriteHeader(204)
	}))
	defer upstream.Close()
	gateway, remote, err := newStorageGateway(t.Context(), Remote{Name: "aeronautes_dst", StorageURL: upstream.URL + gatewayAccount, Token: "parent-token"}, &gatewayScope{container: "owned", object: gatewayFolder}, 64)
	if err != nil {
		t.Fatal(err)
	}
	defer gateway.Close()
	f := gatewayFixture{root: strings.TrimSuffix(remote.StorageURL, gatewayAccount), remote: remote}
	f.request(t, "HEAD", gatewayAccount+"/owned", remote.Token, "", nil)
	f.request(t, "HEAD", gatewayAccount+"/unrelated", remote.Token, "", nil)
	status, _ := f.request(t, "PUT", gatewayAccount+"/owned_segments", remote.Token, "", http.Header{"X-Storage-Policy": []string{"old"}})
	if status != 403 || heads.Load() != 3 || puts.Load() != 0 {
		t.Fatal("sidecar creation trusted earlier or unrelated evidence")
	}
}
