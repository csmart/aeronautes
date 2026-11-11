package app

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/csmart/aeronautes/internal/swiftcontrol"
)

// accountContents is every object this account holds, by container and name,
// with what identifies its bytes. It is the collateral oracle for an ADMITTED
// capability: the refusal cases can count requests and rows because nothing
// should happen at all, but a capability that runs has to be shown touching
// its own namespace and nothing else.
//
// It reads the DEEP listing through the session rather than walking the browse
// view. The browse view is the delimiter one, and it deliberately hides a real
// object whose name is exactly the slash-terminated prefix being listed
// (swiftcontrol/objects.go: "The folder's own object belongs to PrefixDetail"),
// so an oracle built from it misses every slash-named object — a review proved
// exactly that by changing one and watching this snapshot report nothing. The
// deep listing is the stream jobs consume: every real object, no prefix rows,
// and its own paging, so no continuation marker is left unfollowed.
func accountContents(t *testing.T, h *harness) map[string]string {
	t.Helper()
	session, err := h.transfer.connections.SessionFor(t.Context(), h.bookmarkID())
	if err != nil || session == nil {
		t.Fatalf("no session for the collateral oracle: %v", err)
	}
	contents := map[string]string{}
	var containers []string
	// Both iterators page themselves to their own end, so no continuation
	// marker is left unfollowed by this oracle.
	for entry, err := range session.Containers(t.Context(), swiftcontrol.ListOptions{PageSize: 1000}) {
		if err != nil {
			t.Fatalf("listing containers: %v", err)
		}
		containers = append(containers, entry.Name)
	}
	for _, container := range containers {
		for entry, err := range session.Objects(t.Context(), container,
			swiftcontrol.ObjectListOptions{PageSize: 1000, Deep: true}) {
			if err != nil {
				t.Fatalf("listing %s: %v", container, err)
			}
			object, ok := entry.(swiftcontrol.ObjectEntry)
			if !ok {
				// A deep listing yields no prefix rows; anything else here
				// would mean the oracle is reading the wrong view.
				t.Fatalf("deep listing of %s yielded %T", container, entry)
			}
			contents[container+"/"+object.Name] = strings.Trim(object.ETag, `"`)
		}
	}
	return contents
}

// outside names every difference that is not inside the one prefix this run
// was allowed to write, sorted so a failure reads the same way twice.
func outside(before, after map[string]string, namespace string) []string {
	var changed []string
	for key, etag := range after {
		if strings.HasPrefix(key, namespace) {
			continue
		}
		if was, existed := before[key]; !existed {
			changed = append(changed, "added "+key)
		} else if was != etag {
			changed = append(changed, "changed "+key)
		}
	}
	for key := range before {
		if strings.HasPrefix(key, namespace) {
			continue
		}
		if _, still := after[key]; !still {
			changed = append(changed, "removed "+key)
		}
	}
	sort.Strings(changed)
	return changed
}

// ADR 0082's second proof bullet, the half that was missing: with the gate
// FORCED and NOTHING switched on, the upload entry points do not merely stop
// refusing — they reach their routes and the work lands. This used to need
// the switch to name upload; the operator returned that capability on
// 16 October 2026, so the same assertions now make the stronger statement
// that a shipped process reaches the route with no token at all. The
// entry-point cases assert the absence of the gate's refusal, which a route
// that had been deleted would also satisfy.
//
// The collateral half is asserted against the account's own contents rather
// than a request count, because this capability is supposed to make requests:
// what it must not do is change anything outside the prefix it was given.
func TestUploadReachesItsRouteAndStaysInItsNamespace(t *testing.T) {
	h := newHarness(t)
	h.connect(t)
	h.installEngine(nil)
	forceV1Gate(t)
	restoreSwitch(t)
	if names, bad := EnableCapabilities(""); len(names) != 0 || len(bad) != 0 {
		t.Fatalf("switch = %v %v", names, bad)
	}
	h.mock.AddContainer("admitted", "standard")
	// Bystanders in the same container outside the prefix, in another
	// container, and one already live under the prefix but not selected.
	seedObject(h.mock, "admitted", "elsewhere/keep", "untouched", nil)
	seedObject(h.mock, "admitted", "target/keep", "untouched", nil)
	h.mock.AddContainer("other", "standard")
	seedObject(h.mock, "other", "keep", "untouched", nil)
	// A real object whose name ENDS in a slash. The delimiter view hides this
	// one behind the prefix row it looks like, so an oracle built from that
	// view misses it entirely; the deep listing sees it.
	seedObject(h.mock, "admitted", "elsewhere/nested/", "slash named", nil)

	dir := t.TempDir()
	var paths []string
	for i := range 2 {
		path := filepath.Join(dir, fmt.Sprintf("sent-%d", i))
		if err := os.WriteFile(path, []byte("the bytes this job sent"), 0o600); err != nil {
			t.Fatal(err)
		}
		paths = append(paths, path)
	}
	h.transfer.setFilePicker(fakePicker{files: paths})
	before := accountContents(t, h)

	// Each gated method in turn, as the webview would call them.
	picked, err := h.transfer.PickUploadFiles(t.Context(), SelectionScopeDTO{
		BookmarkID: h.bookmarkID(), Container: "admitted", Prefix: "target/"})
	if err != nil || picked == nil || len(picked.Files) != 2 {
		t.Fatalf("PickUploadFiles did not reach its route: %+v %v", picked, err)
	}
	req := UploadRequestDTO{BookmarkID: h.bookmarkID(), Container: "admitted", Prefix: "target/",
		SelectionToken: picked.Token}
	view, err := h.transfer.PreflightUpload(t.Context(), req)
	if err != nil || view == nil || view.Rejected() || len(view.Items) != 2 {
		t.Fatalf("PreflightUpload did not reach its route: %+v %v", view, err)
	}
	id, err := h.transfer.StartUpload(t.Context(), req)
	if err != nil {
		t.Fatalf("StartUpload did not reach its route: %v", err)
	}
	if job := awaitTerminal(t, h, id); job.State != "complete" || job.Counters.ObjectsCopied != 2 {
		t.Fatalf("the admitted upload did not publish: %s %+v", job.State, job.Counters)
	}

	after := accountContents(t, h)
	// The work landed where it was asked to.
	for i := range 2 {
		if _, ok := after[fmt.Sprintf("admitted/target/sent-%d", i)]; !ok {
			t.Errorf("the admitted upload did not publish sent-%d", i)
		}
	}
	// And nowhere else. The prefix it was given is the whole of its licence.
	if changed := outside(before, after, "admitted/target/"); len(changed) != 0 {
		t.Errorf("an admitted capability changed %v outside its own namespace", changed)
	}
	// The oracle itself is checked: it must see the slash-named object, and it
	// must report a change to it. Without this the whole collateral assertion
	// above could be passing because it cannot see anything at all.
	if _, seen := after["admitted/elsewhere/nested/"]; !seen {
		t.Fatal("the collateral oracle does not see a real object whose name ends in a slash")
	}
	seedObject(h.mock, "admitted", "elsewhere/nested/", "changed behind the oracle's back", nil)
	if changed := outside(before, accountContents(t, h), "admitted/target/"); len(changed) != 1 ||
		changed[0] != "changed admitted/elsewhere/nested/" {
		t.Errorf("the oracle missed a change to a slash-named object: %v", changed)
	}
}
