package swiftcontrol

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"strconv"
	"sync"
	"testing"
	"time"
)

func TestListingContinuationContract(t *testing.T) {
	type listingCase struct {
		name, prefix, marker string
		pages                [][]string
		pseudo               map[string]bool
		want, markers        []string
		wantPrefixes         []string
		limit                int
		delimited            bool
		wantError            bool
		cancelBefore         bool
		cancelAfter          int
		breakAfter           int
		failOnPage           int
	}
	for _, kind := range []string{"containers", "objects"} {
		t.Run(kind, func(t *testing.T) {
			cases := []listingCase{
				{name: "short-pages", pages: [][]string{{"a"}, {"b"}, {}}, want: []string{"a", "b"}, markers: []string{"", "a", "b"}},
				{name: "full-pages", limit: 2, pages: [][]string{{"a", "b"}, {"c", "d"}, {}}, want: []string{"a", "b", "c", "d"}, markers: []string{"", "b", "d"}},
				{name: "empty", pages: [][]string{{}}, markers: []string{""}},
				{name: "default-limit", limit: -1, pages: [][]string{{"a"}, {}}, want: []string{"a"}, markers: []string{"", "a"}},
				{name: "starting-marker", marker: "a", pages: [][]string{{"b"}, {"c"}, {}}, want: []string{"b", "c"}, markers: []string{"a", "b", "c"}},
				{name: "literal-scope", prefix: " +%2F日本語/", marker: " +%2F日本語/A", pages: [][]string{{" +%2F日本語/a"}, {" +%2F日本語/é"}, {}}, want: []string{" +%2F日本語/a", " +%2F日本語/é"}, markers: []string{" +%2F日本語/A", " +%2F日本語/a", " +%2F日本語/é"}},
				{name: "byte-order", limit: 5, pages: [][]string{{"A", "a", "e\u0301", "é", "日本語"}, {}}, want: []string{"A", "a", "e\u0301", "é", "日本語"}, markers: []string{"", "日本語"}},
				{name: "empty-name", pages: [][]string{{""}}, markers: []string{""}, wantError: true},
				{name: "later-empty-name", pages: [][]string{{"a", ""}}, markers: []string{""}, wantError: true},
				{name: "duplicate-in-page", pages: [][]string{{"a", "a"}}, markers: []string{""}, wantError: true},
				{name: "backward-in-page", pages: [][]string{{"b", "a"}}, markers: []string{""}, wantError: true},
				{name: "equal-starting-marker", marker: "b", pages: [][]string{{"b"}}, markers: []string{"b"}, wantError: true},
				{name: "before-starting-marker", marker: "b", pages: [][]string{{"a"}}, markers: []string{"b"}, wantError: true},
				{name: "repeat-page", limit: 1, pages: [][]string{{"b"}, {"b"}}, want: []string{"b"}, markers: []string{"", "b"}, wantError: true},
				{name: "backward-page", limit: 1, pages: [][]string{{"b"}, {"a"}}, want: []string{"b"}, markers: []string{"", "b"}, wantError: true},
				{name: "later-page-disorder", pages: [][]string{{"a"}, {"c", "b"}}, want: []string{"a"}, markers: []string{"", "a"}, wantError: true},
				{name: "outside-prefix", prefix: "scope/", pages: [][]string{{"outside"}}, markers: []string{""}, wantError: true},
				{name: "later-row-outside-prefix", prefix: "scope/", pages: [][]string{{"scope/a", "z-outside"}}, markers: []string{""}, wantError: true},
				{name: "early-break", pages: [][]string{{"a", "b"}, {"c"}, {}}, want: []string{"a"}, markers: []string{""}, breakAfter: 1},
				{name: "cancel-before-fetch", cancelBefore: true, wantError: true},
				{name: "cancel-mid-page", pages: [][]string{{"a", "b"}, {}}, want: []string{"a"}, markers: []string{""}, cancelAfter: 1, wantError: true},
				{name: "cancel-before-next-fetch", limit: 1, pages: [][]string{{"a"}, {"b"}, {}}, want: []string{"a"}, markers: []string{""}, cancelAfter: 1, wantError: true},
				{name: "later-response-error", pages: [][]string{{"a"}, {"b"}}, want: []string{"a"}, markers: []string{"", "a"}, failOnPage: 2, wantError: true},
			}
			if kind == "objects" {
				cases = append(cases,
					listingCase{name: "deep-pseudo-row", pages: [][]string{{"folder/"}}, pseudo: map[string]bool{"folder/": true}, markers: []string{""}, wantError: true},
					listingCase{name: "deep-later-pseudo-row", pages: [][]string{{"a", "folder/"}}, pseudo: map[string]bool{"folder/": true}, markers: []string{""}, wantError: true},
					listingCase{name: "delimiter-prefix", delimited: true, prefix: "scope/", pages: [][]string{{"scope/a", "scope/folder/"}, {}}, pseudo: map[string]bool{"scope/folder/": true}, want: []string{"scope/a", "scope/folder/"}, wantPrefixes: []string{"scope/folder/"}, markers: []string{"", "scope/folder/"}},
					listingCase{name: "self-only-short-page", delimited: true, prefix: "scope/", pages: [][]string{{"scope/"}, {"scope/child"}, {}}, want: []string{"scope/child"}, markers: []string{"", "scope/", "scope/child"}},
					listingCase{name: "deep-self-row", prefix: "scope/", pages: [][]string{{"scope/"}, {"scope/child"}, {}}, want: []string{"scope/", "scope/child"}, markers: []string{"", "scope/", "scope/child"}},
					listingCase{name: "self-before-starting-marker", delimited: true, prefix: "scope/", marker: "scope/z", pages: [][]string{{"scope/"}}, markers: []string{"scope/z"}, wantError: true},
				)
			}
			for _, tc := range cases {
				t.Run(tc.name, func(t *testing.T) {
					ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
					defer cancel()
					var mu sync.Mutex
					var markers []string
					limit := tc.limit
					if limit == 0 {
						limit = 3
					}
					wantLimit := limit
					if wantLimit < 0 {
						wantLimit = 10000
					}
					session := listingResponseSession(t, func(w http.ResponseWriter, r *http.Request) {
						query := r.URL.Query()
						mu.Lock()
						index := len(markers)
						markers = append(markers, query.Get("marker"))
						mu.Unlock()
						delimiter := ""
						if kind == "objects" && tc.delimited {
							delimiter = "/"
						}
						if r.Method != http.MethodGet || query.Get("format") != "json" || query.Get("prefix") != tc.prefix || query.Get("delimiter") != delimiter || query.Get("limit") != strconv.Itoa(wantLimit) {
							t.Errorf("listing request lost its contract: method=%s query=%v", r.Method, query)
						}
						if index >= len(tc.pages) {
							http.Error(w, "unexpected extra listing request", http.StatusBadRequest)
							return
						}
						if tc.failOnPage == index+1 {
							fmt.Fprint(w, "[")
							return
						}
						rows := make([]map[string]any, 0, len(tc.pages[index]))
						for _, name := range tc.pages[index] {
							if tc.pseudo[name] {
								rows = append(rows, map[string]any{"subdir": name})
							} else {
								rows = append(rows, map[string]any{"name": name, "bytes": 1, "count": 1})
							}
						}
						if err := json.NewEncoder(w).Encode(rows); err != nil {
							t.Errorf("write controlled listing page: %v", err)
						}
					})
					if tc.cancelBefore {
						cancel()
					}
					var names, prefixes []string
					var lastErr error
					consume := func(name string, prefix bool, err error) bool {
						if err != nil {
							lastErr = err
							return false
						}
						names = append(names, name)
						if prefix {
							prefixes = append(prefixes, name)
						}
						if len(names) == tc.cancelAfter {
							cancel()
						}
						return tc.breakAfter == 0 || len(names) < tc.breakAfter
					}
					if kind == "containers" {
						for entry, err := range session.Containers(ctx, ListOptions{PageSize: limit, Prefix: tc.prefix, Marker: tc.marker}) {
							if !consume(entry.Name, false, err) {
								break
							}
						}
					} else {
						for entry, err := range session.Objects(ctx, "owned", ObjectListOptions{PageSize: limit, Prefix: tc.prefix, Marker: tc.marker, Deep: !tc.delimited}) {
							var name string
							var prefix bool
							if err == nil {
								switch entry := entry.(type) {
								case ObjectEntry:
									name = entry.Name
								case PrefixEntry:
									name, prefix = entry.Prefix, true
								default:
									t.Fatalf("unexpected listing row type %T", entry)
								}
							}
							if !consume(name, prefix, err) {
								break
							}
						}
					}
					mu.Lock()
					gotMarkers := append([]string(nil), markers...)
					mu.Unlock()
					if !reflect.DeepEqual(names, tc.want) || !reflect.DeepEqual(prefixes, tc.wantPrefixes) {
						t.Errorf("published names=%q prefixes=%q, want names=%q prefixes=%q", names, prefixes, tc.want, tc.wantPrefixes)
					}
					if !reflect.DeepEqual(gotMarkers, tc.markers) {
						t.Errorf("requested markers=%q, want %q", gotMarkers, tc.markers)
					}
					if (lastErr != nil) != tc.wantError {
						t.Errorf("listing error=%v, want error=%v", lastErr, tc.wantError)
					}
					if (tc.cancelBefore || tc.cancelAfter > 0) && !errors.Is(lastErr, context.Canceled) {
						t.Errorf("cancellation not preserved: %v", lastErr)
					}
					if errors.Is(lastErr, context.DeadlineExceeded) {
						t.Fatal("controlled listing exhausted its test deadline")
					}
				})
			}
		})
	}
}
