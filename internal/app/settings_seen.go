package app

import (
	"context"
	"database/sql"
	"encoding/json"

	"github.com/csmart/aeronautes/internal/applog"
	"github.com/csmart/aeronautes/internal/db"
	"github.com/csmart/aeronautes/internal/swiftcontrol"
)

// rememberedSettings keeps the last settings seen for each container, per
// bookmark and storage URL, so a later connection can show them, muted and
// dated, before they are read again (ADR 0110 decision 3). It is a
// convenience: nothing with authority reads it, and a failed write is
// logged rather than failing the read that produced the fact.
type rememberedSettings struct{ db *db.DB }

// WireRememberedSettings gives the connections the database the last-seen
// settings are kept in; without it nothing is remembered.
func WireRememberedSettings(c *Connections, database *db.DB) {
	c.seen = &rememberedSettings{db: database}
}

func (r *rememberedSettings) record(ctx context.Context, bookmarkID, storageURL, container string, s ContainerSettingsDTO) error {
	data, err := json.Marshal(s)
	if err != nil {
		return err
	}
	return r.db.WriteTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `
			INSERT INTO container_settings_seen (bookmark_id, storage_url, container, observed_at, settings_json)
			VALUES (?, ?, ?, ?, ?)
			ON CONFLICT(bookmark_id, storage_url, container)
			DO UPDATE SET observed_at = excluded.observed_at, settings_json = excluded.settings_json`,
			bookmarkID, storageURL, container, s.ObservedAt, string(data))
		return err
	})
}

func (r *rememberedSettings) forget(ctx context.Context, bookmarkID, storageURL, container string) error {
	return r.db.WriteTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx,
			`DELETE FROM container_settings_seen WHERE bookmark_id = ? AND storage_url = ? AND container = ?`,
			bookmarkID, storageURL, container)
		return err
	})
}

// lastSeen returns the remembered settings for those of names recorded
// under exactly this bookmark and storage URL. The bookmark's rows are
// filtered here rather than with an IN list, which a page of 5,000 names
// would push past SQLite's parameter limit; there are only as many rows as
// containers ever read.
func (r *rememberedSettings) lastSeen(ctx context.Context, bookmarkID, storageURL string, names []string) (map[string]ContainerSettingsDTO, error) {
	wanted := make(map[string]bool, len(names))
	for _, name := range names {
		wanted[name] = true
	}
	rows, err := r.db.Reader().QueryContext(ctx,
		`SELECT container, settings_json FROM container_settings_seen WHERE bookmark_id = ? AND storage_url = ?`,
		bookmarkID, storageURL)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]ContainerSettingsDTO{}
	for rows.Next() {
		var name, data string
		if err := rows.Scan(&name, &data); err != nil {
			return nil, err
		}
		if !wanted[name] {
			continue
		}
		var s ContainerSettingsDTO
		if err := json.Unmarshal([]byte(data), &s); err != nil {
			return nil, err
		}
		out[name] = s
	}
	return out, rows.Err()
}

// recordSettings keeps a settings fact for the connection it was read under
// (ADR 0071) and remembers it as the last seen (ADR 0110 decision 3). The
// write outlives the caller's context, because the fact was already read.
func (c *Connections) recordSettings(ctx context.Context, bookmarkID string, generation int64, session *swiftcontrol.Session, name string, s ContainerSettingsDTO) {
	c.settings.remember(bookmarkID, generation, name, s)
	if c.seen == nil {
		return
	}
	if err := c.seen.record(context.WithoutCancel(ctx), bookmarkID, session.StorageURL(), name, s); err != nil {
		applog.Warnf("the settings of container %q were not remembered: %v", name, err)
	}
}

// forgetSettings drops the remembered settings of a container a HEAD found
// gone, so its last-seen badges do not outlive it.
func (c *Connections) forgetSettings(ctx context.Context, bookmarkID string, session *swiftcontrol.Session, name string) {
	if c.seen == nil {
		return
	}
	if err := c.seen.forget(context.WithoutCancel(ctx), bookmarkID, session.StorageURL(), name); err != nil {
		applog.Warnf("the remembered settings of container %q were not removed: %v", name, err)
	}
}

// lastSeenSettings is the remembered settings for a listing's names under
// its session's storage URL; an unreadable table shows none.
func (c *Connections) lastSeenSettings(ctx context.Context, bookmarkID string, session *swiftcontrol.Session, names []string) map[string]ContainerSettingsDTO {
	if c.seen == nil {
		return nil
	}
	seen, err := c.seen.lastSeen(ctx, bookmarkID, session.StorageURL(), names)
	if err != nil {
		applog.Warnf("remembered container settings could not be read: %v", err)
		return nil
	}
	return seen
}
