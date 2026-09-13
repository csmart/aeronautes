package local

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	transfercopy "github.com/csmart/aeronautes/internal/transfer/copy"
	"github.com/csmart/aeronautes/internal/transfer/rclone"
)

type downloadRecordingEngine struct {
	Engine
	calls int
	check func(string, []string)
	after func()
}

func (e *downloadRecordingEngine) Copy(ctx context.Context, src, dst string, names []string, event func(rclone.Event)) (*rclone.Result, error) {
	e.calls++
	if e.check != nil {
		e.check(dst, names)
	}
	result, err := e.Engine.Copy(ctx, src, dst, names, event)
	if e.after != nil {
		e.after()
	}
	return result, err
}

func TestDownloadRecordingAcknowledgement(t *testing.T) {
	for _, native := range []bool{false, true} {
		for _, refusal := range []string{"none", "admit", "execute", "verify"} {
			t.Run(fmt.Sprintf("native_%v_%s", native, refusal), func(t *testing.T) {
				source, mock := session(t)
				prefix := "scope/"
				if native {
					prefix = "scope\n/"
				}
				for _, name := range []string{"a", "b"} {
					seed(mock, "media", prefix+name, "bytes "+name)
				}
				root := t.TempDir()
				writeFile(t, root, "keep", "unrelated")
				preflight, err := PreflightDownload(t.Context(), source, "media", prefix, root)
				if err != nil || preflight.Rejected() || len(preflight.Items) != 2 {
					t.Fatalf("fixture preflight: %+v %v", preflight, err)
				}
				beforeRequests := mock.Requests()
				admissions := map[string]DownloadAdmission{}
				acknowledged := map[string]bool{}
				var phases []string
				refused := errors.New("required record refused")
				engine := &downloadRecordingEngine{Engine: &fsEngine{session: source}}
				engine.check = func(stage string, names []string) {
					if len(admissions) != 2 || len(names) != 2 || stage != filepath.Join(root, ".aeronautes-stage-fixture") {
						t.Fatalf("engine ran without every exact admission: %+v %q %v", admissions, stage, names)
					}
				}
				recorder := &DownloadRecorder{
					Admit: func(a DownloadAdmission) error {
						phases = append(phases, "admit:"+a.Item.Name)
						if _, err := os.Lstat(a.StagingPath); !errors.Is(err, os.ErrNotExist) {
							t.Fatalf("staging preceded admission: %q %v", a.StagingPath, err)
						}
						if !native && (engine.calls != 0 || mock.Requests() != beforeRequests) || native && len(admissions) == 0 && mock.Requests() != beforeRequests {
							t.Fatal("selected data read preceded admission")
						}
						if refusal == "admit" {
							return refused
						}
						admissions[a.Item.Name] = a
						return nil
					},
					Execute: func(item DownloadItem, o transfercopy.Observation) error {
						phases = append(phases, "execute:"+item.Name)
						admitted, ok := admissions[item.Name]
						if !ok || admitted.Item != item {
							t.Fatal("execution lost its original admitted identity")
						}
						data, err := os.ReadFile(filepath.Join(root, item.RelPath))
						if err != nil || string(data) != "bytes "+item.RelPath || o.Result != transfercopy.ResultCopied || o.CopiedBytes != int64(len(data)) {
							t.Fatalf("execution lacked witnessed final content: %q %+v %v", data, o, err)
						}
						if _, err := os.Lstat(admitted.StagingPath); !errors.Is(err, os.ErrNotExist) {
							t.Fatalf("item cleanup was deferred past execution acknowledgement: %v", err)
						}
						if refusal == "execute" {
							return refused
						}
						acknowledged[item.Name] = true
						return nil
					},
					Verify: func(item DownloadItem, o transfercopy.Observation) error {
						if !acknowledged[item.Name] || len(phases) == 0 || phases[len(phases)-1] != "execute:"+item.Name || o.Result != transfercopy.ResultVerified {
							t.Fatalf("verification preceded execution acknowledgement: %v %+v", phases, o)
						}
						phases = append(phases, "verify:"+item.Name)
						if refusal == "verify" {
							return refused
						}
						return nil
					},
				}
				report, err := Download(t.Context(), source, engine, "remote:media", "media", prefix, root, "fixture", preflight, nil, recorder)
				if errors.Is(err, refused) != (refusal != "none") {
					t.Fatalf("required refusal was lost: %+v %v", report, err)
				}
				wantDone := int64(2)
				if refusal == "admit" {
					wantDone = 0
				} else if refusal != "none" {
					wantDone = 1
				}
				if report.Done != wantDone || report.Bytes != wantDone*7 || len(report.Failures) != 0 {
					t.Fatalf("record refusal changed witnessed placement: %+v", report)
				}
				if refusal == "admit" && (engine.calls != 0 || mock.Requests() != beforeRequests) {
					t.Fatal("admission refusal allowed dependent transfer work")
				}
				if refusal != "none" {
					if _, err := os.Lstat(filepath.Join(root, "b")); !errors.Is(err, os.ErrNotExist) {
						t.Fatal("required record refusal allowed a later placement")
					}
					if native && mock.Requests() != beforeRequests+int(wantDone) {
						t.Fatalf("required record refusal allowed another native GET: %d -> %d", beforeRequests, mock.Requests())
					}
				}
				if !native && refusal != "admit" {
					if report.StageCleanup == nil || report.StageCleanup.Retained != (refusal != "none") || report.StageCleanup.Issues != 0 {
						t.Fatalf("cleanup retention was lost or fabricated an issue: %+v", report.StageCleanup)
					}
					if refusal != "none" {
						data, err := os.ReadFile(filepath.Join(root, ".aeronautes-stage-fixture", "b"))
						if err != nil || string(data) != "bytes b" {
							t.Fatalf("record refusal reclaimed unresolved child data: %q %v", data, err)
						}
					}
				}
				if data, err := os.ReadFile(filepath.Join(root, "keep")); err != nil || string(data) != "unrelated" {
					t.Fatal("unrelated data changed")
				}
			})
		}
	}
}

func TestDownloadAdmittedTemporaryCollision(t *testing.T) {
	for _, kind := range []string{"file", "directory", "symlink"} {
		t.Run(kind, func(t *testing.T) {
			source, mock := session(t)
			seed(mock, "media", "scope\n/selected", "bytes")
			root := t.TempDir()
			writeFile(t, root, "keep", "other writer")
			preflight, err := PreflightDownload(t.Context(), source, "media", "scope\n/", root)
			if err != nil || preflight.Rejected() {
				t.Fatalf("fixture preflight: %+v %v", preflight, err)
			}
			var temporary string
			var observed transfercopy.Observation
			recorder := &DownloadRecorder{
				Admit: func(a DownloadAdmission) error {
					temporary = a.StagingPath
					if filepath.Dir(temporary) != root || !strings.HasPrefix(filepath.Base(temporary), ".aeronautes-part-") {
						t.Fatalf("wrong native temporary identity: %+v", a)
					}
					switch kind {
					case "file":
						return os.WriteFile(temporary, []byte("collision"), 0o600)
					case "directory":
						return os.Mkdir(temporary, 0o700)
					default:
						return os.Symlink(filepath.Join(root, "keep"), temporary)
					}
				},
				Execute: func(_ DownloadItem, o transfercopy.Observation) error { observed = o; return nil },
				Verify: func(DownloadItem, transfercopy.Observation) error {
					t.Fatal("collision invented verification")
					return nil
				},
			}
			report, err := Download(t.Context(), source, nil, "", "media", "scope\n/", root, "fixture", preflight, nil, recorder)
			if err != nil || report.Done != 0 || len(report.Failures) != 1 || observed.Result != transfercopy.ResultConflict || observed.Effect != transfercopy.EffectUnresolved {
				t.Fatalf("collision lost its refusal: %+v %+v %v", report, observed, err)
			}
			info, err := os.Lstat(temporary)
			if err != nil {
				t.Fatalf("failed exclusive creation unlinked another entry: %v", err)
			}
			if kind == "directory" && !info.IsDir() || kind == "symlink" && info.Mode()&os.ModeSymlink == 0 || kind == "file" && !info.Mode().IsRegular() {
				t.Fatal("collision entry changed type")
			}
			if kind == "file" {
				if data, err := os.ReadFile(temporary); err != nil || string(data) != "collision" {
					t.Fatal("collision entry changed content")
				}
			}
			if entries, err := os.ReadDir(root); err != nil || len(entries) != 2 {
				t.Fatalf("collision silently selected another temporary name or placed a target: %+v %v", entries, err)
			}
			if data, err := os.ReadFile(filepath.Join(root, "keep")); err != nil || string(data) != "other writer" {
				t.Fatal("collision changed unrelated bytes")
			}
		})
	}
}

func TestDownloadRejectsConstructedIdentity(t *testing.T) {
	for _, fault := range []string{"prefix", "relative", "dot", "empty", "control", "backslash", "bytes", "duplicate", "directory_size", "directory_name", "case_fold", "file_parent", "file_parent_nonadjacent"} {
		t.Run(fault, func(t *testing.T) {
			root := t.TempDir()
			item := DownloadItem{Name: "scope/selected", RelPath: "selected", Size: 5, Route: "native"}
			switch fault {
			case "prefix":
				item.Name = "other/selected"
			case "relative":
				item.RelPath = "another"
			case "dot":
				item.Name, item.RelPath = "scope/sub/../selected", "sub/../selected"
			case "empty":
				item.Name, item.RelPath = "scope/sub//selected", "sub//selected"
			case "control":
				item.Name, item.RelPath = "scope/sel\nected", "sel\nected"
			case "backslash":
				item.Name, item.RelPath = "scope/sub\\selected", "sub\\selected"
			case "bytes":
				item.Size = -1
			case "directory_size":
				item.Name, item.Dir = "scope/selected/", true
			case "directory_name":
				item.Size, item.Dir = 0, true
			}
			items := []DownloadItem{item}
			if fault == "duplicate" {
				items = append(items, item)
			}
			switch fault {
			case "case_fold":
				// Two directories that differ only by case are an alias
				// the executor admits (TestDownloadDirAliasesDirect); two
				// files are a collision it refuses.
				items = append(items, DownloadItem{Name: "scope/Selected", RelPath: "Selected", Size: 5})
			case "file_parent", "file_parent_nonadjacent":
				if fault == "file_parent_nonadjacent" {
					items = append(items, DownloadItem{Name: "scope/selected.txt", RelPath: "selected.txt", Size: 5})
				}
				items = append(items, DownloadItem{Name: "scope/selected/child", RelPath: "selected/child", Size: 5})
			}
			recorder := &DownloadRecorder{
				Admit: func(DownloadAdmission) error { t.Fatal("invalid constructed identity reached admission"); return nil },
				Execute: func(DownloadItem, transfercopy.Observation) error {
					t.Fatal("invalid constructed identity reached execution")
					return nil
				},
				Verify: func(DownloadItem, transfercopy.Observation) error {
					t.Fatal("invalid constructed identity reached verification")
					return nil
				},
			}
			if _, err := Download(t.Context(), nil, nil, "", "media", "scope/", root, "fixture", &DownloadPreflight{Items: items}, nil, recorder); err == nil {
				t.Fatal("invalid constructed identity was accepted")
			}
			if entries, err := os.ReadDir(root); err != nil || len(entries) != 0 {
				t.Fatalf("invalid constructed identity changed the filesystem: %+v %v", entries, err)
			}
		})
	}
}

func TestDownloadDirectoryRecording(t *testing.T) {
	for _, cancelAfterAdmission := range []bool{false, true} {
		t.Run(fmt.Sprint(cancelAfterAdmission), func(t *testing.T) {
			root := t.TempDir()
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			items := []DownloadItem{{Name: "scope/", Dir: true}, {Name: "scope/nested/", RelPath: "nested", Dir: true}}
			var admitted []DownloadItem
			var executed, verified []transfercopy.Observation
			recorder := &DownloadRecorder{
				Admit: func(a DownloadAdmission) error {
					if a.StagingPath != "" {
						t.Fatal("directory marker invented a temporary identity")
					}
					admitted = append(admitted, a.Item)
					if cancelAfterAdmission {
						cancel()
					}
					return nil
				},
				Execute: func(_ DownloadItem, o transfercopy.Observation) error { executed = append(executed, o); return nil },
				Verify:  func(_ DownloadItem, o transfercopy.Observation) error { verified = append(verified, o); return nil },
			}
			report, err := Download(ctx, nil, nil, "", "media", "scope/", root, "fixture", &DownloadPreflight{Items: items}, nil, recorder)
			if cancelAfterAdmission {
				if !errors.Is(err, context.Canceled) || report.Done != 0 || len(admitted) != 1 || len(verified) != 0 || len(executed) != 1 || executed[0].Result != transfercopy.ResultNotAttempted || executed[0].Effect != transfercopy.EffectNotAttempted {
					t.Fatalf("cancelled marker invented materialization: %+v %+v %+v %v", report, executed, verified, err)
				}
				if _, err := os.Stat(filepath.Join(root, "nested")); !errors.Is(err, os.ErrNotExist) {
					t.Fatal("cancelled directory created a later marker")
				}
			} else if err != nil || report.Done != 2 || report.Bytes != 0 || !reflect.DeepEqual(admitted, items) || len(executed) != 2 || len(verified) != 2 {
				t.Fatalf("directory records lost observed materialization: %+v %+v %+v %v", report, executed, verified, err)
			} else if info, err := os.Stat(filepath.Join(root, "nested")); err != nil || !info.IsDir() {
				t.Fatal("successful marker lacks an actual directory")
			}
		})
	}
}

func TestDownloadCancelledBodyClosure(t *testing.T) {
	for _, closeFailure := range []bool{false, true} {
		t.Run(fmt.Sprint(closeFailure), func(t *testing.T) {
			source, mock := session(t)
			seed(mock, "media", "scope\n/nested/selected", "bytes")
			body, _, err := source.OpenObject(t.Context(), "media", "scope\n/nested/selected")
			if err != nil {
				t.Fatal(err)
			}
			root := t.TempDir()
			item := DownloadItem{Name: "scope\n/nested/selected", RelPath: "nested/selected", Size: 5, Route: "native"}
			temporary := filepath.Join(root, "nested", ".aeronautes-part-fixture")
			closeErr := errors.New("independent source close failure")
			closes := 0
			wrapped := downloadCloseReader{Reader: body, close: func() error {
				closes++
				err := body.Close()
				if closeFailure {
					return errors.Join(err, closeErr)
				}
				return err
			}}
			ctx, cancel := context.WithCancel(t.Context())
			cancel()
			outcome := nativeDownloadBody(ctx, wrapped, item, testDestination(t, root), temporary, ladderFor(t, nil))
			want := transfercopy.ResultNotAttempted
			if closeFailure {
				want = transfercopy.ResultFailed
			}
			if outcome.Result != want || outcome.Effect != transfercopy.EffectNotAttempted || outcome.verification != nil ||
				closes != 1 || !errors.Is(outcome.err(), context.Canceled) || errors.Is(outcome.err(), closeErr) != closeFailure {
				t.Fatalf("cancelled open body lost independent closure facts: %+v closes=%d error=%v", outcome, closes, outcome.err())
			}
			if closeFailure && outcome.Diagnostic.Code != transfercopy.CodeIO {
				t.Fatalf("cancellation concealed the independent close failure: %+v", outcome.Diagnostic)
			}
			if entries, err := os.ReadDir(root); err != nil || len(entries) != 0 {
				t.Fatalf("cancellation after GET allowed local effects: %+v %v", entries, err)
			}
		})
	}
}

func TestDownloadProgressCountsPlacedFiles(t *testing.T) {
	for _, native := range []bool{false, true} {
		t.Run(fmt.Sprint(native), func(t *testing.T) {
			source, mock := session(t)
			prefix := "scope/"
			if native {
				prefix = "scope\n/"
			}
			for _, name := range []string{"a", "b"} {
				seed(mock, "media", prefix+name, "bytes "+name)
			}
			root := t.TempDir()
			preflight, err := PreflightDownload(t.Context(), source, "media", prefix, root)
			if err != nil || preflight.Rejected() {
				t.Fatalf("fixture preflight: %+v %v", preflight, err)
			}
			admitted := 0
			recorder := &DownloadRecorder{
				Admit: func(a DownloadAdmission) error {
					admitted++
					// Existing collisions now refuse the whole plan at entry.
					// Keep this progress oracle about a later writer.
					if a.Item.Name == prefix+"a" {
						writeFile(t, root, "a", "other writer")
					}
					return nil
				},
				Execute: func(DownloadItem, transfercopy.Observation) error { return nil },
				Verify:  func(DownloadItem, transfercopy.Observation) error { return nil },
			}
			updates := 0
			progress := func(done, total, bytes int64) {
				updates++
				var placed, size int64
				for _, name := range []string{"a", "b"} {
					data, err := os.ReadFile(filepath.Join(root, name))
					if err == nil && string(data) == "bytes "+name {
						placed++
						size += int64(len(data))
					} else if err != nil && !errors.Is(err, os.ErrNotExist) {
						t.Fatal(err)
					}
				}
				if done != placed || bytes != size || total != 2 {
					t.Fatalf("progress counted attempted names as copied: done=%d bytes=%d total=%d actual files=%d bytes=%d", done, bytes, total, placed, size)
				}
			}
			report, err := Download(t.Context(), source, &fsEngine{session: source}, "remote:media", "media", prefix, root, "fixture", preflight, progress, recorder)
			if err != nil || report.Done != 1 || report.Bytes != 7 || len(report.Failures) != 1 || updates != 2 || admitted != 2 {
				t.Fatalf("mixed transfer lost final accounting: %+v updates=%d error=%v", report, updates, err)
			}
			if data, err := os.ReadFile(filepath.Join(root, "a")); err != nil || string(data) != "other writer" {
				t.Fatal("progress control overwrote the raced target")
			}
		})
	}
}

func TestDownloadDirectoryObservation(t *testing.T) {
	for _, kind := range []string{"directory", "missing", "file", "symlink_directory"} {
		t.Run(kind, func(t *testing.T) {
			root := t.TempDir()
			target := filepath.Join(root, "marker")
			switch kind {
			case "directory":
				if err := os.Mkdir(target, 0o700); err != nil {
					t.Fatal(err)
				}
			case "file":
				writeFile(t, root, "marker", "other writer")
			case "symlink_directory":
				if err := os.Symlink(root, target); err != nil {
					t.Fatal(err)
				}
			}
			// A link at the marker's name is refused through the handle: the
			// marker must be an ordinary directory, exactly as admission requires.
			outcome := observeDownloadDirectory(testDestination(t, root).root, "marker")
			if kind == "directory" {
				if outcome.Result != transfercopy.ResultCopied || outcome.CopiedBytes != 0 || outcome.verification == nil || outcome.verification.Result != transfercopy.ResultVerified {
					t.Fatalf("observed directory was not a zero-byte materialization: %+v", outcome)
				}
			} else if outcome.Result != transfercopy.ResultFailed || outcome.Effect != transfercopy.EffectUnresolved || outcome.verification == nil ||
				outcome.verification.Result != transfercopy.ResultFailed || outcome.verification.Diagnostic.Stage != transfercopy.StageVerification {
				t.Fatalf("failed directory observation invented success or no effect: %+v", outcome)
			}
			if kind == "file" {
				if data, err := os.ReadFile(target); err != nil || string(data) != "other writer" {
					t.Fatal("directory observation changed another file")
				}
			}
		})
	}
}

func TestDownloadEngineCancellationBoundaries(t *testing.T) {
	for _, boundary := range []string{"control", "before_child", "after_child", "between_placements"} {
		t.Run(boundary, func(t *testing.T) {
			source, mock := session(t)
			for _, name := range []string{"a", "b"} {
				seed(mock, "media", "scope/"+name, "bytes "+name)
			}
			root := t.TempDir()
			writeFile(t, root, "keep", "unrelated")
			preflight, err := PreflightDownload(t.Context(), source, "media", "scope/", root)
			if err != nil || preflight.Rejected() {
				t.Fatalf("fixture preflight: %+v %v", preflight, err)
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			beforeRequests := mock.Requests()
			admitted := 0
			executed := map[string]transfercopy.Observation{}
			verified := map[string]transfercopy.Observation{}
			engine := &downloadRecordingEngine{Engine: &fsEngine{session: source}}
			if boundary == "after_child" {
				engine.after = func() {
					for _, name := range []string{"a", "b"} {
						data, err := os.ReadFile(filepath.Join(root, ".aeronautes-stage-fixture", name))
						if err != nil || string(data) != "bytes "+name {
							t.Fatalf("child cancellation lacks actual stage bytes: %q %v", data, err)
						}
					}
					cancel()
				}
			}
			recorder := &DownloadRecorder{
				Admit: func(DownloadAdmission) error {
					admitted++
					if admitted == 2 && boundary == "before_child" {
						cancel()
					}
					return nil
				},
				Execute: func(item DownloadItem, observation transfercopy.Observation) error {
					executed[item.Name] = observation
					return nil
				},
				Verify: func(item DownloadItem, observation transfercopy.Observation) error {
					verified[item.Name] = observation
					if boundary == "between_placements" {
						cancel()
					}
					return nil
				},
			}
			report, err := Download(ctx, source, engine, "remote:media", "media", "scope/", root, "fixture", preflight, nil, recorder)
			if admitted != 2 || errors.Is(err, context.Canceled) != (boundary != "control") {
				t.Fatalf("cancellation lost admitted scope: %+v admitted=%d err=%v", report, admitted, err)
			}
			// Every item carries an execution record at every boundary; the
			// ones a cancel left staged are recorded as unplaced failures.
			wantDone, wantFailures := int64(0), 0
			switch boundary {
			case "control":
				wantDone = 2
			case "after_child":
				wantFailures = 2
			case "between_placements":
				wantDone, wantFailures = 1, 1
			}
			if report.Done != wantDone || report.Bytes != wantDone*7 || len(report.Failures) != wantFailures || len(executed) != 2 || int64(len(verified)) != wantDone {
				t.Fatalf("cancellation invented or erased observed outcomes: %+v execution=%+v verification=%+v", report, executed, verified)
			}
			for _, failure := range report.Failures {
				if !failure.Cancelled {
					t.Fatalf("cancellation failure not marked as the cancellation: %+v", failure)
				}
			}
			for index, name := range []string{"a", "b"} {
				data, err := os.ReadFile(filepath.Join(root, name))
				if int64(index) < wantDone {
					if err != nil || string(data) != "bytes "+name || executed["scope/"+name].Result != transfercopy.ResultCopied || verified["scope/"+name].Result != transfercopy.ResultVerified {
						t.Fatalf("cancellation lost an earlier copied file: %q %+v %v", data, executed, err)
					}
				} else if !errors.Is(err, os.ErrNotExist) {
					t.Fatal("known cancellation allowed a later final placement")
				}
				if boundary == "before_child" {
					got := executed["scope/"+name]
					if got.Result != transfercopy.ResultNotAttempted || got.Effect != transfercopy.EffectNotAttempted || got.Diagnostic.Code != transfercopy.CodeCancelled {
						t.Fatalf("undispatched cancellation lost positive no-effect knowledge: %+v", got)
					}
				} else if boundary != "control" && int64(index) >= wantDone {
					if got := executed["scope/"+name]; got != unplacedAfterCancel {
						t.Fatalf("staged item left unplaced by the cancel lacks its one record: %+v", got)
					}
					staged, err := os.ReadFile(filepath.Join(root, ".aeronautes-stage-fixture", name))
					if err != nil || string(staged) != "bytes "+name {
						t.Fatal("cancellation reclaimed uncertain child data")
					}
				}
			}
			if boundary == "before_child" {
				if engine.calls != 0 || mock.Requests() != beforeRequests || report.StageCleanup != nil {
					t.Fatal("known cancellation dispatched the child or created a stage")
				}
				if _, err := os.Lstat(filepath.Join(root, ".aeronautes-stage-fixture")); !errors.Is(err, os.ErrNotExist) {
					t.Fatal("known cancellation created staging")
				}
			} else if report.StageCleanup == nil || report.StageCleanup.Retained || report.StageCleanup.Issues != map[bool]int64{true: 0, false: 1}[boundary == "control"] {
				t.Fatalf("cancellation confused conservative cleanup with record-refusal retention: %+v", report.StageCleanup)
			}
			if data, err := os.ReadFile(filepath.Join(root, "keep")); err != nil || string(data) != "unrelated" {
				t.Fatal("cancellation changed unrelated bytes")
			}
		})
	}
}
