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
// Execution iterates Items; the bridge never returns this value (ADR 0056).
type DownloadPreflight struct {
	Items      []DownloadItem `json:"items"`
	Problems   []Problem      `json:"problems"`
	TotalBytes int64          `json:"totalBytes"`
	Objects    int64          `json:"objects"`
	// nativeSource is the Start listing/HEAD identity for native files.
	// It is not a bridge field (ADR 0060).
	nativeSource map[string]downloadSourceIdentity
}

func (p *DownloadPreflight) Rejected() bool { return rejected(p.Problems) }

// DownloadPreflightView is the bridge-facing collision report (ADR 0056).
// Objects, TotalBytes, ProblemCount and Rejected are complete.
// ItemSamples and ProblemSamples are bounded; counts are never rebuilt from them.
type DownloadPreflightView struct {
	ItemSamples    []DownloadItem `json:"itemSamples"`
	ProblemSamples []Problem      `json:"problemSamples"`
	TotalBytes     int64          `json:"totalBytes"`
	Objects        int64          `json:"objects"`
	ProblemCount   int64          `json:"problemCount"`
	Rejected       bool           `json:"rejected"`
}

const maxFailures = 200

// View copies complete counts and a bounded sample. The execution report is unchanged.
func (p *DownloadPreflight) View() DownloadPreflightView {
	view := DownloadPreflightView{ItemSamples: []DownloadItem{}, ProblemSamples: []Problem{}}
	if p == nil {
		return view
	}
	view.Objects = p.Objects
	view.TotalBytes = p.TotalBytes
	view.ProblemCount = int64(len(p.Problems))
	view.Rejected = p.Rejected()
	view.ItemSamples = sampleOf(p.Items)
	view.ProblemSamples = sampleOf(p.Problems)
	return view
}

func sampleOf[T any](in []T) []T {
	n := len(in)
	if n > maxFailures {
		n = maxFailures
	}
	return append([]T{}, in[:n]...)
}

func collisionDetail(kind string, twins []string) string {
	quoted := twins
	if len(quoted) > maxFailures {
		quoted = quoted[:maxFailures]
	}
	switch kind {
	case "name-form":
		return fmt.Sprintf("%d names collide on filesystems that ignore case or Unicode normalization form: %q", len(twins), quoted)
	default:
		return fmt.Sprintf("%d names collide on case-insensitive filesystems: %q", len(twins), quoted)
	}
}

const downloadPathRefusal = "the selected destination path cannot be used unchanged; select it again using an absolute UTF-8 path without dot or empty components"

func validDownloadRoot(path string) bool {
	// Joining a child cleans the root, which can redirect a path when
	// a symlink precedes '..'. Preserve the captured selection instead.
	return utf8.ValidString(path) && filepath.IsAbs(path) && filepath.Clean(path) == path
}

// PreflightDownload checks the complete selected namespace and existing
// destination before any write. Its portable namespace policy refuses
// case-folded conflicts even on hosts that could materialize both paths.
func PreflightDownload(ctx context.Context, src *swiftcontrol.Session, container, prefix, destDir string) (report *DownloadPreflight, err error) {
	return PreflightDownloadBounded(ctx, src, container, prefix, destDir, 0)
}

// PreflightDownloadBounded is PreflightDownload with a selection ceiling:
// once the scope's object count passes objectCeiling the selection is
// refused, naming the count reached and the ceiling, and no further
// listing page is requested; the plan is never truncated (ADR 0072
// decision 4). A ceiling of zero means unbounded.
func PreflightDownloadBounded(ctx context.Context, src *swiftcontrol.Session, container, prefix, destDir string, objectCeiling int64) (report *DownloadPreflight, err error) {
	report = &DownloadPreflight{}
	if !validDownloadRoot(destDir) {
		report.Problems = []Problem{{Name: "destination", Kind: "destination-path", Detail: downloadPathRefusal, Reject: true}}
		return report, nil
	}
	if prefix != "" && !strings.HasSuffix(prefix, "/") {
		return nil, fmt.Errorf("prefix %q must be empty or end with '/'", prefix)
	}
	destination, err := OpenDestinationRoot(destDir)
	if err != nil {
		report.Problems = []Problem{{Name: "destination", Kind: "destination-unavailable", Detail: "cannot inspect the selected destination directory: " + err.Error(), Reject: true}}
		return report, nil
	}
	defer func() { err = errors.Join(err, destination.Close()) }()
	return PreflightDownloadIntoBounded(ctx, src, container, prefix, destination, objectCeiling)
}

// PreflightDownloadInto runs the same complete report through a held
// destination handle, so the directory inspected is the directory the
// job writes into whatever its pathname names later.
func PreflightDownloadInto(ctx context.Context, src *swiftcontrol.Session, container, prefix string, destination *DestinationRoot) (report *DownloadPreflight, err error) {
	return PreflightDownloadIntoBounded(ctx, src, container, prefix, destination, 0)
}

// PreflightDownloadIntoBounded is PreflightDownloadInto with the selection
// ceiling of PreflightDownloadBounded.
func PreflightDownloadIntoBounded(ctx context.Context, src *swiftcontrol.Session, container, prefix string, destination *DestinationRoot, objectCeiling int64) (report *DownloadPreflight, err error) {
	report = &DownloadPreflight{}
	if objectCeiling < 0 {
		return nil, fmt.Errorf("object ceiling %d is negative", objectCeiling)
	}
	if prefix != "" && !strings.HasSuffix(prefix, "/") {
		return nil, fmt.Errorf("prefix %q must be empty or end with '/'", prefix)
	}
	if destination == nil {
		return nil, errors.New("download preflight needs the held destination directory")
	}
	if removed, err := destination.Removed(); err != nil {
		report.Problems = []Problem{{Name: "destination", Kind: "destination-unavailable", Detail: "cannot inspect the selected destination directory: " + err.Error(), Reject: true}}
		return report, nil
	} else if removed {
		report.Problems = []Problem{{Name: "destination", Kind: "destination-unavailable", Detail: "the selected destination directory was deleted after it was picked; pick a directory again", Reject: true}}
		return report, nil
	}
	var names []string
	var candidates []DownloadItem
	nativeSource := map[string]downloadSourceIdentity{}
	// One group per relation; a collision in either refuses the selection.
	byLower := map[string][]string{}
	byCaseless := map[string][]string{}

	var counted int64
	for entry, err := range src.Objects(ctx, container, swiftcontrol.ObjectListOptions{Deep: true, Prefix: prefix}) {
		if err != nil {
			return nil, err
		}
		object, ok := entry.(swiftcontrol.ObjectEntry)
		if !ok {
			continue
		}
		counted++
		if objectCeiling > 0 && counted > objectCeiling {
			// The selection is wider than the ceiling: refuse it with the
			// numbers, keep no plan, and stop listing here; the page that
			// crossed the ceiling is the last one requested.
			report.Items = nil
			report.Objects = 0
			report.TotalBytes = 0
			report.Problems = append(report.Problems, Problem{Name: prefix, Kind: "selection-ceiling", Reject: true,
				Detail: fmt.Sprintf("the selection holds more than %d objects, the download ceiling set in Settings (%d counted before stopping); narrow the prefix, or raise the ceiling", objectCeiling, counted)})
			return report, nil
		}
		rel := object.Name[len(prefix):]
		names = append(names, rel)
		// Files only: two directories that differ only by spelling are
		// not a collision but an alias, admitted like the same two
		// directories implied by files; a file against a directory is
		// the ancestor rule's case below.
		if rel != "" && !strings.HasSuffix(rel, "/") {
			lower, caseless := lowercaseIdentity(rel), caselessIdentity(rel)
			byLower[lower] = append(byLower[lower], rel)
			byCaseless[caseless] = append(byCaseless[caseless], rel)
		}

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
		// Portable component policy, also BEFORE the directory-marker
		// branch: a marker named "CON/" would otherwise become a MkdirAll
		// of a Windows device name.
		if refusal := portableNameRefusal(trimmed); refusal != "" {
			problem("unportable-name", refusal)
			continue
		}
		// Component length, also BEFORE the directory-marker branch and
		// independent of what exists beneath the destination: the
		// existing-destination inspection stops at the first absent
		// component, so it can never be the length rule.
		if refusal := componentLengthRefusal(trimmed); refusal != "" {
			problem("name-length", refusal)
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
		var source downloadSourceIdentity
		if size == 0 {
			// A zero-byte listing row may be a DLO manifest whose logical
			// size the listing does not show (§28.4); one HEAD resolves it.
			detail, err := src.ObjectDetail(ctx, container, object.Name)
			if err != nil {
				return nil, err
			}
			size = detail.ContentLength
			source = headSourceIdentity(detail)
		} else {
			source = listingSourceIdentity(object)
		}
		route := "rclone"
		if plan.RcloneUnsafe(prefix + rel) {
			route = "native"
		}
		candidates = append(candidates, DownloadItem{
			Name: object.Name, RelPath: filepath.FromSlash(rel), Size: size, Route: route,
		})
		nativeSource[object.Name] = source
	}

	// Directory aliases: a planned directory identity with more than one
	// spelling, from markers or from the ancestors of files. The pinned
	// engine keys directory entries by their NFC form everywhere and by
	// their lowercase form on case-insensitive hosts and ignores the
	// second spelling as a duplicate while exiting successfully, so files
	// beneath an aliased directory go natively on every platform; the
	// target filesystem then decides, as it does for rsync and scp.
	spellingsByLower, spellingsByCaseless := map[string]map[string]bool{}, map[string]map[string]bool{}
	for _, name := range names {
		for _, dir := range plannedDirectories(name) {
			noteSpelling(spellingsByLower, lowercaseIdentity(dir), dir)
			noteSpelling(spellingsByCaseless, caselessIdentity(dir), dir)
		}
	}
	for i, item := range candidates {
		if item.Dir {
			continue
		}
		for _, dir := range plannedDirectories(item.Name[len(prefix):]) {
			if len(spellingsByLower[lowercaseIdentity(dir)]) > 1 || len(spellingsByCaseless[caselessIdentity(dir)]) > 1 {
				candidates[i].Route = "native"
				break
			}
		}
	}

	// File-vs-prefix conflicts: a real ancestor lookup, not sorted
	// adjacency — "foo", "foo.txt", "foo/child" sorts foo.txt between
	// the conflicting pair and adjacency misses it (follow-up review).
	filesByLower, filesByCaseless := map[string]string{}, map[string]string{}
	for _, name := range names {
		if name != "" && !strings.HasSuffix(name, "/") {
			filesByLower[lowercaseIdentity(name)] = name
			filesByCaseless[caselessIdentity(name)] = name
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
			file, exists := filesByLower[lowercaseIdentity(ancestor)]
			if !exists {
				file, exists = filesByCaseless[caselessIdentity(ancestor)]
			}
			// Flagged by the selected file, so one file conflicting under
			// both relations is reported once.
			if exists && !flagged[file] {
				flagged[file] = true
				report.Problems = append(report.Problems, Problem{
					Name: prefix + file, Kind: "file-vs-prefix",
					Detail: fmt.Sprintf("object %q needs to be both a file and the directory holding %q", file, name),
					Reject: true,
				})
			}
		}
	}
	// Rejected, not warned: the same operator moves this data to
	// macOS/Windows next — one namespace policy per target set. The
	// lowercase groups are emitted first so an existing case collision
	// keeps its exact kind and wording; a group the second relation finds
	// on its own is reported as a name-form collision.
	reported := map[string]bool{}
	emit := func(twins []string, kind, detail string) {
		if len(twins) < 2 {
			return
		}
		key := strings.Join(twins, "\x00")
		if reported[key] {
			return
		}
		reported[key] = true
		report.Problems = append(report.Problems, Problem{
			Name: prefix + twins[0], Kind: kind, Detail: detail, Reject: true,
		})
	}
	for _, twins := range byLower {
		emit(twins, "case-fold", collisionDetail("case-fold", twins))
	}
	for _, twins := range byCaseless {
		emit(twins, "name-form", collisionDetail("name-form", twins))
	}
	for _, item := range candidates {
		if refusal := inspectDownloadPath(destination.root, item); refusal != nil {
			report.Problems = append(report.Problems, *refusal)
			continue
		}
		report.Items = append(report.Items, item)
		report.Objects++
		report.TotalBytes += item.Size
	}
	report.nativeSource = nativeSource
	return report, nil
}

// plannedDirectories lists every directory a relative name plans, the
// marker's own directory included, shallowest first.
func plannedDirectories(rel string) []string {
	trimmed := strings.TrimSuffix(rel, "/")
	var dirs []string
	for i, c := range trimmed {
		if c == '/' {
			dirs = append(dirs, trimmed[:i])
		}
	}
	if rel != "" && strings.HasSuffix(rel, "/") {
		dirs = append(dirs, trimmed)
	}
	return dirs
}

func noteSpelling(spellings map[string]map[string]bool, identity, spelling string) {
	if spellings[identity] == nil {
		spellings[identity] = map[string]bool{}
	}
	spellings[identity][spelling] = true
}

func hasDotSegment(rel string) bool {
	for _, segment := range strings.Split(rel, "/") {
		if segment == "." || segment == ".." {
			return true
		}
	}
	return false
}

// portableNameRefusal reports why a selected name below the prefix cannot be
// materialized on every supported target, or "" when every component is
// admissible. One namespace policy covers all six targets (§65.9 item 13):
// Windows device names, the characters it forbids and components ending in
// an ASCII space or dot are refused everywhere rather than materialized
// differently per platform. The original bytes are never trimmed, encoded
// or renamed; refusal carries the exact component.
func portableNameRefusal(rel string) string {
	for _, component := range strings.Split(rel, "/") {
		switch {
		case component == "":
			continue
		case reservedDeviceBase(component):
			return fmt.Sprintf("path component %q is a reserved device name on Windows; refused on every platform", component)
		case strings.ContainsAny(component, `<>:"|?*`):
			return fmt.Sprintf("path component %q holds a character Windows forbids in a name; refused on every platform", component)
		case strings.HasSuffix(component, " ") || strings.HasSuffix(component, "."):
			return fmt.Sprintf("path component %q ends with a space or dot, which Windows does not preserve; refused on every platform", component)
		}
	}
	return ""
}

// reservedDeviceBase applies the documented device families to one path
// component: the base is what precedes the first '.' or ':' without its
// trailing ASCII spaces. Since Windows 11 a device name with an extension is
// no longer reserved and the runtime asks the OS instead of deciding
// statically, so this rule keeps those forms refused for every target
// (2026-09-10 portable-name experiment). Folding is ASCII-only, exactly as
// the pinned runtime compares these families.
func reservedDeviceBase(component string) bool {
	base := component
	if cut := strings.IndexAny(base, ".:"); cut >= 0 {
		base = base[:cut]
	}
	base = strings.TrimRight(base, " ")
	switch asciiUpper(base) {
	case "CON", "PRN", "AUX", "NUL", "CONIN$", "CONOUT$":
		return true
	}
	if len(base) < 4 {
		return false
	}
	switch asciiUpper(base[:3]) {
	case "COM", "LPT":
		// Superscript ¹, ² and ³ are device digits as well.
		return len(base) == 4 && '1' <= base[3] && base[3] <= '9' ||
			base[3:] == "¹" || base[3:] == "²" || base[3:] == "³"
	}
	return false
}

// maxComponentBytes is the strictest per-name limit among the supported
// targets for UTF-8 input: ext4, XFS, btrfs and APFS limit a name to 255
// bytes, and NTFS to 255 UTF-16 code units, which a UTF-8 sequence never
// needs fewer bytes than. One rule on every platform, measured in bytes
// of the exact name, refuses before any write what every target would
// refuse at write time; the host's own refusal stays as the backstop.
const maxComponentBytes = 255

// componentLengthRefusal reports why a component of rel is too long for
// every supported target, or "" when every component fits. Like the
// portable rule it inspects the exact bytes and never trims or renames.
func componentLengthRefusal(rel string) string {
	for _, component := range strings.Split(rel, "/") {
		if len(component) > maxComponentBytes {
			return fmt.Sprintf("path component of %d bytes exceeds the %d-byte name limit of the supported filesystems; refused on every platform", len(component), maxComponentBytes)
		}
	}
	return ""
}

func asciiUpper(name string) string {
	folded := []byte(name)
	for index, letter := range folded {
		if 'a' <= letter && letter <= 'z' {
			folded[index] = letter - 'a' + 'A'
		}
	}
	return string(folded)
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
	// Cancelled marks a failure that is the cancellation itself, so a
	// caller can tell a job that was only cancelled from one that failed.
	Cancelled bool
}

// Report is the execution outcome.
type Report struct {
	Done int64
	// Later publication failures reduce verification without erasing
	// the copied objects and bytes that were already witnessed.
	Verified     int64
	Bytes        int64
	NativeRouted int64
	Failures     []Failure
	// A verified copy still counts when bookkeeping or cleanup fails.
	// Bound the examples without truncating the issue count (ADR 0023).
	PostCommitIssues       int64
	PostCommitIssueSamples []Failure
	StageCleanup           *StageCleanupResult
	// RootDisplacement records when the captured pathname stopped naming
	// the held destination; effects continued through the handle.
	RootDisplacement string
	// CreatedDirectories counts the directories the ancestor walks made
	// inside the picked directory for items that did not copy, from
	// observed creation rather than from a directory existing afterwards
	// (ADR 0043). Those are residue: a job that stopped leaves them
	// behind and the operator is told what now exists that did not
	// before. An item that copied is not counted, because its
	// directories are the intended outcome and its own record carries
	// the placed file. Bound the examples without truncating the count.
	CreatedDirectories      int64
	CreatedDirectorySamples []string
	// CapturedPrefixes: large uploads segment like any engine write.
	CapturedPrefixes []rclone.SegmentPrefix
}

// createdDirectories records observed ancestor creation. Callers pass
// what the walk actually made, so a pre-existing or raced directory
// never arrives here.
func (r *Report) createdDirectories(created []string) {
	for _, dir := range created {
		r.CreatedDirectories++
		if len(r.CreatedDirectorySamples) < maxFailures {
			r.CreatedDirectorySamples = append(r.CreatedDirectorySamples, dir)
		}
	}
}

func (r *Report) postCommitIssue(name, stage string, err error) {
	r.PostCommitIssues++
	if len(r.PostCommitIssueSamples) < maxFailures {
		r.PostCommitIssueSamples = append(r.PostCommitIssueSamples, Failure{Name: name, Stage: stage, Err: err.Error()})
	}
}
