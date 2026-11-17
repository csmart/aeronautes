package systemtest

import (
	"crypto/sha256"
	"fmt"
	"io/fs"
	"testing"
)

// The retention oracle, exercised against the schedules the pipeline can
// actually produce — including the one a review identified from the source and
// which no fast local run reaches: the earlier snapshot catches a short prefix,
// the rest of what the barrier already released then reaches the disk, and only
// afterwards does the child stop. That is correct behaviour, and an equality on
// the whole snapshot called it a change to retained state.
//
// This is deterministic and needs no fixture, because the interleaving is the
// subject. Driving it through a real child would make the schedule a matter of
// luck, which is the problem rather than a way to test it.
func TestLifetimeRetainedAllowsProgressButNothingElse(t *testing.T) {
	const released = 64 * 1024
	body := make([]byte, 512*1024)
	for i := range body {
		body[i] = byte(i*7 + 3)
	}
	prefix := func(n int64) lifetimeFile {
		return lifetimeFile{Mode: 0o644, Size: n, Hash: fmt.Sprintf("%x", sha256.Sum256(body[:n]))}
	}
	const partial = "nested/file.bin.0123abcd.partial"
	sentinel := lifetimeFile{Mode: 0o600, Size: 9, Hash: "deadbeef"}
	folder := lifetimeFile{Mode: fs.ModeDir | 0o700}
	snapshot := func(p lifetimeFile, edits ...func(map[string]lifetimeFile)) map[string]lifetimeFile {
		m := map[string]lifetimeFile{"nested": folder, partial: p, "sentinel": sentinel}
		for _, edit := range edits {
			edit(m)
		}
		return m
	}

	for _, c := range []struct {
		name   string
		before map[string]lifetimeFile
		after  map[string]lifetimeFile
		accept bool
	}{
		{"nothing moved", snapshot(prefix(released)), snapshot(prefix(released)), true},
		{"a short prefix grew to the whole release", snapshot(prefix(32 * 1024)), snapshot(prefix(released)), true},
		{"a short prefix grew part of the way", snapshot(prefix(16 * 1024)), snapshot(prefix(48 * 1024)), true},
		{"a short prefix did not grow at all", snapshot(prefix(32 * 1024)), snapshot(prefix(32 * 1024)), true},
		{"it grew past what the barrier released", snapshot(prefix(released)), snapshot(prefix(released + 1)), false},
		{"it shrank", snapshot(prefix(released)), snapshot(prefix(32 * 1024)), false},
		{"it is no longer the object's prefix", snapshot(prefix(released)),
			snapshot(lifetimeFile{Mode: 0o644, Size: released, Hash: fmt.Sprintf("%x", sha256.Sum256(body[1:released+1]))}), false},
		{"its mode changed", snapshot(prefix(released)), snapshot(lifetimeFile{Mode: 0o600, Size: released,
			Hash: fmt.Sprintf("%x", sha256.Sum256(body[:released]))}), false},
		{"the sentinel changed", snapshot(prefix(released)), snapshot(prefix(released), func(m map[string]lifetimeFile) {
			m["sentinel"] = lifetimeFile{Mode: 0o600, Size: 10, Hash: "f00d"}
		}), false},
		{"a folder's mode changed", snapshot(prefix(released)), snapshot(prefix(released), func(m map[string]lifetimeFile) {
			m["nested"] = lifetimeFile{Mode: fs.ModeDir | 0o755}
		}), false},
		{"an entry disappeared", snapshot(prefix(released)), snapshot(prefix(released), func(m map[string]lifetimeFile) {
			delete(m, "sentinel")
		}), false},
		{"an entry appeared", snapshot(prefix(released)), snapshot(prefix(released), func(m map[string]lifetimeFile) {
			m["unexpected"] = sentinel
		}), false},
	} {
		t.Run(c.name, func(t *testing.T) {
			err := lifetimeRetained(c.before, c.after, partial, body, released)
			if c.accept && err != nil {
				t.Fatalf("correct retention was reported as a change: %v", err)
			}
			if !c.accept && err == nil {
				t.Fatal("a change to retained state was accepted")
			}
		})
	}
}
