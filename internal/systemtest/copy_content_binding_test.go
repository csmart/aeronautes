package systemtest

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/csmart/aeronautes/internal/app"
	"github.com/csmart/aeronautes/internal/jobs"
)

// ADR 0122 decision 4, "the application's own copy", held against a real
// store.
//
// A name the engine cannot carry is one GET and one PUT by the application.
// A plain source's entity tag is the MD5 of its content, so the PUT declares
// it and a body that does not hash to it is refused BY THE STORE: the object
// fails and the name is left as it was. A large source has no such tag for
// its assembled bytes, declares none, and arrives as one plain object.
//
// The proxy below alters the body and keeps the declared header, because the
// store's refusal against it is the protection under test.

// copyBodyProxy fronts the DESTINATION account and alters the body of one
// object PUT, preserving length and every header, so the only thing that
// changes between the cases is the bytes the store receives.
type copyBodyProxy struct {
	hits  int
	alter func([]byte) []byte
	path  string
	front string
}

func installCopyBodyProxy(t *testing.T, h *Harness, account *Account, path string, alter func([]byte) []byte) *copyBodyProxy {
	t.Helper()
	storage, err := url.Parse(account.storageURL)
	if err != nil {
		t.Fatal(err)
	}
	target := *storage
	target.Path, target.RawPath, target.RawQuery = "", "", ""
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.DisableCompression = true
	transport.ResponseHeaderTimeout = 30 * time.Second
	t.Cleanup(transport.CloseIdleConnections)

	p := &copyBodyProxy{alter: alter, path: path}
	reverse := httputil.NewSingleHostReverseProxy(&target)
	reverse.Transport = transport
	reverse.ErrorHandler = func(w http.ResponseWriter, _ *http.Request, err error) {
		t.Errorf("copy body proxy: %v", err)
		http.Error(w, "fixture proxy failed", http.StatusBadGateway)
	}
	front := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPut && strings.HasSuffix(r.URL.Path, p.path) {
			original, err := io.ReadAll(r.Body)
			if err != nil {
				http.Error(w, "fixture read failed", http.StatusBadRequest)
				return
			}
			altered := p.alter(original)
			if len(altered) != len(original) {
				t.Fatalf("fixture changed length %d -> %d; these cases are only about same-size difference", len(original), len(altered))
			}
			p.hits++
			r.Body = io.NopCloser(bytes.NewReader(altered))
			r.ContentLength = int64(len(altered))
		}
		reverse.ServeHTTP(w, r)
	}))
	t.Cleanup(front.Close)
	p.front = front.URL

	bookmarks, err := h.Bookmarks.List(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for _, bookmark := range bookmarks {
		if bookmark.ID != account.BookmarkID {
			continue
		}
		bookmark.StorageURLOverride = front.URL + storage.EscapedPath()
		if _, err := h.Bookmarks.Save(t.Context(), bookmark, account.key); err != nil {
			t.Fatal(err)
		}
		return p
	}
	t.Fatal("bookmark for the proxied account is missing")
	return nil
}

// The oracle reports a SHA-256 of the body it read, independently of the MD5
// this route declares, so a case states the content it expects in the oracle's
// own terms rather than in the route's.
func copyBindingDigest(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func flipEveryByte(b []byte) []byte {
	out := make([]byte, len(b))
	for i, c := range b {
		out[i] = c ^ 0xFF
	}
	return out
}

func TestCopyContentIsBoundToTheSource(t *testing.T) {
	for _, testCase := range []struct {
		name     string
		altered  bool
		manifest bool
	}{
		{name: "plain_same_size_difference_is_refused", altered: true},
		{name: "plain_matching_content_is_copied"},
		{name: "manifest_source_arrives_as_one_plain_object", manifest: true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			h := New(t)
			if h.Secondary == nil {
				t.Skip("two owned Swift accounts are required")
			}
			src := h.Container(h.Primary, "copy-binding-src")
			dst := h.Container(h.Secondary, "copy-binding-dst")
			body := bytes.Repeat([]byte("bound copy bytes "), 512)
			base := "subject" + engineReservedRune + ".bin"
			name := "scope/" + base
			if testCase.manifest {
				// A dynamic large object whose own name carries the reserved
				// rune, so the manifest itself is planned onto the native route.
				segments := h.Container(h.Primary, "copy-binding-segments")
				h.Seed(h.Primary, segments, "parts/00000000", body, nil)
				h.Seed(h.Primary, src, name, nil,
					map[string]string{"X-Object-Manifest": segments + "/parts/"})
			} else {
				h.Seed(h.Primary, src, name, body, nil)
			}

			alter := func(b []byte) []byte { return b }
			if testCase.altered {
				alter = flipEveryByte
			}
			proxy := installCopyBodyProxy(t, h, h.Secondary, "/"+dst+"/scope/"+base, alter)
			if _, err := h.Browse.Connect(t.Context(), h.Secondary.BookmarkID); err != nil {
				t.Fatal(err)
			}
			id, err := h.Transfer.StartCopy(t.Context(), app.CopyRequestDTO{
				SourceBookmarkID: h.Primary.BookmarkID, SourceContainer: src, SourcePrefix: "scope/",
				DestBookmarkID: h.Secondary.BookmarkID, DestContainer: dst, DestPrefix: "scope/",
				Settings: app.TransferSettingsDTO{ChunkSize: "1Mi"},
			})
			if err != nil {
				t.Fatal(err)
			}
			job := h.AwaitJob(id)
			if job.Counters.NativeRouted != 1 {
				t.Fatalf("this case is about the native route but %d items took it", job.Counters.NativeRouted)
			}
			if proxy.hits != 1 {
				t.Fatalf("the destination took %d bodies for the name, want the one PUT", proxy.hits)
			}

			after := h.SnapshotNamespace(h.Secondary)
			published := after.Containers[dst].Objects["scope/"+base]
			page, err := h.jobStore.ReadItems(t.Context(), id, 0, 100)
			if err != nil || len(page.Items) != 1 || page.Items[0].Key != base || page.Items[0].Execution == nil {
				t.Fatalf("the object's record: %+v %v", page, err)
			}
			record := page.Items[0]

			if testCase.altered {
				// The store refuses the body against the declared tag, so the
				// name holds nothing at all, and the record says nothing was
				// written.
				if published.Size != 0 || published.Digest != "" {
					t.Errorf("a same-size difference was published: %+v", published)
				}
				if job.State != "partial" || job.Counters.ObjectsCopied != 0 || job.Counters.ObjectsFailed != 1 || job.Counters.ObjectsVerified != 0 {
					t.Errorf("altered body: %s %+v, want partial with one failed and none copied", job.State, job.Counters)
				}
				if record.Execution.Result != jobs.ItemFailed || record.Execution.Effect != jobs.EffectNotAttempted ||
					record.Execution.Diagnostic.HTTPStatus != http.StatusUnprocessableEntity || record.Verification != nil {
					t.Errorf("the store's refusal was recorded as %+v, second record %+v", record.Execution, record.Verification)
				}
			} else {
				if published.Size != int64(len(body)) || published.Digest != copyBindingDigest(body) {
					t.Fatalf("matching content was not published: %+v", published)
				}
				// A manifest source lands as one plain object on this route.
				// Nothing else is added to it: no stamp, and no metadata the
				// source does not have.
				if published.Class != "plain" || len(published.Meta) != 0 {
					t.Errorf("published class %q with metadata %v, want a plain object carrying only the source's", published.Class, published.Meta)
				}
				if job.State != "complete" || job.Counters.ObjectsCopied != 1 || job.Counters.ObjectsFailed != 0 || job.Counters.ObjectsVerified != 1 {
					t.Errorf("matching content: %s %+v, want complete with one copied", job.State, job.Counters)
				}
				if record.Execution.Result != jobs.ItemCopied || record.Verification == nil {
					t.Errorf("the copied object was recorded as %+v, second record %+v", record.Execution, record.Verification)
				}
			}

			// The source account is untouched in every case: a copy reads it.
			source := h.SnapshotNamespace(h.Primary)
			got := source.Containers[src].Objects[name]
			if testCase.manifest {
				if got.Class != "dlo" || got.Size != int64(len(body)) || got.Digest != copyBindingDigest(body) {
					t.Errorf("the source manifest changed: %+v", got)
				}
			} else if got.Class != "plain" || got.Digest != copyBindingDigest(body) {
				t.Errorf("the source object changed: %+v", got)
			}
		})
	}
}
