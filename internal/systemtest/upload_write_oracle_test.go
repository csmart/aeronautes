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
// The bound itself, pinned against the measurement rather than against the
// expression that computes it: if either retry layer's number changes, this
// says so instead of the cases quietly moving with it.
func TestUploadWriteOracleBoundMatchesTheMeasuredLifecycle(t *testing.T) {
	if rclone.DefaultLowLevelRetries != 2 {
		t.Fatalf("the engine's measured budget at one request is 2 total attempts, the runner passes %d; re-measure before changing this",
			rclone.DefaultLowLevelRetries)
	}
	if got := maxEngineWritesPerIdentity(); got != 4 {
		t.Fatalf("one staged object can be written at most 4 times across a job, 2 attempts in each of 2 child runs; the oracle allows %d", got)
	}
}

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
		// These counts are the MEASURED ones, written as literals on
		// purpose. Deriving them from the implementation's own formula
		// would make them agree with a wrong bound, which is how the
		// first version of this oracle allowed an attempt the engine
		// never makes. Two is the engine's whole budget at one request,
		// measured against rclone v1.74.3; four is that budget twice
		// over, because uploadRun.copy runs the engine again after an
		// authentication-shaped failure; five is past both.
		{"the engine retrying one staged upload is sound", func(m map[string]int) { m[staged] = 2 }, ""},
		{"one staged upload written once in each of two child runs is sound",
			func(m map[string]int) { m[staged] = 3 }, ""},
		{"both retry layers together at their limit is sound",
			func(m map[string]int) { m[staged] = 4 }, ""},
		{"one write past both retry layers is not",
			func(m map[string]int) { m[staged] = 5 }, "outside the engine's budget"},
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
