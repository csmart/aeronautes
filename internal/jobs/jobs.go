// Package jobs is the §46 job model: every long operation is a
// first-class, persistent job with a typed state machine, counters, and
// bookmark scoping for the §47 drawer filter. The store enforces legal
// transitions — a job cannot silently skip Verifying, and terminal
// states are terminal.
package jobs

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	"github.com/csmart/aeronautes/internal/db"
)

// Type is the job kind.
type Type string

const (
	TypeCopy          Type = "copy"
	TypeMirrorPreview Type = "mirror_preview"
	TypeUpload        Type = "upload"
	TypeDownload      Type = "download"
	TypeMirror        Type = "mirror"
	TypeDeletePrefix  Type = "delete_prefix"
	TypeEmpty         Type = "empty_container"
	TypeDeleteCont    Type = "delete_container"
)

// State is the §46 lifecycle state.
type State string

const (
	StateQueued    State = "queued"
	StatePlanning  State = "planning"
	StateRunning   State = "running"
	StateVerifying State = "verifying"
	StateComplete  State = "complete"
	StatePartial   State = "partial"
	StateCancelled State = "cancelled"
	StateFailed    State = "failed"
)

// legalNext defines the §46 machine. Terminal states have no exits.
var legalNext = map[State][]State{
	StateQueued:    {StatePlanning, StateCancelled, StateFailed},
	StatePlanning:  {StateRunning, StateComplete, StateCancelled, StateFailed},
	StateRunning:   {StateVerifying, StateCancelled, StateFailed},
	StateVerifying: {StateComplete, StatePartial, StateCancelled, StateFailed},
}

// Terminal reports whether a state has no exits.
func Terminal(s State) bool {
	return len(legalNext[s]) == 0
}

// Counters is the §46/§47 progress surface, serialized as JSON.
type Counters struct {
	ObjectsPlanned  int64 `json:"objectsPlanned"`
	ObjectsCopied   int64 `json:"objectsCopied"`
	ObjectsVerified int64 `json:"objectsVerified"`
	ObjectsFailed   int64 `json:"objectsFailed"`
	BytesPlanned    int64 `json:"bytesPlanned"`
	BytesCopied     int64 `json:"bytesCopied"`
	// Overwrites counts stage-and-commit objects; NativeRouted counts
	// names the §14.3 classification sent down the native path.
	Overwrites   int64 `json:"overwrites"`
	NativeRouted int64 `json:"nativeRouted"`
	// ObjectsDeleted counts a Mirror's destination-only deletions.
	ObjectsDeleted int64 `json:"objectsDeleted"`
}

// Job is one persistent job row.
type Job struct {
	ID    string
	Type  Type
	State State
	// Source/Destination are display locators ("bookmark:container/prefix").
	Source           string
	Destination      string
	SourceBookmarkID string
	DestBookmarkID   string
	CreatedAt        time.Time
	StartedAt        time.Time
	FinishedAt       time.Time
	Engine           string
	Counters         Counters
	Error            string
	// Settings is the §45 effective-configuration JSON recorded at
	// creation ("" when the job carries none).
	Settings string
}

// Store persists jobs.
type Store struct {
	db *db.DB
}

// NewStore builds a Store over the state database.
func NewStore(d *db.DB) *Store {
	return &Store{db: d}
}

// Create inserts a new job in StateQueued and returns it with its id.
func (s *Store) Create(ctx context.Context, j Job) (Job, error) {
	if j.Type == "" {
		return Job{}, fmt.Errorf("job needs a type")
	}
	var raw [8]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return Job{}, err
	}
	j.ID = hex.EncodeToString(raw[:])
	j.State = StateQueued
	j.CreatedAt = time.Now().UTC().Truncate(time.Second)

	counters, err := json.Marshal(j.Counters)
	if err != nil {
		return Job{}, err
	}
	err = s.db.WriteTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `
			INSERT INTO jobs (id, type, state, source, destination, created_at,
				execution_engine, counters_json, source_bookmark_id, dest_bookmark_id, settings_json)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			j.ID, string(j.Type), string(j.State), nullable(j.Source), nullable(j.Destination),
			j.CreatedAt.Format(time.RFC3339), nullable(j.Engine), string(counters),
			nullable(j.SourceBookmarkID), nullable(j.DestBookmarkID), nullable(j.Settings))
		return err
	})
	if err != nil {
		return Job{}, err
	}
	return j, nil
}

// Transition moves a job to a new state, enforcing §46 legality, and
// stamps started_at on entering Running and finished_at on any terminal
// state.
func (s *Store) Transition(ctx context.Context, id string, to State) error {
	now := time.Now().UTC().Format(time.RFC3339)
	return s.db.WriteTx(ctx, func(tx *sql.Tx) error {
		var current string
		err := tx.QueryRowContext(ctx, `SELECT state FROM jobs WHERE id=?`, id).Scan(&current)
		if err == sql.ErrNoRows {
			return fmt.Errorf("job %s not found", id)
		}
		if err != nil {
			return err
		}
		if !transitionLegal(State(current), to) {
			return fmt.Errorf("job %s: illegal transition %s → %s", id, current, to)
		}
		if to == StateRunning {
			_, err = tx.ExecContext(ctx, `UPDATE jobs SET state=?, started_at=? WHERE id=?`, string(to), now, id)
		} else if Terminal(to) {
			_, err = tx.ExecContext(ctx, `UPDATE jobs SET state=?, finished_at=? WHERE id=?`, string(to), now, id)
		} else {
			_, err = tx.ExecContext(ctx, `UPDATE jobs SET state=? WHERE id=?`, string(to), id)
		}
		return err
	})
}

func transitionLegal(from, to State) bool {
	for _, next := range legalNext[from] {
		if next == to {
			return true
		}
	}
	return false
}

// UpdateCounters replaces a job's counters.
func (s *Store) UpdateCounters(ctx context.Context, id string, c Counters) error {
	encoded, err := json.Marshal(c)
	if err != nil {
		return err
	}
	return s.db.WriteTx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `UPDATE jobs SET counters_json=? WHERE id=?`, string(encoded), id)
		if err != nil {
			return err
		}
		n, _ := res.RowsAffected()
		if n == 0 {
			return fmt.Errorf("job %s not found", id)
		}
		return nil
	})
}

// SetSettings records the §45 effective-configuration JSON (used when
// the effective strategy is only known once execution selects it).
func (s *Store) SetSettings(ctx context.Context, id, settingsJSON string) error {
	return s.db.WriteTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE jobs SET settings_json=? WHERE id=?`, nullable(settingsJSON), id)
		return err
	})
}

// SetError records a job's error text (kept with whatever state the job
// transitions to; setting it does not change state).
func (s *Store) SetError(ctx context.Context, id, message string) error {
	return s.db.WriteTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE jobs SET error=? WHERE id=?`, nullable(message), id)
		return err
	})
}

const selectJob = `
	SELECT id, type, state, COALESCE(source,''), COALESCE(destination,''),
		created_at, COALESCE(started_at,''), COALESCE(finished_at,''),
		COALESCE(execution_engine,''), COALESCE(counters_json,'{}'),
		COALESCE(error,''), COALESCE(source_bookmark_id,''), COALESCE(dest_bookmark_id,''),
		COALESCE(settings_json,'')
	FROM jobs`

// Get returns one job.
func (s *Store) Get(ctx context.Context, id string) (Job, error) {
	row := s.db.Reader().QueryRowContext(ctx, selectJob+` WHERE id=?`, id)
	j, err := scanJob(row.Scan)
	if err == sql.ErrNoRows {
		return Job{}, fmt.Errorf("job %s not found", id)
	}
	return j, err
}

// Filter narrows List (§47: the drawer's bookmark filter).
type Filter struct {
	// BookmarkID matches jobs whose source OR destination is the bookmark.
	BookmarkID string
	// ActiveOnly keeps non-terminal jobs.
	ActiveOnly bool
	Limit      int
}

// List returns jobs newest first.
func (s *Store) List(ctx context.Context, f Filter) ([]Job, error) {
	query := selectJob + ` WHERE 1=1`
	var args []any
	if f.BookmarkID != "" {
		query += ` AND (source_bookmark_id=? OR dest_bookmark_id=?)`
		args = append(args, f.BookmarkID, f.BookmarkID)
	}
	if f.ActiveOnly {
		query += ` AND state NOT IN ('complete','partial','cancelled','failed')`
	}
	limit := f.Limit
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	query += ` ORDER BY created_at DESC, id DESC LIMIT ?`
	args = append(args, limit)

	rows, err := s.db.Reader().QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Job
	for rows.Next() {
		j, err := scanJob(rows.Scan)
		if err != nil {
			return nil, err
		}
		out = append(out, j)
	}
	return out, rows.Err()
}

func scanJob(scan func(...any) error) (Job, error) {
	var j Job
	var typ, state, created, started, finished, counters string
	err := scan(&j.ID, &typ, &state, &j.Source, &j.Destination,
		&created, &started, &finished, &j.Engine, &counters,
		&j.Error, &j.SourceBookmarkID, &j.DestBookmarkID, &j.Settings)
	if err != nil {
		return Job{}, err
	}
	j.Type = Type(typ)
	j.State = State(state)
	if j.CreatedAt, err = time.Parse(time.RFC3339, created); err != nil {
		return Job{}, fmt.Errorf("job %s: bad created_at: %w", j.ID, err)
	}
	if started != "" {
		if j.StartedAt, err = time.Parse(time.RFC3339, started); err != nil {
			return Job{}, fmt.Errorf("job %s: bad started_at: %w", j.ID, err)
		}
	}
	if finished != "" {
		if j.FinishedAt, err = time.Parse(time.RFC3339, finished); err != nil {
			return Job{}, fmt.Errorf("job %s: bad finished_at: %w", j.ID, err)
		}
	}
	if err := json.Unmarshal([]byte(counters), &j.Counters); err != nil {
		return Job{}, fmt.Errorf("job %s: bad counters: %w", j.ID, err)
	}
	return j, nil
}

func nullable(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// FailInterrupted marks every non-terminal job as failed — the startup
// crash-recovery sweep (§65.6): a process that died mid-job must never
// leave the drawer showing phantom running work. This is a deliberate
// bulk bypass of the per-job transition rules: the legal paths assume a
// live executor, and after a crash there is none. Staged litter is
// swept by the next job's reconciliation; the message says so.
func (s *Store) FailInterrupted(ctx context.Context) (int64, error) {
	now := time.Now().UTC().Format(time.RFC3339)
	var count int64
	err := s.db.WriteTx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `
			UPDATE jobs SET state=?, finished_at=?,
				error='interrupted by shutdown; any staged transfer litter is swept by reconciliation on the next run'
			WHERE state NOT IN ('complete','partial','cancelled','failed')`,
			string(StateFailed), now)
		if err != nil {
			return err
		}
		count, err = res.RowsAffected()
		return err
	})
	return count, err
}
