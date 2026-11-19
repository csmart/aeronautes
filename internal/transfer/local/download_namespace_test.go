package local

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	transfercopy "github.com/csmart/aeronautes/internal/transfer/copy"
)

func downloadNamespaceBody(name string) string {
	if strings.HasSuffix(name, "/") {
		return ""
	}
	return "complete source bytes\x00\xff " + name
}

func downloadNamespaceItems(prefix, route string, names []string) []DownloadItem {
	var items []DownloadItem
	for _, name := range names {
		item := DownloadItem{Name: prefix + name, RelPath: filepath.FromSlash(strings.TrimSuffix(name, "/")), Route: route}
		if strings.HasSuffix(name, "/") {
			item.Dir, item.Route = true, ""
		} else {
			item.Size = int64(len(downloadNamespaceBody(name)))
		}
		items = append(items, item)
	}
	return items
}

// Each namespace policy shares these execution boundaries: refusal has no
// effects, and accepted names produce whole bytes and the same item records.
// Keep the policy tables and exact preflight refusal checks in their own tests.
func testDownloadNamespaceDirect(t *testing.T, route, label string, names []string, refusalText string) {
	t.Helper()
	for _, recorded := range []bool{false, true} {
		for _, reverse := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/%s/recorded_%v/reverse_%v", route, label, recorded, reverse), func(t *testing.T) {
				source, mock := session(t)
				prefix := "scope/"
				if route == "native" {
					prefix = "scope\n/"
				}
				for _, name := range names {
					seed(mock, "media", prefix+name, downloadNamespaceBody(name))
				}
				owned := t.TempDir()
				root := filepath.Join(owned, "selected")
				if err := os.Mkdir(root, 0o700); err != nil {
					t.Fatal(err)
				}
				writeFile(t, root, "keep", "selected sentinel\x00\xff")
				writeFile(t, owned, "outside", "outside sentinel\x00\xff")
				items := downloadNamespaceItems(prefix, route, names)
				if reverse {
					slices.Reverse(items)
				}
				before := downloadDestinationSnapshot(t, owned)
				plan := &DownloadPreflight{Items: items}
				requests := mock.Requests()
				engine := &downloadRecordingEngine{Engine: &fsEngine{session: source}}
				var admissions []DownloadAdmission
				executed, verified := map[DownloadItem]transfercopy.Observation{}, map[DownloadItem]transfercopy.Observation{}
				progressCalls := 0
				var recorders []*DownloadRecorder
				if recorded {
					recorders = []*DownloadRecorder{{
						Admit:   func(admission DownloadAdmission) error { admissions = append(admissions, admission); return nil },
						Execute: func(item DownloadItem, result transfercopy.Observation) error { executed[item] = result; return nil },
						Verify:  func(item DownloadItem, result transfercopy.Observation) error { verified[item] = result; return nil },
					}}
				}
				report, err := Download(t.Context(), source, engine, "remote:media", "media", prefix, root,
					plan, func(int64, int64, int64, int64) { progressCalls++ }, recorders...)
				after := downloadDestinationSnapshot(t, owned)
				if refusalText != "" {
					if err != nil && !strings.Contains(err.Error(), refusalText) {
						t.Errorf("direct refusal changed its reported reason: %v", err)
					}
					if err == nil || report != nil || engine.calls != 0 || mock.Requests() != requests ||
						len(admissions)+len(executed)+len(verified)+progressCalls != 0 {
						t.Errorf("collision admitted a direct plan: report=%+v error=%v Copy calls=%d source requests=%d admissions=%+v executed=%+v verified=%+v progress=%d",
							report, err, engine.calls, mock.Requests()-requests, admissions, executed, verified, progressCalls)
					}
					if !reflect.DeepEqual(before, after) {
						t.Errorf("refused direct plan changed owned entries: before=%+v after=%+v", before, after)
					}
					return
				}
				// An accepted control must prove the download
				// happened, not merely that admission returned.
				// A no-op executor that admits and returns an
				// empty successful report must fail here.
				var total, fileCount int64
				for _, item := range items {
					total += item.Size
					if !item.Dir {
						fileCount++
					}
				}
				want := &Report{Done: int64(len(items)), Verified: int64(len(items)), Bytes: total}
				wantCalls := 0
				if route == "native" {
					want.NativeRouted = fileCount
				} else if fileCount > 0 {
					wantCalls = 1
				}
				if err != nil || !reflect.DeepEqual(report, want) || engine.calls != wantCalls || progressCalls == 0 {
					t.Fatalf("accepted control lost its exact outcome: report=%+v want=%+v error=%v Copy calls=%d want=%d progress=%d",
						report, want, err, engine.calls, wantCalls, progressCalls)
				}
				if recorded {
					if len(admissions) != len(items) || len(executed) != len(items) || len(verified) != len(items) {
						t.Fatalf("accepted control lost records: admissions=%+v executed=%+v verified=%+v", admissions, executed, verified)
					}
					for index, item := range items {
						a := admissions[index]
						x, v := executed[item], verified[item]
						if a.Item != item || x.Result != transfercopy.ResultCopied || x.Effect != transfercopy.EffectReportedApplied ||
							x.CopiedBytes != item.Size || x.Diagnostic != (transfercopy.Diagnostic{}) ||
							v.Result != transfercopy.ResultVerified || v.Effect != transfercopy.EffectNotAttempted ||
							v.Diagnostic != (transfercopy.Diagnostic{}) || v.CopiedBytes != 0 {
							t.Fatalf("accepted control lost identity or observations: item=%+v admission=%+v execution=%+v verification=%+v", item, a, x, v)
						}
					}
				}
				// Every owned entry that existed must be
				// unchanged, every selected name must be present
				// with its exact bytes, and nothing else may
				// appear.
				for path, state := range before {
					if after[path] != state {
						t.Errorf("accepted control changed existing entry %q: %+v -> %+v", path, state, after[path])
					}
					delete(after, path)
				}
				for _, name := range names {
					path := filepath.Join(root, filepath.FromSlash(strings.TrimSuffix(name, "/")))
					if strings.HasSuffix(name, "/") {
						continue
					}
					state, ok := after[path]
					if !ok || !state.mode.IsRegular() || state.body != downloadNamespaceBody(name) {
						t.Fatalf("accepted control lost the full file bytes for %q: %+v", name, state)
					}
					delete(after, path)
				}
				permitted := map[string]bool{}
				for _, name := range names {
					path := filepath.Join(root, filepath.FromSlash(strings.TrimSuffix(name, "/")))
					if !strings.HasSuffix(name, "/") {
						path = filepath.Dir(path)
					}
					for path != root && strings.HasPrefix(path, root) {
						permitted[path] = true
						path = filepath.Dir(path)
					}
				}
				for path := range permitted {
					if state, ok := after[path]; !ok || !state.mode.IsDir() {
						t.Errorf("accepted control is missing the directory %q: %+v", path, state)
					}
					delete(after, path)
				}
				if len(after) != 0 {
					t.Errorf("accepted control left unexpected entries: %+v", after)
				}
			})
		}
	}
}
