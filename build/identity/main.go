// Build identity is resolved once before compiling, so native resources and
// linker values cannot describe different releases.
package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"text/template"
)

const metadataDir = "bin/build-identity"

type identity struct {
	Version        string `json:"version"`
	SourceRevision string `json:"sourceRevision"`
	SourceDirty    string `json:"sourceDirty"`
	// DevelopedFrom is, for a development build, the release its commit
	// follows: the nearest vMAJOR.MINOR.PATCH tag behind it, as git describe
	// finds it, without the "v"; empty for a release or with no tag behind.
	DevelopedFrom  string `json:"developedFrom,omitempty"`
	NativeVersion  string `json:"nativeVersion"`
	PackageVersion string `json:"packageVersion"`
	GOOS           string `json:"goos"`
	Arch           string `json:"arch"`
	GoVersion      string `json:"goVersion"`
	ArtifactName   string `json:"artifactName"`
	Binary         string `json:"binary"`
	BinarySHA256   string `json:"binarySHA256"`
}

var stableVersion = regexp.MustCompile(`^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$`)

func releaseVersion(tag string) (string, error) {
	parts := stableVersion.FindStringSubmatch(tag)
	if parts == nil {
		return "", fmt.Errorf("release version %q: require vMAJOR.MINOR.PATCH; prerelease/build suffixes are not supported", tag)
	}
	for _, part := range parts[1:] {
		if _, err := strconv.ParseUint(part, 10, 16); err != nil {
			return "", fmt.Errorf("release version %q: each component must fit a Windows 16-bit version field", tag)
		}
	}
	return strings.TrimPrefix(tag, "v"), nil
}

// nearestRelease is the version of the nearest release tag behind HEAD, or
// "" when there is none, or none the checkout fetched.
func nearestRelease(root string) string {
	tag, err := git(root, "describe", "--tags", "--abbrev=0", "--match", "v[0-9]*")
	if err != nil {
		return ""
	}
	version, err := releaseVersion(tag)
	if err != nil {
		return ""
	}
	return version
}

func git(root string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = root
	data, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("git %s: %w", strings.Join(args, " "), err)
	}
	return strings.TrimSpace(string(data)), nil
}

func resolve(root, tag, goos, arch string) (identity, error) {
	i := identity{Version: "dev", NativeVersion: "0.0.0", GOOS: goos, Arch: arch, GoVersion: runtime.Version()}
	if (goos != "linux" && goos != "darwin" && goos != "windows") || (arch != "amd64" && arch != "arm64") {
		return i, fmt.Errorf("unsupported target %s/%s", goos, arch)
	}
	var err error
	if tag != "" {
		i.Version, err = releaseVersion(tag)
		if err != nil {
			return i, err
		}
		i.NativeVersion = i.Version
	}
	i.SourceRevision, err = git(root, "rev-parse", "HEAD")
	if err != nil {
		return i, err
	}
	status, err := git(root, "status", "--porcelain", "--untracked-files=normal")
	if err != nil {
		return i, err
	}
	i.SourceDirty = "clean"
	if status != "" {
		i.SourceDirty = "dirty"
	}
	i.PackageVersion = i.Version
	if tag != "" {
		tagHead, err := git(root, "rev-parse", "--verify", "refs/tags/"+tag+"^{commit}")
		if err != nil || tagHead != i.SourceRevision {
			return i, fmt.Errorf("release tag %q must identify HEAD", tag)
		}
		if i.SourceDirty != "clean" {
			return i, errors.New("release builds require a clean source tree, including untracked files")
		}
	} else {
		i.DevelopedFrom = nearestRelease(root)
		i.PackageVersion = "0.0.0-dev." + i.SourceRevision[:12]
		if i.SourceDirty == "dirty" {
			i.PackageVersion += ".dirty"
		}
	}
	artifactOS := goos
	if goos == "darwin" {
		artifactOS = "macos"
	}
	i.ArtifactName = "aeronautes-" + i.PackageVersion + "-" + artifactOS + "-" + arch + "-UNSIGNED"
	return i, nil
}

func generate(root string, i identity) error {
	for source, dest := range map[string]string{
		"build/darwin/Info.plist":    "Info.plist",
		"build/windows/info.json":    "info.json",
		"build/linux/nfpm/nfpm.yaml": "nfpm.yaml",
	} {
		input, err := os.ReadFile(filepath.Join(root, source))
		if err != nil {
			return err
		}
		t, err := template.New(source).Option("missingkey=error").Parse(string(input))
		if err != nil {
			return err
		}
		var output bytes.Buffer
		if err := t.Execute(&output, i); err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Join(root, metadataDir), 0755); err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(root, metadataDir, dest), output.Bytes(), 0644); err != nil {
			return err
		}
	}
	return nil
}

func buildArgs(i identity, output string, dev bool) []string {
	link := "-X main.version=" + i.Version + " -X main.sourceRevision=" + i.SourceRevision + " -X main.sourceDirty=" + i.SourceDirty + " -X main.developedFrom=" + i.DevelopedFrom
	args := []string{"build", "-buildvcs=false"}
	if dev {
		args = append(args, "-gcflags=all=-l")
	} else {
		args = append(args, "-tags", "production", "-trimpath")
		link = "-w -s " + link
		if i.GOOS == "windows" {
			link += " -H windowsgui"
		}
	}
	return append(args, "-ldflags", link, "-o", output, ".")
}

func run(root string, env []string, name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Dir, cmd.Env = root, env
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	return cmd.Run()
}

func checksum(path string) (string, error) {
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

func writeJSON(path string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0644)
}

func build(root, tag, goos, arch, output string, dev bool) error {
	// A failed rebuild must not leave the previous build eligible for packaging.
	if err := os.Remove(filepath.Join(root, metadataDir, "identity.json")); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if dev && tag != "" {
		return errors.New("DEV=true cannot carry a release version")
	}
	i, err := resolve(root, tag, goos, arch)
	if err != nil {
		return err
	}
	i.Binary = output
	if err := generate(root, i); err != nil {
		return err
	}
	if goos == "windows" {
		syso := "wails_windows_" + arch + ".syso"
		defer os.Remove(filepath.Join(root, syso))
		if err := run(root, os.Environ(), "wails3", "generate", "syso", "-arch", arch, "-icon", "build/windows/icon.ico", "-manifest", "build/windows/wails.exe.manifest", "-info", metadataDir+"/info.json", "-out", syso); err != nil {
			return err
		}
	}
	env := append(os.Environ(), "GOOS="+goos, "GOARCH="+arch)
	if goos == "windows" {
		env = append(env, "CGO_ENABLED=0")
	} else {
		env = append(env, "CGO_ENABLED=1")
	}
	if err := run(root, env, "go", buildArgs(i, output, dev)...); err != nil {
		return err
	}
	after, err := resolve(root, tag, goos, arch)
	if err != nil {
		return err
	}
	// The output is a fixed build input, not a source observation.
	after.Binary = output
	if after != i {
		return errors.New("source identity changed during compilation; rebuild before packaging")
	}
	path := output
	if !filepath.IsAbs(path) {
		path = filepath.Join(root, path)
	}
	i.BinarySHA256, err = checksum(path)
	if err != nil {
		return err
	}
	return writeJSON(filepath.Join(root, metadataDir, "identity.json"), i)
}

func readIdentity(root string) (identity, error) {
	var i identity
	data, err := os.ReadFile(filepath.Join(root, metadataDir, "identity.json"))
	if err != nil {
		return i, err
	}
	if err := json.Unmarshal(data, &i); err != nil {
		return i, err
	}
	path := i.Binary
	if !filepath.IsAbs(path) {
		path = filepath.Join(root, path)
	}
	digest, err := checksum(path)
	if err != nil || digest != i.BinarySHA256 {
		return i, errors.New("binary does not match recorded build identity; rebuild before packaging")
	}
	return i, nil
}

func appimage(i identity) error {
	if i.GOOS != "linux" || i.Arch != runtime.GOARCH {
		return errors.New("AppImage requires a matching native Linux build")
	}
	out := metadataDir + "/appimage"
	if err := os.MkdirAll(out, 0755); err != nil {
		return err
	}
	icon, err := os.ReadFile("build/appicon.png")
	if err != nil {
		return err
	}
	iconPath := metadataDir + "/aeronautes.png"
	if err := os.WriteFile(iconPath, icon, 0644); err != nil {
		return err
	}
	if err := run(".", os.Environ(), "wails3", "generate", "appimage", "-binary", i.Binary, "-icon", iconPath, "-desktopfile", "build/linux/aeronautes.desktop", "-outputdir", out, "-builddir", "build/linux/appimage/build"); err != nil {
		return err
	}
	arch := "x86_64"
	if i.Arch == "arm64" {
		arch = "aarch64"
	}
	return os.Rename(filepath.Join(out, "aeronautes-"+arch+".AppImage"), filepath.Join("bin", i.ArtifactName+".AppImage"))
}

func record(dir string, i identity) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	checksums := make(map[string]string)
	for _, entry := range entries {
		if entry.Name() == i.ArtifactName+".provenance.json" {
			continue
		}
		if entry.IsDir() || !strings.HasPrefix(entry.Name(), i.ArtifactName+".") {
			return fmt.Errorf("unexpected release artifact %q", entry.Name())
		}
		digest, err := checksum(filepath.Join(dir, entry.Name()))
		if err != nil {
			return err
		}
		checksums[entry.Name()] = digest
	}
	if len(checksums) == 0 {
		return errors.New("no release artifacts to record")
	}
	return writeJSON(filepath.Join(dir, i.ArtifactName+".provenance.json"), struct {
		identity
		SHA256 map[string]string `json:"sha256"`
	}{i, checksums})
}

func main() {
	flags := flag.NewFlagSet("build identity", flag.ExitOnError)
	goos := flags.String("os", runtime.GOOS, "target OS")
	arch := flags.String("arch", runtime.GOARCH, "target architecture")
	output := flags.String("output", "bin/aeronautes", "binary output")
	dev := flags.Bool("dev", false, "development build")
	dir := flags.String("dir", "dist", "artifact directory")
	format := flags.String("format", "", "package format (rpm or deb)")
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: go run ./build/identity build|package|appimage|name|record [flags]")
		os.Exit(2)
	}
	flags.Parse(os.Args[2:])
	var err error
	if os.Args[1] == "build" {
		err = build(".", os.Getenv("AERONAUTES_RELEASE_VERSION"), *goos, *arch, *output, *dev)
	} else {
		var i identity
		i, err = readIdentity(".")
		if err == nil {
			switch os.Args[1] {
			case "name":
				fmt.Println(i.ArtifactName)
			case "record":
				err = record(*dir, i)
			case "package":
				if i.GOOS != "linux" || (*format != "rpm" && *format != "deb") {
					err = errors.New("package requires a Linux build and rpm or deb format")
				} else {
					err = run(".", os.Environ(), "wails3", "tool", "package", "-name", i.ArtifactName, "-format", *format, "-config", metadataDir+"/nfpm.yaml", "-out", "bin")
				}
			case "appimage":
				err = appimage(i)
			default:
				err = errors.New("unknown identity command")
			}
		}
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
