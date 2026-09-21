package app

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

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
	// Settings are the per-job §44.1 overrides.
	Settings TransferSettingsDTO `json:"settings"`
}

// DownloadRequestDTO scopes a prefix download; DestDirToken is the
// picker-issued directory handle (paths never cross the bridge).
type DownloadRequestDTO struct {
	BookmarkID string `json:"bookmarkId"`
	Container  string `json:"container"`
	Prefix     string `json:"prefix"`
	// DestDirToken names a picker-issued destination directory.
	DestDirToken string `json:"destDirToken"`
	// Settings are the per-job §44.1 overrides.
	Settings TransferSettingsDTO `json:"settings"`
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
	paths, err := s.picker.PickFiles("Choose files to upload")
	if err != nil || len(paths) == 0 {
		return nil, err
	}
	if err := s.endPick(ctx, observed, bound); err != nil {
		return nil, err
	}
	names := make([]string, len(paths))
	for i, p := range paths {
		names[i] = filepath.Base(p)
	}
	return &PickedFilesDTO{Token: s.selections.putFiles(bound, paths), Files: names}, nil
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
	dir, err := s.picker.PickDirectory("Choose a destination directory")
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

// pickScope reads the account half of the binding from the connection
// registry and joins it to the remote scope the pick is for.
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

// stillPicking re-reads the registry when the native dialog returns.
// The picker blocks, so a page can connect elsewhere while the user is
// choosing; a selection made under a connection that has since moved is
// refused rather than issued. These are two observations and not a
// record of every transient change (ADR 0041).
// endPick re-reads both halves when the dialog returns. The endpoint
// must be the same observation it was, and the named bookmark must still
// be at the generation the selection is about to record. These are two
// observations and not a record of every transient change (ADR 0041).
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
	paths, err := s.selections.getFiles(req.SelectionToken, scope)
	if err != nil {
		return nil, err
	}
	if _, err := req.Settings.forDestination(ctx, session); err != nil {
		return nil, err
	}
	return local.PreflightUpload(ctx, session, req.Container, req.Prefix, paths)
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
	scope, session, err := s.requestScope(ctx, req.BookmarkID, req.Container, req.Prefix)
	if err != nil {
		return nil, err
	}
	destination, destDir, err := s.selections.leaseDir(req.DestDirToken, scope)
	if err != nil {
		return nil, err
	}
	if destination == nil {
		// The pick could not be held; the complete report says why,
		// without any effect.
		return local.PreflightDownload(ctx, session, req.Container, req.Prefix, destDir)
	}
	report, err := local.PreflightDownloadInto(ctx, session, req.Container, req.Prefix, destination)
	return report, errors.Join(err, destination.Close())
}

// localEngine builds the single-remote engine for the bookmark's end;
// remotes resolve per spawn with token refresh (§26.2).
func (s *TransferService) localEngine(ctx context.Context, session *swiftcontrol.Session, remoteName string, settings TransferSettingsDTO) (local.Engine, error) {
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
		return s.engineFactory(source, settings.options())
	}
	runner, err := rclone.NewRunner(settings.options())
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

// CopyInto anchors the child's destination to the held stage directory
// where the runner supports it (local.AnchoredEngine).
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
	defer func() {
		if releasePins != nil {
			releasePins()
		}
	}()
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
	// The start consumes the selection, so a confirmed pick cannot be
	// replayed into further jobs; two concurrent starts cannot both win.
	scope, _, err := s.requestScope(ctx, req.BookmarkID, req.Container, req.Prefix)
	if err != nil {
		return "", err
	}
	if _, err := s.selections.takeFiles(req.SelectionToken, scope); err != nil {
		return "", err
	}
	settings, err := req.Settings.forDestination(ctx, session)
	if err != nil {
		return "", err
	}
	engine, err := s.localEngine(ctx, session, "aeronautes_dst", settings)
	if err != nil {
		return "", fmt.Errorf("transfer engine: %w", err)
	}
	job, err := s.jobs.Create(ctx, jobs.Job{
		Type:               jobs.TypeUpload,
		ItemRecordsVersion: 1,
		Settings:           settings.effectiveJSON(),
		Source:             fmt.Sprintf("%d local files", len(preflight.Items)),
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
	// Retained staging intent makes uncertain residue visible after a
	// crash; it does not prove that reclaiming that residue is safe.
	if err := s.jobs.RecoveryAdd(context.WithoutCancel(ctx), jobs.RecoveryRecord{
		JobID: job.ID, BookmarkID: req.BookmarkID, Kind: jobs.RecoveryStaging,
		Container: req.Container, Prefix: ".aeronautes/stage/" + job.ID + "/",
	}); err != nil {
		owner.cancel()
		s.finishLocal(context.WithoutCancel(ctx), job.ID, req.BookmarkID, job.ItemRecordsVersion, job.Counters, jobs.StateFailed, "recovery store unavailable before first write: "+err.Error(), "")
		return "", fmt.Errorf("recovery store unavailable: %w", err)
	}
	if err := s.startLocalAudit(ctx, job, req.BookmarkID, "transfer.upload.start"); err != nil {
		owner.cancel()
		return "", err
	}

	owner.launch(job.ID, req.BookmarkID, releasePins, func(runCtx context.Context) {
		s.runLocal(runCtx, job.ID, req.BookmarkID, job.ItemRecordsVersion, job.Counters, func(innerCtx context.Context, onProgress func(done, total, bytes int64)) (*local.Report, error) {
			background := context.WithoutCancel(innerCtx)
			report, err := local.Upload(innerCtx, session, engine, "aeronautes_dst:"+req.Container, req.Container, req.Prefix, job.ID,
				preflight, onProgress, s.recoveryRecorder(background, job.ID, req.BookmarkID), s.uploadItemRecorder(background, job.ID, req))
			if report != nil && err == nil && len(report.Failures) == 0 {
				// Verification does not prove cleanup completed or exclude
				// outside references, so recovery records stay (ADR 0020).
				s.recordOwnership(background, req.BookmarkID, job.ID, report.CapturedPrefixes)
			}
			return report, err
		})
	}, func(cancelCtx context.Context) {
		s.finishLocal(cancelCtx, job.ID, req.BookmarkID, job.ItemRecordsVersion, job.Counters, jobs.StateCancelled, "", "")
	})
	releasePins = nil
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
	defer func() {
		if releasePins != nil {
			releasePins()
		}
	}()
	session, err := s.connections.SessionFor(ctx, req.BookmarkID)
	if err != nil {
		return "", err
	}
	preflight, err := s.downloadPreflight(ctx, req)
	if err != nil {
		return "", err
	}
	if preflight.Rejected() {
		return "", errors.New("preflight found problems; resolve them and retry (nothing was written)")
	}
	if err := req.Settings.Validate(); err != nil {
		return "", err
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
	// The job owns this handle. It closes with the pins after the whole
	// body has returned, on every path, and on any refusal below.
	release := releasePins
	releasePins = func() {
		release()
		_ = destination.Close()
	}
	engine, err := s.localEngine(ctx, session, "aeronautes_src", req.Settings)
	if err != nil {
		return "", fmt.Errorf("transfer engine: %w", err)
	}
	if boundary := s.downloadStageBoundary; boundary != nil {
		engine = wrapDownloadStageBoundary(engine, boundary)
	}
	job, err := s.jobs.Create(ctx, jobs.Job{
		Type:               jobs.TypeDownload,
		ItemRecordsVersion: 1,
		Settings:           req.Settings.effectiveJSON(),
		Source:             req.Container + "/" + req.Prefix,
		Destination:        "local directory (picker-selected)",
		SourceBookmarkID:   req.BookmarkID,
		Engine:             "rclone+native",
		Counters: jobs.Counters{
			ObjectsPlanned: preflight.Objects, BytesPlanned: preflight.TotalBytes,
		},
	})
	if err != nil {
		return "", err
	}
	s.runner.register(job.ID, owner.cancel)
	if err := s.startLocalAudit(ctx, job, req.BookmarkID, "transfer.download.start"); err != nil {
		owner.cancel()
		return "", err
	}

	owner.launch(job.ID, req.BookmarkID, releasePins, func(runCtx context.Context) {
		s.runLocal(runCtx, job.ID, req.BookmarkID, job.ItemRecordsVersion, job.Counters, func(innerCtx context.Context, onProgress func(done, total, bytes int64)) (*local.Report, error) {
			return local.DownloadInto(innerCtx, session, engine, "aeronautes_src:"+req.Container, req.Container, req.Prefix, destination, job.ID, preflight,
				s.downloadProgress(innerCtx, job.ID, onProgress), s.downloadItemRecorder(context.WithoutCancel(innerCtx), job.ID, req, destination.Path()))
		})
	}, func(cancelCtx context.Context) {
		s.finishLocal(cancelCtx, job.ID, req.BookmarkID, job.ItemRecordsVersion, job.Counters, jobs.StateCancelled, "", "")
	})
	releasePins = nil
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
		log.Printf("job %s terminal outcome was not saved: %v", jobID, err)
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
	run func(ctx context.Context, onProgress func(done, total, bytes int64)) (*local.Report, error)) {

	background := context.WithoutCancel(ctx)
	counters := base
	var stageCleanup *local.StageCleanupResult
	rootDisplacement := ""
	createdDirectories := int64(0)
	var createdSamples []string
	finish := func(state jobs.State, message string) {
		auditSummary := ""
		if stageCleanup != nil && (stageCleanup.Issues > 0 || stageCleanup.Retained) {
			if state == jobs.StateComplete {
				state = jobs.StatePartial
			}
			if message != "" {
				message += "; "
			}
			// One of three outcomes, and never an assertion of the other
			// two: cleanup did not run, or it ran and left residue, or it
			// ran and left none (ADR 0048). Selecting on Retained alone
			// claimed a recording failure whenever cleanup ran and
			// deliberately left files behind.
			switch {
			case !stageCleanup.Attempted:
				auditSummary = "download stage retained after required item recording failed; cleanup was not attempted"
				message += auditSummary
			case stageCleanup.Retained:
				auditSummary = fmt.Sprintf("download stage retained after cleanup: %d issues", stageCleanup.Issues)
				message += fmt.Sprintf("%s (first: %s)", auditSummary, stageCleanup.FirstError)
			default:
				auditSummary = fmt.Sprintf("download stage cleanup: %d issues", stageCleanup.Issues)
				message += fmt.Sprintf("%s (first: %s)", auditSummary, stageCleanup.FirstError)
			}
		}
		// Effects went into the directory picked, but the recorded paths
		// keep the picked spelling, which no longer names it. The outcome
		// is partial so the operator looks before trusting the records.
		if rootDisplacement != "" {
			if state == jobs.StateComplete {
				state = jobs.StatePartial
			}
			if message != "" {
				message += "; "
			}
			message += rootDisplacement
			if auditSummary != "" {
				auditSummary += "; "
			}
			auditSummary += rootDisplacement
		}
		// A job that did not finish leaves these behind, and they are the
		// operator's own directory (ADR 0043). On a complete job the same
		// directories are the intended outcome and the item records
		// already carry them, so this says nothing there. It is a fact
		// about the disk and never changes the state.
		if createdDirectories > 0 && state != jobs.StateComplete {
			created := fmt.Sprintf("created %d directories inside the selected directory that were not there before", createdDirectories)
			if len(createdSamples) > 0 {
				created += " (" + strings.Join(createdSamples, ", ")
				if int64(len(createdSamples)) < createdDirectories {
					created += ", …"
				}
				created += ")"
			}
			if message != "" {
				message += "; "
			}
			message += created
			if auditSummary != "" {
				auditSummary += "; "
			}
			auditSummary += created
		}
		s.finishLocal(background, jobID, bookmarkID, version, counters, state, message, auditSummary)
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

	report, err := run(ctx, func(done, total, bytes int64) {
		// Progress flushes are best-effort; the terminal flush is exact.
		// Bytes stream live so the drawer's rate and ETA work for local
		// transfers too (§65.8 item 5).
		counters.ObjectsCopied = done
		counters.BytesCopied = bytes
		_ = s.jobs.UpdateCounters(background, jobID, counters)
	})
	if report == nil && err == nil {
		err = errors.New("local transfer returned no outcome")
	}
	if report != nil {
		stageCleanup = report.StageCleanup
		rootDisplacement = report.RootDisplacement
		createdDirectories = report.CreatedDirectories
		createdSamples = report.CreatedDirectorySamples
		counters.ObjectsCopied = report.Done
		counters.ObjectsVerified = report.Verified
		counters.BytesCopied = report.Bytes
		counters.NativeRouted = report.NativeRouted
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
		// interrupted so the retained stage or temporary is disclosed.
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
