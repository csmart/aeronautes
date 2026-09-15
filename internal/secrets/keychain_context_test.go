package secrets

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestKeychainHonoursCancelledContext(t *testing.T) {
	store := &KeychainStore{}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	got, err := store.Get(ctx, "test/cancelled")
	if got != nil || !errors.Is(err, context.Canceled) || errors.Is(err, ErrUnresolved) {
		t.Fatalf("Get = %q, %v", got, err)
	}
	if err := store.Put(ctx, "test/cancelled", []byte("v")); !errors.Is(err, context.Canceled) || !errors.Is(err, ErrUnresolved) {
		t.Fatalf("Put = %v, want canceled unresolved", err)
	}
	if err := store.Delete(ctx, "test/cancelled"); !errors.Is(err, context.Canceled) || !errors.Is(err, ErrUnresolved) {
		t.Fatalf("Delete = %v, want canceled unresolved", err)
	}
}

func TestOpenDefaultTimeoutIsTenSeconds(t *testing.T) {
	if openDefaultTimeout != 10*time.Second {
		t.Fatalf("openDefaultTimeout = %s, want 10s", openDefaultTimeout)
	}
}
