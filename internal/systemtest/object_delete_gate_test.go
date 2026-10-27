package systemtest

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/csmart/aeronautes/internal/app"
	"github.com/csmart/aeronautes/internal/mutationfacts"
	"github.com/csmart/aeronautes/internal/swiftcontrol"
)

// swiftGate fronts the owned Swift for one bookmark: every request of a
// session connected through it passes the gate, so a case can act on the
// real cluster between a DELETE and the verifying read, or just before the
// DELETE arrives, with the session's own token.
type swiftGate struct {
	upstream string
	server   *httptest.Server
	client   *http.Client
	mu       sync.Mutex
	// beforeDelete and afterDelete run around the forwarded DELETE of name.
	name                      string
	beforeDelete, afterDelete func(token string)
	fired                     map[string]bool
	// afterAny runs after every forwarded request has been answered, with
	// the method and path, so a case can act between a job's members.
	afterAny func(method, path string)
}

func newSwiftGate(t *testing.T, authURL string) *swiftGate {
	t.Helper()
	upstream := authURL[:strings.Index(authURL, "/auth/")]
	g := &swiftGate{upstream: upstream, client: &http.Client{}, fired: map[string]bool{}}
	g.server = httptest.NewServer(http.HandlerFunc(g.serve))
	t.Cleanup(g.server.Close)
	return g
}

func (g *swiftGate) serve(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	isDelete := r.Method == http.MethodDelete && strings.HasSuffix(r.URL.Path, "/"+g.name) && g.name != ""
	token := r.Header.Get("X-Auth-Token")
	g.mu.Lock()
	before, after := g.beforeDelete, g.afterDelete
	fireBefore := isDelete && before != nil && !g.fired["before"]
	fireAfter := isDelete && after != nil && !g.fired["after"]
	if fireBefore {
		g.fired["before"] = true
	}
	if fireAfter {
		g.fired["after"] = true
	}
	g.mu.Unlock()
	if fireBefore {
		before(token)
	}
	req, err := http.NewRequestWithContext(r.Context(), r.Method, g.upstream+r.URL.RequestURI(), strings.NewReader(string(body)))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	req.Header = r.Header.Clone()
	resp, err := g.client.Do(req)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()
	replyBody, _ := io.ReadAll(resp.Body)
	if fireAfter {
		after(token)
	}
	g.mu.Lock()
	afterAny := g.afterAny
	g.mu.Unlock()
	if afterAny != nil {
		afterAny(r.Method, r.URL.Path)
	}
	for k, values := range resp.Header {
		if k == "X-Storage-Url" {
			values = []string{strings.Replace(values[0], g.upstream, g.server.URL, 1)}
		}
		w.Header()[k] = append([]string(nil), values...)
	}
	w.WriteHeader(resp.StatusCode)
	_, _ = w.Write(replyBody)
}

// put writes an object straight to the upstream with the session's token.
func (g *swiftGate) put(t *testing.T, token, path string, body []byte, headers map[string]string) {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPut, g.upstream+path, strings.NewReader(string(body)))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("X-Auth-Token", token)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := g.client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		t.Fatalf("direct PUT %s: %d", path, resp.StatusCode)
	}
}

// gatedSwift connects a bookmark through the gate and returns its id.
func gatedSwift(t *testing.T, h *Harness, g *swiftGate) string {
	t.Helper()
	ctx := context.Background()
	saved, err := h.Bookmarks.Save(ctx, app.BookmarkDTO{DisplayName: "gated", AuthURL: g.server.URL + "/auth/v1.0",
		Username: h.Primary.user, TLSMode: "system"}, h.Primary.key)
	if err != nil {
		t.Fatal(err)
	}
	if _, rec := h.ConnectBookmark(ctx, saved.ID); rec.Err() != nil {
		t.Fatal(rec.Err())
	}
	return saved.ID
}

// On the owned Swift, with a writer acting between the DELETE and the
// verifying read: a manifest whose segments are missing is reported absent by
// a following read and found by the literal one, so the delete is applied with
// that successor observed; an empty object replaced by a link is observed as a
// link; the same content written back is unverified; and a replacement that
// lands after the final comparison, before the DELETE arrives, is deleted,
// which is ADR 0085's accepted exposure, measured here and not prevented.
func TestObjectDeleteSuccessorsAndExposureOnRealSwift(t *testing.T) {
	if os.Getenv("AERONAUTES_SAIO_AUTH_URL") == "" {
		t.Skip("owned Swift endpoint not supplied")
	}
	h := New(t)
	ctx := context.Background()
	c := h.Container(h.Primary, "gate")
	account := h.Primary.storageURL[strings.LastIndex(h.Primary.storageURL, "/v1/"):]
	g := newSwiftGate(t, h.Primary.authURL)
	bookmark := gatedSwift(t, h, g)
	objectPath := func(name string) string { return account + "/" + c + "/" + name }

	h.Seed(h.Primary, c, "keep.txt", []byte("collateral that must survive"), textType)
	h.Seed(h.Primary, c, "manifest-after.txt", []byte("to be replaced by a manifest"), textType)
	h.Seed(h.Primary, c, "empty-after", nil, textType)
	h.Seed(h.Primary, c, "same-after.txt", []byte("written back"), textType)
	h.Seed(h.Primary, c, "exposed.txt", []byte("shown"), textType)

	cases := []struct {
		name        string
		before      func(token string)
		after       func(token string)
		wantOutcome string
		wantErr     bool
		check       func(t *testing.T, result app.MutationResultDTO)
	}{
		{name: "manifest-after.txt", after: func(token string) {
			g.put(t, token, objectPath("manifest-after.txt"), nil, map[string]string{"X-Object-Manifest": c + "-missing/segs/", "Content-Type": "video/mp4"})
		}, wantOutcome: "applied", check: func(t *testing.T, result app.MutationResultDTO) {
			if result.ObjectAfter == nil || result.ObjectAfter.Class != "dlo" {
				t.Fatalf("successor = %+v, want the manifest the literal read found", result.ObjectAfter)
			}
			// The following read a client would make reports the name absent.
			if _, err := h.Inspector.Object(ctx, bookmark, c, "manifest-after.txt"); !swiftcontrol.IsNotFound(err) {
				t.Fatalf("a following read of the manifest with missing segments answered: %v", err)
			}
		}},
		{name: "empty-after", after: func(token string) {
			g.put(t, token, objectPath("empty-after"), nil, map[string]string{"X-Symlink-Target": c + "/nowhere"})
		}, wantOutcome: "applied", check: func(t *testing.T, result app.MutationResultDTO) {
			if result.ObjectAfter == nil || result.ObjectAfter.SymlinkTarget == "" {
				t.Fatalf("successor = %+v, want the link", result.ObjectAfter)
			}
		}},
		{name: "same-after.txt", after: func(token string) {
			g.put(t, token, objectPath("same-after.txt"), []byte("written back"), textType)
		}, wantErr: true},
		{name: "exposed.txt", before: func(token string) {
			g.put(t, token, objectPath("exposed.txt"), []byte("landed inside the window"), textType)
		}, wantOutcome: "applied", check: func(t *testing.T, result app.MutationResultDTO) {
			if result.ObjectAfter != nil {
				t.Fatalf("successor = %+v, want an observed absence", result.ObjectAfter)
			}
			if _, err := h.Inspector.Object(ctx, bookmark, c, "exposed.txt"); !swiftcontrol.IsNotFound(err) {
				t.Fatalf("the replacement inside the window survived: %v", err)
			}
		}},
	}
	keepBefore, keepBody := literalHeadStatus(t, h, h.Primary, c, "keep.txt"), rawBody(t, h, h.Primary, c, "keep.txt")
	for _, tc := range cases {
		shown, err := h.Inspector.Object(ctx, bookmark, c, tc.name)
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		g.mu.Lock()
		g.name, g.beforeDelete, g.afterDelete, g.fired = tc.name, tc.before, tc.after, map[string]bool{}
		g.mu.Unlock()
		result, err := h.Mutate.DeleteObject(ctx, h.ActiveScope(ctx), c, tc.name, shown.ContentFingerprint)
		fired := g.fired["before"] || g.fired["after"]
		if !fired {
			t.Fatalf("%s: the writer never acted", tc.name)
		}
		facts := deleteFactsFor(t, h, c+"/"+tc.name)
		if tc.wantErr {
			if !errors.Is(err, swiftcontrol.ErrUnverifiedDelete) {
				t.Fatalf("%s: err = %v, want unverified", tc.name, err)
			}
			if len(facts) != 1 || facts[0].Outcome != mutationfacts.OutcomeUnverified {
				t.Fatalf("%s: facts = %+v", tc.name, facts)
			}
		} else {
			if err != nil || result.Outcome != tc.wantOutcome {
				t.Fatalf("%s: result = %+v, err = %v", tc.name, result, err)
			}
			// The record carries the DELETE's transaction id beside the HEAD's.
			if len(facts) != 1 || facts[0].Outcome != mutationfacts.OutcomeApplied || distinctIDs(facts[0].TransIDs) < 2 {
				t.Fatalf("%s: facts = %+v", tc.name, facts)
			}
			tc.check(t, result)
		}
		// Collateral: the neighbour is byte for byte what it was.
		if status, body := literalHeadStatus(t, h, h.Primary, c, "keep.txt"), rawBody(t, h, h.Primary, c, "keep.txt"); status != keepBefore || body != keepBody {
			t.Fatalf("%s: the neighbour changed: %d %q", tc.name, status, body)
		}
	}
}

// rawBody GETs an object's bytes directly, for a collateral comparison that
// does not go through the product.
func rawBody(t *testing.T, h *Harness, a *Account, container, name string) string {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, a.storageURL+"/"+container+"/"+name, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("X-Auth-Token", a.token)
	resp, err := a.rawClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return string(body)
}
