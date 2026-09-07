package swifttest_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"testing"

	"github.com/csmart/aeronautes/internal/swifttest"
)

func TestDelimiterMarkerHTTP(t *testing.T) {
	mock := swifttest.New("test:tester", "testing")
	mock.AddContainer("markers", "standard")
	for _, name := range []string{"scope/", "scope/a/", "scope/a/one", "scope/a/two", "scope/b", "scope/b/three", "scope/z", "outside", "repeated//", "repeated//a/child", "repeated//z", " 雪%2F/", " 雪%2F/a/child", " 雪%2F/z"} {
		mock.PutObject("markers", name, swifttest.Object{Data: []byte(name), Bytes: int64(len(name))})
	}
	token := literalMockToken(t, mock)
	cases := []struct {
		label, prefix, marker, delimiter string
		want                             []string
	}{
		{"initial own object", "scope/", "", "/", []string{"object:scope/", "prefix:scope/a/", "object:scope/b", "prefix:scope/b/", "object:scope/z"}},
		{"after own marker", "scope/", "scope/", "/", []string{"prefix:scope/a/", "object:scope/b", "prefix:scope/b/", "object:scope/z"}},
		{"after child group", "scope/", "scope/a/", "/", []string{"object:scope/b", "prefix:scope/b/", "object:scope/z"}},
		{"inside child group", "scope/", "scope/a/o", "/", []string{"prefix:scope/a/", "object:scope/b", "prefix:scope/b/", "object:scope/z"}},
		{"inside nested group", "scope/", "scope/a/one/", "/", []string{"prefix:scope/a/", "object:scope/b", "prefix:scope/b/", "object:scope/z"}},
		{"marker before partial filter", "scope/b", "scope/", "/", []string{"object:scope/b", "prefix:scope/b/"}},
		{"root after partial group", "", "scope", "/", []string{"prefix:scope/"}},
		{"root after group interior", "", "scope/a", "/", []string{"prefix:scope/"}},
		{"deep ignores groups", "scope/", "scope/a/", "", []string{"object:scope/a/one", "object:scope/a/two", "object:scope/b", "object:scope/b/three", "object:scope/z"}},
		{"repeated separator own marker", "repeated//", "repeated//", "/", []string{"prefix:repeated//a/", "object:repeated//z"}},
		{"literal unicode own marker", " 雪%2F/", " 雪%2F/", "/", []string{"prefix: 雪%2F/a/", "object: 雪%2F/z"}},
	}
	for _, tc := range cases {
		t.Run(tc.label, func(t *testing.T) {
			query := url.Values{"format": {"json"}, "prefix": {tc.prefix}, "marker": {tc.marker}, "delimiter": {tc.delimiter}, "limit": {"100"}}
			request := httptest.NewRequest(http.MethodGet, "/v1/AUTH_test/markers?"+query.Encode(), nil)
			request.Header.Set("X-Auth-Token", token)
			response := httptest.NewRecorder()
			mock.ServeHTTP(response, request)
			if response.Code != http.StatusOK {
				t.Fatalf("listing HTTP status = %d", response.Code)
			}
			var rows []struct{ Name, Subdir string }
			if err := json.Unmarshal(response.Body.Bytes(), &rows); err != nil {
				t.Fatal(err)
			}
			var got []string
			for _, row := range rows {
				if (row.Name == "") == (row.Subdir == "") {
					t.Fatalf("ambiguous listing row: %+v", row)
				}
				if row.Name != "" {
					got = append(got, "object:"+row.Name)
				} else {
					got = append(got, "prefix:"+row.Subdir)
				}
			}
			if !slices.Equal(got, tc.want) {
				t.Fatalf("prefix %q marker %q: got %q, want %q", tc.prefix, tc.marker, got, tc.want)
			}
		})
	}
}
