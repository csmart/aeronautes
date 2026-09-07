package swiftcontrol

import (
	"maps"
	"reflect"
	"testing"
)

func TestMetadataValidation(t *testing.T) {
	cases := []struct {
		name    string
		meta    map[string]string
		emptyOK bool
		wantErr bool
	}{
		{"nil working copy", nil, false, false},
		{"empty working copy", map[string]string{}, false, false},
		{"empty object value", map[string]string{"Empty": ""}, true, false},
		{"empty container value", map[string]string{"Empty": ""}, false, true},
		{"literal special names", map[string]string{"constructor": "value", "toString": "value"}, false, false},
		{"case duplicates", map[string]string{"Owner": "one", "owner": "two"}, true, true},
		{"empty key", map[string]string{"": "value"}, true, true},
		{"underscore key", map[string]string{"a_b": "value"}, true, true},
		{"prototype key", map[string]string{"__proto__": "value"}, true, true},
		{"colon key", map[string]string{"a:b": "value"}, true, true},
		{"slash key", map[string]string{"a/b": "value"}, true, true},
		{"space key", map[string]string{"a b": "value"}, true, true},
		{"tab key", map[string]string{"a\tb": "value"}, true, true},
		{"unicode key", map[string]string{"雪": "value"}, true, true},
		{"leading value space", map[string]string{"Key": " value"}, true, true},
		{"trailing value space", map[string]string{"Key": "value "}, true, true},
		{"leading value tab", map[string]string{"Key": "\tvalue"}, true, true},
		{"trailing value tab", map[string]string{"Key": "value\t"}, true, true},
		{"interior whitespace", map[string]string{"Key": "two  spaces\tand a tab"}, false, false},
		{"unicode whitespace", map[string]string{"Key": "\u00a0雪\u2003"}, false, false},
		{"literal percent text", map[string]string{"Key": "%2F + 100%"}, false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			before := maps.Clone(tc.meta)
			err := validateMetadata(tc.meta, tc.emptyOK)
			if (err != nil) != tc.wantErr {
				t.Fatalf("validation error=%v, want error=%v", err, tc.wantErr)
			}
			if !reflect.DeepEqual(tc.meta, before) {
				t.Fatal("validation changed the working copy")
			}
		})
	}
	t.Run("all supported token punctuation", func(t *testing.T) {
		key := "!#$%&'*+-.^`|~AZaz09"
		if err := validateMetadata(map[string]string{key: "value"}, false); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("remaining header control bytes", func(t *testing.T) {
		for _, value := range []byte{0, 1, 2, 3, 4, 5, 6, 7, 8, 10, 11, 12, 13, 14, 15, 16, 17, 18, 19, 20, 21, 22, 23, 24, 25, 26, 27, 28, 29, 30, 31, 127} {
			if err := validateMetadata(map[string]string{"Key": "a" + string(value) + "b"}, true); err == nil {
				t.Errorf("HTTP control byte %#x was accepted", value)
			}
		}
	})
}
