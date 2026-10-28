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
					if Type(job.kind).deletion() {
						// A deletion member with no issued mark was never requested,
						// and says so; one issued with no outcome keeps its silence,
						// which the totals count as unresolved because its DELETE
						// may have been sent (ADR 0093 decision 4).
						if _, err := tx.ExecContext(ctx, `INSERT INTO job_item_observations
							(item_id, phase, result, effect, diagnostic_stage, diagnostic_code, http_status, copied_bytes, created_at)
							SELECT i.id, 'execution', ?, ?, '', '', 0, 0, ? FROM job_items i
							WHERE i.job_id=? AND i.issued_at IS NULL
							AND NOT EXISTS (SELECT 1 FROM job_item_observations o WHERE o.item_id=i.id AND o.phase='execution')`,
							string(ItemNotAttempted), string(EffectNotAttempted), time.Now().UTC().Format(time.RFC3339Nano), job.id); err != nil {
							return err
						}
					}
					totals, err := itemTotals(ctx, tx, job.id)
					if err != nil {
						return err
					}
					counters.ObjectsFailed = totals.Failed
					if Type(job.kind).deletion() {
						counters.ObjectsDeleted = totals.Deleted
					}
					encoded, err := json.Marshal(counters)
					if err != nil {
						return err
					}
					job.counters = string(encoded)
					reason = interruptedRetention + "; other counters show the last saved progress"
				default:
					return ErrInvalidItemRecord
				}
				// ADR 0088: the two interruption facts are written in the
				// transaction that already classifies the job, so the count
				// this returns and the set the notice derives cannot disagree.
				result, err := tx.ExecContext(ctx, `UPDATE jobs
					SET state=?, finished_at=?, counters_json=?, error=?, interrupted_at=?, interrupted_state=?
					WHERE id=?`,
					string(StateFailed), now, job.counters, reason, now, job.state, job.id)
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
	kind     string
	counters string
	version  int
	// state is what this sweep is about to overwrite. ADR 0088 keeps it
	// because 'planning' and 'queued' are otherwise indistinguishable
	// afterwards, and the difference is whether any request was made.
	state string
}

func interruptedPage(ctx context.Context, tx *sql.Tx) ([]interruptedJob, error) {
	rows, err := tx.QueryContext(ctx, `SELECT id, type, COALESCE(counters_json,'{}'), item_records_version, state FROM jobs
		WHERE state NOT IN ('complete','partial','cancelled','failed') ORDER BY id LIMIT 100`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	page := make([]interruptedJob, 0, 100)
	for rows.Next() {
		var job interruptedJob
		if err := rows.Scan(&job.id, &job.kind, &job.counters, &job.version, &job.state); err != nil {
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

// DiscloseInterrupted records that the operator was TOLD about these
// interruptions, and nothing else (ADR 0088 decision 3). It does not touch
// job_recovery, does not change any job's state, counters or error, and makes
// no claim that anything was cleaned up — the retained-record summary keeps
// counting those records afterwards. Two schema triggers enforce that rather
// than trusting this caller or the next one.
//
// It belongs in this slice, beside the facts and the retention exemption,
// because the exemption is what keeps an undisclosed interruption past
// JobRetentionDays: without the write that ends it, that retention has no end.
//
// Already-disclosed and never-interrupted ids are skipped rather than refused,
// so a dismissal that races another is idempotent; the count is of the rows
// this call actually disclosed.
func (s *Store) DiscloseInterrupted(ctx context.Context, ids []string) (int64, error) {
	if err := s.Available(); err != nil {
		return 0, err
	}
	if len(ids) == 0 {
		return 0, nil
	}
	now := time.Now().UTC().Format(time.RFC3339)
	var disclosed int64
	err := s.db.WriteTx(ctx, func(tx *sql.Tx) error {
		disclosed = 0
		for _, id := range ids {
			result, err := tx.ExecContext(ctx, `UPDATE jobs SET interrupt_disclosed_at=?
				WHERE id=? AND interrupted_at IS NOT NULL AND interrupt_disclosed_at IS NULL`, now, id)
			if err != nil {
				return err
			}
			changed, err := result.RowsAffected()
			if err != nil {
				return err
			}
			disclosed += changed
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	return disclosed, nil
}
