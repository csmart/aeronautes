package app

import (
	"errors"
	"os"
	"testing"

	transfercopy "github.com/csmart/aeronautes/internal/transfer/copy"
	"github.com/csmart/aeronautes/internal/transfer/rclone"
)

func TestDownloadRootGuardBeforeEngineConstruction(t *testing.T) {
	for _, fixture := range []struct {
		name     string
		pathCase bool
		portable bool
		refused  bool
	}{
		{"refused_false", false, false, false},
		{"refused_true", false, false, true},
		{"path_case_false", true, false, false},
		{"path_case_true", true, false, true},
		{"portable_false", false, true, false},
		{"portable_true", false, true, true},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			h := newHarness(t)
			h.connect(t)
			h.mock.AddContainer("media", "standard")
			captured := t.TempDir()
			if !fixture.pathCase && !fixture.portable && fixture.refused {
				captured += string(os.PathSeparator)
			}
			prefix := ""
			if fixture.pathCase || fixture.portable {
				prefix = "scope/"
				child := "bar/child"
				if fixture.refused {
					child = "foo/child"
				}
				if fixture.portable {
					// One portable component policy: a Windows device name
					// must not reach engine construction either.
					child = "NULx"
					if fixture.refused {
						child = "CON"
					}
				}
				for _, name := range []string{"000-safe", "Foo", child} {
					seedObject(h.mock, "media", prefix+name, "complete source bytes\x00\xff "+name, nil)
				}
			}
			h.transfer.setFilePicker(fakePicker{dir: captured})
			picked, err := h.transfer.PickDownloadDir(t.Context(), SelectionScopeDTO{BookmarkID: h.transfer.connections.ActiveBookmarkID(), Container: "media", Prefix: prefix})
			if err != nil || picked == nil || picked.Dir != captured {
				t.Fatalf("captured picker spelling differs: %+v %v", picked, err)
			}
			calls := 0
			construction := errors.New("fixture observed engine construction")
			h.transfer.setEngineFactory(func(transfercopy.RemoteSource, rclone.Options) (transfercopy.Engine, error) {
				calls++
				return nil, construction
			})
			id, err := h.transfer.StartDownload(t.Context(), DownloadRequestDTO{
				BookmarkID: h.transfer.connections.ActiveBookmarkID(),
				Container:  "media", Prefix: prefix, DestDirToken: picked.Token,
			})
			if fixture.refused {
				if id != "" || err == nil || err.Error() != "preflight found problems; resolve them and retry (nothing was written)" || calls != 0 {
					t.Errorf("invalid destination reached engine construction: id=%q err=%v calls=%d", id, err, calls)
				}
			} else if id != "" || !errors.Is(err, construction) || calls != 1 {
				t.Errorf("canonical control did not reach engine construction: id=%q err=%v calls=%d", id, err, calls)
			}
		})
	}
}
