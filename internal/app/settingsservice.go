package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"time"

	"github.com/csmart/aeronautes/internal/applog"
	"github.com/csmart/aeronautes/internal/audit"
	"github.com/csmart/aeronautes/internal/jobs"
	"github.com/csmart/aeronautes/internal/swiftcontrol"
)

// SettingsService is the §38/§40 application-settings surface. The settings
// are one JSON object in a configuration file of their own, which an operator
// can read and edit by hand (ADR 0109 decision 1). Bookmark-level budgets are
// deferred to the full settings screen (§44) — stated here, not silently
// missing.
type SettingsService struct {
	path   string
	runner *jobRunner
	// saving is held across a save's write and its application to the
	// runner, so that two saves cannot leave the runner on one budget and
	// the file on the other, and across the backup's two reads (readAfter).
	saving sync.Mutex
}

// NewSettingsService wires the service over the settings file at path; it
// applies the budget in force to the runner at construction.
func NewSettingsService(path string, runner *jobRunner) *SettingsService {
	s := &SettingsService{path: path, runner: runner}
	settings, problem := s.read()
	if problem != nil {
		// Said aloud, because a budget that quietly differs from the one in
		// the file cannot be explained afterwards.
		applog.Errorf("%v", problem)
	}
	runner.setMaxActiveJobs(settings.MaxActiveJobs)
	runner.setMaxQueuedJobs(settings.MaxQueuedJobs)
	if err := applog.Start(settings.LogLevel, settings.LogToFile); err != nil {
		applog.Warnf("the log file could not be kept: %v", err)
	}
	return s
}

// runRetention applies the §51 prune per the persisted policy: audit
// events and terminal jobs past their windows (0 disables either).
// Non-terminal jobs and jobs holding crash-recovery records survive
// regardless. An unreadable settings file prunes nothing: the default windows
// in force are not the operator's, and a pruned record cannot come back.
func (s *SettingsService) runRetention(ctx context.Context, jobStore *jobs.Store, auditStore *audit.Store) {
	settings, err := s.read()
	if err != nil {
		applog.Warnf("retention: read settings: %v", err)
		return
	}
	now := time.Now()
	if settings.AuditRetentionDays > 0 {
		if n, err := auditStore.Prune(ctx, now.AddDate(0, 0, -settings.AuditRetentionDays)); err != nil {
			applog.Warnf("retention: audit prune: %v", err)
		} else if n > 0 {
			applog.Infof("retention: pruned %d audit events older than %d days", n, settings.AuditRetentionDays)
		}
	}
	if settings.JobRetentionDays > 0 {
		if n, err := jobStore.PruneTerminal(ctx, now.AddDate(0, 0, -settings.JobRetentionDays)); err != nil {
			applog.Warnf("retention: job prune: %v", err)
		} else if n > 0 {
			applog.Infof("retention: pruned %d terminal jobs older than %d days", n, settings.JobRetentionDays)
		}
	}
}

// AppSettingsDTO is the whole application-settings document.
type AppSettingsDTO struct {
	// MaxActiveJobs is how many jobs run at once (§38): one to four, two
	// by default (ADR 0105). Reads report the effective value, and a write
	// outside the range is refused rather than stored with no effect.
	MaxActiveJobs int `json:"maxActiveJobs"`
	// DefaultProfile seeds transfer dialogs (§40).
	DefaultProfile string `json:"defaultProfile"`
	// AuditRetentionDays / JobRetentionDays drive the §51 startup prune
	// (audit events; terminal jobs). 0 disables that prune.
	AuditRetentionDays int `json:"auditRetentionDays"`
	JobRetentionDays   int `json:"jobRetentionDays"`
	// DownloadObjectCeiling is the most objects one download selection may
	// hold; the preflight refuses a wider prefix once its count passes it,
	// naming the count and the ceiling, rather than truncating the plan
	// (ADR 0072 decision 4). The operator sets it; the default is the
	// peak-memory record's recommendation.
	DownloadObjectCeiling int `json:"downloadObjectCeiling"`
	// BrowserRetentionCeiling is the most rows a listing column keeps
	// loaded; past it the browser stops loading further pages, keeps every
	// row shown and says so in place of the next page (ADR 0074
	// decision 1). The operator sets it.
	BrowserRetentionCeiling int `json:"browserRetentionCeiling"`
	// MaxQueuedJobs is the most jobs that may wait behind the active
	// slots; a further start is refused with the count named rather than
	// queued without bound (ADR 0074 decision 2). The operator sets it.
	MaxQueuedJobs int `json:"maxQueuedJobs"`
	// MaxConnsPerHost caps the connections a native session opens to one
	// host, applied when the session is opened (ADR 0074 decision 4).
	// The operator sets it.
	MaxConnsPerHost int `json:"maxConnsPerHost"`
	// ReclaimReplacedFragments says the transfer engine may remove a
	// replaced large object's old segments as it writes the new object
	// (ADR 0122 decision 3). It defaults to removing them, which is what
	// the operator asked for; off refuses the engine's deletes, and a
	// container that keeps versions keeps the segments either way.
	// It is an ordinary stored setting although it qualifies a deletion,
	// because replacing is already gated by ADR 0081 decision 2's native
	// confirmation, which no page can answer, and that confirmation states
	// what happens to the segments.
	ReclaimReplacedFragments bool `json:"reclaimReplacedFragments"`
	// RailWidthPx is the width the operator dragged the bookmark rail to,
	// in CSS pixels; 0 means the layout's own default. It lives with the
	// settings because the webview's own storage is not guaranteed to
	// survive a restart, and a width that resets on every launch is not
	// a setting.
	RailWidthPx int `json:"railWidthPx"`
	// GPUAcceleration lets the window's webview draw with the graphics
	// card, as it does by default. Off draws in software, for a host whose
	// driver makes the window slow or blank. The webview is made once, at
	// start, so a change applies when the application next starts.
	GPUAcceleration bool `json:"gpuAcceleration"`
	// LogLevel is the lowest level the log writes: debug, info, warn or
	// error (ADR 0127 decision 2). A change applies at once.
	LogLevel string `json:"logLevel"`
	// LogToFile keeps the log in a file in the state folder as well as on
	// standard error, rotated at 10 MiB (ADR 0127 decision 3).
	LogToFile bool `json:"logToFile"`
	// Log reports the file's path and which launch switches are in force
	// beside the two settings above, which a read reports as in force
	// (ADR 0127 decision 4). It is a report only: a save ignores it and it
	// is never written to the file.
	Log *LogStatusDTO `json:"log,omitempty"`
	// FileProblem says why the settings file could not be read, when it
	// could not; the defaults are then in force until the next save (ADR
	// 0109 decision 1). It is a report only: a save ignores it and it is
	// never written to the file.
	FileProblem string `json:"fileProblem,omitempty"`
	// ResetToDefaults asks a save to store this version's defaults in place
	// of the document's other values (ADR 0126 decision 4). It rides on the
	// existing save so a reset is that one action, and it is never written
	// to the file.
	ResetToDefaults bool `json:"resetToDefaults,omitempty"`
}

// LogStatusDTO is the log in force: its level, the file Settings shows and
// whether it is being written, and which halves a launch switch set.
type LogStatusDTO struct {
	Level        string `json:"level"`
	FilePath     string `json:"filePath"`
	FileOn       bool   `json:"fileOn"`
	LevelFromEnv bool   `json:"levelFromEnv"`
	FileFromEnv  bool   `json:"fileFromEnv"`
}

// inForce reports the log as it is in force in the document's two log
// settings, which a launch switch can make differ from the stored ones, with
// the file's path and the switches beside them (ADR 0127 decision 4).
func inForce(settings AppSettingsDTO) AppSettingsDTO {
	now := applog.Current()
	settings.LogLevel, settings.LogToFile = now.Level, now.FileOn
	settings.Log = &LogStatusDTO{Level: now.Level, FilePath: now.FilePath, FileOn: now.FileOn, LevelFromEnv: now.LevelFromEnv, FileFromEnv: now.FileFromEnv}
	return settings
}

// defaultDownloadObjectCeiling is 250,000 objects: about 352 MiB of peak
// resident memory for the inventory on the measured host (the 22
// September 2026 peak-memory record).
const defaultDownloadObjectCeiling = 250_000

// The ceiling's range: below a thousand objects the refusal would fire on
// ordinary prefixes; above five million the measured inventory cost passes
// six gibibytes, which no setting should quietly permit.
const (
	minDownloadObjectCeiling = 1_000
	maxDownloadObjectCeiling = 5_000_000
)

// The browser retention ceiling: 100,000 rows by default, the value ADR
// 0074 proposed and the operator accepted; the range follows the download
// ceiling's, since both bound what one selection or column holds.
const (
	defaultBrowserRetentionCeiling = 100_000
	minBrowserRetentionCeiling     = 1_000
	maxBrowserRetentionCeiling     = 5_000_000
)

// The queue depth: sixteen waiting jobs by default (ADR 0074 decision 2);
// one is the least a queue can hold and a thousand more than any
// interactive use queues.
const (
	defaultMaxQueuedJobs = 16
	minMaxQueuedJobs     = 1
	maxMaxQueuedJobs     = 1_000
)

// The native client's per-host connection cap: the session package's
// default (ADR 0074 decision 4); one is a serial client and 256 is more
// than any endpoint budget here would grant one session.
const (
	defaultMaxConnsPerHost = swiftcontrol.DefaultMaxConnsPerHost
	minMaxConnsPerHost     = 1
	maxMaxConnsPerHost     = 256
)

// The bookmark rail's dragged width: below 64 pixels the account badge no
// longer fits, above 480 the rail crowds out the panes beside it.
const (
	minRailWidthPx = 64
	maxRailWidthPx = 480
)

func defaults() AppSettingsDTO {
	return AppSettingsDTO{
		MaxActiveJobs: defaultMaxActiveJobs, DefaultProfile: string(ProfileAuto),
		AuditRetentionDays: 90, JobRetentionDays: 30,
		DownloadObjectCeiling:   defaultDownloadObjectCeiling,
		BrowserRetentionCeiling: defaultBrowserRetentionCeiling,
		MaxQueuedJobs:           defaultMaxQueuedJobs,
		MaxConnsPerHost:         defaultMaxConnsPerHost,
		// Defaulting to true is safe against a key the file leaves out,
		// because read starts from these defaults and overrides only keys
		// the file actually holds; an absent setting is the default rather
		// than the zero value.
		ReclaimReplacedFragments: true,
		GPUAcceleration:          true,
		LogLevel:                 "info",
	}
}

// Get returns the settings in force. A settings file that could not be read is
// reported in FileProblem rather than as an error, because the application
// runs on the defaults and the Settings view says why.
func (s *SettingsService) Get(context.Context) (AppSettingsDTO, error) {
	settings, problem := s.read()
	if problem != nil {
		settings.FileProblem = problem.Error()
	}
	return inForce(settings), nil
}

// read returns the settings in force: the keys the file holds over the
// defaults, or the defaults alone when there is no file. A file that cannot be
// used whole is not used at all, so a bad hand edit is never half-applied, and
// the error says why.
func (s *SettingsService) read() (AppSettingsDTO, error) {
	data, err := os.ReadFile(s.path)
	if errors.Is(err, fs.ErrNotExist) {
		return defaults(), nil
	}
	if err != nil {
		return defaults(), s.unreadable(err)
	}
	// Unmarshal sets only the keys present, so a key left out keeps its
	// default rather than becoming the zero value: reclaiming stays on in a
	// file that never mentions it.
	settings := defaults()
	if err := json.Unmarshal(data, &settings); err != nil {
		return defaults(), s.unreadable(err)
	}
	settings.FileProblem = ""
	checked, err := checkSettings(settings)
	if err != nil {
		return defaults(), s.unreadable(err)
	}
	return checked, nil
}

func (s *SettingsService) unreadable(cause error) error {
	return fmt.Errorf("the settings file %s could not be read, so the defaults are in force; saving the settings keeps that file as %s: %w",
		s.path, filepath.Base(s.path)+".unreadable", cause)
}

// readAfter runs snapshot and then reads the settings, holding off every
// settings save until both are done, so what snapshot reads elsewhere pairs
// with settings that existed at the same moment (ADR 0109 decision 5). A file
// that could not be read refuses, because the defaults in force are not the
// operator's settings.
func (s *SettingsService) readAfter(snapshot func() error) (AppSettingsDTO, error) {
	s.saving.Lock()
	defer s.saving.Unlock()
	if err := snapshot(); err != nil {
		return AppSettingsDTO{}, err
	}
	settings, problem := s.read()
	if problem != nil {
		return AppSettingsDTO{}, problem
	}
	return settings, nil
}

// settingsRefusal is a save refused before anything was written. The bridge
// hands a returned error's exported fields to the window as the rejection's
// cause, and Refused is how the window knows that this save stored and
// applied nothing, so that what it read before is still current. Any other
// failure, a write that did not finish or a panic in this method, carries no
// such field, and the window treats its outcome as unknown until it reads the
// settings again.
type settingsRefusal struct {
	Refused bool   `json:"refused"`
	Reason  string `json:"reason"`
}

func (e *settingsRefusal) Error() string { return e.Reason }

func refuseSettings(format string, args ...any) error {
	return &settingsRefusal{Refused: true, Reason: fmt.Sprintf(format, args...)}
}

// Set validates, persists, and applies the settings.
func (s *SettingsService) Set(_ context.Context, settings AppSettingsDTO) (AppSettingsDTO, error) {
	return s.save(settings, false)
}

// restore saves a backup's settings whole: each log setting is stored as the
// backup holds it, even where it matches what a launch switch put in force.
func (s *SettingsService) restore(settings AppSettingsDTO) (AppSettingsDTO, error) {
	return s.save(settings, true)
}

func (s *SettingsService) save(settings AppSettingsDTO, restoring bool) (AppSettingsDTO, error) {
	if settings.ResetToDefaults {
		// Only the settings file changes: bookmarks, saved keys, jobs and
		// history are kept elsewhere and are not touched.
		settings = defaults()
	}
	settings, err := checkSettings(settings)
	if err != nil {
		return AppSettingsDTO{}, err
	}
	settings.FileProblem = ""
	settings.Log = nil
	s.saving.Lock()
	defer s.saving.Unlock()
	// The page shows the log in force, so a log setting that differs from
	// it is the operator's change and applies at once, over a launch switch
	// too; one that matches it is left alone and keeps its stored value, so
	// saving another setting neither undoes a launch switch nor saves its
	// value as the operator's (ADR 0127 decision 4). A file that cannot be
	// opened refuses the save.
	saved, _ := s.read()
	now := applog.Current()
	if settings.LogToFile != now.FileOn {
		if err := applog.KeepFile(settings.LogToFile); err != nil {
			return AppSettingsDTO{}, refuseSettings("%v", err)
		}
	} else if !restoring {
		settings.LogToFile = saved.LogToFile
	}
	if settings.LogLevel != now.Level {
		applog.SetLevel(settings.LogLevel)
	} else if !restoring {
		settings.LogLevel = saved.LogLevel
	}
	if err := s.write(settings); err != nil {
		return AppSettingsDTO{}, err
	}
	s.runner.setMaxActiveJobs(settings.MaxActiveJobs)
	s.runner.setMaxQueuedJobs(settings.MaxQueuedJobs)
	return inForce(settings), nil
}

// checkSettings refuses a value no setting accepts and fills the default for a
// ceiling left at zero; a save and a read of the file judge values alike.
func checkSettings(settings AppSettingsDTO) (AppSettingsDTO, error) {
	// Refused rather than clamped: a control that appears to change
	// something it cannot is worse than no control (ADR 0047). One range
	// and one sentence (ADR 0105 decision 7).
	if settings.MaxActiveJobs < 1 || settings.MaxActiveJobs > maxActiveJobsCeiling {
		return AppSettingsDTO{}, refuseSettings("max active jobs %d is out of range: choose 1 to %d", settings.MaxActiveJobs, maxActiveJobsCeiling)
	}
	if _, err := profileBase(Profile(settings.DefaultProfile)); err != nil {
		return AppSettingsDTO{}, refuseSettings("%v", err)
	}
	if settings.AuditRetentionDays < 0 || settings.AuditRetentionDays > 3650 ||
		settings.JobRetentionDays < 0 || settings.JobRetentionDays > 3650 {
		return AppSettingsDTO{}, refuseSettings("retention days out of range 0..3650")
	}
	// A document without the ceiling (an older client, or a test that
	// names only what it changes) keeps the default rather than refusing.
	if settings.DownloadObjectCeiling == 0 {
		settings.DownloadObjectCeiling = defaultDownloadObjectCeiling
	}
	if settings.DownloadObjectCeiling < minDownloadObjectCeiling || settings.DownloadObjectCeiling > maxDownloadObjectCeiling {
		return AppSettingsDTO{}, refuseSettings("download object ceiling %d out of range %d..%d", settings.DownloadObjectCeiling, minDownloadObjectCeiling, maxDownloadObjectCeiling)
	}
	if settings.BrowserRetentionCeiling == 0 {
		settings.BrowserRetentionCeiling = defaultBrowserRetentionCeiling
	}
	if settings.BrowserRetentionCeiling < minBrowserRetentionCeiling || settings.BrowserRetentionCeiling > maxBrowserRetentionCeiling {
		return AppSettingsDTO{}, refuseSettings("browser retention ceiling %d out of range %d..%d", settings.BrowserRetentionCeiling, minBrowserRetentionCeiling, maxBrowserRetentionCeiling)
	}
	if settings.MaxQueuedJobs == 0 {
		settings.MaxQueuedJobs = defaultMaxQueuedJobs
	}
	if settings.MaxQueuedJobs < minMaxQueuedJobs || settings.MaxQueuedJobs > maxMaxQueuedJobs {
		return AppSettingsDTO{}, refuseSettings("max queued jobs %d out of range %d..%d", settings.MaxQueuedJobs, minMaxQueuedJobs, maxMaxQueuedJobs)
	}
	if settings.MaxConnsPerHost == 0 {
		settings.MaxConnsPerHost = defaultMaxConnsPerHost
	}
	if settings.MaxConnsPerHost < minMaxConnsPerHost || settings.MaxConnsPerHost > maxMaxConnsPerHost {
		return AppSettingsDTO{}, refuseSettings("max connections per host %d out of range %d..%d", settings.MaxConnsPerHost, minMaxConnsPerHost, maxMaxConnsPerHost)
	}
	switch settings.LogLevel {
	case "":
		settings.LogLevel = "info"
	case "debug", "info", "warn", "error":
	default:
		return AppSettingsDTO{}, refuseSettings("log level %q is not one of debug, info, warn or error", settings.LogLevel)
	}
	if settings.RailWidthPx != 0 && (settings.RailWidthPx < minRailWidthPx || settings.RailWidthPx > maxRailWidthPx) {
		return AppSettingsDTO{}, refuseSettings("rail width %d px out of range %d..%d (0 for the default)", settings.RailWidthPx, minRailWidthPx, maxRailWidthPx)
	}
	return settings, nil
}

// write replaces the settings file whole: a temporary file in the same
// directory, private from creation, flushed and renamed over the old one, then
// the directory flushed, so a crash leaves the old file or the new one and
// never a mixture. A file that could not be read is first kept beside it as
// settings.json.unreadable, so a save never destroys a hand edit that went
// wrong (ADR 0109 decision 1).
func (s *SettingsService) write(settings AppSettingsDTO) error {
	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return err
	}
	// CreateTemp opens the file with mode 0600, so the settings are never
	// readable by others, even before the rename.
	file, err := os.CreateTemp(dir, ".settings-*.json")
	if err != nil {
		return err
	}
	temporary := file.Name()
	renamed := false
	defer func() {
		if !renamed {
			os.Remove(temporary)
		}
	}()
	if _, err := file.Write(append(data, '\n')); err != nil {
		file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	if _, problem := s.read(); problem != nil {
		if err := os.Rename(s.path, s.path+".unreadable"); err != nil {
			return fmt.Errorf("keep the unreadable settings file: %w", err)
		}
	}
	if err := os.Rename(temporary, s.path); err != nil {
		return err
	}
	renamed = true
	return syncDirectory(dir)
}

// syncDirectory flushes a directory, so a rename in it survives a crash.
// Windows cannot flush a directory handle and journals the rename itself.
func syncDirectory(dir string) error {
	if runtime.GOOS == "windows" {
		return nil
	}
	handle, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer handle.Close()
	return handle.Sync()
}
