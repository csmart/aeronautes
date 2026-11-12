package main

import (
	"path/filepath"
	"testing"

	"github.com/adrg/xdg"
)

// The settings are configuration and the database is data, each in its own
// XDG directory, and the development override puts both in one directory
// (ADR 0109 decisions 1, 2 and 4).
func TestStorePathsFollowTheBaseDirectories(t *testing.T) {
	// Registered first so it runs last, after the variables are restored.
	t.Cleanup(xdg.Reload)
	config, data := t.TempDir(), t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", config)
	t.Setenv("XDG_DATA_HOME", data)
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("AERONAUTES_STATE_DIR", "")
	xdg.Reload()

	settings, database, err := storePaths()
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(config, "aeronautes", "settings.json"); settings != want {
		t.Fatalf("settings at %s, want %s", settings, want)
	}
	if want := filepath.Join(data, "aeronautes", "aeronautes.sqlite"); database != want {
		t.Fatalf("database at %s, want %s", database, want)
	}

	scratch := t.TempDir()
	t.Setenv("AERONAUTES_STATE_DIR", scratch)
	settings, database, err = storePaths()
	if err != nil || settings != filepath.Join(scratch, "settings.json") || database != filepath.Join(scratch, "aeronautes.sqlite") {
		t.Fatalf("with the override: settings %s, database %s (%v); want both in %s", settings, database, err, scratch)
	}
}
