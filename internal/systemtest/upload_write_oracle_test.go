package systemtest

import (
	"strings"
	"testing"

	"github.com/csmart/aeronautes/internal/transfer/rclone"
)

// What an upload's mutations are allowed to be, tested on the comparison
// itself rather than through a fixture, so the rule is pinned rather than
// inferred from whichever attempts a runner happened to make.
//
// PR 182's first attempt failed because this comparison demanded that the
// ENGINE write each staged object exactly once, which the engine's own
// retry budget contradicts. These cases fix what it now demands: exactly
// once for the mutations this application issues, and between one and the
// engine's budget for the ones the engine issues.
func TestUploadWriteOracle(t *testing.T) {
	const prefix = "/v1/AUTH_test/media/"
	const id = "jobid"
	paths := []string{"/local/a.bin", "/local/z-engine.bin"}
	sound := func() map[string]int {
		return map[string]int{
			"PUT " + prefix + "scope/a.bin":                                        1,
			"PUT " + prefix + "scope/z-engine.bin":                                 1,
			"PUT " + prefix + ".aeronautes/stage/" + id + "/batch/a.bin":           1,
			"PUT " + prefix + ".aeronautes/stage/" + id + "/batch/z-engine.bin":    1,
			"DELETE " + prefix + ".aeronautes/stage/" + id + "/batch/z-engine.bin": 1,
		}
	}
	staged := "PUT " + prefix + ".aeronautes/stage/" + id + "/batch/a.bin"
	live := "PUT " + prefix + "scope/a.bin"
	deleted := "DELETE " + prefix + ".aeronautes/stage/" + id + "/batch/z-engine.bin"

	for _, tc := range []struct {
		name    string
		alter   func(map[string]int)
		wantFor string
	}{
		{"one attempt at everything is sound", func(map[string]int) {}, ""},
		// The condition PR 182 hit: the engine re-sent one staged upload.
		{"the engine retrying one staged upload is sound", func(m map[string]int) { m[staged] = 1 + rclone.DefaultLowLevelRetries }, ""},
		{"the engine exceeding its retry budget is not",
			func(m map[string]int) { m[staged] = 2 + rclone.DefaultLowLevelRetries }, "outside the engine's budget"},
		{"the engine skipping a staged upload is not",
			func(m map[string]int) { delete(m, staged) }, "never wrote a staged object"},
		// The half that must stay exact: this application publishes each
		// object once and deletes each staged object once.
		{"publishing the same object twice is not",
			func(m map[string]int) { m[live] = 2 }, "own mutations differ"},
		{"deleting the staged object twice is not",
			func(m map[string]int) { m[deleted] = 2 }, "own mutations differ"},
		{"publishing an object nobody asked for is not",
			func(m map[string]int) { m["PUT "+prefix+"scope/intruder.bin"] = 1 }, "own mutations differ"},
		{"not publishing at all is not",
			func(m map[string]int) { delete(m, live) }, "own mutations differ"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := sound()
			tc.alter(got)
			problems := uploadWriteProblems(got, prefix, id, false, paths)
			joined := strings.Join(problems, "; ")
			if tc.wantFor == "" {
				if len(problems) != 0 {
					t.Fatalf("sound mutations were refused: %s", joined)
				}
				return
			}
			if len(problems) == 0 || !strings.Contains(joined, tc.wantFor) {
				t.Fatalf("unsound mutations were not named as %q: %s", tc.wantFor, joined)
			}
		})
	}
}

// The native route stages only the one name the engine handles, so the
// other items must show no staged write at all and none is demanded.
func TestUploadWriteOracleNativeRouteStagesOnlyTheEnginesName(t *testing.T) {
	const prefix = "/v1/AUTH_test/media/"
	const id = "jobid"
	paths := []string{"/local/a.bin", "/local/z-engine.bin"}
	got := map[string]int{
		"PUT " + prefix + "scope/a.bin":                                        1,
		"PUT " + prefix + "scope/z-engine.bin":                                 1,
		"PUT " + prefix + ".aeronautes/stage/" + id + "/batch/z-engine.bin":    1,
		"DELETE " + prefix + ".aeronautes/stage/" + id + "/batch/z-engine.bin": 1,
	}
	if problems := uploadWriteProblems(got, prefix, id, true, paths); len(problems) != 0 {
		t.Fatalf("a native upload's own mutations were refused: %s", strings.Join(problems, "; "))
	}
	got["PUT "+prefix+".aeronautes/stage/"+id+"/batch/a.bin"] = 1
	if problems := uploadWriteProblems(got, prefix, id, true, paths); len(problems) == 0 {
		t.Fatal("a native item staged behind the engine's back was accepted")
	}
}
