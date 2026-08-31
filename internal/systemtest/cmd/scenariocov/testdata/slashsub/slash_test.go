// Package slashsub is a fixture, run only as a subprocess by the scenariocov
// regressions (never by ./...; testdata is excluded from wildcards). It
// exercises the real go test -json node hierarchy rather than synthetic
// events. TestTopLevel emits a valid top-level claim; TestSlash emits a claim
// from a slash-named subtest that has a separate sibling named "a", the
// ambiguous case where an observed lexical prefix is a sibling, not a parent.
package slashsub

import "testing"

const marker = "AERONAUTES_SCENARIO_CLAIM "

func TestTopLevel(t *testing.T) {
	t.Logf("%s%s", marker, `{"action":"AppService.About","scenario":"happy","evidence":"real top-level"}`)
}

func TestSlash(t *testing.T) {
	// t.Run("a/b") and t.Run("a") create sibling nodes "TestSlash/a/b" and
	// "TestSlash/a"; the latter is NOT the former's parent. A claim from the
	// subtest must be rejected (claims are top-level only), not silently
	// dropped by mistaking the skipped sibling for an ancestor.
	t.Run("a/b", func(t *testing.T) {
		t.Logf("%s%s", marker, `{"action":"AppService.Info","scenario":"happy","evidence":"subtest claim"}`)
	})
	t.Run("a", func(t *testing.T) { t.Skip("separate sibling") })
}
