package swiftcontrol

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ncw/swift/v2"
)

type pairingFixture struct {
	session *Session
	urls    [2]string
	auths   atomic.Int64
	storage atomic.Int64
	mixed   atomic.Int64
}

func newPairingFixture(t *testing.T, change string, handle http.HandlerFunc) *pairingFixture {
	t.Helper()
	f := &pairingFixture{}
	storage := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.storage.Add(1)
		n, err := strconv.ParseInt(strings.TrimPrefix(r.Header.Get("X-Auth-Token"), "fixture-"), 10, 64)
		if err != nil || n < 1 || n > f.auths.Load() {
			f.mixed.Add(1)
			w.WriteHeader(http.StatusForbidden)
			return
		}
		want := f.urls[(n-1)%2]
		actual := "http://" + r.Host + r.URL.Path
		if actual != want && !strings.HasPrefix(actual, want+"/") {
			f.mixed.Add(1)
			w.WriteHeader(http.StatusForbidden)
			return
		}
		if handle != nil {
			handle(w, r)
			return
		}
		if r.Method == http.MethodGet {
			fmt.Fprint(w, `[]`)
			return
		}
		w.Header().Set("X-Container-Bytes-Used", "0")
		w.Header().Set("X-Container-Object-Count", "0")
		w.WriteHeader(http.StatusNoContent)
	})
	first := httptest.NewServer(storage)
	t.Cleanup(first.Close)
	second := httptest.NewServer(storage)
	t.Cleanup(second.Close)
	f.urls = [2]string{first.URL + "/v1/AUTH_a", second.URL + "/v1/AUTH_b"}
	if change == "account" {
		f.urls[1] = first.URL + "/v1/AUTH_b"
	}
	advertised := f.urls
	override := ""
	if change == "override" {
		override = first.URL + "/v1/AUTH_override"
		f.urls = [2]string{override, override}
	}
	auth := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := f.auths.Add(1)
		w.Header().Set("X-Storage-Url", advertised[(n-1)%2])
		w.Header().Set("X-Auth-Token", fmt.Sprintf("fixture-%d", n))
	}))
	t.Cleanup(auth.Close)
	var err error
	f.session, err = New(Config{
		AuthURL: auth.URL, Username: "fixture", APIKey: "fixture", StorageURLOverride: override,
	})
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func (f *pairingFixture) check(t *testing.T, storage, auths int64) {
	t.Helper()
	if f.mixed.Load() != 0 || f.storage.Load() != storage || f.auths.Load() != auths {
		t.Fatalf("mixed/storage/auth requests = %d/%d/%d, want 0/%d/%d",
			f.mixed.Load(), f.storage.Load(), f.auths.Load(), storage, auths)
	}
}

func TestLibraryTargetCallbackRefusal(t *testing.T) {
	for _, authenticated := range []bool{false, true} {
		for _, result := range []string{"error", "empty", "explicit target"} {
			t.Run(fmt.Sprintf("authenticated=%t/%s", authenticated, result), func(t *testing.T) {
				f := newPairingFixture(t, "origin", func(w http.ResponseWriter, r *http.Request) {
					if !strings.HasSuffix(r.URL.Path, "/explicit") {
						t.Error("nil callback replaced the explicit target")
					}
					w.WriteHeader(http.StatusNoContent)
				})
				ctx := context.Background()
				if authenticated {
					if err := f.session.Authenticate(ctx); err != nil {
						t.Fatal(err)
					}
				}
				refused := errors.New("fixture callback refusal")
				calls := 0
				callback := func() (string, error) {
					calls++
					if result == "error" {
						return f.urls[0], refused
					}
					return "", nil
				}
				if result == "explicit target" {
					callback = nil
				}
				_, _, err := f.session.conn.Call(ctx, f.urls[0]+"/explicit", swift.RequestOpts{
					Operation: "HEAD", NoResponse: true, OnReAuth: callback,
				})
				if result == "explicit target" {
					if err != nil || calls != 0 {
						t.Fatalf("explicit target: error=%v calls=%d", err, calls)
					}
					f.check(t, 1, 1)
					return
				}
				if err == nil || calls != 1 || (result == "error" && !errors.Is(err, refused)) {
					t.Fatalf("refusal: error=%v callback calls=%d", err, calls)
				}
				if result == "empty" && err.Error() != "storage target callback returned an empty URL" {
					t.Fatalf("empty callback was not refused during target selection: %v", err)
				}
				f.check(t, 0, 1)
			})
		}
	}
}

func TestDirectTargetRefusesMissingSnapshot(t *testing.T) {
	var auth v1Auth
	if target, err := auth.storageTarget(); err == nil || target != "" {
		t.Fatalf("missing snapshot selected target %q: %v", target, err)
	}
	const target = "http://127.0.0.1/v1/AUTH_fixture"
	auth.snap.Store(&authSnapshot{storageURL: target, token: "fixture"})
	if got, err := auth.storageTarget(); err != nil || got != target {
		t.Fatalf("accepted snapshot target %q: %v", got, err)
	}
}

func TestDirectLibraryTargetKeepsPair(t *testing.T) {
	for _, change := range []string{"origin", "account", "override"} {
		t.Run(change, func(t *testing.T) {
			f := newPairingFixture(t, change, nil)
			ctx := context.Background()
			credentials, err := f.session.Credentials(ctx, 0)
			if err != nil {
				t.Fatal(err)
			}
			if err := f.session.Authenticate(ctx); err != nil {
				t.Fatal(err)
			}
			_, _, err = f.session.conn.Call(ctx, credentials.StorageURL, swift.RequestOpts{
				Operation: "HEAD", NoResponse: true, OnReAuth: f.session.auth.storageTarget,
			})
			if err != nil {
				t.Fatal(err)
			}
			f.check(t, 1, 2)
		})
	}
}

func TestLibraryPairingUnderConcurrentAuthentication(t *testing.T) {
	for _, change := range []string{"origin", "account", "override"} {
		t.Run(change, func(t *testing.T) {
			f := newPairingFixture(t, change, nil)
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			if err := f.session.Authenticate(ctx); err != nil {
				t.Fatal(err)
			}
			start := make(chan struct{})
			results := make(chan error, 9)
			for worker := range 8 {
				go func() {
					<-start
					for range 150 {
						var err error
						switch worker % 3 {
						case 0:
							_, _, err = f.session.conn.Container(ctx, "owned")
						case 1:
							_, err = f.session.post(ctx, "owned", "object", nil)
						case 2:
							_, err = f.session.SLOManifest(ctx, "owned", "object")
						}
						if err != nil {
							results <- err
							return
						}
					}
					results <- nil
				}()
			}
			go func() {
				<-start
				for range 150 {
					if err := f.session.Authenticate(ctx); err != nil {
						results <- err
						return
					}
				}
				results <- nil
			}()
			close(start)
			for range 9 {
				if err := <-results; err != nil {
					t.Error(err)
				}
			}
			f.check(t, 1200, 151)
		})
	}
}

type pairingRoundTrip func(*http.Request) (*http.Response, error)

func (f pairingRoundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestPreparedLibraryRequestSurvivesAuthentication(t *testing.T) {
	f := newPairingFixture(t, "origin", nil)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	t.Cleanup(unblock)
	inner := f.session.conn.Transport
	f.session.conn.Transport = pairingRoundTrip(func(r *http.Request) (*http.Response, error) {
		if strings.HasSuffix(r.URL.Path, "/held") {
			close(entered)
			select {
			case <-release:
			case <-r.Context().Done():
				return nil, r.Context().Err()
			}
		}
		return inner.RoundTrip(r)
	})
	if err := f.session.Authenticate(ctx); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, _, err := f.session.conn.Container(ctx, "held")
		done <- err
	}()
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal("request never reached the transport barrier")
	}
	// Authentication and another storage request must finish while the old
	// request is held; a session lock across network I/O would deadlock here.
	if err := f.session.Authenticate(ctx); err != nil {
		t.Fatal(err)
	}
	if _, _, err := f.session.conn.Container(ctx, "fresh"); err != nil {
		t.Fatal(err)
	}
	unblock()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	f.check(t, 2, 2)
}

func TestLibraryPutReplayKeepsPairAndBody(t *testing.T) {
	for _, change := range []string{"origin", "account", "override"} {
		t.Run(change, func(t *testing.T) {
			const body = "fixture payload through a token rollover"
			var received atomic.Int64
			f := newPairingFixture(t, change, func(w http.ResponseWriter, r *http.Request) {
				data, err := io.ReadAll(r.Body)
				if err != nil || string(data) != body || r.Method != http.MethodPut {
					t.Errorf("PUT body=%q method=%s: %v", data, r.Method, err)
				}
				if received.Add(1) == 1 {
					w.WriteHeader(http.StatusUnauthorized)
					return
				}
				w.Header().Set("Etag", "fixture-etag")
				w.WriteHeader(http.StatusCreated)
			})
			etag, err := f.session.PutObject(context.Background(), "owned", "object",
				strings.NewReader(body), "text/plain", nil)
			if err != nil || etag != "fixture-etag" {
				t.Fatalf("PUT replay etag=%q: %v", etag, err)
			}
			f.check(t, 2, 2)
		})
	}
}

func TestLibraryTransportRetryReselectsPair(t *testing.T) {
	f := newPairingFixture(t, "origin", nil)
	inner := f.session.conn.Transport
	var attempts atomic.Int64
	f.session.conn.Transport = pairingRoundTrip(func(r *http.Request) (*http.Response, error) {
		if strings.HasSuffix(r.URL.Path, "/owned") && attempts.Add(1) == 1 {
			// A different caller can finish authentication before the wire
			// library retries this still-authenticated GET/HEAD operation.
			if err := f.session.Authenticate(r.Context()); err != nil {
				return nil, err
			}
			return nil, errors.New("fixture transport interruption")
		}
		return inner.RoundTrip(r)
	})
	if _, _, err := f.session.conn.Container(context.Background(), "owned"); err != nil {
		t.Fatal(err)
	}
	if attempts.Load() != 2 {
		t.Fatalf("transport attempts=%d, want 2", attempts.Load())
	}
	f.check(t, 1, 2)
}

func TestLibraryLazyPagesReselectPair(t *testing.T) {
	for _, kind := range []string{"containers", "objects"} {
		t.Run(kind, func(t *testing.T) {
			f := newPairingFixture(t, "origin", func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if r.URL.Query().Get("marker") == "" {
					fmt.Fprint(w, `[{"name":"first","count":0,"bytes":0}]`)
				} else {
					fmt.Fprint(w, `[]`)
				}
			})
			ctx := context.Background()
			rows := 0
			afterRow := func(err error) {
				t.Helper()
				if err != nil {
					t.Fatal(err)
				}
				rows++
				f.check(t, 1, 1)
				if err := f.session.Authenticate(ctx); err != nil {
					t.Fatal(err)
				}
			}
			if kind == "containers" {
				for _, err := range f.session.Containers(ctx, ListOptions{PageSize: 1}) {
					afterRow(err)
				}
			} else {
				for _, err := range f.session.Objects(ctx, "owned", ObjectListOptions{PageSize: 1}) {
					afterRow(err)
				}
			}
			if rows != 1 {
				t.Fatalf("rows=%d, want 1", rows)
			}
			f.check(t, 2, 2)
		})
	}
}

func TestLibraryStreamReopenReselectsPair(t *testing.T) {
	f := newPairingFixture(t, "account", func(w http.ResponseWriter, r *http.Request) {
		body := "abcdef"
		if r.Header.Get("Range") == "bytes=3-" {
			body = body[3:]
			w.Header().Set("Content-Range", "bytes 3-5/6")
			w.WriteHeader(http.StatusPartialContent)
		}
		fmt.Fprint(w, body)
	})
	ctx := context.Background()
	file, _, err := f.session.conn.ObjectOpen(ctx, "owned", "object", false, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if err := f.session.Authenticate(ctx); err != nil {
		t.Fatal(err)
	}
	if data, err := io.ReadAll(file); err != nil || string(data) != "abcdef" {
		t.Fatalf("already opened body=%q: %v", data, err)
	}
	if _, err := file.Seek(ctx, 3, io.SeekStart); err != nil {
		t.Fatal(err)
	}
	if data, err := io.ReadAll(file); err != nil || string(data) != "def" {
		t.Fatalf("reopened body=%q: %v", data, err)
	}
	f.check(t, 2, 2)
}
