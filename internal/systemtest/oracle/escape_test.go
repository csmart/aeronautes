package oracle

import "testing"

// TestEscapeControlBytes: percent-encoding must be two hex digits per
// byte — a control byte < 0x10 needs a leading zero (grok war-room:
// "%A" for 0x0A would 404 on Swift). Pure unit, no SAIO.
func TestEscapeControlBytes(t *testing.T) {
	cases := map[string]string{
		"plain.txt": "plain.txt",
		"a b":       "a%20b",
		"tab\there": "tab%09here",
		"nl\nhere":  "nl%0Ahere",
		"a/b/c":     "a/b/c",    // slashes are path separators, kept
		"~keep-._":  "~keep-._", // unreserved
		"pct%sign":  "pct%25sign",
	}
	for in, want := range cases {
		if got := Escape(in); got != want {
			t.Errorf("Escape(%q) = %q, want %q", in, got, want)
		}
	}
}
