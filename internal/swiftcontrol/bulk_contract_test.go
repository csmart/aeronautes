package swiftcontrol_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/csmart/aeronautes/internal/swiftcontrol"
	"github.com/csmart/aeronautes/internal/swifttest"
)

func craftedCluster(t *testing.T, reply map[string]any) *swiftcontrol.Session {
	t.Helper()
	payload, err := json.Marshal(reply)
	if err != nil {
		t.Fatal(err)
	}
	mock := swifttest.New("test:tester", "testing")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Query().Has("bulk-delete") {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write(payload)
			return
		}
		mock.ServeHTTP(w, r)
	}))
	t.Cleanup(server.Close)
	return effectObserver(t, server.URL+"/auth/v1.0")
}

func failureNames(result *swiftcontrol.BulkDeleteResult) []string {
	names := make([]string, len(result.Failures))
	for i, failure := range result.Failures {
		names[i] = failure.Name
	}
	slices.Sort(names)
	return names
}

func namesN(n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = fmt.Sprintf("n%04d", i)
	}
	return out
}

func reply(deleted, notFound int, errors ...[2]string) map[string]any {
	rows := [][2]string{}
	rows = append(rows, errors...)
	return map[string]any{
		"Number Deleted": deleted, "Number Not Found": notFound,
		"Response Status": "200 OK", "Response Body": "",
		"Errors": rows,
	}
}

func wire(container, name string) string {
	segments := strings.Split(name, "/")
	for i, s := range segments {
		segments[i] = url.PathEscape(s)
	}
	return "/" + container + "/" + strings.Join(segments, "/")
}

func replyShaped(status string, deleted, notFound int, rows ...[]string) map[string]any {
	if rows == nil {
		rows = [][]string{}
	}
	return map[string]any{
		"Number Deleted": deleted, "Number Not Found": notFound,
		"Response Status": status, "Response Body": "",
		"Errors": rows,
	}
}

func TestBulkRejectsUntrustedReplies(t *testing.T) {
	cases := []struct {
		label      string
		requested  []string
		reply      map[string]any
		wantFailed []string
	}{
		{"one row naming the last name, nothing else accounted", namesN(5),
			reply(0, 0, [2]string{wire("media", "n0004"), "500 Internal Server Error"}),
			[]string{"n0004"}},
		{"rows out of request order", namesN(5),
			reply(0, 0, [2]string{wire("media", "n0003"), "500 Internal Server Error"},
				[2]string{wire("media", "n0000"), "503 Service Unavailable"}),
			[]string{"n0000", "n0003"}},
		{"the same row twice", namesN(5),
			reply(0, 0, [2]string{wire("media", "n0003"), "500 Internal Server Error"},
				[2]string{wire("media", "n0003"), "500 Internal Server Error"}),
			[]string{"n0003"}},
		{"two rows disagreeing about one name", namesN(2),
			reply(0, 0, [2]string{wire("media", "n0000"), "500 Internal Server Error"},
				[2]string{wire("media", "n0000"), "409 Conflict"}),
			[]string{"n0000"}},
		{"a row naming a path never requested", namesN(4),
			reply(0, 0, [2]string{wire("media", "never-requested"), "500 Internal Server Error"}),
			nil},
		{"counts claim successes and one name is unaccounted", namesN(5),
			reply(3, 0, [2]string{wire("media", "n0000"), "500 Internal Server Error"}),
			[]string{"n0000"}},
		{"a row whose status is empty", namesN(3),
			reply(0, 0, [2]string{wire("media", "n0000"), ""}),
			nil},
		{"a row whose status is itself a success", namesN(3),
			reply(0, 0, [2]string{wire("media", "n0000"), "200 OK"}),
			nil},
		{"a row whose status cannot be parsed", namesN(3),
			reply(0, 0, [2]string{wire("media", "n0000"), "broken"}),
			nil},
		{"a single name and nothing accounted", namesN(1), reply(0, 0), nil},
		{"names whose spelling needs escaping", []string{"dir/a b", "dir/c%d", "héllo", "line\nbreak", "plain"},
			reply(0, 0, [2]string{wire("media", "dir/a b"), "500 Internal Server Error"}),
			[]string{"dir/a b"}},
	}

	for _, tc := range cases {
		t.Run(tc.label, func(t *testing.T) {
			session := craftedCluster(t, tc.reply)
			result, err := session.BulkDelete(t.Context(), "media", tc.requested)
			if err == nil || result.Validated || len(result.Anomalies) == 0 {
				t.Fatalf("untrusted reply accepted: %+v, %v", result, err)
			}
			if got := failureNames(result); !slices.Equal(got, tc.wantFailed) {
				t.Errorf("reported identities = %v, want %v", got, tc.wantFailed)
			}
			for _, failure := range result.Failures {
				if !strings.HasPrefix(failure.Status, "4") && !strings.HasPrefix(failure.Status, "5") {
					t.Errorf("failure lost its status: %+v", failure)
				}
			}
		})
	}
}

func TestBulkValidatedAggregates(t *testing.T) {
	for _, tc := range []struct {
		name              string
		requested         []string
		reply             map[string]any
		deleted, notFound int64
		failed            []string
	}{
		{"all settled", namesN(5), reply(4, 1), 4, 1, nil},
		{"partial", []string{"a", "b"}, reply(1, 0, [2]string{wire("media", "b"), "500 Internal Server Error"}), 1, 0, []string{"b"}},
		{"encoded partial", []string{"a.bin", "b+plus.bin"}, reply(1, 0, [2]string{replyPath("media", "b+plus.bin"), "500 Internal Server Error"}), 1, 0, []string{"b+plus.bin"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result, err := craftedCluster(t, tc.reply).BulkDelete(t.Context(), "media", tc.requested)
			if err != nil || !result.Validated || result.Deleted != tc.deleted || result.NotFound != tc.notFound || !slices.Equal(failureNames(result), tc.failed) {
				t.Fatalf("result = %+v, %v", result, err)
			}
		})
	}
}

func TestBulkRejectsMalformedRepliesThatReconcile(t *testing.T) {
	cases := []struct {
		label string
		reply map[string]any
	}{
		{"an overall status that is not a status",
			replyShaped("200not-a-status", 1, 0)},
		{"an overall status that is empty",
			replyShaped("", 1, 0)},
		{"a row code carrying a sign",
			replyShaped("200 OK", 1, 0, []string{wire("media", "a"), "+500 Internal Server Error"})},
		{"a row code with a leading zero",
			replyShaped("200 OK", 1, 0, []string{wire("media", "a"), "0500 Internal Server Error"})},
		{"a row code with no separator",
			replyShaped("200 OK", 1, 0, []string{wire("media", "a"), "500Internal"})},
		{"a row carrying a third element",
			replyShaped("200 OK", 1, 0, []string{wire("media", "a"), "500 Internal Server Error", "extra"})},
		{"a row path padded with spaces",
			replyShaped("200 OK", 1, 0, []string{" " + wire("media", "a") + " ", "500 Internal Server Error"})},
	}
	for _, tc := range cases {
		t.Run(tc.label, func(t *testing.T) {
			result, err := craftedCluster(t, tc.reply).BulkDelete(t.Context(), "media", []string{"a"})
			if err == nil || result.Validated || len(result.Failures) != 0 {
				t.Fatalf("malformed reply became a disposition: %+v, %v", result, err)
			}
		})
	}
}

func TestBulkRejectsUndocumentedOutcomes(t *testing.T) {
	row := [2]string{wire("media", "b"), "500 Internal Server Error"}
	cases := []struct {
		label string
		reply map[string]any
	}{
		{"an informational overall status", map[string]any{
			"Number Deleted": 1, "Number Not Found": 0, "Response Status": "100 Continue",
			"Response Body": "", "Errors": [][2]string{row}}},
		{"a created overall status", map[string]any{
			"Number Deleted": 1, "Number Not Found": 0, "Response Status": "201 Created",
			"Response Body": "", "Errors": [][2]string{row}}},
		{"a no-content overall status", map[string]any{
			"Number Deleted": 1, "Number Not Found": 0, "Response Status": "204 No Content",
			"Response Body": "", "Errors": [][2]string{row}}},
		{"a redirect overall status", map[string]any{
			"Number Deleted": 1, "Number Not Found": 0, "Response Status": "302 Found",
			"Response Body": "", "Errors": [][2]string{row}}},
		{"no deleted count at all", map[string]any{
			"Number Not Found": 1, "Response Status": "200 OK",
			"Response Body": "", "Errors": [][2]string{row}}},
		{"a null deleted count", map[string]any{
			"Number Deleted": nil, "Number Not Found": 1, "Response Status": "200 OK",
			"Response Body": "", "Errors": [][2]string{row}}},
		{"no not-found count at all", map[string]any{
			"Number Deleted": 1, "Response Status": "200 OK",
			"Response Body": "", "Errors": [][2]string{row}}},
		{"a null not-found count", map[string]any{
			"Number Deleted": 1, "Number Not Found": nil, "Response Status": "200 OK",
			"Response Body": "", "Errors": [][2]string{row}}},
	}
	for _, tc := range cases {
		t.Run(tc.label, func(t *testing.T) {
			result, err := craftedCluster(t, tc.reply).BulkDelete(t.Context(), "media", []string{"a", "b"})
			if err == nil || result.Validated || len(result.Anomalies) == 0 {
				t.Fatalf("undocumented outcome accepted: %+v, %v", result, err)
			}
		})
	}
}

func TestBulkRetainsMalformedRowsWhole(t *testing.T) {
	row := []string{wire("media", "a"), "500 Internal Server Error", "third", "fourth"}
	result, err := craftedCluster(t, replyShaped("200 OK", 1, 0, row)).BulkDelete(t.Context(), "media", []string{"a"})
	if err == nil || result.Validated || !reflect.DeepEqual(result.MalformedRows, [][]string{row}) {
		t.Fatalf("malformed row evidence changed: %+v, %v", result, err)
	}
}

// Swift quotes reply paths more strictly than Go's request encoder.
// Keep this independent so both spellings are tested against decoded identity.
func swiftQuote(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		unreserved := (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') ||
			c == '-' || c == '.' || c == '_' || c == '~' || c == '/'
		if unreserved {
			b.WriteByte(c)
			continue
		}
		fmt.Fprintf(&b, "%%%02X", c)
	}
	return b.String()
}

func replyPath(container, name string) string {
	return "/" + swiftQuote(container) + "/" + swiftQuote(name)
}

func TestBulkResolvesTheServiceReplyEncoding(t *testing.T) {
	names := []string{
		"plus+and&amp.bin",
		"colon:name.bin",
		"equals=name.bin",
		"at@name.bin",
		"dollar$name.bin",
		"comma,semi;name.bin",
		"literal%percent.bin",
		"dir/with a space.bin",
		"héllo-ünicode.bin",
	}
	for _, name := range names {
		for _, spelling := range []struct {
			label string
			path  func(string, string) string
		}{
			{"as the service returns it", replyPath},
			{"as the request sent it", func(c, n string) string { return wire(c, n) }},
		} {
			t.Run(name+" "+spelling.label, func(t *testing.T) {
				row := [2]string{spelling.path("media", name), "500 Internal Server Error"}
				result, err := craftedCluster(t, reply(0, 0, row)).BulkDelete(t.Context(), "media", []string{name})
				if err != nil || !result.Validated || len(result.Failures) != 1 || result.Failures[0].Name != name || result.Failures[0].Status != "500 Internal Server Error" {
					t.Fatalf("legitimate identity/status lost: %+v, %v", result, err)
				}
			})
		}
	}
}

func TestBulkRefusesRowsThatAreNotOurs(t *testing.T) {
	cases := []struct{ label, path string }{
		{"another container", replyPath("other-container", "a b.bin")},
		{"a name never requested", replyPath("media", "never.bin")},
		{"whitespace added around the path", " " + replyPath("media", "a b.bin") + " "},
		{"malformed percent encoding", "/media/a%zz.bin"},
		{"no object part at all", "/media"},
		{"an empty object part", "/media/"},
		{"a double-encoded name", replyPath("media", swiftQuote("a b.bin"))},
	}
	for _, tc := range cases {
		t.Run(tc.label, func(t *testing.T) {
			result, err := craftedCluster(t, reply(0, 0, [2]string{tc.path, "500 Internal Server Error"})).BulkDelete(t.Context(), "media", []string{"a b.bin"})
			if err == nil || result.Validated || len(result.Failures) != 0 || !slices.Equal(result.UnmatchedPaths, []string{tc.path}) {
				t.Fatalf("foreign row lost or adopted: %+v, %v", result, err)
			}
		})
	}
}

func realSwiftSession(t *testing.T) *swiftcontrol.Session {
	t.Helper()
	authURL := os.Getenv("AERONAUTES_SAIO_AUTH_URL")
	if authURL == "" {
		t.Skip("the owned Swift fixture is not configured")
	}
	session, err := swiftcontrol.New(swiftcontrol.Config{
		AuthURL: authURL, Username: "test:tester", APIKey: "testing",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := session.Authenticate(context.Background()); err != nil {
		t.Fatal(err)
	}
	return session
}

func TestBulkDeleteEncodingRoundTripsRealSwift(t *testing.T) {
	session := realSwiftSession(t)
	ctx := context.Background()
	capabilities, err := session.FetchCapabilities(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if capabilities.BulkDeleteMaxDeletes() <= 0 {
		t.Skip("the owned fixture does not advertise bulk delete")
	}
	stamp := time.Now().UnixNano()
	container := fmt.Sprintf("aeronautes-bulkid-%d", stamp)
	neighbour := fmt.Sprintf("aeronautes-bulkid-%d-neighbour", stamp)
	for _, name := range []string{container, neighbour} {
		if _, err := session.CreateContainer(ctx, swiftcontrol.ContainerCreateRequest{Name: name}); err != nil {
			t.Fatal(err)
		}
	}

	doomed := []string{
		"plain.bin",
		"dir/with a space.bin",
		"literal%percent.bin",
		"percent-like%41.bin",
		"héllo-ünicode.bin",
		"line\nbreak.bin",
		"deep/nested/child.bin",
		"trailing-plus+and&amp.bin",
	}
	kept := "kept-neighbour.bin"
	inNeighbour := "other-namespace.bin"
	seeded := append(append([]string{}, doomed...), kept)

	// Every object this test creates is removed again, in both
	// namespaces, whatever the test does in between.
	t.Cleanup(func() {
		background := context.Background()
		for _, pair := range [][2]string{{container, kept}, {neighbour, inNeighbour}} {
			for _, name := range append([]string{pair[1]}, doomed...) {
				_ = session.DeleteObject(background, pair[0], name)
			}
		}
		for _, name := range []string{container, neighbour} {
			if err := session.DeleteContainer(background, name); err != nil {
				t.Errorf("cleanup left %q behind: %v", name, err)
			}
		}
	})

	for _, name := range seeded {
		if _, err := session.PutObject(ctx, container, name, strings.NewReader("payload"), "application/octet-stream", nil); err != nil {
			t.Fatalf("seed %q: %v", name, err)
		}
	}
	if _, err := session.PutObject(ctx, neighbour, inNeighbour, strings.NewReader("payload"), "application/octet-stream", nil); err != nil {
		t.Fatal(err)
	}
	// Each seeded name must be authoritatively readable under its exact
	// spelling first, or the delete would prove nothing.
	for _, name := range seeded {
		if _, err := session.ObjectDetail(ctx, container, name); err != nil {
			t.Fatalf("the fixture did not store %q under that exact spelling: %v", name, err)
		}
	}

	result, err := session.BulkDelete(ctx, container, doomed)
	if err != nil || !result.Validated || result.Deleted != int64(len(doomed)) || len(result.Failures) != 0 {
		t.Fatalf("owned service reply = %+v, %v", result, err)
	}

	// Authoritative witnesses: each named object is gone, the unnamed
	// neighbour in the same container survives, and the second namespace
	// is untouched.
	for _, name := range doomed {
		if _, err := session.ObjectDetail(ctx, container, name); err == nil {
			t.Errorf("%q survived, so its request encoding did not name it", name)
		} else if !swiftcontrol.IsNotFound(err) {
			t.Errorf("%q could not be witnessed as absent: %v", name, err)
		}
	}
	if _, err := session.ObjectDetail(ctx, container, kept); err != nil {
		t.Errorf("the neighbour %q was destroyed by a request that never named it: %v", kept, err)
	}
	if _, err := session.ObjectDetail(ctx, neighbour, inNeighbour); err != nil {
		t.Errorf("the second namespace was disturbed: %v", err)
	}
}

// The same reply can accompany opposite effects. The observer bypasses
// this proxy, which substitutes bytes after either forwarding or withholding
// the actual request. Authentication stays routed through the proxy.
type swiftProxy struct {
	target, self string
	forward      bool
	payload      []byte
	client       *http.Client
}

func (p *swiftProxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	bulk := r.Method == http.MethodPost && r.URL.Query().Has("bulk-delete")
	if !bulk || p.forward {
		outbound, err := http.NewRequest(r.Method, p.target+r.URL.RequestURI(), bytes.NewReader(body))
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		outbound.Header = r.Header.Clone()
		resp, err := p.client.Do(outbound)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		defer resp.Body.Close()
		payload, _ := io.ReadAll(resp.Body)
		if !bulk {
			for key, values := range resp.Header {
				for _, v := range values {
					if strings.EqualFold(key, "X-Storage-Url") {
						v = strings.Replace(v, p.target, p.self, 1)
					}
					w.Header().Add(key, v)
				}
			}
			w.WriteHeader(resp.StatusCode)
			_, _ = w.Write(payload)
			return
		}
	}

	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(p.payload)
}

func effectObserver(t *testing.T, authURL string) *swiftcontrol.Session {
	t.Helper()
	session, err := swiftcontrol.New(swiftcontrol.Config{
		AuthURL: authURL, Username: "test:tester", APIKey: "testing",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := session.Authenticate(context.Background()); err != nil {
		t.Fatal(err)
	}
	return session
}

func witnessGone(t *testing.T, observer *swiftcontrol.Session, container, name string) {
	t.Helper()
	_, err := observer.ObjectDetail(context.Background(), container, name)
	switch {
	case err == nil:
		t.Errorf("%s/%s is still present, so nothing was witnessed as deleted", container, name)
	case !swiftcontrol.IsNotFound(err):
		t.Fatalf("%s/%s could not be observed, so absence is not established: %v", container, name, err)
	}
}

func witnessBytes(t *testing.T, observer *swiftcontrol.Session, container, name, want string) {
	t.Helper()
	body, _, err := observer.OpenObject(context.Background(), container, name)
	if err != nil {
		t.Fatalf("%s/%s is not present: %v", container, name, err)
	}
	defer body.Close()
	got, err := io.ReadAll(body)
	if err != nil {
		t.Fatalf("%s/%s could not be read: %v", container, name, err)
	}
	if string(got) != want {
		t.Errorf("%s/%s content = %q, want %q", container, name, got, want)
	}
}

func TestBulkEffectSubstitutionMock(t *testing.T) {
	mock := swifttest.New("test:tester", "testing")
	server := httptest.NewServer(mock)
	t.Cleanup(server.Close)
	for _, valid := range []bool{false, true} {
		t.Run(fmt.Sprintf("valid_%v", valid), func(t *testing.T) {
			runBulkEffectPair(t, server.URL, effectObserver(t, server.URL+"/auth/v1.0"), valid)
		})
	}
}

func TestBulkEffectSubstitutionRealSwift(t *testing.T) {
	authURL := os.Getenv("AERONAUTES_SAIO_AUTH_URL")
	if authURL == "" {
		t.Skip("the owned Swift fixture is not configured")
	}
	base, _, ok := strings.Cut(authURL, "/auth/")
	if !ok {
		t.Fatal("owned fixture URL has no authentication path")
	}
	runBulkEffectPair(t, base, effectObserver(t, authURL), false)
}

func runBulkEffectPair(t *testing.T, base string, observer *swiftcontrol.Session, valid bool) {
	t.Helper()
	ctx := t.Context()
	container := fmt.Sprintf("aeronautes-bulksub-%d", time.Now().UnixNano())
	if _, err := observer.CreateContainer(ctx, swiftcontrol.ContainerCreateRequest{Name: container}); err != nil {
		t.Fatal(err)
	}
	targets := []string{"target-one", "target-two"}
	rows := [][2]string{{replyPath(container, targets[0]), "500 Internal Server Error"}}
	if valid {
		rows = append(rows, [2]string{replyPath(container, targets[1]), "403 Forbidden"})
	}
	payload, err := json.Marshal(reply(0, 0, rows...))
	if err != nil {
		t.Fatal(err)
	}
	contents := map[string]string{targets[0]: "one payload", targets[1]: "two payload", "neighbour": "keep bytes"}
	t.Cleanup(func() {
		for name := range contents {
			_ = observer.DeleteObject(context.Background(), container, name)
		}
		if err := observer.DeleteContainer(context.Background(), container); err != nil {
			t.Errorf("cleanup: %v", err)
		}
	})
	var results []*swiftcontrol.BulkDeleteResult
	for _, forward := range []bool{false, true} {
		for name, body := range contents {
			if _, err := observer.PutObject(ctx, container, name, strings.NewReader(body), "application/octet-stream", nil); err != nil {
				t.Fatal(err)
			}
		}
		proxy := &swiftProxy{target: base, forward: forward, payload: payload, client: &http.Client{}}
		server := httptest.NewServer(proxy)
		t.Cleanup(server.Close)
		proxy.self = server.URL
		session := effectObserver(t, server.URL+"/auth/v1.0")
		result, err := session.BulkDelete(ctx, container, targets)
		wantFailed := targets[:1]
		if valid {
			wantFailed = targets
		}
		if (err == nil) != valid || result.Validated != valid || result.Deleted != 0 || result.NotFound != 0 || !slices.Equal(failureNames(result), wantFailed) {
			t.Fatalf("effect=%v reply result = %+v, %v", forward, result, err)
		}
		results = append(results, result)
		for _, name := range targets {
			if forward {
				witnessGone(t, observer, container, name)
			} else {
				witnessBytes(t, observer, container, name, contents[name])
			}
		}
		witnessBytes(t, observer, container, "neighbour", contents["neighbour"])
	}
	if !reflect.DeepEqual(results[0], results[1]) {
		t.Errorf("identical reply changed with hidden effects: %+v, %+v", results[0], results[1])
	}
}

func TestSharedSegmentsSurviveManifestDeletion(t *testing.T) {
	mock := swifttest.New("test:tester", "testing")
	server := httptest.NewServer(mock)
	t.Cleanup(server.Close)
	session := effectObserver(t, server.URL+"/auth/v1.0")
	observer := effectObserver(t, server.URL+"/auth/v1.0")
	mock.AddContainer("media", "standard")
	mock.AddContainer("segments", "standard")
	payload := strings.Repeat("x", 300)
	mock.PutObject("segments", "shared/0", swifttest.Object{Bytes: 300, ETag: "cc", Data: []byte(payload)})
	mock.PutObject("media", "sibling", swifttest.Object{Manifest: "segments/shared/"})
	for _, bulk := range []bool{false, true} {
		t.Run(fmt.Sprintf("bulk_%v", bulk), func(t *testing.T) {
			mock.PutObject("media", "target", swifttest.Object{Manifest: "segments/shared/"})
			if bulk {
				result, err := session.BulkDelete(t.Context(), "media", []string{"target"})
				if err != nil || !result.Validated || result.Deleted != 1 {
					t.Fatalf("bulk result = %+v, %v", result, err)
				}
			} else if err := session.DeleteObject(t.Context(), "media", "target"); err != nil {
				t.Fatal(err)
			}
			witnessGone(t, observer, "media", "target")
			witnessBytes(t, observer, "media", "sibling", payload)
			witnessBytes(t, observer, "segments", "shared/0", payload)
		})
	}
}
