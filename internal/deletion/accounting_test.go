package deletion

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/csmart/aeronautes/internal/swiftcontrol"
)

func ownedDeletionCache(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", root)
	t.Setenv("LocalAppData", root)
	t.Setenv("HOME", root)
	cache, err := os.UserCacheDir()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(cache, 0o700); err != nil {
		t.Fatal(err)
	}
	return cache
}

func TestDeletionIndexSetupRefusesDispatch(t *testing.T) {
	for _, prefix := range []bool{false, true} {
		for _, parallel := range []bool{false, true} {
			for _, blocked := range []bool{false, true} {
				t.Run(fmt.Sprintf("prefix_%t/parallel_%t/blocked_%t", prefix, parallel, blocked), func(t *testing.T) {
					cache := ownedDeletionCache(t)
					if blocked {
						if err := os.WriteFile(filepath.Join(cache, "aeronautes"), []byte("owned cache obstruction"), 0o600); err != nil {
							t.Fatal(err)
						}
					}
					session, capabilities, mock := cluster(t, "")
					mock.AddContainer("media", "standard")
					seedMany(mock, "media", "selected/", 3)
					seedMany(mock, "media", "kept/", 1)
					names := []string{"selected/obj-0000", "selected/obj-0001", "selected/obj-0002"}
					opts := Options{ForceParallel: parallel, Workers: 2, BulkSize: 2, Passes: 1}
					var result *Result
					var err error
					if prefix {
						result, err = DeletePrefix(t.Context(), session, capabilities, "media", "selected/", opts)
					} else {
						result, err = DeleteNames(t.Context(), session, capabilities, "media", names, opts)
					}
					if blocked {
						if err == nil || result == nil || result.Deleted != 0 || result.Failed != 0 || result.Unresolved != 0 {
							t.Errorf("unavailable index did not refuse the run: result=%+v err=%v", result, err)
						}
						for _, request := range mock.RequestLog() {
							if strings.HasPrefix(request, "DELETE ") || strings.Contains(request, "bulk-delete") {
								t.Errorf("index setup failure admitted a destructive request: %s", request)
							}
						}
					} else if err != nil || result == nil || result.Deleted != 3 || result.Remaining != 0 {
						t.Errorf("healthy index control: result=%+v err=%v", result, err)
					}
					for _, name := range names {
						_, err := session.ObjectDetail(t.Context(), "media", name)
						if blocked && err != nil {
							t.Errorf("refused name did not survive: %s: %v", name, err)
						} else if !blocked && !swiftcontrol.IsNotFound(err) {
							t.Errorf("control name was not deleted: %s: %v", name, err)
						}
					}
					if _, err := session.ObjectDetail(t.Context(), "media", "kept/obj-0000"); err != nil {
						t.Fatalf("collateral was changed: %v", err)
					}
				})
			}
		}
	}
}
