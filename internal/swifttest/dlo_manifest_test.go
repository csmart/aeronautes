package swifttest_test

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/csmart/aeronautes/internal/swifttest"
)

func TestDLORawManifestPreservesStoredBody(t *testing.T) {
	mock := swifttest.New("test:tester", "testing")
	mock.AddContainer("media", "standard")
	mock.AddContainer("segments", "standard")
	mock.PutObject("segments", "shared/0", swifttest.Object{Data: []byte("assembled bytes"), Bytes: 15})
	mock.PutObject("media", "dlo", swifttest.Object{Data: []byte("raw"), Bytes: 3, Manifest: "segments/shared/", ContentType: "application/octet-stream"})
	auth := httptest.NewRequest(http.MethodGet, "/auth/v1.0", nil)
	auth.Header.Set("X-Auth-User", "test:tester")
	auth.Header.Set("X-Auth-Key", "testing")
	login := httptest.NewRecorder()
	mock.ServeHTTP(login, auth)
	if login.Code != http.StatusOK || login.Header().Get("X-Auth-Token") == "" {
		t.Fatal("fixture authentication failed")
	}
	for _, method := range []string{http.MethodGet, http.MethodHead} {
		for _, query := range []string{"", "?multipart-manifest=get"} {
			t.Run(method+query, func(t *testing.T) {
				request := httptest.NewRequest(method, "/v1/AUTH_test/media/dlo"+query, nil)
				request.Header.Set("X-Auth-Token", login.Header().Get("X-Auth-Token"))
				response := httptest.NewRecorder()
				mock.ServeHTTP(response, request)
				body := "assembled bytes"
				if query != "" {
					body = "raw"
				}
				length := len(body)
				if method == http.MethodHead {
					body = ""
				}
				if response.Code != http.StatusOK || response.Body.String() != body || response.Header().Get("Content-Length") != strconv.Itoa(length) || response.Header().Get("X-Object-Manifest") != "segments/shared/" {
					t.Fatalf("DLO fixture confused stored and assembled identities: status=%d body=%q headers=%v", response.Code, response.Body.String(), response.Header())
				}
			})
		}
	}
}
