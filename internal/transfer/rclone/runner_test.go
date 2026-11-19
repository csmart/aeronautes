package rclone

import (
	"context"
	"encoding/pem"
	"fmt"
	"io"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"
)

// stub writes a fake rclone whose behaviour is baked in at generation
// time; it dumps argv and env into dir for hygiene assertions.
func stub(t *testing.T, version string, body string) string {
	t.Helper()
	dir := t.TempDir()
	script := fmt.Sprintf(`#!/usr/bin/env bash
if [ "$1" = "version" ]; then echo "rclone %s"; echo "- os/version: test"; exit 0; fi
printf '%%s\n' "$@" > %q/args
env > %q/env
%s
`, version, dir, dir, body)
	path := filepath.Join(dir, "rclone")
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func dumpDir(stubPath string) string { return filepath.Dir(stubPath) }

func remotes() []Remote {
	return []Remote{
		{Name: "aeronautes_src", StorageURL: "https://src/v1/AUTH_a", Token: "tok-src-SECRET"},
		{Name: "aeronautes_dst", StorageURL: "https://dst/v1/AUTH_b", Token: "tok-dst-SECRET"},
	}
}

func TestPinCheck(t *testing.T) {
	if _, err := NewRunner(Options{Binary: stub(t, "v1.74.3", "exit 0")}); err != nil {
		t.Fatalf("pinned version refused: %v", err)
	}
	if _, err := NewRunner(Options{Binary: stub(t, "v1.99.0", "exit 0")}); err == nil {
		t.Fatal("unpinned version accepted")
	}
	r, err := NewRunner(Options{Binary: stub(t, "v1.99.0", "exit 0"), AllowVersionMismatch: true})
	if err != nil {
		t.Fatal(err)
	}
	if r.Version() != "v1.99.0" {
		t.Errorf("Version = %q", r.Version())
	}
}

func TestCopyHygieneAndContract(t *testing.T) {
	// The list file lives in the runner's ephemeral workdir; the stub
	// copies it out before the runner cleans up.
	bin := stub(t, "v1.74.3",
		`LIST=$(printf '%s\n' "$@" | grep -A1 -x -- --files-from-raw | tail -1); cp "$LIST" "$(dirname "$0")/list"`)
	r, err := NewRunner(Options{Binary: bin, ChunkSize: "1Mi", Transfers: 4, BWLimit: "10M"})
	if err != nil {
		t.Fatal(err)
	}
	result, err := r.Copy(context.Background(), remotes(),
		"aeronautes_src:media", "aeronautes_dst:media/folder", []string{"a b", "photos/x.jpg"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.ExitCode != 0 {
		t.Errorf("exit = %d", result.ExitCode)
	}

	args, err := os.ReadFile(filepath.Join(dumpDir(bin), "args"))
	if err != nil {
		t.Fatal(err)
	}
	argv := string(args)
	// Tokens never in argv (§26.2).
	if strings.Contains(argv, "SECRET") {
		t.Fatalf("token leaked into argv:\n%s", argv)
	}
	for _, want := range []string{"copy", "--no-traverse", "--ignore-times", "--files-from-raw", "--use-json-log", "-vv", "--no-unicode-normalization",
		"--retries", "--low-level-retries", "--swift-chunk-size", "1Mi", "--transfers", "4", "--bwlimit", "10M", "--config"} {
		if !strings.Contains(argv, want) {
			t.Errorf("argv missing %q", want)
		}
	}
	if strings.Contains(argv, "--max-duration") || strings.Contains(argv, "leave-parts") {
		t.Error("forbidden flag present (docs/rclone.md never-set list)")
	}

	env, err := os.ReadFile(filepath.Join(dumpDir(bin), "env"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"RCLONE_CONFIG_AERONAUTES_SRC_STORAGE_URL=http://127.0.0.1:",
		"RCLONE_CONFIG_AERONAUTES_SRC_TYPE=swift",
	} {
		if !strings.Contains(string(env), want) {
			t.Errorf("env missing %q", want)
		}
	}
	if strings.Contains(string(env), "SECRET") || strings.Contains(string(env), "https://") {
		t.Fatal("upstream credentials or endpoint reached the child environment")
	}
	values := map[string]string{}
	for line := range strings.SplitSeq(string(env), "\n") {
		key, value, _ := strings.Cut(line, "=")
		values[key] = value
	}
	for _, role := range []string{"SRC", "DST"} {
		prefix := "RCLONE_CONFIG_AERONAUTES_" + role
		if len(values[prefix+"_AUTH_TOKEN"]) < 26 {
			t.Fatal("child gateway token missing")
		}
		endpoint, err := url.Parse(values[prefix+"_STORAGE_URL"])
		if err != nil || endpoint.Hostname() != "127.0.0.1" || endpoint.Scheme != "http" {
			t.Fatal("child endpoint is not the literal loopback gateway")
		}
	}
	if values["RCLONE_CONFIG_AERONAUTES_SRC_AUTH_TOKEN"] == values["RCLONE_CONFIG_AERONAUTES_DST_AUTH_TOKEN"] {
		t.Fatal("remotes share a local token")
	}

	// The files list rode along with exact names.
	list, err := os.ReadFile(filepath.Join(dumpDir(bin), "list"))
	if err != nil {
		t.Fatal(err)
	}
	if string(list) != "a b\nphotos/x.jpg\n" {
		t.Errorf("list = %q", list)
	}
}

// The child's size check stays on unless the caller turns it off, which
// only a segmented upload does (ADR 0111 decision 2).
func TestIgnoreSizeOnlyWhenAsked(t *testing.T) {
	for _, ignore := range []bool{false, true} {
		bin := stub(t, "v1.74.3", "exit 0")
		r, err := NewRunner(Options{Binary: bin, IgnoreSize: ignore})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := r.Copy(t.Context(), remotes(), "aeronautes_src:media", "aeronautes_dst:media/folder", []string{"a"}, nil); err != nil {
			t.Fatal(err)
		}
		args, err := os.ReadFile(filepath.Join(dumpDir(bin), "args"))
		if err != nil {
			t.Fatal(err)
		}
		if got := slices.Contains(strings.Split(string(args), "\n"), "--ignore-size"); got != ignore {
			t.Errorf("IgnoreSize %t: --ignore-size passed = %t", ignore, got)
		}
	}
}

// A download into a local folder is told to leave a file that exists alone
// (ADR 0122 decision 5); a copy to a store is never told so, because there
// the plan decides what is replaced and the engine must not skip
// (K-XFER-009).
func TestLeaveExistingOnlyForALocalDestination(t *testing.T) {
	for _, tc := range []struct {
		name, dst string
		leave     bool
		passed    bool
		refused   bool
	}{
		{name: "a download, told to", dst: t.TempDir(), leave: true, passed: true},
		{name: "a download, not told to", dst: t.TempDir()},
		{name: "a copy to a store", dst: "aeronautes_dst:media/folder", leave: true, refused: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bin := stub(t, "v1.74.3", "exit 0")
			r, err := NewRunner(Options{Binary: bin, LeaveExisting: tc.leave})
			if err != nil {
				t.Fatal(err)
			}
			_, err = r.Copy(t.Context(), remotes(), "aeronautes_src:media", tc.dst, []string{"a"}, nil)
			if tc.refused {
				if err == nil {
					t.Fatal("a copy to a store was told to leave existing objects alone")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			args, err := os.ReadFile(filepath.Join(dumpDir(bin), "args"))
			if err != nil {
				t.Fatal(err)
			}
			if got := slices.Contains(strings.Split(string(args), "\n"), "--ignore-existing"); got != tc.passed {
				t.Errorf("--ignore-existing passed = %t, want %t", got, tc.passed)
			}
		})
	}
}

// ADR 0122 decision 12: a confirmed upload or download lets the engine compare
// each name as rclone copy does, so it is not given the flag that forces every
// transfer; every other run keeps it, and a copy between stores is refused the
// option, because its plan decides what is the same.
func TestCompareExistingDropsTheForcingFlagOnlyWhereItIsAllowed(t *testing.T) {
	for _, tc := range []struct {
		name, src, dst string
		compare, leave bool
		forced         bool
		refused        bool
	}{
		{name: "a confirmed download", src: "aeronautes_src:media", dst: t.TempDir(), compare: true},
		{name: "a confirmed upload", src: t.TempDir(), dst: "aeronautes_dst:media/up", compare: true},
		{name: "an unconfirmed download", src: "aeronautes_src:media", dst: t.TempDir(), leave: true, forced: true},
		{name: "an unconfirmed upload", src: t.TempDir(), dst: "aeronautes_dst:media/up", forced: true},
		{name: "a copy between stores", src: "aeronautes_src:media", dst: "aeronautes_dst:media", compare: true, refused: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bin := stub(t, "v1.74.3", "exit 0")
			r, err := NewRunner(Options{Binary: bin, CompareExisting: tc.compare, LeaveExisting: tc.leave})
			if err != nil {
				t.Fatal(err)
			}
			_, err = r.Copy(t.Context(), remotes(), tc.src, tc.dst, []string{"a"}, nil)
			if tc.refused {
				if err == nil {
					t.Fatal("a copy between stores was told to compare")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			args, err := os.ReadFile(filepath.Join(dumpDir(bin), "args"))
			if err != nil {
				t.Fatal(err)
			}
			argv := strings.Split(string(args), "\n")
			if got := slices.Contains(argv, "--ignore-times"); got != tc.forced {
				t.Errorf("--ignore-times passed = %t, want %t", got, tc.forced)
			}
			if got := slices.Contains(argv, "--ignore-existing"); got != tc.leave {
				t.Errorf("--ignore-existing passed = %t, want %t", got, tc.leave)
			}
		})
	}
}

func TestNewlineNamesRefused(t *testing.T) {
	r, err := NewRunner(Options{Binary: stub(t, "v1.74.3", "exit 0")})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.Copy(context.Background(), remotes(), "s:c", "d:c", []string{"line\nbreak"}, nil); err == nil {
		t.Fatal("newline name accepted by the runner")
	}
}

func TestChildConfigOmitsUpstreamTLS(t *testing.T) {
	// Equal captured trust remains a supported pairing, even though the
	// gateway now keeps the CA out of the child configuration.
	bin := stub(t, "v1.74.3",
		`CONF=$(printf '%s\n' "$@" | grep -A1 -x -- --config | tail -1); cp "$CONF" "$(dirname "$0")/conf"`)
	r, err := NewRunner(Options{Binary: bin})
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	caA, caB := filepath.Join(dir, "a.pem"), filepath.Join(dir, "b.pem")
	server := httptest.NewTLSServer(nil)
	defer server.Close()
	ca := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw})
	for _, p := range []string{caA, caB} {
		if err := os.WriteFile(p, ca, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	tlsRemotes := []Remote{
		{Name: "aeronautes_src", StorageURL: "https://src/v1/AUTH_a", Token: "tok-src-SECRET", CACertFile: caA},
		{Name: "aeronautes_dst", StorageURL: "https://dst/v1/AUTH_b", Token: "tok-dst-SECRET", CACertFile: caB},
	}
	if _, err := r.Copy(context.Background(), tlsRemotes, "aeronautes_src:c", "aeronautes_dst:c/folder", []string{"x"}, nil); err != nil {
		t.Fatal(err)
	}
	conf, err := os.ReadFile(filepath.Join(dumpDir(bin), "conf"))
	if err != nil {
		t.Fatal(err)
	}
	if len(conf) != 0 {
		t.Fatal("child config should be empty; upstream trust belongs to the gateway")
	}
}

func TestNonIdenticalTrustRefused(t *testing.T) {
	// The §12.3 pairing matrix: overrides leak across remotes in one
	// child, so ONLY equal trust identities may share it. These pairs
	// blessed the leak before the review wave; they must refuse.
	bin := stub(t, "v1.74.3", "exit 0")
	r, err := NewRunner(Options{Binary: bin})
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	caOne, caTwo := filepath.Join(dir, "one.pem"), filepath.Join(dir, "two.pem")
	os.WriteFile(caOne, []byte("bundle-one"), 0o600)
	os.WriteFile(caTwo, []byte("bundle-two"), 0o600)
	refuse := [][2]Remote{
		{{Name: "aeronautes_src", StorageURL: "https://a", Token: "t1", CACertFile: caOne},
			{Name: "aeronautes_dst", StorageURL: "https://b", Token: "t2", InsecureTLS: true}},
		{{Name: "aeronautes_src", StorageURL: "https://a", Token: "t1", CACertFile: caOne},
			{Name: "aeronautes_dst", StorageURL: "https://b", Token: "t2", CACertFile: caTwo}},
		{{Name: "aeronautes_src", StorageURL: "https://a", Token: "t1", InsecureTLS: true},
			{Name: "aeronautes_dst", StorageURL: "https://b", Token: "t2"}},
	}
	for i, pair := range refuse {
		if _, err := r.Copy(context.Background(), pair[:], "aeronautes_src:c", "aeronautes_dst:c/folder", []string{"x"}, nil); err == nil {
			t.Errorf("pair %d: non-identical trust accepted in one child (§12.3)", i)
		}
	}
	// Equal identities pass: both insecure, and both system trust.
	allow := [][2]Remote{
		{{Name: "aeronautes_src", StorageURL: "https://a", Token: "t1", InsecureTLS: true},
			{Name: "aeronautes_dst", StorageURL: "https://b", Token: "t2", InsecureTLS: true}},
		{{Name: "aeronautes_src", StorageURL: "https://a", Token: "t1"},
			{Name: "aeronautes_dst", StorageURL: "https://b", Token: "t2"}},
	}
	for i, pair := range allow {
		if _, err := r.Copy(context.Background(), pair[:], "aeronautes_src:c", "aeronautes_dst:c/folder", []string{"x"}, nil); err != nil {
			t.Errorf("pair %d: identical trust refused: %v", i, err)
		}
	}
	// An unreadable CA bundle fails closed.
	broken := []Remote{{Name: "aeronautes_src", StorageURL: "https://a", Token: "t1", CACertFile: filepath.Join(dir, "missing.pem")}}
	if _, err := r.Copy(context.Background(), broken, "aeronautes_src:c", "aeronautes_dst:c/folder", []string{"x"}, nil); err == nil {
		t.Error("unreadable CA bundle did not fail closed")
	}
}

func TestMixedTrustRefused(t *testing.T) {
	r, err := NewRunner(Options{Binary: stub(t, "v1.74.3", "exit 0")})
	if err != nil {
		t.Fatal(err)
	}
	mixed := []Remote{
		{Name: "aeronautes_src", StorageURL: "https://a", Token: "t1", CACertFile: "/tmp/ca.pem"},
		{Name: "aeronautes_dst", StorageURL: "https://b", Token: "t2"},
	}
	if _, err := r.Copy(context.Background(), mixed, "aeronautes_src:c", "aeronautes_dst:c/folder", []string{"x"}, nil); err == nil {
		t.Fatal("mixed TLS trust in one child accepted (§12.3 forbids it)")
	}
}

func TestStreamParsing(t *testing.T) {
	body := `cat >&2 << 'LOG'
{"level":"debug","msg":"Uploading segment file \"video/2026-01-01-abc/00000000\" into \"media_segments\""}
{"level":"debug","msg":"Uploading segment file \"video/2026-01-01-abc/00000001\" into \"media_segments\""}
{"level":"error","msg":"boom on one object","object":"bad.bin"}
{"level":"info","msg":"stats","stats":{"bytes":2048,"totalBytes":4096,"transfers":1,"totalTransfers":2,"speed":128.5,"errors":1}}
not-json noise line
LOG
exit 3`
	r, err := NewRunner(Options{Binary: stub(t, "v1.74.3", body)})
	if err != nil {
		t.Fatal(err)
	}
	var events []Event
	result, err := r.Copy(context.Background(), remotes(), "aeronautes_src:m", "aeronautes_dst:m/folder", []string{"x"},
		func(e Event) { events = append(events, e) })
	if err != nil {
		t.Fatal(err)
	}
	if result.ExitCode != 3 {
		t.Errorf("exit = %d", result.ExitCode)
	}
	if len(result.SegmentPrefixes) != 1 ||
		result.SegmentPrefixes[0] != (SegmentPrefix{Container: "media_segments", Prefix: "video/2026-01-01-abc/"}) {
		t.Errorf("prefixes = %+v, want one deduped capture", result.SegmentPrefixes)
	}
	if len(result.Errors) != 1 || result.Errors[0] != "boom on one object" {
		t.Errorf("errors = %v", result.Errors)
	}
	if result.Stats.Bytes != 2048 || result.Stats.TotalTransfers != 2 || result.Stats.Errors != 1 {
		t.Errorf("stats = %+v", result.Stats)
	}
	if len(events) < 4 {
		t.Errorf("events streamed = %d", len(events))
	}
}

// The capture reads the container out of the child's own notice, so what it
// records depends on a line shape only the engine decides. These three lines are
// REAL, captured from the pinned binary against SAIO on 2026-10-02 and retained
// in docs/experiments/2026-10-02-engine-claims-measured.md: once with the
// segments sidecar the engine chooses by itself, once with it demanded, and once
// with it refused — which is the case worth having, because then the segments
// live inside the DATA container under a dotted prefix and the container the
// notice names is the data container itself.
func TestPrefixFromEventOnThePinnedEnginesOwnLines(t *testing.T) {
	for _, c := range []struct {
		name      string
		msg       string
		container string
		prefix    string
	}{
		{
			name:      "sidecar chosen by the engine",
			msg:       `Uploading segment file "seg-unset.bin/2026-10-02-224341-wzs9Le7-p-pgnmOAv9WNNQ/00000000" into "engine-claims-p3_segments"`,
			container: "engine-claims-p3_segments",
			prefix:    "seg-unset.bin/2026-10-02-224341-wzs9Le7-p-pgnmOAv9WNNQ/",
		},
		{
			name:      "sidecar demanded",
			msg:       `Uploading segment file "seg-true.bin/2026-10-02-224341-gcY5WjeBBSuYaCw5a5BguA/00000000" into "engine-claims-p3_segments"`,
			container: "engine-claims-p3_segments",
			prefix:    "seg-true.bin/2026-10-02-224341-gcY5WjeBBSuYaCw5a5BguA/",
		},
		{
			name:      "sidecar refused, so the segments are in the data container",
			msg:       `Uploading segment file ".file-segments/seg-false.bin/2026-10-02-224341-ypxbwtHMdDyq1ljLQhYWzQ/00000000" into "engine-claims-p3"`,
			container: "engine-claims-p3",
			prefix:    ".file-segments/seg-false.bin/2026-10-02-224341-ypxbwtHMdDyq1ljLQhYWzQ/",
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			got, ok := PrefixFromEvent(Event{Msg: c.msg})
			if !ok {
				t.Fatalf("the engine's own notice was not recognised: %s", c.msg)
			}
			want := SegmentPrefix{Container: c.container, Prefix: c.prefix}
			if got != want {
				t.Errorf("captured %+v, want %+v", got, want)
			}
			if got.Canonical() != want {
				t.Errorf("canonical form %+v differs from the captured one", got.Canonical())
			}
		})
	}
}

func TestCancelEscalation(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "got-int")
	body := fmt.Sprintf(`trap 'touch %q; exit 130' INT
sleep 30 & wait $!`, marker)
	r, err := NewRunner(Options{Binary: stub(t, "v1.74.3", body)})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(500 * time.Millisecond); cancel() }()

	start := time.Now()
	result, err := r.Copy(ctx, remotes(), "aeronautes_src:m", "aeronautes_dst:m/folder", []string{"x"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Cancelled {
		t.Error("Cancelled not set")
	}
	if _, err := os.Stat(marker); err != nil {
		t.Error("child never saw SIGINT (graceful-first rule)")
	}
	if elapsed := time.Since(start); elapsed > 15*time.Second {
		t.Errorf("cancel took %v", elapsed)
	}
}

// withExecutableDir points sidecar resolution at a test directory.
func withExecutableDir(t *testing.T, dir string) {
	t.Helper()
	previous := executableDir
	executableDir = func() (string, error) { return dir, nil }
	t.Cleanup(func() { executableDir = previous })
}

func TestSidecarIntegrity(t *testing.T) {
	// A tampered sidecar refuses loudly and never falls back to PATH.
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "rclone"), []byte("#!/bin/sh\necho tampered\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	withExecutableDir(t, dir)
	_, err := NewRunner(Options{})
	if err == nil || !strings.Contains(err.Error(), "integrity") {
		t.Fatalf("tampered sidecar accepted: %v", err)
	}

	// No sidecar: resolution falls through (to PATH or an explicit
	// binary; here the stub proves the explicit path still works).
	withExecutableDir(t, t.TempDir())
	if _, err := NewRunner(Options{Binary: stub(t, "v1.74.3", "exit 0")}); err != nil {
		t.Fatalf("no-sidecar fallback: %v", err)
	}
}

// TestSidecarOfficialBinary runs only when the verified official build
// is present (fetched by scripts/fetch-rclone.sh in CI or locally):
// the sidecar must be selected over PATH and pass both checksum and
// version pin.
func TestSidecarOfficialBinary(t *testing.T) {
	official := os.Getenv("AERONAUTES_OFFICIAL_RCLONE_DIR")
	if official == "" {
		t.Skip("AERONAUTES_OFFICIAL_RCLONE_DIR not set")
	}
	withExecutableDir(t, official)
	runner, err := NewRunner(Options{})
	if err != nil {
		t.Fatal(err)
	}
	if runner.Version() != PinnedVersion {
		t.Fatalf("sidecar version = %s", runner.Version())
	}
	if !strings.HasPrefix(runner.binary, official) {
		t.Fatalf("sidecar not selected: %s", runner.binary)
	}
}

func TestArgvHygiene(t *testing.T) {
	// The child argv closes flags with "--" before positionals, refuses
	// an empty selected set, and refuses dash-leading paths outright.
	bin := stub(t, "v1.74.3", `printf '%s\n' "$@" > "$(dirname "$0")/argv"`)
	r, err := NewRunner(Options{Binary: bin})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, err := r.Copy(ctx, remotes(), "aeronautes_src:c", "aeronautes_dst:c/folder", nil, nil); err == nil {
		t.Error("empty selected set accepted: whole-scope copy possible")
	}
	if _, err := r.Copy(ctx, remotes(), "-aeronautes_src:c", "aeronautes_dst:c/folder", []string{"x"}, nil); err == nil {
		t.Error("dash-leading source accepted")
	}
	if _, err := r.Copy(ctx, remotes(), "aeronautes_src:c", "--no-check-certificate", []string{"x"}, nil); err == nil {
		t.Error("flag-shaped destination accepted")
	}
	if _, err := r.Copy(ctx, remotes(), "aeronautes_src:c", "aeronautes_dst:c/folder", []string{"x"}, nil); err != nil {
		t.Fatal(err)
	}
	argv, err := os.ReadFile(filepath.Join(dumpDir(bin), "argv"))
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(argv)), "\n")
	if len(lines) < 3 || lines[len(lines)-3] != "--" ||
		lines[len(lines)-2] != "aeronautes_src:c" || lines[len(lines)-1] != "aeronautes_dst:c/folder" {
		t.Errorf("argv does not end with -- src dst:\n%s", argv)
	}
	// The verb allowlist and the forbidden surface, locked (review
	// K-XFER-001/009/012/014/015 test-gap note): the FIRST argv word is
	// exactly the copy verb, and none of the destructive or
	// semantics-changing flags may ever appear.
	if lines[0] != "copy" {
		t.Errorf("first argv word = %q, want copy", lines[0])
	}
	forbidden := []string{"sync", "purge", "delete", "deletefile", "move", "moveto",
		"--ignore-existing", "--server-side-across-configs", "--swift-no-large-objects",
		"--swift-leave-parts-on-error"}
	for _, line := range lines {
		for _, bad := range forbidden {
			if line == bad {
				t.Errorf("forbidden token %q in argv", bad)
			}
		}
	}
	// Control characters in remote values refuse before any INI write.
	evil := []Remote{{Name: "aeronautes_src", StorageURL: "https://a/v1\nInjected = true", Token: "t"}}
	if _, err := r.Copy(ctx, evil, "aeronautes_src:c", "aeronautes_dst:c/folder", []string{"x"}, nil); err == nil {
		t.Error("control character in storage URL accepted")
	}
}

// ADR 0122 decisions 3 and 4: a remote destination is the names themselves,
// a folder or the container's top, and a ".." segment is refused.
func TestARemoteDestinationIsTheNamesThemselves(t *testing.T) {
	live, err := NewRunner(Options{Binary: stub(t, "v1.74.3", "exit 0")})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	for _, dst := range []string{"aeronautes_dst:c/folder", "aeronautes_dst:c/folder/sub", "aeronautes_dst:c"} {
		if _, err := live.Copy(ctx, remotes(), t.TempDir(), dst, []string{"x"}, nil); err != nil {
			t.Errorf("a live destination %q was refused: %v", dst, err)
		}
	}
	if _, err := live.Copy(ctx, remotes(), t.TempDir(), "aeronautes_dst:c/folder/../other", []string{"x"}, nil); err == nil {
		t.Error("a destination with a '..' segment was accepted")
	}
}

// The sidecar directories per platform (ADR 0089 decision 3): beside
// the executable everywhere, then the Linux package layout on Linux only.
func TestSidecarDirs(t *testing.T) {
	exe := filepath.Join(string(filepath.Separator), "usr", "local", "bin")
	libexec := filepath.Join(string(filepath.Separator), "usr", "local", "libexec", "aeronautes")
	for _, tc := range []struct {
		goos string
		want []sidecarDir
	}{
		{"linux", []sidecarDir{{exe, SourceBeside}, {libexec, SourceLibexec}}},
		{"darwin", []sidecarDir{{exe, SourceBeside}}},
		{"windows", []sidecarDir{{exe, SourceBeside}}},
	} {
		got := sidecarDirs(tc.goos, exe)
		if len(got) != len(tc.want) {
			t.Fatalf("%s: sidecarDirs = %v, want %v", tc.goos, got, tc.want)
		}
		for i := range got {
			if got[i] != tc.want[i] {
				t.Errorf("%s: sidecarDirs[%d] = %v, want %v", tc.goos, i, got[i], tc.want[i])
			}
		}
	}
}

// copyFile installs src at dst, executable, for sidecar fixtures.
func copyFile(t *testing.T, src, dst string) {
	t.Helper()
	in, err := os.Open(src)
	if err != nil {
		t.Fatal(err)
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o755)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(out, in); err != nil {
		t.Fatal(err)
	}
	if err := out.Close(); err != nil {
		t.Fatal(err)
	}
}

// A Linux package's engine lives in ../libexec/aeronautes relative to
// the executable (ADR 0089 decision 3). Tampering there refuses and
// names the path, with no fall-through to PATH; the official binary
// there is selected and verified; a binary beside the executable still
// wins over it.
func TestLibexecSidecar(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("the libexec layout is probed on Linux only")
	}
	prefix := t.TempDir()
	bin := filepath.Join(prefix, "bin")
	libexec := filepath.Join(prefix, "libexec", "aeronautes")
	for _, dir := range []string{bin, libexec} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	withExecutableDir(t, bin)

	t.Run("tampered libexec refuses", func(t *testing.T) {
		if err := os.WriteFile(filepath.Join(libexec, "rclone"), []byte("#!/bin/sh\necho tampered\n"), 0o755); err != nil {
			t.Fatal(err)
		}
		_, err := Resolve(Options{})
		if err == nil || !strings.Contains(err.Error(), "integrity") || !strings.Contains(err.Error(), libexec) {
			t.Fatalf("tampered libexec sidecar accepted or misreported: %v", err)
		}
		if report := Describe(); report.Error == "" || report.Verified || report.Pin != PinnedVersion {
			t.Fatalf("Describe hid the refusal: %+v", report)
		}
	})

	official := os.Getenv("AERONAUTES_OFFICIAL_RCLONE_DIR")
	if official == "" {
		t.Skip("AERONAUTES_OFFICIAL_RCLONE_DIR not set; the verified libexec cases need the official binary")
	}
	t.Run("official libexec selected and verified", func(t *testing.T) {
		copyFile(t, filepath.Join(official, "rclone"), filepath.Join(libexec, "rclone"))
		resolution, err := Resolve(Options{})
		if err != nil {
			t.Fatal(err)
		}
		want := Resolution{Path: filepath.Join(libexec, "rclone"), Version: PinnedVersion, Source: SourceLibexec, Verified: true}
		if resolution != want {
			t.Fatalf("Resolve = %+v, want %+v", resolution, want)
		}
		report := Describe()
		if report.Error != "" || !report.Verified || report.Source != SourceLibexec || report.Version != PinnedVersion || report.Path != want.Path {
			t.Fatalf("Describe = %+v", report)
		}
	})
	t.Run("beside the executable wins over libexec", func(t *testing.T) {
		copyFile(t, filepath.Join(official, "rclone"), filepath.Join(bin, "rclone"))
		resolution, err := Resolve(Options{})
		if err != nil {
			t.Fatal(err)
		}
		if resolution.Source != SourceBeside || resolution.Path != filepath.Join(bin, "rclone") || !resolution.Verified {
			t.Fatalf("Resolve = %+v, want the sidecar beside the executable", resolution)
		}
	})
}

// With no sidecar and no rclone on PATH the report carries the refusal
// and the pin, and claims nothing verified.
func TestDescribeReportsTheRefusal(t *testing.T) {
	withExecutableDir(t, t.TempDir())
	t.Setenv("PATH", t.TempDir())
	report := Describe()
	if report.Error == "" || report.Verified || report.Pin != PinnedVersion || report.Engine != "rclone" || report.Path != "" {
		t.Fatalf("Describe = %+v", report)
	}
	if !strings.Contains(report.Error, "libexec/aeronautes") || !strings.Contains(report.Error, "PATH") {
		t.Fatalf("the refusal does not say where it looked: %s", report.Error)
	}
}
