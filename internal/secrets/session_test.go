package secrets

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
)

func TestSessionStoreRoundTrip(t *testing.T) {
	s := NewSessionStore()
	ctx := context.Background()

	if _, err := s.Get(ctx, "bookmark/prod"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Get on empty store: err = %v, want ErrNotFound", err)
	}

	if err := s.Put(ctx, "bookmark/prod", []byte("swift-key")); err != nil {
		t.Fatal(err)
	}
	got, err := s.Get(ctx, "bookmark/prod")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, []byte("swift-key")) {
		t.Fatalf("Get = %q, want %q", got, "swift-key")
	}

	if err := s.Put(ctx, "bookmark/prod", []byte("rotated")); err != nil {
		t.Fatal(err)
	}
	got, err = s.Get(ctx, "bookmark/prod")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, []byte("rotated")) {
		t.Fatalf("Get after overwrite = %q, want %q", got, "rotated")
	}

	if err := s.Delete(ctx, "bookmark/prod"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Get(ctx, "bookmark/prod"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Get after Delete: err = %v, want ErrNotFound", err)
	}
	if err := s.Delete(ctx, "bookmark/prod"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("second Delete: err = %v, want ErrNotFound", err)
	}
}

func TestSessionStoreCopiesValues(t *testing.T) {
	s := NewSessionStore()
	ctx := context.Background()

	in := []byte("original")
	if err := s.Put(ctx, "n", in); err != nil {
		t.Fatal(err)
	}
	in[0] = 'X' // caller mutates its slice after Put

	got, err := s.Get(ctx, "n")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, []byte("original")) {
		t.Fatalf("stored value changed by caller mutation: %q", got)
	}

	got[0] = 'Y' // caller mutates the returned slice
	again, err := s.Get(ctx, "n")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(again, []byte("original")) {
		t.Fatalf("stored value changed via returned slice: %q", again)
	}
}

func TestSessionStoreWipe(t *testing.T) {
	s := NewSessionStore()
	ctx := context.Background()
	for i := range 3 {
		if err := s.Put(ctx, fmt.Sprintf("n%d", i), []byte("v")); err != nil {
			t.Fatal(err)
		}
	}
	s.Wipe()
	for i := range 3 {
		if _, err := s.Get(ctx, fmt.Sprintf("n%d", i)); !errors.Is(err, ErrNotFound) {
			t.Fatalf("Get(n%d) after Wipe: err = %v, want ErrNotFound", i, err)
		}
	}
}

func TestSessionStoreConcurrent(t *testing.T) {
	s := NewSessionStore()
	ctx := context.Background()
	var wg sync.WaitGroup
	for w := range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			name := fmt.Sprintf("bookmark/%d", w)
			for range 100 {
				if err := s.Put(ctx, name, []byte(name)); err != nil {
					t.Error(err)
					return
				}
				if v, err := s.Get(ctx, name); err != nil || !bytes.Equal(v, []byte(name)) {
					t.Errorf("Get(%s) = %q, %v", name, v, err)
					return
				}
			}
		}()
	}
	wg.Wait()
}
