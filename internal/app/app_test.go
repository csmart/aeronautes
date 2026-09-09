package app

import "testing"

func TestAppServiceInfo(t *testing.T) {
	identity := BuildIdentity{Version: "1.2.3", SourceRevision: "abc123", SourceDirty: "clean"}
	s := NewAppService(identity, "keychain")
	info := s.Info()
	if info.Name != "Aeronautes" {
		t.Errorf("Name = %q", info.Name)
	}
	if info.Version != "1.2.3" {
		t.Errorf("Version = %q", info.Version)
	}
	if info.BuildIdentity != identity || s.About().BuildIdentity != identity {
		t.Fatal("startup and About must preserve the complete build identity")
	}
	if info.SecretBackend != "keychain" {
		t.Errorf("SecretBackend = %q", info.SecretBackend)
	}
	// The test binary does not link wails, so the module lookup must fall
	// back rather than fail; the real shell binary reports the linked
	// version.
	if info.Wails == "" {
		t.Error("Wails version must never be empty")
	}
}
