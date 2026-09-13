package systemtest

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/csmart/aeronautes/internal/jobs"
)

// Maintained regression for ADR 0043. A cancel at the native staging
// boundary still stops wherever it stops, so which effects occur is not
// fixed. What must hold on every run is that the item is never reported
// uncertain when nothing was created for it, and that the directories
// the job says it made are exactly the ones now on disk.
// mode "reporting" waits until the ancestor walk has actually created
// its directories before cancelling, so the reporting invariant is
// exercised on every run rather than when the scheduler happens to
// oblige. mode "early" cancels as soon as the body is held, which is an
// equally permitted ordering that must leave nothing behind and claim
// nothing.
func downloadCancelDeltaCase(t *testing.T, h *Harness, mode string) {
	t.Helper()
	container := h.Container(h.Primary, "cancel-delta")
	root := t.TempDir()
	// The marker sorts first and materialises; the nested native item
	// then has ancestors that do not exist yet.
	h.Seed(h.Primary, container, "scope/aaa/", nil, nil)
	nested := "scope/deep/inner/n／native.bin"
	body := make([]byte, 256*1024)
	for i := range body {
		body[i] = byte('a' + i%26)
	}
	h.Seed(h.Primary, container, nested, body, nil)

	hold, _ := downloadCancelBodyProxy(t, h, container, nested, 64*1024)
	id := downloadCancelStart(t, h, container, "scope/", root)
	select {
	case <-hold.reached:
	case <-time.After(15 * time.Second):
		t.Fatal("native body hold never reached")
	}
	inner := filepath.Join(root, "deep", "inner")
	if mode == "reporting" {
		// The proxy releasing bytes says the server made progress, not
		// that the executor reached any particular point, so observe the
		// walk's own effect instead of racing it.
		deadline := time.Now().Add(15 * time.Second)
		for {
			if info, err := os.Lstat(inner); err == nil && info.IsDir() {
				break
			}
			if time.Now().After(deadline) {
				t.Fatal("the ancestor walk never created its directories, so the reporting case was not reached")
			}
			time.Sleep(5 * time.Millisecond)
		}
	}
	if err := h.Transfer.CancelJob(t.Context(), id); err != nil {
		t.Fatal(err)
	}
	close(hold.release)

	h.AwaitJob(id)
	saved := uploadRecoveryRead(t, h.database, id)

	found := false
	for _, item := range saved.Items {
		if item.Key != nested {
			continue
		}
		found = true
		if item.Execution == nil {
			t.Fatalf("the cancelled item lost its execution record: %+v", item)
		}
		observed := item.Execution.ItemObservation
		// The temporary's exclusive creation is below every check that
		// can fire here, so no file effect for this item is possible and
		// none may be reported as uncertain.
		if observed.Effect == jobs.EffectUnresolved {
			t.Errorf("a cancel before the temporary exists was reported uncertain: %+v", observed)
		}
		if observed.Diagnostic.Code != jobs.CodeCancelled {
			t.Errorf("the cancel was not named as one: %+v", observed.Diagnostic)
		}
	}

	// Every directory beneath the picked root, other than the marker's
	// own, is one the ancestor walk made.
	made := []string{}
	if err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil || rel == "." || !d.IsDir() || rel == "aaa" {
			return relErr
		}
		made = append(made, rel)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	sort.Strings(made)

	if !found {
		t.Fatalf("no durable record for the cancelled item %q", nested)
	}

	// The operator reads both, so both must agree with the disk.
	audit := ""
	for _, event := range saved.Audit {
		if event.Resource == "job "+id {
			audit = event.Summary
		}
	}
	if audit == "" {
		t.Fatal("terminal audit event missing")
	}
	for _, reported := range []string{saved.Job.Error, audit} {
		if len(made) == 0 {
			if strings.Contains(reported, "directories inside the selected directory") {
				t.Errorf("the job claimed directories it did not create: %q", reported)
			}
			continue
		}
		want := fmt.Sprintf("created %d directories inside the selected directory that were not there before", len(made))
		if !strings.Contains(reported, want) {
			t.Errorf("directories were left in the picked directory and not reported: on disk %v, reported %q", made, reported)
		}
		for _, dir := range made {
			if !strings.Contains(reported, dir) {
				t.Errorf("a created directory %q is missing from the report: %q", dir, reported)
			}
		}
	}
	if mode == "reporting" && len(made) == 0 {
		t.Fatal("the reporting case reached the cancel without the directories it waited for")
	}
	if mode == "early" && len(made) > 0 {
		t.Logf("the early cancel still reached the walk; the invariant was checked against %v", made)
	}
}

func downloadCancelDeltaCases(t *testing.T, makeHarness func(*testing.T) *Harness) {
	t.Helper()
	for _, mode := range []string{"reporting", "early"} {
		t.Run(mode, func(t *testing.T) { downloadCancelDeltaCase(t, makeHarness(t), mode) })
	}
}

func TestDownloadCancelDeltaMock(t *testing.T)      { downloadCancelDeltaCases(t, findMockHarness) }
func TestDownloadCancelDeltaRealSwift(t *testing.T) { downloadCancelDeltaCases(t, New) }
