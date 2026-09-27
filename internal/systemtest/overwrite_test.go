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

// relabel renames a bookmark through the public service, the way the
// operator would, so a confirmation is exercised against a label the
// store actually holds. The binding is unchanged, so the stored key is
// retained and the account can still be connected afterwards.
func (h *Harness) relabel(a *Account, label string) {
	h.T.Helper()
	if _, err := h.Bookmarks.Save(h.ctx, app.BookmarkDTO{ID: a.BookmarkID,
		DisplayName: label, AuthURL: a.authURL, Username: a.user}, ""); err != nil {
		h.T.Fatalf("relabelling %s: %v", a.BookmarkID, err)
	}
}

// accountName is the account this fixture's own auth response named, taken
// from the harness's raw client so the expectation does not come from the
// code being tested.
func (h *Harness) accountName(bookmarkID string) string {
	for _, a := range []*Account{h.Primary, h.Secondary} {
		if a == nil || a.BookmarkID != bookmarkID {
			continue
		}
		if i := strings.LastIndex(a.storageURL, "/"); i >= 0 {
			return a.storageURL[i+1:]
		}
		return a.storageURL
	}
	return ""
}

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

	// ADR 0081 decision 3, which exists because of ADR 0073's
	// counterexample: a page can connect account B while displaying
	// account A, every backend check legitimately binds B, and a
	// same-named container with the same number of collisions produces an
	// identical question. So the question has to name the account the
	// backend resolved, or the operator has nothing to tell them apart
	// with.
	//
	// The statement also has to survive being shortened for display. A
	// label long enough to fill the whole display allowance on its own
	// used to consume it and leave the resolved account out, which
	// recreates the very ambiguity the account was added to resolve. The
	// long label is given to BOTH accounts, so in that shape nothing but
	// the resolved account can tell the two questions apart.
	t.Run("names the account it resolved, not just the count", func(t *testing.T) {
		for _, shape := range []struct {
			name, label string
			sameLabel   bool
		}{
			{name: "as the accounts are labelled"},
			{name: "under one long label on both", label: strings.Repeat("long-bookmark-label-", 12), sameLabel: true},
		} {
			t.Run(shape.name, func(t *testing.T) {
				h := makeHarness(t)
				// The same container name in both accounts, holding the same one
				// collision, which Container cannot do because it counts.
				name := h.ns + "-overwrite-ambiguous"
				for _, account := range []*Account{h.Primary, h.Secondary} {
					h.rawPut(account, "/"+name, nil, nil)
					acc := account
					t.Cleanup(func() { h.purge(acc, name) })
					h.Seed(account, name, "live/taken", []byte("the bytes already there"), nil)
					if shape.label != "" {
						h.relabel(account, shape.label)
					}
				}
				path := filepath.Join(t.TempDir(), "taken")
				if err := os.WriteFile(path, []byte("the bytes this job sent"), 0o600); err != nil {
					t.Fatal(err)
				}

				asked := map[string]app.OverwriteConfirmation{}
				for _, account := range []*Account{h.Primary, h.Secondary} {
					confirmer := &serviceConfirmer{yes: false}
					app.WireOverwriteConfirmer(h.Transfer, confirmer)
					if _, err := h.Browse.Connect(t.Context(), account.BookmarkID); err != nil {
						t.Fatal(err)
					}
					app.WireFilePicker(h.Transfer, listingFilePicker{files: []string{path}, dir: filepath.Dir(path)})
					picked, err := h.Transfer.PickUploadFiles(t.Context(), app.SelectionScopeDTO{
						BookmarkID: account.BookmarkID, Container: name, Prefix: "live/"})
					if err != nil || picked == nil {
						t.Fatalf("pick: %+v %v", picked, err)
					}
					if _, err := h.Transfer.StartUpload(t.Context(), app.UploadRequestDTO{
						BookmarkID: account.BookmarkID, Container: name, Prefix: "live/",
						SelectionToken: picked.Token, AcknowledgeOverwrite: true}); err == nil {
						t.Fatal("an unanswered confirmation admitted the start")
					}
					questions := confirmer.questions()
					if len(questions) != 1 {
						t.Fatalf("the confirmation was asked %d times for %s", len(questions), account.BookmarkID)
					}
					asked[account.BookmarkID] = questions[0]
				}

				first, second := asked[h.Primary.BookmarkID], asked[h.Secondary.BookmarkID]
				// Identical in every way the count-only question would have been.
				if first.Container != second.Container || len(first.Names) != len(second.Names) ||
					first.Prefix != second.Prefix {
					t.Fatalf("the fixture did not produce the ambiguous pair: %+v and %+v", first, second)
				}
				// And, in the long shape, identical in the label as well, so
				// the fixture is the whole counterexample and not half of it.
				if shape.sameLabel && (first.Bookmark != second.Bookmark || first.Bookmark == "") {
					t.Fatalf("the long-label fixture did not produce one label: %q and %q", first.Bookmark, second.Bookmark)
				}
				// And distinguishable only because the account is named.
				if first.Account == "" || second.Account == "" || first.Account == second.Account {
					t.Fatalf("the two questions are indistinguishable: %q and %q", first.Account, second.Account)
				}
				// The account survives in full: a shortened statement that
				// merely differs is not the same as one that still names the
				// account the backend resolved.
				for id, about := range asked {
					if want := h.accountName(id); want == "" || about.Account != want {
						t.Errorf("the question for %s names %q, not the resolved account %q", id, about.Account, want)
					}
				}
			})
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
