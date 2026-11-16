package systemtest

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/csmart/aeronautes/internal/app"
)

// Maintained regressions for an acknowledged overwrite through the public
// service, on both fixtures: what it asks, what it replaces, what it refuses
// and what it leaves alone (ADR 0081 decisions 2 to 4, ADR 0122 decision 3).
//
// A replaced large object's old segments are removed by the transfer engine
// as it writes the new object, which is what rclone does when used alone,
// with an option for the operator who cannot accept the consequence. That
// consequence — an outside reference over the predecessor's segments loses
// its backing data — is a case here rather than a sentence, in both
// directions of the option.

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
	// before runs inside the dialog, after the question is composed and
	// before it is answered, which is where the store can move under an
	// answer.
	before func()
}

func (c *serviceConfirmer) ConfirmOverwrite(_ context.Context, about app.OverwriteConfirmation) (bool, error) {
	c.mu.Lock()
	c.asked = append(c.asked, about)
	c.mu.Unlock()
	if c.before != nil {
		c.before()
	}
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

// setReclamation moves the operator's option the way the settings page
// does, through the service the shell wires.
func setReclamation(t *testing.T, h *Harness, on bool) {
	t.Helper()
	current, err := h.Settings.Get(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	current.ReclaimReplacedFragments = on
	if _, err := h.Settings.Set(t.Context(), current); err != nil {
		t.Fatal(err)
	}
}

func TestOverwriteMock(t *testing.T)      { overwriteCases(t, findMockHarness) }
func TestOverwriteRealSwift(t *testing.T) { overwriteCases(t, New) }

func overwriteCases(t *testing.T, makeHarness func(*testing.T) *Harness) {
	t.Helper()
	// ADR 0122 decision 1: another writer changes the name while the operator
	// is answering, which is after the preview and before the request. The
	// upload writes over what that writer left and reports success, because
	// the last write to a name wins and nothing compares the name with what
	// the question was composed from.
	t.Run("writes over a name another writer changed during the dialog", func(t *testing.T) {
		h := makeHarness(t)
		container := h.Container(h.Primary, "overwrite-other-writer")
		h.Seed(h.Primary, container, "live/taken", []byte("the bytes already there"), nil)

		const sent, others = "the bytes this job sent", "the bytes another writer sent"
		confirmer := &serviceConfirmer{yes: true, before: func() {
			h.rawPut(h.Primary, "/"+container+"/live/taken", []byte(others), nil)
		}}
		app.WireOverwriteConfirmer(h.Transfer, confirmer)
		if _, err := h.Browse.Connect(t.Context(), h.Primary.BookmarkID); err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(t.TempDir(), "taken")
		if err := os.WriteFile(path, []byte(sent), 0o600); err != nil {
			t.Fatal(err)
		}
		id, err := overwriteStart(t, h, container, "live/", []string{path}, true)
		if err != nil {
			t.Fatalf("a confirmed replacement was refused at the start: %v", err)
		}
		job := h.AwaitJob(id)
		if job.State != "complete" || job.Counters.ObjectsCopied != 1 || job.Counters.Overwrites != 1 || job.Counters.ObjectsFailed != 0 {
			t.Fatalf("a replacement of a name another writer changed = %s %+v; want complete with one name replaced", job.State, job.Counters)
		}
		if len(confirmer.questions()) != 1 {
			t.Fatalf("the confirmation was asked %d times", len(confirmer.questions()))
		}
		if now := liveETag(t, h, container, "live/taken"); now != md5of(sent) {
			t.Fatalf("the name holds %s, want this job's bytes %s (the other writer's are %s)", now, md5of(sent), md5of(others))
		}
	})

	t.Run("replaces a plain object", func(t *testing.T) {
		h := makeHarness(t)
		container := h.Container(h.Primary, "overwrite-plain")
		other := h.Container(h.Secondary, "overwrite-untouched")
		h.Seed(h.Primary, container, "live/taken", []byte("the bytes already there"), nil)
		h.Seed(h.Secondary, other, "keep", []byte("other account sentinel"), nil)
		beforeB := h.SnapshotNamespace(h.Secondary)

		confirmer := &serviceConfirmer{yes: true}
		app.WireOverwriteConfirmer(h.Transfer, confirmer)
		if _, err := h.Browse.Connect(t.Context(), h.Primary.BookmarkID); err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(t.TempDir(), "taken")
		const sent = "the bytes this job sent"
		if err := os.WriteFile(path, []byte(sent), 0o600); err != nil {
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
			t.Fatalf("the replacement was not written or was not counted: %s %+v", job.State, job.Counters)
		}
		if now := liveETag(t, h, container, "live/taken"); now != md5of(sent) {
			t.Fatalf("the name holds %s, want the bytes this job sent %s", now, md5of(sent))
		}
		// The question named what the backend resolved.
		asked := confirmer.questions()
		if len(asked) != 1 || asked[0].Container != container || len(asked[0].Names) != 1 ||
			asked[0].Names[0] != "live/taken" {
			t.Fatalf("the confirmation did not name the resolved plan: %+v", asked)
		}
		h.AssertNoCollateral(beforeB, h.SnapshotNamespace(h.Secondary))
	})

	// ADR 0122 decision 3, in both directions of the operator's option, and
	// on both sides of where the engine may delete. The fixture is the
	// exposure itself: two outside objects over EXACTLY the segments the
	// predecessor published in its own header, which anything that could read
	// the predecessor could have made. The engine's removal takes their
	// backing data with it, and that is the accepted consequence rather than
	// a surprise; turning the option off preserves them, and so does keeping
	// the segments outside the folder being uploaded to, where the engine may
	// not delete. The manifest names its prefix with a trailing slash, as
	// python-swiftclient writes it.
	for _, mode := range []struct {
		name string
		// under is where the predecessor's segments lie in the segments
		// container, and removed whether the engine takes them.
		under             string
		reclaims, removed bool
	}{
		{name: "removes a replaced manifest's segments under the upload's folder", under: "live/taken/run/", reclaims: true, removed: true},
		{name: "keeps a replaced manifest's segments when the option is off", under: "live/taken/run/"},
		{name: "keeps a replaced manifest's segments that lie outside the upload's folder", under: "subject/run/", reclaims: true},
	} {
		t.Run(mode.name, func(t *testing.T) {
			h := makeHarness(t)
			container := h.Container(h.Primary, "overwrite-manifest")
			segments := container + "_segments"
			h.rawPut(h.Primary, "/"+segments, nil, nil)
			t.Cleanup(func() { h.purge(h.Primary, segments) })
			h.Seed(h.Primary, segments, mode.under+"0001", []byte("01234567"), nil)
			h.Seed(h.Primary, segments, mode.under+"0002", []byte("89abcdef"), nil)
			h.Seed(h.Primary, container, "live/taken", nil,
				map[string]string{"X-Object-Manifest": segments + "/" + mode.under})
			// The static alias names the segment identities one by one, so it
			// shares them rather than holding private copies: an alias over
			// its own copies would survive the deletion of every part it was
			// supposed to be sharing and would prove nothing either way.
			h.Seed(h.Primary, container, "outside/alias", nil,
				map[string]string{"X-Object-Manifest": segments + "/" + mode.under})
			h.SeedSLO(h.Primary, container, "outside/slo", `[`+
				`{"path":"`+segments+`/`+mode.under+`0001","etag":"`+md5of("01234567")+`","size_bytes":8},`+
				`{"path":"`+segments+`/`+mode.under+`0002","etag":"`+md5of("89abcdef")+`","size_bytes":8}]`)
			setReclamation(t, h, mode.reclaims)

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
				t.Fatalf("the manifest replacement was not written: %s %+v", job.State, job.Counters)
			}
			// The operator was told which way it would go before answering.
			asked := confirmer.questions()
			wantSaid := "the transfer engine REMOVES its old segments under live/ in " + segments
			if !mode.reclaims {
				wantSaid = "are KEPT, because reclaiming is turned off"
			}
			if len(asked) != 1 || !strings.Contains(asked[0].Fragments, wantSaid) {
				t.Fatalf("the question did not say %q: %+v", wantSaid, asked)
			}

			// What the store holds now, by identity rather than by listing:
			// a container listing lags a delete either way (DESIGN §15.3).
			for _, name := range []string{mode.under + "0001", mode.under + "0002"} {
				_, err := h.Inspector.Object(t.Context(), h.Primary.BookmarkID, segments, name)
				switch {
				case mode.removed && err == nil:
					t.Errorf("a segment the question said would be removed is still an object: %s", name)
				case !mode.removed && err != nil:
					t.Errorf("a segment the question said would be kept was removed: %s: %v", name, err)
				}
			}

			// The outside references, read as bytes from this store, which
			// assembles a large object on read: the consequence is visible
			// here in a way a cached copy would not be.
			for _, name := range []string{"outside/alias", "outside/slo"} {
				body, err := rawGet(h.Primary, "/"+container+"/"+name, "")
				assembled := err == nil && string(body) == "0123456789abcdef"
				// The in-process store assembles a STATIC manifest's bytes
				// when the manifest is written and serves them from that
				// copy, so on that fixture a deleted segment is invisible
				// behind the static alias. The dynamic alias is assembled per
				// read on both, and the owned fixture assembles both.
				if name == "outside/slo" && h.MockPrimary != nil {
					continue
				}
				if mode.removed && assembled {
					t.Errorf("the question said the segments would be removed but %s still assembles them", name)
				}
				if !mode.removed && !assembled {
					t.Errorf("an outside reference lost its backing data: %s: %q %v", name, body, err)
				}
			}
			// Either way nothing removed the outside manifests themselves,
			// and the static one still names the segment identities it was
			// written with: only the replaced name's own segments can go.
			for _, name := range []string{"outside/alias", "outside/slo"} {
				if _, err := h.Inspector.Object(t.Context(), h.Primary.BookmarkID, container, name); err != nil {
					t.Errorf("an outside manifest was removed: %s: %v", name, err)
				}
			}
			manifest, err := rawGet(h.Primary, "/"+container+"/outside/slo", "multipart-manifest=get")
			if err != nil {
				t.Fatalf("the outside manifest could not be read: %v", err)
			}
			for _, name := range []string{mode.under + "0001", mode.under + "0002"} {
				if !strings.Contains(string(manifest), segments+"/"+name) {
					t.Errorf("the outside manifest no longer names %s: %s", name, manifest)
				}
			}
		})
	}

	// The other large-object shape. A static manifest names its segments one
	// by one, so the engine removes the ones the replaced manifest listed and
	// infers nothing from a prefix.
	for _, mode := range []struct {
		name     string
		reclaims bool
	}{
		{name: "removes a replaced static manifest's segments", reclaims: true},
		{name: "keeps a replaced static manifest's segments when the option is off"},
	} {
		t.Run(mode.name, func(t *testing.T) {
			h := makeHarness(t)
			container := h.Container(h.Primary, "overwrite-static")
			segments := container + "_segments"
			h.rawPut(h.Primary, "/"+segments, nil, nil)
			t.Cleanup(func() { h.purge(h.Primary, segments) })
			h.Seed(h.Primary, segments, "live/taken/parts/0001", []byte("01234567"), nil)
			h.Seed(h.Primary, segments, "live/taken/parts/0002", []byte("89abcdef"), nil)
			// The predecessor is the static manifest itself, not an alias
			// over somebody else's segments.
			h.SeedSLO(h.Primary, container, "live/taken", `[`+
				`{"path":"`+segments+`/live/taken/parts/0001","etag":"`+md5of("01234567")+`","size_bytes":8},`+
				`{"path":"`+segments+`/live/taken/parts/0002","etag":"`+md5of("89abcdef")+`","size_bytes":8}]`)
			setReclamation(t, h, mode.reclaims)

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
				t.Fatalf("a confirmed replacement of a static manifest was refused: %v", err)
			}
			h.AwaitJob(id)
			job, err := h.Jobs.Get(t.Context(), h.Primary.BookmarkID, id)
			if err != nil {
				t.Fatal(err)
			}
			if job.State != "complete" || job.Counters.Overwrites != 1 {
				t.Fatalf("the static replacement was not written: %s %+v", job.State, job.Counters)
			}
			for _, name := range []string{"live/taken/parts/0001", "live/taken/parts/0002"} {
				_, err := h.Inspector.Object(t.Context(), h.Primary.BookmarkID, segments, name)
				switch {
				case mode.reclaims && err == nil:
					t.Errorf("a segment the replaced manifest named is still an object: %s", name)
				case !mode.reclaims && err != nil:
					t.Errorf("a segment the option keeps was removed: %s: %v", name, err)
				}
			}
			// The new object is readable either way: removing the
			// predecessor's parts does not touch what replaced it.
			body, err := rawGet(h.Primary, "/"+container+"/live/taken", "")
			if err != nil || string(body) != "the bytes this job sent" {
				t.Errorf("the replacement is not readable: %q %v", body, err)
			}
		})
	}

	// ADR 0122 decision 3: a container that keeps versions keeps the replaced
	// object, and a replaced manifest's segments are that retained version's.
	// Removing them would empty a version the store is holding on the
	// operator's behalf, so the mode wins over the option. The upload reads
	// the mode once, at its start, and the question states what it decided.
	t.Run("keeps a replaced manifest's segments where the container keeps versions", func(t *testing.T) {
		h := makeHarness(t)
		archive := h.Container(h.Primary, "reclaim-archive")
		container := h.Container(h.Primary, "reclaim-versioned")
		h.rawPut(h.Primary, "/"+container, nil, map[string]string{"X-Versions-Location": archive})
		// A DELETE on a versioned container RESTORES the previous version
		// rather than removing the object, so the mode comes off before the
		// purge the container helper registered runs. Cleanups run in
		// reverse order of registration.
		t.Cleanup(func() {
			h.rawPut(h.Primary, "/"+container, nil, map[string]string{"X-Remove-Versions-Location": "yes"})
		})
		segments := container + "_segments"
		h.rawPut(h.Primary, "/"+segments, nil, nil)
		t.Cleanup(func() { h.purge(h.Primary, segments) })
		h.Seed(h.Primary, segments, "live/taken/run/0001", []byte("01234567"), nil)
		h.Seed(h.Primary, segments, "live/taken/run/0002", []byte("89abcdef"), nil)
		h.Seed(h.Primary, container, "live/taken", nil,
			map[string]string{"X-Object-Manifest": segments + "/live/taken/run/"})
		// The option is ON and the segments lie where the engine may delete,
		// so what stops this is the mode and nothing else.
		setReclamation(t, h, true)

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
			t.Fatalf("a confirmed replacement on a versioned container was refused: %v", err)
		}
		h.AwaitJob(id)
		job, err := h.Jobs.Get(t.Context(), h.Primary.BookmarkID, id)
		if err != nil {
			t.Fatal(err)
		}
		if job.State != "complete" || job.Counters.Overwrites != 1 {
			t.Fatalf("the replacement was not written: %s %+v", job.State, job.Counters)
		}
		for _, name := range []string{"live/taken/run/0001", "live/taken/run/0002"} {
			if _, err := h.Inspector.Object(t.Context(), h.Primary.BookmarkID, segments, name); err != nil {
				t.Errorf("a retained version's segment was removed: %s: %v", name, err)
			}
		}
		// And the operator was told the same thing before answering.
		if asked := confirmer.questions(); len(asked) != 1 ||
			!strings.Contains(asked[0].Fragments, "are KEPT, because the container keeps versions") {
			t.Fatalf("the question did not say the segments would be kept: %+v", asked)
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

	// ADR 0122 decision 2 keeps "unknown" as an outcome: a write whose
	// answer never arrived may have landed, so the record says the outcome
	// is unknown and claims nothing about what was there before.
	//
	// It needs a store that loses a response after the write applied, so it
	// runs on the in-process fixture.
	t.Run("claims no rollback where a replacement's answer is lost", func(t *testing.T) {
		h := makeHarness(t)
		if h.MockPrimary == nil {
			t.Skip("a response lost after the write applied needs the in-process store")
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
		h.MockPrimary.DropNextObjectPUTResponse(container, "live/taken")
		id, err := overwriteStart(t, h, container, "live/", []string{path}, true)
		if err != nil {
			t.Fatalf("a confirmed replacement was refused before it was attempted: %v", err)
		}
		h.AwaitJob(id)
		job, err := h.Jobs.Get(t.Context(), h.Primary.BookmarkID, id)
		if err != nil {
			t.Fatal(err)
		}
		// It may not be reported as a clean success, and it may not be
		// reported as having left the predecessor.
		if job.State == "complete" {
			t.Errorf("a replacement with no answer was reported complete: %+v", job.Counters)
		}
		if job.Counters.ObjectsVerified != 0 {
			t.Errorf("a replacement with no answer was counted as verified: %+v", job.Counters)
		}
		// It is not counted as an overwrite either. The counter counts
		// answered writes, which is why it is not a count of displaced
		// objects: this one landed.
		if job.Counters.Overwrites != 0 {
			t.Errorf("a replacement with no answer was counted: %+v", job.Counters)
		}
		for _, forbidden := range []string{"unchanged", "rolled back", "rollback", "left as it was", "restored"} {
			if strings.Contains(strings.ToLower(job.Error), forbidden) {
				t.Errorf("the job claims %q about the predecessor: %q", forbidden, job.Error)
			}
		}

		// What the store actually holds, read independently: the write
		// reached it, so the predecessor is gone and nothing may say
		// otherwise.
		body, err := rawGet(h.Primary, "/"+container+"/live/taken", "")
		if err != nil || string(body) != sent {
			t.Errorf("the live object is neither what was sent nor readable: %q %v", body, err)
		}
		if now := liveETag(t, h, container, "live/taken"); now == priorTag {
			t.Error("the fixture did not land the write it was supposed to lose the answer to")
		}
		// And its segments are all still there: the engine removes a
		// replaced object's segments only after a write it saw answered.
		for _, name := range []string{"subject/run/0001", "subject/run/0002"} {
			if _, err := h.Inspector.Object(t.Context(), h.Primary.BookmarkID, segments, name); err != nil {
				t.Errorf("an unresolved replacement removed a segment: %s: %v", name, err)
			}
		}

		// The stored item outcome says the effect is unresolved, which is
		// the whole of what this contract promises here.
		page, err := h.Jobs.Items(t.Context(), h.Primary.BookmarkID, id, "", 10)
		if err != nil || page.Status != "available" || len(page.Items) != 1 {
			t.Fatalf("items: %+v %v", page, err)
		}
		item := page.Items[0]
		if item.Execution == nil {
			t.Fatalf("the item has no execution outcome: %+v", item)
		}
		// Unresolved, not not_attempted: a write whose answer is unknown
		// must never be reported as one that did not happen. The engine
		// sent it, so the engine's error is where it became unknown.
		if item.Execution.Result != "failed" || item.Execution.Effect != "unresolved" || item.Execution.Stage != "engine" {
			t.Errorf("the outcome does not say it became unknown in the engine: %+v", item.Execution)
		}
		if item.Verification != nil {
			t.Errorf("an item with no answer carries a second record: %+v", item.Verification)
		}
	})

	// ADR 0081 decision 8 and its required proof: on a container carrying a
	// legacy versioning mode the confirmation names that mode and the
	// archive container, and an archived entry exists afterwards.
	t.Run("names a legacy versioning mode and its archive", func(t *testing.T) {
		h := makeHarness(t)
		archive := h.Container(h.Primary, "overwrite-archive")
		container := h.Container(h.Primary, "overwrite-versioned")
		h.rawPut(h.Primary, "/"+container, nil, map[string]string{"X-Versions-Location": archive})
		// A DELETE on a versioned container RESTORES the previous version
		// rather than removing the object, so the purge the container
		// helper registered would leave the restored object behind and
		// then fail on the container itself. Cleanups run in reverse
		// order of registration, so taking the mode off here happens
		// first.
		t.Cleanup(func() {
			h.rawPut(h.Primary, "/"+container, nil, map[string]string{"X-Remove-Versions-Location": "yes"})
		})
		h.Seed(h.Primary, container, "live/taken", []byte("the bytes already there"), nil)
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
			t.Fatalf("a confirmed replacement on a versioned container was refused: %v", err)
		}
		h.AwaitJob(id)
		job, err := h.Jobs.Get(t.Context(), h.Primary.BookmarkID, id)
		if err != nil {
			t.Fatal(err)
		}
		if job.State != "complete" {
			t.Fatalf("the replacement was not written: %s %+v", job.State, job.Counters)
		}
		asked := confirmer.questions()
		if len(asked) != 1 {
			t.Fatalf("the confirmation was asked %d times", len(asked))
		}
		// The mode as the container reports it, the archive container by
		// name, and no promise that anything can be recovered from it.
		for _, want := range []string{"versions_location", archive, "no archived version has been read back"} {
			if !strings.Contains(asked[0].Versioning, want) {
				t.Errorf("the question does not state %q: %q", want, asked[0].Versioning)
			}
		}
		if now := liveETag(t, h, container, "live/taken"); now == priorTag {
			t.Fatal("the object was not replaced")
		}
		// The store's own archive, listed raw. The mock does not implement
		// versioned writes, so this half is the owned fixture's.
		if h.MockPrimary != nil {
			t.Log("this store does not archive a replaced object; the archived entry is the owned fixture's half")
			return
		}
		listing, err := rawGet(h.Primary, "/"+archive, "")
		if err != nil {
			t.Fatalf("the archive container could not be listed: %v", err)
		}
		if !strings.Contains(string(listing), "live/taken") {
			t.Fatalf("the replaced object was not archived: %q", listing)
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
