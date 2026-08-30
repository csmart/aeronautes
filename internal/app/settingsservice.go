package app

import (
	"context"
	"database/sql"
	"fmt"
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
	}
	return s
}

// RunRetention applies the §51 prune per the persisted policy: audit
// events and terminal jobs past their windows (0 disables either).
// Non-terminal jobs and jobs holding crash-recovery records survive
// regardless.
func (s *SettingsService) RunRetention(ctx context.Context, jobStore *jobs.Store, auditStore *audit.Store) {
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
	// MaxActiveJobs is the §38 whole-application ceiling (1..16).
	MaxActiveJobs int `json:"maxActiveJobs"`
	// DefaultProfile seeds transfer dialogs (§40).
	DefaultProfile string `json:"defaultProfile"`
	// AuditRetentionDays / JobRetentionDays drive the §51 startup prune
	// (audit events; terminal jobs). 0 disables that prune.
	AuditRetentionDays int `json:"auditRetentionDays"`
	JobRetentionDays   int `json:"jobRetentionDays"`
}

func defaults() AppSettingsDTO {
	return AppSettingsDTO{
		MaxActiveJobs: defaultMaxActiveJobs, DefaultProfile: string(ProfileAuto),
		AuditRetentionDays: 90, JobRetentionDays: 30,
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
		}
	}
	return settings, rows.Err()
}

// Set validates, persists, and applies the settings.
func (s *SettingsService) Set(ctx context.Context, settings AppSettingsDTO) (AppSettingsDTO, error) {
	if settings.MaxActiveJobs < 1 || settings.MaxActiveJobs > 16 {
		return AppSettingsDTO{}, fmt.Errorf("max active jobs %d out of range 1..16", settings.MaxActiveJobs)
	}
	if _, err := profileBase(Profile(settings.DefaultProfile)); err != nil {
		return AppSettingsDTO{}, err
	}
	if settings.AuditRetentionDays < 0 || settings.AuditRetentionDays > 3650 ||
		settings.JobRetentionDays < 0 || settings.JobRetentionDays > 3650 {
		return AppSettingsDTO{}, fmt.Errorf("retention days out of range 0..3650")
	}
	err := s.db.WriteTx(ctx, func(tx *sql.Tx) error {
		for key, value := range map[string]string{
			"max_active_jobs":      strconv.Itoa(settings.MaxActiveJobs),
			"default_profile":      settings.DefaultProfile,
			"audit_retention_days": strconv.Itoa(settings.AuditRetentionDays),
			"job_retention_days":   strconv.Itoa(settings.JobRetentionDays),
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
	return settings, nil
}
