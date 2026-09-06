package protocoltest

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"strings"
	"testing"

	"github.com/csmart/aeronautes/internal/secrets"
	"github.com/zalando/go-keyring"
)

func TestKeychainBinaryRoundTrip(t *testing.T) {
	fixture(t)
	store, err := secrets.NewKeychainStore()
	if err != nil {
		t.Fatal(err)
	}
	name := "fixture/" + rand.Text()
	for _, value := range [][]byte{{0, 0xff, '\n', 'k', 0x80}, []byte("rotated fixture value")} {
		if err := store.Put(t.Context(), name, value); err != nil {
			t.Fatal(err)
		}
		got, err := store.Get(t.Context(), name)
		if err != nil || !bytes.Equal(got, value) {
			t.Fatalf("binary round trip failed: %v", err)
		}
	}
	if err := store.Delete(t.Context(), name); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Get(t.Context(), name); !errors.Is(err, secrets.ErrNotFound) {
		t.Fatalf("deleted fixture value remains readable: %v", err)
	}
}

func TestKeychainDecodeErrorHidesReference(t *testing.T) {
	fixture(t)
	store, err := secrets.NewKeychainStore()
	if err != nil {
		t.Fatal(err)
	}
	name := "private-reference/" + rand.Text()
	malformed := "malformed synthetic credential%"
	// A different keyring client or damaged value can bypass our byte encoding.
	if err := keyring.Set("aeronautes", name, malformed); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = keyring.Delete("aeronautes", name) })
	_, err = store.Get(t.Context(), name)
	if err == nil {
		t.Fatal("malformed keychain value was accepted")
	}
	for _, private := range []string{name, malformed, base64.StdEncoding.EncodeToString([]byte(malformed))} {
		if strings.Contains(err.Error(), private) {
			t.Fatal("decode error exposed a credential reference or value")
		}
	}
	var corrupt base64.CorruptInputError
	if !errors.As(err, &corrupt) {
		t.Fatal("decode error lost its safe corruption category")
	}
}
