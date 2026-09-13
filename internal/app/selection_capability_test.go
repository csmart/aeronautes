package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	transfercopy "github.com/csmart/aeronautes/internal/transfer/copy"
	"github.com/csmart/aeronautes/internal/transfer/rclone"
)

// Maintained regressions for ADR 0041. A picker selection authorises one
// operation in one scope, expires, and does not survive the start it
// authorised. These replace the probe archived with the plan; the four
// cases that reproduced SEC-07 there are the four refusals asserted
// here, reached through the same bridge-facing methods.

// activeScope is the scope a request for the connected account carries.
func activeScope(t *testing.T, h *harness, container, prefix string) selectionScope {
	t.Helper()
	entry, id, _, err := h.transfer.connections.activeSnapshot()
	if err != nil {
		t.Fatalf("no active connection: %v", err)
	}
	return selectionScope{bookmarkID: id, generation: entry.generation, container: container, prefix: prefix}
}

func pickFiles(t *testing.T, h *harness, ctx context.Context, container, prefix string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "payload.txt")
	if err := os.WriteFile(path, []byte("payload"), 0o600); err != nil {
		t.Fatal(err)
	}
	h.transfer.setFilePicker(fakePicker{files: []string{path}})
	picked, err := h.transfer.PickUploadFiles(ctx, SelectionScopeDTO{BookmarkID: h.transfer.connections.ActiveBookmarkID(), Container: container, Prefix: prefix})
	if err != nil || picked == nil || picked.Token == "" {
		t.Fatalf("pick upload files: %+v %v", picked, err)
	}
	return picked.Token
}

func pickDir(t *testing.T, h *harness, ctx context.Context, container, prefix string) string {
	t.Helper()
	h.transfer.setFilePicker(fakePicker{dir: t.TempDir()})
	picked, err := h.transfer.PickDownloadDir(ctx, SelectionScopeDTO{BookmarkID: h.transfer.connections.ActiveBookmarkID(), Container: container, Prefix: prefix})
	if err != nil || picked == nil || picked.Token == "" {
		t.Fatalf("pick download dir: %+v %v", picked, err)
	}
	return picked.Token
}

// A directory picked while one account was connected must not receive a
// download from another account.
func TestSelectionRefusesAnotherAccountForDownload(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	h.connect(t)
	other, otherMock := secondCluster(t, h)
	otherMock.LoadCanonicalFixtures()

	token := pickDir(t, h, ctx, "fixtures", "normal/")
	_, err := h.transfer.PreflightDownload(ctx, DownloadRequestDTO{
		BookmarkID: other, Container: "fixtures", Prefix: "normal/", DestDirToken: token,
	})
	if !errors.Is(err, errSelectionAccount) {
		t.Errorf("cross-account download = %v, want a different-account refusal", err)
	}
}

// A file selection picked for one account must not upload into another.
func TestSelectionRefusesAnotherAccountForUpload(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	h.connect(t)
	other, otherMock := secondCluster(t, h)
	otherMock.LoadCanonicalFixtures()

	token := pickFiles(t, h, ctx, "fixtures", "up/")
	_, err := h.transfer.PreflightUpload(ctx, UploadRequestDTO{
		BookmarkID: other, Container: "fixtures", Prefix: "up/", SelectionToken: token,
	})
	if !errors.Is(err, errSelectionAccount) {
		t.Errorf("cross-account upload = %v, want a different-account refusal", err)
	}
}

// A selection must not outlive the credential generation it was made
// under: re-saving the bookmark republishes it with a new generation.
func TestSelectionRefusesAfterGenerationMoves(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	account := h.connect(t)
	bookmark := h.transfer.connections.ActiveBookmarkID()

	token := pickDir(t, h, ctx, "fixtures", "normal/")
	saved, err := h.bookmarks.Save(ctx, BookmarkDTO{
		ID: bookmark, DisplayName: account.Name + " again", AuthURL: h.authURL, Username: "test:tester",
	}, "testing")
	if err != nil {
		t.Fatalf("re-save bookmark: %v", err)
	}
	_, err = h.transfer.PreflightDownload(ctx, DownloadRequestDTO{
		BookmarkID: saved.ID, Container: "fixtures", Prefix: "normal/", DestDirToken: token,
	})
	if err == nil {
		t.Error("a selection issued under an earlier generation was still accepted")
	}
}

// A selection must not be replayed against a scope it was not made for.
func TestSelectionRefusesAnotherPrefix(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	h.connect(t)
	h.mock.LoadCanonicalFixtures()
	bookmark := h.transfer.connections.ActiveBookmarkID()

	token := pickDir(t, h, ctx, "fixtures", "normal/")
	if _, err := h.transfer.PreflightDownload(ctx, DownloadRequestDTO{
		BookmarkID: bookmark, Container: "fixtures", Prefix: "normal/", DestDirToken: token,
	}); err != nil {
		t.Fatalf("own-scope preflight failed: %v", err)
	}
	_, err := h.transfer.PreflightDownload(ctx, DownloadRequestDTO{
		BookmarkID: bookmark, Container: "fixtures", Prefix: "implied/", DestDirToken: token,
	})
	if !errors.Is(err, errSelectionScope) {
		t.Errorf("replay against another prefix = %v, want a scope refusal", err)
	}
}

// An upload selection must not be presented at the download boundary.
func TestSelectionRefusesTheOtherOperation(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	h.connect(t)
	h.mock.LoadCanonicalFixtures()
	bookmark := h.transfer.connections.ActiveBookmarkID()

	token := pickFiles(t, h, ctx, "fixtures", "normal/")
	_, err := h.transfer.PreflightDownload(ctx, DownloadRequestDTO{
		BookmarkID: bookmark, Container: "fixtures", Prefix: "normal/", DestDirToken: token,
	})
	if !errors.Is(err, errSelectionOperation) {
		t.Errorf("upload selection at the download boundary = %v, want an operation refusal", err)
	}
}

// A selection that is never started expires.
func TestSelectionExpires(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	h.connect(t)
	h.mock.LoadCanonicalFixtures()
	bookmark := h.transfer.connections.ActiveBookmarkID()

	now := time.Now()
	h.transfer.selections.now = func() time.Time { return now }
	token := pickDir(t, h, ctx, "fixtures", "normal/")
	if _, err := h.transfer.PreflightDownload(ctx, DownloadRequestDTO{
		BookmarkID: bookmark, Container: "fixtures", Prefix: "normal/", DestDirToken: token,
	}); err != nil {
		t.Fatalf("preflight inside the lifetime failed: %v", err)
	}
	now = now.Add(selectionLifetime + time.Second)
	_, err := h.transfer.PreflightDownload(ctx, DownloadRequestDTO{
		BookmarkID: bookmark, Container: "fixtures", Prefix: "normal/", DestDirToken: token,
	})
	if !errors.Is(err, errSelectionExpired) {
		t.Errorf("preflight past the lifetime = %v, want an expiry refusal", err)
	}
}

// The pick is refused when the connected account moves while the native
// dialog is open. Two observations, not a record of every transient
// change: ADR 0041 records that limit.
func TestPickRefusesAnAccountSwappedUnderTheDialog(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	h.connect(t)
	other, _ := secondCluster(t, h)
	bookmark := h.transfer.connections.ActiveBookmarkID()

	h.transfer.setFilePicker(swappingPicker{dir: t.TempDir(), during: func() {
		if _, err := h.browse.Connect(ctx, other); err != nil {
			t.Errorf("second connect failed: %v", err)
		}
	}})
	picked, err := h.transfer.PickDownloadDir(ctx, SelectionScopeDTO{BookmarkID: bookmark, Container: "fixtures", Prefix: "normal/"})
	if err == nil || picked != nil {
		t.Errorf("pick under a swapped account = %+v %v, want a refusal", picked, err)
	}
}

// The pick is refused when the account's credentials are re-saved while
// the native dialog is open, even though the account itself did not
// change.
func TestPickRefusesAGenerationMovedUnderTheDialog(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	account := h.connect(t)
	bookmark := h.transfer.connections.ActiveBookmarkID()

	h.transfer.setFilePicker(swappingPicker{dir: t.TempDir(), during: func() {
		if _, err := h.bookmarks.Save(ctx, BookmarkDTO{
			ID: bookmark, DisplayName: account.Name + " again", AuthURL: h.authURL, Username: "test:tester",
		}, "testing"); err != nil {
			t.Errorf("re-save during the dialog failed: %v", err)
		}
	}})
	picked, err := h.transfer.PickDownloadDir(ctx, SelectionScopeDTO{
		BookmarkID: bookmark, Container: "fixtures", Prefix: "normal/"})
	if err == nil || picked != nil {
		t.Errorf("pick across a re-save = %+v %v, want a refusal", picked, err)
	}
}

// The endpoint observation is kept across the dialog in its own right.
// Connecting elsewhere and then republishing that bookmark leaves no
// active connection at all, so the two observations differ even though
// the requested bookmark and its generation never moved. Both pickers
// must refuse.
func TestPickRefusesAConnectionThatEndsUnderTheDialog(t *testing.T) {
	for _, operation := range []string{"download", "upload"} {
		t.Run(operation, func(t *testing.T) {
			h := newHarness(t)
			ctx := context.Background()
			h.connect(t)
			bookmark := h.transfer.connections.ActiveBookmarkID()
			other, _ := secondCluster(t, h)

			// Connect to the second account, then republish it: the
			// active connection is dropped rather than moved.
			during := func() {
				saved, err := h.bookmarks.List(ctx)
				if err != nil {
					t.Errorf("list bookmarks: %v", err)
					return
				}
				if _, err := h.browse.Connect(ctx, other); err != nil {
					t.Errorf("second connect failed: %v", err)
					return
				}
				for _, entry := range saved {
					if entry.ID != other {
						continue
					}
					if _, err := h.bookmarks.Save(ctx, entry, "testing2"); err != nil {
						t.Errorf("re-save the second bookmark: %v", err)
					}
				}
			}
			scope := SelectionScopeDTO{BookmarkID: bookmark, Container: "fixtures", Prefix: "normal/"}
			if operation == "download" {
				h.transfer.setFilePicker(swappingPicker{dir: t.TempDir(), during: during})
				picked, err := h.transfer.PickDownloadDir(ctx, scope)
				if err == nil || picked != nil {
					t.Errorf("download pick across an ended connection = %+v %v, want a refusal", picked, err)
				}
				return
			}
			dir := t.TempDir()
			path := filepath.Join(dir, "payload.txt")
			if err := os.WriteFile(path, []byte("payload"), 0o600); err != nil {
				t.Fatal(err)
			}
			h.transfer.setFilePicker(swappingPicker{files: []string{path}, during: during})
			picked, err := h.transfer.PickUploadFiles(ctx, scope)
			if err == nil || picked != nil {
				t.Errorf("upload pick across an ended connection = %+v %v, want a refusal", picked, err)
			}
		})
	}
}

// A pick must name the account the user is connected to. This is the
// check before the dialog opens, distinct from the endpoint comparison
// either side of it.
func TestPickRefusesAnAccountOtherThanTheConnectedOne(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	h.connect(t)
	other, _ := secondCluster(t, h)

	h.transfer.setFilePicker(fakePicker{dir: t.TempDir()})
	picked, err := h.transfer.PickDownloadDir(ctx, SelectionScopeDTO{
		BookmarkID: other, Container: "fixtures", Prefix: "normal/"})
	if err == nil || picked != nil {
		t.Errorf("pick naming an unconnected account = %+v %v, want a refusal", picked, err)
	}
}

// With nothing connected the endpoint observation is absent either side
// of the dialog, so only the named bookmark's generation can catch a
// re-save while the user was choosing.
func TestPickRefusesAGenerationMovedWithNothingConnected(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	account := h.connect(t)
	bookmark := h.transfer.connections.ActiveBookmarkID()
	resave := func() error {
		_, err := h.bookmarks.Save(ctx, BookmarkDTO{
			ID: bookmark, DisplayName: account.Name + " again", AuthURL: h.authURL, Username: "test:tester",
		}, "testing")
		return err
	}
	// Retire the active connection the way the product does: re-save,
	// then let the next resolution drop the obsolete entry.
	if err := resave(); err != nil {
		t.Fatalf("re-save bookmark: %v", err)
	}
	_, _ = h.transfer.connections.SessionFor(ctx, bookmark)
	if observed := h.transfer.observeConnection(); observed.connected {
		t.Fatalf("fixture did not reach an unconnected endpoint: %+v", observed)
	}

	h.transfer.setFilePicker(swappingPicker{dir: t.TempDir(), during: func() {
		if err := resave(); err != nil {
			t.Errorf("re-save during the dialog: %v", err)
		}
	}})
	picked, err := h.transfer.PickDownloadDir(ctx, SelectionScopeDTO{
		BookmarkID: bookmark, Container: "fixtures", Prefix: "normal/"})
	if err == nil || picked != nil {
		t.Errorf("pick across a re-save with nothing connected = %+v %v, want a refusal", picked, err)
	}
}

// A connection that changes while it is being read is not an absent
// one. activeSnapshot reports the same error for both, so the endpoint
// is read directly; reading it while another connect is in flight must
// never report absence.
func TestConnectionObservationNeverReadsAChangeAsAbsence(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	h.connect(t)
	first := h.transfer.connections.ActiveBookmarkID()
	second, _ := secondCluster(t, h)

	done := make(chan struct{})
	go func() {
		defer close(done)
		for range 150 {
			if _, err := h.browse.Connect(ctx, second); err != nil {
				return
			}
			if _, err := h.browse.Connect(ctx, first); err != nil {
				return
			}
		}
	}()
	for {
		select {
		case <-done:
			return
		default:
		}
		if observed := h.transfer.observeConnection(); !observed.connected {
			t.Fatal("a connection being replaced was read as no connection at all")
		}
	}
}

// Starting consumes the selection, so a confirmed pick cannot be
// replayed into a second job. The selection is taken before the engine
// is built, so a start that fails there has still spent it; that is the
// cost of deciding the winner under one lock (ADR 0041).
func TestStartConsumesTheSelection(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	h.connect(t)
	h.mock.AddContainer("consume", "standard")
	seedObject(h.mock, "consume", "scope/file", "contents", nil)
	bookmark := h.transfer.connections.ActiveBookmarkID()

	construction := errors.New("fixture observed engine construction")
	calls := 0
	h.transfer.setEngineFactory(func(transfercopy.RemoteSource, rclone.Options) (transfercopy.Engine, error) {
		calls++
		return nil, construction
	})
	token := pickDir(t, h, ctx, "consume", "scope/")
	req := DownloadRequestDTO{BookmarkID: bookmark, Container: "consume", Prefix: "scope/", DestDirToken: token}
	if _, err := h.transfer.StartDownload(ctx, req); !errors.Is(err, construction) || calls != 1 {
		t.Fatalf("first start = %v after %d engine calls, want to reach construction once", err, calls)
	}
	_, err := h.transfer.StartDownload(ctx, req)
	if !errors.Is(err, errSelectionUnknown) {
		t.Errorf("second start on a consumed selection = %v, want an unknown-selection refusal", err)
	}
	if calls != 1 {
		t.Errorf("second start reached engine construction: calls=%d", calls)
	}
}

// The upload start consumes its selection too. The dual path is the
// blind spot here: a rule applied to download and forgotten on upload
// leaves half the boundary open.
func TestStartUploadConsumesTheSelection(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	h.connect(t)
	h.mock.AddContainer("consume-up", "standard")
	bookmark := h.transfer.connections.ActiveBookmarkID()

	construction := errors.New("fixture observed engine construction")
	calls := 0
	h.transfer.setEngineFactory(func(transfercopy.RemoteSource, rclone.Options) (transfercopy.Engine, error) {
		calls++
		return nil, construction
	})
	token := pickFiles(t, h, ctx, "consume-up", "up/")
	req := UploadRequestDTO{BookmarkID: bookmark, Container: "consume-up", Prefix: "up/", SelectionToken: token}
	if _, err := h.transfer.StartUpload(ctx, req); !errors.Is(err, construction) || calls != 1 {
		t.Fatalf("first start = %v after %d engine calls, want to reach construction once", err, calls)
	}
	_, err := h.transfer.StartUpload(ctx, req)
	if !errors.Is(err, errSelectionUnknown) {
		t.Errorf("second upload start on a consumed selection = %v, want an unknown-selection refusal", err)
	}
	if calls != 1 {
		t.Errorf("second start reached engine construction: calls=%d", calls)
	}
}

// Consumption is atomic: concurrent starts on one selection cannot both
// take it.
func TestConcurrentStartsTakeTheSelectionOnce(t *testing.T) {
	base := t.TempDir()
	selected := filepath.Join(base, "selected")
	if err := os.Mkdir(selected, 0o755); err != nil {
		t.Fatal(err)
	}
	store := &selectionStore{}
	scope := selectionScope{bookmarkID: "b", generation: 1, container: "c", prefix: "p/"}
	token := store.putDir(scope, selected)

	const racers = 8
	var wg sync.WaitGroup
	var mu sync.Mutex
	var won int
	for range racers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			root, _, err := store.takeDir(token, scope)
			if err != nil {
				return
			}
			mu.Lock()
			won++
			mu.Unlock()
			if root != nil {
				_ = root.Close()
			}
		}()
	}
	wg.Wait()
	if won != 1 {
		t.Errorf("%d concurrent starts took the selection, want exactly 1", won)
	}
}

// Healthy control: a selection works for the scope it was made in.
func TestSelectionWorksInItsOwnScope(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	h.connect(t)
	h.mock.LoadCanonicalFixtures()
	bookmark := h.transfer.connections.ActiveBookmarkID()

	token := pickDir(t, h, ctx, "fixtures", "normal/")
	if _, err := h.transfer.PreflightDownload(ctx, DownloadRequestDTO{
		BookmarkID: bookmark, Container: "fixtures", Prefix: "normal/", DestDirToken: token,
	}); err != nil {
		t.Errorf("own-scope download preflight = %v, want success", err)
	}
}

// Healthy control: preflight is read-only, so a report may be repeated
// before the user confirms.
func TestPreflightMayRepeatBeforeStart(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	h.connect(t)
	h.mock.LoadCanonicalFixtures()
	bookmark := h.transfer.connections.ActiveBookmarkID()

	token := pickDir(t, h, ctx, "fixtures", "normal/")
	req := DownloadRequestDTO{BookmarkID: bookmark, Container: "fixtures", Prefix: "normal/", DestDirToken: token}
	for attempt := range 3 {
		if _, err := h.transfer.PreflightDownload(ctx, req); err != nil {
			t.Fatalf("preflight %d of 3 = %v, want success", attempt+1, err)
		}
	}
}

// Healthy control: a selection the picker never issued stays refused.
func TestForgedSelectionRefused(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	h.connect(t)
	bookmark := h.transfer.connections.ActiveBookmarkID()

	_, err := h.transfer.PreflightDownload(ctx, DownloadRequestDTO{
		BookmarkID: bookmark, Container: "fixtures", Prefix: "normal/", DestDirToken: "forged",
	})
	if !errors.Is(err, errSelectionUnknown) {
		t.Errorf("forged token = %v, want an unknown-selection refusal", err)
	}
}

// Healthy control: an upload selection works in its own scope.
func TestUploadSelectionWorksInItsOwnScope(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	h.connect(t)
	h.mock.LoadCanonicalFixtures()
	bookmark := h.transfer.connections.ActiveBookmarkID()

	token := pickFiles(t, h, ctx, "fixtures", "up/")
	if _, err := h.transfer.PreflightUpload(ctx, UploadRequestDTO{
		BookmarkID: bookmark, Container: "fixtures", Prefix: "up/", SelectionToken: token,
	}); err != nil {
		t.Errorf("own-scope upload preflight = %v, want success", err)
	}
}

// swappingPicker runs during the blocking dialog, standing in for a page
// that connects elsewhere while the user is choosing.
type swappingPicker struct {
	dir    string
	files  []string
	during func()
}

func (p swappingPicker) PickFiles(string) ([]string, error) {
	if p.during != nil {
		p.during()
	}
	return p.files, nil
}

func (p swappingPicker) PickDirectory(string) (string, error) {
	if p.during != nil {
		p.during()
	}
	return p.dir, nil
}
