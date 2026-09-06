package swiftcontrol

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/ncw/swift/v2"
)

func TestLibraryStorageURLAndTokenStayPaired(t *testing.T) {
	for _, change := range []string{"origin", "account", "unchanged endpoint"} {
		t.Run(change, func(t *testing.T) {
			var requests, mixed atomic.Int64
			handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				want := "second"
				if strings.Contains(r.URL.Path, "AUTH_old") {
					want = "first"
				}
				if r.Header.Get("X-Auth-Token") != want {
					mixed.Add(1)
					w.WriteHeader(http.StatusForbidden)
					return
				}
				w.WriteHeader(http.StatusNoContent)
			})
			oldServer := httptest.NewServer(handler)
			defer oldServer.Close()
			newServer := httptest.NewServer(handler)
			defer newServer.Close()
			firstURL := oldServer.URL + "/v1/AUTH_old"
			secondURL := newServer.URL + "/v1/AUTH_new"
			if change == "account" {
				secondURL = oldServer.URL + "/v1/AUTH_new"
			} else if change == "unchanged endpoint" {
				firstURL = oldServer.URL + "/v1/AUTH_shared"
				secondURL = firstURL
			}
			var authentications atomic.Int64
			authServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if authentications.Add(1) == 1 {
					w.Header().Set("X-Storage-Url", firstURL)
					w.Header().Set("X-Auth-Token", "first")
				} else {
					w.Header().Set("X-Storage-Url", secondURL)
					w.Header().Set("X-Auth-Token", "second")
				}
			}))
			defer authServer.Close()
			connection := &swift.Connection{
				AuthUrl: authServer.URL, UserName: "fixture", ApiKey: "fixture", AuthVersion: 1,
			}
			ctx := context.Background()
			if err := connection.Authenticate(ctx); err != nil {
				t.Fatal(err)
			}
			// This separates the same two reads as the library's storage
			// helper, making the permitted reauthentication gap deterministic.
			target, err := connection.GetStorageUrl(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if err := connection.Authenticate(ctx); err != nil {
				t.Fatal(err)
			}
			_, _, err = connection.Call(ctx, target, swift.RequestOpts{
				Container: "owned", ObjectName: "object", Operation: http.MethodHead, NoResponse: true,
				// The library calls this callback under its authentication lock.
				OnReAuth: func() (string, error) { return connection.StorageUrl, nil },
			})
			if mixed.Load() != 0 {
				t.Error("storage received a URL and token from different authentication responses")
			}
			if err != nil {
				t.Errorf("storage call after reauthentication: %v", err)
			}
			if requests.Load() != 1 || authentications.Load() != 2 {
				t.Errorf("storage/auth requests: %d/%d, want 1/2", requests.Load(), authentications.Load())
			}
		})
	}
}
