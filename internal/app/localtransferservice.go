package app

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"os"
	"strings"

	"github.com/csmart/aeronautes/internal/applog"
	"github.com/csmart/aeronautes/internal/audit"
	"github.com/csmart/aeronautes/internal/jobs"
	"github.com/csmart/aeronautes/internal/swiftcontrol"
	transfercopy "github.com/csmart/aeronautes/internal/transfer/copy"
	"github.com/csmart/aeronautes/internal/transfer/local"
	"github.com/csmart/aeronautes/internal/transfer/rclone"
)

// The local↔Swift half of TransferService (§27.5): preflight is a
// separate, complete, read-only step the UI must show before Start; the
// v1 policy rejects the whole operation on any collision.

// UploadRequestDTO scopes a multi-file upload. Filesystem paths NEVER
// cross the bridge (ADR 0011): the webview holds an opaque selection
// token from the Go-side picker and the service resolves it here.
type UploadRequestDTO struct {
	BookmarkID string `json:"bookmarkId"`
	Container  string `json:"container"`
	Prefix     string `json:"prefix"`
	// SelectionToken names a picker-issued file selection.
	SelectionToken string `json:"selectionToken"`
	// Profile is the §40 performance mode ("" = balanced), as on a copy.
	Profile string `json:"profile"`
	// Settings are the per-job §44.1 overrides.
	Settings TransferSettingsDTO `json:"settings"`
	// AcknowledgeOverwrite says the operator asked for an upload that may
	// replace what is already at these names (ADR 0081 decision 1).
	// Without it this route behaves exactly as it did: the preflight
	// refuses a live name and a collision at commit is a conflict.
	// It guards against an accident, not against a hostile page, which
	// can set it itself. What guards against that is decision 2's native
	// confirmation, which the backend raises and the page cannot answer.
	AcknowledgeOverwrite bool `json:"acknowledgeOverwrite"`
}

// DownloadRequestDTO scopes a prefix download; DestDirToken is the
// picker-issued directory handle (paths never cross the bridge).
type DownloadRequestDTO struct {
	BookmarkID string `json:"bookmarkId"`
	Container  string `json:"container"`
	Prefix     string `json:"prefix"`
	// DestDirToken names a picker-issued destination directory.
	DestDirToken string `json:"destDirToken"`
	// Profile is the §40 performance mode ("" = balanced), as on a copy.
	Profile string `json:"profile"`
	// Settings are the per-job §44.1 overrides.
	Settings TransferSettingsDTO `json:"settings"`
	// AcknowledgeOverwrite says the operator asked for a download that may
	// replace local files that differ from the objects (ADR 0122 decision
	// 12), as an upload's does. Without it a download never replaces a local
	// file. What guards it is the native confirmation the backend raises.
	AcknowledgeOverwrite bool `json:"acknowledgeOverwrite"`
	// Members, when given, are the full names of the selected objects to
	// download instead of the whole scope (ADR 0108); empty means the scope.
	Members []string `json:"members,omitempty"`
}

// SelectionScopeDTO is the scope a pick is being made for. The service
// resolves the named bookmark's current generation itself and, whenever
// there is an active connection, requires the pick to name it — before
// the dialog opens and again when it returns (ADR 0041).
type SelectionScopeDTO struct {
	BookmarkID string `json:"bookmarkId"`
	Container  string `json:"container"`
	Prefix     string `json:"prefix"`
}

// PickedFilesDTO is what the webview learns about a file selection:
// display names and a token — never the paths.
type PickedFilesDTO struct {
	Token string   `json:"token"`
	Files []string `json:"files"`
	// Folder, the two counts and the sample are a folder pick's summary (ADR
	// 0103 decision 1): the chosen folder's own name, how many entries the
	// walk skipped, the first of them named, and how many directories were
	// empty. A file pick leaves them zero. A folder with no regular file
	// returns the summary with no token and no names.
	Folder           string   `json:"folder"`
	Skipped          int      `json:"skipped"`
	SkippedSample    []string `json:"skippedSample"`
	EmptyDirectories int      `json:"emptyDirectories"`
}

// PickedDirDTO is the directory selection: a display path (shown to
// the operator, never trusted back) and the token the request carries.
type PickedDirDTO struct {
	Token string `json:"token"`
	Dir   string `json:"dir"`
}

// FilePicker opens native file dialogs. It is an injected seam so
// internal/app stays plain Go with no webview/CGO imports (ADR 0011):
// main.go supplies the Wails implementation; headless contexts have
// none and the pick methods say so instead of linking a webview.
type FilePicker interface {
	PickFiles(title string) ([]string, error)
	PickDirectory(title string) (string, error)
}

// setFilePicker wires the shell's dialog implementation.
func (s *TransferService) setFilePicker(picker FilePicker) {
	s.picker = picker
}

// OverwriteConfirmation is what the backend asks about, stated in its own
// terms rather than the caller's: the bookmark and account suffix it
// resolved, the container and prefix it resolved, the names it found live,
// and what it observed about the container keeping versions. Names is the
// whole set, so the question cannot be asked about fewer names than the
// job will replace.
type OverwriteConfirmation struct {
	// Bookmark is the display name the backend resolved, shortened for
	// display. Account is the account the backend is connected to, in its
	// own field so that shortening a long label can never remove the one
	// part of the statement that tells two accounts apart (ADR 0073's
	// counterexample). A caller rendering one line should state both.
	Bookmark string
	Account  string
	// SourceBookmark and SourceAccount name where a copy's bytes come FROM,
	// so a replacement that reads one account and writes another states both
	// (ADR 0073's counterexample, ADR 0087). They are empty for an upload,
	// whose bytes come from the local disk and whose single account is the one
	// above; when SourceAccount is set the question names the two accounts.
	SourceBookmark string
	SourceAccount  string
	// Folder is the local folder a download writes into, set only for a
	// download, whose bytes come from SourceBookmark and SourceAccount.
	Folder    string
	Container string
	Prefix    string
	// Names are the live object names this job would replace, in the
	// order the preflight reported them.
	Names []string
	// Versioning is what the container said about keeping versions, as an
	// observation and not a promise of recovery (ADR 0081 decision 8).
	Versioning string
	// Fragments says what this start does to a replaced large object's old
	// segments: that the transfer engine removes those under the upload's
	// folder, or why they are kept (ADR 0122 decision 3). An answer of yes
	// has to cover the whole effect, and deleting objects the operator was
	// never shown is what this sentence exists to put in front of them.
	Fragments string
}

// OverwriteConfirmer asks the operator, on a surface the page cannot draw,
// whether an upload may replace the objects already at those names
// (ADR 0081 decision 2). It is an injected seam like FilePicker so
// internal/app stays plain Go (ADR 0011): main.go supplies the shell's
// native dialog, tests supply answers, and a service with none refuses
// rather than assumes. It is never reachable as a bridge action, so a page
// cannot answer its own question.
type OverwriteConfirmer interface {
	ConfirmOverwrite(ctx context.Context, about OverwriteConfirmation) (bool, error)
}

// setOverwriteConfirmer wires the shell's native confirmation.
func (s *TransferService) setOverwriteConfirmer(confirmer OverwriteConfirmer) {
	s.overwrites = confirmer
}

// PickUploadFiles opens the OS multi-file picker and returns a token
// plus display names — the paths stay in Go (ADR 0011: a compromised
// page must not be able to name ~/.ssh/id_rsa). Nil token when
// cancelled.
func (s *TransferService) PickUploadFiles(ctx context.Context, scope SelectionScopeDTO) (*PickedFilesDTO, error) {
	if err := refuseIfDeferred("TransferService.PickUploadFiles"); err != nil {
		return nil, err
	}
	if s.picker == nil {
		return nil, errors.New("no file picker available (headless shell)")
	}
	observed, bound, err := s.beginPick(ctx, scope)
	if err != nil {
		return nil, err
	}
	paths, err := s.picker.PickFiles(s.uploadPickTitle(ctx, bound))
	if err != nil || len(paths) == 0 {
		return nil, err
	}
	if err := s.endPick(ctx, observed, bound); err != nil {
		return nil, err
	}
	sources := local.SourcesByBaseName(paths)
	names := make([]string, len(sources))
	for i, source := range sources {
		names[i] = source.Name
	}
	return &PickedFilesDTO{Token: s.selections.putSources(bound, sources, nil), Files: names}, nil
}

// PickUploadDirectory opens the OS directory chooser and walks the chosen
// folder into an upload selection (ADR 0103): each regular file under it is
// named by the folder and its relative path, links and other special entries
// are skipped and counted, and the selection is bound to the scope exactly
// as a file pick is. A folder with no regular file returns its summary with
// no token. Nil when cancelled.
func (s *TransferService) PickUploadDirectory(ctx context.Context, scope SelectionScopeDTO) (*PickedFilesDTO, error) {
	if err := refuseIfDeferred("TransferService.PickUploadDirectory"); err != nil {
		return nil, err
	}
	if s.picker == nil {
		return nil, errors.New("no file picker available (headless shell)")
	}
	observed, bound, err := s.beginPick(ctx, scope)
	if err != nil {
		return nil, err
	}
	dir, err := s.picker.PickDirectory(s.uploadFolderPickTitle(ctx, bound))
	if err != nil || dir == "" {
		return nil, err
	}
	if err := s.endPick(ctx, observed, bound); err != nil {
		return nil, err
	}
	selection, err := local.WalkUploadDirectory(ctx, dir, local.MaxDirectoryFiles)
	if err != nil {
		return nil, err
	}
	// A walk that finished as the caller gave up issues nothing.
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	picked := &PickedFilesDTO{Folder: selection.Folder, Skipped: selection.Skipped,
		SkippedSample: selection.SkippedSample, EmptyDirectories: selection.EmptyDirectories}
	if len(selection.Sources) == 0 {
		return picked, nil
	}
	picked.Files = make([]string, len(selection.Sources))
	for i, source := range selection.Sources {
		picked.Files[i] = source.Name
	}
	picked.Token = s.selections.putSources(bound, selection.Sources, selection)
	return picked, nil
}

// PickDownloadDir opens the OS directory picker. Nil when cancelled.
func (s *TransferService) PickDownloadDir(ctx context.Context, scope SelectionScopeDTO) (*PickedDirDTO, error) {
	if s.picker == nil {
		return nil, errors.New("no file picker available (headless shell)")
	}
	observed, bound, err := s.beginPick(ctx, scope)
	if err != nil {
		return nil, err
	}
	// The chooser's title is the backend's own statement of what the
	// selection will bind to (ADR 0073 option B): the account and the
	// container and prefix it resolved, drawn by the native dialog where
	// the page cannot draw over it.
	dir, err := s.picker.PickDirectory(s.pickTitle(ctx, bound))
	if err != nil || dir == "" {
		return nil, err
	}
	if err := s.endPick(ctx, observed, bound); err != nil {
		return nil, err
	}
	// The pick captures the directory behind a handle; the token names
	// that directory, not whatever its pathname names later.
	return &PickedDirDTO{Token: s.selections.putDir(bound, dir), Dir: dir}, nil
}

// activeObservation is the connection endpoint as it stood at one
// moment, including the honest absence of one. The pick compares the
// observation either side of the dialog, so a connection that moved —
// to another account, to a new generation, or to none at all — is seen.
type activeObservation struct {
	connected  bool
	bookmarkID string
	generation int64
}

// observeConnection reads the endpoint. Absence means nothing is
// connected and nothing else, so a connection that merely changed while
// being read can never be mistaken for one that is gone.
func (s *TransferService) observeConnection() activeObservation {
	id, generation, connected := s.connections.activeEndpoint()
	if !connected {
		return activeObservation{}
	}
	return activeObservation{connected: true, bookmarkID: id, generation: generation}
}

// beginPick binds the selection to the named bookmark's current
// generation and records the endpoint the dialog is opening under.
func (s *TransferService) beginPick(ctx context.Context, scope SelectionScopeDTO) (activeObservation, selectionScope, error) {
	if scope.BookmarkID == "" || scope.Container == "" {
		return activeObservation{}, selectionScope{}, errors.New("a bookmark and container are required before picking")
	}
	observed := s.observeConnection()
	if observed.connected && observed.bookmarkID != scope.BookmarkID {
		return activeObservation{}, selectionScope{}, errors.New("pick for the account you are connected to")
	}
	entry, err := s.connections.sessionEntryFor(ctx, scope.BookmarkID)
	if err != nil {
		return activeObservation{}, selectionScope{}, err
	}
	return observed, selectionScope{
		bookmarkID: scope.BookmarkID,
		generation: entry.generation,
		container:  scope.Container,
		prefix:     scope.Prefix,
	}, nil
}

// pickTitle composes the native chooser's title from the bookmark and
// session the backend resolved for the pick, never from the caller's
// words: the display name and account suffix come from the store and the
// held session, the container and prefix from the bound scope. Control
// characters are replaced and a long prefix is cut, so a hostile name
// cannot garble the one surface this feature claims is trustworthy.
func (s *TransferService) pickTitle(ctx context.Context, bound selectionScope) string {
	label, account, target := s.pickBinding(ctx, bound)
	return "Download from " + bindingText(label, account, 80) + ", " + titleText(target, 120) + " into…"
}

// uploadPickTitle is the same statement for the other direction. The
// upload chooser was titled "Choose files to upload", which names neither
// the account nor the container, so a page could connect one account
// while displaying another and the dialog would not tell the operator
// apart (ADR 0081 decision 3, ADR 0073's counterexample).
func (s *TransferService) uploadPickTitle(ctx context.Context, bound selectionScope) string {
	label, account, target := s.pickBinding(ctx, bound)
	return "Upload into " + bindingText(label, account, 80) + ", " + titleText(target, 120) + " — choose files…"
}

// uploadFolderPickTitle is the same statement for the folder chooser.
func (s *TransferService) uploadFolderPickTitle(ctx context.Context, bound selectionScope) string {
	label, account, target := s.pickBinding(ctx, bound)
	return "Upload into " + bindingText(label, account, 80) + ", " + titleText(target, 120) + " — choose a folder…"
}

// pickBinding is what the backend resolved, as the label and the account
// it is connected to and the remote target within it, kept apart so a
// caller can state the account in its own right and shared by both
// chooser titles so the two cannot drift into describing the binding
// differently.
func (s *TransferService) pickBinding(ctx context.Context, bound selectionScope) (label, account, target string) {
	if entry, err := s.connections.sessionEntryFor(ctx, bound.bookmarkID); err == nil && entry.session != nil {
		account = accountSuffix(entry.session.StorageURL())
	}
	label = s.connections.displayName(ctx, bound.bookmarkID)
	target = bound.container
	if bound.prefix != "" {
		target += "/" + bound.prefix
	}
	return label, account, target
}

// bindingText states a resolved binding on one line within limit runes.
//
// The account is what tells two bookmarks that read alike apart, so its
// space is reserved before the label is measured and the label is what
// gives way: a label long enough to fill the whole allowance is cut, and
// the account still appears. Its own share is capped in turn, so an
// endpoint that answers with an enormous account name cannot push the
// label out instead.
func bindingText(label, account string, limit int) string {
	label, account = titleSafe(label), titleSafe(account)
	switch {
	case label == "" && account == "":
		return "the connected account"
	case label == "":
		return shortenText(account, limit)
	case account == "":
		return shortenText(label, limit)
	}
	account = shortenText(account, limit/2)
	room := limit - len([]rune(account)) - len(" ()")
	if room < 1 {
		return "(" + account + ")"
	}
	return shortenText(label, room) + " (" + account + ")"
}

// titleText keeps a title legible: control characters become "?" and text
// past limit runes ends in an ellipsis.
func titleText(text string, limit int) string {
	return shortenText(titleSafe(text), limit)
}

// titleSafe replaces the control characters that would let a stored name
// garble the one surface this feature claims is trustworthy.
func titleSafe(text string) string {
	var out []rune
	for _, r := range text {
		if r < 0x20 || r == 0x7f || (r >= 0x80 && r < 0xa0) || r == '\u2028' || r == '\u2029' {
			r = '?'
		}
		out = append(out, r)
	}
	return string(out)
}

// shortenText cuts text past limit runes and ends it in an ellipsis.
func shortenText(text string, limit int) string {
	out := []rune(text)
	if len(out) <= limit {
		return text
	}
	return string(out[:limit]) + "…"
}

// endPick re-reads both halves when the dialog returns. The picker blocks,
// so a page can connect elsewhere while the user is choosing; a selection
// made under a connection that has since moved is refused rather than
// issued. The endpoint must be the same observation it was, and the named
// bookmark must still be at the generation the selection is about to
// record. These are two observations and not a record of every transient
// change (ADR 0041).
func (s *TransferService) endPick(ctx context.Context, before activeObservation, bound selectionScope) error {
	if after := s.observeConnection(); after != before {
		return errors.New("the connected account changed while you were choosing; pick again")
	}
	entry, err := s.connections.sessionEntryFor(ctx, bound.bookmarkID)
	if err != nil {
		return err
	}
	if entry.generation != bound.generation {
		return errors.New("the account's credentials changed while you were choosing; pick again")
	}
	return nil
}

// PreflightUpload is the BRIDGE-facing report: absolute local paths are
// blanked so they never cross to the webview (ADR 0011, review R-09) —
// the page shows destination NAMES. Execution uses uploadPreflight
// (below), which keeps the paths it needs.
func (s *TransferService) PreflightUpload(ctx context.Context, req UploadRequestDTO) (*local.UploadPreflight, error) {
	if err := refuseIfDeferred("TransferService.PreflightUpload"); err != nil {
		return nil, err
	}
	report, err := s.uploadPreflight(ctx, req)
	if err != nil {
		return nil, err
	}
	safe := *report
	safe.Items = make([]local.UploadItem, len(report.Items))
	for i, item := range report.Items {
		item.Path = "" // display-only: paths stay Go-side
		safe.Items[i] = item
	}
	return &safe, nil
}

// uploadPreflight computes the full report WITH local paths, for
// execution — never returned across the bridge.
func (s *TransferService) uploadPreflight(ctx context.Context, req UploadRequestDTO) (*local.UploadPreflight, error) {
	if req.BookmarkID == "" || req.Container == "" {
		return nil, errors.New("bookmark and container are required")
	}
	scope, session, err := s.requestScope(ctx, req.BookmarkID, req.Container, req.Prefix)
	if err != nil {
		return nil, err
	}
	sources, walk, err := s.selections.getSources(req.SelectionToken, scope)
	if err != nil {
		return nil, err
	}
	resolved, _, err := s.localSettings(req.Profile, req.Settings, req.BookmarkID)
	if err != nil {
		return nil, err
	}
	effective, err := resolved.forDestination(ctx, session)
	if err != nil {
		return nil, err
	}
	var report *local.UploadPreflight
	if req.AcknowledgeOverwrite {
		report, err = local.PreflightUploadSourcesReplacing(ctx, session, req.Container, req.Prefix, sources)
	} else {
		report, err = local.PreflightUploadSources(ctx, session, req.Container, req.Prefix, sources)
	}
	if err != nil {
		return nil, err
	}
	discloseWalk(report, walk)
	// Name what a file larger than the effective chunk size is published
	// as before Start (§63.8, ADR 0111), and keep the chunk size the
	// static publication pairs segments by.
	if uploadsLargeObject(report.Items, effective.ChunkSize) {
		if report.LargeObjects, report.LargeObjectsOrigin, err = s.largeObjectsFor(ctx, req, session); err != nil {
			return nil, err
		}
		report.ChunkBytes, _ = engineChunkBytes(effective.ChunkSize)
		_, report.StaticManifestsReturned = s.connections.staticManifestsOf(session)
	}
	return report, nil
}

// largeObjectsFor decides what a segmented upload publishes, and the
// origin its job records (ADR 0111 decision 1): the job's own choice, else
// the bookmark's, else static. A static choice becomes dynamic when the
// session's readable /info has no static large object entry, or a job on
// the session found the store does not return static manifests (decision
// 10); when the capability is unknown it stays static and the publication
// is the attempt. It also becomes dynamic where the container keeps
// versions: the static manifest is written over the dynamic one the engine
// wrote, and such a container keeps what is written over, so every large
// upload would leave the engine's manifest behind as a version, which a
// versions location puts back at the name when the object is deleted (ADR
// 0122 decision 3).
func (s *TransferService) largeObjectsFor(ctx context.Context, req UploadRequestDTO, session *swiftcontrol.Session) (kind, origin string, err error) {
	bookmark, err := s.connections.store.Get(ctx, req.BookmarkID)
	if err != nil {
		return "", "", err
	}
	kind, origin = string(swiftcontrol.ClassSLO), "default"
	switch {
	case req.Settings.LargeObjects != "":
		kind, origin = req.Settings.LargeObjects, "override"
	case bookmark.LargeObjects != "":
		kind, origin = bookmark.LargeObjects, "bookmark"
	}
	if kind != string(swiftcontrol.ClassSLO) {
		return kind, origin, nil
	}
	capabilities, err := s.connections.capabilitiesOf(ctx, session)
	if err != nil {
		return "", "", err
	}
	if capabilities.State("slo") == swiftcontrol.CapabilityUnsupported {
		return string(swiftcontrol.ClassDLO), "capability", nil
	}
	if known, returned := s.connections.staticManifestsOf(session); known && !returned {
		return string(swiftcontrol.ClassDLO), "capability", nil
	}
	observed, err := s.readVersioning(ctx, req.BookmarkID, req.Container)
	if err != nil {
		return "", "", errors.New("the destination container's versioning could not be read, and it decides what a large file uploads as")
	}
	if keepsVersions(observed) {
		return string(swiftcontrol.ClassDLO), "versioning", nil
	}
	return kind, origin, nil
}

// discloseWalk adds what a folder pick's walk left out to the preflight
// report as warnings that reject nothing (ADR 0103 decision 3): the skipped
// entries it named, one row for the count when it named fewer than it
// skipped, and one row for the empty directories. A file pick has no walk.
func discloseWalk(report *local.UploadPreflight, walk *local.DirectorySelection) {
	if walk == nil {
		return
	}
	for _, rel := range walk.SkippedSample {
		report.Problems = append(report.Problems, local.Problem{
			Name: walk.Folder + "/" + rel, Kind: "skipped",
			Detail: "a link, another special entry or a download's own partial file, which a folder upload leaves out; not uploaded"})
	}
	if walk.Skipped > len(walk.SkippedSample) {
		report.Problems = append(report.Problems, local.Problem{
			Name: walk.Folder, Kind: "skipped",
			Detail: fmt.Sprintf("%d entries were skipped in all; the first %d are named above", walk.Skipped, len(walk.SkippedSample))})
	}
	if walk.EmptyDirectories > 0 {
		report.Problems = append(report.Problems, local.Problem{
			Name: walk.Folder, Kind: "empty-directories",
			Detail: fmt.Sprintf("%d empty %s no object: the store has no directories", walk.EmptyDirectories, plural(walk.EmptyDirectories, "directory produces", "directories produce"))})
	}
}

// uploadsLargeObject reports whether any selected file will be uploaded in
// segments as a destination large object (§63.8): only the engine route segments, and only
// a file larger than the effective --swift-chunk-size. A native-routed name is
// one plain PutObject whatever its size, so it never counts.
func uploadsLargeObject(items []local.UploadItem, chunkSize string) bool {
	chunkBytes, ok := engineChunkBytes(chunkSize)
	if !ok {
		return false
	}
	for _, item := range items {
		if item.Route == "rclone" && item.Size > chunkBytes {
			return true
		}
	}
	return false
}

// engineChunkBytes converts a --swift-chunk-size value to bytes the way the
// pinned rclone does, so the preview names the same threshold execution uses.
// rclone's SizeSuffix agrees with ParseSize on every unit suffix but reads a
// bare number as KiB, not bytes; naming the placement with ParseSize's reading
// would disclose the wrong threshold for a suffixless override. ParseSize stays
// the admission and capping parser it already is.
func engineChunkBytes(chunkSize string) (int64, bool) {
	if chunkSize == "" {
		return 0, false
	}
	normalized := chunkSize
	if last := chunkSize[len(chunkSize)-1]; last >= '0' && last <= '9' {
		normalized += "Ki" // a bare number is KiB to the engine, not bytes
	}
	bytes, err := ParseSize(normalized)
	if err != nil || bytes <= 0 {
		return 0, false
	}
	return bytes, true
}

// confirmOverwrite raises the one native confirmation that authorises
// replacing what is live, and refuses unless it is answered yes about
// exactly this plan (ADR 0081 decisions 2, 3 and 4).
//
// It is raised after the preflight, so the names it states are the ones
// this start will replace rather than an earlier guess. The connection is
// observed either side of the dialog for the same reason the pick is: an
// answer given about one account must not authorise a write to another.
// The question states what happens to a replaced large object's old
// segments, from what keepsReplaced decided.
func (s *TransferService) confirmOverwrite(ctx context.Context, req UploadRequestDTO, scope selectionScope,
	preflight *local.UploadPreflight, observed swiftcontrol.ContainerVersioning, versioningErr error, segments string) error {
	var names []string
	for _, item := range preflight.Items {
		if item.Replaces {
			names = append(names, item.Name)
		}
	}
	if len(names) == 0 {
		// Acknowledged, but nothing is there to replace, so there is
		// nothing to ask about.
		return nil
	}
	if s.overwrites == nil {
		return errors.New("replacing an existing object needs the shell's confirmation, and this build has none")
	}
	// A container this build cannot read or reason about is refused without
	// the operator being asked to authorise something it cannot describe.
	if versioningErr != nil {
		return versioningErr
	}
	versioning, err := versioningStatement(observed)
	if err != nil {
		return err
	}
	before := s.observeConnection()
	label, account, _ := s.pickBinding(ctx, scope)
	question, err := overwriteQuestion(label, account, req.Container, req.Prefix, versioning, names, segments)
	if err != nil {
		return err
	}
	return s.askOverwrite(ctx, scope, before, question)
}

// askOverwrite raises the question and accepts only a yes about the same
// account and credentials the question named: the connection is observed
// either side of the dialog, because an answer given about one account must
// not authorise a write to another.
func (s *TransferService) askOverwrite(ctx context.Context, scope selectionScope, before activeObservation, question OverwriteConfirmation) error {
	yes, err := s.overwrites.ConfirmOverwrite(ctx, question)
	if err != nil {
		return err
	}
	if !yes {
		return errors.New("the replacement was not confirmed; nothing was written")
	}
	if after := s.observeConnection(); after != before {
		return errors.New("the connected account changed while you were answering; start again")
	}
	entry, err := s.connections.sessionEntryFor(ctx, scope.bookmarkID)
	if err != nil {
		return err
	}
	if entry.generation != scope.generation {
		return errors.New("the account's credentials changed while you were answering; start again")
	}
	return nil
}

// confirmDownloadOverwrite asks the one native confirmation for a download
// that may replace local files (ADR 0122 decision 12), about the files the
// preflight found and the folder, account and container it names.
func (s *TransferService) confirmDownloadOverwrite(ctx context.Context, req DownloadRequestDTO, scope selectionScope,
	preflight *local.DownloadPreflight, folder string) error {
	var names []string
	for _, item := range preflight.Items {
		if item.Replaces {
			names = append(names, item.RelPath)
		}
	}
	if len(names) == 0 {
		return nil
	}
	if s.overwrites == nil {
		return errors.New("replacing a local file needs the shell's confirmation, and this build has none")
	}
	before := s.observeConnection()
	label, account, _ := s.pickBinding(ctx, scope)
	if account == "" {
		return errors.New("the source account could not be identified, so replacing local files is refused")
	}
	return s.askOverwrite(ctx, scope, before, OverwriteConfirmation{
		Folder: folder, SourceBookmark: titleText(label, 80), SourceAccount: titleText(account, 80),
		Container: req.Container, Prefix: req.Prefix, Names: names,
	})
}

// keepsReplaced decides, once at the start of an upload or a copy, whether the engine
// may remove a replaced large object's old segments, and gives the sentence
// that says so (ADR 0122 decision 3). One switch gives both, because a
// sentence that promises the segments stay beside permission to remove them
// is the defect the pair exists to prevent.
//
// The engine removes them itself as it writes over a large object, where
// the gateway lets it delete: under the upload's folder. It does so even
// where the container has a versions location, whose archived version still
// uses them (measured on the pinned engine), so there, and where the
// operator's setting keeps replaced segments or cannot be read, the engine's
// deletes are refused and the segments stay.
func (s *TransferService) keepsReplaced(ctx context.Context, container, prefix string, observed swiftcontrol.ContainerVersioning) (keeps bool, sentence string) {
	reclaiming, err := s.reclamationSetting(ctx)
	switch {
	case keepsVersions(observed):
		return true, "where a large object is replaced its old segments are KEPT, because the container keeps versions and they belong to the version the store keeps"
	case err != nil:
		return true, "where a large object is replaced its old segments are KEPT, because the setting that allows removing them could not be read"
	case !reclaiming:
		return true, "where a large object is replaced its old segments are KEPT, because reclaiming is turned off in Settings"
	}
	// The engine may delete only where it may write: under the upload's
	// folder, in the container and in its segments container.
	where := "in " + container + "_segments"
	if prefix != "" {
		where = "under " + prefix + " " + where
	}
	return false, "where a large object is replaced, the transfer engine REMOVES its old segments " + where + " as it writes the new object; old segments kept anywhere else, and those of a name this application sends itself, stay where they are"
}

// overwriteQuestion is the statement the operator is asked to authorise.
//
// It is composed here rather than assembled at the call site so that the
// rule which makes it answerable can be checked on its own: a question
// that cannot name the account it is about is refused instead of asked.
// That is the part ADR 0073's counterexample turns on, because a label may
// belong to either of two accounts and the operator has nothing else to
// tell them apart with. A question missing any other part of the plan it
// would authorise is refused for the same reason. segments is the sentence
// about a replaced large object's old segments (keepsReplaced).
func overwriteQuestion(label, account, container, prefix, versioning string, names []string,
	segments string) (OverwriteConfirmation, error) {
	switch {
	case account == "":
		return OverwriteConfirmation{}, errors.New("the destination account could not be identified, so an overwrite is refused")
	case container == "":
		return OverwriteConfirmation{}, errors.New("the destination container could not be identified, so an overwrite is refused")
	case versioning == "":
		return OverwriteConfirmation{}, errors.New("the destination container's versioning could not be stated, so an overwrite is refused")
	case len(names) == 0:
		return OverwriteConfirmation{}, errors.New("there is nothing live to ask about, so no overwrite is authorised")
	}
	return OverwriteConfirmation{
		Bookmark: titleText(label, 80), Account: titleText(account, 80),
		Container: container, Prefix: prefix, Names: names, Versioning: versioning,
		Fragments: segments,
	}, nil
}

// keepsVersions reports whether the container keeps what is replaced, which is
// what makes removing a predecessor's segments the wrong thing to do: they
// belong to a version that is retained. It reads the observation itself, so
// the decision cannot drift from how the question happens to word it.
func keepsVersions(observed swiftcontrol.ContainerVersioning) bool {
	return observed.Mode != swiftcontrol.VersioningOff && observed.Mode != ""
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// readVersioning is the exact observation the question is composed from and
// the fixed point compares against (ADR 0102 decision 2): the mode and the
// archive as the container reports them, before any wording.
func (s *TransferService) readVersioning(ctx context.Context, bookmarkID, container string) (swiftcontrol.ContainerVersioning, error) {
	unreadable := errors.New("the destination container's versioning could not be read, so an overwrite is refused")
	session, err := s.connections.SessionFor(ctx, bookmarkID)
	if err != nil {
		return swiftcontrol.ContainerVersioning{}, unreadable
	}
	detail, err := session.ContainerDetail(ctx, container)
	if err != nil || detail == nil {
		return swiftcontrol.ContainerVersioning{}, unreadable
	}
	return detail.Versioning, nil
}

// versioningStatement words one observation for the operator. It is display
// only: the archive name is bounded and sanitised here, so two archives can
// read alike, which is why the fixed point compares observations and not
// statements.
//
// A mode this build cannot reason about, modern object versioning, which ADR
// 0054 already refuses to edit, is refused rather than described: not knowing
// and knowing there is none are different answers, and only one of them may
// carry a write.
func versioningStatement(observed swiftcontrol.ContainerVersioning) (string, error) {
	switch observed.Mode {
	case swiftcontrol.VersioningOff, "":
		return "none: what is replaced is not kept", nil
	case swiftcontrol.VersioningVersionsLocation, swiftcontrol.VersioningHistoryLocation:
		// The archive name comes from the container's own headers, so it is
		// stored data on a sentence an operator reads to authorise a
		// destruction: it is sanitised and bounded HERE, where it is
		// interpolated, so the fixed words after it cannot be cut away by a
		// long one and cannot be displaced by a crafted one.
		archive := titleText(observed.Archive, 80)
		if archive == "" {
			archive = "an archive it did not name"
		}
		return "the container reports " + string(observed.Mode) + ", archiving into " + archive +
			"; no archived version has been read back by anything here", nil
	default:
		return "", fmt.Errorf("the destination container %s, so an overwrite is refused: %w",
			observed.Mode, swiftcontrol.ErrModernVersioning)
	}
}

// PreflightDownload is the BRIDGE-facing collision report (ADR 0056):
// complete counts and a bounded sample. Execution uses downloadPreflight.
func (s *TransferService) PreflightDownload(ctx context.Context, req DownloadRequestDTO) (*local.DownloadPreflightView, error) {
	report, err := s.downloadPreflight(ctx, req)
	if err != nil {
		return nil, err
	}
	view := report.View()
	return &view, nil
}

// downloadPreflight computes the complete report for execution — never
// returned across the bridge.
func (s *TransferService) downloadPreflight(ctx context.Context, req DownloadRequestDTO) (*local.DownloadPreflight, error) {
	if req.BookmarkID == "" || req.Container == "" {
		return nil, errors.New("bookmark and container are required")
	}
	if err := validateMembers(req.Prefix, req.Members); err != nil {
		return nil, err
	}
	if ceiling := s.downloadObjectCeiling(ctx); int64(len(req.Members)) > ceiling {
		return nil, fmt.Errorf("%d objects are selected, more than the selection ceiling of %d; download the folder instead, or raise the ceiling in Settings", len(req.Members), ceiling)
	}
	scope, session, err := s.requestScope(ctx, req.BookmarkID, req.Container, req.Prefix)
	if err != nil {
		return nil, err
	}
	destination, destDir, err := s.selections.leaseDir(req.DestDirToken, scope)
	if err != nil {
		return nil, err
	}
	ceiling := s.downloadObjectCeiling(ctx)
	if destination == nil {
		// The pick could not be held; the complete report says why,
		// without any effect.
		return local.PreflightDownloadBounded(ctx, session, req.Container, req.Prefix, destDir, ceiling)
	}
	preflight := local.PreflightDownloadMembersInto
	if req.AcknowledgeOverwrite {
		preflight = local.PreflightDownloadMembersIntoReplacing
	}
	report, err := preflight(ctx, session, req.Container, req.Prefix, destination, ceiling, req.Members)
	if err == nil && report != nil {
		// A directory another active download is using is refused before
		// any effect, naming that job (ADR 0078).
		if id, dir, ok := s.destinations.overlap(destination, destination.Path()); ok {
			report.Problems = append(report.Problems, local.Problem{
				Name: destination.Path(), Kind: destinationOverlapKind, Reject: true,
				Detail: fmt.Sprintf("download job %s is using %s; cancel it or pick another directory", id, dir),
			})
		}
	}
	return report, errors.Join(err, destination.Close())
}

// downloadObjectCeiling is the operator's download selection ceiling from
// the application settings, or the default when settings are not wired or
// cannot be read (ADR 0072 decision 4).
func (s *TransferService) downloadObjectCeiling(ctx context.Context) int64 {
	if s.settings == nil {
		return defaultDownloadObjectCeiling
	}
	settings, err := s.settings.Get(ctx)
	if err != nil {
		return defaultDownloadObjectCeiling
	}
	return int64(settings.DownloadObjectCeiling)
}

// reclamationSetting is the operator's setting for a replaced large object's
// old segments, read where it permits a DELETION. An unreadable store is not
// the default here: the default is to remove, and removing objects on a
// guess is the wrong direction, so the caller keeps them and says why. A
// service with no settings wired has no store to disagree with and takes the
// default. The file is read strictly, because Get reports an unreadable file
// and carries on with the defaults.
func (s *TransferService) reclamationSetting(context.Context) (bool, error) {
	if s.settings == nil {
		return true, nil
	}
	settings, err := s.settings.read()
	if err != nil {
		return false, err
	}
	return settings.ReclaimReplacedFragments, nil
}

// WireSettings gives the transfer service the application settings it
// reads at preflight time.
func WireSettings(t *TransferService, settings *SettingsService) {
	t.settings = settings
}

// localSettings resolves an upload's or download's profile and per-job
// overrides as a copy's are (§40, §38.3), with the recommendation of the
// one bookmark the job touches. One recommendation read serves both the
// values and their recorded origins, so the two cannot disagree.
func (s *TransferService) localSettings(profile string, overrides TransferSettingsDTO, bookmarkID string) (TransferSettingsDTO, map[string]string, error) {
	recommendation := s.recommendationFor(bookmarkID)
	settings, err := ResolveSettings(Profile(profile), overrides, recommendation)
	if err != nil {
		return TransferSettingsDTO{}, nil, err
	}
	return settings, settingOrigins(Profile(profile), overrides, recommendation), nil
}

// localEngine builds the single-remote engine for the bookmark's end;
// remotes resolve per spawn with token refresh (§26.2).
func (s *TransferService) localEngine(ctx context.Context, session *swiftcontrol.Session, remoteName string, options rclone.Options) (local.Engine, error) {
	source := func(spawnCtx context.Context) ([]rclone.Remote, error) {
		remote, err := s.connections.EngineRemote(spawnCtx, session, remoteName)
		if err != nil {
			return nil, fmt.Errorf("credentials: %w", err)
		}
		return []rclone.Remote{remote}, nil
	}
	if _, err := source(ctx); err != nil {
		return nil, err
	}
	if s.engineFactory != nil {
		return s.engineFactory(source, options)
	}
	runner, err := rclone.NewRunner(options)
	if err != nil {
		return nil, err
	}
	return boundLocal{runner: runner, source: source}, nil
}

// boundLocal adapts the Runner to local.Engine.
type boundLocal struct {
	runner *rclone.Runner
	source transfercopy.RemoteSource
}

func (b boundLocal) Copy(ctx context.Context, src, dst string, names []string, onEvent func(rclone.Event)) (*rclone.Result, error) {
	remotes, err := b.source(ctx)
	if err != nil {
		return nil, err
	}
	return b.runner.Copy(ctx, remotes, src, dst, names, onEvent)
}

// CopyInto anchors the child's destination to the held folder where the
// runner supports it (local.AnchoredEngine).
func (b boundLocal) CopyInto(ctx context.Context, src, dst string, anchor *os.File, names []string, onEvent func(rclone.Event)) (*rclone.Result, error) {
	remotes, err := b.source(ctx)
	if err != nil {
		return nil, err
	}
	return b.runner.CopyInto(ctx, remotes, src, anchor, names, onEvent)
}

// StartUpload re-runs the preflight (state may have moved since the UI
// showed it), rejects on any collision, and launches the job.
func (s *TransferService) StartUpload(ctx context.Context, req UploadRequestDTO) (string, error) {
	if err := refuseIfDeferred("TransferService.StartUpload"); err != nil {
		return "", err
	}
	owner, ctx, err := s.runner.admit(ctx)
	if err != nil {
		return "", err
	}
	defer owner.finishStart()
	releasePins, err := s.connections.pinForJob(ctx, req.BookmarkID)
	if err != nil {
		return "", err
	}
	owner.hold(releasePins)
	session, err := s.connections.SessionFor(ctx, req.BookmarkID)
	if err != nil {
		return "", err
	}
	preflight, err := s.uploadPreflight(ctx, req)
	if err != nil {
		return "", err
	}
	if preflight.Rejected() {
		return "", errors.New("preflight found problems; resolve them and retry (nothing was written)")
	}
	// Claimed before the selection is consumed, so a start refused for
	// another job's sweep leaves the pick usable. An upload writes under
	// its destination folder (ADR 0105 decision 3, ADR 0122 decision 3).
	if err := owner.claim(destinationClaims(swiftcontrol.AccountIdentity(session.StorageURL()), req.Container, req.Prefix)...); err != nil {
		return "", err
	}
	// The start consumes the selection, so a confirmed pick cannot be
	// replayed into further jobs; two concurrent starts cannot both win.
	scope, _, err := s.requestScope(ctx, req.BookmarkID, req.Container, req.Prefix)
	if err != nil {
		return "", err
	}
	if _, err := s.selections.takeSources(req.SelectionToken, scope); err != nil {
		return "", err
	}
	// One read of the container's versioning decides whether the engine may
	// remove a replaced large object's old segments, for the whole job; a
	// read that fails keeps them.
	observed, versioningErr := s.readVersioning(ctx, req.BookmarkID, req.Container)
	keepsSegments, segments := true, ""
	if versioningErr == nil {
		keepsSegments, segments = s.keepsReplaced(ctx, req.Container, req.Prefix, observed)
	}
	if req.AcknowledgeOverwrite {
		if err := s.confirmOverwrite(ctx, req, scope, preflight, observed, versioningErr, segments); err != nil {
			return "", err
		}
	}
	resolved, origins, err := s.localSettings(req.Profile, req.Settings, req.BookmarkID)
	if err != nil {
		return "", err
	}
	settings, err := resolved.forDestination(ctx, session)
	if err != nil {
		return "", err
	}
	options := settings.options()
	// The engine removes a replaced large object's old segments only where
	// keepsReplaced allowed it (ADR 0122 decision 3).
	options.KeepReplacedSegments = keepsSegments
	// Confirmed, the engine compares each name as rclone copy does and
	// replaces only what differs (ADR 0122 decision 12).
	options.CompareExisting = req.AcknowledgeOverwrite
	// The engine's one size check after a segmented upload reads the
	// dynamic manifest's size through a listing that can lag the last
	// segment (F35); the store checks the segments against the static
	// manifest instead (ADR 0111 decision 2).
	options.IgnoreSize = preflight.LargeObjects != ""
	if preflight.LargeObjects != "" {
		// The job records the kind it publishes, and why (ADR 0111
		// decision 1); an upload that segments nothing records none.
		settings.LargeObjects = preflight.LargeObjects
		origins["largeObjects"] = preflight.LargeObjectsOrigin
	}
	engine, err := s.localEngine(ctx, session, "aeronautes_dst", options)
	if err != nil {
		return "", fmt.Errorf("transfer engine: %w", err)
	}
	job, err := s.jobs.Create(ctx, jobs.Job{
		Type:               jobs.TypeUpload,
		ItemRecordsVersion: 1,
		Settings:           effectiveJSONWithProfile(Profile(req.Profile), settings, origins),
		Source:             fmt.Sprintf("%d local %s", len(preflight.Items), plural(len(preflight.Items), "file", "files")),
		Destination:        req.Container + "/" + req.Prefix,
		DestBookmarkID:     req.BookmarkID,
		Engine:             "rclone+native",
		Counters: jobs.Counters{
			ObjectsPlanned: int64(len(preflight.Items)), BytesPlanned: preflight.TotalBytes,
		},
	})
	if err != nil {
		return "", err
	}
	s.runner.register(job.ID, owner.cancel)
	if err := s.startLocalAudit(ctx, job, req.BookmarkID, "transfer.upload.start"); err != nil {
		owner.cancel()
		return "", err
	}

	owner.launch(job.ID, req.BookmarkID, func(runCtx context.Context) {
		s.runLocal(runCtx, job.ID, req.BookmarkID, job.ItemRecordsVersion, job.Counters, func(innerCtx context.Context, onProgress func(done, same, total, bytes int64)) (*local.Report, error) {
			background := context.WithoutCancel(innerCtx)
			recorder := s.uploadItemRecorder(background, job.ID, req)
			recorder.StaticManifests = func(returned bool) error {
				s.connections.observeStaticManifests(session, returned)
				if returned {
					return nil
				}
				// The record states what is published, and it is about to
				// be dynamic (ADR 0111 decision 10).
				recorded, recordedOrigins := settings, maps.Clone(origins)
				recorded.LargeObjects, recordedOrigins["largeObjects"] = string(swiftcontrol.ClassDLO), "capability"
				return s.jobs.SetSettings(background, job.ID, effectiveJSONWithProfile(Profile(req.Profile), recorded, recordedOrigins))
			}
			report, err := local.Upload(innerCtx, session, engine, "aeronautes_dst:"+req.Container, req.Container,
				preflight, onProgress, s.recoveryRecorder(background, job.ID, req.BookmarkID), recorder)
			if report != nil && err == nil && len(report.Failures) == 0 {
				// The segment paths the engine reported are this job's own;
				// the recovery records of them stay (ADR 0020).
				s.recordOwnership(background, req.BookmarkID, job.ID, report.CapturedPrefixes)
			}
			return report, err
		})
	}, func(cancelCtx context.Context) {
		s.finishLocal(cancelCtx, job.ID, req.BookmarkID, job.ItemRecordsVersion, job.Counters, jobs.StateCancelled, "", "")
	})
	return job.ID, nil
}

// StartDownload mirrors StartUpload for materialization.
func (s *TransferService) StartDownload(ctx context.Context, req DownloadRequestDTO) (string, error) {
	owner, ctx, err := s.runner.admit(ctx)
	if err != nil {
		return "", err
	}
	defer owner.finishStart()
	releasePins, err := s.connections.pinForJob(ctx, req.BookmarkID)
	if err != nil {
		return "", err
	}
	owner.hold(releasePins)
	session, err := s.connections.SessionFor(ctx, req.BookmarkID)
	if err != nil {
		return "", err
	}
	preflight, err := s.downloadPreflight(ctx, req)
	if err != nil {
		return "", err
	}
	if preflight.Rejected() {
		for _, problem := range preflight.Problems {
			if problem.Kind == destinationOverlapKind {
				return "", fmt.Errorf("%w: %s", errDestinationOverlap, problem.Detail)
			}
		}
		return "", errors.New("preflight found problems; resolve them and retry (nothing was written)")
	}
	settings, origins, err := s.localSettings(req.Profile, req.Settings, req.BookmarkID)
	if err != nil {
		return "", err
	}
	// A download segments nothing, so no chunk size is calculated for it
	// and the record must not say one was.
	if settings.ChunkSize == "" {
		delete(origins, "chunkSize")
	}
	if boundary := s.downloadStartBoundary; boundary != nil {
		if err := boundary(ctx); err != nil {
			return "", err
		}
	}
	// The start consumes the selection, so a confirmed pick cannot be
	// replayed into further jobs; two concurrent starts cannot both win.
	scope, _, err := s.requestScope(ctx, req.BookmarkID, req.Container, req.Prefix)
	if err != nil {
		return "", err
	}
	destination, _, err := s.selections.takeDir(req.DestDirToken, scope)
	if err != nil {
		return "", err
	}
	if destination == nil {
		return "", errors.New("the selected directory cannot be held for the job; pick it again")
	}
	// The destination is reserved before the job row exists, in the same
	// critical section as the overlap check, so two starts into
	// overlapping directories cannot both win and a refused start leaves
	// nothing behind (ADR 0078). The reservation moves to the job id
	// once the row exists and leaves with the pins.
	reservation := req.DestDirToken
	if err := s.destinations.reserve(reservation, destination, destination.Path()); err != nil {
		_ = destination.Close()
		return "", err
	}
	// The job owns this handle. It closes with the pins after the whole
	// body has returned, on every path, and on any refusal below.
	owner.hold(func() {
		releasePins()
		s.destinations.release(reservation)
		_ = destination.Close()
	})
	if req.AcknowledgeOverwrite {
		if err := s.confirmDownloadOverwrite(ctx, req, scope, preflight, destination.Path()); err != nil {
			return "", err
		}
	}
	options := settings.options()
	// The engine writes each file in the folder itself. Unconfirmed, it
	// leaves alone one that is there, which it reports in a line of its own
	// (ADR 0122 decision 5); confirmed, it compares each one as rclone copy
	// does and replaces what differs (decision 12).
	options.LeaveExisting = !req.AcknowledgeOverwrite
	options.CompareExisting = req.AcknowledgeOverwrite
	engine, err := s.localEngine(ctx, session, "aeronautes_src", options)
	if err != nil {
		return "", fmt.Errorf("transfer engine: %w", err)
	}
	if before := s.downloadBoundary; before != nil {
		engine = wrapDownloadBoundary(engine, before)
	}
	job, err := s.jobs.Create(ctx, jobs.Job{
		Type:               jobs.TypeDownload,
		ItemRecordsVersion: 1,
		Settings:           effectiveJSONWithProfile(Profile(req.Profile), settings, origins),
		Source:             req.Container + "/" + req.Prefix,
		Destination:        "local directory (picker-selected)",
		SourceBookmarkID:   req.BookmarkID,
		Engine:             "rclone+native",
		SelectedMembers:    len(req.Members),
		Counters: jobs.Counters{
			ObjectsPlanned: preflight.Objects, BytesPlanned: preflight.TotalBytes,
		},
	})
	if err != nil {
		return "", err
	}
	s.destinations.rekey(reservation, job.ID)
	reservation = job.ID
	s.runner.register(job.ID, owner.cancel)
	if err := s.startLocalAudit(ctx, job, req.BookmarkID, "transfer.download.start"); err != nil {
		owner.cancel()
		return "", err
	}

	owner.launch(job.ID, req.BookmarkID, func(runCtx context.Context) {
		s.runLocal(runCtx, job.ID, req.BookmarkID, job.ItemRecordsVersion, job.Counters, func(innerCtx context.Context, onProgress func(done, same, total, bytes int64)) (*local.Report, error) {
			return local.DownloadInto(innerCtx, session, engine, "aeronautes_src:"+req.Container, req.Container, req.Prefix, destination, preflight,
				onProgress, s.downloadItemRecorder(context.WithoutCancel(innerCtx), job.ID, req, destination.Path()))
		})
	}, func(cancelCtx context.Context) {
		s.finishLocal(cancelCtx, job.ID, req.BookmarkID, job.ItemRecordsVersion, job.Counters, jobs.StateCancelled, "", "")
	})
	return job.ID, nil
}

func (s *TransferService) startLocalAudit(ctx context.Context, job jobs.Job, bookmarkID, operation string) error {
	err := s.runner.audit.Append(ctx, audit.Event{
		BookmarkID: bookmarkID, Operation: operation, Resource: job.Source + " → " + job.Destination,
		Summary: "job " + job.ID + " · " + job.Settings, Result: audit.ResultApplied,
	})
	if err != nil {
		s.finishLocal(context.WithoutCancel(ctx), job.ID, bookmarkID, job.ItemRecordsVersion, job.Counters, jobs.StateFailed, "start audit unavailable before first write: "+err.Error(), "")
		return fmt.Errorf("start audit unavailable: %w", err)
	}
	return nil
}

// disclosure is one fact a finished local transfer states beside its own
// message, for the operator and for the audit trail; the two differ only in
// how much of a first error the operator's line quotes.
type disclosure struct {
	message, audit string
}

// localDisclosures is what the report obliges a finished job to say: a
// download's engine that did not end normally, a destination the picked
// spelling no longer names, and directories an unfinished job left behind.
// The second turns a complete job partial, because the operator must look
// before trusting the records; the others are facts about the disk and
// never change the state.
func localDisclosures(report *local.Report, state jobs.State) ([]disclosure, jobs.State) {
	if report == nil {
		return nil, state
	}
	var out []disclosure
	// Interrupted, the engine removes the file it was writing; killed, it
	// leaves it (ADR 0122 decision 5). Nothing looks, so this says what may
	// be there and how to know it.
	if report.EngineStopped {
		left := "the transfer engine did not end normally: a file it was writing may remain in the selected directory under a name ending .partial"
		out = append(out, disclosure{message: left, audit: left})
	}
	// Effects went into the directory picked, but the recorded paths keep
	// the picked spelling, which no longer names it.
	if report.RootDisplacement != "" {
		out = append(out, disclosure{message: report.RootDisplacement, audit: report.RootDisplacement})
		if state == jobs.StateComplete {
			state = jobs.StatePartial
		}
	}
	// A job that did not finish leaves these behind, and they are the
	// operator's own directory (ADR 0043). On a complete job the same
	// directories are the intended outcome and the item records already
	// carry them, so this says nothing there.
	if report.CreatedDirectories > 0 && state != jobs.StateComplete {
		count := fmt.Sprintf("created %d directories inside the selected directory that were not there before", report.CreatedDirectories)
		created := count
		if len(report.CreatedDirectorySamples) > 0 {
			created += " (" + strings.Join(report.CreatedDirectorySamples, ", ")
			if int64(len(report.CreatedDirectorySamples)) < report.CreatedDirectories {
				created += ", …"
			}
			created += ")"
		}
		recorded := created
		if audit.SummaryRefusal(recorded) != "" {
			// A directory name can carry a word the audit store refuses, and the
			// store would then withhold this job's whole terminal sentence; the
			// record keeps the count and the job's own message keeps the names.
			recorded = count + "; their names are withheld from this record"
		}
		out = append(out, disclosure{message: created, audit: recorded})
	}
	return out, state
}

func (s *TransferService) finishLocal(ctx context.Context, jobID, bookmarkID string, version int, counters jobs.Counters, state jobs.State, message, auditSummary string) {
	// A completed body must no longer accept cancellation, even when
	// persistence fails and the shared store reports unavailability.
	s.runner.release(jobID)
	finish := s.jobs.Finish
	if version == 1 {
		finish = s.jobs.FinishFromItems
		totals, err := s.jobs.ReadItemTotals(ctx, jobID)
		if err != nil {
			if message != "" {
				message += "; "
			}
			message += err.Error()
			if state == jobs.StateComplete {
				state = jobs.StatePartial
			}
		} else if state == jobs.StateComplete && (totals.Failed != 0 || totals.Conflicts != 0 || totals.Refused != 0 || totals.Unresolved != 0 || totals.Unverified != 0 || totals.Problems != 0) {
			state = jobs.StatePartial
			message = "retained item outcomes contain failures or uncertainty"
		}
	}
	err := finish(ctx, jobID, jobs.Completion{
		State: state, Counters: counters, Error: message,
		Audit: audit.Event{BookmarkID: bookmarkID, Operation: "transfer.local." + string(state), Resource: "job " + jobID, Summary: auditSummary, Result: terminalAuditResult(state)},
	}, s.runner.audit)
	if err != nil {
		applog.Errorf("job %s terminal outcome was not saved: %v", jobID, err)
	}
}

// onlyCancellations reports whether every recorded failure is the
// cancellation itself; a job with such failures and nothing copied was
// cancelled rather than partially done.
func onlyCancellations(failures []local.Failure) bool {
	for _, failure := range failures {
		if !failure.Cancelled {
			return false
		}
	}
	return true
}

// runLocal is the shared local-transfer job goroutine: preflight was
// the Planning half; execution verifies per object as it goes (§27.5),
// so the lifecycle is Planning → Running → Verifying → terminal with
// verification counted from the per-object checks.
func (s *TransferService) runLocal(ctx context.Context, jobID, bookmarkID string, version int, base jobs.Counters,
	run func(ctx context.Context, onProgress func(done, same, total, bytes int64)) (*local.Report, error)) {

	background := context.WithoutCancel(ctx)
	counters := base
	var report *local.Report
	finish := func(state jobs.State, message string) {
		var disclosures []disclosure
		disclosures, state = localDisclosures(report, state)
		messages, audits := []string{message}, make([]string, 0, len(disclosures))
		for _, d := range disclosures {
			messages = append(messages, d.message)
			audits = append(audits, d.audit)
		}
		s.finishLocal(background, jobID, bookmarkID, version, counters, state, joinNotes(messages...), joinNotes(audits...))
	}
	for _, state := range []jobs.State{jobs.StatePlanning, jobs.StateRunning} {
		if err := s.jobs.Available(); err != nil {
			s.runner.release(jobID)
			return
		}
		if err := s.jobs.Transition(background, jobID, state); err != nil {
			finish(jobs.StateFailed, "job admission unavailable before first write: "+err.Error())
			return
		}
	}

	var err error
	report, err = run(ctx, func(done, same, total, bytes int64) {
		// Progress flushes are best-effort; the terminal flush is exact.
		// Bytes stream live so the drawer's rate and ETA work for local
		// transfers too (§65.8 item 5).
		counters.ObjectsCopied = done
		// A file left as the same shows as soon as the engine says so
		// (ADR 0122 decision 14), not only when the run returns.
		counters.ObjectsSame = same
		counters.BytesCopied = bytes
		_ = s.jobs.UpdateCounters(background, jobID, counters)
	})
	if report == nil && err == nil {
		err = errors.New("local transfer returned no outcome")
	}
	if report != nil {
		counters.ObjectsCopied = report.Done
		counters.ObjectsVerified = report.Verified
		counters.BytesCopied = report.Bytes
		counters.NativeRouted = report.NativeRouted
		// The operator asked for this on 27 September: a job that replaced
		// objects reports how many, rather than leaving a field named
		// overwrites reading zero for whoever wires it to a screen later.
		//
		// What it counts is narrower than it sounds, and the difference
		// matters to whoever renders it: writes the engine or the store
		// answered for, to names the preview found live. It is not a count
		// of proven displaced objects. Another writer means the object a
		// write landed on may not be the one the preview saw, and a write
		// whose answer was lost is not counted at all, because its outcome
		// is unknown.
		counters.Overwrites = report.Replaced
		counters.ObjectsSame = report.Same
		if version == 0 {
			counters.ObjectsFailed = int64(len(report.Failures))
		}
	}
	failures := ""
	if report != nil && len(report.Failures) > 0 {
		label := "transfer failures"
		if version == 0 {
			label = fmt.Sprintf("%d failed", len(report.Failures))
		}
		failures = fmt.Sprintf("%s (first: %s: %s)", label, report.Failures[0].Name, report.Failures[0].Err)
	}
	switch {
	case report != nil && report.PostCommitIssues > 0:
		message := fmt.Sprintf("%d post-commit issues", report.PostCommitIssues)
		if len(report.PostCommitIssueSamples) > 0 {
			first := report.PostCommitIssueSamples[0]
			message += fmt.Sprintf(" (first: %s: %s: %s)", first.Name, first.Stage, first.Err)
		}
		if len(report.Failures) > 0 {
			message += "; " + failures
		}
		if err != nil {
			message += "; " + err.Error()
		}
		finish(jobs.StatePartial, message)
	case err != nil && errors.Is(err, context.Canceled) && report != nil && report.Done == 0 && onlyCancellations(report.Failures):
		// Nothing landed and every failure is the cancellation itself:
		// the job was cancelled, and its message still names what was
		// interrupted.
		finish(jobs.StateCancelled, failures)
	case err != nil && report != nil && (report.Done > 0 || len(report.Failures) > 0):
		message := err.Error()
		if len(report.Failures) > 0 {
			message = failures + "; " + message
		}
		finish(jobs.StatePartial, message)
	case err != nil && errors.Is(err, context.Canceled):
		finish(jobs.StateCancelled, "")
	case err != nil:
		finish(jobs.StateFailed, err.Error())
	default:
		if err := s.jobs.Transition(background, jobID, jobs.StateVerifying); err != nil {
			message := "verification state could not be saved: " + err.Error()
			if report != nil && len(report.Failures) > 0 {
				message += "; " + failures
			}
			finish(jobs.StatePartial, message)
			return
		}
		if report != nil && len(report.Failures) > 0 {
			finish(jobs.StatePartial, failures)
		} else {
			finish(jobs.StateComplete, "")
		}
	}
}

// requestScope builds the scope a request is asking for. Resolving the
// session revalidates the bookmark's generation, so the value compared
// against the selection is the live one.
func (s *TransferService) requestScope(ctx context.Context, bookmarkID, container, prefix string) (selectionScope, *swiftcontrol.Session, error) {
	entry, err := s.connections.sessionEntryFor(ctx, bookmarkID)
	if err != nil {
		return selectionScope{}, nil, err
	}
	return selectionScope{
		bookmarkID: bookmarkID,
		generation: entry.generation,
		container:  container,
		prefix:     prefix,
	}, entry.session, nil
}
