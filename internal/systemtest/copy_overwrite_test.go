package systemtest

import (
	"crypto/sha256"
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/csmart/aeronautes/internal/app"
)

// A copy that replaces what the destination holds, through the public
// service with the pinned engine, on both fixtures (ADR 0122 decisions 2 and
// 4). The engine writes over the name; nothing is compared with what the
// operator was shown. A replaced large object's old segments follow the
// upload's rule (overwrite_test.go holds the engine's side of it, such as the
// segments it may not reach): here it is the copy's own start that decides,
// from one read of the destination container, and says which way it went.

func TestCopyOverwriteMock(t *testing.T)      { copyOverwriteCases(t, findMockHarness) }
func TestCopyOverwriteRealSwift(t *testing.T) { copyOverwriteCases(t, New) }

func copyOverwriteCases(t *testing.T, makeHarness func(*testing.T) *Harness) {
	t.Helper()
	// The acknowledgement is what lets a name be replaced, and it is asked
	// about the names that will be replaced and no others. Another writer
	// changes the name while the operator is answering, after the preview and
	// before the request: the copy writes over what that writer left and
	// reports success, because the last write to a name wins.
	t.Run("replaces a changed object, also one another writer changed during the dialog", func(t *testing.T) {
		h := makeHarness(t)
		src, dst := h.Container(h.Primary, "copy-overwrite-src"), h.Container(h.Secondary, "copy-overwrite-dst")
		const sent, others = "the bytes this copy sent", "the bytes another writer sent"
		h.Seed(h.Primary, src, "from/changed", []byte(sent), nil)
		h.Seed(h.Primary, src, "from/same", []byte("on both sides"), nil)
		h.Seed(h.Primary, src, "from/new", []byte("only at the source"), nil)
		h.Seed(h.Secondary, dst, "to/changed", []byte("the bytes already there"), nil)
		h.Seed(h.Secondary, dst, "to/same", []byte("on both sides"), nil)
		h.Seed(h.Secondary, dst, "to/kept", []byte("only at the destination"), nil)
		srcBefore, dstBefore := h.SnapshotNamespace(h.Primary), h.SnapshotNamespace(h.Secondary)

		confirmer := &serviceConfirmer{yes: true, before: func() {
			h.rawPut(h.Secondary, "/"+dst+"/to/changed", []byte(others), nil)
		}}
		app.WireOverwriteConfirmer(h.Transfer, confirmer)
		id, err := h.Transfer.StartCopy(t.Context(), app.CopyRequestDTO{
			SourceBookmarkID: h.Primary.BookmarkID, SourceContainer: src, SourcePrefix: "from/",
			DestBookmarkID: h.Secondary.BookmarkID, DestContainer: dst, DestPrefix: "to/", AcknowledgeOverwrite: true})
		if err != nil {
			t.Fatalf("a confirmed replacement was refused at the start: %v", err)
		}
		job := h.AwaitJob(id)
		if job.State != "complete" || job.Counters.ObjectsCopied != 2 || job.Counters.Overwrites != 1 ||
			job.Counters.OverwritesRefused != 0 || job.Counters.ObjectsFailed != 0 {
			t.Fatalf("the replacement = %s %+v (%s); want complete with two copied, one of them a replacement", job.State, job.Counters, job.Error)
		}
		// One question, naming both accounts and the one name that differs.
		asked := confirmer.questions()
		if len(asked) != 1 || asked[0].Container != dst || asked[0].Prefix != "to/" || !reflect.DeepEqual(asked[0].Names, []string{"changed"}) ||
			asked[0].Account != h.accountName(h.Secondary.BookmarkID) || asked[0].SourceAccount != h.accountName(h.Primary.BookmarkID) {
			t.Fatalf("the confirmation did not name the resolved plan: %+v", asked)
		}
		dstAfter := h.SnapshotNamespace(h.Secondary)
		if now := dstAfter.Containers[dst].Objects["to/changed"]; now.Digest != fmt.Sprintf("%x", sha256.Sum256([]byte(sent))) {
			t.Fatalf("the name holds %+v, want this copy's bytes and not the other writer's", now)
		}
		if now, want := dstAfter.Containers[dst].Objects["to/new"], srcBefore.Containers[src].Objects["from/new"]; now.Digest != want.Digest {
			t.Fatalf("the new name holds %+v, want the source's %+v", now, want)
		}
		h.AssertNoCollateral(srcBefore, h.SnapshotNamespace(h.Primary))
		h.AssertNoCollateral(dstBefore, dstAfter, dst+"/to/changed", dst+"/to/new")
	})

	// A dynamic large object at the destination is replaced by the source's
	// object. Its two segments lie under the destination folder in the
	// segments container, where the engine may delete, and an outside
	// manifest over the same segments shows the consequence either way.
	for _, mode := range []struct {
		name               string
		reclaims, versions bool
		// said is in the question, and removed whether the segments go.
		said    string
		removed bool
	}{
		{name: "removes a replaced large object's segments under the destination folder", reclaims: true, removed: true,
			said: "the transfer engine REMOVES its old segments under to/ in "},
		{name: "keeps a replaced large object's segments when the option is off",
			said: "are KEPT, because reclaiming is turned off"},
		{name: "keeps a replaced large object's segments where the container keeps versions", reclaims: true, versions: true,
			said: "are KEPT, because the container keeps versions"},
	} {
		t.Run(mode.name, func(t *testing.T) {
			h := makeHarness(t)
			src := h.Container(h.Primary, "copy-overwrite-large-src")
			var dst string
			if mode.versions {
				archive := h.Container(h.Secondary, "copy-overwrite-archive")
				dst = h.Container(h.Secondary, "copy-overwrite-versioned")
				h.rawPut(h.Secondary, "/"+dst, nil, map[string]string{"X-Versions-Location": archive})
				// A DELETE in a container with a versions location restores
				// the previous version, so the mode comes off before the purge
				// the container helper registered. Cleanups run in reverse
				// order of registration.
				t.Cleanup(func() {
					h.rawPut(h.Secondary, "/"+dst, nil, map[string]string{"X-Remove-Versions-Location": "yes"})
				})
			} else {
				dst = h.Container(h.Secondary, "copy-overwrite-large-dst")
			}
			segments := dst + "_segments"
			h.rawPut(h.Secondary, "/"+segments, nil, nil)
			t.Cleanup(func() { h.purge(h.Secondary, segments) })
			h.Seed(h.Secondary, segments, "to/taken/run/0001", []byte("01234567"), nil)
			h.Seed(h.Secondary, segments, "to/taken/run/0002", []byte("89abcdef"), nil)
			h.Seed(h.Secondary, dst, "to/taken", nil, map[string]string{"X-Object-Manifest": segments + "/to/taken/run/"})
			h.Seed(h.Secondary, dst, "outside/alias", nil, map[string]string{"X-Object-Manifest": segments + "/to/taken/run/"})
			const sent = "the bytes this copy sent"
			h.Seed(h.Primary, src, "from/taken", []byte(sent), nil)
			setReclamation(t, h, mode.reclaims)

			confirmer := &serviceConfirmer{yes: true}
			app.WireOverwriteConfirmer(h.Transfer, confirmer)
			id, err := h.Transfer.StartCopy(t.Context(), app.CopyRequestDTO{
				SourceBookmarkID: h.Primary.BookmarkID, SourceContainer: src, SourcePrefix: "from/",
				DestBookmarkID: h.Secondary.BookmarkID, DestContainer: dst, DestPrefix: "to/", AcknowledgeOverwrite: true})
			if err != nil {
				t.Fatalf("a confirmed replacement of a large object was refused: %v", err)
			}
			if job := h.AwaitJob(id); job.State != "complete" || job.Counters.Overwrites != 1 || job.Counters.ObjectsCopied != 1 {
				t.Fatalf("the large object was not replaced: %s %+v (%s)", job.State, job.Counters, job.Error)
			}
			// The operator was told which way it would go before answering.
			if asked := confirmer.questions(); len(asked) != 1 || !strings.Contains(asked[0].Fragments, mode.said) ||
				(mode.removed && !strings.Contains(asked[0].Fragments, segments)) {
				t.Fatalf("the question did not say %q: %+v", mode.said, asked)
			}
			// What the store holds now, by identity and not by listing: a
			// container listing lags a delete either way (DESIGN §15.3).
			for _, name := range []string{"to/taken/run/0001", "to/taken/run/0002"} {
				status, _, err := rawRequest(h.Secondary, http.MethodHead, "/"+segments+"/"+name, "", nil)
				want := http.StatusOK
				if mode.removed {
					want = http.StatusNotFound
				}
				if err != nil || status != want {
					t.Errorf("segment %s answers %d (%v), want %d: the question said otherwise", name, status, err, want)
				}
			}
			alias, err := rawGet(h.Secondary, "/"+dst+"/outside/alias", "")
			if assembled := err == nil && string(alias) == "0123456789abcdef"; assembled == mode.removed {
				t.Errorf("the outside manifest reads %q (%v), which is not what the question said would happen", alias, err)
			}
			// The replacement itself is readable either way.
			if body, err := rawGet(h.Secondary, "/"+dst+"/to/taken", ""); err != nil || string(body) != sent {
				t.Errorf("the replacement is not readable: %q %v", body, err)
			}
		})
	}
}
