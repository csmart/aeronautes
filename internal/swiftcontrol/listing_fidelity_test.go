package swiftcontrol

import (
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
)

func TestListingIdentityDecoding(t *testing.T) {
	cases := []struct {
		name, literal, decoded string
	}{
		{name: "invalid-byte", literal: "\"b\xff\""},
		{name: "stray-continuation", literal: "\"b\x80\""},
		{name: "overlong", literal: "\"b\xc0\xaf\""},
		{name: "encoded-surrogate", literal: "\"b\xed\xa0\x80\""},
		{name: "beyond-unicode", literal: "\"b\xf4\x90\x80\x80\""},
		{name: "truncated-rune", literal: "\"b\xe2\x82\""},
		{name: "lone-high", literal: `"b\ud800"`},
		{name: "lone-low", literal: `"b\udc00"`},
		{name: "high-then-text", literal: `"b\ud800x"`},
		{name: "reversed-pair", literal: `"b\udc00\ud800"`},
		{name: "two-high", literal: `"b\ud800\ud800"`},
		{name: "literal-replacement", literal: `"b�"`, decoded: "b�"},
		{name: "escaped-replacement", literal: `"b\ufffd"`, decoded: "b�"},
		{name: "valid-pair", literal: `"b\ud83d\ude80"`, decoded: "b🚀"},
		{name: "literal-pair", literal: `"b🚀"`, decoded: "b🚀"},
		{name: "ordinary-unicode", literal: `"b日本語é"`, decoded: "b日本語é"},
		{name: "combining", literal: `"be\u0301"`, decoded: "be\u0301"},
		{name: "literal-escape", literal: `"b\\ud800"`, decoded: `b\ud800`},
		{name: "nul-newline", literal: `"b\u0000\n"`, decoded: "b\x00\n"},
	}
	for _, kind := range []string{"containers", "objects", "prefixes"} {
		t.Run(kind, func(t *testing.T) {
			for _, tc := range cases {
				t.Run(tc.name, func(t *testing.T) {
					field := "name"
					literal, decoded := tc.literal, tc.decoded
					if kind == "prefixes" {
						field = "subdir"
						literal = literal[:len(literal)-1] + `/"`
						if decoded != "" {
							decoded += "/"
						}
					}
					assertListingIdentity(t, kind, fmt.Sprintf(`{%q:%s,"bytes":2,"count":1}`, field, literal), decoded, kind == "prefixes")
				})
			}
		})
	}
}

func TestListingIdentityMembers(t *testing.T) {
	cases := []struct {
		name, row, decoded string
		prefix             bool
	}{
		{name: "duplicate-name", row: `{"name":"b-one","name":"b-two"}`},
		{name: "escaped-duplicate-name", row: `{"name":"b-one","\u006eame":"b-two"}`},
		{name: "case-name", row: `{"name":"b-one","Name":"b-two"}`},
		{name: "case-name-reversed", row: `{"Name":"b-one","name":"b-two"}`},
		{name: "two-name-aliases", row: `{"NAME":"b-one","Name":"b-two"}`},
		{name: "escaped-case-name", row: `{"name":"b-one","\u004eame":"b-two"}`},
		{name: "same-value-aliases", row: `{"name":"b-one","Name":"b-one"}`},
		{name: "duplicate-subdir", row: `{"name":"b","subdir":"b-one/","subdir":"b-two/"}`},
		{name: "escaped-duplicate-subdir", row: `{"name":"b","subdir":"b-one/","\u0073ubdir":"b-two/"}`},
		{name: "case-subdir", row: `{"name":"b","subdir":"b-one/","SubDir":"b-two/"}`},
		{name: "case-subdir-reversed", row: `{"name":"b","SubDir":"b-one/","subdir":"b-two/"}`},
		{name: "two-subdir-aliases", row: `{"name":"b","SUBDIR":"b-one/","SubDir":"b-two/"}`},
		{name: "unicode-fold-subdir", row: `{"name":"b","subdir":"b-one/","ſubdir":"b-two/"}`},
		{name: "escaped-fold-subdir", row: `{"name":"b","\u017fubdir":"b-one/","subdir":"b-two/"}`},
		{name: "unknown-invalid-byte", row: "{\"name\":\"b\",\"unknown\":\"provider-secret\xff\"}"},
		{name: "unknown-invalid-escape", row: `{"name":"b","unknown":"provider-secret\ud800"}`},
		{name: "unknown-invalid-member", row: "{\"name\":\"b\",\"provider-secret\xff\":1}"},
		{name: "unknown-duplicate", row: `{"name":"b","provider-secret":1,"provider-secret":2}`},
		{name: "nested-invalid-byte", row: "{\"name\":\"b\",\"unknown\":[{\"nested\":\"provider-secret\xff\"}]}"},
		{name: "nested-invalid-member", row: `{"name":"b","unknown":{"provider-secret\udc00":1}}`},
		{name: "nested-duplicate", row: `{"name":"b","unknown":[{"name":"provider-secret","\u006eame":"later"}]}`},
		{name: "single-name-alias", row: `{"Name":"b"}`, decoded: "b"},
		{name: "single-escaped-name", row: `{"\u004eame":"b"}`, decoded: "b"},
		{name: "single-subdir-alias", row: `{"name":"b","SubDir":"b/"}`, decoded: "b", prefix: true},
		{name: "single-fold-subdir", row: `{"name":"b","ſubdir":"b/"}`, decoded: "b", prefix: true},
		{name: "separate-identity-fields", row: `{"name":"b","subdir":"b/"}`, decoded: "b", prefix: true},
		{name: "nested-independent-names", row: `{"name":"b","unknown":[{"name":"one"},{"name":"two"}]}`, decoded: "b"},
		{name: "nested-case-aliases", row: `{"name":"b","unknown":{"name":"one","Name":"two"}}`, decoded: "b"},
	}
	for _, kind := range []string{"containers", "objects", "prefixes"} {
		t.Run(kind, func(t *testing.T) {
			for _, tc := range cases {
				t.Run(tc.name, func(t *testing.T) {
					decoded := tc.decoded
					if tc.prefix && kind != "containers" {
						decoded += "/"
					}
					assertListingIdentity(t, kind, tc.row, decoded, tc.prefix)
				})
			}
		})
	}
}

func assertListingIdentity(t *testing.T, kind, laterRow, decoded string, prefix bool) {
	t.Helper()
	var requests atomic.Int32
	body := `[{"name":"a-control","bytes":1,"count":1},` + laterRow + `]`
	session := listingResponseSession(t, func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.Method != http.MethodGet || r.URL.Query().Get("format") != "json" {
			t.Errorf("unexpected dependent request: %s %s", r.Method, r.URL)
			http.Error(w, "unexpected request", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Query().Get("marker") == "" {
			fmt.Fprint(w, body)
		} else {
			fmt.Fprint(w, "[]")
		}
	})
	var names []string
	var failure error
	var gotPrefix bool
	if kind == "containers" {
		for row, err := range session.Containers(t.Context(), ListOptions{PageSize: 3}) {
			if err != nil {
				failure = err
				break
			}
			names = append(names, row.Name)
		}
	} else {
		for row, err := range session.Objects(t.Context(), "owned", ObjectListOptions{PageSize: 3, Deep: kind == "objects"}) {
			if err != nil {
				failure = err
				break
			}
			switch row := row.(type) {
			case ObjectEntry:
				names = append(names, row.Name)
			case PrefixEntry:
				names = append(names, row.Prefix)
				gotPrefix = true
			default:
				t.Fatalf("unexpected listing row %T", row)
			}
		}
	}
	if kind == "objects" && prefix {
		if failure == nil || !strings.Contains(failure.Error(), "deep listing contains a prefix row") || len(names) != 0 || requests.Load() != 1 {
			t.Errorf("deep listing accepted prefix row: names=%q requests=%d error=%v", names, requests.Load(), failure)
		}
		return
	}
	if decoded == "" {
		if failure == nil || len(names) != 0 || requests.Load() != 1 {
			t.Errorf("LOSSY LISTING ACCEPTED: names=%q requests=%d error=%v; want zero rows, one request and an error", names, requests.Load(), failure)
		}
		if failure != nil && (!strings.Contains(failure.Error(), "listing row requires valid Unicode and unambiguous identity fields") || strings.Contains(failure.Error(), "provider-secret")) {
			t.Errorf("validation diagnostic changed or exposed provider text: %v", failure)
		}
	} else if failure != nil || !reflect.DeepEqual(names, []string{"a-control", decoded}) || requests.Load() != 2 {
		t.Errorf("VALID NAME CHANGED: names=%q requests=%d error=%v", names, requests.Load(), failure)
	} else if kind != "containers" && gotPrefix != prefix {
		t.Errorf("listing entry kind changed: got prefix=%v, want %v", gotPrefix, prefix)
	}
}
