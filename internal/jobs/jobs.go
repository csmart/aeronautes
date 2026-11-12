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
	"sync"
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
	// TypeDeleteSegments deletes a manifest with its segments as one job
	// whose members are the reviewed manifest and segments (ADR 0095
	// decision 8).
	TypeDeleteSegments Type = "delete_segments"
	// TypeDeleteSelected deletes the selected objects one by one with the
	// verified per-name protocol and keeps its members as items (ADR 0093).
	TypeDeleteSelected Type = "delete_selected"
)

// State is the §46 lifecycle state.
type State string

const (
	StateQueued    State = "queued"
	StatePlanning  State = "planning"
	StateRunning   State = "running"
	StatePaused    State = "paused"
	StateVerifying State = "verifying"
	StateComplete  State = "complete"
	StatePartial   State = "partial"
	StateCancelled State = "cancelled"
	StateFailed    State = "failed"
)

// legalNext defines the §46 machine. Terminal states have no exits.
var legalNext = map[State][]State{
	StateQueued:   {StatePlanning, StateCancelled, StateFailed},
	StatePlanning: {StateRunning, StateComplete, StateCancelled, StateFailed},
	// Running may end PARTIAL directly: once any irreversible subset
	// succeeded, an interruption must not be recorded as a plain
	// cancel/fail (review F-10).
	StateRunning: {StateVerifying, StatePaused, StatePartial, StateCancelled, StateFailed},
	// A running job may be parked at a unit boundary and carry on later (ADR
	// 0104). A paused job ends the ways a running one can be stopped, and
	// neither verifies nor completes without running again.
	StatePaused:    {StateRunning, StatePartial, StateCancelled, StateFailed},
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
	// Overwrites remains for historical jobs; current Copy refuses them.
	// An acknowledged upload sets it, where it counts verified publications
	// to names its preflight found live rather than proven displaced
	// objects (ADR 0081 decision 7).
	// Refusals are separate from failed or verified copy attempts.
	Overwrites        int64 `json:"overwrites"`
	OverwritesRefused int64 `json:"overwritesRefused"`
	NativeRouted      int64 `json:"nativeRouted"`
	// ObjectsDeleted counts a Mirror's destination-only deletions.
	ObjectsDeleted int64 `json:"objectsDeleted"`
	// A deletion accounting failure leaves only last-confirmed progress;
	// the stored reason explains unavailable final totals and evidence.
	DeletionAccountingIncomplete bool `json:"deletionAccountingIncomplete,omitempty"`
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
	// Counts evidence rows, not remote objects or confirmed residue.
	RecoveryRecords int64
	// Version zero means item facts were never captured, not an empty ledger.
	ItemRecordsVersion int
	// SelectedMembers counts the objects a copy or download of selected
	// objects named; zero means the job acted on its whole scope (ADR 0108).
	SelectedMembers int
	// SelectedNames are those objects' names, written with the job so Re-run
	// plans the same members; only Create reads it.
	SelectedNames []string
}

// Store persists jobs.
type Store struct {
	db          *db.DB
	healthMu    sync.RWMutex
	unavailable error
	// onStop is told, once, that the store has stopped recording (OnStop).
	onStop func(jobID string)
	// running holds the cancel of each job whose body is running
	// (CancelOnStop), so that a stop reaches them before the write that
	// caused it returns.
	running map[string]context.CancelFunc
}

// CancelOnStop registers the cancel of a job whose body is running, until
// ForgetOnStop. When the store stops it cancels each of them, except the job
// whose record failed, before the failing write returns. The notice OnStop
// gives the runner arrives later, on a goroutine of its own, and in between a
// sibling could begin another publication that nothing would stop (ADR 0105
// decision 9). A context's cancel takes no lock of the caller's, which is why
// the store may call it where it cannot call the runner.
func (s *Store) CancelOnStop(jobID string, cancel context.CancelFunc) {
	s.healthMu.Lock()
	if s.running == nil {
		s.running = map[string]context.CancelFunc{}
	}
	s.running[jobID] = cancel
	s.healthMu.Unlock()
}

// ForgetOnStop removes a job's cancel once its body can publish nothing more.
func (s *Store) ForgetOnStop(jobID string) {
	s.healthMu.Lock()
	delete(s.running, jobID)
	s.healthMu.Unlock()
}

// OnStop registers what to tell when the store stops recording for the
// session. With several jobs running, a record one job could not write would
// otherwise leave the others publishing what they had already admitted, so
// the runner asks to be told and cancels the jobs it holds (ADR 0105 decision
// 9). The call is made once, on a goroutine of its own, because the store
// stops in the middle of a write whose caller may hold locks of its own; the
// running jobs have been cancelled by then (CancelOnStop), and the notice is
// what reaches a job that is starting, queued or parked. It
// names the job whose record could not be written, or "" when the stop was no
// one job's: that job is told by the error its own write returns, and ends on
// that account.
func (s *Store) OnStop(notify func(jobID string)) {
	s.healthMu.Lock()
	s.onStop = notify
	s.healthMu.Unlock()
}

// latch records why the store stopped, for the job whose record it was, and
// returns the reason that stands. The first reason is kept unless the new one
// overrides it, as a lost final result overrides a lost item record.
func (s *Store) latch(jobID string, reason error, override bool) error {
	s.healthMu.Lock()
	// Which job stopped the store is decided here, with the transition,
	// and not by whichever later write reaches the notice first: a sibling
	// this stop cancels can fail its own final save in turn, and a notice
	// naming it would cancel the job that actually failed.
	first := s.unavailable == nil
	var siblings []context.CancelFunc
	if first {
		for id, cancel := range s.running {
			if id != jobID {
				siblings = append(siblings, cancel)
			}
		}
	}
	if first || override {
		s.unavailable = reason
	}
	visible, notify := s.unavailable, s.onStop
	s.healthMu.Unlock()
	for _, cancel := range siblings {
		cancel()
	}
	if first && notify != nil {
		go notify(jobID)
	}
	return visible
}

// NewStore builds a Store over the state database.
func NewStore(d *db.DB) *Store {
	return &Store{db: d}
}

// Create inserts a new job in StateQueued and returns it with its id.
func (s *Store) Create(ctx context.Context, j Job) (Job, error) {
	if err := s.Available(); err != nil {
		return Job{}, err
	}
	if j.Type == "" {
		return Job{}, fmt.Errorf("job needs a type")
	}
	if j.ItemRecordsVersion != 0 && (j.ItemRecordsVersion != 1 || !recordsItems(j.Type)) {
		return Job{}, ErrInvalidItemRecord
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
		if err := s.Available(); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `
			INSERT INTO jobs (id, type, state, source, destination, created_at,
				execution_engine, counters_json, source_bookmark_id, dest_bookmark_id, settings_json, item_records_version,
				selected_members)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			j.ID, string(j.Type), string(j.State), nullable(j.Source), nullable(j.Destination),
			j.CreatedAt.Format(time.RFC3339), nullable(j.Engine), string(counters),
			nullable(j.SourceBookmarkID), nullable(j.DestBookmarkID), nullable(j.Settings), j.ItemRecordsVersion,
			j.SelectedMembers)
		if err != nil {
			return err
		}
		// In the same transaction as the job, so a job that says it acted on
		// selected objects can never lose their names and be re-run as its
		// whole scope.
		for i, name := range j.SelectedNames {
			if _, err := tx.ExecContext(ctx,
				`INSERT INTO job_selected_names (job_id, ordinal, name) VALUES (?, ?, ?)`, j.ID, i, name); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return Job{}, err
	}
	return j, nil
}

// recordsItems says which job types keep the durable item ledger: the three
// transfers, and every deletion whose members are its items.
func recordsItems(kind Type) bool {
	return kind == TypeCopy || kind == TypeUpload || kind == TypeDownload || kind.deletion()
}

// deletion says the job's members are deletion members (ADR 0093 decision 4,
// ADR 0095 decisions 2 and 8): a Swift source alone, an issued mark before
// the final HEAD and one deletion outcome each.
func (t Type) deletion() bool {
	switch t {
	case TypeDeleteSelected, TypeDeletePrefix, TypeEmpty, TypeDeleteCont, TypeDeleteSegments:
		return true
	}
	return false
}

// Transition moves a job to a new state, enforcing §46 legality, and
// stamps started_at on first entering Running and finished_at on any terminal
// state.
func (s *Store) Transition(ctx context.Context, id string, to State) error {
	now := time.Now().UTC().Format(time.RFC3339)
	return s.db.WriteTx(ctx, func(tx *sql.Tx) error {
		var current string
		var version int
		err := tx.QueryRowContext(ctx, `SELECT state, item_records_version FROM jobs WHERE id=?`, id).Scan(&current, &version)
		if err == sql.ErrNoRows {
			return fmt.Errorf("job %s not found", id)
		}
		if err != nil {
			return err
		}
		if version == 1 && Terminal(to) {
			err := ErrItemCompletionRequired
			s.itemWriteFailed(id, &err)
			return err
		}
		if !transitionLegal(State(current), to) {
			return fmt.Errorf("job %s: illegal transition %s → %s", id, current, to)
		}
		if to == StateRunning {
			// The first start is the job's start: resuming from a pause keeps it.
			_, err = tx.ExecContext(ctx, `UPDATE jobs SET state=?, started_at=COALESCE(started_at, ?) WHERE id=?`, string(to), now, id)
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
		COALESCE(settings_json,''),
		(SELECT COUNT(*) FROM job_recovery r WHERE r.job_id=jobs.id), item_records_version, selected_members
	FROM jobs`

// Get returns one job.
func (s *Store) Get(ctx context.Context, id string) (Job, error) {
	if err := s.Available(); err != nil {
		return Job{}, err
	}
	row := s.db.Reader().QueryRowContext(ctx, selectJob+` WHERE id=?`, id)
	j, err := scanJob(row.Scan)
	if err == sql.ErrNoRows {
		return Job{}, fmt.Errorf("job %s: %w", id, ErrItemJobNotFound)
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
	if err := s.Available(); err != nil {
		return nil, err
	}
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
		&j.Error, &j.SourceBookmarkID, &j.DestBookmarkID, &j.Settings, &j.RecoveryRecords, &j.ItemRecordsVersion,
		&j.SelectedMembers)
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

// SelectedNames returns the names a copy of selected objects recorded, in
// the order they were given; empty for a job that acted on its whole scope.
func (s *Store) SelectedNames(ctx context.Context, jobID string) ([]string, error) {
	rows, err := s.db.Reader().QueryContext(ctx,
		`SELECT name FROM job_selected_names WHERE job_id=? ORDER BY ordinal`, jobID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var names []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		names = append(names, name)
	}
	return names, rows.Err()
}

func nullable(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// RecoveryKind labels one crash-recovery record (§65.8 item 2).
type RecoveryKind string

const (
	// RecoveryStaging is a job's staging namespace (container+prefix).
	RecoveryStaging RecoveryKind = "staging"
	// RecoveryCaptured is a generated segment prefix observed live.
	RecoveryCaptured RecoveryKind = "captured"
	// RecoveryCommitting: persisted BEFORE the live manifest PUT — the
	// prefix may already be referenced by the recorded live object.
	RecoveryCommitting RecoveryKind = "committing"
	// RecoveryCommitted marks a captured prefix now referenced by a
	// committed manifest — never swept.
	RecoveryCommitted RecoveryKind = "committed"
)

// RecoveryRecord preserves intent or observed provenance, not deletion authority.
type RecoveryRecord struct {
	JobID      string
	BookmarkID string
	Kind       RecoveryKind
	Container  string
	Prefix     string
	// Object is the live "container/name" a committing record targets.
	Object string
}

// RecoveryAdd avoids duplicate facts without discarding earlier evidence.
func (s *Store) RecoveryAdd(ctx context.Context, r RecoveryRecord) error {
	if r.JobID == "" || r.BookmarkID == "" || r.Container == "" || r.Prefix == "" {
		return fmt.Errorf("recovery record needs job, bookmark, container, and prefix")
	}
	return s.db.WriteTx(ctx, func(tx *sql.Tx) error {
		var exists int
		if err := tx.QueryRowContext(ctx, `
			SELECT COUNT(*) FROM job_recovery
			WHERE job_id=? AND kind=? AND container=? AND prefix=? AND object=?`,
			r.JobID, string(r.Kind), r.Container, r.Prefix, r.Object).Scan(&exists); err != nil {
			return err
		}
		if exists > 0 {
			return nil
		}
		_, err := tx.ExecContext(ctx, `
			INSERT INTO job_recovery (job_id, bookmark_id, kind, container, prefix, object)
			VALUES (?, ?, ?, ?, ?, ?)`,
			r.JobID, r.BookmarkID, string(r.Kind), r.Container, r.Prefix, r.Object)
		return err
	})
}

type RecoveryStatus struct {
	Jobs    int64
	Records int64
}

// RecoveryStatus aggregates all terminal history without materialising
// the retained rows or limiting the count to the recent-job window.
func (s *Store) RecoveryStatus(ctx context.Context) (RecoveryStatus, error) {
	var status RecoveryStatus
	err := s.db.Reader().QueryRowContext(ctx, `
		SELECT COUNT(DISTINCT r.job_id), COUNT(*)
		FROM job_recovery r JOIN jobs j ON j.id=r.job_id
		WHERE j.state IN ('complete','partial','cancelled','failed')`).Scan(&status.Jobs, &status.Records)
	if err != nil {
		return RecoveryStatus{}, err
	}
	return status, nil
}

// RecoveryPending lists records whose job is TERMINAL — a live run
// still owns its records. Even a completed job retains records because
// transfer success does not establish safe reclamation (ADR 0020).
func (s *Store) RecoveryPending(ctx context.Context) ([]RecoveryRecord, error) {
	rows, err := s.db.Reader().QueryContext(ctx, `
		SELECT r.job_id, r.bookmark_id, r.kind, r.container, r.prefix, r.object
		FROM job_recovery r JOIN jobs j ON j.id = r.job_id
		WHERE j.state IN ('complete','partial','cancelled','failed')
		ORDER BY r.job_id, r.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []RecoveryRecord
	for rows.Next() {
		var r RecoveryRecord
		var kind string
		if err := rows.Scan(&r.JobID, &r.BookmarkID, &kind, &r.Container, &r.Prefix, &r.Object); err != nil {
			return nil, err
		}
		r.Kind = RecoveryKind(kind)
		out = append(out, r)
	}
	return out, rows.Err()
}

// PruneTerminal deletes terminal jobs finished before the cutoff —
// §51's retention, never touching non-terminal rows and never a job
// that still holds recovery records (uncertainty must retain its evidence).
// Returns how many jobs were pruned.
func (s *Store) PruneTerminal(ctx context.Context, before time.Time) (int64, error) {
	cutoff := before.UTC().Format(time.RFC3339)
	var pruned int64
	err := s.db.WriteTx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `
			DELETE FROM jobs
			WHERE state IN ('complete','partial','cancelled','failed')
			  AND finished_at IS NOT NULL AND finished_at < ?
			  AND id NOT IN (SELECT DISTINCT job_id FROM job_recovery)
			  AND id NOT IN (SELECT job_id FROM job_items)
			  -- ADR 0088: an interruption the operator has not been told about
			  -- outlives retention. A job that was merely queued holds neither
			  -- recovery nor item rows, so without this the one case whose
			  -- whole message is that nothing was attempted is the one case
			  -- that silently expires. Dismissal returns it to ordinary
			  -- retention; until then the notice must be able to return.
			  AND NOT (interrupted_at IS NOT NULL AND interrupt_disclosed_at IS NULL)`, cutoff)
		if err != nil {
			return err
		}
		pruned, err = res.RowsAffected()
		return err
	})
	return pruned, err
}

// ActiveForBookmark counts non-terminal jobs referencing a bookmark on
// either end — the bookmark-deletion guard (§65.9 item 15).
func (s *Store) ActiveForBookmark(ctx context.Context, bookmarkID string) (int, error) {
	var n int
	err := s.db.Reader().QueryRowContext(ctx, `
		SELECT COUNT(*) FROM jobs
		WHERE state NOT IN ('complete','partial','cancelled','failed')
		AND (source_bookmark_id = ? OR dest_bookmark_id = ?)`,
		bookmarkID, bookmarkID).Scan(&n)
	return n, err
}
