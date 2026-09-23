package app

import (
	"errors"
	"testing"

	"github.com/csmart/aeronautes/internal/audit"
	"github.com/csmart/aeronautes/internal/jobs"
)

func TestInspectorReadsAreAccountScoped(t *testing.T) {
	h := newHarness(t)
	h.connect(t)
	ctx := t.Context()
	other, err := h.bookmarks.Save(ctx, BookmarkDTO{
		DisplayName: "Other", AuthURL: h.authURL, Username: "test:tester",
	}, "testing")
	if err != nil {
		t.Fatal(err)
	}

	t.Run("a detail states the account that answered", func(t *testing.T) {
		d, err := h.inspector.Object(ctx, h.bookmarkID(), "fixtures", "normal/hello.txt")
		if err != nil {
			t.Fatalf("object: %v", err)
		}
		if d.Scope.BookmarkID != h.bookmarkID() || d.Scope.Generation == 0 {
			t.Errorf("object scope = %+v, want the connected bookmark", d.Scope)
		}
		c, err := h.inspector.Container(ctx, h.bookmarkID(), "fixtures")
		if err != nil {
			t.Fatalf("container: %v", err)
		}
		if c.Scope.BookmarkID != h.bookmarkID() {
			t.Errorf("container scope = %+v", c.Scope)
		}
		m, err := h.inspector.SLOManifest(ctx, h.bookmarkID(), "fixtures", "external-slo")
		if err != nil {
			t.Fatalf("manifest: %v", err)
		}
		if m.Scope.BookmarkID != h.bookmarkID() || m.Scope.Generation != d.Scope.Generation {
			t.Errorf("manifest scope = %+v, want %+v", m.Scope, d.Scope)
		}
		seg, err := h.inspector.DLOSegments(ctx, h.bookmarkID(), "external-dlo-segments/wedding-video/")
		if err != nil {
			t.Fatalf("segments: %v", err)
		}
		if seg.Scope.BookmarkID != h.bookmarkID() || seg.Scope.Generation != d.Scope.Generation {
			t.Errorf("segments scope = %+v, want %+v", seg.Scope, d.Scope)
		}
	})

	t.Run("naming the other account refuses rather than answering", func(t *testing.T) {
		_, err := h.inspector.Object(ctx, other.ID, "fixtures", "normal/hello.txt")
		if !errors.Is(err, ErrAccountScopeChanged) {
			t.Fatalf("object naming the unconnected account = %v, want ErrAccountScopeChanged", err)
		}
		_, err = h.inspector.Prefix(ctx, other.ID, "fixtures", "implied/")
		if !errors.Is(err, ErrAccountScopeChanged) {
			t.Fatalf("prefix naming the unconnected account = %v", err)
		}
		_, err = h.inspector.Container(ctx, other.ID, "fixtures")
		if !errors.Is(err, ErrAccountScopeChanged) {
			t.Fatalf("container naming the unconnected account = %v", err)
		}
		_, err = h.inspector.SLOManifest(ctx, other.ID, "fixtures", "external-slo")
		if !errors.Is(err, ErrAccountScopeChanged) {
			t.Fatalf("manifest naming the unconnected account = %v", err)
		}
		_, err = h.inspector.DLOSegments(ctx, other.ID, "external-dlo-segments/wedding-video/")
		if !errors.Is(err, ErrAccountScopeChanged) {
			t.Fatalf("segments naming the unconnected account = %v", err)
		}
	})

	t.Run("an empty bookmark refuses", func(t *testing.T) {
		_, err := h.inspector.Object(ctx, "", "fixtures", "normal/hello.txt")
		if !errors.Is(err, ErrNotConnected) {
			t.Fatalf("empty bookmark = %v, want ErrNotConnected", err)
		}
	})
}

func TestFindReadsAreAccountScoped(t *testing.T) {
	h := newHarness(t)
	h.connect(t)
	ctx := t.Context()
	other, err := h.bookmarks.Save(ctx, BookmarkDTO{
		DisplayName: "Other", AuthURL: h.authURL, Username: "test:tester",
	}, "testing")
	if err != nil {
		t.Fatal(err)
	}
	service := NewFindService(h.transfer.connections)
	t.Cleanup(func() { _ = service.ServiceShutdown() })

	t.Run("start naming the other account refuses", func(t *testing.T) {
		id, err := service.StartFind(ctx, other.ID, "fixtures", "", "hello", false)
		if id != "" || !errors.Is(err, ErrAccountScopeChanged) {
			t.Fatalf("StartFind naming the unconnected account = (%q, %v)", id, err)
		}
	})

	t.Run("a poll under a different bookmark refuses", func(t *testing.T) {
		id, err := service.StartFind(ctx, h.bookmarkID(), "fixtures", "", "hello", false)
		if err != nil {
			t.Fatalf("StartFind: %v", err)
		}
		t.Cleanup(func() { _ = service.CancelFind(ctx, h.bookmarkID(), id) })
		status, err := service.FindStatus(ctx, h.bookmarkID(), id)
		if err != nil {
			t.Fatalf("status: %v", err)
		}
		if status.Scope == nil || status.Scope.BookmarkID != h.bookmarkID() {
			t.Errorf("status scope = %+v", status.Scope)
		}
		_, err = service.FindStatus(ctx, other.ID, id)
		if !errors.Is(err, ErrAccountScopeChanged) {
			t.Fatalf("poll naming another account = %v, want ErrAccountScopeChanged", err)
		}
		if err := service.CancelFind(ctx, other.ID, id); !errors.Is(err, ErrAccountScopeChanged) {
			t.Fatalf("cancel naming another account = %v", err)
		}
	})

	t.Run("a scan from an earlier generation refuses after a rekey and reconnect", func(t *testing.T) {
		id, err := service.StartFind(ctx, h.bookmarkID(), "fixtures", "", "hello", false)
		if err != nil {
			t.Fatalf("StartFind: %v", err)
		}
		bookmarks, err := h.bookmarks.List(ctx)
		if err != nil {
			t.Fatal(err)
		}
		var mine BookmarkDTO
		for _, bookmark := range bookmarks {
			if bookmark.ID == h.bookmarkID() {
				mine = bookmark
			}
		}
		if mine.ID == "" {
			t.Fatal("the connected bookmark is missing from the list")
		}
		// Re-entering the key raises the generation and retires the
		// connection; the scan keeps the session it captured, and its
		// bookmark's generation has moved on before any reconnect.
		if _, err := h.bookmarks.Save(ctx, mine, "testing"); err != nil {
			t.Fatal(err)
		}
		if _, err := service.FindStatus(ctx, h.bookmarkID(), id); !errors.Is(err, ErrAccountScopeChanged) {
			t.Fatalf("poll after the rekey with no connection = %v, want ErrAccountScopeChanged", err)
		}
		if _, err := h.browse.Connect(ctx, h.bookmarkID()); err != nil {
			t.Fatal(err)
		}
		if _, err := service.FindStatus(ctx, h.bookmarkID(), id); !errors.Is(err, ErrAccountScopeChanged) {
			t.Fatalf("poll after reconnecting = %v, want ErrAccountScopeChanged", err)
		}
		if err := service.CancelFind(ctx, h.bookmarkID(), id); !errors.Is(err, ErrAccountScopeChanged) {
			t.Fatalf("cancel after reconnecting = %v, want ErrAccountScopeChanged", err)
		}
		fresh, err := service.StartFind(ctx, h.bookmarkID(), "fixtures", "", "hello", false)
		if err != nil {
			t.Fatalf("StartFind after reconnecting: %v", err)
		}
		t.Cleanup(func() { _ = service.CancelFind(ctx, h.bookmarkID(), fresh) })
		status, err := service.FindStatus(ctx, h.bookmarkID(), fresh)
		if err != nil || status.Scope == nil || status.Scope.Generation <= 1 {
			t.Fatalf("a scan under the new generation = %+v %v, want generation above 1", status, err)
		}
	})
}

func TestAuditRecentForIsAccountScoped(t *testing.T) {
	h := newHarness(t)
	ctx := t.Context()
	if err := h.auditSvc.audit.Append(ctx, audit.Event{
		BookmarkID: "alpha", Operation: "object.metadata.update", Resource: "a/x",
		Summary: "alpha event", Result: audit.ResultApplied,
	}); err != nil {
		t.Fatal(err)
	}
	if err := h.auditSvc.audit.Append(ctx, audit.Event{
		BookmarkID: "beta", Operation: "object.metadata.update", Resource: "b/y",
		Summary: "beta event", Result: audit.ResultApplied,
	}); err != nil {
		t.Fatal(err)
	}

	t.Run("RecentFor returns only the named bookmark", func(t *testing.T) {
		events, err := h.auditSvc.RecentFor(ctx, "alpha", 10)
		if err != nil {
			t.Fatalf("RecentFor: %v", err)
		}
		if len(events) != 1 || events[0].BookmarkID != "alpha" || events[0].Resource != "a/x" {
			t.Fatalf("RecentFor alpha = %+v", events)
		}
	})

	t.Run("unscoped Recent refuses", func(t *testing.T) {
		_, err := h.auditSvc.Recent(ctx, 10)
		if !errors.Is(err, audit.ErrBookmarkRequired) {
			t.Fatalf("Recent = %v, want ErrBookmarkRequired", err)
		}
	})

	t.Run("empty bookmark refuses", func(t *testing.T) {
		_, err := h.auditSvc.RecentFor(ctx, "", 10)
		if !errors.Is(err, ErrAccountScopeChanged) {
			t.Fatalf("RecentFor empty = %v, want ErrAccountScopeChanged", err)
		}
	})
}

func TestJobGetIsAccountScoped(t *testing.T) {
	h := newHarness(t)
	ctx := t.Context()
	alphaBM, err := h.bookmarks.Save(ctx, BookmarkDTO{DisplayName: "Alpha", AuthURL: h.authURL, Username: "test:tester"}, "testing")
	if err != nil {
		t.Fatal(err)
	}
	betaBM, err := h.bookmarks.Save(ctx, BookmarkDTO{DisplayName: "Beta", AuthURL: h.authURL, Username: "test:tester"}, "testing")
	if err != nil {
		t.Fatal(err)
	}
	alpha, err := h.jobsSvc.jobs.Create(ctx, jobs.Job{Type: jobs.TypeDownload, SourceBookmarkID: alphaBM.ID})
	if err != nil {
		t.Fatal(err)
	}
	beta, err := h.jobsSvc.jobs.Create(ctx, jobs.Job{Type: jobs.TypeDownload, DestBookmarkID: betaBM.ID})
	if err != nil {
		t.Fatal(err)
	}

	t.Run("Get answers for the named bookmark", func(t *testing.T) {
		got, err := h.jobsSvc.Get(ctx, alphaBM.ID, alpha.ID)
		if err != nil || got.ID != alpha.ID {
			t.Fatalf("Get alpha = (%v, %v)", got, err)
		}
	})

	t.Run("Get of another account's job refuses", func(t *testing.T) {
		_, err := h.jobsSvc.Get(ctx, alphaBM.ID, beta.ID)
		if !errors.Is(err, ErrAccountScopeChanged) {
			t.Fatalf("Get other account = %v, want ErrAccountScopeChanged", err)
		}
	})

	t.Run("empty bookmark refuses", func(t *testing.T) {
		_, err := h.jobsSvc.Get(ctx, "", alpha.ID)
		if !errors.Is(err, ErrAccountScopeChanged) {
			t.Fatalf("Get empty = %v", err)
		}
	})

	t.Run("item pages carry the same binding", func(t *testing.T) {
		_, err := h.jobsSvc.Items(ctx, alphaBM.ID, beta.ID, "", 10)
		if !errors.Is(err, ErrAccountScopeChanged) {
			t.Fatalf("Items other account = %v", err)
		}
		_, err = h.jobsSvc.ItemTotals(ctx, betaBM.ID, alpha.ID)
		if !errors.Is(err, ErrAccountScopeChanged) {
			t.Fatalf("ItemTotals other account = %v", err)
		}
	})
}
