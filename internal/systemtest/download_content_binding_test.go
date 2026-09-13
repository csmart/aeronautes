package systemtest

import (
	"bytes"
	"crypto/md5"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/csmart/aeronautes/internal/app"
)

// Maintained regressions for ADR 0045 on the download route. A download
// compares the bytes it received against the source's content
// expectation before publishing anything, so a same-size difference
// places nothing and retains what it staged.
//
// Only the native path carries this comparison. The engine path is
// deliberately unchanged: with a comparison in place there, deleting it
// left the altered engine case still refusing, so the refusal came from
// the pinned engine and not from this code. Adding code that cannot be
// shown to do anything would invite a later reader to trust it. The
// engine case kept here asserts only that matching content still
// arrives; the observation about the engine is recorded in the
// experiment record as an unestablished finding, not as protection.
//
// The native copy, native upload and staged upload commit routes the
// contract's probe also reproduced are untouched and remain the gap the
// contract names.

// contentProxy fronts one account's storage URL and rewrites the body of
// responses whose path ends in the given suffix, preserving length
// exactly so every length check on the route still passes.
type contentProxy struct {
	hits   int
	alter  func([]byte) []byte
	suffix string
}

func invertBytes(b []byte) []byte {
	out := make([]byte, len(b))
	for i, c := range b {
		out[i] = c ^ 0xFF
	}
	return out
}

func passBytes(b []byte) []byte { return b }

func objectDigest(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func contentMD5(b []byte) string {
	sum := md5.Sum(b)
	return hex.EncodeToString(sum[:])
}

// installContentProxy points the account's bookmark at a proxy that
// rewrites one object's GET body. The identity form is the control: if a
// case fails with it, the proxy is the cause and the altering case says
// nothing about the route.
func installContentProxy(t *testing.T, h *Harness, account *Account, suffix string, alter func([]byte) []byte) *contentProxy {
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

	p := &contentProxy{alter: alter, suffix: suffix}
	proxy := httputil.NewSingleHostReverseProxy(&target)
	proxy.Transport = transport
	proxy.ErrorHandler = func(w http.ResponseWriter, _ *http.Request, err error) {
		t.Errorf("content proxy: %v", err)
		http.Error(w, "fixture proxy failed", http.StatusBadGateway)
	}
	proxy.ModifyResponse = func(resp *http.Response) error {
		if resp.Request.Method != http.MethodGet || !strings.HasSuffix(resp.Request.URL.Path, p.suffix) || resp.StatusCode != http.StatusOK {
			return nil
		}
		original, err := io.ReadAll(resp.Body)
		if err != nil {
			return err
		}
		if err := resp.Body.Close(); err != nil {
			return err
		}
		altered := p.alter(original)
		if len(altered) != len(original) {
			t.Fatalf("fixture changed length %d -> %d; these cases are only about same-size difference", len(original), len(altered))
		}
		p.hits++
		resp.Body = io.NopCloser(bytes.NewReader(altered))
		return nil
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

// engineReservedRune is respelled by the pinned engine, so a name
// carrying it is planned onto the native route.
const engineReservedRune = "‛"

type downloadBindingCase struct {
	name     string
	base     string
	altered  bool
	wantHits bool
}

func TestDownloadContentIsBoundToTheSource(t *testing.T) {
	cases := []downloadBindingCase{
		{name: "native_route_refuses_a_same_size_difference", base: "subject" + engineReservedRune + ".bin", altered: true, wantHits: true},
		{name: "native_route_places_matching_content", base: "subject" + engineReservedRune + ".bin", altered: false, wantHits: true},
		{name: "engine_route_places_matching_content", base: "subject.bin", altered: false, wantHits: true},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			h := New(t)
			container := h.Container(h.Primary, "download-binding")
			body := bytes.Repeat([]byte("bound download bytes "), 512)
			name := "scope/" + testCase.base
			h.Seed(h.Primary, container, name, body, nil)

			root := t.TempDir()
			alter := passBytes
			if testCase.altered {
				alter = invertBytes
			}
			proxy := installContentProxy(t, h, h.Primary, "/"+container+"/"+name, alter)

			app.WireFilePicker(h.Transfer, bindingPicker{dir: filepath.Join(root, "selected")})
			picked, err := h.Transfer.PickDownloadDir(t.Context(), app.SelectionScopeDTO{
				BookmarkID: h.Primary.BookmarkID, Container: container, Prefix: "scope/"})
			if err != nil || picked == nil {
				t.Fatalf("pick the destination: %+v %v", picked, err)
			}
			request := app.DownloadRequestDTO{BookmarkID: h.Primary.BookmarkID, Container: container, Prefix: "scope/", DestDirToken: picked.Token}
			preflight, err := h.Transfer.PreflightDownload(t.Context(), request)
			if err != nil || preflight == nil || len(preflight.Items) != 1 {
				t.Fatalf("preflight: %+v %v", preflight, err)
			}
			wantRoute := "native"
			if !strings.Contains(testCase.base, engineReservedRune) {
				wantRoute = "rclone"
			}
			if preflight.Items[0].Route != wantRoute {
				t.Fatalf("case is about the %s route but planned %q", wantRoute, preflight.Items[0].Route)
			}
			id, err := h.Transfer.StartDownload(t.Context(), request)
			if err != nil {
				t.Fatal(err)
			}
			job := h.AwaitJob(id)

			if testCase.wantHits && proxy.hits == 0 {
				t.Fatal("the fixture never saw the object's content; the case proved nothing")
			}
			placed, readErr := os.ReadFile(filepath.Join(root, "selected", testCase.base))

			if !testCase.altered {
				if readErr != nil || !bytes.Equal(placed, body) {
					t.Fatalf("matching content was not placed: err=%v equal=%v", readErr, bytes.Equal(placed, body))
				}
				if job.Counters.ObjectsVerified != 1 || job.Counters.ObjectsFailed != 0 {
					t.Fatalf("matching content: verified=%d failed=%d, want 1 and 0",
						job.Counters.ObjectsVerified, job.Counters.ObjectsFailed)
				}
				return
			}

			// The whole point: a same-size difference publishes nothing.
			if readErr == nil {
				t.Fatalf("a same-size difference was published: %d bytes, matches source=%v, source md5 %s",
					len(placed), bytes.Equal(placed, body), contentMD5(body))
			}
			if !os.IsNotExist(readErr) {
				t.Fatalf("target could not be inspected: %v", readErr)
			}
			if job.Counters.ObjectsVerified != 0 {
				t.Errorf("a refused download counted %d verified", job.Counters.ObjectsVerified)
			}
			if job.Counters.ObjectsFailed != 1 {
				t.Errorf("a refused download counted %d failed, want 1", job.Counters.ObjectsFailed)
			}
			// The source object is untouched either way: a download never
			// writes to the account it reads.
			after := h.SnapshotNamespace(h.Primary)
			if stored := after.Containers[container].Objects[name]; stored.Digest != objectDigest(body) {
				t.Errorf("the source object changed: %+v", stored)
			}
		})
	}
}

type bindingPicker struct{ dir string }

func (p bindingPicker) PickFiles(string) ([]string, error) { return nil, nil }

func (p bindingPicker) PickDirectory(string) (string, error) {
	if err := os.MkdirAll(p.dir, 0o755); err != nil {
		return "", err
	}
	return p.dir, nil
}
