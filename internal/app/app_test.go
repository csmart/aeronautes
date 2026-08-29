package app

import "testing"

func TestAppServiceInfo(t *testing.T) {
	s := NewAppService("1.2.3", "keychain")
	info := s.Info()
	if info.Name != "Aeronautes" {
		t.Errorf("Name = %q", info.Name)
	}
	if info.Version != "1.2.3" {
		t.Errorf("Version = %q", info.Version)
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
