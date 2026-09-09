package systemtest

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/csmart/aeronautes/internal/app"
)

type chunkLimitWrite struct {
	method, path, agent string
	size                int64
}

func destinationChunkLimitProxy(t *testing.T, h *Harness, account *Account) func() []chunkLimitWrite {
	t.Helper()
	storage, err := url.Parse(account.storageURL)
	if err != nil {
		t.Fatal(err)
	}
	target := *storage
	target.Path, target.RawPath, target.RawQuery = "", "", ""
	proxy := httputil.NewSingleHostReverseProxy(&target)
	var mu sync.Mutex
	var writes []chunkLimitWrite
	front := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/info" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"swift":{"max_file_size":1048576}}`))
			return
		}
		if r.Method == http.MethodPut || r.Method == http.MethodPost || r.Method == http.MethodDelete || r.Method == "COPY" {
			mu.Lock()
			writes = append(writes, chunkLimitWrite{r.Method, r.URL.Path, r.UserAgent(), r.ContentLength})
			mu.Unlock()
		}
		proxy.ServeHTTP(w, r)
	}))
	t.Cleanup(front.Close)
	bookmarks, err := h.Bookmarks.List(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, bookmark := range bookmarks {
		if bookmark.ID == account.BookmarkID {
			bookmark.StorageURLOverride = front.URL + storage.EscapedPath()
			if _, err := h.Bookmarks.Save(t.Context(), bookmark, account.key); err != nil {
				t.Fatal(err)
			}
			found = true
		}
	}
	if !found {
		t.Fatal("destination bookmark missing")
	}
	if _, err := h.Browse.Connect(t.Context(), account.BookmarkID); err != nil {
		t.Fatal(err)
	}
	return func() []chunkLimitWrite {
		mu.Lock()
		defer mu.Unlock()
		return append([]chunkLimitWrite(nil), writes...)
	}
}

func destinationChunkSettings(t *testing.T, h *Harness, operation, chunk string) {
	t.Helper()
	account := h.Primary
	if operation == "copy" {
		account = h.Secondary
	}
	destination := h.Container(account, "chunk-destination")
	source := h.Container(h.Primary, "chunk-source")
	other := h.Container(h.Secondary, "chunk-sentinel")
	h.Seed(account, destination, "keep", []byte("destination sentinel"), nil)
	h.Seed(account, destination, ".aeronautes/stage/other-job/keep", []byte("other job sentinel"), nil)
	h.Seed(h.Secondary, other, "keep", []byte("other account sentinel"), nil)
	h.rawPut(account, "/"+destination+"_segments", nil, nil)
	t.Cleanup(func() { h.purge(account, destination+"_segments") })
	h.Seed(account, destination+"_segments", "outside/keep", []byte("segment sentinel"), nil)
	body := bytes.Repeat([]byte("a"), (1<<20)+128)
	h.Seed(h.Primary, source, "asset", body, nil)
	root := t.TempDir()
	path := seedListingLocal(t, root, "asset", body)
	seedListingLocal(t, root, "unselected", []byte("local sentinel"))
	localBefore := snapshotListingLocal(t, root)
	beforeA, beforeB := h.SnapshotNamespace(h.Primary), h.SnapshotNamespace(h.Secondary)
	writes := destinationChunkLimitProxy(t, h, account)
	counts := func() []int64 {
		var result []int64
		for _, query := range []string{
			"SELECT COUNT(*) FROM jobs", "SELECT COUNT(*) FROM job_recovery",
			"SELECT COUNT(*) FROM job_items", "SELECT COUNT(*) FROM job_item_observations",
			"SELECT COUNT(*) FROM audit_events WHERE operation LIKE 'transfer.%'",
		} {
			var count int64
			if err := h.database.Reader().QueryRowContext(t.Context(), query).Scan(&count); err != nil {
				t.Fatal(err)
			}
			result = append(result, count)
		}
		return result
	}
	beforeCounts := counts()
	settings := app.TransferSettingsDTO{ChunkSize: chunk}
	var id string
	var err error
	if operation == "upload" {
		app.WireFilePicker(h.Transfer, listingFilePicker{files: []string{path}})
		picked, pickErr := h.Transfer.PickUploadFiles(t.Context())
		if pickErr != nil {
			t.Fatal(pickErr)
		}
		id, err = h.Transfer.StartUpload(t.Context(), app.UploadRequestDTO{BookmarkID: account.BookmarkID, Container: destination, SelectionToken: picked.Token, Settings: settings})
	} else {
		id, err = h.Transfer.StartCopy(t.Context(), app.CopyRequestDTO{SourceBookmarkID: h.Primary.BookmarkID, SourceContainer: source, DestBookmarkID: account.BookmarkID, DestContainer: destination, Settings: settings})
	}
	if id != "" {
		copyCompletionAwaitPins(t, h, h.Primary, h.Secondary)
	}
	if chunk != "" {
		if err == nil || id != "" || !strings.Contains(err.Error(), "chunk size") {
			t.Errorf("invalid chunk admitted: id=%q error=%v", id, err)
		}
		if after := counts(); !reflect.DeepEqual(beforeCounts, after) || len(writes()) != 0 {
			t.Errorf("chunk refusal admitted work: before=%v after=%v writes=%+v", beforeCounts, after, writes())
		}
		h.AssertNoCollateral(beforeA, h.SnapshotNamespace(h.Primary))
		h.AssertNoCollateral(beforeB, h.SnapshotNamespace(h.Secondary))
	} else {
		if err != nil || id == "" {
			t.Fatalf("default refused: id=%q error=%v", id, err)
		}
		job, err := h.Jobs.Get(t.Context(), id)
		if err != nil || job.State != "complete" || job.Counters.ObjectsCopied != 1 || job.Counters.ObjectsVerified != 1 || job.Counters.BytesCopied != int64(len(body)) || job.Counters.ObjectsFailed != 0 {
			t.Fatalf("capped default outcome: %+v %v", job, err)
		}
		var recorded app.TransferSettingsDTO
		if err := json.Unmarshal([]byte(job.Settings), &recorded); err != nil || recorded.ChunkSize != "1048576b" {
			t.Errorf("visible settings did not record capped default: %s %v", job.Settings, err)
		}
		var durable string
		if err := h.database.Reader().QueryRowContext(t.Context(), "SELECT settings_json FROM jobs WHERE id=?", id).Scan(&durable); err != nil || durable != job.Settings {
			t.Errorf("durable/visible settings differ: %q %v", durable, err)
		}
		var sizes []int64
		for _, write := range writes() {
			if write.method == http.MethodPut && write.size > 0 {
				if !strings.Contains(write.path, "/"+destination+"_segments/") || !strings.HasPrefix(write.agent, "rclone/") {
					t.Errorf("data did not use real segmented engine: %+v", write)
				}
				sizes = append(sizes, write.size)
			}
		}
		sort.Slice(sizes, func(i, j int) bool { return sizes[i] < sizes[j] })
		if !reflect.DeepEqual(sizes, []int64{128, 1 << 20}) {
			t.Errorf("actual engine chunks differ from cap: sizes=%v writes=%+v", sizes, writes())
		}
		afterA, afterB := h.SnapshotNamespace(h.Primary), h.SnapshotNamespace(h.Secondary)
		before, after := beforeA, afterA
		if operation == "copy" {
			before, after = beforeB, afterB
			h.AssertNoCollateral(beforeA, afterA)
		} else {
			h.AssertNoCollateral(beforeB, afterB)
		}
		object := after.Containers[destination].Objects["asset"]
		allowed := uploadRecoveryVerifyObject(t, object, body, "dlo", destination+"_segments", ".aeronautes/stage/"+id+"/batch/asset")
		if stray := collateral(before, after, append(allowed, destination+"/asset")); len(stray) != 0 {
			t.Errorf("chunk transfer changed collateral: %v", stray)
		}
		t.Logf("%s capped default: recorded=%s actual_segment_sizes=%v copied=%d verified=%d", operation, recorded.ChunkSize, sizes, job.Counters.ObjectsCopied, job.Counters.ObjectsVerified)
	}
	if !reflect.DeepEqual(localBefore, snapshotListingLocal(t, root)) {
		t.Error("chunk admission/transfer changed local source or sentinel")
	}
}

func destinationChunkSettingsCases(t *testing.T, makeHarness func(*testing.T) *Harness) {
	for _, operation := range []string{"upload", "copy"} {
		t.Run(operation, func(t *testing.T) {
			for _, tc := range []struct{ name, chunk string }{{"above_maximum", "8Mi"}, {"below_minimum", "512Ki"}, {"capped_default", ""}} {
				t.Run(tc.name, func(t *testing.T) { destinationChunkSettings(t, makeHarness(t), operation, tc.chunk) })
			}
		})
	}
}

func TestDestinationChunkSettingsMock(t *testing.T) {
	destinationChunkSettingsCases(t, findMockHarness)
}
func TestDestinationChunkSettingsRealSwift(t *testing.T) {
	destinationChunkSettingsCases(t, New)
}
