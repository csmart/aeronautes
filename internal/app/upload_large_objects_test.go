package app

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/csmart/aeronautes/internal/swiftcontrol"
)

// uploadSegmented uploads one file larger than a 1 MiB chunk through the
// pinned engine and returns the finished job and what is live.
func uploadSegmented(t *testing.T, h *harness, settings TransferSettingsDTO) (JobDTO, *swiftcontrol.ObjectDetail) {
	t.Helper()
	h.mock.AddContainer("media", "standard")
	content := bytes.Repeat([]byte("segment!"), (3<<20)/8+1)
	path := filepath.Join(t.TempDir(), "big.bin")
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatal(err)
	}
	h.transfer.setFilePicker(fakePicker{files: []string{path}})
	picked, err := h.transfer.PickUploadFiles(t.Context(), SelectionScopeDTO{BookmarkID: h.bookmarkID(), Container: "media"})
	if err != nil {
		t.Fatal(err)
	}
	settings.ChunkSize = "1Mi"
	id, err := h.transfer.StartUpload(t.Context(), UploadRequestDTO{
		BookmarkID: h.bookmarkID(), Container: "media", SelectionToken: picked.Token, Settings: settings})
	if err != nil {
		t.Fatal(err)
	}
	job := awaitTerminal(t, h, id)
	session := h.transfer.connections.sessions[h.bookmarkID()].session
	live, err := session.ObjectDetail(t.Context(), "media", "big.bin")
	if err != nil {
		return job, nil
	}
	if live.ContentLength != int64(len(content)) {
		t.Errorf("live object is %d bytes, the file %d", live.ContentLength, len(content))
	}
	return job, live
}

// A segmented upload completes when the store's listing lags behind its
// last segment, as the genuine Swift cloud's did (F35): the engine's own
// size check is off for the run, and the commit reads the staged object
// again while it is short (ADR 0111 decisions 2 and 6).
func TestSegmentedUploadCompletesUnderALaggingListing(t *testing.T) {
	h := newHarness(t)
	h.connect(t)
	h.mock.ListingLag = 2
	job, live := uploadSegmented(t, h, TransferSettingsDTO{})
	if job.State != "complete" || live == nil || live.Class != swiftcontrol.ClassDLO {
		t.Fatalf("job %s %+v, live %+v", job.State, job.Counters, live)
	}
}
