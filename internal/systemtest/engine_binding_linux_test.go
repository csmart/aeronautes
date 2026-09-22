//go:build linux

package systemtest

import (
	"bytes"
	"context"
	"crypto/md5"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/csmart/aeronautes/internal/swiftcontrol"
	transfercopy "github.com/csmart/aeronautes/internal/transfer/copy"
	"github.com/csmart/aeronautes/internal/transfer/local"
	"github.com/csmart/aeronautes/internal/transfer/rclone"
)

// ADR 0070: an engine download publishes only bytes that match the source
// it was planned from. These cases run the production runner with the
// pinned engine into the held stage and apply one fault after the child
// has returned, at the seams the retained assessment probe used: the
// staged bytes, the staged entry, the source object and the published
// file. Every fault must refuse before publication with the staged entry
// retained, except a published replacement, which the final-publication
// fact must report; the controls must still place and verify.

// engineBindingEngine delegates bytes and process lifetime to the pinned
// production runner and applies its fault only after the child returned.
type engineBindingEngine struct {
	runner *rclone.Runner
	remote rclone.Remote
	after  func(*os.File) error
	result *rclone.Result
}

func (e *engineBindingEngine) Copy(context.Context, string, string, []string, func(rclone.Event)) (*rclone.Result, error) {
	return nil, errors.New("engine binding cases must use the held stage anchor")
}

func (e *engineBindingEngine) CopyInto(ctx context.Context, source, _ string, anchor *os.File, names []string, onEvent func(rclone.Event)) (*rclone.Result, error) {
	r, err := e.runner.CopyInto(ctx, []rclone.Remote{e.remote}, source, anchor, names, onEvent)
	e.result = r
	if err != nil || transfercopy.ResultFailure(r) != nil {
		return r, err
	}
	if e.after != nil {
		if err := e.after(anchor); err != nil {
			return nil, err
		}
	}
	return r, nil
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
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()
	c := h.Container(h.Primary, "engine-binding")
	segments := h.Container(h.Primary, "engine-parts")
	other := h.Container(h.Secondary, "engine-collateral")
	left, right := bytes.Repeat([]byte("A"), 32), bytes.Repeat([]byte("B"), 32)
	body := append(append([]byte(nil), left...), right...)
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
	h.Seed(h.Primary, c, "scope/z-control.bin", []byte("valid control"), nil)
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
	expectedStage := body
	if mode == "source_replaced" {
		expectedStage = engineBindingInvert(body)
		seedSource(expectedStage)
	}
	beforeA, beforeB := h.SnapshotNamespace(h.Primary), h.SnapshotNamespace(h.Secondary)
	runner, err := rclone.NewRunner(rclone.Options{Binary: filepath.Join(official, "rclone"), Transfers: 1, Checkers: 1})
	if err != nil {
		t.Fatal(err)
	}
	e := &engineBindingEngine{runner: runner, remote: rclone.Remote{Name: "aeronautes_src", StorageURL: h.Primary.storageURL, Token: h.Primary.token}}
	faultReached := false
	var pin *os.File
	t.Cleanup(func() {
		if pin != nil {
			if err := pin.Close(); err != nil {
				t.Error(err)
			}
		}
	})
	// refusedBytes is what the stage must still hold after a refusal.
	refusedBytes := expectedStage
	e.after = func(anchor *os.File) error {
		staged := filepath.Join(fmt.Sprintf("/proc/self/fd/%d", anchor.Fd()), "a.bin")
		actual, err := os.ReadFile(staged)
		if err != nil {
			return err
		}
		if !bytes.Equal(actual, expectedStage) {
			return fmt.Errorf("child's staged bytes differ from fixture before fault: got %x want %x", md5.Sum(actual), md5.Sum(expectedStage))
		}
		changed := []byte(nil)
		switch mode {
		case "corrupt", "stage_replaced":
			changed = engineBindingInvert(body)
		case "reordered":
			changed = append(append([]byte(nil), right...), left...)
		case "missing_with_duplicate":
			changed = append(append([]byte(nil), left...), left...)
		case "short":
			changed = left
		}
		if changed == nil {
			return nil
		}
		if mode == "stage_replaced" {
			pin, err = os.Open(staged)
			if err != nil {
				return err
			}
			if err := os.Remove(staged); err != nil {
				return err
			}
		}
		if err := os.WriteFile(staged, changed, 0600); err != nil {
			return err
		}
		refusedBytes = changed
		faultReached = true
		return nil
	}
	executed, verified := map[string]transfercopy.Observation{}, map[string]transfercopy.Observation{}
	publications := map[string]transfercopy.Observation{}
	recorder := &local.DownloadRecorder{
		Admit:       func(local.DownloadAdmission) error { return nil },
		Execute:     func(i local.DownloadItem, o transfercopy.Observation) error { executed[i.Name] = o; return nil },
		Verify:      func(i local.DownloadItem, o transfercopy.Observation) error { verified[i.Name] = o; return nil },
		Publication: func(i local.DownloadItem, o transfercopy.Observation) error { publications[i.Name] = o; return nil },
	}
	progress := func(done, total, n int64) {
		if mode != "published_replaced" || done != 1 || faultReached {
			return
		}
		path := filepath.Join(root, "a.bin")
		pin, err = os.Open(path)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.Remove(path); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, engineBindingInvert(body), 0600); err != nil {
			t.Fatal(err)
		}
		old, err := pin.Stat()
		if err != nil {
			t.Fatal(err)
		}
		now, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if os.SameFile(old, now) {
			t.Fatal("publication fixture did not substitute a distinct pinned inode")
		}
		faultReached = true
	}
	report, runErr := local.Download(ctx, session, e, "aeronautes_src:"+c, c, "scope/", root, "binding", plan, progress, recorder)
	if e.result == nil || transfercopy.ResultFailure(e.result) != nil {
		t.Fatalf("real engine did not succeed before placement: result=%+v error=%v", e.result, runErr)
	}
	if mode != "control" && mode != "source_replaced" && !faultReached {
		t.Fatal("required fault was not reached")
	}
	if runErr != nil {
		t.Fatalf("executor failed outside the item outcome: %v", runErr)
	}
	control, err := os.ReadFile(filepath.Join(root, "z-control.bin"))
	if err != nil || string(control) != "valid control" {
		t.Fatalf("valid control changed: %q %v", control, err)
	}
	sentinel, err := os.ReadFile(filepath.Join(root, "keep"))
	if err != nil || string(sentinel) != "local sentinel" {
		t.Fatalf("local collateral changed: %q %v", sentinel, err)
	}
	h.AssertNoCollateral(beforeA, h.SnapshotNamespace(h.Primary))
	h.AssertNoCollateral(beforeB, h.SnapshotNamespace(h.Secondary))
	actual, readErr := os.ReadFile(filepath.Join(root, "a.bin"))
	stagedPath := filepath.Join(root, ".aeronautes-stage-binding", "a.bin")
	staged, stagedErr := os.ReadFile(stagedPath)
	t.Logf("engine binding class=%s mode=%s pinned=%s done=%d verified=%d failures=%+v execute=%+v verify=%+v final=%+v published=%d read=%v staged=%d stagedErr=%v cleanup=%+v", class, mode, runner.Version(), report.Done, report.Verified, report.Failures, executed[name], verified[name], publications[name], len(actual), readErr, len(staged), stagedErr, report.StageCleanup)
	if mode == "control" {
		if readErr != nil || !bytes.Equal(actual, body) || report.Done != 2 || report.Verified != 2 || len(report.Failures) != 0 {
			t.Fatalf("matching engine control refused: %+v read=%v", report, readErr)
		}
		if publications[name].Result != transfercopy.ResultVerified {
			t.Fatalf("control lacks its final-publication fact: %+v", publications[name])
		}
		if !errors.Is(stagedErr, os.ErrNotExist) || report.StageCleanup == nil || report.StageCleanup.Retained || report.StageCleanup.Refused != 0 {
			t.Fatalf("control left stage residue: stagedErr=%v cleanup=%+v", stagedErr, report.StageCleanup)
		}
		return
	}
	if mode == "published_replaced" {
		// The bytes were verified and published; the replacement of the
		// published name is the final-publication fact's finding, and
		// the occupant is not this executor's to unlink (ADR 0045).
		if verified[name].Result != transfercopy.ResultVerified || publications[name].Result != transfercopy.ResultFailed || publications[name].Effect != transfercopy.EffectUnresolved {
			t.Fatalf("published replacement not reported by the final-publication fact: verify=%+v final=%+v", verified[name], publications[name])
		}
		if report.Verified != 1 || len(report.Failures) != 1 || report.Failures[0].Name != name || report.Failures[0].Stage != "place" {
			t.Fatalf("published replacement not counted against verification: %+v", report)
		}
		if readErr != nil || !bytes.Equal(actual, engineBindingInvert(body)) {
			t.Fatalf("the replacement occupant was touched: read=%v bytes=%x", readErr, actual)
		}
		return
	}
	// Every other fault refuses before publication: nothing at the target
	// name, the item unverified with effect unresolved, and the staged
	// entry retained with the refused bytes, exempt from cleanup.
	if !errors.Is(readErr, os.ErrNotExist) {
		t.Fatalf("%s %s published %d bytes (read=%v)", class, mode, len(actual), readErr)
	}
	if verified[name].Result != transfercopy.ResultFailed || verified[name].Diagnostic.Code != transfercopy.CodeUnverified || executed[name].Effect != transfercopy.EffectUnresolved {
		t.Fatalf("%s %s did not report unverified/unresolved: execute=%+v verify=%+v", class, mode, executed[name], verified[name])
	}
	if report.Verified != 1 || report.Done != 1 || len(report.Failures) != 1 || report.Failures[0].Name != name {
		t.Fatalf("%s %s report counts wrong: %+v", class, mode, report)
	}
	if _, published := publications[name]; published {
		t.Fatalf("%s %s got a publication fact without a publication: %+v", class, mode, publications[name])
	}
	if stagedErr != nil || !bytes.Equal(staged, refusedBytes) {
		t.Fatalf("%s %s did not retain the refused bytes in the stage: err=%v got %x want %x", class, mode, stagedErr, md5.Sum(staged), md5.Sum(refusedBytes))
	}
	if report.StageCleanup == nil || !report.StageCleanup.Retained || report.StageCleanup.Refused != 1 || report.StageCleanup.Issues != 0 {
		t.Fatalf("%s %s cleanup did not exempt the refused entry cleanly: %+v", class, mode, report.StageCleanup)
	}
}

func engineBindingCases(t *testing.T, makeHarness func(*testing.T) *Harness) {
	for _, class := range []string{"plain", "dlo", "slo", "link"} {
		t.Run(class, func(t *testing.T) {
			for _, mode := range []string{"control", "corrupt", "reordered", "missing_with_duplicate", "short", "stage_replaced", "source_replaced", "published_replaced"} {
				t.Run(mode, func(t *testing.T) { engineBindingCase(t, makeHarness(t), class, mode) })
			}
		})
	}
}

func TestEngineBindingMock(t *testing.T)      { engineBindingCases(t, findMockHarness) }
func TestEngineBindingRealSwift(t *testing.T) { engineBindingCases(t, New) }
