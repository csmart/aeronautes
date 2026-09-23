package systemtest

import (
	"context"
	"crypto/md5"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/csmart/aeronautes/internal/app"
)

type namedReadReply struct {
	scope app.ListingScopeDTO
	owner string
	err   error
}

type namedRead struct {
	name string
	read func(context.Context, *Account) namedReadReply
}

func namedReads(t *testing.T, h *Harness, mock bool) []namedRead {
	t.Helper()
	shared := h.Container(h.Primary, "read-shared")
	h.rawPut(h.Secondary, "/"+shared, nil, nil)
	t.Cleanup(func() { h.purge(h.Secondary, shared) })
	primaryOnly := h.Container(h.Primary, "primary-only")
	secondaryOnly := h.Container(h.Secondary, "secondary-only")
	for _, fixture := range []struct {
		account *Account
		owner   string
	}{{h.Primary, "primary"}, {h.Secondary, "secondary"}} {
		h.rawPut(fixture.account, "/"+shared, nil, map[string]string{"X-Container-Meta-Owner": fixture.owner})
		headers := map[string]string{"X-Object-Meta-Owner": fixture.owner}
		h.Seed(fixture.account, shared, "common", []byte(fixture.owner), headers)
		h.Seed(fixture.account, shared, "folder/", []byte(fixture.owner), headers)
		h.Seed(fixture.account, shared, "only-"+fixture.owner, []byte(fixture.owner), nil)
		segment := "parts/" + fixture.owner
		body := []byte(fixture.owner)
		h.Seed(fixture.account, shared, segment, body, nil)
		if mock {
			// The mock does not implement multipart-manifest=put. Seed its
			// raw GET representation; the real Swift control uses SLO PUT.
			manifest := fmt.Sprintf(`[{"name":"/%s/%s","hash":"%x","bytes":%d}]`, shared, segment, md5.Sum(body), len(body))
			h.Seed(fixture.account, shared, "manifest", []byte(manifest), nil)
		} else {
			manifest := fmt.Sprintf(`[{"path":"/%s/%s","etag":"%x","size_bytes":%d}]`, shared, segment, md5.Sum(body), len(body))
			h.SeedSLO(fixture.account, shared, "manifest", manifest)
		}
	}
	ownerFromNames := func(names []string, prefix string) string {
		if reflect.DeepEqual(names, []string{prefix + "primary"}) {
			return "primary"
		}
		if reflect.DeepEqual(names, []string{prefix + "secondary"}) {
			return "secondary"
		}
		return fmt.Sprintf("unexpected names %q", names)
	}
	return []namedRead{
		{"containers", func(ctx context.Context, account *Account) namedReadReply {
			page, err := h.Browse.Containers(ctx, account.BookmarkID, "", 5000)
			owner := "neither owned account marker"
			for _, row := range page.Rows {
				if row.Name == primaryOnly {
					owner = "primary"
				}
				if row.Name == secondaryOnly {
					owner = "secondary"
				}
			}
			return namedReadReply{page.Scope, owner, err}
		}},
		{"objects", func(ctx context.Context, account *Account) namedReadReply {
			page, err := h.Browse.Objects(ctx, account.BookmarkID, shared, "only-", "", 10)
			var names []string
			for _, row := range page.Rows {
				names = append(names, row.Name)
			}
			return namedReadReply{page.Scope, ownerFromNames(names, "only-"), err}
		}},
		{"object", func(ctx context.Context, account *Account) namedReadReply {
			detail, err := h.Inspector.Object(ctx, account.BookmarkID, shared, "common")
			return namedReadReply{detail.Scope, detail.Meta["Owner"], err}
		}},
		{"prefix", func(ctx context.Context, account *Account) namedReadReply {
			detail, err := h.Inspector.Prefix(ctx, account.BookmarkID, shared, "folder/")
			owner := "missing self object"
			if detail.SelfObject != nil {
				owner = detail.SelfObject.Meta["Owner"]
			}
			return namedReadReply{detail.Scope, owner, err}
		}},
		{"container", func(ctx context.Context, account *Account) namedReadReply {
			detail, err := h.Inspector.Container(ctx, account.BookmarkID, shared)
			return namedReadReply{detail.Scope, detail.Meta["Owner"], err}
		}},
		{"slo-manifest", func(ctx context.Context, account *Account) namedReadReply {
			manifest, err := h.Inspector.SLOManifest(ctx, account.BookmarkID, shared, "manifest")
			var names []string
			for _, row := range manifest.Entries {
				names = append(names, row.Object)
			}
			return namedReadReply{manifest.Scope, ownerFromNames(names, "parts/"), err}
		}},
		{"dlo-segments", func(ctx context.Context, account *Account) namedReadReply {
			preview, err := h.Inspector.DLOSegments(ctx, account.BookmarkID, shared+"/parts/")
			var names []string
			for _, row := range preview.Segments {
				names = append(names, row.Name)
			}
			return namedReadReply{preview.Scope, ownerFromNames(names, "parts/"), err}
		}},
		{"find-start", func(ctx context.Context, account *Account) namedReadReply {
			id, err := h.Find.StartFind(ctx, account.BookmarkID, shared, "only-", "only-", false)
			if err != nil {
				return namedReadReply{err: err}
			}
			defer h.Find.CancelFind(context.Background(), account.BookmarkID, id)
			deadline := time.NewTimer(10 * time.Second)
			defer deadline.Stop()
			tick := time.NewTicker(time.Millisecond)
			defer tick.Stop()
			for {
				status, err := h.Find.FindStatus(ctx, account.BookmarkID, id)
				if err != nil {
					return namedReadReply{err: err}
				}
				if status.Done {
					if status.Error != "" || status.Scope == nil {
						return namedReadReply{err: fmt.Errorf("Find result: %+v", status)}
					}
					var names []string
					for _, row := range status.Matches {
						names = append(names, row.Name)
					}
					return namedReadReply{*status.Scope, ownerFromNames(names, "only-"), nil}
				}
				select {
				case <-tick.C:
				case <-deadline.C:
					return namedReadReply{err: fmt.Errorf("Find did not finish")}
				case <-ctx.Done():
					return namedReadReply{err: ctx.Err()}
				}
			}
		}},
	}
}

func namedReadConnect(t *testing.T, h *Harness, account *Account) {
	t.Helper()
	portrait, err := h.Browse.Connect(t.Context(), account.BookmarkID)
	if err != nil {
		t.Fatal(err)
	}
	want := account.storageURL[strings.LastIndex(account.storageURL, "/")+1:]
	if portrait.Name != want {
		t.Fatalf("public Connect portrait = %q, want %q", portrait.Name, want)
	}
	// This public bound read proves which bookmark is active after Connect.
	if _, err := h.Browse.Containers(t.Context(), account.BookmarkID, "", 1); err != nil {
		t.Fatalf("public Connect did not activate its requested bookmark: %v", err)
	}
}

// namedReadRekey re-enters the account's key on its bookmark, which
// raises the bookmark generation without changing where it points.
func namedReadRekey(t *testing.T, h *Harness, account *Account) {
	t.Helper()
	bookmarks, err := h.Bookmarks.List(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for _, bookmark := range bookmarks {
		if bookmark.ID != account.BookmarkID {
			continue
		}
		if _, err := h.Bookmarks.Save(t.Context(), bookmark, account.key); err != nil {
			t.Fatal(err)
		}
		return
	}
	t.Fatal("account bookmark missing")
}

func namedReadControls(t *testing.T, h *Harness, mock bool) {
	reads := namedReads(t, h, mock)
	beforeA, beforeB := h.SnapshotNamespace(h.Primary), h.SnapshotNamespace(h.Secondary)
	for _, fixture := range []struct {
		account *Account
		owner   string
	}{{h.Primary, "primary"}, {h.Secondary, "secondary"}} {
		namedReadConnect(t, h, fixture.account)
		for _, read := range reads {
			t.Run(fixture.owner+"/"+read.name, func(t *testing.T) {
				reply := read.read(t.Context(), fixture.account)
				if reply.err != nil || reply.owner != fixture.owner || reply.scope.BookmarkID != fixture.account.BookmarkID || reply.scope.Generation != 1 {
					t.Fatalf("same-account reply = %+v, want %s generation 1", reply, fixture.owner)
				}
			})
		}
	}
	h.AssertNoCollateral(beforeA, h.SnapshotNamespace(h.Primary))
	h.AssertNoCollateral(beforeB, h.SnapshotNamespace(h.Secondary))
}

func namedReadScheduled(t *testing.T, h *Harness, mock bool) {
	reads := namedReads(t, h, mock)
	beforeA, beforeB := h.SnapshotNamespace(h.Primary), h.SnapshotNamespace(h.Secondary)
	for _, schedule := range []string{"no-switch", "reconnect-same", "switch-account", "edit-bookmark"} {
		for _, read := range reads {
			t.Run(schedule+"/"+read.name, func(t *testing.T) {
				namedReadConnect(t, h, h.Primary)
				entered := make(chan struct{})
				release := make(chan struct{})
				var enteredOnce, releaseOnce sync.Once
				unblock := func() { releaseOnce.Do(func() { close(release) }) }
				t.Cleanup(unblock)
				ctx := context.WithValue(t.Context(), namedReadCheckpointKey, func() {
					enteredOnce.Do(func() { close(entered) })
					select {
					case <-release:
					case <-t.Context().Done():
					}
				})
				finished := make(chan namedReadReply, 1)
				go func() { finished <- read.read(ctx, h.Primary) }()
				select {
				case <-entered:
				case reply := <-finished:
					t.Fatalf("observation barrier not reached: %+v", reply)
				case <-time.After(10 * time.Second):
					t.Fatal("observation barrier timed out")
				}
				switch schedule {
				case "reconnect-same":
					namedReadConnect(t, h, h.Primary)
				case "switch-account":
					namedReadConnect(t, h, h.Secondary)
				case "edit-bookmark":
					namedReadRekey(t, h, h.Primary)
				}
				unblock()
				select {
				case reply := <-finished:
					if schedule == "edit-bookmark" {
						// The captured session survives the pause, but the
						// edited bookmark's generation admission refuses its
						// next remote request (ADR 0066: edits still reach
						// request-time admission).
						if reply.err == nil {
							t.Fatalf("read issued a remote request through an edited bookmark's stale session: %+v", reply)
						}
						t.Logf("schedule=edit-bookmark refused: %v", reply.err)
						return
					}
					if reply.err != nil {
						t.Fatalf("read failed instead of returning its captured account: %+v", reply)
					}
					t.Logf("schedule=%s requested=primary payload=%s scope=%+v", schedule, reply.owner, reply.scope)
					if reply.scope.BookmarkID != h.Primary.BookmarkID || reply.scope.Generation != 1 {
						t.Fatalf("reply lost requested scope: %+v", reply)
					}
					if reply.owner != "primary" {
						t.Fatalf("misattributed %s: payload from %q labelled as primary bookmark generation 1", read.name, reply.owner)
					}
				case <-time.After(10 * time.Second):
					t.Fatal("read did not finish after observation barrier release")
				}
			})
		}
	}
	h.AssertNoCollateral(beforeA, h.SnapshotNamespace(h.Primary))
	h.AssertNoCollateral(beforeB, h.SnapshotNamespace(h.Secondary))
}

func TestNamedReadControlsMock(t *testing.T) {
	namedReadControls(t, findMockHarness(t), true)
}

func TestNamedReadControlsRealSwift(t *testing.T) {
	namedReadControls(t, New(t), false)
}

// The scheduled cases need the observation call that only the instrumented
// child build carries; see named_read_overlay_test.go.
func TestNamedReadSessionSnapshotMock(t *testing.T) {
	if namedReadRunsInChild(t, "TestNamedReadSessionSnapshotMock") {
		return
	}
	namedReadScheduled(t, findMockHarness(t), true)
}

func TestNamedReadSessionSnapshotRealSwift(t *testing.T) {
	if namedReadRunsInChild(t, "TestNamedReadSessionSnapshotRealSwift") {
		return
	}
	namedReadScheduled(t, New(t), false)
}
