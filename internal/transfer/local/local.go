// Package local is the local↔Swift boundary: multi-file upload and
// prefix-scoped download, each behind the §27.5 rule — a COMPLETE
// collision preflight before any write, and the v1 policy on a
// collision is to reject the whole operation, reported fully. The
// engine (rclone) moves safe names; identities it cannot carry stream
// natively. v1 targets the filesystem the app runs on (Linux first);
// portability hazards for other OS families are reported as warnings,
// not silent surprises.
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

// PreflightUpload maps the selected files onto container/prefix and
// reports every problem before anything is written: unreadable or
// non-regular paths, duplicate basenames in the selection, and names
// already live at the destination (v1 refuses upload-overwrites — the
// engine must only ever write planned-new names, §28.6(a), and upload
// has no stage-and-commit path yet).
func PreflightUpload(ctx context.Context, dst *swiftcontrol.Session, container, prefix string, paths []string) (*UploadPreflight, error) {
	if prefix != "" && !strings.HasSuffix(prefix, "/") {
		return nil, fmt.Errorf("prefix %q must be empty or end with '/'", prefix)
	}
	report := &UploadPreflight{}
	seen := map[string]string{}
	for _, path := range paths {
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

// PreflightDownload walks the prefix scope and decides how every name
// materializes under destDir — completely, before any write. Rejections
// (Linux target): dot-segment and leading-slash names (path escapes),
// non-empty names ending "/" (file/directory ambiguity), file-vs-prefix
// conflicts (an object `foo` alongside `foo/...`), and existing local
// files (v1 never overwrites). Case-fold and Unicode-normalization
// twins materialize fine on this target but travel badly — warned.
func PreflightDownload(ctx context.Context, src *swiftcontrol.Session, container, prefix, destDir string) (*DownloadPreflight, error) {
	if prefix != "" && !strings.HasSuffix(prefix, "/") {
		return nil, fmt.Errorf("prefix %q must be empty or end with '/'", prefix)
	}
	report := &DownloadPreflight{}
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
		switch {
		case rel == "" || strings.HasSuffix(rel, "/"):
			if object.Bytes == 0 {
				report.Items = append(report.Items, DownloadItem{Name: object.Name, RelPath: filepath.FromSlash(strings.TrimSuffix(rel, "/")), Dir: true})
				report.Objects++
				continue
			}
			problem("unmaterializable", "a non-empty object named like a directory cannot be a local file and a directory at once")
			continue
		case strings.HasPrefix(rel, "/"):
			problem("path-escape", "leading slash would escape the destination directory")
			continue
		case hasDotSegment(rel):
			problem("path-escape", "'.' or '..' path segments would escape or alias inside the destination directory")
			continue
		}

		local := filepath.Join(destDir, filepath.FromSlash(rel))
		if info, err := os.Stat(local); err == nil && !info.IsDir() {
			problem("local-exists", local+" already exists; v1 download never overwrites")
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
		report.TotalBytes += object.Bytes
	}

	// File-vs-prefix conflicts: sorted adjacency makes them cheap.
	sort.Strings(names)
	for i := 0; i+1 < len(names); i++ {
		if names[i] != "" && !strings.HasSuffix(names[i], "/") && strings.HasPrefix(names[i+1], names[i]+"/") {
			report.Problems = append(report.Problems, Problem{
				Name: prefix + names[i], Kind: "file-vs-prefix",
				Detail: fmt.Sprintf("object %q needs to be both a file and the directory holding %q", names[i], names[i+1]),
				Reject: true,
			})
		}
	}
	for _, twins := range folded {
		if len(twins) > 1 {
			report.Problems = append(report.Problems, Problem{
				Name: prefix + twins[0], Kind: "case-fold",
				Detail: fmt.Sprintf("%d names collide on case-insensitive filesystems: %q", len(twins), twins),
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
	// CapturedPrefixes: large uploads segment like any engine write.
	CapturedPrefixes []rclone.SegmentPrefix
}

const maxFailures = 200

// Upload runs a preflighted upload: engine-safe files batch per parent
// directory (rclone copies FROM a directory), native ones stream, and
// every name is verified by destination HEAD — never the engine's exit
// code.
func Upload(ctx context.Context, dst *swiftcontrol.Session, engine Engine, dstSpec, container, prefix string,
	preflight *UploadPreflight, onProgress func(done, total, bytes int64)) (*Report, error) {
	if preflight.Rejected() {
		return nil, errors.New("preflight rejected the upload; nothing was written")
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

	byDir := map[string][]UploadItem{}
	for _, item := range preflight.Items {
		if item.Route == "native" {
			report.NativeRouted++
			if err := nativeUpload(ctx, dst, container, item); err != nil {
				fail(item.Name, "native-put", err)
			} else {
				report.Done++
				report.Bytes += item.Size
			}
			progress()
			continue
		}
		dir := filepath.Dir(item.Path)
		byDir[dir] = append(byDir[dir], item)
	}

	for dir, items := range byDir {
		if err := ctx.Err(); err != nil {
			return report, err
		}
		var names []string
		for _, item := range items {
			names = append(names, filepath.Base(item.Path))
		}
		result, err := engine.Copy(ctx, dir, dstSpec+"/"+strings.TrimSuffix(prefix, "/"), names, nil)
		if result != nil {
			report.CapturedPrefixes = append(report.CapturedPrefixes, result.SegmentPrefixes...)
		}
		if err != nil {
			for _, item := range items {
				fail(item.Name, "engine", err)
			}
			progress()
			continue
		}
		for _, item := range items {
			detail, err := dst.ObjectDetail(ctx, container, item.Name)
			switch {
			case err != nil:
				fail(item.Name, "verify", fmt.Errorf("missing after upload: %w", err))
			case detail.ContentLength != item.Size:
				fail(item.Name, "verify", fmt.Errorf("size %d != local %d", detail.ContentLength, item.Size))
			default:
				report.Done++
				report.Bytes += item.Size
			}
			progress()
		}
	}
	return report, nil
}

func nativeUpload(ctx context.Context, dst *swiftcontrol.Session, container string, item UploadItem) error {
	file, err := os.Open(item.Path)
	if err != nil {
		return err
	}
	defer file.Close()
	_, err = dst.PutObject(ctx, container, item.Name, file, "application/octet-stream", nil)
	return err
}

// Download materializes a preflighted scope: directories first, engine
// batch for safe names, native streams for the rest, then a local stat
// verify per file.
func Download(ctx context.Context, src *swiftcontrol.Session, engine Engine, srcSpec, container, prefix, destDir string,
	preflight *DownloadPreflight, onProgress func(done, total, bytes int64)) (*Report, error) {
	if preflight.Rejected() {
		return nil, errors.New("preflight rejected the download; nothing was written")
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
		result, err := engine.Copy(ctx, srcSpec+"/"+strings.TrimSuffix(prefix, "/"), destDir, engineNames, nil)
		_ = result
		if err != nil {
			for _, rel := range engineNames {
				fail(engineItems[rel].Name, "engine", err)
			}
			progress()
		} else {
			for _, rel := range engineNames {
				item := engineItems[rel]
				info, statErr := os.Stat(filepath.Join(destDir, item.RelPath))
				switch {
				case statErr != nil:
					fail(item.Name, "verify", fmt.Errorf("missing after download (silent drop?): %w", statErr))
				case info.Size() != item.Size:
					fail(item.Name, "verify", fmt.Errorf("size %d != object %d", info.Size(), item.Size))
				default:
					report.Done++
					report.Bytes += item.Size
				}
				progress()
			}
		}
	}
	return report, nil
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
	// O_EXCL enforces the preflight's no-overwrite promise at write time.
	file, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return err
	}
	if _, err := io.Copy(file, body); err != nil {
		file.Close()
		return err
	}
	return file.Close()
}
