package rclone

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
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
		{Name: "aeronautes_src", StorageURL: "http://src/v1/AUTH_a", Token: "tok-src-SECRET"},
		{Name: "aeronautes_dst", StorageURL: "http://dst/v1/AUTH_b", Token: "tok-dst-SECRET"},
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
		"aeronautes_src:media", "aeronautes_dst:media", []string{"a b", "photos/x.jpg"}, nil)
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
	for _, want := range []string{"copy", "--ignore-times", "--files-from-raw", "--use-json-log", "-vv",
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
		"RCLONE_CONFIG_AERONAUTES_SRC_AUTH_TOKEN=tok-src-SECRET",
		"RCLONE_CONFIG_AERONAUTES_DST_AUTH_TOKEN=tok-dst-SECRET",
		"RCLONE_CONFIG_AERONAUTES_SRC_STORAGE_URL=http://src/v1/AUTH_a",
		"RCLONE_CONFIG_AERONAUTES_SRC_TYPE=swift",
	} {
		if !strings.Contains(string(env), want) {
			t.Errorf("env missing %q", want)
		}
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

func TestNewlineNamesRefused(t *testing.T) {
	r, err := NewRunner(Options{Binary: stub(t, "v1.74.3", "exit 0")})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.Copy(context.Background(), remotes(), "s:c", "d:c", []string{"line\nbreak"}, nil); err == nil {
		t.Fatal("newline name accepted by the runner")
	}
}

func TestTLSConfigFile(t *testing.T) {
	bin := stub(t, "v1.74.3",
		`CONF=$(printf '%s\n' "$@" | grep -A1 -x -- --config | tail -1); cp "$CONF" "$(dirname "$0")/conf"`)
	r, err := NewRunner(Options{Binary: bin})
	if err != nil {
		t.Fatal(err)
	}
	tlsRemotes := []Remote{
		{Name: "aeronautes_src", StorageURL: "https://src/v1/AUTH_a", Token: "tok-src-SECRET", CACertFile: "/tmp/ca.pem"},
		{Name: "aeronautes_dst", StorageURL: "https://dst/v1/AUTH_b", Token: "tok-dst-SECRET", InsecureTLS: true},
	}
	if _, err := r.Copy(context.Background(), tlsRemotes, "aeronautes_src:c", "aeronautes_dst:c", nil, nil); err != nil {
		t.Fatal(err)
	}
	conf, err := os.ReadFile(filepath.Join(dumpDir(bin), "conf"))
	if err != nil {
		t.Fatal(err)
	}
	body := string(conf)
	for _, want := range []string{"[aeronautes_src]", "override.ca_cert = /tmp/ca.pem",
		"[aeronautes_dst]", "override.no_check_certificate = true", "storage_url = https://src/v1/AUTH_a"} {
		if !strings.Contains(body, want) {
			t.Errorf("config missing %q:\n%s", want, body)
		}
	}
	// The config file carries NO secrets (§26.2): tokens stay in env.
	if strings.Contains(body, "SECRET") || strings.Contains(body, "auth_token") {
		t.Fatalf("secret material in the config file:\n%s", body)
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
	if _, err := r.Copy(context.Background(), mixed, "aeronautes_src:c", "aeronautes_dst:c", nil, nil); err == nil {
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
	result, err := r.Copy(context.Background(), remotes(), "aeronautes_src:m", "aeronautes_dst:m", nil,
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
	result, err := r.Copy(ctx, remotes(), "aeronautes_src:m", "aeronautes_dst:m", nil, nil)
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
