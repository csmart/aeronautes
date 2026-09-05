package swiftcontrol

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestDirectLibraryCallsRefreshStorageURL(t *testing.T) {
	for _, operation := range []string{"metadata post", "SLO manifest"} {
		t.Run(operation, func(t *testing.T) {
			var generation atomic.Int64
			var requests atomic.Int64
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if strings.HasPrefix(r.URL.Path, "/auth/") {
					n := generation.Add(1)
					w.Header().Set("X-Storage-Url", fmt.Sprintf("http://%s/v1/AUTH_%d", r.Host, n))
					w.Header().Set("X-Auth-Token", fmt.Sprint(n))
					return
				}
				if requests.Add(1) == 1 {
					w.WriteHeader(http.StatusUnauthorized)
					return
				}
				if r.URL.Path != "/v1/AUTH_"+r.Header.Get("X-Auth-Token")+"/owned/object" {
					t.Error("library retry did not refresh its storage URL")
					w.WriteHeader(http.StatusForbidden)
					return
				}
				if operation == "metadata post" {
					if r.Method != http.MethodPost {
						t.Errorf("metadata method: %s", r.Method)
					}
					w.WriteHeader(http.StatusNoContent)
				} else {
					if r.Method != http.MethodGet || r.URL.Query().Get("multipart-manifest") != "get" {
						t.Errorf("manifest request: %s %s", r.Method, r.URL)
					}
					fmt.Fprint(w, "[]")
				}
			}))
			defer srv.Close()
			session := mustSession(t, srv.URL)
			var err error
			if operation == "metadata post" {
				_, err = session.post(context.Background(), "owned", "object", nil)
			} else {
				_, err = session.SLOManifest(context.Background(), "owned", "object")
			}
			if err != nil {
				t.Fatal(err)
			}
			if generation.Load() != 2 || requests.Load() != 2 {
				t.Fatal("library request did not make exactly one authenticated retry")
			}
		})
	}
}
