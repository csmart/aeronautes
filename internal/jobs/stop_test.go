package jobs

import (
	"errors"
	"sync"
	"testing"
	"time"
)

// ADR 0105 decision 9: the store names the job whose record stopped it, and
// only that job. A sibling the stop cancels can fail its own final save in
// turn; that is not a second stop, so it cancels nothing and is told nothing,
// though its reason may still stand as the one reported. The choice of who
// stopped the store is made with the transition, under the store's lock, so
// no interleaving of the two writes can make the later one the named job.
func TestTheFirstStopNamesItsJobAndALaterOneNothing(t *testing.T) {
	s := NewStore(nil)
	var mu sync.Mutex
	cancelled := map[string]int{}
	for _, id := range []string{"failed", "sibling", "other"} {
		s.CancelOnStop(id, func() {
			mu.Lock()
			cancelled[id]++
			mu.Unlock()
		})
	}
	told := make(chan string, 4)
	s.OnStop(func(id string) { told <- id })

	s.latch("failed", errors.New("the failed job's item record"), false)
	s.latch("sibling", errors.New("the sibling's final result"), true)

	select {
	case id := <-told:
		if id != "failed" {
			t.Fatalf("the notice named %q, want the job whose record stopped the store", id)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the store gave no notice")
	}
	select {
	case id := <-told:
		t.Fatalf("a second notice named %q", id)
	case <-time.After(100 * time.Millisecond):
	}
	mu.Lock()
	defer mu.Unlock()
	if cancelled["failed"] != 0 || cancelled["sibling"] != 1 || cancelled["other"] != 1 {
		t.Fatalf("cancels = %v, want each other job once and the failed one never", cancelled)
	}
	if err := s.Available(); err == nil || err.Error() != "the sibling's final result" {
		t.Fatalf("the reason that stands = %v, want the overriding one", err)
	}
}
