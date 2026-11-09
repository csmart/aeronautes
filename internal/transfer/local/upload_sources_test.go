package local

import (
	"context"
	"crypto/sha256"
	"fmt"
	"reflect"
	"testing"

	"github.com/csmart/aeronautes/internal/transfer/copy"
	"github.com/csmart/aeronautes/internal/transfer/rclone"
)

// A source names its own destination under the prefix; a name this upload
// cannot take is a rejecting problem before any destination request.
func TestPreflightSourcesTakeTheirNamesAsGiven(t *testing.T) {
	s, _ := session(t)
	dir := t.TempDir()
	sources := []UploadSource{
		{Path: writeFile(t, dir, "one", "1"), Name: "photos/a/one"},
		{Path: writeFile(t, dir, "two", "2"), Name: ""},
		{Path: writeFile(t, dir, "three", "3"), Name: "/photos/three"},
		{Path: writeFile(t, dir, "four", "4"), Name: "photos//four"},
	}
	report, err := PreflightUploadSources(t.Context(), s, "media", "live/", sources)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Items) != 1 || report.Items[0].Name != "live/photos/a/one" {
		t.Fatalf("items = %+v, want the one well-named source under the prefix", report.Items)
	}
	if !report.Rejected() || len(report.Problems) != 3 {
		t.Fatalf("problems = %+v, want the three bad names refused", report.Problems)
	}
	for _, p := range report.Problems {
		if p.Kind != "source-name" || !p.Reject {
			t.Errorf("problem = %+v, want a rejecting source-name problem", p)
		}
	}
}

// ADR 0103 decision 4: two sources of one base name in different folders are
// distinct items with distinct staging objects. The first group's engine run
// fails after staging, so its stage is retained; the second group still runs,
// publishes, and leaves the retained stage's bytes and its record untouched.
func TestNamedSourcesStageApart(t *testing.T) {
	s, mock := session(t)
	mock.AddContainer("media_segments", "standard")
	mock.AddContainer("outside", "standard")
	witness := uploadWitness(t, mock)
	dirA, dirB := t.TempDir(), t.TempDir()
	sources := []UploadSource{
		{Path: writeFile(t, dirA, "image.jpg", "alpha"), Name: "photos/a/image.jpg"},
		{Path: writeFile(t, dirB, "image.jpg", "bravo"), Name: "photos/b/image.jpg"},
	}
	preflight, err := PreflightUploadSources(t.Context(), s, "media", "live/", sources)
	if err != nil || preflight.Rejected() || len(preflight.Items) != 2 {
		t.Fatalf("preflight: %+v %v", preflight, err)
	}
	if preflight.Items[0].Name != "live/photos/a/image.jpg" || preflight.Items[1].Name != "live/photos/b/image.jpg" {
		t.Fatalf("items = %+v", preflight.Items)
	}

	var staged []string
	recorder := &UploadRecorder{
		Admit:   func(a UploadAdmission) error { staged = append(staged, a.StagingObject); return nil },
		Execute: func(UploadItem, copy.Observation) error { return nil },
		Verify:  func(UploadItem, copy.Observation) error { return nil },
	}
	engine := &uploadRecordingEngine{}
	engine.run = func(ctx context.Context, src, dst string, names []string, event func(rclone.Event)) (*rclone.Result, error) {
		result, err := (&fsEngine{s}).Copy(ctx, src, dst, names, event)
		if err != nil {
			return result, err
		}
		if engine.calls == 1 {
			// Staged, then the engine reports failure: the stage stays.
			return &rclone.Result{ExitCode: 3}, nil
		}
		return result, nil
	}
	report, runErr := Upload(t.Context(), s, engine, "aeronautes_dst:media", "media", "live/", "stage-apart", preflight, nil, nil, recorder)
	if runErr != nil || report.Done != 1 || len(report.Failures) != 1 || engine.calls != 2 {
		t.Fatalf("report = %+v, err = %v, engine calls = %d; want one failed group and one published", report, runErr, engine.calls)
	}
	want := []string{".aeronautes/stage/stage-apart/batch/photos/a/image.jpg", ".aeronautes/stage/stage-apart/batch/photos/b/image.jpg"}
	if !reflect.DeepEqual(staged, want) {
		t.Fatalf("recorded staging objects = %q, want %q", staged, want)
	}
	objects := uploadSnapshot(t, witness).Containers["media"].Objects
	digest := func(body string) string { return fmt.Sprintf("%x", sha256.Sum256([]byte(body))) }
	if retained, ok := objects[want[0]]; !ok || retained.Size != 5 || retained.Digest != digest("alpha") {
		t.Fatalf("the failed group's stage was not retained untouched: %+v ok=%v", retained, ok)
	}
	if _, ok := objects["live/photos/a/image.jpg"]; ok {
		t.Fatal("the failed group published")
	}
	if live, ok := objects["live/photos/b/image.jpg"]; !ok || live.Digest != digest("bravo") {
		t.Fatalf("the second group did not publish its own bytes: %+v ok=%v", live, ok)
	}
	if _, ok := objects[want[1]]; ok {
		t.Fatal("the published group left its stage behind")
	}
}
