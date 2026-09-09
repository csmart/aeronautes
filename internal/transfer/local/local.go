// Package local is the local↔Swift boundary: multi-file upload and
// prefix-scoped download, each behind the §27.5 rule — a COMPLETE
// collision preflight before any write, and the v1 policy on a
// collision is to reject the whole operation, reported fully. The
// engine (rclone) moves safe names; identities it cannot carry stream
// natively. v1 enforces ONE cross-platform namespace policy: names
// that cannot materialize identically on every desktop target
// (backslashes, dot segments, empty segments, control characters,
// case-fold twins) REJECT rather than behave per-OS; §65.10 tracks
// per-target relaxations.
package local

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/csmart/aeronautes/internal/swiftcontrol"
	transfercopy "github.com/csmart/aeronautes/internal/transfer/copy"
	"github.com/csmart/aeronautes/internal/transfer/plan"
	"github.com/csmart/aeronautes/internal/transfer/rclone"
)

// Problem is one preflight finding. Reject=true findings abort the
// operation (the §27.5 v1 policy); the rest are portability warnings.
type Problem struct {
	Name   string `json:"name"`
	Kind   string `json:"kind"`
	Detail string `json:"detail"`
	Reject bool   `json:"reject"`
}

// UploadItem is one file cleared for upload.
type UploadItem struct {
	Path string `json:"path"`
	// Name is the destination object name (prefix + basename).
	Name  string `json:"name"`
	Size  int64  `json:"size"`
	Route string `json:"route"` // "rclone" | "native"
}

// UploadPreflight is the complete pre-write report.
type UploadPreflight struct {
	Items      []UploadItem `json:"items"`
	Problems   []Problem    `json:"problems"`
	TotalBytes int64        `json:"totalBytes"`
}

// Rejected reports whether any finding aborts the operation.
func rejected(problems []Problem) bool {
	for _, p := range problems {
		if p.Reject {
			return true
		}
	}
	return false
}

func (p *UploadPreflight) Rejected() bool { return rejected(p.Problems) }

const uploadPathRefusal = "the selected file path cannot be used unchanged; select it again using an absolute UTF-8 path without dot or empty components"

func validUploadPath(path string) bool {
	// Grouping cleans the parent path; a changed spelling can resolve to
	// another file when a symlink precedes '..'. Never substitute that file.
	return utf8.ValidString(path) && filepath.IsAbs(path) && filepath.Clean(path) == path
}

// PreflightUpload maps the selected files onto container/prefix and
// reports every problem before anything is written: unreadable or
// non-regular paths, duplicate basenames in the selection, and names
// already live at the destination (v1 refuses upload-overwrites). The
// engine writes only into the job staging namespace and each name
// commits natively with a conditional create (§28.6, Upload).
func PreflightUpload(ctx context.Context, dst *swiftcontrol.Session, container, prefix string, paths []string) (*UploadPreflight, error) {
	if prefix != "" && !strings.HasSuffix(prefix, "/") {
		return nil, fmt.Errorf("prefix %q must be empty or end with '/'", prefix)
	}
	report := &UploadPreflight{}
	seen := map[string]string{}
	for index, path := range paths {
		if !validUploadPath(path) {
			report.Problems = append(report.Problems, Problem{
				Name: fmt.Sprintf("selection %d", index+1), Kind: "source-path",
				Detail: uploadPathRefusal, Reject: true,
			})
			continue
		}
		base := filepath.Base(path)
		name := prefix + base
		info, err := os.Stat(path)
		switch {
		case err != nil:
			report.Problems = append(report.Problems, Problem{Name: name, Kind: "unreadable", Detail: err.Error(), Reject: true})
			continue
		case !info.Mode().IsRegular():
			report.Problems = append(report.Problems, Problem{Name: name, Kind: "not-a-file", Detail: path, Reject: true})
			continue
		}
		if firstPath, dup := seen[name]; dup {
			report.Problems = append(report.Problems, Problem{
				Name: name, Kind: "duplicate",
				Detail: fmt.Sprintf("%s and %s map to the same object name", firstPath, path), Reject: true,
			})
			continue
		}
		seen[name] = path

		if _, err := dst.ObjectDetail(ctx, container, name); err == nil {
			report.Problems = append(report.Problems, Problem{
				Name: name, Kind: "exists",
				Detail: "already live at the destination; v1 upload never overwrites", Reject: true,
			})
			continue
		} else if !swiftcontrol.IsNotFound(err) {
			return nil, err
		}

		route := "rclone"
		if plan.RcloneUnsafe(name) {
			route = "native"
		}
		report.Items = append(report.Items, UploadItem{Path: path, Name: name, Size: info.Size(), Route: route})
		report.TotalBytes += info.Size()
	}
	return report, nil
}

// DownloadItem is one object cleared for materialization.
type DownloadItem struct {
	Name string `json:"name"`
	// RelPath is the local path relative to the destination directory.
	RelPath string `json:"relPath"`
	Size    int64  `json:"size"`
	Route   string `json:"route"`
	// Dir marks a zero-byte folder marker materialized as a directory.
	Dir bool `json:"dir"`
}

// DownloadPreflight is the complete pre-write report.
type DownloadPreflight struct {
	Items      []DownloadItem `json:"items"`
	Problems   []Problem      `json:"problems"`
	TotalBytes int64          `json:"totalBytes"`
	Objects    int64          `json:"objects"`
}

func (p *DownloadPreflight) Rejected() bool { return rejected(p.Problems) }

const downloadPathRefusal = "the selected destination path cannot be used unchanged; select it again using an absolute UTF-8 path without dot or empty components"

func validDownloadRoot(path string) bool {
	// Joining a child cleans the root, which can redirect a path when
	// a symlink precedes '..'. Preserve the captured selection instead.
	return utf8.ValidString(path) && filepath.IsAbs(path) && filepath.Clean(path) == path
}

// PreflightDownload walks the prefix scope and decides how every name
// materializes under destDir — completely, before any write. Rejections
// (Linux target): dot-segment and leading-slash names (path escapes),
// non-empty names ending "/" (file/directory ambiguity), file-vs-prefix
// conflicts (an object `foo` alongside `foo/...`), and existing local
// files (v1 never overwrites). Case-fold and Unicode-normalization
// twins materialize fine on this target but travel badly — warned.
func PreflightDownload(ctx context.Context, src *swiftcontrol.Session, container, prefix, destDir string) (*DownloadPreflight, error) {
	report := &DownloadPreflight{}
	if !validDownloadRoot(destDir) {
		report.Problems = []Problem{{Name: "destination", Kind: "destination-path", Detail: downloadPathRefusal, Reject: true}}
		return report, nil
	}
	if prefix != "" && !strings.HasSuffix(prefix, "/") {
		return nil, fmt.Errorf("prefix %q must be empty or end with '/'", prefix)
	}
	var names []string
	folded := map[string][]string{}

	for entry, err := range src.Objects(ctx, container, swiftcontrol.ObjectListOptions{Deep: true, Prefix: prefix}) {
		if err != nil {
			return nil, err
		}
		object, ok := entry.(swiftcontrol.ObjectEntry)
		if !ok {
			continue
		}
		rel := object.Name[len(prefix):]
		names = append(names, rel)
		folded[strings.ToLower(rel)] = append(folded[strings.ToLower(rel)], rel)

		problem := func(kind, detail string) {
			report.Problems = append(report.Problems, Problem{Name: object.Name, Kind: kind, Detail: detail, Reject: true})
		}
		// Namespace-policy checks run BEFORE the directory-marker branch:
		// a folder marker named "scope/../" would otherwise become a
		// MkdirAll outside the destination (the follow-up review's
		// dir-marker escape).
		trimmed := strings.TrimSuffix(rel, "/")
		switch {
		case strings.HasPrefix(rel, "/"):
			problem("path-escape", "leading slash would escape the destination directory")
			continue
		case hasDotSegment(trimmed):
			problem("path-escape", "'.' or '..' path segments would escape or alias inside the destination directory")
			continue
		case strings.ContainsRune(rel, '\\'):
			// A backslash is a legal Swift name byte and a path separator
			// on Windows: one namespace policy for every target (§65.9
			// item 13) — reject rather than materialize differently per OS.
			problem("path-escape", "backslash in the name is a path separator on Windows; refused on every platform")
			continue
		case strings.Contains(rel, "//"):
			problem("collision", "empty path segment: the name collapses onto another path when joined locally")
			continue
		case strings.ContainsFunc(rel, func(c rune) bool { return c < 0x20 || c == 0x7f }):
			problem("unmaterializable", "control character in the name cannot ride a local path")
			continue
		}
		if rel == "" || strings.HasSuffix(rel, "/") {
			logical := object.Bytes
			if logical == 0 {
				// A zero-byte listing row may be a DLO/SLO manifest whose
				// LOGICAL size the listing hides (review F-09): resolve
				// before treating it as a folder marker, or a large
				// object could "complete" as an empty directory.
				detail, err := src.ObjectDetail(ctx, container, object.Name)
				if err != nil {
					return nil, err
				}
				logical = detail.ContentLength
			}
			if logical == 0 {
				report.Items = append(report.Items, DownloadItem{Name: object.Name, RelPath: filepath.FromSlash(trimmed), Dir: true})
				report.Objects++
				continue
			}
			problem("unmaterializable", "a non-empty object named like a directory cannot be a local file and a directory at once")
			continue
		}

		local := filepath.Join(destDir, filepath.FromSlash(rel))
		if info, err := os.Stat(local); err == nil {
			if info.IsDir() {
				problem("local-exists", filepath.FromSlash(rel)+" is an existing DIRECTORY under the destination; a file cannot land on it")
			} else {
				problem("local-exists", filepath.FromSlash(rel)+" already exists under the destination; v1 download never overwrites")
			}
			continue
		}
		size := object.Bytes
		if size == 0 {
			// A zero-byte listing row may be a DLO manifest whose logical
			// size the listing does not show (§28.4); one HEAD resolves it.
			detail, err := src.ObjectDetail(ctx, container, object.Name)
			if err != nil {
				return nil, err
			}
			size = detail.ContentLength
		}
		route := "rclone"
		if plan.RcloneUnsafe(prefix + rel) {
			route = "native"
		}
		report.Items = append(report.Items, DownloadItem{
			Name: object.Name, RelPath: filepath.FromSlash(rel), Size: size, Route: route,
		})
		report.Objects++
		report.TotalBytes += size
	}

	// File-vs-prefix conflicts: a real ancestor lookup, not sorted
	// adjacency — "foo", "foo.txt", "foo/child" sorts foo.txt between
	// the conflicting pair and adjacency misses it (follow-up review).
	fileSet := map[string]bool{}
	for _, name := range names {
		if name != "" && !strings.HasSuffix(name, "/") {
			fileSet[name] = true
		}
	}
	sort.Strings(names)
	flagged := map[string]bool{}
	for _, name := range names {
		segments := strings.Split(name, "/")
		ancestor := ""
		for _, segment := range segments[:len(segments)-1] {
			if ancestor == "" {
				ancestor = segment
			} else {
				ancestor += "/" + segment
			}
			if fileSet[ancestor] && !flagged[ancestor] {
				flagged[ancestor] = true
				report.Problems = append(report.Problems, Problem{
					Name: prefix + ancestor, Kind: "file-vs-prefix",
					Detail: fmt.Sprintf("object %q needs to be both a file and the directory holding %q", ancestor, name),
					Reject: true,
				})
			}
		}
	}
	for _, twins := range folded {
		if len(twins) > 1 {
			// Rejected, not warned: the same operator moves this data to
			// macOS/Windows next — one namespace policy per target set.
			report.Problems = append(report.Problems, Problem{
				Name: prefix + twins[0], Kind: "case-fold",
				Detail: fmt.Sprintf("%d names collide on case-insensitive filesystems: %q", len(twins), twins),
				Reject: true,
			})
		}
	}
	return report, nil
}

func hasDotSegment(rel string) bool {
	for _, segment := range strings.Split(rel, "/") {
		if segment == "." || segment == ".." {
			return true
		}
	}
	return false
}

// Engine is the same boundary the copy executor uses; local paths ride
// the src/dst specs as plain absolute paths (no "remote:" prefix).
type Engine interface {
	Copy(ctx context.Context, src, dst string, names []string, onEvent func(rclone.Event)) (*rclone.Result, error)
}

// Failure mirrors the copy executor's per-object failure shape.
type Failure struct {
	Name  string
	Stage string
	Err   string
}

// Report is the execution outcome.
type Report struct {
	Done         int64
	Bytes        int64
	NativeRouted int64
	Failures     []Failure
	// A verified copy still counts when bookkeeping or cleanup fails.
	// Bound the examples without truncating the issue count (ADR 0023).
	PostCommitIssues       int64
	PostCommitIssueSamples []Failure
	StageCleanup           *StageCleanupResult
	// CapturedPrefixes: large uploads segment like any engine write.
	CapturedPrefixes []rclone.SegmentPrefix
}

const maxFailures = 200

func (r *Report) postCommitIssue(name, stage string, err error) {
	r.PostCommitIssues++
	if len(r.PostCommitIssueSamples) < maxFailures {
		r.PostCommitIssueSamples = append(r.PostCommitIssueSamples, Failure{Name: name, Stage: stage, Err: err.Error()})
	}
}

// Download materializes a preflighted scope: directories first, engine
// batch for safe names, native streams for the rest, then a local stat
// verify per file.
func Download(ctx context.Context, src *swiftcontrol.Session, engine Engine, srcSpec, container, prefix, destDir, stageID string,
	preflight *DownloadPreflight, onProgress func(done, total, bytes int64)) (*Report, error) {
	if !validDownloadRoot(destDir) {
		return nil, errors.New(downloadPathRefusal)
	}
	if preflight == nil {
		return nil, errors.New("download needs a preflight plan")
	}
	if preflight.Rejected() {
		return nil, errors.New("preflight rejected the download; nothing was written")
	}
	if stageID == "" {
		return nil, errors.New("download needs a stage id (the job id)")
	}
	report := &Report{}
	fail := func(name, stage string, err error) {
		if len(report.Failures) < maxFailures {
			report.Failures = append(report.Failures, Failure{Name: name, Stage: stage, Err: err.Error()})
		}
	}
	total := int64(len(preflight.Items))
	progress := func() {
		if onProgress != nil {
			onProgress(report.Done+int64(len(report.Failures)), total, report.Bytes)
		}
	}

	var engineNames []string
	engineItems := map[string]DownloadItem{}
	for _, item := range preflight.Items {
		switch {
		case item.Dir:
			if err := os.MkdirAll(filepath.Join(destDir, item.RelPath), 0o755); err != nil {
				fail(item.Name, "mkdir", err)
			} else {
				report.Done++
			}
			progress()
		case item.Route == "native":
			report.NativeRouted++
			if err := nativeDownload(ctx, src, container, item, destDir); err != nil {
				fail(item.Name, "native-get", err)
			} else {
				report.Done++
				report.Bytes += item.Size
			}
			progress()
		default:
			rel := strings.TrimPrefix(item.Name, prefix)
			engineNames = append(engineNames, rel)
			engineItems[rel] = item
		}
	}

	if len(engineNames) > 0 {
		if err := ctx.Err(); err != nil {
			return report, err
		}
		// The engine writes into a job stage directory, never the live
		// destination tree: a partial or racing write cannot land on a
		// final path, and each file moves into place no-replace with an
		// exact byte check (the follow-up review's G2/G16 remainder).
		stageDir := filepath.Join(destDir, ".aeronautes-stage-"+stageID)
		if err := os.Mkdir(stageDir, 0o700); err != nil {
			return report, err
		}
		defer func() {
			report.StageCleanup = cleanupDownloadStage(stageDir, engineItems)
		}()
		result, err := copyWithAuthRetry(ctx, src, engine, srcSpec+"/"+strings.TrimSuffix(prefix, "/"), stageDir, engineNames, nil)
		if err == nil {
			err = transfercopy.ResultFailure(result)
		}
		if err != nil {
			for _, rel := range engineNames {
				fail(engineItems[rel].Name, "engine", err)
			}
			progress()
		} else {
			for _, rel := range engineNames {
				item := engineItems[rel]
				staged := filepath.Join(stageDir, item.RelPath)
				target := filepath.Join(destDir, item.RelPath)
				placed, err := placeDownloaded(staged, target, item.Size)
				if placed {
					report.Done++
					report.Bytes += item.Size
					if err != nil {
						report.postCommitIssue(item.Name, "staging-cleanup", err)
					}
				} else if err != nil {
					fail(item.Name, "place", err)
				}
				progress()
			}
		}
	}
	return report, nil
}

// placeDownloaded moves one staged download onto its final path: exact
// byte check first, then os.Link — a true no-replace landing (EEXIST if
// anything is already at the target), never a replacement-capable
// rename.
func placeDownloaded(staged, target string, wantSize int64) (bool, error) {
	info, err := os.Stat(staged)
	if err != nil {
		return false, fmt.Errorf("missing after download (silent drop?): %w", err)
	}
	if info.Size() != wantSize {
		return false, fmt.Errorf("size %d != object %d", info.Size(), wantSize)
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return false, err
	}
	// os.Link is a true no-replace landing (EEXIST if anything appears
	// at the target) — a rename would replace whatever won the race.
	// The staged copy is removed only after the link exists. (Same-
	// volume on the happy path since the stage dir sits beside the
	// target; cross-device atomicity is the §65.10 platform matrix.)
	if err := os.Link(staged, target); err != nil {
		return false, err
	}
	return true, os.Remove(staged)
}

func nativeDownload(ctx context.Context, src *swiftcontrol.Session, container string, item DownloadItem, destDir string) error {
	body, _, err := src.OpenObject(ctx, container, item.Name)
	if err != nil {
		return err
	}
	defer body.Close()
	target := filepath.Join(destDir, item.RelPath)
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	// Private temp beside the target, fsync, then a no-replace move: a
	// failed read never leaves a partial file at the final name, and
	// the byte count must match the preflight before the move.
	temp, err := os.CreateTemp(filepath.Dir(target), ".aeronautes-part-*")
	if err != nil {
		return err
	}
	defer os.Remove(temp.Name())
	written, err := io.Copy(temp, body)
	if err != nil {
		temp.Close()
		return err
	}
	if written != item.Size {
		temp.Close()
		return fmt.Errorf("wrote %d bytes, object says %d — refusing to place a short file", written, item.Size)
	}
	if err := temp.Sync(); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	// os.Link is a true no-replace landing: EEXIST if anything appears
	// at the target between the preflight and now (review F-09).
	if err := os.Link(temp.Name(), target); err != nil {
		return err
	}
	return nil
}

// copyWithAuthRetry mirrors the executor's bounded auth lifecycle for
// the local paths (review F-06 parity): one auth-shaped failure forces
// re-authentication and earns exactly one fresh child.
func copyWithAuthRetry(ctx context.Context, session *swiftcontrol.Session, engine Engine,
	src, dst string, names []string, onEvent func(rclone.Event)) (*rclone.Result, error) {
	for attempt := 0; ; attempt++ {
		result, err := engine.Copy(ctx, src, dst, names, onEvent)
		if err != nil {
			return result, err
		}
		if failure := transfercopy.ResultFailure(result); failure != nil &&
			attempt == 0 && transfercopy.IsAuthFailure(result) && ctx.Err() == nil {
			session.ForceReauth()
			continue
		}
		return result, nil
	}
}
