package app

import (
	"context"
	"errors"
	"fmt"
	"log"

	"github.com/csmart/aeronautes/internal/audit"
	"github.com/csmart/aeronautes/internal/jobs"
	"github.com/csmart/aeronautes/internal/swiftcontrol"
	"github.com/csmart/aeronautes/internal/transfer/local"
	"github.com/csmart/aeronautes/internal/transfer/rclone"
	"github.com/wailsapp/wails/v3/pkg/application"
)

// The local↔Swift half of TransferService (§27.5): preflight is a
// separate, complete, read-only step the UI must show before Start; the
// v1 policy rejects the whole operation on any collision.

// UploadRequestDTO scopes a multi-file upload.
type UploadRequestDTO struct {
	BookmarkID string   `json:"bookmarkId"`
	Container  string   `json:"container"`
	Prefix     string   `json:"prefix"`
	Paths      []string `json:"paths"`
	// Settings are the per-job §44.1 overrides.
	Settings TransferSettingsDTO `json:"settings"`
}

// DownloadRequestDTO scopes a prefix download.
type DownloadRequestDTO struct {
	BookmarkID string `json:"bookmarkId"`
	Container  string `json:"container"`
	Prefix     string `json:"prefix"`
	DestDir    string `json:"destDir"`
	// Settings are the per-job §44.1 overrides.
	Settings TransferSettingsDTO `json:"settings"`
}

// PickUploadFiles opens the OS multi-file picker (Go side: the webview
// never sees filesystem paths otherwise). Empty when cancelled.
func (s *TransferService) PickUploadFiles(ctx context.Context) ([]string, error) {
	app := application.Get()
	if app == nil {
		return nil, errors.New("no application shell (headless)")
	}
	paths, err := app.Dialog.OpenFile().
		CanChooseFiles(true).
		SetTitle("Choose files to upload").
		PromptForMultipleSelection()
	if err != nil {
		return nil, err
	}
	return paths, nil
}

// PickDownloadDir opens the OS directory picker. "" when cancelled.
func (s *TransferService) PickDownloadDir(ctx context.Context) (string, error) {
	app := application.Get()
	if app == nil {
		return "", errors.New("no application shell (headless)")
	}
	return app.Dialog.OpenFile().
		CanChooseDirectories(true).
		CanChooseFiles(false).
		SetTitle("Choose a destination directory").
		PromptForSingleSelection()
}

// PreflightUpload runs the complete §27.5 pre-write report.
func (s *TransferService) PreflightUpload(ctx context.Context, req UploadRequestDTO) (*local.UploadPreflight, error) {
	if req.BookmarkID == "" || req.Container == "" || len(req.Paths) == 0 {
		return nil, errors.New("bookmark, container, and at least one file are required")
	}
	session, err := s.connections.SessionFor(ctx, req.BookmarkID)
	if err != nil {
		return nil, err
	}
	return local.PreflightUpload(ctx, session, req.Container, req.Prefix, req.Paths)
}

// PreflightDownload runs the complete §27.5 materialization report.
func (s *TransferService) PreflightDownload(ctx context.Context, req DownloadRequestDTO) (*local.DownloadPreflight, error) {
	if req.BookmarkID == "" || req.Container == "" || req.DestDir == "" {
		return nil, errors.New("bookmark, container, and a destination directory are required")
	}
	session, err := s.connections.SessionFor(ctx, req.BookmarkID)
	if err != nil {
		return nil, err
	}
	return local.PreflightDownload(ctx, session, req.Container, req.Prefix, req.DestDir)
}

// localEngine builds the single-remote engine for the bookmark's end.
func (s *TransferService) localEngine(ctx context.Context, bookmarkID string, session *swiftcontrol.Session, remoteName string, settings TransferSettingsDTO) (local.Engine, error) {
	insecure, err := s.connections.TLSInsecure(ctx, bookmarkID)
	if err != nil {
		return nil, err
	}
	remotes := []rclone.Remote{{
		Name: remoteName, StorageURL: session.StorageURL(), Token: session.AuthToken(), InsecureTLS: insecure,
	}}
	if s.engineFactory != nil {
		return s.engineFactory(remotes, settings.options())
	}
	runner, err := rclone.NewRunner(settings.options())
	if err != nil {
		return nil, err
	}
	return boundLocal{runner: runner, remotes: remotes}, nil
}

// boundLocal adapts the Runner to local.Engine.
type boundLocal struct {
	runner  *rclone.Runner
	remotes []rclone.Remote
}

func (b boundLocal) Copy(ctx context.Context, src, dst string, names []string, onEvent func(rclone.Event)) (*rclone.Result, error) {
	return b.runner.Copy(ctx, b.remotes, src, dst, names, onEvent)
}

// StartUpload re-runs the preflight (state may have moved since the UI
// showed it), rejects on any collision, and launches the job.
func (s *TransferService) StartUpload(ctx context.Context, req UploadRequestDTO) (string, error) {
	session, err := s.connections.SessionFor(ctx, req.BookmarkID)
	if err != nil {
		return "", err
	}
	preflight, err := s.PreflightUpload(ctx, req)
	if err != nil {
		return "", err
	}
	if preflight.Rejected() {
		return "", errors.New("preflight found collisions; resolve them and retry (nothing was written)")
	}
	if err := req.Settings.Validate(); err != nil {
		return "", err
	}
	engine, err := s.localEngine(ctx, req.BookmarkID, session, "aeronautes_dst", req.Settings)
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
	s.record(ctx, req.BookmarkID, "transfer.upload.start", job.Source+" → "+job.Destination, "job "+job.ID+" · "+job.Settings, audit.ResultApplied)

	s.launch(jobCtx, job.ID, req.BookmarkID, func(runCtx context.Context) {
		s.runLocal(runCtx, job.ID, req.BookmarkID, job.Counters, func(innerCtx context.Context, onProgress func(done, total int64)) (*local.Report, error) {
			report, err := local.Upload(innerCtx, session, engine, "aeronautes_dst:"+req.Container, req.Container, req.Prefix, preflight, onProgress)
			if report != nil && err == nil && len(report.Failures) == 0 {
				// Every upload verified: the captured segment prefixes
				// are job-written and now referenced — record ownership.
				s.recordOwnership(context.WithoutCancel(innerCtx), req.BookmarkID, job.ID, report.CapturedPrefixes)
			}
			return report, err
		})
	})
	return job.ID, nil
}

// StartDownload mirrors StartUpload for materialization.
func (s *TransferService) StartDownload(ctx context.Context, req DownloadRequestDTO) (string, error) {
	session, err := s.connections.SessionFor(ctx, req.BookmarkID)
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
	engine, err := s.localEngine(ctx, req.BookmarkID, session, "aeronautes_src", req.Settings)
	if err != nil {
		return "", fmt.Errorf("transfer engine: %w", err)
	}
	job, err := s.jobs.Create(ctx, jobs.Job{
		Type:             jobs.TypeDownload,
		Settings:         req.Settings.effectiveJSON(),
		Source:           req.Container + "/" + req.Prefix,
		Destination:      req.DestDir,
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
	s.record(ctx, req.BookmarkID, "transfer.download.start", job.Source+" → "+job.Destination, "job "+job.ID+" · "+job.Settings, audit.ResultApplied)

	s.launch(jobCtx, job.ID, req.BookmarkID, func(runCtx context.Context) {
		s.runLocal(runCtx, job.ID, req.BookmarkID, job.Counters, func(innerCtx context.Context, onProgress func(done, total int64)) (*local.Report, error) {
			return local.Download(innerCtx, session, engine, "aeronautes_src:"+req.Container, req.Container, req.Prefix, req.DestDir, preflight, onProgress)
		})
	})
	return job.ID, nil
}

// runLocal is the shared local-transfer job goroutine: preflight was
// the Planning half; execution verifies per object as it goes (§27.5),
// so the lifecycle is Planning → Running → Verifying → terminal with
// verification counted from the per-object checks.
func (s *TransferService) runLocal(ctx context.Context, jobID, bookmarkID string, base jobs.Counters,
	run func(ctx context.Context, onProgress func(done, total int64)) (*local.Report, error)) {

	background := context.WithoutCancel(ctx)
	transition := func(state jobs.State) {
		if err := s.jobs.Transition(background, jobID, state); err != nil {
			log.Printf("job %s: %v", jobID, err)
		}
	}
	transition(jobs.StatePlanning)
	transition(jobs.StateRunning)

	counters := base
	report, err := run(ctx, func(done, total int64) {
		// Progress flushes are best-effort; the terminal flush is exact.
		counters.ObjectsCopied = done
		_ = s.jobs.UpdateCounters(background, jobID, counters)
	})
	if report != nil {
		counters.ObjectsCopied = report.Done
		counters.ObjectsVerified = report.Done
		counters.BytesCopied = report.Bytes
		counters.NativeRouted = report.NativeRouted
		counters.ObjectsFailed = int64(len(report.Failures))
	}
	_ = s.jobs.UpdateCounters(background, jobID, counters)

	finish := func(state jobs.State, message string) {
		if message != "" {
			_ = s.jobs.SetError(background, jobID, message)
		}
		transition(state)
		s.record(background, bookmarkID, "transfer.local."+string(state), "job "+jobID, "", terminalAuditResult(state))
		s.runner.release(jobID)
	}
	switch {
	case err != nil && errors.Is(err, context.Canceled):
		finish(jobs.StateCancelled, "")
	case err != nil:
		finish(jobs.StateFailed, err.Error())
	default:
		transition(jobs.StateVerifying)
		if len(report.Failures) > 0 {
			finish(jobs.StatePartial, fmt.Sprintf("%d failed (first: %s: %s)",
				len(report.Failures), report.Failures[0].Name, report.Failures[0].Err))
		} else {
			finish(jobs.StateComplete, "")
		}
	}
}
