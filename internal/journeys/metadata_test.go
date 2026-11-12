//go:build realcloud

package journeys

import (
	"bytes"
	"fmt"
	"strings"
	"testing"

	"github.com/csmart/aeronautes/internal/app"
)

// TestMetadataJourney: on each cloud, edit an object's metadata, a
// container's metadata, set a world-readable read ACL with the
// acknowledgement and clear it again, and change the run's own account key,
// each through the editor's path and each read back through the independent
// session (journey 4). Every edit starts from the details as just read, as
// the editors do, so nothing else is changed.
func TestMetadataJourney(t *testing.T) {
	w := newWorld(t)
	for round := 1; round <= repeats(t); round++ {
		for _, label := range []string{"ceph", "swift"} {
			t.Run(fmt.Sprintf("%s/%d", label, round), func(t *testing.T) {
				w.t = t
				metadataJourney(w, w.accounts[label], round)
			})
		}
	}
}

func metadataJourney(w *world, a *account, round int) {
	t := w.t
	if _, err := w.browse.Connect(w.ctx, a.bookmarkID); err != nil {
		t.Fatalf("connect: %s", failure(err))
	}
	scope := w.scope(a)
	c := w.container(a, fmt.Sprintf("meta%d", round))
	if _, err := a.check.PutObject(w.ctx, c, "doc.txt", bytes.NewReader([]byte("document")), "text/plain", nil); err != nil {
		t.Fatalf("seed: %s", failure(err))
	}

	// Object metadata.
	object, err := w.inspector.Object(w.ctx, a.bookmarkID, c, "doc.txt")
	if err != nil {
		t.Fatalf("inspect the object: %s", failure(err))
	}
	result, err := w.mutate.UpdateObjectMetadata(w.ctx, scope, c, "doc.txt", object.EditableFingerprint, app.ObjectMetadataEditDTO{
		Meta: with(object.Meta, "Journey", "object"), ContentType: object.ContentType, DeleteAt: object.DeleteAt,
	})
	if err != nil || result.Outcome != "applied" {
		t.Fatalf("edit the object: %s outcome %v", failure(err), result.Outcome)
	}
	if problem := w.settled(func() string {
		d, err := a.check.ObjectDetail(w.ctx, c, "doc.txt")
		if err != nil {
			return failure(err)
		}
		if d.Meta["Journey"] != "object" {
			return fmt.Sprintf("object metadata reads %v", d.Meta)
		}
		return ""
	}); problem != "" {
		t.Fatal(problem)
	}

	// Container metadata and a world-readable read ACL, then the ACL cleared.
	for _, step := range []struct{ name, acl string }{{"make public", ".r:*,.rlistings"}, {"make private", ""}} {
		container, err := w.inspector.Container(w.ctx, a.bookmarkID, c)
		if err != nil {
			t.Fatalf("inspect the container: %s", failure(err))
		}
		result, err := w.mutate.UpdateContainer(w.ctx, scope, c, container.EditableFingerprint, app.ContainerEditDTO{
			Meta: with(container.Meta, "Journey", "container"), ReadACL: step.acl, WriteACL: container.WriteACL,
			VersioningMode: container.VersioningMode, VersioningArchive: container.VersioningArchive,
			QuotaBytes: container.QuotaBytes, QuotaCount: container.QuotaCount, AckPublic: step.acl != "",
		})
		if err != nil || result.Outcome != "applied" {
			t.Fatalf("%s: %s outcome %v", step.name, failure(err), result.Outcome)
		}
		if problem := w.settled(func() string {
			d, err := a.check.ContainerDetail(w.ctx, c)
			if err != nil {
				return failure(err)
			}
			if d.Meta["Journey"] != "container" || strings.Contains(d.ReadACL, ".r:*") != (step.acl != "") {
				return fmt.Sprintf("%s: the container reads ACL %q and metadata %v", step.name, d.ReadACL, d.Meta)
			}
			return ""
		}); problem != "" {
			t.Fatal(problem)
		}
	}

	// The run's own account key, changed through the account editor, which
	// carries every other key and the quota as just read.
	key, err := a.owned.SetAccountKey(w.ctx, "set")
	if err != nil {
		t.Fatalf("set the run's account key: %s", failure(err))
	}
	accountView, err := w.browse.Account(w.ctx, a.bookmarkID)
	if err != nil {
		t.Fatalf("read the account: %s", failure(err))
	}
	result, err = w.mutate.UpdateAccountMetadata(w.ctx, scope, accountView.EditableFingerprint, app.AccountEditDTO{
		Meta: with(accountView.Meta, key, fmt.Sprintf("edited in round %d", round)), QuotaBytes: accountView.QuotaBytes,
	})
	if err != nil || result.Outcome != "applied" {
		t.Fatalf("edit the account: %s outcome %v", failure(err), result.Outcome)
	}
	if problem := w.settled(func() string {
		p, err := a.check.AccountPortrait(w.ctx)
		if err != nil {
			return failure(err)
		}
		if p.Meta[key] != fmt.Sprintf("edited in round %d", round) || p.QuotaBytes != accountView.QuotaBytes || len(p.Meta) != len(accountView.Meta) {
			return "the account edit did not read back as made, or changed another key or the quota"
		}
		return ""
	}); problem != "" {
		t.Fatal(problem)
	}
}

// with is a copy of meta with one key set.
func with(meta map[string]string, key, value string) map[string]string {
	out := make(map[string]string, len(meta)+1)
	for k, v := range meta {
		out[k] = v
	}
	out[key] = value
	return out
}
