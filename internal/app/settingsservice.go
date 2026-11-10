package app

import (
	"context"
	"database/sql"
	"fmt"
	"github.com/csmart/aeronautes/internal/applog"
	"github.com/csmart/aeronautes/internal/swiftcontrol"
	"strconv"
	"sync"
	"time"

	"github.com/csmart/aeronautes/internal/audit"
	"github.com/csmart/aeronautes/internal/db"
	"github.com/csmart/aeronautes/internal/jobs"
)

// SettingsService is the minimal §38/§40 application-settings surface:
// the active-job budget and the default performance profile, persisted
// in the typed app_settings KV. Bookmark-level budgets are deferred to
// the full settings screen (§44) — stated here, not silently missing.
type SettingsService struct {
	db     *db.DB
	runner *jobRunner
	// saving is held across a save's write and its application to the
	// runner, so that two saves cannot leave the runner on one budget and
	// the store on the other.
	saving sync.Mutex
}

// NewSettingsService wires the service; it applies the persisted
// budget to the runner at construction.
func NewSettingsService(database *db.DB, runner *jobRunner) *SettingsService {
	s := &SettingsService{db: database, runner: runner}
	settings, err := s.Get(context.Background())
	if err != nil {
		// The runner keeps the defaults it was built with. Said aloud,
		// because a budget that quietly differs from the stored one cannot
		// be explained afterwards.
		applog.Errorf("the settings could not be read at startup, so the job budget and the queue depth are the defaults: %v", err)
		return s
	}
	runner.setMaxActiveJobs(settings.MaxActiveJobs)
	runner.setMaxQueuedJobs(settings.MaxQueuedJobs)
	return s
}

// runRetention applies the §51 prune per the persisted policy: audit
// events and terminal jobs past their windows (0 disables either).
// Non-terminal jobs and jobs holding crash-recovery records survive
// regardless.
func (s *SettingsService) runRetention(ctx context.Context, jobStore *jobs.Store, auditStore *audit.Store) {
	settings, err := s.Get(ctx)
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
	// ReclaimReplacedFragments says an acknowledged overwrite of a large
	// object reclaims the predecessor's own recorded segments once the
	// replacement is verified (ADR 0081 decision 5). It defaults to
	// reclaiming, which is what the operator asked for.
	// It is an ordinary stored setting although it qualifies a deletion,
	// because the only direction a page can move it unattended is towards
	// reclaiming LESS: the operation it qualifies is already gated by
	// decision 2's native confirmation, which no page can answer, and that
	// confirmation states what will be removed.
	ReclaimReplacedFragments bool `json:"reclaimReplacedFragments"`
	// RailWidthPx is the width the operator dragged the bookmark rail to,
	// in CSS pixels; 0 means the layout's own default. It lives with the
	// settings because the webview's own storage is not guaranteed to
	// survive a restart, and a width that resets on every launch is not
	// a setting.
	RailWidthPx int `json:"railWidthPx"`
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
		// Defaulting to true is safe against a missing row, because Get
		// starts from these defaults and overrides only keys the table
		// actually holds; an absent setting is the default rather than the
		// zero value.
		ReclaimReplacedFragments: true,
	}
}

// Get returns the persisted settings with defaults filled in.
func (s *SettingsService) Get(ctx context.Context) (AppSettingsDTO, error) {
	return s.getFrom(ctx, s.db.Reader())
}

// getFrom reads the application settings through a caller-supplied querier,
// so the settings backup can take them inside its own read-transaction
// snapshot (ADR 0098) rather than on a separate pooled connection.
func (s *SettingsService) getFrom(ctx context.Context, q dbQuerier) (AppSettingsDTO, error) {
	settings := defaults()
	rows, err := q.QueryContext(ctx, `SELECT key, value FROM app_settings`)
	if err != nil {
		return settings, err
	}
	defer rows.Close()
	for rows.Next() {
		var key, value string
		if err := rows.Scan(&key, &value); err != nil {
			return settings, err
		}
		switch key {
		case "max_active_jobs":
			if n, err := strconv.Atoi(value); err == nil {
				settings.MaxActiveJobs = n
			}
		case "default_profile":
			settings.DefaultProfile = value
		case "audit_retention_days":
			if n, err := strconv.Atoi(value); err == nil {
				settings.AuditRetentionDays = n
			}
		case "job_retention_days":
			if n, err := strconv.Atoi(value); err == nil {
				settings.JobRetentionDays = n
			}
		case "download_object_ceiling":
			if n, err := strconv.Atoi(value); err == nil {
				settings.DownloadObjectCeiling = n
			}
		case "browser_retention_ceiling":
			if n, err := strconv.Atoi(value); err == nil {
				settings.BrowserRetentionCeiling = n
			}
		case "max_queued_jobs":
			if n, err := strconv.Atoi(value); err == nil {
				settings.MaxQueuedJobs = n
			}
		case "max_conns_per_host":
			if n, err := strconv.Atoi(value); err == nil {
				settings.MaxConnsPerHost = n
			}
		case "reclaim_replaced_fragments":
			if on, err := strconv.ParseBool(value); err == nil {
				settings.ReclaimReplacedFragments = on
			}
		case "rail_width_px":
			if n, err := strconv.Atoi(value); err == nil {
				settings.RailWidthPx = n
			}
		}
	}
	if err := rows.Err(); err != nil {
		return settings, err
	}
	// Report what will happen, not what is stored. A value persisted
	// before the ceiling was enforced must not be read back as though it
	// still applied; the stored row is deliberately left as written so a
	// later revert restores the operator's earlier intent (ADR 0047). A
	// stored value below one reads as the default the runner applies.
	if settings.MaxActiveJobs > maxActiveJobsCeiling {
		settings.MaxActiveJobs = maxActiveJobsCeiling
	}
	if settings.MaxActiveJobs < 1 {
		settings.MaxActiveJobs = defaultMaxActiveJobs
	}
	// A stored ceiling outside the range (or none) reads back as the
	// default the preflight will actually apply.
	if settings.DownloadObjectCeiling < minDownloadObjectCeiling || settings.DownloadObjectCeiling > maxDownloadObjectCeiling {
		settings.DownloadObjectCeiling = defaultDownloadObjectCeiling
	}
	if settings.BrowserRetentionCeiling < minBrowserRetentionCeiling || settings.BrowserRetentionCeiling > maxBrowserRetentionCeiling {
		settings.BrowserRetentionCeiling = defaultBrowserRetentionCeiling
	}
	if settings.MaxQueuedJobs < minMaxQueuedJobs || settings.MaxQueuedJobs > maxMaxQueuedJobs {
		settings.MaxQueuedJobs = defaultMaxQueuedJobs
	}
	if settings.MaxConnsPerHost < minMaxConnsPerHost || settings.MaxConnsPerHost > maxMaxConnsPerHost {
		settings.MaxConnsPerHost = defaultMaxConnsPerHost
	}
	// A stored width the range no longer admits reads back as the default.
	if settings.RailWidthPx != 0 && (settings.RailWidthPx < minRailWidthPx || settings.RailWidthPx > maxRailWidthPx) {
		settings.RailWidthPx = 0
	}
	return settings, nil
}

// Set validates, persists, and applies the settings.
func (s *SettingsService) Set(ctx context.Context, settings AppSettingsDTO) (AppSettingsDTO, error) {
	// Refused rather than clamped: a control that appears to change
	// something it cannot is worse than no control (ADR 0047). One range
	// and one sentence (ADR 0105 decision 7).
	if settings.MaxActiveJobs < 1 || settings.MaxActiveJobs > maxActiveJobsCeiling {
		return AppSettingsDTO{}, fmt.Errorf("max active jobs %d is out of range: choose 1 to %d", settings.MaxActiveJobs, maxActiveJobsCeiling)
	}
	if _, err := profileBase(Profile(settings.DefaultProfile)); err != nil {
		return AppSettingsDTO{}, err
	}
	if settings.AuditRetentionDays < 0 || settings.AuditRetentionDays > 3650 ||
		settings.JobRetentionDays < 0 || settings.JobRetentionDays > 3650 {
		return AppSettingsDTO{}, fmt.Errorf("retention days out of range 0..3650")
	}
	// A document without the ceiling (an older client, or a test that
	// names only what it changes) keeps the default rather than refusing.
	if settings.DownloadObjectCeiling == 0 {
		settings.DownloadObjectCeiling = defaultDownloadObjectCeiling
	}
	if settings.DownloadObjectCeiling < minDownloadObjectCeiling || settings.DownloadObjectCeiling > maxDownloadObjectCeiling {
		return AppSettingsDTO{}, fmt.Errorf("download object ceiling %d out of range %d..%d", settings.DownloadObjectCeiling, minDownloadObjectCeiling, maxDownloadObjectCeiling)
	}
	if settings.BrowserRetentionCeiling == 0 {
		settings.BrowserRetentionCeiling = defaultBrowserRetentionCeiling
	}
	if settings.BrowserRetentionCeiling < minBrowserRetentionCeiling || settings.BrowserRetentionCeiling > maxBrowserRetentionCeiling {
		return AppSettingsDTO{}, fmt.Errorf("browser retention ceiling %d out of range %d..%d", settings.BrowserRetentionCeiling, minBrowserRetentionCeiling, maxBrowserRetentionCeiling)
	}
	if settings.MaxQueuedJobs == 0 {
		settings.MaxQueuedJobs = defaultMaxQueuedJobs
	}
	if settings.MaxQueuedJobs < minMaxQueuedJobs || settings.MaxQueuedJobs > maxMaxQueuedJobs {
		return AppSettingsDTO{}, fmt.Errorf("max queued jobs %d out of range %d..%d", settings.MaxQueuedJobs, minMaxQueuedJobs, maxMaxQueuedJobs)
	}
	if settings.MaxConnsPerHost == 0 {
		settings.MaxConnsPerHost = defaultMaxConnsPerHost
	}
	if settings.MaxConnsPerHost < minMaxConnsPerHost || settings.MaxConnsPerHost > maxMaxConnsPerHost {
		return AppSettingsDTO{}, fmt.Errorf("max connections per host %d out of range %d..%d", settings.MaxConnsPerHost, minMaxConnsPerHost, maxMaxConnsPerHost)
	}
	if settings.RailWidthPx != 0 && (settings.RailWidthPx < minRailWidthPx || settings.RailWidthPx > maxRailWidthPx) {
		return AppSettingsDTO{}, fmt.Errorf("rail width %d px out of range %d..%d (0 for the default)", settings.RailWidthPx, minRailWidthPx, maxRailWidthPx)
	}
	s.saving.Lock()
	defer s.saving.Unlock()
	err := s.db.WriteTx(ctx, func(tx *sql.Tx) error {
		for key, value := range map[string]string{
			"max_active_jobs":           strconv.Itoa(settings.MaxActiveJobs),
			"default_profile":           settings.DefaultProfile,
			"audit_retention_days":      strconv.Itoa(settings.AuditRetentionDays),
			"job_retention_days":        strconv.Itoa(settings.JobRetentionDays),
			"download_object_ceiling":   strconv.Itoa(settings.DownloadObjectCeiling),
			"browser_retention_ceiling": strconv.Itoa(settings.BrowserRetentionCeiling),
			"max_queued_jobs":           strconv.Itoa(settings.MaxQueuedJobs),
			"max_conns_per_host":        strconv.Itoa(settings.MaxConnsPerHost),
			// Written explicitly either way, so an operator who turns
			// reclaiming off has a row saying so rather than relying on the
			// absence of one.
			"reclaim_replaced_fragments": strconv.FormatBool(settings.ReclaimReplacedFragments),
			"rail_width_px":              strconv.Itoa(settings.RailWidthPx),
		} {
			if _, err := tx.ExecContext(ctx,
				`INSERT INTO app_settings (key, value) VALUES (?, ?)
				 ON CONFLICT(key) DO UPDATE SET value=excluded.value`, key, value); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return AppSettingsDTO{}, err
	}
	s.runner.setMaxActiveJobs(settings.MaxActiveJobs)
	s.runner.setMaxQueuedJobs(settings.MaxQueuedJobs)
	return settings, nil
}
