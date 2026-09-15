package app

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestShutdownWorkWaitsForHeldMetadataWrite(t *testing.T) {
	h := newHarness(t)
	h.connect(t)
	held := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	h.mock.AfterRequest = func(method, path string) {
		if method != "POST" || !strings.Contains(path, "/fixtures/normal/hello.txt") {
			return
		}
		once.Do(func() {
			close(held)
			<-release
		})
	}
	done := make(chan error, 1)
	go func() {
		_, err := h.mutate.UpdateObjectMetadata(context.Background(), "fixtures", "normal/hello.txt", "",
			ObjectMetadataEditDTO{Meta: map[string]string{"Held": "1"}})
		done <- err
	}()
	select {
	case <-held:
	case <-time.After(10 * time.Second):
		t.Fatal("write never reached the POST that changes the object")
	}
	find := NewFindService(h.transfer.connections)
	drained := make(chan error, 1)
	go func() {
		drained <- ShutdownWork(context.Background(), h.transfer.runner, find, h.mutate)
	}()
	select {
	case err := <-drained:
		t.Fatalf("drain returned while the write is held: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	if err := h.db.Reader().PingContext(t.Context()); err != nil {
		t.Fatalf("database closed under the held write: %v", err)
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatalf("held write = %v", err)
	}
	if err := <-drained; err != nil {
		t.Fatalf("drain after leave = %v", err)
	}
}

func TestShutdownWorkRefusesWritesAfterDrain(t *testing.T) {
	h := newHarness(t)
	h.connect(t)
	find := NewFindService(h.transfer.connections)
	before := append([]string(nil), h.mock.RequestLog()...)
	if err := ShutdownWork(t.Context(), h.transfer.runner, find, h.mutate); err != nil {
		t.Fatal(err)
	}
	result, err := h.mutate.UpdateObjectMetadata(t.Context(), "fixtures", "normal/hello.txt", "",
		ObjectMetadataEditDTO{Meta: map[string]string{"After": "drain"}})
	if !errors.Is(err, errWritersStopped) {
		t.Fatalf("post-drain write = %+v err=%v", result, err)
	}
	after := h.mock.RequestLog()
	if len(after) != len(before) {
		t.Fatalf("post-drain issued store requests: before=%v after=%v", before, after)
	}
}
