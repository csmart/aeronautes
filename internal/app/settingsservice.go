package app

import (
	"context"
	"database/sql"
	"fmt"
	"strconv"

	"github.com/csmart/aeronautes/internal/db"
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

// AppSettingsDTO is the whole application-settings document.
type AppSettingsDTO struct {
	// MaxActiveJobs is the §38 whole-application ceiling (1..16).
	MaxActiveJobs int `json:"maxActiveJobs"`
	// DefaultProfile seeds transfer dialogs (§40).
	DefaultProfile string `json:"defaultProfile"`
}

func defaults() AppSettingsDTO {
	return AppSettingsDTO{MaxActiveJobs: defaultMaxActiveJobs, DefaultProfile: string(ProfileAuto)}
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
	err := s.db.WriteTx(ctx, func(tx *sql.Tx) error {
		for key, value := range map[string]string{
			"max_active_jobs": strconv.Itoa(settings.MaxActiveJobs),
			"default_profile": settings.DefaultProfile,
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
