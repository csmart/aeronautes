package swifttest_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/csmart/aeronautes/internal/swifttest"
)

func TestLiteralContainerNames(t *testing.T) {
	for index, name := range []string{" leading", "trailing  ", "   ", "日本語", "%2F +%20"} {
		t.Run(fmt.Sprint(index), func(t *testing.T) {
			mock := swifttest.New("test:tester", "testing")
			token := literalMockToken(t, mock)
			call := func(method, target, body string, status int) *httptest.ResponseRecorder {
				t.Helper()
				r := httptest.NewRequest(method, target, strings.NewReader(body))
				r.Header.Set("X-Auth-Token", token)
				w := httptest.NewRecorder()
				mock.ServeHTTP(w, r)
				if w.Code != status {
					t.Fatalf("%s %q: HTTP %d, want %d: %s", method, target, w.Code, status, w.Body.String())
				}
				return w
			}
			containerPath := "/v1/AUTH_test/" + url.PathEscape(name)
			objectName := "a%2F// b"
			objectPath := containerPath + "/" + url.PathEscape(objectName)
			call(http.MethodPut, containerPath, "", http.StatusCreated)
			call(http.MethodPut, objectPath, "known body", http.StatusCreated)
			account := call(http.MethodGet, "/v1/AUTH_test?format=json", "", http.StatusOK)
			var containers []struct{ Name string }
			if err := json.Unmarshal(account.Body.Bytes(), &containers); err != nil {
				t.Fatal(err)
			}
			if len(containers) != 1 || containers[0].Name != name {
				t.Fatalf("container identity changed: got %+v, want %q", containers, name)
			}
			listing := call(http.MethodGet, containerPath+"?format=json", "", http.StatusOK)
			var objects []struct{ Name string }
			if err := json.Unmarshal(listing.Body.Bytes(), &objects); err != nil {
				t.Fatal(err)
			}
			if len(objects) != 1 || objects[0].Name != objectName {
				t.Fatalf("object identity changed: got %+v, want %q", objects, objectName)
			}
			if got := call(http.MethodHead, containerPath, "", http.StatusNoContent).Header().Get("X-Container-Object-Count"); got != "1" {
				t.Fatalf("literal container count: %q", got)
			}
			if got := call(http.MethodGet, objectPath, "", http.StatusOK).Body.String(); got != "known body" {
				t.Fatalf("literal object body: %q", got)
			}
			call(http.MethodDelete, objectPath, "", http.StatusNoContent)
			call(http.MethodDelete, containerPath, "", http.StatusNoContent)
			if got := call(http.MethodGet, "/v1/AUTH_test?format=json", "", http.StatusOK).Body.String(); strings.TrimSpace(got) != "[]" {
				t.Fatalf("literal container not deleted: %s", got)
			}
		})
	}
}

func TestRefuseEncodedSlashContainer(t *testing.T) {
	for index, target := range []string{"/%2F", "/one%2Ftwo", "/one%2ftwo/object", "/one%2F/object"} {
		t.Run(fmt.Sprint(index), func(t *testing.T) {
			mock := swifttest.New("test:tester", "testing")
			token := literalMockToken(t, mock)
			r := httptest.NewRequest(http.MethodPut, "/v1/AUTH_test"+target, strings.NewReader("must not be stored"))
			r.Header.Set("X-Auth-Token", token)
			w := httptest.NewRecorder()
			mock.ServeHTTP(w, r)
			if w.Code != http.StatusBadRequest {
				t.Fatalf("invalid container component %q: HTTP %d", target, w.Code)
			}
			r = httptest.NewRequest(http.MethodGet, "/v1/AUTH_test?format=json", nil)
			r.Header.Set("X-Auth-Token", token)
			w = httptest.NewRecorder()
			mock.ServeHTTP(w, r)
			if w.Code != http.StatusOK || strings.TrimSpace(w.Body.String()) != "[]" {
				t.Fatalf("refused request changed the namespace: HTTP %d %s", w.Code, w.Body.String())
			}
		})
	}
}

func TestCopyToLiteralContainer(t *testing.T) {
	for index, name := range []string{" leading", "trailing  ", "   ", "日本語", "%2F +%20"} {
		t.Run(fmt.Sprint(index), func(t *testing.T) {
			mock := swifttest.New("test:tester", "testing")
			mock.AddContainer("source", "standard")
			mock.AddContainer(name, "standard")
			mock.PutObject("source", "original", swifttest.Object{Data: []byte("known body"), Bytes: 10})
			token := literalMockToken(t, mock)
			object := "copied%2F// +"
			r := httptest.NewRequest("COPY", "/v1/AUTH_test/source/original", nil)
			r.Header.Set("X-Auth-Token", token)
			r.Header.Set("Destination", url.PathEscape(name)+"/"+url.PathEscape(object))
			w := httptest.NewRecorder()
			mock.ServeHTTP(w, r)
			if w.Code != http.StatusCreated {
				t.Fatalf("COPY to literal container %q: HTTP %d", name, w.Code)
			}
			r = httptest.NewRequest(http.MethodGet, "/v1/AUTH_test/"+url.PathEscape(name)+"/"+url.PathEscape(object), nil)
			r.Header.Set("X-Auth-Token", token)
			w = httptest.NewRecorder()
			mock.ServeHTTP(w, r)
			if w.Code != http.StatusOK || w.Body.String() != "known body" {
				t.Fatalf("literal COPY destination: HTTP %d %s", w.Code, w.Body.String())
			}
		})
	}
}

func literalMockToken(t *testing.T, mock *swifttest.Server) string {
	t.Helper()
	auth := httptest.NewRequest(http.MethodGet, "/auth/v1.0", nil)
	auth.Header.Set("X-Auth-User", "test:tester")
	auth.Header.Set("X-Auth-Key", "testing")
	authResponse := httptest.NewRecorder()
	mock.ServeHTTP(authResponse, auth)
	token := authResponse.Header().Get("X-Auth-Token")
	if token == "" {
		t.Fatal("fixture authentication returned no token")
	}
	return token
}

func TestCopyRefusesEncodedSlashContainer(t *testing.T) {
	for index, destination := range []string{"%2F/object", "one%2Ftwo/object", "one%2ftwo/object"} {
		t.Run(fmt.Sprint(index), func(t *testing.T) {
			mock := swifttest.New("test:tester", "testing")
			mock.AddContainer("source", "standard")
			mock.PutObject("source", "original", swifttest.Object{Data: []byte("known body"), Bytes: 10})
			token := literalMockToken(t, mock)
			request := func(method, target string) *httptest.ResponseRecorder {
				t.Helper()
				r := httptest.NewRequest(method, target, nil)
				r.Header.Set("X-Auth-Token", token)
				r.Header.Set("Destination", destination)
				w := httptest.NewRecorder()
				mock.ServeHTTP(w, r)
				return w
			}
			before := request(http.MethodGet, "/v1/AUTH_test?format=json")
			if before.Code != http.StatusOK {
				t.Fatal(before.Code)
			}
			if got := request("COPY", "/v1/AUTH_test/source/original"); got.Code != http.StatusBadRequest {
				t.Fatalf("invalid COPY container %q: HTTP %d", destination, got.Code)
			}
			after := request(http.MethodGet, "/v1/AUTH_test?format=json")
			if after.Code != http.StatusOK || before.Body.String() != after.Body.String() {
				t.Fatalf("refused COPY changed the inventory: HTTP %d %s", after.Code, after.Body.String())
			}
			body := request(http.MethodGet, "/v1/AUTH_test/source/original")
			if body.Code != http.StatusOK || body.Body.String() != "known body" {
				t.Fatalf("refused COPY changed its source: HTTP %d %s", body.Code, body.Body.String())
			}
		})
	}
}
