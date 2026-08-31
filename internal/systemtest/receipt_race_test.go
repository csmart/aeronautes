package systemtest

import (
	"sync"
	"sync/atomic"
	"testing"
)

// TestReceiptConsumeIsRaceSafe is the focused regression for the receipt
// data race the review reproduced: many goroutines racing to consume one receipt
// must yield exactly one winner, with no data race on the consumed flag.
// Run under -race, this fails loudly if consume() reverts to a plain bool.
// It needs no SAIO — it exercises the consumption primitive directly.
func TestReceiptConsumeIsRaceSafe(t *testing.T) {
	const goroutines = 64
	r := &Receipt{action: "TransferService.StartCopy"}

	var wins int64
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			if r.consume() {
				atomic.AddInt64(&wins, 1)
			}
		}()
	}
	close(start)
	wg.Wait()

	if wins != 1 {
		t.Fatalf("racing consumption produced %d winners, want exactly 1", wins)
	}
	if r.consume() {
		t.Fatal("an already-consumed receipt consumed again")
	}
}
