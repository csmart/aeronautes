package app

import (
	"context"
	"database/sql"
	"fmt"
	"github.com/csmart/aeronautes/internal/swiftcontrol"
	"log"
	"strconv"
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
}

// NewSettingsService wires the service; it applies the persisted
// budget to the runner at construction.
func NewSettingsService(database *db.DB, runner *jobRunner) *SettingsService {
	s := &SettingsService{db: database, runner: runner}
	if settings, err := s.Get(context.Background()); err == nil {
		runner.setMaxActiveJobs(settings.MaxActiveJobs)
		runner.setMaxQueuedJobs(settings.MaxQueuedJobs)
	}
	return s
}

// runRetention applies the §51 prune per the persisted policy: audit
// events and terminal jobs past their windows (0 disables either).
// Non-terminal jobs and jobs holding crash-recovery records survive
// regardless.
func (s *SettingsService) runRetention(ctx context.Context, jobStore *jobs.Store, auditStore *audit.Store) {
	settings, err := s.Get(ctx)
	if err != nil {
		log.Printf("retention: read settings: %v", err)
		return
	}
	now := time.Now()
	if settings.AuditRetentionDays > 0 {
		if n, err := auditStore.Prune(ctx, now.AddDate(0, 0, -settings.AuditRetentionDays)); err != nil {
			log.Printf("retention: audit prune: %v", err)
		} else if n > 0 {
			log.Printf("retention: pruned %d audit events older than %d days", n, settings.AuditRetentionDays)
		}
	}
	if settings.JobRetentionDays > 0 {
		if n, err := jobStore.PruneTerminal(ctx, now.AddDate(0, 0, -settings.JobRetentionDays)); err != nil {
			log.Printf("retention: job prune: %v", err)
		} else if n > 0 {
			log.Printf("retention: pruned %d terminal jobs older than %d days", n, settings.JobRetentionDays)
		}
	}
}

// AppSettingsDTO is the whole application-settings document.
type AppSettingsDTO struct {
	// MaxActiveJobs is the §38 whole-application ceiling. For v1 it is
	// enforced at one (ADR 0047), so reads report the effective value and
	// a write above it is refused rather than stored with no effect.
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
	settings := defaults()
	rows, err := s.db.Reader().QueryContext(ctx, `SELECT key, value FROM app_settings`)
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
		}
	}
	if err := rows.Err(); err != nil {
		return settings, err
	}
	// Report what will happen, not what is stored. A value persisted
	// before the ceiling was enforced must not be read back as though it
	// still applied; the stored row is deliberately left as written so a
	// later revert restores the operator's earlier intent (ADR 0047).
	if settings.MaxActiveJobs > maxActiveJobsCeiling {
		settings.MaxActiveJobs = maxActiveJobsCeiling
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
	return settings, nil
}

// Set validates, persists, and applies the settings.
func (s *SettingsService) Set(ctx context.Context, settings AppSettingsDTO) (AppSettingsDTO, error) {
	if settings.MaxActiveJobs < 1 || settings.MaxActiveJobs > 16 {
		return AppSettingsDTO{}, fmt.Errorf("max active jobs %d out of range 1..16", settings.MaxActiveJobs)
	}
	// Refused rather than clamped: a control that appears to change
	// something it cannot is worse than no control (ADR 0047).
	if settings.MaxActiveJobs > maxActiveJobsCeiling {
		return AppSettingsDTO{}, fmt.Errorf(
			"max active jobs %d exceeds the enforced ceiling of %d",
			settings.MaxActiveJobs, maxActiveJobsCeiling)
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
