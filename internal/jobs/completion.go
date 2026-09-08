package jobs

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/csmart/aeronautes/internal/audit"
)

// Completion keeps the terminal summary inseparable from its audit.
type Completion struct {
	State    State
	Counters Counters
	Error    string
	Audit    audit.Event
}

// Available reports the process latch without exposing database details.
// Restart cannot recover missing observations; it only clears this diagnostic.
func (s *Store) Available() error {
	s.healthMu.RLock()
	defer s.healthMu.RUnlock()
	return s.unavailable
}

// Finish commits the final snapshot and audit together. On failure the old
// row may be stale, so it must not be presented as the current job outcome.
func (s *Store) Finish(ctx context.Context, id string, result Completion, trail *audit.Store) error {
	return s.finish(ctx, id, result, trail, false)
}

// FinishFromItems checks the same durable facts that supply its failed-name total.
func (s *Store) FinishFromItems(ctx context.Context, id string, result Completion, trail *audit.Store) error {
	if err := s.finish(ctx, id, result, trail, true); err != nil {
		return errors.Join(s.Available(), itemError(err))
	}
	return nil
}

func (s *Store) finish(ctx context.Context, id string, result Completion, trail *audit.Store, fromItems bool) (err error) {
	defer func() {
		if err != nil {
			s.healthMu.Lock()
			s.unavailable = errors.New("a job's final result could not be saved; changes may already have occurred. Job status is unavailable and new jobs are blocked for this session. Inspect retained data before retrying")
			s.healthMu.Unlock()
		}
	}()
	if !Terminal(result.State) || trail == nil {
		return errors.New("completion needs a terminal state and audit store")
	}
	now := time.Now().UTC().Format(time.RFC3339)
	return s.db.WriteTx(ctx, func(tx *sql.Tx) error {
		var current State
		var version int
		if err := tx.QueryRowContext(ctx, `SELECT state, item_records_version FROM jobs WHERE id=?`, id).Scan(&current, &version); err != nil {
			return fmt.Errorf("read job completion state: %w", err)
		}
		if !fromItems && version == 1 {
			return ErrItemCompletionRequired
		}
		if fromItems {
			if version != 1 {
				return ErrItemRecordsNotCaptured
			}
			totals, err := itemTotals(ctx, tx, id)
			if err != nil {
				return err
			}
			if result.State == StateComplete && !totals.permitsComplete() {
				return ErrIncompleteItemRecords
			}
			result.Counters.ObjectsFailed = totals.Failed
		}
		if !transitionLegal(current, result.State) {
			return fmt.Errorf("job %s: illegal transition %s → %s", id, current, result.State)
		}
		encoded, err := json.Marshal(result.Counters)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE jobs SET state=?, counters_json=?, error=?, finished_at=? WHERE id=?`,
			string(result.State), string(encoded), nullable(result.Error), now, id); err != nil {
			return err
		}
		return trail.AppendTx(ctx, tx, result.Audit)
	})
}
