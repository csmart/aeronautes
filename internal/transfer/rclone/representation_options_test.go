package rclone

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestCopyRepresentationOptionArgs(t *testing.T) {
	for _, command := range []string{"copy"} {
		for _, enabled := range []bool{false, true} {
			policy := "default"
			if enabled {
				policy = "opaque"
			}
			t.Run(command+"/"+policy, func(t *testing.T) {
				bin := stub(t, PinnedVersion, `
set -e
while [ "$#" -gt 0 ]; do
  case "$1" in
    --config) cp "$2" "$(dirname "$0")/config"; shift ;;
    --files-from-raw) cp "$2" "$(dirname "$0")/list"; shift ;;
  esac
  shift
done
`)
				runner, err := NewRunner(Options{
					Binary: bin, NoGzipEncoding: enabled, ChunkSize: "1Mi",
					Transfers: 7, Checkers: 9, TPSLimit: 11, BWLimit: "10M", BufferSize: "16Mi",
				})
				if err != nil {
					t.Fatal(err)
				}
				src, dst := "aeronautes_src:source", "aeronautes_dst:destination/folder"
				result, err := runner.Copy(t.Context(), remotes(), src, dst, []string{"a b", "photos/x.jpg"}, nil)
				if err != nil || result == nil || result.ExitCode != 0 || result.Cancelled || result.LogStreamErr != "" {
					t.Fatalf("child: result=%+v error=%v", result, err)
				}
				read := func(name string) string {
					t.Helper()
					body, err := os.ReadFile(filepath.Join(dumpDir(bin), name))
					if err != nil {
						t.Fatal(err)
					}
					return string(body)
				}
				args := strings.Split(strings.TrimSuffix(read("args"), "\n"), "\n")
				for i, arg := range args {
					if arg == "--config" || arg == "--files-from-raw" {
						if i+1 == len(args) {
							t.Fatalf("missing argument after %s", arg)
						}
						if _, err := os.Stat(args[i+1]); !os.IsNotExist(err) {
							t.Errorf("child input remains after exit: %s: %v", arg, err)
						}
						args[i+1] = "<private>"
					}
				}
				want := []string{command, "--no-traverse", "--ignore-times", "--use-json-log", "-vv", "--stats", "1s", "--no-unicode-normalization", "--files-from-raw", "<private>"}
				if got := read("list"); got != "a b\nphotos/x.jpg\n" {
					t.Errorf("selected names = %q", got)
				}
				want = append(want, "--config", "<private>", "--retries", "1", "--low-level-retries", "2",
					"--swift-chunk-size", "1Mi", "--transfers", "7", "--checkers", "9", "--tpslimit", "11", "--bwlimit", "10M", "--buffer-size", "16Mi")
				if enabled {
					want = append(want, "--no-gzip-encoding")
				}
				want = append(want, "--", src, dst)
				if !reflect.DeepEqual(args, want) {
					t.Errorf("argv = %q; want %q", args, want)
				}
				if read("config") != "" {
					t.Error("child configuration contains data")
				}
				for _, captured := range []string{read("args"), read("env")} {
					for _, remote := range remotes() {
						if strings.Contains(captured, remote.Token) || strings.Contains(captured, remote.StorageURL) {
							t.Error("upstream credentials or endpoint reached the child")
						}
					}
				}
			})
		}
	}
}
