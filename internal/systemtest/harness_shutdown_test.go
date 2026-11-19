package systemtest

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/csmart/aeronautes/internal/app"
)

func TestHarnessJoinsJobsBeforePurging(t *testing.T) {
	destination := t.TempDir()
	h := findMockHarness(t)
	source := h.Container(h.Primary, "shutdown")
	h.Seed(h.Primary, source, "scope/file", []byte("owned input"), nil)
	app.WireFilePicker(h.Transfer, listingFilePicker{dir: destination})
	picked, err := h.Transfer.PickDownloadDir(t.Context(), app.SelectionScopeDTO{
		BookmarkID: h.Primary.BookmarkID, Container: source, Prefix: "scope/",
	})
	if err != nil {
		t.Fatal(err)
	}
	entered, exited := make(chan struct{}), make(chan struct{})
	app.WireDownloadEngineBoundary(h.Transfer, func(ctx context.Context, _ string) error {
		close(entered)
		<-ctx.Done()
		close(exited)
		return ctx.Err()
	})
	id, err := h.Transfer.StartDownload(t.Context(), app.DownloadRequestDTO{
		BookmarkID: h.Primary.BookmarkID, Container: source, Prefix: "scope/", DestDirToken: picked.Token,
	})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("download did not reach the engine boundary")
	}
	h.MockPrimary.BeforeRequest = func(method, path string) {
		if method != http.MethodDelete || !strings.Contains(path, "/"+source) {
			return
		}
		select {
		case <-exited:
		default:
			t.Error("purging source while its download still runs")
		}
		job, err := h.Jobs.Get(context.Background(), h.Primary.BookmarkID, id)
		if err != nil || job.State != "cancelled" {
			t.Errorf("purging before the final job record: job=%+v error=%v", job, err)
		}
	}
	// Return with the admitted job unfinished, as an assertion failure would.
	// Only the harness owns stopping it and joining its final record writes.
}
