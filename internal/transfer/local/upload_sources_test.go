package local

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"reflect"
	"strings"
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
		// The engine stages a file under its base name, so a source renamed
		// on the way would stage beside another file of that base name.
		{Path: writeFile(t, dir, "five", "5"), Name: "photos/renamed"},
	}
	report, err := PreflightUploadSources(t.Context(), s, "media", "live/", sources)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Items) != 1 || report.Items[0].Name != "live/photos/a/one" {
		t.Fatalf("items = %+v, want the one well-named source under the prefix", report.Items)
	}
	if !report.Rejected() || len(report.Problems) != 4 {
		t.Fatalf("problems = %+v, want the four bad names refused", report.Problems)
	}
	for _, p := range report.Problems {
		if p.Kind != "source-name" || !p.Reject {
			t.Errorf("problem = %+v, want a rejecting source-name problem", p)
		}
	}
}

// ADR 0105 decision 3: the staging namespace is not a destination. A name
// there is, or will be, some job's unpublished copy, so the preflight rejects
// it whether the selection or the destination prefix puts it there, including
// one aimed at a running job's staged manifest; a name that only resembles the
// namespace is an ordinary name.
func TestPreflightRejectsANameInTheStagingNamespace(t *testing.T) {
	s, _ := session(t)
	dir := t.TempDir()
	path := writeFile(t, dir, "file", "x")
	for _, tc := range []struct {
		prefix, name string
		staged       bool
	}{
		{"", ".aeronautes/stage/1b2c/batch/file", true},
		{"backup/", ".aeronautes/stage/1b2c/file", true},
		{"backup/.aeronautes/stage/1b2c/", "file", true},
		{"live/", "file", false},
		{"", "x.aeronautes/stage/file", false},
		{"", ".aeronautes/staged/file", false},
	} {
		report, err := PreflightUploadSources(t.Context(), s, "media", tc.prefix, []UploadSource{{Path: path, Name: tc.name}})
		if err != nil {
			t.Fatal(err)
		}
		rejected := len(report.Problems) == 1 && report.Problems[0].Kind == "staging" && report.Problems[0].Reject && len(report.Items) == 0
		admitted := len(report.Problems) == 0 && len(report.Items) == 1
		if tc.staged && !rejected || !tc.staged && !admitted {
			t.Errorf("%q + %q: items=%+v problems=%+v, want staged=%v", tc.prefix, tc.name, report.Items, report.Problems, tc.staged)
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

// ADR 0104 decision 1: the route asks to pause at the head of each unit and
// nowhere else, a native-routed item and an engine group each being one. A
// cancellation returned from that wait ends the run as a cancel seen at the
// same boundary does: what was published stays, and the groups never started
// are recorded as not attempted.
func TestUploadAsksToPauseAtUnitHeadsOnly(t *testing.T) {
	s, _ := session(t)
	dirA, dirB, dirN := t.TempDir(), t.TempDir(), t.TempDir()
	sources := func(under string) []UploadSource {
		return []UploadSource{
			// A newline in a name is what routes a file natively.
			{Path: writeFile(t, dirN, "line\nname", "native"), Name: under + "line\nname"},
			{Path: writeFile(t, dirA, "one", "alpha"), Name: under + "a/one"},
			{Path: writeFile(t, dirA, "two", "alpha two"), Name: under + "a/two"},
			{Path: writeFile(t, dirB, "three", "bravo"), Name: under + "b/three"},
		}
	}
	run := func(under string, pause func(asked int) error) (*Report, error, int, map[string]copy.Result) {
		t.Helper()
		preflight, err := PreflightUploadSources(t.Context(), s, "media", "live/", sources(under))
		if err != nil || preflight.Rejected() || len(preflight.Items) != 4 {
			t.Fatalf("preflight: %+v %v", preflight, err)
		}
		asked := 0
		executed := map[string]copy.Result{}
		recorder := &UploadRecorder{
			Admit:   func(UploadAdmission) error { return nil },
			Execute: func(item UploadItem, o copy.Observation) error { executed[item.Name] = o.Result; return nil },
			Verify:  func(UploadItem, copy.Observation) error { return nil },
			Pause: func(context.Context) error {
				asked++
				return pause(asked)
			},
		}
		report, runErr := Upload(t.Context(), s, &fsEngine{s}, "aeronautes_dst:media", "media", "live/", "pause-"+strings.Trim(under, "/"), preflight, nil, nil, recorder)
		return report, runErr, asked, executed
	}

	report, err, asked, _ := run("first/", func(int) error { return nil })
	if err != nil || report.Done != 4 || asked != 3 {
		t.Fatalf("report = %+v, %v; asked %d times, want once for the native item and once for each of the two groups", report, err, asked)
	}

	// The second ask is the head of the first group: the native item is done.
	report, err, asked, executed := run("second/", func(asked int) error {
		if asked == 2 {
			return context.Canceled
		}
		return nil
	})
	if !errors.Is(err, context.Canceled) || report.Done != 1 || asked != 2 {
		t.Fatalf("report = %+v, %v after %d asks; want the cancel with only the native item done", report, err, asked)
	}
	for _, name := range []string{"live/second/a/one", "live/second/a/two", "live/second/b/three"} {
		if executed[name] != copy.ResultNotAttempted {
			t.Errorf("%s = %q, want it recorded as not attempted", name, executed[name])
		}
	}
}
