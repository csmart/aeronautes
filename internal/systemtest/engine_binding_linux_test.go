//go:build linux

package systemtest

import (
	"bytes"
	"context"
	"crypto/md5"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/csmart/aeronautes/internal/swiftcontrol"
	"github.com/csmart/aeronautes/internal/systemtest/oracle"
	transfercopy "github.com/csmart/aeronautes/internal/transfer/copy"
	"github.com/csmart/aeronautes/internal/transfer/local"
	"github.com/csmart/aeronautes/internal/transfer/rclone"
)

// ADR 0122 decision 5 with the production runner and the pinned engine bound
// to the held folder, as a download runs it: a plain object, a dynamic and a
// static large object and a link each download whole into the chosen folder.
// A source rewritten after the preview downloads as it now is, because
// nothing is compared against the listing. A static object with a segment
// removed is one the store will not serve whole: that file fails with the
// engine's error line, whatever the store answered for it, and nothing is
// left at its name.

// engineBindingEngine gives the pinned runner the held folder, which is the
// only way a download hands the engine its destination on this platform.
type engineBindingEngine struct {
	runner *rclone.Runner
	remote rclone.Remote
}

func (e engineBindingEngine) Copy(context.Context, string, string, []string, func(rclone.Event)) (*rclone.Result, error) {
	return nil, errors.New("engine binding cases must use the held folder")
}

func (e engineBindingEngine) CopyInto(ctx context.Context, source, _ string, anchor *os.File, names []string, onEvent func(rclone.Event)) (*rclone.Result, error) {
	return e.runner.CopyInto(ctx, []rclone.Remote{e.remote}, source, anchor, names, onEvent)
}

func engineBindingInvert(b []byte) []byte {
	out := make([]byte, len(b))
	for i, c := range b {
		out[i] = c ^ 0xff
	}
	return out
}

func engineBindingCase(t *testing.T, h *Harness, class, mode string) {
	t.Helper()
	official := os.Getenv("AERONAUTES_OFFICIAL_RCLONE_DIR")
	if official == "" {
		t.Skip("AERONAUTES_OFFICIAL_RCLONE_DIR not set")
	}
	if mode == "segment_missing" && h.MockPrimary != nil {
		t.Skip("how a static manifest is served without one of its segments is the real store's behaviour")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()
	c := h.Container(h.Primary, "engine-binding")
	segments := h.Container(h.Primary, "engine-parts")
	other := h.Container(h.Secondary, "engine-collateral")
	body := append(bytes.Repeat([]byte("A"), 32), bytes.Repeat([]byte("B"), 32)...)
	name := "scope/a.bin"
	seedSource := func(value []byte) {
		switch class {
		case "plain":
			h.Seed(h.Primary, c, name, value, nil)
		case "dlo", "slo":
			h.Seed(h.Primary, segments, "subject/0", value[:32], nil)
			h.Seed(h.Primary, segments, "subject/1", value[32:], nil)
			if class == "dlo" {
				h.Seed(h.Primary, c, name, nil, map[string]string{"X-Object-Manifest": segments + "/subject/"})
			} else {
				manifest := fmt.Sprintf(`[{"path":%q,"etag":"%x","size_bytes":32},{"path":%q,"etag":"%x","size_bytes":32}]`, "/"+segments+"/subject/0", md5.Sum(value[:32]), "/"+segments+"/subject/1", md5.Sum(value[32:]))
				h.SeedSLO(h.Primary, c, name, manifest)
			}
		case "link":
			h.Seed(h.Primary, segments, "target", value, nil)
			h.Seed(h.Primary, c, name, nil, map[string]string{"X-Symlink-Target": segments + "/target"})
		default:
			t.Fatalf("unknown class %q", class)
		}
	}
	seedSource(body)
	control := []byte("valid control")
	h.Seed(h.Primary, c, "scope/z-control.bin", control, nil)
	h.Seed(h.Primary, c, "outside", []byte("source sentinel"), nil)
	h.Seed(h.Secondary, other, "outside", []byte("other account sentinel"), nil)
	session, err := swiftcontrol.New(swiftcontrol.Config{AuthURL: h.Primary.authURL, Username: h.Primary.user, APIKey: h.Primary.key})
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	seedListingLocal(t, root, "keep", []byte("local sentinel"))
	plan, err := local.PreflightDownload(ctx, session, c, "scope/", root)
	if err != nil || plan == nil || plan.Rejected() || len(plan.Items) != 2 {
		t.Fatalf("preflight: %+v %v", plan, err)
	}
	for _, item := range plan.Items {
		if item.Route != "rclone" || item.Dir {
			t.Fatalf("unexpected route: %+v", item)
		}
	}
	if plan.Items[0].Size != 64 {
		t.Fatalf("class %s planned size %d, want logical 64", class, plan.Items[0].Size)
	}
	// What the store holds when the engine reads it, after the preview.
	want := body
	switch mode {
	case "source_replaced":
		want = engineBindingInvert(body)
		seedSource(want)
	case "segment_missing":
		if status := h.rawDeleteChecked(h.Primary, "/"+segments+"/subject/1"); status/100 != 2 {
			t.Fatalf("remove the second segment: status %d", status)
		}
	}
	var beforeA *oracle.AccountSnapshot
	if mode != "segment_missing" {
		beforeA = h.SnapshotNamespace(h.Primary)
	}
	beforeB := h.SnapshotNamespace(h.Secondary)
	runner, err := rclone.NewRunner(rclone.Options{Binary: filepath.Join(official, "rclone"), Transfers: 1, Checkers: 1, LeaveExisting: true})
	if err != nil {
		t.Fatal(err)
	}
	engine := engineBindingEngine{runner: runner, remote: rclone.Remote{Name: "aeronautes_src", StorageURL: h.Primary.storageURL, Token: h.Primary.token}}
	executed, verified := map[string]transfercopy.Observation{}, map[string]transfercopy.Observation{}
	recorder := &local.DownloadRecorder{
		Admit:   func(local.DownloadAdmission) error { return nil },
		Execute: func(i local.DownloadItem, o transfercopy.Observation) error { executed[i.Name] = o; return nil },
		Verify:  func(i local.DownloadItem, o transfercopy.Observation) error { verified[i.Name] = o; return nil },
	}
	report, runErr := local.Download(ctx, session, engine, "aeronautes_src:"+c, c, "scope/", root, plan, nil, recorder)
	if runErr != nil {
		t.Fatalf("executor failed outside the item outcome: %v", runErr)
	}
	// The witness reads every object whole, which a static manifest with a
	// missing segment refuses, so that case's own account is not witnessed.
	if mode != "segment_missing" {
		h.AssertNoCollateral(beforeA, h.SnapshotNamespace(h.Primary))
	}
	h.AssertNoCollateral(beforeB, h.SnapshotNamespace(h.Secondary))
	t.Logf("engine binding class=%s mode=%s pinned=%s report=%+v execute=%+v verify=%+v", class, mode, runner.Version(), report, executed[name], verified[name])

	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	var listed []string
	for _, entry := range entries {
		listed = append(listed, entry.Name())
	}
	sort.Strings(listed)
	wantListed := []string{"a.bin", "keep", "z-control.bin"}
	if mode == "segment_missing" {
		wantListed = []string{"keep", "z-control.bin"}
	}
	if !reflect.DeepEqual(listed, wantListed) {
		t.Fatalf("the folder holds %q, want %q and nothing of the engine's own", listed, wantListed)
	}
	for file, content := range map[string][]byte{"keep": []byte("local sentinel"), "z-control.bin": control} {
		if got := engineBindingDigest(t, filepath.Join(root, file)); got != sha256.Sum256(content) {
			t.Fatalf("%s does not hold its bytes", file)
		}
	}
	if mode == "segment_missing" {
		got := executed[name]
		if got.Result != transfercopy.ResultFailed || got.Effect != transfercopy.EffectUnresolved || len(report.Failures) != 1 ||
			report.Failures[0].Name != name || !strings.HasPrefix(report.Failures[0].Err, "Failed to copy: ") {
			t.Fatalf("a source the store would not serve whole: recorded %+v, failures %+v", got, report.Failures)
		}
		if _, recorded := verified[name]; recorded || report.Done != 1 {
			t.Fatalf("a failed file was verified or counted: verify=%+v done=%d", verified[name], report.Done)
		}
		return
	}
	if engineBindingDigest(t, filepath.Join(root, "a.bin")) != sha256.Sum256(want) {
		t.Fatalf("a.bin does not hold the %s object's bytes as the store held them at the download", class)
	}
	if report.Done != 2 || report.Verified != 2 || len(report.Failures) != 0 || executed[name].Result != transfercopy.ResultCopied ||
		verified[name].Result != transfercopy.ResultVerified {
		t.Fatalf("a whole download was not recorded copied: report=%+v execute=%+v verify=%+v", report, executed[name], verified[name])
	}
}

func engineBindingDigest(t *testing.T, path string) [sha256.Size]byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return sha256.Sum256(data)
}

func engineBindingCases(t *testing.T, makeHarness func(*testing.T) *Harness) {
	for _, class := range []string{"plain", "dlo", "slo", "link"} {
		t.Run(class, func(t *testing.T) {
			modes := []string{"control", "source_replaced"}
			if class == "slo" {
				modes = append(modes, "segment_missing")
			}
			for _, mode := range modes {
				t.Run(mode, func(t *testing.T) { engineBindingCase(t, makeHarness(t), class, mode) })
			}
		})
	}
}

func TestEngineBindingMock(t *testing.T)      { engineBindingCases(t, findMockHarness) }
func TestEngineBindingRealSwift(t *testing.T) { engineBindingCases(t, New) }
