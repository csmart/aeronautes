package rclone

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	_ "embed"
)

// The §61.1 sidecar: bundles ship the official pinned rclone next to
// the executable, and it is integrity-checked at runtime before the
// first spawn — a bundle carrying a wrong or tampered binary refuses
// to use it, loudly, and never falls back past it. Without a sidecar,
// resolution falls back to PATH with the version pin (the Linux
// packages depend on the distribution's rclone, whose build differs
// from the official one, so PATH binaries are pinned by version, not
// hash).

//go:embed manifest.txt
var manifestText string

// sidecarNames are the file names probed next to the executable.
func sidecarNames() []string {
	if runtime.GOOS == "windows" {
		return []string{"rclone.exe"}
	}
	return []string{"rclone"}
}

// manifestChecksum returns the expected binary sha256 for this build's
// target, or "" when the manifest does not carry it.
func manifestChecksum() string {
	target := runtime.GOOS + "-" + runtime.GOARCH
	scanner := bufio.NewScanner(strings.NewReader(manifestText))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) == 2 && fields[0] == target {
			return fields[1]
		}
	}
	return ""
}

// fileChecksum computes a file's sha256 hex.
func fileChecksum(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// resolveSidecar looks for a bundled rclone in dir and verifies it.
// Returns ("", nil) when no sidecar exists; a PRESENT sidecar that
// fails verification is an error, never a silent fallback.
func resolveSidecar(dir string) (string, error) {
	for _, name := range sidecarNames() {
		path := filepath.Join(dir, name)
		info, err := os.Stat(path)
		if err != nil || info.IsDir() {
			continue
		}
		expected := manifestChecksum()
		if expected == "" {
			return "", fmt.Errorf(
				"bundled rclone found at %s but the build manifest has no checksum for %s-%s; refusing to spawn it",
				path, runtime.GOOS, runtime.GOARCH)
		}
		actual, err := fileChecksum(path)
		if err != nil {
			return "", fmt.Errorf("bundled rclone at %s: %w", path, err)
		}
		if !bytes.Equal([]byte(actual), []byte(expected)) {
			return "", fmt.Errorf(
				"bundled rclone at %s failed integrity verification (sha256 %s, manifest expects %s for the pinned %s): refusing to spawn it — reinstall the application",
				path, actual, expected, PinnedVersion)
		}
		return path, nil
	}
	return "", nil
}

// executableDir is a seam for tests; the default is the directory of
// the running executable.
var executableDir = func() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	return filepath.Dir(exe), nil
}
