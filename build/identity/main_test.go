package main

import (
	"encoding/json"
	"encoding/xml"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func put(t *testing.T, root, name, value string) {
	t.Helper()
	path := filepath.Join(root, name)
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(value), 0644); err != nil {
		t.Fatal(err)
	}
}

func fixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	put(t, root, ".gitignore", "bin/\n*.syso\n")
	put(t, root, "go.mod", "module fixture\n\ngo 1.26.0\n")
	put(t, root, "main.go", `package main
import "fmt"
var version = "dev"
var sourceRevision = "unknown"
var sourceDirty = "unknown"
func main() { fmt.Printf("%s %s %s\n", version, sourceRevision, sourceDirty) }
`)
	for _, name := range []string{"build/darwin/Info.plist", "build/windows/info.json", "build/linux/nfpm/nfpm.yaml"} {
		content, err := os.ReadFile(filepath.Join("../..", name))
		if err != nil {
			t.Fatal(err)
		}
		put(t, root, name, string(content))
	}
	gitOK(t, root, "init", "-q")
	gitOK(t, root, "add", ".")
	gitOK(t, root, "-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid", "commit", "-qm", "fixture")
	gitOK(t, root, "tag", "v1.2.3")
	return root
}

func gitOK(t *testing.T, root string, args ...string) string {
	t.Helper()
	value, err := git(root, args...)
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func TestReleaseVersion(t *testing.T) {
	for _, tag := range []string{"v0.1.0", "v1.2.3", "v65535.65535.65535"} {
		if got, err := releaseVersion(tag); err != nil || got != tag[1:] {
			t.Errorf("%q: %q %v", tag, got, err)
		}
	}
	for _, tag := range []string{"", "1.2.3", "v1.2", "v01.2.3", "v1.2.3-rc.1", "v1.2.3+build", "v65536.1.1", "v1.2.3\n", "v1.2.3 -X main.version=other"} {
		t.Run(tag, func(t *testing.T) {
			if _, err := releaseVersion(tag); err == nil {
				t.Fatal("accepted unsupported release identity")
			}
		})
	}
}

func TestSourceIdentity(t *testing.T) {
	root := fixture(t)
	i, err := resolve(root, "v1.2.3", "linux", "arm64")
	if err != nil || i.SourceRevision != gitOK(t, root, "rev-parse", "HEAD") || i.SourceDirty != "clean" || i.Version != "1.2.3" || i.DevelopedFrom != "" {
		t.Fatalf("release identity: %+v %v", i, err)
	}
	put(t, root, "untracked", "local change")
	if _, err := resolve(root, "v1.2.3", "linux", "amd64"); err == nil {
		t.Fatal("release accepted untracked source")
	}
	i, err = resolve(root, "", "linux", "amd64")
	if err != nil || i.Version != "dev" || i.SourceDirty != "dirty" || !strings.Contains(i.ArtifactName, i.SourceRevision[:12]+".dirty") {
		t.Fatalf("development identity: %+v %v", i, err)
	}
	// A development build names the release it follows; a release names none.
	if i.DevelopedFrom != "1.2.3" {
		t.Fatalf("development build developed from %q, want 1.2.3", i.DevelopedFrom)
	}
	gitOK(t, root, "add", "untracked")
	gitOK(t, root, "-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid", "commit", "-qm", "changed source")
	if _, err := resolve(root, "v1.2.3", "linux", "amd64"); err == nil {
		t.Fatal("release accepted a tag for another commit")
	}
	if _, err := resolve(root, "v9.9.9", "linux", "amd64"); err == nil {
		t.Fatal("release accepted a nonexistent tag")
	}
	if _, err := resolve(root, "", "plan9", "amd64"); err == nil {
		t.Fatal("accepted unsupported platform")
	}
}

func TestDevelopmentBuildWithNoReleaseBehind(t *testing.T) {
	root := fixture(t)
	gitOK(t, root, "tag", "-d", "v1.2.3")
	gitOK(t, root, "tag", "not-a-release")
	i, err := resolve(root, "", "linux", "amd64")
	if err != nil || i.Version != "dev" || i.DevelopedFrom != "" {
		t.Fatalf("identity with no release tag: %+v %v", i, err)
	}
}

func TestTargetIdentityNames(t *testing.T) {
	root := fixture(t)
	for goos, label := range map[string]string{"linux": "linux", "windows": "windows", "darwin": "macos"} {
		for _, arch := range []string{"amd64", "arm64"} {
			t.Run(goos+"/"+arch, func(t *testing.T) {
				i, err := resolve(root, "v1.2.3", goos, arch)
				if err != nil || i.GOOS != goos || i.Arch != arch || i.ArtifactName != "aeronautes-1.2.3-"+label+"-"+arch+"-UNSIGNED" {
					t.Fatalf("target identity: %+v %v", i, err)
				}
			})
		}
	}
}

func TestFixtureReleaseMetadata(t *testing.T) {
	root := fixture(t)
	var prior identity
	for _, tag := range []string{"v1.2.3", "v2.3.4"} {
		if tag == "v2.3.4" {
			put(t, root, "second-release", "different source revision")
			gitOK(t, root, "add", ".")
			gitOK(t, root, "-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid", "commit", "-qm", "second fixture")
			gitOK(t, root, "tag", tag)
		}
		i, err := resolve(root, tag, runtime.GOOS, runtime.GOARCH)
		if err != nil {
			t.Fatal(err)
		}
		if err := generate(root, i); err != nil {
			t.Fatal(err)
		}
		for _, name := range []string{"Info.plist", "info.json", "nfpm.yaml"} {
			data, err := os.ReadFile(filepath.Join(root, metadataDir, name))
			if err != nil {
				t.Fatal(err)
			}
			for _, want := range []string{i.Version, i.SourceRevision, i.SourceDirty} {
				if !strings.Contains(string(data), want) {
					t.Errorf("%s is missing %s", name, want)
				}
			}
			if strings.Contains(string(data), "{{") {
				t.Errorf("%s retains an unrendered template", name)
			}
			switch name {
			case "Info.plist":
				var plist struct {
					Dict struct {
						Keys   []string `xml:"key"`
						Values []string `xml:"string"`
					} `xml:"dict"`
				}
				if err := xml.Unmarshal(data, &plist); err != nil {
					t.Fatal(err)
				}
				values := make(map[string]string)
				for index, key := range plist.Dict.Keys {
					values[key] = plist.Dict.Values[index]
				}
				if values["CFBundleVersion"] != i.Version || values["CFBundleShortVersionString"] != i.Version || values["AeronautesSourceRevision"] != i.SourceRevision || values["AeronautesSourceDirty"] != i.SourceDirty {
					t.Fatalf("incorrect macOS resources: %v", values)
				}
			case "info.json":
				var info struct {
					Fixed map[string]string            `json:"fixed"`
					Info  map[string]map[string]string `json:"info"`
				}
				if err := json.Unmarshal(data, &info); err != nil {
					t.Fatal(err)
				}
				if info.Fixed["file_version"] != i.Version || info.Fixed["product_version"] != i.Version || info.Info["0409"]["FileVersion"] != i.Version || info.Info["0409"]["ProductVersion"] != i.Version || info.Info["0409"]["Comments"] != "Source "+i.SourceRevision+"; "+i.SourceDirty {
					t.Fatalf("incorrect Windows resources: %v", info)
				}
			case "nfpm.yaml":
				if !strings.Contains(string(data), "\nversion: \""+i.Version+"\"\n") || !strings.Contains(string(data), "\narch: \""+i.Arch+"\"\n") {
					t.Fatal("incorrect package version or architecture")
				}
				// ADR 0089: the package carries the verified engine where the
				// resolver probes and depends on no distribution rclone.
				if !strings.Contains(string(data), "\n    dst: \"/usr/local/libexec/aeronautes/rclone\"\n") {
					t.Fatal("the package does not carry the engine in libexec")
				}
				if strings.Contains(string(data), "\n  - rclone\n") || strings.Contains(string(data), "\n      - rclone\n") {
					t.Fatal("the package still depends on the distribution's rclone")
				}
			}
		}
		// A real compiler/linker verifies the flags independently of the
		// renderer; the tiny fixture does not claim native application proof.
		output := filepath.Join(root, "bin", "fixture")
		if runtime.GOOS == "windows" {
			output += ".exe"
		}
		if runtime.GOOS == "windows" {
			cmd := exec.Command("go", buildArgs(i, output, false)...)
			cmd.Dir = root
			if data, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("fixture build: %v\n%s", err, data)
			}
		} else if err := build(root, tag, runtime.GOOS, runtime.GOARCH, output, false); err != nil {
			t.Fatal(err)
		}
		data, err := exec.Command(output).Output()
		if err != nil || string(data) != i.Version+" "+i.SourceRevision+" clean\n" {
			t.Fatalf("linked identity: %q %v", data, err)
		}
		if tag == "v2.3.4" && (prior.Version == i.Version || prior.SourceRevision == i.SourceRevision || prior.ArtifactName == i.ArtifactName) {
			t.Fatal("fixture releases did not identify distinct versions, revisions and filenames")
		}
		prior = i
	}
}

func TestFailedBuildInvalidatesProvenance(t *testing.T) {
	root := fixture(t)
	path := filepath.Join(root, metadataDir, "identity.json")
	put(t, root, metadataDir+"/identity.json", "previous successful build")
	if err := build(root, "v1.2.3-rc.1", "linux", "amd64", "bin/aeronautes", false); err == nil {
		t.Fatal("invalid release input accepted")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("failed build retained stale provenance")
	}
	if err := build(root, "v1.2.3", "linux", "amd64", "bin/aeronautes", true); err == nil {
		t.Fatal("development build accepted a release label")
	}
}

func TestBinaryAndArtifactProvenance(t *testing.T) {
	root := fixture(t)
	i, err := resolve(root, "v1.2.3", "linux", "amd64")
	if err != nil {
		t.Fatal(err)
	}
	i.Binary = "bin/aeronautes"
	put(t, root, i.Binary, "fixture binary bytes")
	i.BinarySHA256, err = checksum(filepath.Join(root, i.Binary))
	if err != nil {
		t.Fatal(err)
	}
	put(t, root, metadataDir+"/identity.json", "")
	if err := writeJSON(filepath.Join(root, metadataDir, "identity.json"), i); err != nil {
		t.Fatal(err)
	}
	if got, err := readIdentity(root); err != nil || got != i {
		t.Fatalf("read identity: %+v %v", got, err)
	}
	dir := t.TempDir()
	if err := record(dir, i); err == nil {
		t.Fatal("empty artifact set accepted")
	}
	put(t, dir, "wrong-version.rpm", "wrong")
	if err := record(dir, i); err == nil {
		t.Fatal("wrong artifact name accepted")
	}
	if err := os.Remove(filepath.Join(dir, "wrong-version.rpm")); err != nil {
		t.Fatal(err)
	}
	name := i.ArtifactName + ".rpm"
	put(t, dir, name, "fixture package bytes")
	if err := record(dir, i); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dir, i.ArtifactName+".provenance.json"))
	if err != nil {
		t.Fatal(err)
	}
	var provenance struct {
		identity
		SHA256 map[string]string `json:"sha256"`
	}
	if err := json.Unmarshal(data, &provenance); err != nil {
		t.Fatal(err)
	}
	want, _ := checksum(filepath.Join(dir, name))
	if provenance.identity != i || provenance.SHA256[name] != want {
		t.Fatalf("incorrect provenance: %s", data)
	}
	if err := record(dir, i); err != nil {
		t.Fatal(err)
	}
	again, err := os.ReadFile(filepath.Join(dir, i.ArtifactName+".provenance.json"))
	if err != nil || string(again) != string(data) {
		t.Fatal("recording again must not add a checksum of the provenance file itself")
	}
	put(t, root, i.Binary, "replaced binary")
	if _, err := readIdentity(root); err == nil {
		t.Fatal("replaced binary accepted")
	}
}

func TestLinuxPackageSelectsTheRecordedBinary(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Linux package output uses a native Linux fixture build")
	}
	for _, kind := range []string{"default", "relative", "absolute"} {
		t.Run(kind, func(t *testing.T) {
			root := fixture(t)
			output := "bin/aeronautes"
			if kind != "default" {
				put(t, root, "bin/aeronautes", "stale default binary")
				output = `bin/custom "output"`
			}
			if kind == "absolute" {
				output = filepath.Join(root, output)
			}
			if err := build(root, "v1.2.3", "linux", runtime.GOARCH, output, false); err != nil {
				t.Fatal(err)
			}
			i, err := readIdentity(root)
			if err != nil {
				t.Fatal(err)
			}
			data, err := os.ReadFile(filepath.Join(root, metadataDir, "nfpm.yaml"))
			if err != nil {
				t.Fatal(err)
			}
			var config struct{ Contents []struct{ Src, Dst string } }
			if err := yaml.Unmarshal(data, &config); err != nil {
				t.Fatal(err)
			}
			selected := ""
			for _, entry := range config.Contents {
				if entry.Dst == "/usr/local/bin/aeronautes" {
					selected = entry.Src
				}
			}
			if filepath.Clean(selected) != filepath.Clean(output) || i.Binary != output {
				t.Fatalf("package selects %q and identity records %q, want compiled output %q", selected, i.Binary, output)
			}
			path := selected
			if !filepath.IsAbs(path) {
				path = filepath.Join(root, path)
			}
			if digest, err := checksum(path); err != nil || digest != i.BinarySHA256 {
				t.Fatalf("package binary digest %q, %v; identity %q", digest, err, i.BinarySHA256)
			}
		})
	}
}
