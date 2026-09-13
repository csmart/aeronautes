package local

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"

	transfercopy "github.com/csmart/aeronautes/internal/transfer/copy"
)

// Maintained regressions for ADR 0043. A cancel at the native staging
// boundary reports what certainly did not happen as not attempted, and
// the directories the ancestor walk actually made are reported from
// observed creation rather than from a directory existing afterwards.

// cancelAfterFirstCheck reports no cancellation to the first ctx.Err()
// and cancellation to every later one, which places the cancel exactly
// between the ancestor walk and the temporary's exclusive creation.
// The executor consults the context only through Err() here, so this
// selects the boundary instead of racing for it.
type cancelAfterFirstCheck struct {
	context.Context
	checks int
}

func (c *cancelAfterFirstCheck) Err() error {
	c.checks++
	if c.checks == 1 {
		return nil
	}
	return context.Canceled
}

// heldBody must not be read: the boundary under test is before the
// temporary exists, so nothing has anywhere to be written.
type heldBody struct{ closed bool }

func (b *heldBody) Read([]byte) (int, error) { return 0, errors.New("body must not be read") }
func (b *heldBody) Close() error             { b.closed = true; return nil }

func TestNativeStagingCancelIsNotAttempted(t *testing.T) {
	root := t.TempDir()
	destination, err := OpenDestinationRoot(root)
	if err != nil {
		t.Fatal(err)
	}
	defer destination.Close()
	ctx := &cancelAfterFirstCheck{Context: t.Context()}
	item := DownloadItem{Name: "scope/deep/inner/f.bin", RelPath: filepath.Join("deep", "inner", "f.bin"), Size: 7, Route: "native"}
	body := &heldBody{}
	// The body is never read at this boundary, so no content expectation
	// applies; an empty ladder keeps this case about the cancel.
	outcome := nativeDownloadBody(ctx, body, item, destination, filepath.Join(root, "deep", "inner", ".aeronautes-part-x"), transfercopy.Ladder{})
	if ctx.checks < 2 {
		t.Fatalf("the boundary under test was not reached: %d checks", ctx.checks)
	}

	if outcome.Result != transfercopy.ResultNotAttempted || outcome.Effect != transfercopy.EffectNotAttempted {
		t.Fatalf("a cancel before the temporary exists is not uncertain: %+v", outcome.Observation)
	}
	if outcome.Diagnostic.Stage != transfercopy.StageSourceRead || outcome.Diagnostic.Code != transfercopy.CodeCancelled {
		t.Fatalf("cancel record differs from the neighbouring checks: %+v", outcome.Diagnostic)
	}
	if !body.closed {
		t.Fatal("the held body was not closed when the cancel was observed")
	}
	// Nothing may exist for the item itself.
	if entries, err := os.ReadDir(filepath.Join(root, "deep", "inner")); err != nil || len(entries) != 0 {
		t.Fatalf("a cancelled item left an entry behind: %v %v", entries, err)
	}
	// The ancestors the walk made are reported, from observed creation.
	if !slices.Equal(outcome.created, []string{"deep", filepath.Join("deep", "inner")}) {
		t.Fatalf("created ancestors were not reported in order: %v", outcome.created)
	}
}

func TestAncestorCreationIsObservedNotInferred(t *testing.T) {
	root := t.TempDir()
	// "deep" exists already and is the operator's own; only "inner" is
	// this executor's creation.
	if err := os.Mkdir(filepath.Join(root, "deep"), 0o755); err != nil {
		t.Fatal(err)
	}
	destination, err := OpenDestinationRoot(root)
	if err != nil {
		t.Fatal(err)
	}
	defer destination.Close()

	dir, created, err := destination.openDirectory(filepath.Join("deep", "inner"), true)
	if err != nil {
		t.Fatal(err)
	}
	defer dir.Close()
	if !slices.Equal(created, []string{filepath.Join("deep", "inner")}) {
		t.Fatalf("a pre-existing directory was counted as created: %v", created)
	}

	// A second walk over the same path creates nothing.
	again, createdAgain, err := destination.openDirectory(filepath.Join("deep", "inner"), true)
	if err != nil {
		t.Fatal(err)
	}
	defer again.Close()
	if len(createdAgain) != 0 {
		t.Fatalf("an existing directory was reported as created: %v", createdAgain)
	}

	// A refused walk reports only what it created before failing. With
	// creation enabled a refusal can only come from a component that
	// already exists, because an absent one is created; so the prefix is
	// empty here, and a non-empty prefix needs a component to be won by
	// another writer between the walk creating its parent and reaching
	// it. Both are the same code path, and neither may report a
	// directory this executor did not make.
	if err := os.WriteFile(filepath.Join(root, "deep", "blocked"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, refused, err := destination.openDirectory(filepath.Join("deep", "blocked", "below"), true)
	if err == nil {
		t.Fatal("a walk through a regular file was not refused")
	}
	if len(refused) != 0 {
		t.Fatalf("a refused walk reported a directory it did not create: %v", refused)
	}
}

func TestCreatedDirectoriesReachTheReport(t *testing.T) {
	report := &Report{}
	report.createdDirectories([]string{"a", filepath.Join("a", "b")})
	report.createdDirectories(nil)
	if report.CreatedDirectories != 2 || !slices.Equal(report.CreatedDirectorySamples, []string{"a", filepath.Join("a", "b")}) {
		t.Fatalf("observed creation did not reach the report: %+v", report)
	}
	// The count is never truncated by the sample bound.
	many := make([]string, maxFailures+5)
	for i := range many {
		many[i] = filepath.Join("bulk", string(rune('a'+i%26)))
	}
	report.createdDirectories(many)
	if report.CreatedDirectories != int64(2+len(many)) || len(report.CreatedDirectorySamples) != maxFailures {
		t.Fatalf("sample bound truncated the count: %d samples=%d", report.CreatedDirectories, len(report.CreatedDirectorySamples))
	}
}

// The creation decision itself, which the walk cannot reach from a test
// without a filesystem race: an entry won by another writer between the
// inspection and the Mkdir must not count as this executor's.
func TestComponentCreatedIsObserved(t *testing.T) {
	for _, c := range []struct {
		name     string
		absent   bool
		mkdirErr error
		want     bool
	}{
		{"absent and created here", true, nil, true},
		{"absent but won by another writer", true, os.ErrExist, false},
		{"absent but wrapped ErrExist from another writer", true, &os.PathError{Op: "mkdir", Err: os.ErrExist}, false},
		{"already there", false, nil, false},
		{"already there and no mkdir attempted", false, os.ErrExist, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got := componentCreated(c.absent, c.mkdirErr); got != c.want {
				t.Fatalf("componentCreated(%v, %v) = %v, want %v", c.absent, c.mkdirErr, got, c.want)
			}
		})
	}
}

// A walk told not to create must never create. Stage cleanup walks this
// way, and a cleanup that materialises the directory it was inspecting
// would leave the operator a directory nobody asked for.
func TestNonCreatingWalkCreatesNothing(t *testing.T) {
	root := t.TempDir()
	destination, err := OpenDestinationRoot(root)
	if err != nil {
		t.Fatal(err)
	}
	defer destination.Close()

	dir, created, err := destination.openDirectory(filepath.Join("nope", "deeper"), false)
	if err == nil {
		dir.Close()
		t.Fatal("a non-creating walk over an absent path was not refused")
	}
	if len(created) != 0 {
		t.Fatalf("a non-creating walk reported creations: %v", created)
	}
	for _, rel := range []string{"nope", filepath.Join("nope", "deeper")} {
		if _, err := os.Lstat(filepath.Join(root, rel)); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("a non-creating walk created %q: %v", rel, err)
		}
	}
	if entries, err := os.ReadDir(root); err != nil || len(entries) != 0 {
		t.Fatalf("a non-creating walk changed the selected directory: %v %v", entries, err)
	}
}
