package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestCleanReleaseWithGeneratedLineEndings(t *testing.T) {
	root := fixture(t)
	paths := []string{
		".gitattributes",
		"frontend/bindings/github.com/csmart/aeronautes/internal/app/models.ts",
		"frontend/bindings/github.com/wailsapp/wails/v3/internal/eventdata.d.ts",
	}
	inputs := make(map[string]string)
	for _, name := range paths {
		data, err := os.ReadFile(filepath.Join("../..", name))
		if err != nil {
			t.Fatal(err)
		}
		inputs[name] = string(data)
		put(t, root, name, string(data))
	}
	gitOK(t, root, "config", "core.autocrlf", "true")
	gitOK(t, root, "add", ".")
	gitOK(t, root, "-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid", "commit", "-qm", "generated inputs")
	gitOK(t, root, "tag", "v3.4.5")
	for _, name := range append(paths, "go.mod") {
		if err := os.Remove(filepath.Join(root, name)); err != nil {
			t.Fatal(err)
		}
	}
	gitOK(t, root, "checkout", "--", ".")
	if _, err := resolve(root, "v3.4.5", "windows", "amd64"); err != nil {
		t.Fatalf("fresh checkout: %v", err)
	}

	t.Run("unchanged generation", func(t *testing.T) {
		cmd := exec.Command("go", "mod", "tidy")
		cmd.Dir = root
		if data, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("go mod tidy: %v\n%s", err, data)
		}
		// Wails emits LF on Windows too; replay that boundary without
		// making the unit suite depend on an installed Wails CLI.
		for _, name := range paths[1:] {
			put(t, root, name, inputs[name])
		}
		i, err := resolve(root, "v3.4.5", "windows", "amd64")
		if err != nil || i.SourceDirty != "clean" {
			t.Fatalf("unchanged generated inputs refused a release: %+v %v", i, err)
		}
	})
	t.Run("changed generated content", func(t *testing.T) {
		name := paths[1]
		put(t, root, name, inputs[name]+"\n// changed content\n")
		defer put(t, root, name, inputs[name])
		if _, err := resolve(root, "v3.4.5", "windows", "amd64"); err == nil {
			t.Fatal("release accepted changed generated content")
		}
	})
	t.Run("untracked input", func(t *testing.T) {
		put(t, root, "untracked-input", "new input\n")
		if _, err := resolve(root, "v3.4.5", "windows", "amd64"); err == nil {
			t.Fatal("release accepted an untracked input")
		}
	})
}
