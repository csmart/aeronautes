package secrets

import "testing"

func TestOriginVerifiedBy(t *testing.T) {
	session := Origin{Kind: BackendSession, Instance: "s1"}
	keyA := Origin{Kind: BackendKeychain, Instance: "a1", Persistent: "/collection/a"}
	keyB := Origin{Kind: BackendKeychain, Instance: "a2", Persistent: "/collection/b"}
	keySame := Origin{Kind: BackendKeychain, Instance: "a9", Persistent: "/collection/a"}
	legacy := Origin{Kind: BackendKeychain, Instance: "a1"}
	tests := []struct {
		name   string
		origin Origin
		active Origin
		want   bool
	}{
		{"session self", session, session, true},
		{"new session", session, Origin{Kind: BackendSession, Instance: "s2"}, false},
		{"keychain same collection", keyA, keySame, true},
		{"keychain other collection", keyA, keyB, false},
		{"keychain empty persistent", legacy, keyA, false},
		{"legacy origin", Origin{}, session, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.origin.VerifiedBy(tt.active); got != tt.want {
				t.Fatalf("VerifiedBy = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestOriginReadableBy(t *testing.T) {
	keyA := Origin{Kind: BackendKeychain, Instance: "a1", Persistent: "/collection/a"}
	keyB := Origin{Kind: BackendKeychain, Instance: "a2", Persistent: "/collection/b"}
	legacy := Origin{Kind: BackendKeychain, Instance: "a1"}
	other := Origin{Kind: BackendKeychain, Instance: "a2"}
	session := Origin{Kind: BackendSession, Instance: "s1"}
	if !legacy.ReadableBy(other) {
		t.Fatal("empty Persistent should remain readable by any keychain adapter")
	}
	if !keyA.ReadableBy(Origin{Kind: BackendKeychain, Instance: "x", Persistent: "/collection/a"}) {
		t.Fatal("matching Persistent should be readable across adapter instances")
	}
	if keyA.ReadableBy(keyB) {
		t.Fatal("different Persistent should not be readable")
	}
	if keyA.ReadableBy(session) {
		t.Fatal("keychain origin should not be readable by a session store")
	}
}
