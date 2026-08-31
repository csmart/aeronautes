// Package attrprobe is a fixture, run only as a subprocess by the scenariocov
// attr regression (never by ./...; testdata is excluded from wildcards). It
// calls t.Attr, which Go 1.27's test2json emits as an Action:"attr" event
// under -v, so the reconciler must model that event rather than reject it.
package attrprobe

import "testing"

func TestCodexAttr(t *testing.T) {
	t.Attr("review", "probe")
}
