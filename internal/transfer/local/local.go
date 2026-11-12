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
	// Name is the destination object name: the prefix and the name its
	// source carries, a base name for a file pick and the folder with the
	// file's relative path for a folder pick.
	Name  string `json:"name"`
	Size  int64  `json:"size"`
	Route string `json:"route"` // "rclone" | "native"
	// Replaces marks a name the preflight found live and admitted anyway,
	// because the caller had the operator's acknowledgement for it
	// (ADR 0081 decision 1). Only PreflightUploadSourcesReplacing sets it, and
	// only the publication of such a name may drop its conditional create.
	Replaces bool `json:"replaces,omitempty"`
}

// UploadPreflight is the complete pre-write report.
type UploadPreflight struct {
	Items      []UploadItem `json:"items"`
	Problems   []Problem    `json:"problems"`
	TotalBytes int64        `json:"totalBytes"`
	// LargeObjectsAsDLO discloses that the selection contains a file the
	// engine will upload as a segmented destination DLO (§63.8), so the
	// local dialog can name the placement the copy preview already names.
	// The app-layer bridge fills it from the effective --swift-chunk-size;
	// this package, which never resolves that size, leaves it false.
	LargeObjectsAsDLO bool `json:"largeObjectsAsDlo"`
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

// PreflightUploadSources maps a selection onto container/prefix and reports
// every problem before anything is written: unreadable or non-regular paths,
// duplicate names in the selection, and names already live at the destination,
// which it refuses. Each source names its own destination under the prefix
// and the name is taken as given, never derived from the path: a file pick
// names its files by base name (SourcesByBaseName), a folder pick by the
// folder and the relative path (ADR 0103 decision 4). The engine writes only
// into the job staging namespace and each name commits natively with a
// conditional create (§28.6, Upload).
func PreflightUploadSources(ctx context.Context, dst *swiftcontrol.Session, container, prefix string, sources []UploadSource) (*UploadPreflight, error) {
	return preflightUpload(ctx, dst, container, prefix, sources, false)
}

// PreflightUploadSourcesReplacing is the same report for a caller that holds
// the operator's acknowledgement of an overwrite (ADR 0081 decision 1): a name
// already live becomes an item to replace rather than a rejecting problem,
// and is still reported so the operator sees what will be replaced.
//
// It is a separate entry point rather than a parameter on purpose. Every
// other caller keeps the behaviour it had, and none of them can reach the
// replacing behaviour by passing a value: reaching it means naming it, and
// the only caller that names it is the admission that raised the
// confirmation.
func PreflightUploadSourcesReplacing(ctx context.Context, dst *swiftcontrol.Session, container, prefix string, sources []UploadSource) (*UploadPreflight, error) {
	return preflightUpload(ctx, dst, container, prefix, sources, true)
}

// SourcesByBaseName is the file pick's rule, stated once: each path is named
// by its base name under the prefix.
func SourcesByBaseName(paths []string) []UploadSource {
	sources := make([]UploadSource, len(paths))
	for i, p := range paths {
		sources[i] = UploadSource{Path: p, Name: filepath.Base(p)}
	}
	return sources
}

// listingPageSize is the page size this route asks the store for, kept here
// so the estimate below can count requests rather than guess them.
const listingPageSize = 10000

// livenessListingFloor is the largest selection this resolution settles by
// name without listing at all. A listing pass costs at least one page and the
// empty one that ends it, so below this it cannot win.
const livenessListingFloor = 2

// destinationLiveness reports which of these names the destination already
// holds, choosing between one HEAD per name and listing the prefix by what
// each actually COSTS IN REQUESTS.
//
// The container's object count was the old input, and it is only an upper
// bound for the prefix inside it, so a small prefix in a large container
// looked expensive to list and the selection paid one HEAD per name
// (K-PERF-001: a thousand files must not be a thousand requests before a byte
// is written). Rows are the wrong unit too: the client has already fetched and
// validated a whole response before it yields the first row, so abandoning
// part way through one throws away answers already paid for.
//
// So this reads the listing a RESPONSE at a time and decides at page
// boundaries:
//
//   - A selection at or below livenessListingFloor is settled by name. A pass
//     costs at least one page and the empty one that ends it, so listing
//     cannot win.
//   - Otherwise every row of every page is consumed, because those rows cost
//     nothing once their response has arrived.
//   - After each complete page the pass stops when ONE MORE REQUEST would cost
//     at least as much as asking for the names still open: a HEAD each settles
//     them for certain, while another page settles them only if they happen to
//     be in it. That bounds the whole resolution at about twice the selection
//     even against a server that answers one row at a time, and it costs two
//     requests whenever the directory it lists fits in a response or two —
//     which is the ordinary case.
//   - Nothing is concluded from the SHAPE of a page. A full one does not prove
//     another follows and a short one does not prove the listing is finished
//     (ADR 0021); only the empty page the iterator consumes means exhaustion.
//     Stopping early is a deliberate choice about cost, never a claim about
//     what is left.
//
// Stopping is never read as exhaustion, nothing is resumed and no second
// listing is issued, so no row is read twice and none is skipped. Every hit a
// stopped pass found is kept, and only the names it did not settle are asked
// for by name.
//
// A listing is the weaker ORACLE, in both directions, and both are
// conservative here. It can omit an object a HEAD would find, because
// container listings lag writes; a miss leaves the publication carrying its
// conditional, so the store refuses it and the item is a conflict rather than
// an unasked-for replacement. It can also name an object that has since been
// deleted, because listings lag deletes too (DESIGN section 15.3); that name
// is then reported as a collision that no longer exists, which refuses an
// unacknowledged upload that would have succeeded and marks an acknowledged
// one as replacing something that is gone. The second case publishes rather
// than destroys, and the backend still confirms what it replaced, so neither
// error can turn into an unconfirmed overwrite.
//
// Not knowing is never free: a listing that stops, or a HEAD that fails for
// any reason other than not-found, refuses the preflight.
func destinationLiveness(ctx context.Context, dst *swiftcontrol.Session, container, prefix string,
	names []string) (map[string]bool, error) {
	wanted := make(map[string]bool, len(names))
	for _, name := range names {
		wanted[name] = false
	}
	if len(wanted) == 0 {
		return wanted, nil
	}
	unsettled := func() int {
		open := 0
		for _, live := range wanted {
			if !live {
				open++
			}
		}
		return open
	}
	exhausted := false
	if len(wanted) > livenessListingFloor {
		exhausted = true
		spent := 0
		// A folder pick's names all sit under the folder, so the pass lists
		// the tightest directory that holds every candidate. Listing the
		// request's prefix instead would read whatever else it holds, which
		// can be the whole container, before the budget below gave up.
		scope := prefix
		if common := commonDirectory(names); strings.HasPrefix(common, prefix) {
			scope = common
		}
		// Deep, which is the stream jobs consume, and load-bearing since the
		// folder pick: its names carry further slashes, which the delimiter
		// view groups into rows that name no object, and this would then
		// silently find no collisions.
		for page, err := range dst.ObjectPages(ctx, container, swiftcontrol.ObjectListOptions{
			Prefix: scope, PageSize: listingPageSize, Deep: true}) {
			if err != nil {
				// A listing that stopped says nothing about the names it never
				// reached, and treating those as free would admit a
				// replacement the operator was never shown. Refuse the
				// preflight instead.
				return nil, fmt.Errorf("the destination could not be listed, so a collision cannot be ruled out: %w", err)
			}
			for _, entry := range page.Entries {
				object, ok := entry.(swiftcontrol.ObjectEntry)
				if !ok {
					continue
				}
				if _, candidate := wanted[object.Name]; candidate {
					wanted[object.Name] = true
				}
			}
			spent++
			if spent+1 >= unsettled() {
				// One more request would cost at least what the names still
				// open cost to ask for directly, and it might settle none of
				// them. Stop and ask. This is a decision about cost: nothing
				// is being claimed about how much of the prefix is left.
				exhausted = false
				break
			}
		}
	}
	if exhausted {
		// The prefix was enumerated whole. Absence here is the listing's
		// answer, which a create still publishes conditionally, exactly as
		// before.
		return wanted, nil
	}
	for name, live := range wanted {
		if live {
			continue
		}
		switch _, err := dst.ObjectDetail(ctx, container, name); {
		case err == nil:
			wanted[name] = true
		case !swiftcontrol.IsNotFound(err):
			// Not knowing is not the same as free. This refusal is the one the
			// per-file loop used to carry.
			return nil, err
		}
	}
	return wanted, nil
}

// commonDirectory is the longest prefix every name shares that ends at a
// slash, or "" when they share none. A slash byte never falls inside a
// multi-byte character, so the cut is always a whole name prefix.
func commonDirectory(names []string) string {
	if len(names) == 0 {
		return ""
	}
	common := names[0]
	for _, name := range names[1:] {
		shared := 0
		for shared < len(common) && shared < len(name) && common[shared] == name[shared] {
			shared++
		}
		common = common[:shared]
	}
	return common[:strings.LastIndexByte(common, '/')+1]
}

// uploadCandidate is one selected path after everything decidable WITHOUT
// the destination has been decided. A path that cannot become an item
// carries its problem and nothing else, so no destination request is ever
// made on its behalf and its local finding survives a remote failure.
type uploadCandidate struct {
	path    string
	name    string
	size    int64
	problem *Problem
}

func preflightUpload(ctx context.Context, dst *swiftcontrol.Session, container, prefix string, sources []UploadSource,
	replacing bool) (*UploadPreflight, error) {
	if prefix != "" && !strings.HasSuffix(prefix, "/") {
		return nil, fmt.Errorf("prefix %q must be empty or end with '/'", prefix)
	}
	report := &UploadPreflight{}

	// Everything local first, in the selection's own order, because a
	// missing file or a directory is this report's finding and must not be
	// replaced by a destination failure on a name that could never have
	// become an item.
	candidates := make([]uploadCandidate, 0, len(sources))
	seen := map[string]string{}
	for index, source := range sources {
		path := source.Path
		// Between candidates, before any further filesystem call. Deciding
		// everything local first removed the context-aware request that used
		// to end this loop early, so a cancelled start would otherwise go on
		// stating every remaining path — and a large selection on a slow
		// mount is exactly what this change exists to support. The job
		// runner's shutdown cancels this context and waits for its owner.
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if !validUploadPath(path) {
			candidates = append(candidates, uploadCandidate{path: path, problem: &Problem{
				Name: fmt.Sprintf("selection %d", index+1), Kind: "source-path",
				Detail: uploadPathRefusal, Reject: true,
			}})
			continue
		}
		if source.Name == "" || strings.HasPrefix(source.Name, "/") || strings.Contains(source.Name, "//") ||
			source.Name[strings.LastIndexByte(source.Name, '/')+1:] != filepath.Base(path) {
			candidates = append(candidates, uploadCandidate{path: path, problem: &Problem{
				Name: fmt.Sprintf("selection %d", index+1), Kind: "source-name",
				Detail: "the selection names a destination this upload cannot take: empty, absolute, with an empty component, or not ending in the file's own name", Reject: true,
			}})
			continue
		}
		name := prefix + source.Name
		// The staging namespace is not a destination (ADR 0105 decision 3):
		// a name there is, or will be, a job's unpublished copy, and an
		// upload that replaced one would go on to reclaim its segments.
		if rclone.InStageNamespace(name) {
			candidates = append(candidates, uploadCandidate{path: path, name: name, problem: &Problem{
				Name: name, Kind: "staging",
				Detail: "the name is in this application's staging area (" + rclone.StageNamespace + "), which is not a destination", Reject: true}})
			continue
		}
		info, err := os.Stat(path)
		switch {
		case err != nil:
			candidates = append(candidates, uploadCandidate{path: path, name: name, problem: &Problem{
				Name: name, Kind: "unreadable", Detail: err.Error(), Reject: true}})
			continue
		case !info.Mode().IsRegular():
			candidates = append(candidates, uploadCandidate{path: path, name: name, problem: &Problem{
				Name: name, Kind: "not-a-file", Detail: path, Reject: true}})
			continue
		}
		if firstPath, dup := seen[name]; dup {
			candidates = append(candidates, uploadCandidate{path: path, name: name, problem: &Problem{
				Name: name, Kind: "duplicate",
				Detail: fmt.Sprintf("%s and %s map to the same object name", firstPath, path), Reject: true,
			}})
			continue
		}
		seen[name] = path
		candidates = append(candidates, uploadCandidate{path: path, name: name, size: info.Size()})
	}

	// Then the destination, once, for the names that could still become
	// items: a thousand files were a thousand requests before anything was
	// written (K-PERF-001).
	wanted := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		if candidate.problem == nil {
			wanted = append(wanted, candidate.name)
		}
	}
	liveNames, err := destinationLiveness(ctx, dst, container, prefix, wanted)
	if err != nil {
		return nil, err
	}

	for _, candidate := range candidates {
		if candidate.problem != nil {
			report.Problems = append(report.Problems, *candidate.problem)
			continue
		}
		live := false
		if liveNames[candidate.name] {
			if !replacing {
				report.Problems = append(report.Problems, Problem{
					Name: candidate.name, Kind: "exists",
					Detail: "already live at the destination; an upload replaces nothing unless you ask it to", Reject: true,
				})
				continue
			}
			// Admitted, and still reported: the operator acknowledged
			// replacing what is there, so the collision is what they asked
			// about rather than a reason to stop (ADR 0081 decision 1).
			live = true
			report.Problems = append(report.Problems, Problem{
				Name: candidate.name, Kind: "exists",
				Detail: "already live at the destination; this upload will replace it",
			})
		}

		route := "rclone"
		if plan.RcloneUnsafe(candidate.name) {
			route = "native"
		}
		report.Items = append(report.Items, UploadItem{
			Path: candidate.path, Name: candidate.name, Size: candidate.size, Route: route, Replaces: live})
		report.TotalBytes += candidate.size
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
	return PreflightDownloadMembersInto(ctx, src, container, prefix, destination, objectCeiling, nil)
}

// PreflightDownloadMembersInto is PreflightDownloadIntoBounded for the named
// members of the scope alone (ADR 0108): the listing keeps only them, the
// ceiling and every check count only them, and a member the listing does not
// hold refuses the preflight, since it is the last check before the start.
// No members means the whole scope.
func PreflightDownloadMembersInto(ctx context.Context, src *swiftcontrol.Session, container, prefix string, destination *DestinationRoot, objectCeiling int64, members []string) (report *DownloadPreflight, err error) {
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

	var memberSet map[string]bool
	if len(members) > 0 {
		memberSet = make(map[string]bool, len(members))
		for _, name := range members {
			memberSet[name] = false
		}
	}

	var counted int64
	for entry, err := range src.Objects(ctx, container, swiftcontrol.ObjectListOptions{Deep: true, Prefix: prefix}) {
		if err != nil {
			return nil, err
		}
		object, ok := entry.(swiftcontrol.ObjectEntry)
		if !ok {
			continue
		}
		if memberSet != nil {
			if _, member := memberSet[object.Name]; !member {
				continue
			}
			memberSet[object.Name] = true
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

	if missing := missingMember(members, memberSet); missing != "" {
		report.Problems = append(report.Problems, Problem{Name: missing, Kind: "selection-missing", Reject: true,
			Detail: "this selected object is no longer at the source; select the objects again"})
	}

	routeAliasedDirectoriesNatively(prefix, names, candidates)
	report.Problems = append(report.Problems, fileVersusPrefixProblems(prefix, names)...)
	report.Problems = append(report.Problems, collisionProblems(prefix, byLower, byCaseless)...)
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

// missingMember is the first member, in the order given, that the listing
// did not hold, or "" when it held them all.
func missingMember(members []string, seen map[string]bool) string {
	for _, name := range members {
		if !seen[name] {
			return name
		}
	}
	return ""
}

// routeAliasedDirectoriesNatively sends every file beneath a directory that
// the selection spells more than one way down the native route. The pinned
// engine keys directory entries by their NFC form everywhere and by their
// lowercase form on case-insensitive hosts, and ignores the second spelling
// as a duplicate while exiting successfully; natively, the target filesystem
// decides, as it does for rsync and scp. Directory spellings come from
// markers and from the ancestors of files alike.
func routeAliasedDirectoriesNatively(prefix string, names []string, candidates []DownloadItem) {
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
}

// fileVersusPrefixProblems refuses a selection in which one object must be
// both a file and the directory holding another, found by a real ancestor
// lookup rather than sorted adjacency: "foo", "foo.txt", "foo/child" sorts
// foo.txt between the conflicting pair and adjacency misses it.
func fileVersusPrefixProblems(prefix string, names []string) []Problem {
	var problems []Problem
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
				problems = append(problems, Problem{
					Name: prefix + file, Kind: "file-vs-prefix",
					Detail: fmt.Sprintf("object %q needs to be both a file and the directory holding %q", file, name),
					Reject: true,
				})
			}
		}
	}
	return problems
}

// collisionProblems refuses file names that collide on case or on Unicode
// normalization form. Rejected, not warned: the same operator moves this
// data to macOS or Windows next, so one namespace policy covers every
// target. The lowercase groups are emitted first so an existing case
// collision keeps its exact kind and wording; a group the second relation
// finds on its own is reported as a name-form collision.
func collisionProblems(prefix string, byLower, byCaseless map[string][]string) []Problem {
	var problems []Problem
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
		problems = append(problems, Problem{
			Name: prefix + twins[0], Kind: kind, Detail: detail, Reject: true,
		})
	}
	for _, twins := range byLower {
		emit(twins, "case-fold", collisionDetail("case-fold", twins))
	}
	for _, twins := range byCaseless {
		emit(twins, "name-form", collisionDetail("name-form", twins))
	}
	return problems
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
	// Replaced counts the objects this run published over a name that was
	// already live, which only an acknowledged overwrite admits. A job
	// that replaced objects reporting none of them would be a counter
	// that lies by omission (ADR 0081 decision 5).
	Replaced int64
	Failures []Failure
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
