package swiftcontrol

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

const listingResponseRow = `{"name":"a%2F +日本語","count":1,"bytes":4,"last_modified":"2026-08-28T10:00:00.123456Z","content_type":"text/plain","hash":"object-hash","slo_etag":"slo-hash"}`

func TestListingResponseCancellation(t *testing.T) {
	for _, kind := range []string{"containers", "objects"} {
		t.Run(kind, func(t *testing.T) {
			flushed, release := make(chan struct{}), make(chan struct{})
			defer close(release)
			var requests atomic.Int32
			session := listingResponseSession(t, func(w http.ResponseWriter, r *http.Request) {
				if requests.Add(1) != 1 {
					t.Error("cancelled listing retried")
					http.Error(w, "unexpected retry", http.StatusInternalServerError)
					return
				}
				body := "[" + listingResponseRow + "]"
				w.Header().Set("Content-Length", strconv.Itoa(len(body)+1))
				fmt.Fprint(w, body)
				w.(http.Flusher).Flush()
				close(flushed)
				select {
				case <-r.Context().Done():
				case <-release:
				}
			})
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			type result struct {
				rows int
				err  error
			}
			done := make(chan result, 1)
			go func() {
				var got result
				if kind == "containers" {
					for _, err := range session.Containers(ctx, ListOptions{PageSize: 3}) {
						if err != nil {
							got.err = err
							break
						}
						got.rows++
					}
				} else {
					for _, err := range session.Objects(ctx, "owned", ObjectListOptions{Deep: true, PageSize: 3}) {
						if err != nil {
							got.err = err
							break
						}
						got.rows++
					}
				}
				done <- got
			}()
			select {
			case <-flushed:
			case <-time.After(3 * time.Second):
				t.Fatal("listing response did not reach the held body boundary")
			}
			cancel()
			select {
			case got := <-done:
				if got.rows != 0 || !errors.Is(got.err, context.Canceled) {
					t.Fatalf("cancelled incomplete response: rows=%d error=%v", got.rows, got.err)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("listing did not stop after cancellation")
			}
			if requests.Load() != 1 {
				t.Fatalf("cancelled listing made %d requests", requests.Load())
			}
		})
	}
}

func listingResponseSession(t *testing.T, handler http.HandlerFunc) *Session {
	t.Helper()
	auth := &fakeAuth{user: "acct:user", key: "fixture-key", storagePath: "/v1/AUTH_acct"}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/auth/v1.0" {
			auth.handle(w, r)
			return
		}
		handler(w, r)
	}))
	t.Cleanup(server.Close)
	auth.serverURL = server.URL
	session, err := New(Config{AuthURL: server.URL + "/auth/v1.0", Username: "acct:user", APIKey: "fixture-key"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(session.raw.CloseIdleConnections)
	return session
}

func TestListingResponsesRequireCompletePages(t *testing.T) {
	type responseCase struct {
		name      string
		body      string
		status    int
		partial   bool
		truncated bool
		valid     bool
		rows      int
	}
	good := "[" + listingResponseRow + "]"
	for _, kind := range []string{"containers", "objects"} {
		t.Run(kind, func(t *testing.T) {
			cases := []responseCase{
				{name: "valid", body: good, valid: true, rows: 1},
				{name: "whitespace", body: " \r\n\t" + good + " \t\r\n", valid: true, rows: 1},
				{name: "empty-array", body: "[]", valid: true},
				{name: "null", body: "null"},
				{name: "empty-body", body: ""},
				{name: "object", body: "{}"},
				{name: "boolean", body: "true"},
				{name: "number", body: "42"},
				{name: "string", body: `"wrong"`},
				{name: "null-row", body: "[null]"},
				{name: "scalar-row", body: "[42]"},
				{name: "invalid-later-row", body: "[" + listingResponseRow + "," + strings.Replace(listingResponseRow, `"bytes":4`, `"bytes":"invalid"`, 1) + "]"},
				{name: "null-later-row", body: "[" + listingResponseRow + ",null]"},
				{name: "invalid-json", body: "[" + listingResponseRow},
				{name: "second-array", body: good + " []"},
				{name: "second-null", body: good + " null"},
				{name: "trailing-byte", body: good + " x"},
				{name: "truncated-body", body: good, truncated: true},
				{name: "truncated-empty-array", body: "[]", truncated: true},
				{name: "partial-status", body: good, status: http.StatusPartialContent},
				{name: "empty-no-content", status: http.StatusNoContent},
				{name: "content-range", body: good, partial: true},
				{name: "invalid-bytes", body: strings.Replace(good, `"bytes":4`, `"bytes":"invalid"`, 1)},
				{name: "fractional-bytes", body: strings.Replace(good, `"bytes":4`, `"bytes":1.5`, 1)},
				{name: "overflow-bytes", body: strings.Replace(good, `"bytes":4`, `"bytes":9223372036854775808`, 1)},
			}
			if kind == "objects" {
				cases = append(cases,
					responseCase{name: "invalid-date", body: strings.Replace(good, "2026-08-28T10:00:00.123456Z", "invalid", 1)},
					responseCase{name: "invalid-date-fraction", body: strings.Replace(good, ".123456Z", ".invalidZ", 1)},
				)
			}
			for _, tc := range cases {
				t.Run(tc.name, func(t *testing.T) {
					var requests atomic.Int32
					session := listingResponseSession(t, func(w http.ResponseWriter, r *http.Request) {
						requests.Add(1)
						wantPath := "/v1/AUTH_acct"
						if kind == "objects" {
							wantPath += "/owned"
						}
						if r.Method != http.MethodGet || r.URL.Path != wantPath || r.URL.Query().Get("format") != "json" {
							t.Errorf("unexpected listing request: %s %s", r.Method, r.URL)
							w.WriteHeader(http.StatusBadRequest)
							return
						}
						w.Header().Set("Content-Type", "application/json")
						if r.URL.Query().Get("marker") != "" {
							fmt.Fprint(w, "[]")
							return
						}
						if tc.partial {
							w.Header().Set("Content-Range", "")
						}
						if tc.truncated {
							w.Header().Set("Content-Length", strconv.Itoa(len(tc.body)+20))
						}
						status := tc.status
						if status == 0 {
							status = http.StatusOK
						}
						w.WriteHeader(status)
						fmt.Fprint(w, tc.body)
					})
					var rows int
					var failure error
					if kind == "containers" {
						for row, err := range session.Containers(context.Background(), ListOptions{PageSize: 3}) {
							if err != nil {
								failure = err
								break
							}
							rows++
							if tc.valid && (row.Name != "a%2F +日本語" || row.Count != 1 || row.Bytes != 4) {
								t.Errorf("container mapping changed: %+v", row)
							}
						}
					} else {
						for entry, err := range session.Objects(context.Background(), "owned", ObjectListOptions{PageSize: 3, Deep: true}) {
							if err != nil {
								failure = err
								break
							}
							rows++
							if tc.valid {
								row, ok := entry.(ObjectEntry)
								if !ok || row.Name != "a%2F +日本語" || row.Bytes != 4 || row.ContentType != "text/plain" || row.ETag != "object-hash" || !row.SLOListed || row.SLOETag != "slo-hash" || row.LastModified.Format("2006-01-02T15:04:05.999999999Z") != "2026-08-28T10:00:00Z" {
									t.Errorf("object mapping changed: %+v", entry)
								}
							}
						}
					}
					if tc.valid {
						if failure != nil || rows != tc.rows {
							t.Fatalf("valid listing: rows=%d error=%v, want %d rows", rows, failure, tc.rows)
						}
					} else if failure == nil || rows != 0 {
						t.Fatalf("malformed listing was accepted: rows=%d error=%v", rows, failure)
					}
					if !tc.valid && requests.Load() != 1 {
						t.Fatalf("malformed response caused %d requests, want one", requests.Load())
					}
				})
			}
		})
	}
}
