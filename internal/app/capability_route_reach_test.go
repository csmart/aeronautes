package app

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// accountContents is every object this account holds, by container and name,
// with what identifies its bytes. It is the collateral oracle for an ADMITTED
// capability: the refusal cases can count requests and rows because nothing
// should happen at all, but a capability that runs has to be shown touching
// its own namespace and nothing else.
func accountContents(t *testing.T, h *harness) map[string]string {
	t.Helper()
	contents := map[string]string{}
	containers, err := h.browse.Containers(t.Context(), h.bookmarkID(), "", 100)
	if err != nil {
		t.Fatal(err)
	}
	for _, container := range containers.Rows {
		// The browse listing is the delimiter view, so a prefix arrives as
		// one row standing for everything under it. Walking into those rows
		// is what makes this a whole-account oracle rather than a top-level
		// one, and an object hidden under a prefix is exactly what a
		// collateral check must not miss.
		prefixes := []string{""}
		for len(prefixes) > 0 {
			prefix := prefixes[0]
			prefixes = prefixes[1:]
			page, err := h.browse.Objects(t.Context(), h.bookmarkID(), container.Name, prefix, "", 1000)
			if err != nil {
				t.Fatalf("listing %s/%s: %v", container.Name, prefix, err)
			}
			for _, row := range page.Rows {
				if row.Kind == "object" {
					contents[container.Name+"/"+row.Name] = strings.Trim(row.ETag, `"`)
					continue
				}
				prefixes = append(prefixes, row.Name)
			}
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
// FORCED and the switch naming upload, the upload entry points do not merely
// stop refusing — they reach their routes and the work lands. The existing
// entry-point case asserts the absence of the gate's refusal, which a route
// that had been deleted would also satisfy.
//
// The collateral half is asserted against the account's own contents rather
// than a request count, because this capability is supposed to make requests:
// what it must not do is change anything outside the prefix it was given.
func TestSwitchedOnUploadReachesItsRouteAndStaysInItsNamespace(t *testing.T) {
	h := newHarness(t)
	h.connect(t)
	h.installEngine(nil)
	forceV1Gate(t)
	restoreSwitch(t)
	if names, bad := EnableCapabilities("upload"); strings.Join(names, "|") != "Upload" || len(bad) != 0 {
		t.Fatalf("switch = %v %v", names, bad)
	}
	h.mock.AddContainer("admitted", "standard")
	// Bystanders in the same container outside the prefix, in another
	// container, and one already live under the prefix but not selected.
	seedObject(h.mock, "admitted", "elsewhere/keep", "untouched", nil)
	seedObject(h.mock, "admitted", "target/keep", "untouched", nil)
	h.mock.AddContainer("other", "standard")
	seedObject(h.mock, "other", "keep", "untouched", nil)

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
}
