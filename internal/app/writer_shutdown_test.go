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
	var once, released sync.Once
	unblock := func() { released.Do(func() { close(release) }) }
	t.Cleanup(unblock)
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
		_, err := h.mutate.UpdateObjectMetadata(context.Background(), h.portrait(), "fixtures", "normal/hello.txt", "",
			ObjectMetadataEditDTO{Meta: map[string]string{"Held": "1"}})
		done <- err
	}()
	select {
	case <-held:
	case <-time.After(2 * time.Second):
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
	unblock()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("held write = %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("held write did not finish after release")
	}
	select {
	case err := <-drained:
		if err != nil {
			t.Fatalf("drain after leave = %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("drain did not finish after the write left")
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
	for _, c := range mutateStoreWrites(h) {
		t.Run(c.name, func(t *testing.T) {
			if err := c.run(); !errors.Is(err, errWritersStopped) {
				t.Fatalf("post-drain %s = %v", c.name, err)
			}
		})
	}
	after := h.mock.RequestLog()
	if len(after) != len(before) {
		t.Fatalf("post-drain issued store requests: before=%v after=%v", before, after)
	}
}

func TestShutdownWorkWaitsForEveryMutateStoreWrite(t *testing.T) {
	for _, c := range []struct {
		name, method, path string
		run                func(*harness) error
	}{
		{"CreateContainer", "PUT", "/after-hold-create", func(h *harness) error {
			_, err := h.mutate.CreateContainer(context.Background(), h.portrait(), ContainerCreateDTO{Name: "after-hold-create"})
			return err
		}},
		{"CreateMarker", "PUT", "held-marker/", func(h *harness) error {
			_, err := h.mutate.CreateMarker(context.Background(), h.portrait(), "fixtures", "held-marker/")
			return err
		}},
		{"DeleteMarker", "DELETE", "folder-marker/", func(h *harness) error {
			_, err := h.mutate.DeleteMarker(context.Background(), h.portrait(), "fixtures", "folder-marker/")
			return err
		}},
		{"DeleteManifest", "DELETE", "external-dlo", func(h *harness) error {
			_, err := h.mutate.DeleteManifest(context.Background(), h.portrait(), "fixtures", "external-dlo")
			return err
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			assertDrainWaitsForStoreWrite(t, c.method, c.path, c.run)
		})
	}
}

type mutateWrite struct {
	name string
	run  func() error
}

func mutateStoreWrites(h *harness) []mutateWrite {
	return []mutateWrite{
		{"UpdateObjectMetadata", func() error {
			_, err := h.mutate.UpdateObjectMetadata(context.Background(), h.portrait(), "fixtures", "normal/hello.txt", "",
				ObjectMetadataEditDTO{Meta: map[string]string{"After": "drain"}})
			return err
		}},
		{"UpdateContainer", func() error {
			_, err := h.mutate.UpdateContainer(context.Background(), h.portrait(), "fixtures", "", ContainerEditDTO{})
			return err
		}},
		{"CreateContainer", func() error {
			_, err := h.mutate.CreateContainer(context.Background(), h.portrait(), ContainerCreateDTO{Name: "after-drain"})
			return err
		}},
		{"CreateMarker", func() error {
			_, err := h.mutate.CreateMarker(context.Background(), h.portrait(), "fixtures", "after-drain-marker/")
			return err
		}},
		{"DeleteMarker", func() error {
			_, err := h.mutate.DeleteMarker(context.Background(), h.portrait(), "fixtures", "folder-marker/")
			return err
		}},
		{"DeleteManifest", func() error {
			_, err := h.mutate.DeleteManifest(context.Background(), h.portrait(), "fixtures", "external-dlo")
			return err
		}},
	}
}

func assertDrainWaitsForStoreWrite(t *testing.T, method, pathPart string, run func(*harness) error) {
	t.Helper()
	h := newHarness(t)
	h.connect(t)
	held := make(chan struct{})
	release := make(chan struct{})
	var once, released sync.Once
	unblock := func() { released.Do(func() { close(release) }) }
	t.Cleanup(unblock)
	h.mock.AfterRequest = func(gotMethod, path string) {
		if gotMethod != method || !strings.Contains(path, pathPart) {
			return
		}
		once.Do(func() {
			close(held)
			<-release
		})
	}
	done := make(chan error, 1)
	go func() { done <- run(h) }()
	select {
	case <-held:
	case <-time.After(2 * time.Second):
		t.Fatalf("%s %s never reached the store", method, pathPart)
	}
	find := NewFindService(h.transfer.connections)
	drained := make(chan error, 1)
	go func() {
		drained <- ShutdownWork(context.Background(), h.transfer.runner, find, h.mutate)
	}()
	select {
	case err := <-drained:
		t.Fatalf("drain returned while %s is held: %v", pathPart, err)
	case <-time.After(50 * time.Millisecond):
	}
	unblock()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("held %s = %v", pathPart, err)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("held %s did not finish after release", pathPart)
	}
	select {
	case err := <-drained:
		if err != nil {
			t.Fatalf("drain after %s left = %v", pathPart, err)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("drain did not finish after %s left", pathPart)
	}
}
