//go:build realcloud

package journeys

import (
	"bytes"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/csmart/aeronautes/internal/app"
	"github.com/csmart/aeronautes/internal/swiftcontrol"
)

// TestDeleteJourney: on each cloud, delete one object, a selection, a prefix,
// the container's contents and then the container itself, the way the
// working list does, and check after each step that exactly what was meant
// is gone and everything else is still there (journey 5).
func TestDeleteJourney(t *testing.T) {
	w := newWorld(t)
	for round := 1; round <= repeats(t); round++ {
		for _, label := range []string{"ceph", "swift"} {
			t.Run(fmt.Sprintf("%s/%d", label, round), func(t *testing.T) {
				w.t = t
				deleteJourney(w, w.accounts[label], round)
			})
		}
	}
}

func deleteJourney(w *world, a *account, round int) {
	t := w.t
	if _, err := w.browse.Connect(w.ctx, a.bookmarkID); err != nil {
		t.Fatalf("connect: %s", failure(err))
	}
	c := w.container(a, fmt.Sprintf("del%d", round))
	names := []string{"a.txt", "b.txt", "c.txt", "keep.txt", "scope/x.txt", "scope/y.txt"}
	for _, name := range names {
		if _, err := a.check.PutObject(w.ctx, c, name, bytes.NewReader([]byte("body of "+name)), "text/plain", nil); err != nil {
			t.Fatalf("seed: %s", failure(err))
		}
	}
	scope := w.scope(a)

	// One object, as the inspector's Delete does: the detail is read to be
	// shown, and the name is deleted.
	if _, err := w.inspector.Object(w.ctx, a.bookmarkID, c, "a.txt"); err != nil {
		t.Fatalf("inspect: %s", failure(err))
	}
	result, err := w.mutate.DeleteObject(w.ctx, scope, c, "a.txt", "delete a.txt")
	if err != nil || result.Outcome != "applied" {
		t.Fatalf("delete one object: %s outcome %v", failure(err), result.Outcome)
	}
	w.remaining(a, c, "b.txt", "c.txt", "keep.txt", "scope/x.txt", "scope/y.txt")

	// A selection of two, as the working list's Delete does.
	rows := w.rows(a, c, "", "b.txt", "c.txt")
	id, err := w.delete.StartSelectedDelete(w.ctx, app.SelectedDeleteRequestDTO{Scope: scope, Container: c, Members: rows, Confirm: "delete " + strconv.Itoa(len(rows))})
	if err != nil {
		t.Fatalf("delete the selection: %s", failure(err))
	}
	w.ended("selection delete", w.await(a, id), "complete")
	w.remaining(a, c, "keep.txt", "scope/x.txt", "scope/y.txt")

	// A prefix.
	id, err = w.delete.StartDelete(w.ctx, app.DeleteRequestDTO{BookmarkID: a.bookmarkID, Container: c, Prefix: "scope/", Mode: app.ModePrefix, Confirm: "delete " + c + "/scope/"})
	if err != nil {
		t.Fatalf("delete the prefix: %s", failure(err))
	}
	w.ended("prefix delete", w.await(a, id), "complete")
	w.remaining(a, c, "keep.txt")

	// The container's contents, then the container.
	id, err = w.delete.StartDelete(w.ctx, app.DeleteRequestDTO{BookmarkID: a.bookmarkID, Container: c, Mode: app.ModeEmpty, Confirm: "empty " + c})
	if err != nil {
		t.Fatalf("empty the container: %s", failure(err))
	}
	w.ended("empty", w.await(a, id), "complete")
	w.remaining(a, c)
	id, err = w.delete.StartDelete(w.ctx, app.DeleteRequestDTO{BookmarkID: a.bookmarkID, Container: c, Mode: app.ModeDeleteContainer, Confirm: "delete " + c})
	if err != nil {
		t.Fatalf("delete the container: %s", failure(err))
	}
	w.ended("delete container", w.await(a, id), "complete")
	// The store accepted the container's DELETE, and its replicas can go on
	// answering for a moment. The application takes the store's answer and
	// does not wait that out, so this check does.
	if problem := w.settled(func() string {
		if _, err := a.check.ContainerDetail(w.ctx, c); !swiftcontrol.IsNotFound(err) {
			return "the deleted container still answers: " + failure(err)
		}
		return ""
	}); problem != "" {
		t.Fatal(problem)
	}
}

// scope is the listing scope the active connection reads under, as the
// window passes it with a delete.
func (w *world) scope(a *account) app.ListingScopeDTO {
	w.t.Helper()
	capabilities, err := w.browse.Capabilities(w.ctx, a.bookmarkID)
	if err != nil {
		w.t.Fatalf("capabilities: %s", failure(err))
	}
	return capabilities.Scope
}

// rows lists the named objects under prefix as the working list shows them.
func (w *world) rows(a *account, container, prefix string, names ...string) []app.ListingRowDTO {
	w.t.Helper()
	page, err := w.browse.Objects(w.ctx, a.bookmarkID, container, prefix, "", 1000)
	if err != nil {
		w.t.Fatalf("list: %s", failure(err))
	}
	var out []app.ListingRowDTO
	for _, name := range names {
		for _, row := range page.Rows {
			if row.Kind == "object" && row.Name == name {
				out = append(out, row)
			}
		}
	}
	if len(out) != len(names) {
		w.t.Fatalf("listed %d of the %d objects to select", len(out), len(names))
	}
	return out
}

// remaining checks, through the independent session, that the container
// holds exactly the named objects, once its listing has settled.
func (w *world) remaining(a *account, container string, want ...string) {
	w.t.Helper()
	sort.Strings(want)
	problem := w.settled(func() string {
		var got []string
		for row, err := range a.check.Objects(w.ctx, container, swiftcontrol.ObjectListOptions{Deep: true}) {
			if err != nil {
				return "list: " + failure(err)
			}
			if object, ok := row.(swiftcontrol.ObjectEntry); ok {
				got = append(got, object.Name)
			}
		}
		sort.Strings(got)
		if strings.Join(got, ",") != strings.Join(want, ",") {
			return fmt.Sprintf("the container holds %v, want %v", got, want)
		}
		return ""
	})
	if problem != "" {
		w.t.Fatal(problem)
	}
}
