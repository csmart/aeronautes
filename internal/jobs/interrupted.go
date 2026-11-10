package jobs

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
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
				// A paused job is a running job parked between two units (ADR
				// 0104). What this column answers is whether any request was
				// made (ADR 0088), and for that it was running; the schema's
				// set of interrupted states is left as it is.
				overwritten := job.state
				if overwritten == string(StatePaused) {
					overwritten = string(StateRunning)
				}
				result, err := tx.ExecContext(ctx, `UPDATE jobs
					SET state=?, finished_at=?, counters_json=?, error=?, interrupted_at=?, interrupted_state=?
					WHERE id=?`,
					string(StateFailed), now, job.counters, reason, now, overwritten, job.id)
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

// InterruptedDisclosure is one undisclosed interrupted job and the facts the
// retained records prove about it — never a verdict the records cannot give
// (ADR 0088 decision 5). Item facts the ledger acknowledged stand on their own
// (ADR 0031 line 94); an inconclusive commit ledger is reported as inconclusive
// and not as a failure (ADR 0031 line 75); and an absent recovery row is not
// read as "nothing happened", because the download route writes none.
type InterruptedDisclosure struct {
	JobID       string
	Type        Type
	Source      string
	Destination string
	CreatedAt   time.Time
	// InterruptedAt is when the startup sweep recorded the interruption.
	InterruptedAt time.Time
	// InterruptedState is the state the sweep overwrote. Queued is the design's
	// signal that a job was never admitted, but it does not on its own prove
	// nothing was attempted: a deletion records its members' outcomes before it
	// leaves the queue. Admission is therefore read from the ledger (Attempted),
	// not inferred from the state alone.
	InterruptedState State
	// ItemsCaptured is false when the job never captured an item ledger
	// (item_records_version 0). Items is then empty because there is nothing to
	// read, which is not the same as nothing having happened.
	ItemsCaptured bool
	Items         ItemTotals
	// RecoveryRecords counts the job's retained job_recovery rows. They say
	// residue may exist remotely where the route writes them; their absence is
	// not evidence, since the download route writes none.
	RecoveryRecords int64
	// InconclusiveCommits counts committing rows with no committed row: for that
	// many names the recovery ledger is inconclusive (ADR 0031 line 75). It is
	// not a count of failures.
	InconclusiveCommits int64
	// Attempted is true when the ledger records a real attempt: an execution
	// outcome other than the synthetic not_attempted, or an issued deletion
	// member with no recorded outcome (its DELETE may have been sent, ADR 0093).
	// A false value shows that nothing is recorded as attempted; it does not by
	// itself prove no request was made, because a bulk sweep issues listing and
	// container requests that are not member steps, so the notice reports what
	// the ledger shows rather than claiming no request.
	Attempted bool
	// Copied and Verified are the acknowledged positive item facts that stand on
	// their own (ADR 0031 line 94); CopiedBytes is the witnessed byte
	// contribution. They are reported beside the problem and inconclusive facts,
	// never erased by them.
	Copied      int64
	Verified    int64
	CopiedBytes int64
}

// InterruptedDisclosurePage is a bounded, newest-first page of undisclosed
// interrupted jobs with the total count of them — the shape ADR 0020's
// retained-record summary already uses (ADR 0088 decision 4). An unavailable
// read is an error here, so the caller reports it as unknown and never as a
// zero count.
type InterruptedDisclosurePage struct {
	Entries []InterruptedDisclosure
	// Total is every undisclosed interrupted job, which may exceed len(Entries).
	Total int64
}

const interruptedDisclosurePageMax = 50

// InterruptedDisclosures reads a bounded page of interrupted jobs the operator
// has not yet been told about, newest first, with the total count (ADR 0088
// decision 4). The set is derived on read — interrupted_at IS NOT NULL AND
// interrupt_disclosed_at IS NULL — so a crash during disclosure loses nothing,
// and the read contacts no cluster: it performs no listing, HEAD, DELETE or
// authentication.
func (s *Store) InterruptedDisclosures(ctx context.Context, limit int) (InterruptedDisclosurePage, error) {
	if err := s.Available(); err != nil {
		return InterruptedDisclosurePage{}, err
	}
	if limit <= 0 || limit > interruptedDisclosurePageMax {
		limit = interruptedDisclosurePageMax
	}
	page := InterruptedDisclosurePage{Entries: []InterruptedDisclosure{}}
	tx, err := s.db.Reader().BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return InterruptedDisclosurePage{}, err
	}
	defer tx.Rollback()

	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM jobs
		WHERE interrupted_at IS NOT NULL AND interrupt_disclosed_at IS NULL`).Scan(&page.Total); err != nil {
		return InterruptedDisclosurePage{}, err
	}

	rows, err := tx.QueryContext(ctx, `SELECT j.id, j.type, COALESCE(j.source,''), COALESCE(j.destination,''),
			j.created_at, j.interrupted_at, j.interrupted_state, j.item_records_version,
			(SELECT COUNT(*) FROM job_recovery r WHERE r.job_id=j.id),
			(SELECT COUNT(*) FROM job_recovery c WHERE c.job_id=j.id AND c.kind='committing'
				AND NOT EXISTS (SELECT 1 FROM job_recovery d
					WHERE d.job_id=j.id AND d.kind='committed' AND d.prefix=c.prefix))
		FROM jobs j
		WHERE j.interrupted_at IS NOT NULL AND j.interrupt_disclosed_at IS NULL
		ORDER BY j.interrupted_at DESC, j.created_at DESC, j.id DESC
		LIMIT ?`, limit)
	if err != nil {
		return InterruptedDisclosurePage{}, err
	}
	defer rows.Close()

	for rows.Next() {
		var d InterruptedDisclosure
		var typ, state, created, interruptedAt string
		var version int
		if err := rows.Scan(&d.JobID, &typ, &d.Source, &d.Destination, &created,
			&interruptedAt, &state, &version, &d.RecoveryRecords, &d.InconclusiveCommits); err != nil {
			return InterruptedDisclosurePage{}, err
		}
		d.Type = Type(typ)
		d.InterruptedState = State(state)
		d.ItemsCaptured = version >= 1
		if d.CreatedAt, err = time.Parse(time.RFC3339, created); err != nil {
			return InterruptedDisclosurePage{}, fmt.Errorf("job %s: bad created_at: %w", d.JobID, err)
		}
		if d.InterruptedAt, err = time.Parse(time.RFC3339, interruptedAt); err != nil {
			return InterruptedDisclosurePage{}, fmt.Errorf("job %s: bad interrupted_at: %w", d.JobID, err)
		}
		page.Entries = append(page.Entries, d)
	}
	if err := rows.Err(); err != nil {
		return InterruptedDisclosurePage{}, err
	}
	// Finish the page read before the per-entry item queries on the same tx.
	if err := rows.Close(); err != nil {
		return InterruptedDisclosurePage{}, err
	}

	// Item facts come from the acknowledged ledger (ADR 0031 line 94), read only
	// for jobs that captured one; a version-0 job has none to read.
	for i := range page.Entries {
		if !page.Entries[i].ItemsCaptured {
			continue
		}
		totals, err := itemTotals(ctx, tx, page.Entries[i].JobID)
		if err != nil {
			return InterruptedDisclosurePage{}, err
		}
		page.Entries[i].Items = totals
		copied, verified, copiedBytes, attempted, err := disclosureItemFacts(ctx, tx, page.Entries[i].JobID)
		if err != nil {
			return InterruptedDisclosurePage{}, err
		}
		page.Entries[i].Copied = copied
		page.Entries[i].Verified = verified
		page.Entries[i].CopiedBytes = copiedBytes
		page.Entries[i].Attempted = attempted > 0
	}

	if err := tx.Commit(); err != nil {
		return InterruptedDisclosurePage{}, err
	}
	return page, nil
}

// disclosureItemFacts reads the acknowledged positive item facts for the ADR
// 0088 notice: how many members were recorded copied; how many verified — a
// publication-required member only once its final publication is verified too
// (ADR 0061/0070), so a passed staged read is not mistaken for a finished one;
// their witnessed copied bytes; and how many were actually attempted. An
// attempt is an execution outcome other than the synthetic not_attempted the
// sweep writes for never-issued members, or an issued deletion member with no
// recorded outcome, whose DELETE may have been sent (ADR 0093).
func disclosureItemFacts(ctx context.Context, tx *sql.Tx, jobID string) (copied, verified, copiedBytes, attempted int64, err error) {
	err = tx.QueryRowContext(ctx, `SELECT
		COALESCE(SUM(CASE WHEN e.result='copied' THEN 1 ELSE 0 END),0),
		COALESCE(SUM(CASE WHEN v.result='verified'
			AND (i.publication_required=0 OR (p.item_id IS NOT NULL AND p.result='verified'))
			THEN 1 ELSE 0 END),0),
		COALESCE(SUM(COALESCE(e.copied_bytes,0)),0),
		COALESCE(SUM(CASE WHEN (e.item_id IS NOT NULL AND e.result!='not_attempted')
			OR i.issued_at IS NOT NULL THEN 1 ELSE 0 END),0)
		FROM job_items i
		LEFT JOIN job_item_observations e ON e.item_id=i.id AND e.phase='execution'
		LEFT JOIN job_item_observations v ON v.item_id=i.id AND v.phase='verification'
		LEFT JOIN job_item_publications p ON p.item_id=i.id
		WHERE i.job_id=?`, jobID).Scan(&copied, &verified, &copiedBytes, &attempted)
	return copied, verified, copiedBytes, attempted, err
}
