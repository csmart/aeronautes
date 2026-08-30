package integration

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"
	"testing"

	"github.com/csmart/aeronautes/internal/swiftcontrol"
	"github.com/csmart/aeronautes/internal/transfer/rclone"
	"github.com/ncw/swift/v2"
)

// TestRcloneMatrix is the §65.7 compatibility harness: it drives ONE
// rclone binary (AERONAUTES_MATRIX_RCLONE) through the behaviours the
// product depends on and REPORTS findings as MATRIX| lines for
// scripts/rclone-matrix.sh to collect — it only fails on harness
// errors, never on behavioural differences, because its job is to
// measure a candidate, not to gate it. The recorded results in
// docs/rclone.md are what gate a pin change.
func TestRcloneMatrix(t *testing.T) {
	binary := os.Getenv("AERONAUTES_MATRIX_RCLONE")
	if binary == "" {
		t.Skip("AERONAUTES_MATRIX_RCLONE not set")
	}
	h := harness(t)
	if h.Session2() == nil {
		skipOrFailSecondAccount(t)
	}
	ctx := context.Background()
	report := func(key string, format string, args ...any) {
		t.Logf("MATRIX|%s|%s", key, fmt.Sprintf(format, args...))
	}

	runner, err := rclone.NewRunner(rclone.Options{
		Binary: binary, AllowVersionMismatch: true,
		ChunkSize: "1Mi", Retries: 1, LowLevelRetries: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	report("version", "%s", runner.Version())

	// Capability probe: which file-list flags exist.
	help, _ := exec.Command(binary, "copy", "--help").CombinedOutput()
	report("flag.files-from-raw", "%v", bytes.Contains(help, []byte("--files-from-raw")))
	report("flag.files-from0", "%v", bytes.Contains(help, []byte("--files-from0")))

	// §14.3 encoding round-trip through --files-from-raw: seed the
	// suite (newline names cannot ride any list — reported, excluded),
	// copy cross-account, then compare destination names byte-exact.
	src := h.Container("matrix-src")
	dst := h.Prefix + "matrix-dst"
	if err := h.conn.ContainerCreate(ctx, src, nil); err != nil {
		t.Fatal(err)
	}
	if err := h.rawPut2(ctx, dst, nil, ""); err != nil {
		t.Fatal(err)
	}
	suite := append([]string{}, EncodingNames...)
	suite = append(suite, "a. b.", "v1.2/dotted.file")
	var listable []string
	for _, name := range suite {
		if err := h.put(ctx, src, name, []byte("matrix:"+name), "text/plain", nil); err != nil {
			t.Fatal(err)
		}
		if strings.ContainsAny(name, "\n\r") {
			report("encoding.excluded", "%q (newline cannot ride a list)", name)
			continue
		}
		listable = append(listable, name)
	}
	result, err := runner.Copy(ctx, []rclone.Remote{
		{Name: "aeronautes_src", StorageURL: h.conn.StorageUrl, Token: h.conn.AuthToken},
		{Name: "aeronautes_dst", StorageURL: h.Conn2().StorageUrl, Token: h.Conn2().AuthToken},
	}, "aeronautes_src:"+src, "aeronautes_dst:"+dst+"/.aeronautes/stage/it-matrix/batch", listable, nil)
	if err != nil {
		t.Fatal(err)
	}
	report("encoding.exit", "%d (errors reported: %d)", result.ExitCode, len(result.Errors))

	arrived := map[string]bool{}
	for entry, err := range h.Session2().Objects(ctx, dst, swiftcontrol.ObjectListOptions{Deep: true, Prefix: ".aeronautes/stage/it-matrix/batch/"}) {
		if err != nil {
			t.Fatal(err)
		}
		if object, ok := entry.(swiftcontrol.ObjectEntry); ok {
			arrived[strings.TrimPrefix(object.Name, ".aeronautes/stage/it-matrix/batch/")] = true
		}
	}
	var exact, missing []string
	for _, name := range listable {
		if arrived[name] {
			exact = append(exact, name)
			delete(arrived, name)
		} else {
			missing = append(missing, name)
		}
	}
	var renamed []string
	for name := range arrived {
		renamed = append(renamed, name)
	}
	sort.Strings(missing)
	sort.Strings(renamed)
	report("encoding.exact", "%d/%d", len(exact), len(listable))
	for _, name := range missing {
		report("encoding.missing", "%q", name)
	}
	for _, name := range renamed {
		report("encoding.unexpected-arrival", "%q (a listed name arrived under a different identity)", name)
	}

	// Chunked upload: destination class and the §26.4 prefix capture
	// (the log-line regex is version-sensitive — this is the canary).
	big := bytes.Repeat([]byte{0x77}, 3*1024*1024)
	if err := h.put(ctx, src, "matrix-big.bin", big, "application/octet-stream",
		swift.Headers{"X-Object-Meta-Matrix": "tagged"}); err != nil {
		t.Fatal(err)
	}
	result, err = runner.Copy(ctx, []rclone.Remote{
		{Name: "aeronautes_src", StorageURL: h.conn.StorageUrl, Token: h.conn.AuthToken},
		{Name: "aeronautes_dst", StorageURL: h.Conn2().StorageUrl, Token: h.Conn2().AuthToken},
	}, "aeronautes_src:"+src, "aeronautes_dst:"+dst+"/.aeronautes/stage/it-matrixbig/batch", []string{"matrix-big.bin"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	d, err := h.Session2().ObjectDetail(ctx, dst, ".aeronautes/stage/it-matrixbig/batch/matrix-big.bin")
	if err != nil {
		report("large.result", "MISSING after copy (exit %d)", result.ExitCode)
	} else {
		report("large.class", "%s (logical %d bytes)", d.Class, d.ContentLength)
		report("large.prefix-capture", "%d prefixes captured", len(result.SegmentPrefixes))
		report("large.metadata-preserved", "%v (X-Object-Meta on destination)", d.Meta["Matrix"] == "tagged")
	}
}
