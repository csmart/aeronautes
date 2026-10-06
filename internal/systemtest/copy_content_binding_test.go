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
	"github.com/csmart/aeronautes/internal/db"
	"github.com/csmart/aeronautes/internal/jobs"
)

// ADR 0083 decision 1 and ADR 0084, held against a real store.
//
// The native copy route declares what the destination must hash to before the
// PUT, so a body that does not hash to it is refused BY THE STORE and nothing is
// published. For a plain source that declaration is the source's own entity tag.
// For a manifest source there is no such header — the source's tag is an
// aggregate over its segments while the destination this route creates is a
// plain object — so ADR 0084 holds the assembled bytes, compares them against
// the ladder the source's own manifest supplies, and declares the digest of the
// bytes that matched.
//
// The proxy below keeps the declared header on purpose in most cases: the
// store's refusal against it is the protection under test. An older retained
// probe deletes it, which removes that protection and measures a middlebox
// instead of the route — the reason these cases exist as their own file rather
// than as an edit to that probe. One case here deletes it deliberately, to hold
// what is left when the declaration never reaches the store.

// copyBodyProxy fronts the DESTINATION account and alters the body of one
// object PUT, preserving length and every other header, so the only thing that
// changes between the cases is the bytes the store receives and whether the
// declaration survives the trip.
type copyBodyProxy struct {
	hits  int
	alter func([]byte) []byte
	strip bool
	path  string
	front string
}

func installCopyBodyProxy(t *testing.T, h *Harness, account *Account, path string, alter func([]byte) []byte, strip bool) *copyBodyProxy {
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

	p := &copyBodyProxy{alter: alter, strip: strip, path: path}
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
			if p.strip {
				// A middlebox that drops the declaration: the store then has
				// nothing to enforce and accepts whatever body it is handed.
				r.Header.Del("ETag")
				r.Header.Del("Content-Md5")
			}
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
		strip    bool
	}{
		{name: "plain_same_size_difference_is_refused", altered: true},
		{name: "plain_matching_content_is_copied"},
		{name: "held_manifest_matching_content_is_copied", manifest: true},
		{name: "held_manifest_same_size_difference_is_refused", manifest: true, altered: true},
		{name: "held_manifest_difference_is_reported_when_the_declaration_is_stripped", manifest: true, altered: true, strip: true},
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
			proxy := installCopyBodyProxy(t, h, h.Secondary, "/"+dst+"/scope/"+base, alter, testCase.strip)
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
			if proxy.hits == 0 {
				t.Fatal("the fixture never saw a destination body; the case proved nothing")
			}

			after := h.SnapshotNamespace(h.Secondary)
			published := after.Containers[dst].Objects["scope/"+base]

			// ADR 0045: no stamp from this route, on any path. The stamp is
			// the assertion that the destination holds the source's content,
			// and in a one-request publication there is no moment at which it
			// could be written truthfully.
			if published.Meta["Aeronautes-Fingerprint"] != "" {
				t.Errorf("the native route stamped a fingerprint: %q", published.Meta["Aeronautes-Fingerprint"])
			}

			switch {
			case testCase.strip:
				// Nothing prevented this publication: the store had no
				// declaration to enforce. What the route still does is report
				// it, by comparing the digest it declared against the one the
				// store reports for the plain object it created. The bytes are
				// at the live name, which is why the item is a failure with an
				// unresolved effect rather than a clean refusal.
				if published.Digest != copyBindingDigest(flipEveryByte(body)) {
					t.Errorf("published = %+v, want the altered body the store was handed", published)
				}
				if job.Counters.ObjectsCopied != 0 || job.Counters.ObjectsFailed != 1 {
					t.Errorf("stripped declaration: copied=%d failed=%d, want 0 and 1",
						job.Counters.ObjectsCopied, job.Counters.ObjectsFailed)
				}
				// A recorded content failure takes no verified credit. Without
				// this the detected corruption became its own proof: the stamp
				// survived at the live name, the verification pass accepted it,
				// and the name was then skippable as equal forever.
				if job.Counters.ObjectsVerified != 0 {
					t.Errorf("a detected content failure was credited as verified %d times", job.Counters.ObjectsVerified)
				}
				// An item's recorded key is the name relative to the prefixes
				// the request named, which is what the planner works in.
				assertStrippedFacts(t, h.jobStore, id, base, "live")
				assertSecondCopyStaysConservative(t, h, src, dst, "scope/"+base,
					copyBindingDigest(flipEveryByte(body)))
				// Reopening comes last: it closes the harness's handle, so
				// nothing that needs the live database may follow it.
				assertStrippedFacts(t, reopenState(t, h), id, base, "after reopening")
			case testCase.altered:
				// The store refuses the body against the declared tag, so the
				// name holds nothing at all.
				if published.Size != 0 || published.Digest != "" {
					t.Errorf("a same-size difference was published: %+v", published)
				}
				if job.Counters.ObjectsCopied != 0 || job.Counters.ObjectsFailed != 1 {
					t.Errorf("altered body: copied=%d failed=%d, want 0 and 1",
						job.Counters.ObjectsCopied, job.Counters.ObjectsFailed)
				}
				if job.Counters.ObjectsVerified != 0 {
					t.Errorf("a refused publication was credited as verified %d times", job.Counters.ObjectsVerified)
				}
			default:
				if published.Size != int64(len(body)) || published.Digest != copyBindingDigest(body) {
					t.Fatalf("matching content was not published: %+v", published)
				}
				// A manifest source lands as a plain object on this route, which
				// is §25 logical replication and leaves L06 open: the fidelity
				// question is recorded here rather than implied.
				if published.Class != "plain" {
					t.Errorf("published class = %q, want plain", published.Class)
				}
				if job.Counters.ObjectsCopied != 1 || job.Counters.ObjectsFailed != 0 {
					t.Errorf("matching content: copied=%d failed=%d, want 1 and 0",
						job.Counters.ObjectsCopied, job.Counters.ObjectsFailed)
				}
				// A successful copy keeps its verified credit even unstamped.
				// For the manifest row that is the whole point of the
				// current-job evidence: the source is a dynamic large object
				// whose listing row carries no usable length, so the
				// verification pass resolves it by HEAD and, with no stamp to
				// read, has only what this job itself compared.
				if job.Counters.ObjectsVerified != 1 {
					t.Errorf("a successful copy took verified credit %d times, want once", job.Counters.ObjectsVerified)
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

// assertStrippedFacts reads the item's recorded execution and verification back
// through the given store. A detected content failure that survives only in a
// counter is not enough: the operator's later decisions are made from these rows,
// and they are read again from a reopened database to show they are durable.
func assertStrippedFacts(t *testing.T, store *jobs.Store, id, key, when string) {
	t.Helper()
	page, err := store.ReadItems(t.Context(), id, 0, 100)
	if err != nil {
		t.Fatalf("read items %s: %v", when, err)
	}
	var found *jobs.ItemRecord
	for i := range page.Items {
		if page.Items[i].Key == key {
			found = &page.Items[i]
		}
	}
	if found == nil {
		var keys []string
		for _, item := range page.Items {
			keys = append(keys, item.Key)
		}
		t.Fatalf("no record for %q %s; recorded keys %q", key, when, keys)
	}
	if found.Execution == nil || found.Execution.Result != jobs.ItemFailed ||
		found.Execution.Effect != jobs.EffectUnresolved ||
		found.Execution.Diagnostic.Code != jobs.CodeUnverified {
		t.Errorf("execution %s = %+v, want failed/unresolved/unverified", when, found.Execution)
	}
	if found.Verification == nil || found.Verification.Result == jobs.ItemVerified {
		t.Errorf("verification %s = %+v, want anything but verified", when, found.Verification)
	}
}

// reopenState closes the harness's handle and returns a job store over a fresh
// one, so what follows reads only what reached the file.
func reopenState(t *testing.T, h *Harness) *jobs.Store {
	t.Helper()
	if err := h.database.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := db.Open(t.Context(), h.statePath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	return jobs.NewStore(reopened)
}

// assertSecondCopyStaysConservative runs the same copy again over the name the
// first one left holding corrupt bytes. Unstamped, there is nothing for an
// equality decision to stand on, so the second pass must refuse the overwrite
// rather than report the name as already equal — and must leave the bytes alone,
// since this build has no authority to replace them.
func assertSecondCopyStaysConservative(t *testing.T, h *Harness, src, dst, name, digestNow string) {
	t.Helper()
	id, err := h.Transfer.StartCopy(t.Context(), app.CopyRequestDTO{
		SourceBookmarkID: h.Primary.BookmarkID, SourceContainer: src, SourcePrefix: "scope/",
		DestBookmarkID: h.Secondary.BookmarkID, DestContainer: dst, DestPrefix: "scope/",
		Settings: app.TransferSettingsDTO{ChunkSize: "1Mi"},
	})
	if err != nil {
		t.Fatal(err)
	}
	second := h.AwaitJob(id)
	if second.Counters.OverwritesRefused != 1 || second.Counters.ObjectsCopied != 0 ||
		second.Counters.Overwrites != 0 || second.Counters.ObjectsVerified != 0 {
		t.Errorf("second copy counters = %+v, want one overwrite refused and nothing else claimed", second.Counters)
	}
	again := h.SnapshotNamespace(h.Secondary).Containers[dst].Objects[name]
	if again.Digest != digestNow {
		t.Errorf("the second copy changed the retained bytes: %+v", again)
	}
}
