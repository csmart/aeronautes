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

// The §61.1 sidecar: every bundle ships the official pinned rclone and
// integrity-checks it at runtime before the first spawn — a bundle
// carrying a wrong or tampered binary refuses to use it, loudly, and
// never falls back past it. macOS and Windows carry it beside the
// executable, and so does the Linux tarball. The Linux rpm and deb
// install it in ../libexec/aeronautes relative to the executable
// (ADR 0089), off PATH, so it cannot shadow the user's own rclone and
// the user's rclone cannot shadow it. Without any sidecar, resolution
// falls back to PATH under the version pin: that is the development
// checkout, and a PATH binary is pinned by version rather than hash
// because it is whatever build the developer put there.

//go:embed manifest.txt
var manifestText string

// Where a resolved engine came from (ADR 0089 decision 6).
const (
	SourceExplicit = "explicit" // Options.Binary, the caller's choice
	SourceBeside   = "sidecar"  // beside the executable
	SourceLibexec  = "libexec"  // ../libexec/aeronautes, the Linux package layout
	SourcePath     = "path"     // PATH under the version pin, development only
)

// sidecarNames are the file names probed in each sidecar directory.
func sidecarNames() []string {
	if runtime.GOOS == "windows" {
		return []string{"rclone.exe"}
	}
	return []string{"rclone"}
}

// sidecarDir is one place a bundled engine may live and what a binary
// found there resolves as.
type sidecarDir struct {
	dir    string
	source string
}

// sidecarDirs lists the sidecar directories in precedence order. Beside
// the executable comes first because every bundle other than the Linux
// packages places the engine there; the Linux package layout is probed
// only on Linux, so a stray ../libexec/aeronautes on another platform
// is never read.
func sidecarDirs(goos, exeDir string) []sidecarDir {
	dirs := []sidecarDir{{dir: exeDir, source: SourceBeside}}
	if goos == "linux" {
		dirs = append(dirs, sidecarDir{
			dir:    filepath.Join(exeDir, "..", "libexec", "aeronautes"),
			source: SourceLibexec,
		})
	}
	return dirs
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

// resolveBundled returns the first present sidecar across sidecarDirs,
// verified, with its source. The first directory holding a file
// decides: a present sidecar that fails verification is an error and
// never a fall-through to the next directory or to PATH. ("", "", nil)
// means no sidecar exists anywhere.
func resolveBundled(exeDir string) (path, source string, err error) {
	for _, candidate := range sidecarDirs(runtime.GOOS, exeDir) {
		path, err := resolveSidecar(candidate.dir)
		if err != nil {
			return "", "", err
		}
		if path != "" {
			return path, candidate.source, nil
		}
	}
	return "", "", nil
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
