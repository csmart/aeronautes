package systemtest

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/csmart/aeronautes/internal/app"
	"github.com/csmart/aeronautes/internal/jobs"
)

// Maintained regressions for ADR 0080 on the upload routes. An upload
// declares the content it intends before the store keeps it, so bytes
// that differ from the file the job planned to send are refused and
// nothing is published at the operator's name.
//
// The alteration happens in the request, not the response, because that
// is where an upload's content crosses the network: the proxy rewrites
// the body of one PUT while preserving its length exactly, so every
// length check on the route still passes and only the content can catch
// it.

// uploadProxy fronts one account's storage URL and rewrites the body of
// PUT requests whose path ends in the given suffix.
type uploadProxy struct {
	hits   int
	alter  func([]byte) []byte
	suffix string
	// before runs on every proxied request before it is forwarded, so a
	// fixture can change what the store holds at an exact point in the
	// route's own sequence. It returns the storage path prefix stripped
	// from the proxied path, which is what a raw write needs.
	before func(req *http.Request, path string)
}

func installUploadProxy(t *testing.T, h *Harness, account *Account, suffix string, alter func([]byte) []byte) *uploadProxy {
	return installUploadProxyHooked(t, h, account, suffix, alter, nil)
}

func installUploadProxyHooked(t *testing.T, h *Harness, account *Account, suffix string, alter func([]byte) []byte,
	before func(req *http.Request, path string)) *uploadProxy {
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

	p := &uploadProxy{alter: alter, suffix: suffix, before: before}
	proxy := httputil.NewSingleHostReverseProxy(&target)
	proxy.Transport = transport
	proxy.ErrorHandler = func(w http.ResponseWriter, _ *http.Request, err error) {
		t.Errorf("upload proxy: %v", err)
		http.Error(w, "fixture proxy failed", http.StatusBadGateway)
	}
	director := proxy.Director
	proxy.Director = func(req *http.Request) {
		director(req)
		if p.before != nil {
			p.before(req, strings.TrimPrefix(req.URL.Path, storage.EscapedPath()))
		}
		if p.alter == nil || req.Method != http.MethodPut || !strings.HasSuffix(req.URL.Path, p.suffix) || req.Body == nil {
			return
		}
		original, err := io.ReadAll(req.Body)
		if err != nil {
			t.Errorf("upload proxy body: %v", err)
			return
		}
		_ = req.Body.Close()
		altered := p.alter(original)
		if len(altered) != len(original) {
			t.Fatalf("fixture changed length %d -> %d; these cases are only about same-size difference", len(original), len(altered))
		}
		p.hits++
		req.Body = io.NopCloser(bytes.NewReader(altered))
		req.ContentLength = int64(len(altered))
		req.Header.Set("Content-Length", strconv.Itoa(len(altered)))
	}
	front := httptest.NewServer(proxy)
	t.Cleanup(front.Close)

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

func TestUploadContentIsBoundToTheSourceMock(t *testing.T) {
	uploadContentBinding(t, findMockHarness)
}

func TestUploadContentIsBoundToTheSourceRealSwift(t *testing.T) {
	uploadContentBinding(t, New)
}

// The native route publishes at the live name in one request, so it
// declares the digest it intends and the store keeps nothing else. The
// control proves the proxy is not the cause of the refusal.
func uploadContentBinding(t *testing.T, makeHarness func(*testing.T) *Harness) {
	t.Helper()
	for _, tc := range []struct {
		name    string
		altered bool
	}{
		{"refuses a same-size difference", true},
		{"publishes matching content", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := makeHarness(t)
			container := h.Container(h.Primary, "upload-binding")
			other := h.Container(h.Secondary, "upload-binding-untouched")
			h.Seed(h.Primary, container, "keep", []byte("destination sentinel"), nil)
			h.Seed(h.Secondary, other, "keep", []byte("other account sentinel"), nil)
			before, beforeOther := h.SnapshotNamespace(h.Primary), h.SnapshotNamespace(h.Secondary)

			// A name the pinned engine respells is planned onto the
			// native route, which is the path this case is about.
			base := "subject" + engineReservedRune + ".bin"
			body := []byte("the bytes this job planned to send, of a known length")
			dir := t.TempDir()
			path := filepath.Join(dir, base)
			if err := os.WriteFile(path, body, 0o600); err != nil {
				t.Fatal(err)
			}
			alter := passBytes
			if tc.altered {
				alter = invertBytes
			}
			proxy := installUploadProxy(t, h, h.Primary, "/"+base, alter)
			if _, err := h.Browse.Connect(t.Context(), h.Primary.BookmarkID); err != nil {
				t.Fatal(err)
			}

			app.WireFilePicker(h.Transfer, listingFilePicker{files: []string{path}, dir: dir})
			picked, err := h.Transfer.PickUploadFiles(t.Context(), app.SelectionScopeDTO{BookmarkID: h.Primary.BookmarkID, Container: container, Prefix: "live/"})
			if err != nil || picked == nil {
				t.Fatalf("pick: %+v %v", picked, err)
			}
			id, err := h.Transfer.StartUpload(t.Context(), app.UploadRequestDTO{
				BookmarkID: h.Primary.BookmarkID, Container: container, Prefix: "live/", SelectionToken: picked.Token,
			})
			if err != nil {
				t.Fatal(err)
			}
			h.AwaitJob(id)
			job, err := h.Jobs.Get(t.Context(), h.Primary.BookmarkID, id)
			if err != nil {
				t.Fatal(err)
			}
			if proxy.hits == 0 {
				t.Fatalf("the proxy saw no upload body; the case proves nothing. job state=%s counters=%+v error=%q", job.State, job.Counters, job.Error)
			}

			page, err := jobs.NewStore(h.database).ReadItems(t.Context(), id, 0, 100)
			if err != nil || len(page.Items) != 1 || page.Items[0].Execution == nil {
				t.Fatalf("persisted item rows = %d %v", len(page.Items), err)
			}
			execution := page.Items[0].Execution.ItemObservation

			live := container + "/live/" + base
			if tc.altered {
				if job.State == "complete" || job.Counters.ObjectsCopied != 0 {
					t.Fatalf("an altered upload was published: state=%s counters=%+v", job.State, job.Counters)
				}
				// The refusal the store makes is recorded exactly as
				// ADR 0080 decision 7 says: a commit the route cannot
				// verify, whose effect is unresolved because a PUT was
				// invoked and the store's answer is its own to give.
				if execution.Result != jobs.ItemFailed || execution.Effect != jobs.EffectUnresolved ||
					execution.Diagnostic.Stage != jobs.StageCommit || execution.Diagnostic.Code != jobs.CodeUnverified {
					t.Fatalf("a refused native upload was misrecorded: %+v", execution)
				}
				if _, err := h.Inspector.Object(t.Context(), h.Primary.BookmarkID, container, "live/"+base); err == nil {
					t.Errorf("a refused upload left an object at %s", live)
				}
				// Nothing in either account changed.
				h.AssertNoCollateral(before, h.SnapshotNamespace(h.Primary))
				h.AssertNoCollateral(beforeOther, h.SnapshotNamespace(h.Secondary))
				return
			}
			if job.State != "complete" || job.Counters.ObjectsCopied != 1 || job.Counters.ObjectsVerified != 1 {
				t.Fatalf("matching content was not published: state=%s counters=%+v", job.State, job.Counters)
			}
			detail, err := h.Inspector.Object(t.Context(), h.Primary.BookmarkID, container, "live/"+base)
			if err != nil || detail.ContentLength != int64(len(body)) {
				t.Fatalf("published object = %+v %v", detail, err)
			}
			if execution.Result != jobs.ItemCopied || execution.Effect != jobs.EffectReportedApplied ||
				execution.CopiedBytes != int64(len(body)) {
				t.Fatalf("a published native upload was misrecorded: %+v", execution)
			}
			rows, err := jobs.NewStore(h.database).List(t.Context(), jobs.Filter{})
			if err != nil || len(rows) != 1 {
				t.Fatalf("job rows = %d %v", len(rows), err)
			}
			h.AssertNoCollateral(beforeOther, h.SnapshotNamespace(h.Secondary))
		})
	}
}

// The held-source cases through the public service, on both fixtures.
// ADR 0080 decision 2 says the digest and the body come from one held
// descriptor. Two cases distinguish that from a digest taken after the
// fact, and they are opposites:
//
//   - the held file's bytes change in place, at the same length, after
//     the digest was taken. The store must refuse, because the digest the
//     request declared describes the bytes the job read first. An
//     implementation that digested what it was about to send would
//     declare the new bytes and the store would keep corrupted content.
//   - the file's PATHNAME is replaced by a different file of the same
//     length while the job holds the original open. The upload must
//     SUCCEED and publish the bytes the job opened, because a descriptor
//     is what it sends. An implementation that re-opened the pathname for
//     its digest would refuse this, and refusing it would be the defect.
//
// The fixture acts when the proxy sees the application's own PUT, which
// is after the digest read; the request body is still being drawn from
// the descriptor at that point, because the file is far larger than the
// buffers between the two. The proxy records the digest of the body it
// forwarded, so each case proves which bytes actually crossed the wire
// rather than assuming it.

func TestNativeUploadBindsItsHeldSourceMock(t *testing.T) {
	nativeHeldSourceBinding(t, findMockHarness)
}

func TestNativeUploadBindsItsHeldSourceRealSwift(t *testing.T) {
	nativeHeldSourceBinding(t, New)
}

func nativeHeldSourceBinding(t *testing.T, makeHarness func(*testing.T) *Harness) {
	t.Helper()
	for _, tc := range []struct {
		name    string
		replace bool
	}{
		{"refuses a held file that changes in place", false},
		{"sends the held file when its pathname is replaced", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := makeHarness(t)
			container := h.Container(h.Primary, "upload-held-source")
			other := h.Container(h.Secondary, "upload-held-untouched")
			h.Seed(h.Primary, container, "keep", []byte("destination sentinel"), nil)
			h.Seed(h.Secondary, other, "keep", []byte("other account sentinel"), nil)
			beforeA, beforeB := h.SnapshotNamespace(h.Primary), h.SnapshotNamespace(h.Secondary)

			// A name the pinned engine respells goes natively, which is the
			// route that holds a descriptor. The file is large enough that
			// the bytes after the fixture's change cannot already have been
			// drawn from it.
			base := "subject" + engineReservedRune + ".bin"
			body := bytes.Repeat([]byte("the bytes this job planned to send"), 250000)
			dir := t.TempDir()
			path := filepath.Join(dir, base)
			if err := os.WriteFile(path, body, 0o600); err != nil {
				t.Fatal(err)
			}
			spare := filepath.Join(dir, "spare.bin")
			if err := os.WriteFile(spare, inverted(body), 0o600); err != nil {
				t.Fatal(err)
			}

			// atomic.Value refuses a nil interface, and "the change
			// succeeded" is exactly a nil error, so the outcome is wrapped.
			var changed, sent atomic.Value
			change := func(req *http.Request, p string) {
				if req.Method != http.MethodPut || !strings.Contains(req.Header.Get("User-Agent"), appAgent) ||
					!strings.HasSuffix(p, "/live/"+base) || changed.Load() != nil {
					return
				}
				if tc.replace {
					// A different file of the same length takes the
					// pathname. The descriptor the job holds is untouched.
					changed.Store(fixtureOutcome{os.Rename(spare, path)})
					return
				}
				// The held file's own bytes change, in place, at the same
				// length: the second half is inverted through the same
				// inode the job is reading.
				file, err := os.OpenFile(path, os.O_WRONLY, 0o600)
				if err != nil {
					changed.Store(fixtureOutcome{err})
					return
				}
				half := int64(len(body) / 2)
				_, writeErr := file.WriteAt(inverted(body[half:]), half)
				changed.Store(fixtureOutcome{errors.Join(writeErr, file.Close())})
			}
			proxy := installUploadProxyHooked(t, h, h.Primary, "/"+base, func(forwarded []byte) []byte {
				sent.Store(md5Hex(forwarded))
				return forwarded
			}, change)
			if _, err := h.Browse.Connect(t.Context(), h.Primary.BookmarkID); err != nil {
				t.Fatal(err)
			}

			app.WireFilePicker(h.Transfer, listingFilePicker{files: []string{path}, dir: dir})
			picked, err := h.Transfer.PickUploadFiles(t.Context(), app.SelectionScopeDTO{
				BookmarkID: h.Primary.BookmarkID, Container: container, Prefix: "live/"})
			if err != nil || picked == nil {
				t.Fatalf("pick: %+v %v", picked, err)
			}
			id, err := h.Transfer.StartUpload(t.Context(), app.UploadRequestDTO{
				BookmarkID: h.Primary.BookmarkID, Container: container, Prefix: "live/", SelectionToken: picked.Token,
			})
			if err != nil {
				t.Fatal(err)
			}
			h.AwaitJob(id)
			job, err := h.Jobs.Get(t.Context(), h.Primary.BookmarkID, id)
			if err != nil {
				t.Fatal(err)
			}
			page, err := jobs.NewStore(h.database).ReadItems(t.Context(), id, 0, 100)
			if err != nil || len(page.Items) != 1 || page.Items[0].Execution == nil {
				t.Fatalf("persisted item rows = %d %v", len(page.Items), err)
			}
			item := page.Items[0]

			if proxy.hits == 0 {
				t.Fatalf("the proxy saw no upload body; the case proves nothing. state=%s", job.State)
			}
			outcome, acted := changed.Load().(fixtureOutcome)
			if !acted {
				t.Fatal("the fixture never acted; the case proves nothing")
			}
			if outcome.err != nil {
				t.Fatalf("the fixture's change failed: %v", outcome.err)
			}
			forwarded, _ := sent.Load().(string)
			original := md5Hex(body)

			if tc.replace {
				// The control: the descriptor's own bytes crossed the wire
				// and the upload published them.
				if forwarded != original {
					t.Fatalf("a replaced pathname changed the bytes the job sent: forwarded %s, the held file is %s", forwarded, original)
				}
				if job.State != "complete" || job.Counters.ObjectsCopied != 1 || job.Counters.ObjectsVerified != 1 {
					t.Fatalf("the held descriptor's upload did not publish: state=%s counters=%+v", job.State, job.Counters)
				}
				if item.Execution.Result != jobs.ItemCopied || item.Execution.Effect != jobs.EffectReportedApplied ||
					item.Verification == nil || item.Verification.Result != jobs.ItemVerified {
					t.Fatalf("the published control was misrecorded: %+v %+v", item.Execution, item.Verification)
				}
				detail, err := h.Inspector.Object(t.Context(), h.Primary.BookmarkID, container, "live/"+base)
				if err != nil || detail.ContentLength != int64(len(body)) || !strings.EqualFold(strings.Trim(detail.ETag, `"`), original) {
					t.Fatalf("the published object is not the bytes the job opened: %+v %v", detail, err)
				}
				h.AssertNoCollateral(beforeB, h.SnapshotNamespace(h.Secondary))
				return
			}

			// The refusal: the bytes that crossed the wire are not the
			// bytes the declared digest describes, so the store kept
			// nothing.
			if forwarded == original {
				t.Fatalf("the fixture did not change the bytes the job sent; the case proves nothing")
			}
			if job.State == "complete" || job.Counters.ObjectsCopied != 0 {
				t.Fatalf("a held file that changed under the upload was published: state=%s counters=%+v", job.State, job.Counters)
			}
			if item.Execution.Result != jobs.ItemFailed || item.Execution.Effect != jobs.EffectUnresolved ||
				item.Execution.Diagnostic.Stage != jobs.StageCommit || item.Execution.Diagnostic.Code != jobs.CodeUnverified {
				t.Fatalf("a refused native upload was misrecorded: %+v", item.Execution.ItemObservation)
			}
			if _, err := h.Inspector.Object(t.Context(), h.Primary.BookmarkID, container, "live/"+base); err == nil {
				t.Errorf("a refused upload left an object at %s/live/%s", container, base)
			}
			h.AssertNoCollateral(beforeA, h.SnapshotNamespace(h.Primary))
			h.AssertNoCollateral(beforeB, h.SnapshotNamespace(h.Secondary))
		})
	}
}

// fixtureOutcome carries a fixture's result through an atomic.Value,
// which cannot hold the nil interface a successful change produces.
type fixtureOutcome struct{ err error }
