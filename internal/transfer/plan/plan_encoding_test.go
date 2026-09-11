package plan

import "testing"

// Every rune the pinned engine reserves as a substitute or a quote on a
// supported platform routes a name natively, wherever it sits; look-alike
// full-width letters and ordinary punctuation stay on the engine route.
func TestRcloneUnsafeEngineReservedRunes(t *testing.T) {
	for _, c := range []struct {
		name   string
		unsafe bool
	}{
		{"scope/p‛q", true},        // quote rune
		{"‛leading", true},         // quote rune first
		{"scope/s␀t", true},        // NUL picture
		{"scope/s␁t", true},        // C0 control picture
		{"scope/s␟t", true},        // last C0 control picture
		{"scope/s␡t", true},        // DEL picture
		{"scope/t␠", true},         // trailing space picture
		{"scope/␠t", true},         // space picture anywhere
		{"scope/a／b", true},        // full-width slash
		{"scope/a＼b", true},        // full-width backslash
		{"scope/d/．/e", true},      // full-width full stop component
		{"scope/f．", true},         // full-width full stop trailing
		{"scope/f．txt", true},      // full-width full stop inside
		{"scope/a：b", true},        // full-width colon
		{"scope/a？b", true},        // full-width question mark
		{"scope/a＂b", true},        // full-width quotation mark
		{"scope/a＊b", true},        // full-width asterisk
		{"scope/a＜b", true},        // full-width less than
		{"scope/a＞b", true},        // full-width greater than
		{"scope/a｜b", true},        // full-width pipe
		{"scope/plain.txt", false}, // control
		{"scope/Ａbc", false},       // full-width letter is not a substitute
		{"scope/a␢b", false},       // blank symbol is outside the engine's picture range
		{"scope/café", false},      // accented letter
		{"scope/a b", false},       // ordinary space
		{"scope/a.b", false},       // ordinary full stop
		{"scope/‘quoted’", false},  // ordinary curly quotes
	} {
		if got := RcloneUnsafe(c.name); got != c.unsafe {
			t.Errorf("RcloneUnsafe(%q) = %v, want %v", c.name, got, c.unsafe)
		}
	}
}
