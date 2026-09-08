package jobs

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"
)

var ErrInterruptedJobsUnavailable = errors.New("interrupted job results could not be saved; retained outcomes cannot be confirmed. Job status is unavailable and new jobs are blocked for this session. Inspect retained data before retrying")

const interruptedRetention = "interrupted by shutdown; data and recovery records retained, remote residue unverified"

// FailInterrupted bypasses live-executor transitions on startup. Interruption
// establishes no authority to reclaim data or replace acknowledged item facts.
func (s *Store) FailInterrupted(ctx context.Context) (int64, error) {
	if err := s.Available(); err != nil {
		return 0, err
	}
	now := time.Now().UTC().Format(time.RFC3339)
	var count int64
	err := s.db.WriteTx(ctx, func(tx *sql.Tx) error {
		for {
			page, err := interruptedPage(ctx, tx)
			if err != nil {
				return err
			}
			if len(page) == 0 {
				return nil
			}
			for _, job := range page {
				var counters Counters
				if err := json.Unmarshal([]byte(job.counters), &counters); err != nil {
					return err
				}
				reason := interruptedRetention + "; counters show the last saved progress"
				switch job.version {
				case 0:
				case 1:
					totals, err := itemTotals(ctx, tx, job.id)
					if err != nil {
						return err
					}
					counters.ObjectsFailed = totals.Failed
					encoded, err := json.Marshal(counters)
					if err != nil {
						return err
					}
					job.counters = string(encoded)
					reason = interruptedRetention + "; other counters show the last saved progress"
				default:
					return ErrInvalidItemRecord
				}
				result, err := tx.ExecContext(ctx, `UPDATE jobs SET state=?, finished_at=?, counters_json=?, error=? WHERE id=?`,
					string(StateFailed), now, job.counters, reason, job.id)
				if err != nil {
					return err
				}
				changed, err := result.RowsAffected()
				if err != nil {
					return err
				}
				if changed != 1 {
					return ErrInterruptedJobsUnavailable
				}
				count++
			}
		}
	})
	if err != nil {
		s.healthMu.Lock()
		if s.unavailable == nil {
			s.unavailable = ErrInterruptedJobsUnavailable
		}
		visible := s.unavailable
		s.healthMu.Unlock()
		return 0, visible
	}
	return count, nil
}

type interruptedJob struct {
	id       string
	counters string
	version  int
}

func interruptedPage(ctx context.Context, tx *sql.Tx) ([]interruptedJob, error) {
	rows, err := tx.QueryContext(ctx, `SELECT id, COALESCE(counters_json,'{}'), item_records_version FROM jobs
		WHERE state NOT IN ('complete','partial','cancelled','failed') ORDER BY id LIMIT 100`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	page := make([]interruptedJob, 0, 100)
	for rows.Next() {
		var job interruptedJob
		if err := rows.Scan(&job.id, &job.counters, &job.version); err != nil {
			return nil, err
		}
		page = append(page, job)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	// Finish this bounded read before updating rows that determine the next page.
	return page, rows.Close()
}
