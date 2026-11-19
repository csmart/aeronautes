package jobs

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/csmart/aeronautes/internal/applog"
)

// ADR 0127 decision 2: at debug the log holds each job's creation and every
// change of its state, by id and type, and never the names the job touched.
func TestDebugLogsAJobsStepsWithoutItsNames(t *testing.T) {
	path := filepath.Join(t.TempDir(), "debug.log")
	t.Setenv(applog.EnvLevel, "debug")
	t.Setenv(applog.EnvFile, path)
	_, closeLog, err := applog.Configure()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = closeLog() })

	s, bookmark := newStore(t)
	ctx := context.Background()
	job, err := s.Create(ctx, Job{Type: TypeCopy, Source: "secret-container/private-name", Destination: "other-container/", SourceBookmarkID: bookmark, DestBookmarkID: bookmark})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Transition(ctx, job.ID, StatePlanning); err != nil {
		t.Fatal(err)
	}
	written, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	log := string(written)
	for _, want := range []string{"job " + job.ID + " created: copy", "job " + job.ID + ": queued → planning"} {
		if !strings.Contains(log, want) {
			t.Errorf("the debug log lacks %q:\n%s", want, log)
		}
	}
	if strings.Contains(log, "secret-container") || strings.Contains(log, "private-name") {
		t.Errorf("the debug log names what the job touched:\n%s", log)
	}
}
