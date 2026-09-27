package systemtest

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/csmart/aeronautes/internal/app"
	"github.com/csmart/aeronautes/internal/audit"
	"github.com/csmart/aeronautes/internal/db"
	"github.com/csmart/aeronautes/internal/jobs"
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
		// Two outside objects over EXACTLY those segments, which anything
		// that can read the predecessor could have made, because the prefix
		// is published in the predecessor's own header. The static one names
		// the segment identities one by one, so it shares them rather than
		// holding private copies: an alias over its own copies would survive
		// the deletion of every part it was supposed to be sharing.
		h.Seed(h.Primary, container, "outside/alias", nil,
			map[string]string{"X-Object-Manifest": segments + "/subject/run/"})
		h.SeedSLO(h.Primary, container, "outside/slo", `[`+
			`{"path":"`+segments+`/subject/run/0001","etag":"`+md5of("01234567")+`","size_bytes":8},`+
			`{"path":"`+segments+`/subject/run/0002","etag":"`+md5of("89abcdef")+`","size_bytes":8}]`)

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
		// Both outside objects still assemble their own bytes from those
		// same segments, which is why this contract retains rather than
		// reclaims. This store assembles a large object on read, so the
		// bytes are evidence here in a way a cached copy would not be.
		for _, name := range []string{"outside/alias", "outside/slo"} {
			body, err := rawGet(h.Primary, "/"+container+"/"+name, "")
			if err != nil || string(body) != "0123456789abcdef" {
				t.Errorf("an outside reference lost its backing data: %s: %q %v", name, body, err)
			}
		}
		// And the static alias still names the same two leaf objects, read
		// from its own manifest rather than from anything this route says.
		manifest, err := rawGet(h.Primary, "/"+container+"/outside/slo", "multipart-manifest=get")
		if err != nil {
			t.Fatalf("the outside manifest could not be read: %v", err)
		}
		for _, name := range []string{"subject/run/0001", "subject/run/0002"} {
			if !strings.Contains(string(manifest), segments+"/"+name) {
				t.Errorf("the outside manifest no longer names %s: %s", name, manifest)
			}
		}

		// The durable record names those segments, which is what an explicit
		// cleanup would have to read: after the job is terminal, and again
		// through a store opened on the same file that never saw them
		// written.
		want := []jobs.ItemFragment{
			{Container: segments, Object: "subject/run/0001"},
			{Container: segments, Object: "subject/run/0002"},
		}
		for _, source := range []struct {
			name  string
			store *jobs.Store
		}{
			{"the running store", h.jobStore},
			{"a reopened store", reopenedJobs(t, h)},
		} {
			read, ok, err := source.store.ReadPredecessor(t.Context(), id, "live/taken")
			if err != nil || !ok {
				t.Fatalf("%s: read back: ok=%v %v", source.name, ok, err)
			}
			if !reflect.DeepEqual(read.Fragments, want) {
				t.Errorf("%s names %+v, not the segments the manifest named %+v", source.name, read.Fragments, want)
			}
			if read.Prefix != segments+"/subject/run/" || read.Exact || read.Counted != 2 {
				t.Errorf("%s does not describe a listed prefix: %+v", source.name, read)
			}
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

	// ADR 0081 decision 7's narrower promise, exercised rather than
	// asserted: an unchanged predecessor is promised only where the
	// replacement was not attempted or is proven refused. Where it may have
	// landed, or did land and could not then be verified, the record says
	// so and claims no rollback.
	//
	// Both shapes need a store that misbehaves at an exact protocol point —
	// a response lost after the write applied, and a read that fails only
	// after it — so they run on the in-process fixture. Nothing about them
	// is a refusal: the point is an outcome that is unknown.
	t.Run("claims no rollback where a replacement may have landed", func(t *testing.T) {
		for _, tc := range []struct {
			name string
			// arm installs the fault; stage is where the route must say the
			// outcome became unknown.
			arm   func(h *Harness, container string)
			stage string
		}{
			{name: "the publication's response is lost", stage: "commit",
				arm: func(h *Harness, container string) {
					h.MockPrimary.DropNextObjectPUTResponse(container, "live/taken")
				}},
			{name: "verification fails after it landed", stage: "verification",
				arm: func(h *Harness, container string) {
					// Armed at the exact point the publication is served, so
					// the reads before it are untouched.
					h.MockPrimary.AfterRequest = func(method, path string) {
						if method == "PUT" && strings.HasSuffix(path, "/live/taken") {
							h.MockPrimary.FailNextObjectHEADs(1)
						}
					}
				}},
		} {
			t.Run(tc.name, func(t *testing.T) {
				h := makeHarness(t)
				if h.MockPrimary == nil {
					t.Skip("a lost response and a read that fails only after a write need the in-process store")
				}
				container := h.Container(h.Primary, "overwrite-unresolved")
				segments := container + "_segments"
				h.rawPut(h.Primary, "/"+segments, nil, nil)
				t.Cleanup(func() { h.purge(h.Primary, segments) })
				h.Seed(h.Primary, segments, "subject/run/0001", []byte("01234567"), nil)
				h.Seed(h.Primary, segments, "subject/run/0002", []byte("89abcdef"), nil)
				h.Seed(h.Primary, container, "live/taken", nil,
					map[string]string{"X-Object-Manifest": segments + "/subject/run/"})
				priorTag := liveETag(t, h, container, "live/taken")

				app.WireOverwriteConfirmer(h.Transfer, &serviceConfirmer{yes: true})
				if _, err := h.Browse.Connect(t.Context(), h.Primary.BookmarkID); err != nil {
					t.Fatal(err)
				}
				path := filepath.Join(t.TempDir(), "taken")
				const sent = "the bytes this job sent"
				if err := os.WriteFile(path, []byte(sent), 0o600); err != nil {
					t.Fatal(err)
				}
				tc.arm(h, container)
				id, err := overwriteStart(t, h, container, "live/", []string{path}, true)
				if err != nil {
					t.Fatalf("a confirmed replacement was refused before it was attempted: %v", err)
				}
				h.AwaitJob(id)
				job, err := h.Jobs.Get(t.Context(), h.Primary.BookmarkID, id)
				if err != nil {
					t.Fatal(err)
				}
				// Neither shape may be reported as a clean success, and
				// neither may be reported as having left the predecessor.
				if job.State == "complete" {
					t.Errorf("an unverified replacement was reported complete: %+v", job.Counters)
				}
				if job.Counters.ObjectsVerified != 0 {
					t.Errorf("an unverified replacement was counted as verified: %+v", job.Counters)
				}
				// Neither is counted as an overwrite. The counter counts
				// VERIFIED publications, and on both shapes the publication
				// is unverified even though one of them landed, which is
				// exactly why the counter is not a count of displaced
				// objects.
				if job.Counters.Overwrites != 0 {
					t.Errorf("an unverified replacement was counted: %+v", job.Counters)
				}
				for _, forbidden := range []string{"unchanged", "rolled back", "rollback", "left as it was", "restored"} {
					if strings.Contains(strings.ToLower(job.Error), forbidden) {
						t.Errorf("the job claims %q about the predecessor: %q", forbidden, job.Error)
					}
				}

				// What the store actually holds, read independently: on both
				// shapes the write reached it, so the predecessor is gone and
				// nothing may say otherwise.
				body, err := rawGet(h.Primary, "/"+container+"/live/taken", "")
				if err != nil || string(body) != sent {
					t.Errorf("the live object is neither what was sent nor readable: %q %v", body, err)
				}
				if now := liveETag(t, h, container, "live/taken"); now == priorTag {
					t.Error("the fixture did not land the write it was supposed to lose the answer to")
				}
				// And its segments are all still there, because nothing in
				// this route removes one whatever the outcome was.
				for _, name := range []string{"subject/run/0001", "subject/run/0002"} {
					if _, err := h.Inspector.Object(t.Context(), h.Primary.BookmarkID, segments, name); err != nil {
						t.Errorf("an unresolved replacement removed a segment: %s: %v", name, err)
					}
				}

				// The stored item outcome says the effect is unresolved, which
				// is the whole of what this contract promises here.
				page, err := h.Jobs.Items(t.Context(), h.Primary.BookmarkID, id, "", 10)
				if err != nil || page.Status != "available" || len(page.Items) != 1 {
					t.Fatalf("items: %+v %v", page, err)
				}
				item := page.Items[0]
				if item.Execution == nil {
					t.Fatalf("the item has no execution outcome: %+v", item)
				}
				// Unresolved, not not_attempted: a write whose answer is
				// unknown must never be reported as one that did not happen,
				// which is the only claim about the predecessor this contract
				// makes here.
				if item.Execution.Result != "failed" || item.Execution.Effect != "unresolved" {
					t.Errorf("the outcome does not say it is unknown: %+v", item.Execution)
				}
				if item.Execution.Stage != tc.stage {
					t.Errorf("the outcome became unknown at %q, not %q", item.Execution.Stage, tc.stage)
				}
				if item.Verification != nil && item.Verification.Result == "verified" {
					t.Errorf("an unverified item carries a verification: %+v", item.Verification)
				}

				// The predecessor record was written before any of this, and
				// it still names what was there.
				read, ok, err := h.jobStore.ReadPredecessor(t.Context(), id, "live/taken")
				if err != nil || !ok || read.Named != 2 || read.ETag != priorTag {
					t.Fatalf("the record of what was there did not survive: %+v ok=%v %v", read, ok, err)
				}
				record := replacementRecord(t, h, container+"/live/taken")
				if !strings.Contains(record.Summary, "may not be that one") {
					t.Errorf("the record reads as an outcome rather than an observation: %q", record.Summary)
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

// md5of is an expectation computed here rather than read from the code
// under test.
func md5of(content string) string {
	sum := md5.Sum([]byte(content))
	return hex.EncodeToString(sum[:])
}

// reopenedJobs opens the harness's database file again, so a durable record
// is read through a store that never saw it written.
func reopenedJobs(t *testing.T, h *Harness) *jobs.Store {
	t.Helper()
	reopened, err := db.Open(t.Context(), h.statePath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	return jobs.NewStore(reopened)
}
