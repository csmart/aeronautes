package app

import (
	"context"
	"errors"
	"fmt"
	"log"
	"path/filepath"

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
func (s *TransferService) PickUploadFiles(ctx context.Context) (*PickedFilesDTO, error) {
	if s.picker == nil {
		return nil, errors.New("no file picker available (headless shell)")
	}
	paths, err := s.picker.PickFiles("Choose files to upload")
	if err != nil || len(paths) == 0 {
		return nil, err
	}
	names := make([]string, len(paths))
	for i, p := range paths {
		names[i] = filepath.Base(p)
	}
	return &PickedFilesDTO{Token: s.selections.putFiles(paths), Files: names}, nil
}

// PickDownloadDir opens the OS directory picker. Nil when cancelled.
func (s *TransferService) PickDownloadDir(ctx context.Context) (*PickedDirDTO, error) {
	if s.picker == nil {
		return nil, errors.New("no file picker available (headless shell)")
	}
	dir, err := s.picker.PickDirectory("Choose a destination directory")
	if err != nil || dir == "" {
		return nil, err
	}
	return &PickedDirDTO{Token: s.selections.putDir(dir), Dir: dir}, nil
}

// PreflightUpload is the BRIDGE-facing report: absolute local paths are
// blanked so they never cross to the webview (ADR 0011, review R-09) —
// the page shows destination NAMES. Execution uses uploadPreflight
// (below), which keeps the paths it needs.
func (s *TransferService) PreflightUpload(ctx context.Context, req UploadRequestDTO) (*local.UploadPreflight, error) {
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
	paths, err := s.selections.getFiles(req.SelectionToken)
	if err != nil {
		return nil, err
	}
	session, err := s.connections.SessionFor(ctx, req.BookmarkID)
	if err != nil {
		return nil, err
	}
	return local.PreflightUpload(ctx, session, req.Container, req.Prefix, paths)
}

// PreflightDownload runs the complete §27.5 materialization report.
func (s *TransferService) PreflightDownload(ctx context.Context, req DownloadRequestDTO) (*local.DownloadPreflight, error) {
	if req.BookmarkID == "" || req.Container == "" {
		return nil, errors.New("bookmark and container are required")
	}
	destDir, err := s.selections.getDir(req.DestDirToken)
	if err != nil {
		return nil, err
	}
	session, err := s.connections.SessionFor(ctx, req.BookmarkID)
	if err != nil {
		return nil, err
	}
	return local.PreflightDownload(ctx, session, req.Container, req.Prefix, destDir)
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

// StartUpload re-runs the preflight (state may have moved since the UI
// showed it), rejects on any collision, and launches the job.
func (s *TransferService) StartUpload(ctx context.Context, req UploadRequestDTO) (string, error) {
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
	if err := req.Settings.Validate(); err != nil {
		return "", err
	}
	engine, err := s.localEngine(ctx, session, "aeronautes_dst", req.Settings)
	if err != nil {
		return "", fmt.Errorf("transfer engine: %w", err)
	}
	job, err := s.jobs.Create(ctx, jobs.Job{
		Type:           jobs.TypeUpload,
		Settings:       req.Settings.effectiveJSON(),
		Source:         fmt.Sprintf("%d local files", len(preflight.Items)),
		Destination:    req.Container + "/" + req.Prefix,
		DestBookmarkID: req.BookmarkID,
		Engine:         "rclone+native",
		Counters: jobs.Counters{
			ObjectsPlanned: int64(len(preflight.Items)), BytesPlanned: preflight.TotalBytes,
		},
	})
	if err != nil {
		return "", err
	}
	jobCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	s.runner.register(job.ID, cancel)
	// Retained staging intent makes uncertain residue visible after a
	// crash; it does not prove that reclaiming that residue is safe.
	if err := s.jobs.RecoveryAdd(context.WithoutCancel(ctx), jobs.RecoveryRecord{
		JobID: job.ID, BookmarkID: req.BookmarkID, Kind: jobs.RecoveryStaging,
		Container: req.Container, Prefix: ".aeronautes/stage/" + job.ID + "/",
	}); err != nil {
		cancel()
		s.finishLocal(context.WithoutCancel(ctx), job.ID, req.BookmarkID, job.Counters, jobs.StateFailed, "recovery store unavailable before first write: "+err.Error())
		return "", fmt.Errorf("recovery store unavailable: %w", err)
	}
	if err := s.startLocalAudit(ctx, job, req.BookmarkID, "transfer.upload.start"); err != nil {
		cancel()
		return "", err
	}

	s.runner.launchWithQueuedOutcome(jobCtx, job.ID, req.BookmarkID, releasePins, func(runCtx context.Context) {
		s.runLocal(runCtx, job.ID, req.BookmarkID, job.Counters, func(innerCtx context.Context, onProgress func(done, total, bytes int64)) (*local.Report, error) {
			background := context.WithoutCancel(innerCtx)
			report, err := local.Upload(innerCtx, session, engine, "aeronautes_dst:"+req.Container, req.Container, req.Prefix, job.ID,
				preflight, onProgress, s.recoveryRecorder(background, job.ID, req.BookmarkID))
			if report != nil && err == nil && len(report.Failures) == 0 {
				// Verification does not prove cleanup completed or exclude
				// outside references, so recovery records stay (ADR 0020).
				s.recordOwnership(background, req.BookmarkID, job.ID, report.CapturedPrefixes)
			}
			return report, err
		})
	}, func(cancelCtx context.Context) {
		s.finishLocal(cancelCtx, job.ID, req.BookmarkID, job.Counters, jobs.StateCancelled, "")
	})
	releasePins = nil
	return job.ID, nil
}

// StartDownload mirrors StartUpload for materialization.
func (s *TransferService) StartDownload(ctx context.Context, req DownloadRequestDTO) (string, error) {
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
	destDir, err := s.selections.getDir(req.DestDirToken)
	if err != nil {
		return "", err
	}
	preflight, err := s.PreflightDownload(ctx, req)
	if err != nil {
		return "", err
	}
	if preflight.Rejected() {
		return "", errors.New("preflight found collisions; resolve them and retry (nothing was written)")
	}
	if err := req.Settings.Validate(); err != nil {
		return "", err
	}
	engine, err := s.localEngine(ctx, session, "aeronautes_src", req.Settings)
	if err != nil {
		return "", fmt.Errorf("transfer engine: %w", err)
	}
	job, err := s.jobs.Create(ctx, jobs.Job{
		Type:             jobs.TypeDownload,
		Settings:         req.Settings.effectiveJSON(),
		Source:           req.Container + "/" + req.Prefix,
		Destination:      "local directory (picker-selected)",
		SourceBookmarkID: req.BookmarkID,
		Engine:           "rclone+native",
		Counters: jobs.Counters{
			ObjectsPlanned: preflight.Objects, BytesPlanned: preflight.TotalBytes,
		},
	})
	if err != nil {
		return "", err
	}
	jobCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	s.runner.register(job.ID, cancel)
	if err := s.startLocalAudit(ctx, job, req.BookmarkID, "transfer.download.start"); err != nil {
		cancel()
		return "", err
	}

	s.runner.launchWithQueuedOutcome(jobCtx, job.ID, req.BookmarkID, releasePins, func(runCtx context.Context) {
		s.runLocal(runCtx, job.ID, req.BookmarkID, job.Counters, func(innerCtx context.Context, onProgress func(done, total, bytes int64)) (*local.Report, error) {
			return local.Download(innerCtx, session, engine, "aeronautes_src:"+req.Container, req.Container, req.Prefix, destDir, job.ID, preflight, onProgress)
		})
	}, func(cancelCtx context.Context) {
		s.finishLocal(cancelCtx, job.ID, req.BookmarkID, job.Counters, jobs.StateCancelled, "")
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
		s.finishLocal(context.WithoutCancel(ctx), job.ID, bookmarkID, job.Counters, jobs.StateFailed, "start audit unavailable before first write: "+err.Error())
		return fmt.Errorf("start audit unavailable: %w", err)
	}
	return nil
}

func (s *TransferService) finishLocal(ctx context.Context, jobID, bookmarkID string, counters jobs.Counters, state jobs.State, message string) {
	// A completed body must no longer accept cancellation, even when
	// persistence fails and the shared store reports unavailability.
	s.runner.release(jobID)
	err := s.jobs.Finish(ctx, jobID, jobs.Completion{
		State: state, Counters: counters, Error: message,
		Audit: audit.Event{BookmarkID: bookmarkID, Operation: "transfer.local." + string(state), Resource: "job " + jobID, Result: terminalAuditResult(state)},
	}, s.runner.audit)
	if err != nil {
		log.Printf("job %s terminal outcome was not saved: %v", jobID, err)
	}
}

// runLocal is the shared local-transfer job goroutine: preflight was
// the Planning half; execution verifies per object as it goes (§27.5),
// so the lifecycle is Planning → Running → Verifying → terminal with
// verification counted from the per-object checks.
func (s *TransferService) runLocal(ctx context.Context, jobID, bookmarkID string, base jobs.Counters,
	run func(ctx context.Context, onProgress func(done, total, bytes int64)) (*local.Report, error)) {

	background := context.WithoutCancel(ctx)
	counters := base
	finish := func(state jobs.State, message string) {
		s.finishLocal(background, jobID, bookmarkID, counters, state, message)
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
	if report != nil {
		counters.ObjectsCopied = report.Done
		counters.ObjectsVerified = report.Done
		counters.BytesCopied = report.Bytes
		counters.NativeRouted = report.NativeRouted
		counters.ObjectsFailed = int64(len(report.Failures))
	}
	switch {
	case report != nil && report.PostCommitIssues > 0:
		message := fmt.Sprintf("%d post-commit issues", report.PostCommitIssues)
		if len(report.PostCommitIssueSamples) > 0 {
			first := report.PostCommitIssueSamples[0]
			message += fmt.Sprintf(" (first: %s: %s: %s)", first.Name, first.Stage, first.Err)
		}
		if len(report.Failures) > 0 {
			message += fmt.Sprintf("; %d failed (first: %s: %s)", len(report.Failures), report.Failures[0].Name, report.Failures[0].Err)
		}
		if err != nil {
			message += "; " + err.Error()
		}
		finish(jobs.StatePartial, message)
	case err != nil && report != nil && (report.Done > 0 || len(report.Failures) > 0):
		message := err.Error()
		if len(report.Failures) > 0 {
			message = fmt.Sprintf("%d failed (first: %s: %s); %s",
				len(report.Failures), report.Failures[0].Name, report.Failures[0].Err, message)
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
				message += fmt.Sprintf("; %d failed (first: %s: %s)", len(report.Failures), report.Failures[0].Name, report.Failures[0].Err)
			}
			finish(jobs.StatePartial, message)
			return
		}
		if len(report.Failures) > 0 {
			finish(jobs.StatePartial, fmt.Sprintf("%d failed (first: %s: %s)",
				len(report.Failures), report.Failures[0].Name, report.Failures[0].Err))
		} else {
			finish(jobs.StateComplete, "")
		}
	}
}
