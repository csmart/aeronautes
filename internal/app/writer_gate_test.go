package app

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestWriterGateRefusesAfterShutdown(t *testing.T) {
	g := newWriterGate()
	if err := g.enter(); err != nil {
		t.Fatal(err)
	}
	g.leave()
	if err := g.Shutdown(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := g.enter(); !errors.Is(err, errWritersStopped) {
		t.Fatalf("enter after shutdown = %v", err)
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if err := g.Shutdown(cancelled); err != nil {
		t.Fatalf("already-drained wait = %v", err)
	}
}

func TestWriterGateWaitErrorsWhileOccupied(t *testing.T) {
	g := newWriterGate()
	if err := g.enter(); err != nil {
		t.Fatal(err)
	}
	defer g.leave()
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if err := g.Shutdown(cancelled); !errors.Is(err, context.Canceled) {
		t.Fatalf("occupied cancelled wait = %v", err)
	}
	deadline, cancelDeadline := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancelDeadline()
	if err := g.Shutdown(deadline); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("occupied deadline wait = %v", err)
	}
}

func TestWriterGateDoesNotCancelEnteredWriter(t *testing.T) {
	g := newWriterGate()
	if err := g.enter(); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		if err := g.Shutdown(context.Background()); err != nil {
			t.Errorf("drain = %v", err)
		}
	}()
	time.Sleep(20 * time.Millisecond)
	g.leave()
	wg.Wait()
}

func TestWriterGateWrapsWaitError(t *testing.T) {
	g := newWriterGate()
	if err := g.enter(); err != nil {
		t.Fatal(err)
	}
	defer g.leave()
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	err := g.Shutdown(cancelled)
	if err == nil || !strings.Contains(err.Error(), "wait for synchronous writers") {
		t.Fatalf("wrapped wait = %v", err)
	}
}
