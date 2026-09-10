package local

import (
	"sort"
	"strings"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"
)

// Collision identity for selected download names.
//
// A case-insensitive filesystem gives one location to "Foo" and "foo"; a
// normalizing filesystem gives one location to the precomposed and the
// decomposed spelling of one name. Both are the same hazard: a selection
// that promises two paths one target cannot both hold. The identity below
// folds both relations and nothing else, so the whole selection is refused
// before any effect (§27.5, §65.9 item 13, K-XFER-011).
//
// Nothing here rewrites a name. The identity is a comparison key only;
// every selected name, local path, diagnostic and record keeps its exact
// bytes.

// Two relations decide a collision, and neither is closed into the other.
//
// lowercaseIdentity is the relation this package has always used. It is
// left exactly as it was, so every pair refused before this correction is
// still refused by construction.
//
// caselessIdentity decomposes, applies that same lowercase, and
// decomposes again. It subsumes plain canonical equivalence, because
// canonically equivalent names share a decomposition, and it also reaches
// pairs differing in both dimensions at once, such as a precomposed
// upper-case spelling against a decomposed lower-case one.
//
// Neither alone is enough. Used by itself the caseless relation loses the
// precomposed dotted capital I against a plain "i", because Go maps that
// character to "i" with no mark while the decomposition keeps the mark;
// that pair is carried by the lowercase relation. Conversely the
// lowercase relation cannot see normalization at all.
//
// The rule is the pairwise union of the two and deliberately not its
// transitive closure. A plain "i" and "i" followed by a combining dot
// above are therefore permitted together: neither relation holds between
// them, and only a chain through the precomposed character would join
// them.
//
// Both are comparison keys only. Nothing here rewrites a name; every
// selected name, local path, diagnostic and record keeps its exact bytes.

func lowercaseIdentity(rel string) string { return strings.ToLower(rel) }

func caselessIdentity(rel string) string {
	return canonicalDecompose(strings.ToLower(canonicalDecompose(rel)))
}

// collides reports whether two distinct selected names cannot both be
// preserved on a target that ignores case, normalization form, or both.
func collides(a, b string) bool {
	return a != b && (lowercaseIdentity(a) == lowercaseIdentity(b) || caselessIdentity(a) == caselessIdentity(b))
}

// canonicalDecompose returns the canonical decomposition of s with
// canonical ordering applied.
//
// It deliberately does not use norm.NFD directly. That form is
// stream-safe: it breaks a run of more than thirty non-starters with an
// inserted U+034F, so a long combining run and the same run already
// broken by a real U+034F share one normal form although they are not
// canonically equivalent. Comparing those forms would refuse two
// genuinely distinct names, so this walks the canonical decomposition
// tables directly and inserts nothing.
func canonicalDecompose(s string) string {
	if isPlainASCII(s) {
		return s
	}
	var out []rune
	for i := 0; i < len(s); {
		p := norm.NFD.PropertiesString(s[i:])
		size := p.Size()
		if size == 0 {
			// Not a valid sequence the tables recognise; keep the byte's
			// rune as it stands rather than dropping or rewriting it.
			r, width := utf8.DecodeRuneInString(s[i:])
			out = append(out, r)
			i += width
			continue
		}
		out = appendDecomposed(out, s[i:i+size], p)
		i += size
	}
	reorderMarks(out)
	return string(out)
}

// appendDecomposed appends the full canonical decomposition of one
// character, recursing until every element is stable.
func appendDecomposed(out []rune, seq string, p norm.Properties) []rune {
	if hangul := decomposeHangul(seq); hangul != nil {
		return append(out, hangul...)
	}
	d := p.Decomposition()
	if len(d) == 0 {
		for _, r := range seq {
			out = append(out, r)
		}
		return out
	}
	decomposed := string(d)
	if decomposed == seq {
		for _, r := range seq {
			out = append(out, r)
		}
		return out
	}
	for i := 0; i < len(decomposed); {
		q := norm.NFD.PropertiesString(decomposed[i:])
		size := q.Size()
		if size == 0 {
			r, width := utf8.DecodeRuneInString(decomposed[i:])
			out = append(out, r)
			i += width
			continue
		}
		out = appendDecomposed(out, decomposed[i:i+size], q)
		i += size
	}
	return out
}

// decomposeHangul expresses the algorithmic Hangul canonical
// decomposition, which the tables state by rule rather than by entry.
func decomposeHangul(seq string) []rune {
	const sBase, lBase, vBase, tBase = 0xAC00, 0x1100, 0x1161, 0x11A7
	const lCount, vCount, tCount = 19, 21, 28
	const nCount = vCount * tCount
	const sCount = lCount * nCount
	r, width := utf8.DecodeRuneInString(seq)
	if width != len(seq) {
		return nil
	}
	index := int(r) - sBase
	if index < 0 || index >= sCount {
		return nil
	}
	l := rune(lBase + index/nCount)
	v := rune(vBase + (index%nCount)/tCount)
	if index%tCount == 0 {
		return []rune{l, v}
	}
	return []rune{l, v, rune(tBase + index%tCount)}
}

// reorderMarks applies the canonical ordering algorithm: each run of
// non-starters is stably sorted by combining class.
func reorderMarks(rs []rune) {
	for i := 0; i < len(rs); {
		if combiningClass(rs[i]) == 0 {
			i++
			continue
		}
		j := i
		for j < len(rs) && combiningClass(rs[j]) != 0 {
			j++
		}
		run := rs[i:j]
		sort.SliceStable(run, func(a, b int) bool {
			return combiningClass(run[a]) < combiningClass(run[b])
		})
		i = j
	}
}

func combiningClass(r rune) uint8 {
	return norm.NFD.PropertiesString(string(r)).CCC()
}

// isPlainASCII reports whether s needs no decomposition work at all,
// which is the common case for selected names.
func isPlainASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] >= 0x80 {
			return false
		}
	}
	return true
}
