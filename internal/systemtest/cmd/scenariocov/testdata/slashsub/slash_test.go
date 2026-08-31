// Package slashsub is a fixture, run only as a subprocess by the scenariocov
// F-04 regression (never by ./...; testdata is excluded from wildcards). It
// emits a claim from a subtest whose name contains a slash, exercising the
// real go test -json node hierarchy rather than synthetic events.
package slashsub

import "testing"

func TestSlash(t *testing.T) {
	// t.Run("a/b") creates the node "TestSlash/a/b" and its parent
	// "TestSlash", but NO intermediate "TestSlash/a" node. A reconciler that
	// demanded a pass for every lexical slash-prefix would drop this claim.
	t.Run("a/b", func(t *testing.T) {
		t.Logf("%s%s", "AERONAUTES_SCENARIO_CLAIM ",
			`{"action":"AppService.About","scenario":"happy","evidence":"real subprocess subtest"}`)
	})
}
