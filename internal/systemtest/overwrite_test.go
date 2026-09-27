package systemtest

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/csmart/aeronautes/internal/app"
	"github.com/csmart/aeronautes/internal/audit"
)

// Maintained regressions for ADR 0081 through the public service, on both
// fixtures: what an acknowledged overwrite replaces, what it refuses, what
// it records about what was there, and what it leaves alone.
//
// The contract's answer is that a replaced large object's fragments are
// recorded and RETAINED, never removed, because the segment prefix is
// published in the manifest's own header and anything that can read the
// object can reference those segments. The preservation case is what holds
// that answer in place.

type serviceConfirmer struct {
	mu    sync.Mutex
	asked []app.OverwriteConfirmation
	yes   bool
}

func (c *serviceConfirmer) ConfirmOverwrite(_ context.Context, about app.OverwriteConfirmation) (bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.asked = append(c.asked, about)
	return c.yes, nil
}

func (c *serviceConfirmer) questions() []app.OverwriteConfirmation {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]app.OverwriteConfirmation(nil), c.asked...)
}

// overwriteStart picks the given files and starts one upload, returning the
// job id and whatever the start said.
func overwriteStart(t *testing.T, h *Harness, container, prefix string, paths []string,
	acknowledge bool) (string, error) {
	t.Helper()
	dir := filepath.Dir(paths[0])
	app.WireFilePicker(h.Transfer, listingFilePicker{files: paths, dir: dir})
	picked, err := h.Transfer.PickUploadFiles(t.Context(), app.SelectionScopeDTO{
		BookmarkID: h.Primary.BookmarkID, Container: container, Prefix: prefix})
	if err != nil || picked == nil {
		t.Fatalf("pick: %+v %v", picked, err)
	}
	return h.Transfer.StartUpload(t.Context(), app.UploadRequestDTO{
		BookmarkID: h.Primary.BookmarkID, Container: container, Prefix: prefix,
		SelectionToken: picked.Token, AcknowledgeOverwrite: acknowledge})
}

// liveETag is what identifies the object at a name now.
func liveETag(t *testing.T, h *Harness, container, name string) string {
	t.Helper()
	detail, err := h.Inspector.Object(t.Context(), h.Primary.BookmarkID, container, name)
	if err != nil {
		t.Fatalf("reading %s/%s: %v", container, name, err)
	}
	return strings.Trim(detail.ETag, `"`)
}

// replacementRecord is the one audit row a replacement leaves.
func replacementRecord(t *testing.T, h *Harness, resource string) audit.Event {
	t.Helper()
	events, err := audit.NewStore(h.database).List(t.Context(), audit.Query{Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	var found []audit.Event
	for _, event := range events {
		if event.Operation == "object.upload.replace" && event.Resource == resource {
			found = append(found, event)
		}
	}
	if len(found) != 1 {
		t.Fatalf("a replacement of %s left %d records: %+v", resource, len(found), events)
	}
	return found[0]
}

func TestOverwriteMock(t *testing.T)      { overwriteCases(t, findMockHarness) }
func TestOverwriteRealSwift(t *testing.T) { overwriteCases(t, New) }

func overwriteCases(t *testing.T, makeHarness func(*testing.T) *Harness) {
	t.Helper()
	t.Run("replaces a plain object and records what it saw", func(t *testing.T) {
		h := makeHarness(t)
		container := h.Container(h.Primary, "overwrite-plain")
		other := h.Container(h.Secondary, "overwrite-untouched")
		h.Seed(h.Primary, container, "live/taken", []byte("the bytes already there"), nil)
		h.Seed(h.Secondary, other, "keep", []byte("other account sentinel"), nil)
		beforeB := h.SnapshotNamespace(h.Secondary)
		priorTag := liveETag(t, h, container, "live/taken")

		confirmer := &serviceConfirmer{yes: true}
		app.WireOverwriteConfirmer(h.Transfer, confirmer)
		if _, err := h.Browse.Connect(t.Context(), h.Primary.BookmarkID); err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(t.TempDir(), "taken")
		if err := os.WriteFile(path, []byte("the bytes this job sent"), 0o600); err != nil {
			t.Fatal(err)
		}
		id, err := overwriteStart(t, h, container, "live/", []string{path}, true)
		if err != nil {
			t.Fatalf("a confirmed replacement was refused: %v", err)
		}
		h.AwaitJob(id)
		job, err := h.Jobs.Get(t.Context(), h.Primary.BookmarkID, id)
		if err != nil {
			t.Fatal(err)
		}
		if job.State != "complete" || job.Counters.ObjectsCopied != 1 || job.Counters.Overwrites != 1 {
			t.Fatalf("the replacement did not publish or was not counted: %s %+v", job.State, job.Counters)
		}
		if now := liveETag(t, h, container, "live/taken"); now == priorTag {
			t.Fatal("the object was not replaced")
		}
		// The question named what the backend resolved, and the record
		// names what was there rather than what replaced it.
		asked := confirmer.questions()
		if len(asked) != 1 || asked[0].Container != container || len(asked[0].Names) != 1 ||
			asked[0].Names[0] != "live/taken" {
			t.Fatalf("the confirmation did not name the resolved plan: %+v", asked)
		}
		record := replacementRecord(t, h, container+"/live/taken")
		if record.Result != audit.ResultObserved || !strings.Contains(record.Summary, priorTag) ||
			!strings.Contains(record.Summary, "may not be that one") {
			t.Fatalf("the record does not name what was seen: %+v", record)
		}
		// A plain replacement inventories no fragments and touches no
		// segment container.
		if strings.Contains(record.Summary, "segment") {
			t.Errorf("a plain replacement claimed a fragment inventory: %q", record.Summary)
		}
		h.AssertNoCollateral(beforeB, h.SnapshotNamespace(h.Secondary))
	})

	t.Run("retains a replaced manifest's fragments and outside references", func(t *testing.T) {
		h := makeHarness(t)
		container := h.Container(h.Primary, "overwrite-manifest")
		segments := container + "_segments"
		h.rawPut(h.Primary, "/"+segments, nil, nil)
		t.Cleanup(func() { h.purge(h.Primary, segments) })
		h.Seed(h.Primary, segments, "subject/run/0001", []byte("01234567"), nil)
		h.Seed(h.Primary, segments, "subject/run/0002", []byte("89abcdef"), nil)
		h.Seed(h.Primary, container, "live/taken", nil,
			map[string]string{"X-Object-Manifest": segments + "/subject/run/"})
		// An outside object over the same segments, which anything that can
		// read the predecessor could have made.
		h.Seed(h.Primary, container, "outside/alias", nil,
			map[string]string{"X-Object-Manifest": segments + "/subject/run/"})

		confirmer := &serviceConfirmer{yes: true}
		app.WireOverwriteConfirmer(h.Transfer, confirmer)
		if _, err := h.Browse.Connect(t.Context(), h.Primary.BookmarkID); err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(t.TempDir(), "taken")
		if err := os.WriteFile(path, []byte("the bytes this job sent"), 0o600); err != nil {
			t.Fatal(err)
		}
		id, err := overwriteStart(t, h, container, "live/", []string{path}, true)
		if err != nil {
			t.Fatalf("a confirmed replacement of a manifest was refused: %v", err)
		}
		h.AwaitJob(id)
		job, err := h.Jobs.Get(t.Context(), h.Primary.BookmarkID, id)
		if err != nil {
			t.Fatal(err)
		}
		if job.State != "complete" || job.Counters.Overwrites != 1 {
			t.Fatalf("the manifest replacement did not publish: %s %+v", job.State, job.Counters)
		}
		// Every segment is still there, and the record says they were kept.
		for _, name := range []string{"subject/run/0001", "subject/run/0002"} {
			if _, err := h.Inspector.Object(t.Context(), h.Primary.BookmarkID, segments, name); err != nil {
				t.Errorf("a replaced manifest's segment was removed: %s: %v", name, err)
			}
		}
		record := replacementRecord(t, h, container+"/live/taken")
		for _, want := range []string{"2 segments", "listing may undercount", "none was removed"} {
			if !strings.Contains(record.Summary, want) {
				t.Errorf("the record does not say %q: %q", want, record.Summary)
			}
		}
		// The outside object still assembles its own bytes, which is why
		// this contract retains rather than reclaims.
		body, err := rawGet(h.Primary, "/"+container+"/outside/alias", "")
		if err != nil || string(body) != "0123456789abcdef" {
			t.Fatalf("an outside reference lost its backing data: %q %v", body, err)
		}
	})

	t.Run("refuses without an acknowledgement and without a yes", func(t *testing.T) {
		for _, tc := range []struct {
			name        string
			acknowledge bool
			yes         bool
		}{
			{"unacknowledged", false, true},
			{"answered no", true, false},
		} {
			t.Run(tc.name, func(t *testing.T) {
				h := makeHarness(t)
				container := h.Container(h.Primary, "overwrite-refused")
				h.Seed(h.Primary, container, "live/taken", []byte("the bytes already there"), nil)
				before := h.SnapshotNamespace(h.Primary)
				priorTag := liveETag(t, h, container, "live/taken")
				app.WireOverwriteConfirmer(h.Transfer, &serviceConfirmer{yes: tc.yes})
				if _, err := h.Browse.Connect(t.Context(), h.Primary.BookmarkID); err != nil {
					t.Fatal(err)
				}
				path := filepath.Join(t.TempDir(), "taken")
				if err := os.WriteFile(path, []byte("the bytes this job sent"), 0o600); err != nil {
					t.Fatal(err)
				}
				id, err := overwriteStart(t, h, container, "live/", []string{path}, tc.acknowledge)
				if err == nil {
					h.AwaitJob(id)
					t.Fatalf("the start was admitted: job %s", id)
				}
				if now := liveETag(t, h, container, "live/taken"); now != priorTag {
					t.Fatalf("a refused overwrite changed the object: %q, was %q", now, priorTag)
				}
				h.AssertNoCollateral(before, h.SnapshotNamespace(h.Primary))
			})
		}
	})
}
