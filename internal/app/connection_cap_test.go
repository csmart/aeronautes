package app

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/csmart/aeronautes/internal/swiftcontrol"
)

// ADR 0074 decision 4: a session opens with the operator's per-host
// connection cap when the settings are wired, and with the session
// package's default when they are not.
func TestSessionsOpenWithTheOperatorConnectionCap(t *testing.T) {
	h := newHarness(t)
	ctx := t.Context()
	settings := NewSettingsService(settingsFile(t), h.transfer.runner)
	if _, err := settings.Set(ctx, AppSettingsDTO{MaxActiveJobs: 1, DefaultProfile: "auto", MaxConnsPerHost: 3}); err != nil {
		t.Fatal(err)
	}
	WireConnectionCap(h.transfer.connections, settings)
	h.connect(t)
	session, err := h.transfer.connections.SessionFor(ctx, h.transfer.connections.ActiveBookmarkID())
	if err != nil {
		t.Fatal(err)
	}
	if session.ConnectionCap() != 3 {
		t.Fatalf("wired session cap = %d, want 3", session.ConnectionCap())
	}

	unwired := newHarness(t)
	unwired.connect(t)
	session, err = unwired.transfer.connections.SessionFor(ctx, unwired.transfer.connections.ActiveBookmarkID())
	if err != nil {
		t.Fatal(err)
	}
	if session.ConnectionCap() != swiftcontrol.DefaultMaxConnsPerHost {
		t.Fatalf("unwired session cap = %d, want %d", session.ConnectionCap(), swiftcontrol.DefaultMaxConnsPerHost)
	}
}

// ADR 0074 decision 5: while a download child runs and Find is at its
// worker limit, a listing page and a settings read still answer within
// five seconds against the mock.
func TestControlPlaneAnswersUnderLoad(t *testing.T) {
	h := newHarness(t)
	ctx := t.Context()
	h.connect(t)
	bookmark := h.transfer.connections.ActiveBookmarkID()
	h.mock.AddContainer("media", "standard")
	for i := range 3 {
		seedObject(h.mock, "media", fmt.Sprintf("scope/o-%d.bin", i), "x", nil)
	}
	block := newEngineBarrier(t)
	h.installEngine(block)
	h.transfer.setFilePicker(fakePicker{dir: t.TempDir()})
	picked, err := h.transfer.PickDownloadDir(ctx, SelectionScopeDTO{BookmarkID: bookmark, Container: "media", Prefix: "scope/"})
	if err != nil || picked == nil {
		t.Fatalf("pick: %+v %v", picked, err)
	}
	download, err := h.transfer.StartDownload(ctx, DownloadRequestDTO{BookmarkID: bookmark, Container: "media", Prefix: "scope/", DestDirToken: picked.Token})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { block.unblock(); awaitTerminal(t, h, download) })
	block.waitUntilEntered(t)

	// Four scans park inside the session's admission hook, the worker
	// limit; the gate then lets every later request through untouched.
	g := gateFindRequests(t, h, false)
	service := NewFindService(h.transfer.connections)
	var ids []string
	defer cleanupFind(t, service, &ids, g)
	for range maxFindWorkers {
		id, err := service.StartFind(ctx, findBM(service), "media", "", "report", false)
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
		awaitConnection(t, g.entered)
	}
	if id, err := service.StartFind(ctx, findBM(service), "media", "", "report", false); err == nil || id != "" {
		ids = append(ids, id)
		t.Fatalf("Find is not at its worker limit: id=%q err=%v", id, err)
	}
	g.armed.Store(false)

	const bound = 5 * time.Second
	settings := NewSettingsService(settingsFile(t), h.transfer.runner)
	started := time.Now()
	page, err := h.browse.Containers(ctx, bookmark, "", 100)
	listing := time.Since(started)
	if err != nil || len(page.Rows) == 0 {
		t.Fatalf("listing page under load: %+v %v", page, err)
	}
	started = time.Now()
	read, err := settings.Get(context.Background())
	settingsRead := time.Since(started)
	if err != nil || read.MaxActiveJobs == 0 {
		t.Fatalf("settings read under load: %+v %v", read, err)
	}
	if listing > bound || settingsRead > bound {
		t.Fatalf("control plane under load: listing %s, settings %s, bound %s", listing, settingsRead, bound)
	}
	t.Logf("listing page %s, settings read %s under a running download and %d parked scans", listing, settingsRead, maxFindWorkers)
}
