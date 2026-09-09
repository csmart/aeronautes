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
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/csmart/aeronautes/internal/swiftcontrol"
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
func PreflightDownload(ctx context.Context, src *swiftcontrol.Session, container, prefix, destDir string) (report *DownloadPreflight, err error) {
	report = &DownloadPreflight{}
	if !validDownloadRoot(destDir) {
		report.Problems = []Problem{{Name: "destination", Kind: "destination-path", Detail: downloadPathRefusal, Reject: true}}
		return report, nil
	}
	if prefix != "" && !strings.HasSuffix(prefix, "/") {
		return nil, fmt.Errorf("prefix %q must be empty or end with '/'", prefix)
	}
	destination, err := openDownloadDestination(destDir)
	if err != nil {
		report.Problems = []Problem{{Name: "destination", Kind: "destination-unavailable", Detail: "cannot inspect the selected destination directory: " + err.Error(), Reject: true}}
		return report, nil
	}
	defer func() { err = errors.Join(err, destination.Close()) }()
	var names []string
	var candidates []DownloadItem
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
				candidates = append(candidates, DownloadItem{Name: object.Name, RelPath: filepath.FromSlash(trimmed), Dir: true})
				continue
			}
			problem("unmaterializable", "a non-empty object named like a directory cannot be a local file and a directory at once")
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
		candidates = append(candidates, DownloadItem{
			Name: object.Name, RelPath: filepath.FromSlash(rel), Size: size, Route: route,
		})
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
	for _, item := range candidates {
		if refusal := inspectDownloadPath(destination, item); refusal != nil {
			report.Problems = append(report.Problems, *refusal)
			continue
		}
		report.Items = append(report.Items, item)
		report.Objects++
		report.TotalBytes += item.Size
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
