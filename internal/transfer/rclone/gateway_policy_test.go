package rclone

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const gatewayAccount = "/v1/AUTH_fixture"
const gatewayStage = ".aeronautes/stage/job/batch"

type gatewayObservation struct {
	method, uri, host, token, body string
}

type gatewayFixture struct {
	root   string
	remote Remote
	events chan gatewayObservation
}

func newGatewayFixture(t *testing.T, scope *gatewayScope) gatewayFixture {
	t.Helper()
	events := make(chan gatewayObservation, 100)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		body, err := io.ReadAll(req.Body)
		if err != nil {
			t.Error(err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		events <- gatewayObservation{req.Method, req.RequestURI, req.Host, req.Header.Get("X-Auth-Token"), string(body)}
		w.Header().Set("X-Auth-Token", "upstream-secret")
		w.Header().Set("X-Storage-Token", "upstream-secret")
		w.Header().Set("Location", "https://unrelated.invalid/")
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"Errors":[["owned/fixture","403 Forbidden"]]}`)
	}))
	t.Cleanup(upstream.Close)
	gateway, remote, err := newStorageGateway(t.Context(), Remote{Name: "aeronautes_dst", StorageURL: upstream.URL + gatewayAccount, Token: "upstream-secret"}, scope, 64)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(gateway.Close)
	return gatewayFixture{root: strings.TrimSuffix(remote.StorageURL, gatewayAccount), remote: remote, events: events}
}

func (f gatewayFixture) request(t *testing.T, method, target, token, body string, headers http.Header) (int, string) {
	t.Helper()
	req, err := http.NewRequest(method, f.root+target, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if headers != nil {
		req.Header = headers.Clone()
	}
	if token != "" {
		req.Header.Set("X-Auth-Token", token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	for _, header := range []string{"X-Auth-Token", "X-Storage-Token", "Location"} {
		if resp.Header.Get(header) != "" {
			t.Errorf("gateway forwarded response header %s", header)
		}
	}
	return resp.StatusCode, string(data)
}

func TestGatewayRefusesUnscopedRequests(t *testing.T) {
	batch := &gatewayScope{container: "owned", object: gatewayStage}
	exact := &gatewayScope{container: "owned", object: gatewayStage + "/one", exact: true}
	for _, test := range []struct {
		name, method, target, token string
		scope                       *gatewayScope
	}{
		{"missing token", "GET", gatewayAccount + "/owned/one", "", nil},
		{"wrong token", "GET", gatewayAccount + "/owned/one", "wrong", nil},
		{"other account", "GET", "/v1/AUTH_other/owned/one", "local", nil},
		{"account sibling", "GET", gatewayAccount + "_other/owned/one", "local", nil},
		{"dot segment", "GET", gatewayAccount + "/owned/%2e%2e/live", "local", nil},
		{"empty segment in a write", "PUT", gatewayAccount + "/owned/" + gatewayStage + "/a//b", "local", batch},
		{"empty segment in a delete", "DELETE", gatewayAccount + "/owned_segments/" + gatewayStage + "/a//b", "local", batch},
		{"source put", "PUT", gatewayAccount + "/owned/" + gatewayStage + "/one", "local", nil},
		{"source delete", "DELETE", gatewayAccount + "/owned/" + gatewayStage + "/one", "local", nil},
		{"live destination", "PUT", gatewayAccount + "/owned/live", "local", batch},
		{"other stage", "PUT", gatewayAccount + "/owned/.aeronautes/stage/other/one", "local", batch},
		{"stage sibling", "PUT", gatewayAccount + "/owned/" + gatewayStage + "-other/one", "local", batch},
		{"other container", "PUT", gatewayAccount + "/other/" + gatewayStage + "/one", "local", batch},
		{"other sidecar", "PUT", gatewayAccount + "/other_segments/" + gatewayStage + "/one/ts/0", "local", batch},
		{"other segments", "DELETE", gatewayAccount + "/owned_segments/unrelated/0", "local", batch},
		{"container delete", "DELETE", gatewayAccount + "/owned_segments", "local", batch},
		{"main container create", "PUT", gatewayAccount + "/owned", "local", batch},
		{"container post", "POST", gatewayAccount + "/owned", "local", batch},
		{"copy verb", "COPY", gatewayAccount + "/owned/" + gatewayStage + "/one", "local", batch},
		{"object post", "POST", gatewayAccount + "/owned/" + gatewayStage + "/one", "local", batch},
		{"write query", "PUT", gatewayAccount + "/owned/" + gatewayStage + "/one?extract-archive=tar", "local", batch},
		{"exact sibling", "PUT", gatewayAccount + "/owned/" + gatewayStage + "/two", "local", exact},
		{"exact descendant", "PUT", gatewayAccount + "/owned/" + gatewayStage + "/one/child", "local", exact},
		{"exact segment sibling", "PUT", gatewayAccount + "/owned_segments/" + gatewayStage + "/one-other/ts/0", "local", exact},
		{"anonymous other path", "GET", "/info/other", "", nil},
		{"anonymous info query", "GET", "/info?other=1", "", nil},
		{"info put", "PUT", "/info", "", nil},
	} {
		t.Run(test.name, func(t *testing.T) {
			f := newGatewayFixture(t, test.scope)
			token := test.token
			if token == "local" {
				token = f.remote.Token
			}
			status, _ := f.request(t, test.method, test.target, token, "", nil)
			if status != http.StatusForbidden || len(f.events) != 0 {
				t.Fatalf("unscoped request: status=%d upstream requests=%d", status, len(f.events))
			}
		})
	}
}

// ADR 0122 decision 3: an upload's child writes at the names themselves. It
// may write and delete under the destination folder, in the container and in
// its sidecar container, or anywhere in both when the destination is the
// container's top, and nowhere else; and where a replaced large object's
// segments must be kept it may delete nothing at all, one by one or in bulk.
func TestGatewayConfinesAnUploadToItsDestination(t *testing.T) {
	folder := &gatewayScope{container: "owned", object: "folder"}
	top := &gatewayScope{container: "owned"}
	keeps := &gatewayScope{container: "owned", object: "folder", keepsObjects: true}
	compares := &gatewayScope{container: "owned", object: "folder", setsTimes: true}
	for _, test := range []struct {
		name, method, target string
		scope                *gatewayScope
		allowed              bool
	}{
		{"a file under the folder", "PUT", "/owned/folder/sub/file", folder, true},
		{"a segment under the folder", "PUT", "/owned_segments/folder/file/ts/00000000", folder, true},
		{"an old segment under the folder", "DELETE", "/owned_segments/folder/file/old/00000000", folder, true},
		{"a file beside the folder", "PUT", "/owned/folder-other/file", folder, false},
		{"a file above the folder", "PUT", "/owned/file", folder, false},
		{"a segment beside the folder", "DELETE", "/owned_segments/elsewhere/00000000", folder, false},
		{"another container", "PUT", "/other/folder/file", folder, false},
		{"another sidecar", "PUT", "/other_segments/folder/file/ts/00000000", folder, false},
		{"a file at the top", "PUT", "/owned/file", top, true},
		{"a segment at the top", "PUT", "/owned_segments/file/ts/00000000", top, true},
		{"an old segment at the top", "DELETE", "/owned_segments/file/old/00000000", top, true},
		{"another container from the top", "PUT", "/other/file", top, false},
		{"another sidecar from the top", "DELETE", "/other_segments/file/old/00000000", top, false},
		{"a write where segments are kept", "PUT", "/owned/folder/file", keeps, true},
		{"a delete where segments are kept", "DELETE", "/owned_segments/folder/file/old/00000000", keeps, false},
		// ADR 0122 decision 12: a child told to compare sets an object's
		// time by a POST, only to an object it may write.
		{"a time set on a file under the folder", "POST", "/owned/folder/file", compares, true},
		{"a time set where nothing compares", "POST", "/owned/folder/file", folder, false},
		{"a time set beside the folder", "POST", "/owned/folder-other/file", compares, false},
		{"a time set on the container", "POST", "/owned", compares, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			f := newGatewayFixture(t, test.scope)
			status, _ := f.request(t, test.method, gatewayAccount+test.target, f.remote.Token, "", nil)
			if forwarded := status == http.StatusOK && len(f.events) == 1; forwarded != test.allowed {
				t.Fatalf("status=%d upstream requests=%d, want allowed=%v", status, len(f.events), test.allowed)
			}
		})
	}
	// The same bulk delete of an old segment is forwarded for an upload that
	// may remove it, and refused whole where segments are kept.
	for name, scope := range map[string]*gatewayScope{"a bulk delete under the folder": folder, "a bulk delete where segments are kept": keeps} {
		t.Run(name, func(t *testing.T) {
			f := newGatewayFixture(t, scope)
			status, _ := f.request(t, "DELETE", gatewayAccount+"?bulk-delete=1", f.remote.Token,
				"/owned_segments/folder/file/old/00000000\n", http.Header{"Content-Type": {"text/plain"}})
			if forwarded := status == http.StatusOK && len(f.events) == 1; forwarded == scope.keepsObjects {
				t.Fatalf("status=%d upstream requests=%d, want forwarded=%v", status, len(f.events), !scope.keepsObjects)
			}
		})
	}
}

func TestGatewayPreservesScopedPathsAndQueries(t *testing.T) {
	scope := &gatewayScope{container: "owned", object: gatewayStage}
	for _, test := range []struct{ method, path string }{
		{"GET", gatewayAccount},
		{"HEAD", gatewayAccount + "/owned"},
		{"GET", gatewayAccount + "/owned/a%2fb%252F%20%2B%25?prefix=a%252Fb+space&marker=a%2Bb"},
		// A read may name an empty segment: the engine asks for one under a
		// manifest prefix that ends in a slash.
		{"HEAD", gatewayAccount + "/owned_segments/subject/run//0000000000000001"},
		{"PUT", gatewayAccount + "/owned/" + gatewayStage + "/literal%252F%20%2b%25"},
		{"PUT", gatewayAccount + "/owned_segments"},
		{"PUT", gatewayAccount + "/owned_segments/" + gatewayStage + "/object/ts/00000000"},
		{"DELETE", gatewayAccount + "/owned_segments/" + gatewayStage + "/object/ts/00000000"},
	} {
		t.Run(test.method+" "+test.path, func(t *testing.T) {
			f := newGatewayFixture(t, scope)
			status, _ := f.request(t, test.method, test.path, f.remote.Token, "", nil)
			if test.method == "PUT" && test.path == gatewayAccount+"/owned_segments" && len(f.events) == 2 {
				if head := <-f.events; head.method != "HEAD" || head.uri != gatewayAccount+"/owned" {
					t.Fatal("sidecar policy was not checked on the exact destination container")
				}
			}
			if status != http.StatusOK || len(f.events) != 1 {
				t.Fatalf("scoped request refused: status=%d events=%d", status, len(f.events))
			}
			event := <-f.events
			if event.uri != test.path || event.method != test.method || event.token != "upstream-secret" || event.host == strings.TrimPrefix(f.root, "http://") {
				t.Fatalf("upstream path, method, token or Host changed incorrectly: %+v", event)
			}
		})
	}
	f := newGatewayFixture(t, nil)
	status, _ := f.request(t, "GET", "/info", "", "", nil)
	if status != http.StatusOK || len(f.events) != 1 {
		t.Fatal("anonymous capability request refused")
	}
	if event := <-f.events; event.token != "" || event.uri != "/info" {
		t.Fatal("capability request received upstream credentials or changed path")
	}
}

func TestGatewayRefusesContainerPolicyAndDLOEscapes(t *testing.T) {
	scope := &gatewayScope{container: "owned", object: gatewayStage}
	for _, header := range []string{"X-Container-Read", "X-Container-Write", "X-Container-Meta-Test", "X-Versions-Location", "X-History-Location", "X-Remove-Container-Meta-Test"} {
		t.Run(header, func(t *testing.T) {
			f := newGatewayFixture(t, scope)
			headers := http.Header{}
			headers.Set(header, "forbidden")
			status, _ := f.request(t, "PUT", gatewayAccount+"/owned_segments", f.remote.Token, "", headers)
			if status != http.StatusForbidden || len(f.events) != 0 {
				t.Fatal("container policy escaped the gateway")
			}
		})
	}
	for _, manifest := range []string{
		"other/" + gatewayStage + "/one/ts", "owned_segments/live/ts",
		"owned_segments/" + gatewayStage + "/other/ts", "owned_segments/" + gatewayStage + "/one/../live",
		"owned_segments/" + gatewayStage + "/one/%2e%2e/live", "owned_segments/" + gatewayStage + "/one/%XX",
	} {
		f := newGatewayFixture(t, scope)
		headers := http.Header{"X-Object-Manifest": []string{manifest}}
		status, _ := f.request(t, "PUT", gatewayAccount+"/owned/"+gatewayStage+"/one", f.remote.Token, "", headers)
		if status != http.StatusForbidden || len(f.events) != 0 {
			t.Fatalf("DLO scope escape accepted: %q", manifest)
		}
	}
	f := newGatewayFixture(t, scope)
	headers := http.Header{"X-Object-Manifest": []string{"owned_segments/" + gatewayStage + "/one/ts"}}
	if status, _ := f.request(t, "PUT", gatewayAccount+"/owned/"+gatewayStage+"/one", f.remote.Token, "", headers); status != http.StatusOK {
		t.Fatal("scoped DLO manifest refused")
	}
}

func TestGatewayRefusesConnectionHeaderSuppression(t *testing.T) {
	scope := &gatewayScope{container: "owned", object: gatewayStage}
	for _, header := range []string{"X-Storage-Policy", "X-Object-Manifest", "Content-Encoding"} {
		t.Run(header, func(t *testing.T) {
			f := newGatewayFixture(t, scope)
			headers := http.Header{"Connection": []string{header}}
			status, _ := f.request(t, "PUT", gatewayAccount+"/owned_segments", f.remote.Token, "", headers)
			if status != http.StatusForbidden || len(f.events) != 0 {
				t.Fatal("child can suppress a validated header before forwarding")
			}
		})
	}
}

func TestGatewayValidatesTheWholeBulkCleanup(t *testing.T) {
	scope := &gatewayScope{container: "owned", object: gatewayStage}
	valid := "/owned_segments/" + gatewayStage + "/one/ts/00000000\n"
	headers := http.Header{"Content-Type": []string{"text/plain"}}
	for name, body := range map[string]string{
		"mixed scope": valid + "/owned/live\n", "container only": "/owned_segments\n",
		"other job": "/owned_segments/.aeronautes/stage/other/0\n", "bad escape": valid + "/owned/%XX\n",
		"encoded escape": valid + "/owned%2Flive\n", "double encoded boundary": valid + "/owned%252Flive\n",
		"blank line": valid + "\n", "empty": "", "name limit": strings.Repeat(valid, maxGatewayBulkNames+1),
		"body limit": "/owned/" + gatewayStage + "/" + strings.Repeat("x", maxGatewayBulkBytes),
	} {
		t.Run(name, func(t *testing.T) {
			f := newGatewayFixture(t, scope)
			status, _ := f.request(t, "DELETE", gatewayAccount+"?bulk-delete=1", f.remote.Token, body, headers)
			if status != http.StatusForbidden || len(f.events) != 0 {
				t.Fatalf("bulk refusal failed: status=%d events=%d", status, len(f.events))
			}
		})
	}
	f := newGatewayFixture(t, scope)
	body := valid + "/owned/" + gatewayStage + "/literal%252F+%20%25\n"
	status, response := f.request(t, "DELETE", gatewayAccount+"?bulk-delete=1", f.remote.Token, body, headers)
	if status != http.StatusOK || len(f.events) != 1 {
		t.Fatal("scoped bulk cleanup refused")
	}
	if event := <-f.events; event.body != body || event.uri != gatewayAccount+"?bulk-delete=1" {
		t.Fatal("validated bulk cleanup was rewritten")
	}
	if response != `{"Errors":[["owned/fixture","403 Forbidden"]]}` {
		t.Fatal("per-name cleanup errors were lost")
	}
}
