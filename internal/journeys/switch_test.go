//go:build realcloud

package journeys

import (
	"fmt"
	"testing"
	"time"
)

// TestSwitchJourney: switch between the two clouds several times and check
// that each connect lists and reads the account it is on: each account's
// own scratch container is listed there and never under the other, and
// its settings are read and remembered (journey 6). The sign-in count is
// proved against the mock; here each connect's duration is logged, so a
// switch that signs in again shows as the slow one.
func TestSwitchJourney(t *testing.T) {
	w := newWorld(t)
	marks := map[string]string{}
	for _, label := range []string{"ceph", "swift"} {
		marks[label] = w.container(w.accounts[label], "switch")
	}
	for round := 1; round <= repeats(t); round++ {
		for _, label := range []string{"ceph", "swift", "ceph"} {
			t.Run(fmt.Sprintf("%s/%d", label, round), func(t *testing.T) {
				w.t = t
				a := w.accounts[label]
				started := time.Now()
				if _, err := w.browse.Connect(w.ctx, a.bookmarkID); err != nil {
					t.Fatalf("connect: %s", failure(err))
				}
				t.Logf("connect took %s", time.Since(started).Round(time.Millisecond))
				listed := w.listed(a)
				for other, mark := range marks {
					if listed[mark] != (other == label) {
						t.Fatalf("the listing under %s shows %s's scratch container: %v", label, other, listed[mark])
					}
				}
				if _, err := w.inspector.Container(w.ctx, a.bookmarkID, marks[label]); err != nil {
					t.Fatalf("read the scratch container's settings: %s", failure(err))
				}
				for _, row := range w.containerRows(a) {
					if row.Name == marks[label] && row.Settings == nil {
						t.Fatal("the settings just read are not on the listed row")
					}
				}
			})
		}
	}
}

// listed is the set of container names the connected account lists.
func (w *world) listed(a *account) map[string]bool {
	w.t.Helper()
	out := map[string]bool{}
	for _, row := range w.containerRows(a) {
		out[row.Name] = true
	}
	return out
}
