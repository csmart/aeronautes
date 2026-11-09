package systemtest

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/csmart/aeronautes/internal/app"
)

type metadataWriteTrace struct {
	mu     sync.Mutex
	writes []string
}

func (trace *metadataWriteTrace) snapshot() []string {
	trace.mu.Lock()
	defer trace.mu.Unlock()
	return append([]string(nil), trace.writes...)
}

func traceMetadataWrites(t *testing.T, h *Harness) *metadataWriteTrace {
	t.Helper()
	storage, err := url.Parse(h.Primary.storageURL)
	if err != nil {
		t.Fatal(err)
	}
	target := *storage
	target.Path, target.RawPath, target.RawQuery = "", "", ""
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.DisableCompression = true
	transport.ResponseHeaderTimeout = 10 * time.Second
	t.Cleanup(transport.CloseIdleConnections)
	proxy := httputil.NewSingleHostReverseProxy(&target)
	proxy.Transport = transport
	trace := &metadataWriteTrace{}
	direct := proxy.Director
	proxy.Director = func(request *http.Request) {
		direct(request)
		if request.Method != http.MethodGet && request.Method != http.MethodHead {
			trace.mu.Lock()
			trace.writes = append(trace.writes, request.Method+" "+request.URL.EscapedPath())
			trace.mu.Unlock()
		}
	}
	proxy.ErrorHandler = func(w http.ResponseWriter, _ *http.Request, err error) {
		t.Errorf("metadata fixture proxy: %v", err)
		http.Error(w, "metadata fixture proxy failed", http.StatusBadGateway)
	}
	front := httptest.NewServer(proxy)
	t.Cleanup(front.Close)
	bookmarks, err := h.Bookmarks.List(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for _, bookmark := range bookmarks {
		if bookmark.ID != h.Primary.BookmarkID {
			continue
		}
		// Setup and independent snapshots keep their direct provider URL,
		// so a mutation trace cannot be satisfied by fixture writes.
		bookmark.StorageURLOverride = front.URL + storage.EscapedPath()
		if _, err := h.Bookmarks.Save(t.Context(), bookmark, h.Primary.key); err != nil {
			t.Fatal(err)
		}
		if _, err := h.Browse.Connect(t.Context(), bookmark.ID); err != nil {
			t.Fatal(err)
		}
		return trace
	}
	t.Fatal("owned metadata bookmark not found")
	return nil
}

func metadataInputRefusals(t *testing.T, h *Harness) {
	t.Helper()
	container := h.Container(h.Primary, "metadata-refusal")
	h.Seed(h.Primary, container, "target", []byte("target bytes"), map[string]string{
		"Content-Type": "text/plain", "X-Object-Meta-Keep": "sentinel",
	})
	h.Seed(h.Primary, container, "untouched", []byte("same container sentinel"), nil)
	secondary := h.Container(h.Secondary, "metadata-refusal-sentinel")
	h.Seed(h.Secondary, secondary, "untouched", []byte("other account sentinel"), nil)
	trace := traceMetadataWrites(t, h)
	cases := []struct {
		name string
		meta map[string]string
	}{
		{"prototype key", map[string]string{"__proto__": "literal"}},
		{"underscore key", map[string]string{"a_b": "literal"}},
		{"empty key", map[string]string{"": "literal"}},
		{"leading key space", map[string]string{" Key": "literal"}},
		{"trailing key space", map[string]string{"Key ": "literal"}},
		{"space-only key", map[string]string{"  ": "literal"}},
		{"colon key", map[string]string{"Key:other": "literal"}},
		{"non-ascii key", map[string]string{"雪": "literal"}},
		{"tab key", map[string]string{"Key\tname": "literal"}},
		{"newline key", map[string]string{"Key\nname": "literal"}},
		{"case duplicate", map[string]string{"Owner": "one", "OWNER": "two"}},
		{"leading value space", map[string]string{"Literal": " leading"}},
		{"trailing value space", map[string]string{"Literal": "trailing "}},
		{"space-only value", map[string]string{"Literal": "  "}},
		{"leading value tab", map[string]string{"Literal": "\tleading"}},
		{"trailing value tab", map[string]string{"Literal": "trailing\t"}},
		{"nul value", map[string]string{"Literal": "a\x00b"}},
		{"carriage return value", map[string]string{"Literal": "a\rb"}},
		{"newline value", map[string]string{"Literal": "a\nb"}},
		{"del value", map[string]string{"Literal": "a\x7fb"}},
		{"empty container value", map[string]string{"Literal": ""}},
	}
	first := map[string]*Receipt{}
	for _, action := range []string{"object", "container", "create"} {
		for index, tc := range cases {
			if action == "object" && tc.name == "empty container value" {
				continue
			}
			t.Run(action+"/"+tc.name, func(t *testing.T) {
				beforeA, beforeB := h.SnapshotNamespace(h.Primary), h.SnapshotNamespace(h.Secondary)
				writeCount := len(trace.snapshot())
				meta := map[string]string{"Keep": "sentinel"}
				for key, value := range tc.meta {
					meta[key] = value
				}
				var receipt *Receipt
				switch action {
				case "object":
					receipt = h.UpdateObjectMetadata(t.Context(), container, "target", "", app.ObjectMetadataEditDTO{
						Meta: meta, ContentType: "text/plain",
					})
				case "container":
					_, receipt = h.UpdateContainer(t.Context(), container, "", app.ContainerEditDTO{Meta: meta, VersioningMode: "off"})
				case "create":
					name := fmt.Sprintf("%s-refused-create-%d", h.ns, index)
					t.Cleanup(func() { h.purge(h.Primary, name) })
					_, receipt = h.CreateContainer(t.Context(), app.ContainerCreateDTO{Name: name, Meta: meta})
				}
				if first[action] == nil {
					first[action] = receipt
				}
				if receipt.Err() == nil {
					t.Errorf("unsupported metadata was not refused: outcome=%q", receipt.Outcome())
				}
				if writes := trace.snapshot()[writeCount:]; len(writes) != 0 {
					t.Errorf("refused metadata issued writes: %v", writes)
				}
				afterA, afterB := h.SnapshotNamespace(h.Primary), h.SnapshotNamespace(h.Secondary)
				if delta := diffOf(beforeA, afterA); !delta.Empty() {
					t.Errorf("refused metadata changed the primary namespace:\n%s", delta)
				}
				if delta := diffOf(beforeB, afterB); !delta.Empty() {
					t.Errorf("refused metadata changed the secondary namespace:\n%s", delta)
				}
			})
		}
	}
	if !t.Failed() {
		for _, action := range []string{"object", "container", "create"} {
			first[action].CreditRefusal()
		}
	}
}

func TestMetadataInputRefusalsMock(t *testing.T) {
	metadataInputRefusals(t, findMockHarness(t))
}

func TestMetadataInputRefusalsRealSwift(t *testing.T) {
	metadataInputRefusals(t, New(t))
}

func literalMetadataRoundTrips(t *testing.T, h *Harness) {
	t.Helper()
	container := h.Container(h.Primary, "literal-metadata")
	segments := h.Container(h.Primary, "literal-metadata-segments")
	h.Seed(h.Primary, segments, "parts/a", []byte("first"), nil)
	h.Seed(h.Primary, segments, "parts/b", []byte("second"), nil)
	baseHeaders := map[string]string{"Content-Type": "text/plain", "X-Object-Meta-Keep": "sentinel", "X-Object-Meta-Remove": "old"}
	h.Seed(h.Primary, container, "plain", []byte("plain bytes"), baseHeaders)
	h.Seed(h.Primary, container, "dlo", nil, map[string]string{
		"Content-Type": "text/plain", "X-Object-Meta-Keep": "sentinel", "X-Object-Meta-Remove": "old",
		"X-Object-Manifest": segments + "/parts/",
	})
	h.Seed(h.Primary, container, "untouched", []byte("same container sentinel"), nil)
	h.rawPut(h.Primary, "/"+container, nil, map[string]string{
		"X-Container-Meta-Keep": "sentinel", "X-Container-Meta-Remove": "old",
		"X-Container-Meta-Access-Control-Allow-Origin": "https://fixture.invalid",
		"X-Container-Meta-Web-Index":                   "index.html", "X-Container-Meta-Temp-Url-Key": "synthetic-fixture",
	})
	secondary := h.Container(h.Secondary, "literal-metadata-sentinel")
	h.Seed(h.Secondary, secondary, "untouched", []byte("other account sentinel"), nil)
	trace := traceMetadataWrites(t, h)
	var objectApplied, containerApplied *Receipt
	for _, action := range []string{"plain", "dlo", "container", "create"} {
		t.Run(action, func(t *testing.T) {
			meta := map[string]string{
				"Keep": "sentinel", "constructor": "own constructor", "toString": "own toString",
				"mIxEd": "mixed case", "Percent": "literal%2F+100%", "Unicode": "\u00a0雪\u2003",
				"Spaces": "two  spaces", "Tabs": "two\ttabs", "Token!#$%&'*+-.^|~": "punctuation",
			}
			wantMeta := map[string]string{
				"Keep": "sentinel", "Constructor": "own constructor", "Tostring": "own toString",
				"Mixed": "mixed case", "Percent": "literal%2F+100%", "Unicode": "\u00a0雪\u2003",
				"Spaces": "two  spaces", "Tabs": "two\ttabs", "Token!#$%&'*+-.^|~": "punctuation",
			}
			beforeA, beforeB := h.SnapshotNamespace(h.Primary), h.SnapshotNamespace(h.Secondary)
			writeCount := len(trace.snapshot())
			allowed, method, path := container+"/"+action, http.MethodPost, h.Primary.storageURL+"/"+container+"/"+action
			switch action {
			case "plain", "dlo":
				meta["Empty"] = ""
				wantMeta["Empty"] = ""
				receipt := h.UpdateObjectMetadata(t.Context(), container, action, "", app.ObjectMetadataEditDTO{
					Meta: meta, ContentType: "text/plain",
				})
				if receipt.Err() != nil || receipt.Outcome() != "applied" {
					t.Fatalf("literal object metadata failed: outcome=%q error=%v", receipt.Outcome(), receipt.Err())
				}
				if objectApplied == nil {
					objectApplied = receipt
				}
			case "container":
				// Static web and CORS keys are part of the working copy (ADR
				// 0090, ADR 0101): a copy built from the detail carries the
				// seeded ones along.
				meta["Web-Index"] = "index.html"
				wantMeta["Web-Index"] = "index.html"
				meta["Access-Control-Allow-Origin"] = "https://fixture.invalid"
				wantMeta["Access-Control-Allow-Origin"] = "https://fixture.invalid"
				result, receipt := h.UpdateContainer(t.Context(), container, "", app.ContainerEditDTO{Meta: meta, VersioningMode: "off"})
				if receipt.Err() != nil || receipt.Outcome() != "applied" {
					t.Fatalf("literal container metadata failed: outcome=%q error=%v", receipt.Outcome(), receipt.Err())
				}
				if result.ContainerAfter == nil || !reflect.DeepEqual(result.ContainerAfter.Meta, wantMeta) {
					t.Fatalf("container result lost intended metadata: %+v", result.ContainerAfter)
				}
				containerApplied = receipt
				wantMeta["Temp-Url-Key"] = "synthetic-fixture"
				allowed, path = container, h.Primary.storageURL+"/"+container
			case "create":
				name := h.ns + "-literal-created"
				t.Cleanup(func() { h.purge(h.Primary, name) })
				result, receipt := h.CreateContainer(t.Context(), app.ContainerCreateDTO{Name: name, Meta: meta})
				if receipt.Err() != nil || result.Name != name || !reflect.DeepEqual(result.Meta, wantMeta) {
					t.Fatalf("literal container creation failed: result=%+v error=%v", result, receipt.Err())
				}
				allowed, method, path = name, http.MethodPut, h.Primary.storageURL+"/"+name
			}
			endpoint, err := url.Parse(path)
			if err != nil {
				t.Fatal(err)
			}
			if writes := trace.snapshot()[writeCount:]; !reflect.DeepEqual(writes, []string{method + " " + endpoint.EscapedPath()}) {
				t.Errorf("unexpected metadata writes: %v", writes)
			}
			afterA, afterB := h.SnapshotNamespace(h.Primary), h.SnapshotNamespace(h.Secondary)
			if action == "plain" || action == "dlo" {
				before, after := beforeA.Containers[container].Objects[action], afterA.Containers[container].Objects[action]
				if !reflect.DeepEqual(after.Meta, wantMeta) {
					t.Errorf("literal object metadata = %q, want %q", after.Meta, wantMeta)
				}
				after.Meta = before.Meta
				if !reflect.DeepEqual(before, after) {
					t.Errorf("metadata edit changed object content or manifest: before=%+v after=%+v", before, after)
				}
			} else if !reflect.DeepEqual(afterA.Containers[allowed].Meta, wantMeta) {
				t.Errorf("literal container metadata = %q, want %q", afterA.Containers[allowed].Meta, wantMeta)
			}
			if stray := collateral(beforeA, afterA, []string{allowed}); len(stray) != 0 {
				t.Errorf("metadata mutation changed unrelated primary state: %s", strings.Join(stray, "; "))
			}
			if delta := diffOf(beforeB, afterB); !delta.Empty() {
				t.Errorf("metadata mutation changed secondary state:\n%s", delta)
			}
		})
	}
	if !t.Failed() {
		objectApplied.CreditApplied()
		containerApplied.CreditApplied()
	}
}

func TestLiteralMetadataRoundTripsMock(t *testing.T) {
	literalMetadataRoundTrips(t, findMockHarness(t))
}

func TestLiteralMetadataRoundTripsRealSwift(t *testing.T) {
	literalMetadataRoundTrips(t, New(t))
}

// Omitting the CORS row from a working copy removes it like any other key
// (ADR 0101 decision 6), on the mock and on the owned Swift, while the
// hidden TempURL slot and the unrelated row stay exactly as they were. The
// removal is witnessed on the raw snapshot, not by the adapter under test.
func TestCORSRowOmissionMock(t *testing.T)      { corsRowOmission(t, findMockHarness(t)) }
func TestCORSRowOmissionRealSwift(t *testing.T) { corsRowOmission(t, New(t)) }

func corsRowOmission(t *testing.T, h *Harness) {
	t.Helper()
	ctx := t.Context()
	if _, err := h.Browse.Connect(ctx, h.Primary.BookmarkID); err != nil {
		t.Fatal(err)
	}
	container := h.Container(h.Primary, "cors-omission")
	h.rawPut(h.Primary, "/"+container, nil, map[string]string{
		"X-Container-Meta-Keep":                        "sentinel",
		"X-Container-Meta-Access-Control-Allow-Origin": "https://fixture.invalid",
		"X-Container-Meta-Temp-Url-Key":                "synthetic-fixture",
	})
	detail, err := h.Inspector.Container(ctx, h.Primary.BookmarkID, container)
	if err != nil {
		t.Fatal(err)
	}
	if !detail.CORS || detail.Meta["Access-Control-Allow-Origin"] != "https://fixture.invalid" {
		t.Fatalf("detail meta = %v, cors = %v; want the CORS row in the editable set", detail.Meta, detail.CORS)
	}
	result, receipt := h.UpdateContainer(ctx, container, detail.EditableFingerprint, app.ContainerEditDTO{Meta: map[string]string{"Keep": "sentinel"}, VersioningMode: "off"})
	if receipt.Err() != nil || receipt.Outcome() != "applied" {
		t.Fatalf("edit omitting the CORS row: outcome %q, %v", receipt.Outcome(), receipt.Err())
	}
	if result.ContainerAfter == nil || result.ContainerAfter.CORS || result.ContainerAfter.Meta["Access-Control-Allow-Origin"] != "" {
		t.Fatalf("after-state = %+v; want the CORS row gone and no badge", result.ContainerAfter)
	}
	raw := h.SnapshotNamespace(h.Primary).Containers[container].Meta
	if raw["Access-Control-Allow-Origin"] != "" || raw["Keep"] != "sentinel" || raw["Temp-Url-Key"] != "synthetic-fixture" {
		t.Fatalf("raw metadata after the omission = %v; want the CORS row removed, the sentinel and the hidden slot kept", raw)
	}
}
