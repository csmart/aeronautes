package app

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/csmart/aeronautes/internal/swiftcontrol"
)

// uploadFile uploads one file of size bytes at a 1 MiB chunk through the
// pinned engine and returns the finished job and what is live. A case that
// needs the container set up some way arranges it once it exists.
func uploadFile(t *testing.T, h *harness, size int, settings TransferSettingsDTO, arrange ...func()) (JobDTO, *swiftcontrol.ObjectDetail) {
	t.Helper()
	h.mock.AddContainer("media", "standard")
	for _, step := range arrange {
		step()
	}
	content := bytes.Repeat([]byte("segment!"), size/8+1)[:size]
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
	session, err := h.transfer.connections.SessionFor(t.Context(), h.bookmarkID())
	if err != nil {
		t.Fatal(err)
	}
	live, err := session.ObjectDetail(t.Context(), "media", "big.bin")
	if err != nil {
		return job, nil
	}
	if live.ContentLength != int64(size) {
		t.Errorf("live object is %d bytes, the file %d", live.ContentLength, size)
	}
	return job, live
}

// recordedLargeObjects is the kind and origin a job's settings record states.
func recordedLargeObjects(t *testing.T, job JobDTO) (kind, origin string) {
	t.Helper()
	var record struct {
		LargeObjects string            `json:"largeObjects"`
		Origins      map[string]string `json:"origins"`
	}
	if err := json.Unmarshal([]byte(job.Settings), &record); err != nil {
		t.Fatalf("settings record %q: %v", job.Settings, err)
	}
	return record.LargeObjects, record.Origins["largeObjects"]
}

func setBookmarkLargeObjects(t *testing.T, h *harness, kind string) {
	t.Helper()
	stored, err := h.transfer.connections.store.Get(t.Context(), h.bookmarkID())
	if err != nil {
		t.Fatal(err)
	}
	dto := toBookmarkDTO(stored)
	dto.LargeObjects = kind
	if _, err := h.bookmarks.Save(t.Context(), dto, ""); err != nil {
		t.Fatal(err)
	}
}

const segmentedSize = 3<<20 + 8

// A static upload does not depend on the store's listing, which lagged
// behind the last segment on the genuine Swift cloud (F35): the manifest
// names each segment and the store checks each one itself (ADR 0111, ADR 0122
// decision 3). A dynamic object's length is made from that listing, by the
// store, and is the store's to settle.
func TestStaticUploadCompletesUnderALaggingListing(t *testing.T) {
	h := newHarness(t)
	h.connect(t)
	h.mock.ListingLag = 2
	job, live := uploadFile(t, h, segmentedSize, TransferSettingsDTO{LargeObjects: string(swiftcontrol.ClassSLO)})
	if job.State != "complete" || live == nil || live.Class != swiftcontrol.ClassSLO {
		t.Fatalf("job %s %+v, live %+v", job.State, job.Counters, live)
	}
}

// What a segmented upload publishes comes from the job's choice, else the
// bookmark's, else static; a readable /info without static large objects
// turns a static choice dynamic, and an unreadable one does not. The job
// records what it published and why (ADR 0111 decision 1).
//
// A container that keeps versions turns a static choice dynamic too (ADR
// 0122 decision 3): the static manifest is written over the engine's dynamic
// one, and there the store would keep that as a version of every large file.
func TestUploadPublishesAndRecordsTheLargeObjectKind(t *testing.T) {
	const noSLO = `{"swift":{"max_file_size":5368709122}}`
	for _, tc := range []struct {
		name, info, bookmark, job string
		versions, history         bool
		size                      int
		kind, origin              string
	}{
		{name: "the default", size: segmentedSize, kind: "slo", origin: "default"},
		{name: "the bookmark's", bookmark: "dlo", size: segmentedSize, kind: "dlo", origin: "bookmark"},
		{name: "the job's over the bookmark's", bookmark: "dlo", job: "slo", size: segmentedSize, kind: "slo", origin: "override"},
		{name: "no static objects advertised", info: noSLO, size: segmentedSize, kind: "dlo", origin: "capability"},
		{name: "a dynamic choice where none are advertised", info: noSLO, job: "dlo", size: segmentedSize, kind: "dlo", origin: "override"},
		{name: "capabilities unknown", info: "not json", size: segmentedSize, kind: "slo", origin: "default"},
		{name: "a versions location", versions: true, size: segmentedSize, kind: "dlo", origin: "versioning"},
		{name: "a history location, over the job's static choice", versions: true, history: true, job: "slo", size: segmentedSize, kind: "dlo", origin: "versioning"},
		{name: "a dynamic choice where versions are kept", versions: true, job: "dlo", size: segmentedSize, kind: "dlo", origin: "override"},
		{name: "nothing segmented", job: "dlo", size: 1024},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarnessWithInfo(t, tc.info)
			h.connect(t)
			if tc.bookmark != "" {
				setBookmarkLargeObjects(t, h, tc.bookmark)
			}
			job, live := uploadFile(t, h, tc.size, TransferSettingsDTO{LargeObjects: tc.job}, func() {
				if tc.versions {
					h.mock.AddContainer("archive", "standard")
					h.mock.EnableVersionArchive("media", "archive", tc.history)
				}
			})
			if job.State != "complete" || live == nil {
				t.Fatalf("job %s %+v, live %+v", job.State, job.Counters, live)
			}
			want := swiftcontrol.ObjectClass(tc.kind)
			if tc.kind == "" {
				want = swiftcontrol.ClassPlain
			}
			if live.Class != want {
				t.Errorf("live object is %s, want %s", live.Class, want)
			}
			if kind, origin := recordedLargeObjects(t, job); kind != tc.kind || origin != tc.origin {
				t.Errorf("job records %q from %q, want %q from %q", kind, origin, tc.kind, tc.origin)
			}
		})
	}
}

// Whether the container keeps versions decides what a large file uploads as,
// so a preview that cannot read it refuses instead of guessing, and an upload
// that segments nothing does not need the answer.
func TestUploadPreviewRefusesALargeFileWhenVersioningCannotBeRead(t *testing.T) {
	for _, tc := range []struct {
		name    string
		size    int
		refused bool
	}{
		{"a file the engine segments", 2 << 20, true},
		{"a file it does not", 1024, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			h.connect(t)
			h.mock.AddContainer("media", "standard")
			path := filepath.Join(t.TempDir(), "file.bin")
			if err := os.WriteFile(path, make([]byte, tc.size), 0o600); err != nil {
				t.Fatal(err)
			}
			h.transfer.setFilePicker(fakePicker{files: []string{path}})
			picked, err := h.transfer.PickUploadFiles(t.Context(), SelectionScopeDTO{BookmarkID: h.bookmarkID(), Container: "media"})
			if err != nil {
				t.Fatal(err)
			}
			h.mock.FailNextContainerHEADs(1)
			_, err = h.transfer.PreflightUpload(t.Context(), UploadRequestDTO{
				BookmarkID: h.bookmarkID(), Container: "media", SelectionToken: picked.Token, Settings: TransferSettingsDTO{ChunkSize: "1Mi"}})
			if tc.refused && (err == nil || !strings.Contains(err.Error(), "versioning could not be read")) {
				t.Fatalf("preview ended %v, want it refused for the unread versioning", err)
			}
			if !tc.refused && err != nil {
				t.Fatalf("preview ended %v, want it to need no versioning read", err)
			}
		})
	}
}

// The preview names what the start will publish, and why when the store
// decided it, from the same capability snapshot (ADR 0111 decision 1).
func TestUploadPreviewNamesTheCapabilityDowngrade(t *testing.T) {
	h := newHarnessWithInfo(t, `{"swift":{"max_file_size":5368709122}}`)
	h.connect(t)
	h.mock.AddContainer("media", "standard")
	path := filepath.Join(t.TempDir(), "big.bin")
	if err := os.WriteFile(path, make([]byte, 2<<20), 0o600); err != nil {
		t.Fatal(err)
	}
	h.transfer.setFilePicker(fakePicker{files: []string{path}})
	picked, err := h.transfer.PickUploadFiles(t.Context(), SelectionScopeDTO{BookmarkID: h.bookmarkID(), Container: "media"})
	if err != nil {
		t.Fatal(err)
	}
	reads := h.mock.InfoRequests()
	preview, err := h.transfer.PreflightUpload(t.Context(), UploadRequestDTO{
		BookmarkID: h.bookmarkID(), Container: "media", SelectionToken: picked.Token, Settings: TransferSettingsDTO{ChunkSize: "1Mi"}})
	if err != nil {
		t.Fatal(err)
	}
	if preview.LargeObjects != "dlo" || preview.LargeObjectsOrigin != "capability" {
		t.Errorf("preview names %q from %q, want dlo from capability", preview.LargeObjects, preview.LargeObjectsOrigin)
	}
	// The chunk cap still reads /info itself; the kind adds no read of its
	// own, because it uses the snapshot taken at connect.
	if extra := h.mock.InfoRequests() - reads; extra != 1 {
		t.Errorf("the preview read /info %d times, want only the chunk cap's one", extra)
	}
}

// manifestPuts counts the static manifest writes the mock has answered.
func manifestPuts(h *harness) int {
	n := 0
	for _, line := range h.mock.RequestLog() {
		if strings.HasPrefix(line, "PUT ") && strings.Contains(line, "multipart-manifest=put") {
			n++
		}
	}
	return n
}

// A store that does not return static manifests gets a dynamic upload,
// recorded as the capability's, and the connection remembers: the next
// preview names dynamic from the capability and the next job writes no
// static manifest. A store that returns them is asked once per connection
// too, by reading the first static manifest back at its own name (ADR 0111
// decisions 9 and 10, ADR 0122 decision 3).
func TestUploadLearnsWhetherTheStoreReturnsStaticManifests(t *testing.T) {
	for _, tc := range []struct {
		name         string
		notReturned  bool
		kind, origin string
		// first and second are the static manifests each job writes, and
		// reads how often the first one is read back.
		first, second, reads int
	}{
		{"returned", false, "slo", "default", 1, 1, 1},
		{"not returned", true, "dlo", "capability", 1, 0, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			h.connect(t)
			h.mock.ManifestsNotReturned = tc.notReturned
			job, live := uploadFile(t, h, segmentedSize, TransferSettingsDTO{})
			if job.State != "complete" || live == nil || string(live.Class) != tc.kind {
				t.Fatalf("first job %s, live %+v; want a complete %s upload", job.State, live, tc.kind)
			}
			if kind, origin := recordedLargeObjects(t, job); kind != tc.kind || origin != tc.origin {
				t.Errorf("first job records %q from %q, want %q from %q", kind, origin, tc.kind, tc.origin)
			}
			first := manifestPuts(h)

			path := filepath.Join(t.TempDir(), "again.bin")
			if err := os.WriteFile(path, bytes.Repeat([]byte("again!!!"), segmentedSize/8), 0o600); err != nil {
				t.Fatal(err)
			}
			h.transfer.setFilePicker(fakePicker{files: []string{path}})
			picked, err := h.transfer.PickUploadFiles(t.Context(), SelectionScopeDTO{BookmarkID: h.bookmarkID(), Container: "media"})
			if err != nil {
				t.Fatal(err)
			}
			req := UploadRequestDTO{BookmarkID: h.bookmarkID(), Container: "media", SelectionToken: picked.Token, Settings: TransferSettingsDTO{ChunkSize: "1Mi"}}
			preview, err := h.transfer.PreflightUpload(t.Context(), req)
			if err != nil || preview.LargeObjects != tc.kind || preview.LargeObjectsOrigin != tc.origin {
				t.Fatalf("second preview names %q from %q (%v), want %q from %q", preview.LargeObjects, preview.LargeObjectsOrigin, err, tc.kind, tc.origin)
			}
			id, err := h.transfer.StartUpload(t.Context(), req)
			if err != nil {
				t.Fatal(err)
			}
			if second := awaitTerminal(t, h, id); second.State != "complete" {
				t.Fatalf("second job %s", second.State)
			}
			if second := manifestPuts(h) - first; first != tc.first || second != tc.second {
				t.Errorf("static manifest writes: first job %d, second %d; want %d and %d", first, second, tc.first, tc.second)
			}
			reads := 0
			for _, line := range h.mock.RequestLog() {
				if strings.HasPrefix(line, "GET ") && strings.Contains(line, "multipart-manifest=get") {
					reads++
				}
			}
			if reads != tc.reads {
				t.Errorf("static manifest reads = %d, want %d: the question is asked once for the connection", reads, tc.reads)
			}
		})
	}
}
