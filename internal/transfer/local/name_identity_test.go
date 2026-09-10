package local

import (
	"strings"
	"testing"
	"unicode"
)

// The two contracts are separate and must not be conflated.
//
// canonicalDecompose is a strict canonical helper: two names share its
// output exactly when they are canonically equivalent.
//
// collisionIdentity is deliberately wider. It is the established Unicode
// lowercase relation together with canonical equivalence, and nothing
// else.

const (
	// Every non-precomposed fixture is written as an explicit escape.
	// A decomposed sequence typed as a literal is normalized by editors
	// and tools, which would silently compare a string with itself.
	combiningDot      = "\u0307" // combining dot above
	combiningAcute    = "\u0301"
	combiningGrave    = "\u0300"
	combiningOgonek   = "\u0328"
	combiningDotBelow = "\u0323"
	graphemeJoiner    = "\u034f"
	dottedCapitalI    = "\u0130"
	ohmSign           = "\u2126"
	capitalOmega      = "\u03a9"
	hangulGa          = "\uac00"
	hangulGaJamo      = "\u1100\u1161"
	sDotBelowAbove    = "\u1e69"
	ligatureFi        = "\ufb01"
)

func TestCanonicalDecomposeMatchesCanonicalEquivalence(t *testing.T) {
	for _, c := range []struct {
		name       string
		a, b       string
		equivalent bool
	}{
		{"precomposed_vs_decomposed", "étude", "e" + combiningAcute + "tude", true},
		{"identical_long_run", "x" + strings.Repeat(combiningAcute, 31), "x" + strings.Repeat(combiningAcute, 31), true},
		{"canonical_reordering", "q" + combiningOgonek + combiningAcute, "q" + combiningAcute + combiningOgonek, true},
		{"hangul_syllable", hangulGa, hangulGaJamo, true},
		{"singleton_ohm", ohmSign, capitalOmega, true},
		{"nested_decomposition", sDotBelowAbove, "s" + combiningDotBelow + combiningDot, true},
		{"dotted_capital_i", dottedCapitalI, "I" + combiningDot, true},
		{"distinct_names", "resume", "résumé", false},
		{"same_class_order_is_significant", "q" + combiningAcute + combiningGrave, "q" + combiningGrave + combiningAcute, false},
		{"compatibility_is_excluded", ligatureFi, "fi", false},
		{"real_joiner_is_preserved", "a" + graphemeJoiner + "b", "ab", false},
		{"long_run_vs_joiner_broken_run", "x" + strings.Repeat(combiningAcute, 31),
			"x" + strings.Repeat(combiningAcute, 30) + graphemeJoiner + combiningAcute, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			if c.a == c.b && c.name != "identical_long_run" {
				t.Fatalf("fixture compares a string with itself: %q", c.a)
			}
			if got := canonicalDecompose(c.a) == canonicalDecompose(c.b); got != c.equivalent {
				t.Errorf("canonical equivalence of %q and %q: got %v want %v", c.a, c.b, got, c.equivalent)
			}
		})
	}
}

// The stream-safe normal forms equate the last pair above, which is why
// this package does not compare them.
func TestCanonicalDecomposeRejectsStreamSafeConflation(t *testing.T) {
	long := "x" + strings.Repeat(combiningAcute, 31)
	broken := "x" + strings.Repeat(combiningAcute, 30) + graphemeJoiner + combiningAcute
	if long == broken {
		t.Fatal("fixtures are not distinct")
	}
	if canonicalDecompose(long) == canonicalDecompose(broken) {
		t.Error("a run broken by a real joiner was treated as canonically equivalent")
	}
	if strings.Contains(canonicalDecompose(long), graphemeJoiner) {
		t.Error("the helper inserted a grapheme joiner of its own")
	}
}

func TestCollidesFoldsCaseAndFormAndNothingElse(t *testing.T) {
	for _, c := range []struct {
		name     string
		a, b     string
		collides bool
		reason   string
	}{
		{"ascii_case", "Foo", "foo", true, "established case relation"},
		{"unicode_case", "Étude", "étude", true, "established case relation"},
		{"dotted_i_case", "I" + combiningDot, "i" + combiningDot, true, "established case relation"},
		{"dotted_i_canonical", dottedCapitalI, "I" + combiningDot, true, "canonical equivalence"},
		{"normalization_twins", "étude", "e" + combiningAcute + "tude", true, "canonical equivalence"},
		{"case_and_form_together", "Étude", "e" + combiningAcute + "tude", true, "both relations"},
		{"distinct_names", "resume", "résumé", false, "genuinely distinct"},
		{"sharp_s_is_not_folded", "ß", "ss", false, "lowercase is not a full case folding"},
		{"compatibility_is_excluded", ligatureFi, "fi", false, "compatibility equivalence is out of scope"},
		{"same_class_order", "q" + combiningAcute + combiningGrave, "q" + combiningGrave + combiningAcute, false, "same class does not reorder"},
		{"long_run_vs_joiner", "x" + strings.Repeat(combiningAcute, 31),
			"x" + strings.Repeat(combiningAcute, 30) + graphemeJoiner + combiningAcute, false, "not canonically equivalent"},
		{"dotted_i_vs_plain_i", dottedCapitalI, "i", true, "the lowercase relation alone carries this pair"},
		{"mixed_case_and_form", "\u00c9tude", "e" + combiningAcute + "tude", true, "the caseless relation alone carries this pair"},
		{"mixed_reverse", "e" + combiningAcute + "TUDE", "\u00e9tude", true, "the caseless relation, other direction"},
		{"plain_i_vs_i_dot", "i", "i" + combiningDot, false, "neither relation; the union is not closed"},
	} {
		t.Run(c.name, func(t *testing.T) {
			if c.a == c.b {
				t.Fatalf("fixture compares a string with itself: %q", c.a)
			}
			if got := collides(c.a, c.b); got != c.collides {
				t.Errorf("collision of %q and %q: got %v want %v (%s)", c.a, c.b, got, c.collides, c.reason)
			}
		})
	}
}

// Every pair the established relation refuses must still be refused, and
// this walks the whole rune range rather than a chosen list, because a
// single composed key loses exactly one of these pairs.
func TestCollidesKeepsEveryEstablishedRefusal(t *testing.T) {
	changed, lost := 0, 0
	for r := rune(0); r <= unicode.MaxRune; r++ {
		name := string(r)
		lower := strings.ToLower(name)
		if lower == name {
			continue
		}
		changed++
		if !collides(name, lower) {
			lost++
			if lost <= 5 {
				t.Errorf("established refusal lost for %q (%U) against %q", name, r, lower)
			}
		}
	}
	if changed == 0 {
		t.Fatal("the rune walk found no lowercase mappings at all")
	}
	t.Logf("runes whose lowercase differs: %d, refusals lost: %d", changed, lost)
}

// The caseless relation alone would lose one of those pairs, which is why
// the established relation is kept beside it rather than replaced.
func TestCaselessRelationAloneWouldLoseTheDottedCapitalI(t *testing.T) {
	if caselessIdentity(dottedCapitalI) == caselessIdentity("i") {
		t.Fatal("fixture no longer demonstrates the gap the lowercase relation covers")
	}
	if !collides(dottedCapitalI, "i") {
		t.Error("the pair the lowercase relation carries is no longer refused")
	}
}
