// Package deletion is the §31.2 strategy engine: Aeronautes-native
// destruction only, planned and verified. Every executor here is
// manifest-only by construction — a native DELETE (single or bulk)
// removes exactly the named object and never follows DLO/SLO segments,
// which is what makes the §28.6 plan-to-delete race survivable: an
// object that becomes a shared-segment manifest between planning and
// deletion costs at most that manifest, never another object's
// segments. There are no rclone executors (§36).
package deletion

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/csmart/aeronautes/internal/swiftcontrol"
)

// Strategy names the §31.2 executor a run selected — recorded in the
// job's effective configuration (§45), never a silent choice.
type Strategy string

const (
	// StrategyBulk batches names into POST ?bulk-delete requests.
	StrategyBulk Strategy = "bulk-delete"
	// StrategyParallel is the bounded worker pool of single DELETEs.
	StrategyParallel Strategy = "parallel-delete"
)

// Options bounds one deletion run.
type Options struct {
	// Workers bounds the parallel executor (default 8, max 64).
	Workers int
	// BulkSize caps names per bulk request; 0 uses the capability's
	// max_deletes_per_request (bounded to 1000 per request here — one
	// request should stay small enough to fail cheaply).
	BulkSize int
	// ForceParallel skips bulk even when the capability advertises it.
	ForceParallel bool
	// Passes bounds re-list sweeps (a listing consumed while deleting
	// lags behind the deletes); default 3.
	Passes int
	// OnProgress is told cumulative counts as the run advances.
	OnProgress func(deleted, failed int64)
}

// Failure is one name that would not die.
type Failure struct {
	Name string
	Err  string
}

// Result accounts for one run.
type Result struct {
	Strategy Strategy
	Deleted  int64
	NotFound int64
	Failures []Failure
	// Remaining counts names still listed after the final pass and
	// verification retries: eventual consistency or a concurrent writer.
	// Remaining > 0 means Partial, never silent success.
	Remaining int64
	// Passes is how many list+delete sweeps ran.
	Passes int
	// VerifyPolicy states the §32 postcondition this run checked.
	VerifyPolicy string
}

const maxFailures = 200

func (r *Result) fail(name string, err error) {
	if len(r.Failures) < maxFailures {
		r.Failures = append(r.Failures, Failure{Name: name, Err: err.Error()})
	}
}

// DeletePrefix removes every object under container/prefix (prefix ""
// = the whole container's contents): the §32 pipeline — stream listing
// pages into bounded batches, delete, re-sweep a bounded number of
// passes, then verify the operation-specific postcondition (a
// `limit=1` listing must come back empty, with bounded retries for
// listing consistency).
func DeletePrefix(ctx context.Context, session *swiftcontrol.Session, capabilities *swiftcontrol.Capabilities,
	container, prefix string, opts Options) (*Result, error) {

	workers := opts.Workers
	if workers <= 0 {
		workers = 8
	}
	if workers > 64 {
		workers = 64
	}
	passes := opts.Passes
	if passes <= 0 {
		passes = 3
	}

	result := &Result{Strategy: StrategyParallel}
	bulkSize := 0
	if !opts.ForceParallel && capabilities != nil {
		if max := capabilities.BulkDeleteMaxDeletes(); max > 0 {
			bulkSize = opts.BulkSize
			if bulkSize <= 0 || bulkSize > 1000 {
				bulkSize = 1000
			}
			if bulkSize > max {
				bulkSize = max
			}
			result.Strategy = StrategyBulk
		}
	}
	result.VerifyPolicy = fmt.Sprintf("prefix limit=1 empty after ≤%d passes", passes)

	for pass := 1; pass <= passes; pass++ {
		result.Passes = pass
		deletedThisPass, err := deletePass(ctx, session, container, prefix, bulkSize, workers, result, opts.OnProgress)
		if err != nil {
			return result, err
		}
		if deletedThisPass == 0 {
			break
		}
	}

	remaining, err := verifyPrefixEmpty(ctx, session, container, prefix)
	if err != nil {
		return result, fmt.Errorf("verification: %w", err)
	}
	result.Remaining = remaining
	return result, nil
}

// deletePass consumes one full listing sweep, deleting as it streams.
func deletePass(ctx context.Context, session *swiftcontrol.Session, container, prefix string,
	bulkSize, workers int, result *Result, onProgress func(deleted, failed int64)) (int64, error) {

	var passDeleted int64
	var mu sync.Mutex
	progress := func() {
		if onProgress != nil {
			mu.Lock()
			deleted, failed := result.Deleted, int64(len(result.Failures))
			mu.Unlock()
			onProgress(deleted, failed)
		}
	}

	if bulkSize > 0 {
		batch := make([]string, 0, bulkSize)
		flush := func() error {
			if len(batch) == 0 {
				return nil
			}
			bulk, err := session.BulkDelete(ctx, container, batch)
			batch = batch[:0]
			if err != nil {
				return err
			}
			mu.Lock()
			result.Deleted += bulk.Deleted
			result.NotFound += bulk.NotFound
			passDeleted += bulk.Deleted
			for _, item := range bulk.Errors {
				result.fail(item[0], fmt.Errorf("%s", item[1]))
			}
			mu.Unlock()
			progress()
			return nil
		}
		for entry, err := range session.Objects(ctx, container, swiftcontrol.ObjectListOptions{Deep: true, Prefix: prefix}) {
			if err != nil {
				return passDeleted, err
			}
			if object, ok := entry.(swiftcontrol.ObjectEntry); ok {
				batch = append(batch, object.Name)
				if len(batch) == bulkSize {
					if err := flush(); err != nil {
						return passDeleted, err
					}
				}
			}
		}
		return passDeleted, flush()
	}

	// Parallel single DELETEs: LIST page → bounded queue → N workers.
	names := make(chan string, workers*2)
	var wg sync.WaitGroup
	workerCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for name := range names {
				err := session.DeleteObject(workerCtx, container, name)
				mu.Lock()
				switch {
				case err == nil:
					result.Deleted++
					passDeleted++
				case swiftcontrol.IsNotFound(err):
					result.NotFound++
				default:
					result.fail(name, err)
				}
				mu.Unlock()
				progress()
			}
		}()
	}
	var listErr error
	for entry, err := range session.Objects(ctx, container, swiftcontrol.ObjectListOptions{Deep: true, Prefix: prefix}) {
		if err != nil {
			listErr = err
			break
		}
		if object, ok := entry.(swiftcontrol.ObjectEntry); ok {
			select {
			case names <- object.Name:
			case <-ctx.Done():
				listErr = ctx.Err()
			}
			if listErr != nil {
				break
			}
		}
	}
	close(names)
	wg.Wait()
	return passDeleted, listErr
}

// verifyPrefixEmpty is the §32 low-cost postcondition: one-row listing
// with bounded retries for listing consistency (§15.3). Returns how
// many rows still answer (0 or 1 — it never rescans the namespace).
func verifyPrefixEmpty(ctx context.Context, session *swiftcontrol.Session, container, prefix string) (int64, error) {
	var remaining int64
	for attempt := 0; attempt < 5; attempt++ {
		remaining = 0
		for entry, err := range session.Objects(ctx, container, swiftcontrol.ObjectListOptions{
			Deep: true, Prefix: prefix, PageSize: 1,
		}) {
			if err != nil {
				return remaining, err
			}
			if _, ok := entry.(swiftcontrol.ObjectEntry); ok {
				remaining = 1
			}
			break
		}
		if remaining == 0 {
			return 0, nil
		}
		select {
		case <-time.After(300 * time.Millisecond):
		case <-ctx.Done():
			return remaining, ctx.Err()
		}
	}
	return remaining, nil
}

// ContainerConfig is the §33 preserved surface an Empty Container run
// must not change.
type ContainerConfig struct {
	Policy     string
	Meta       map[string]string
	ReadACL    string
	WriteACL   string
	Versioning swiftcontrol.ContainerVersioning
	Quota      int64
}

// SnapshotConfig captures the §33 acceptance baseline from a container
// HEAD.
func SnapshotConfig(d *swiftcontrol.ContainerDetail) ContainerConfig {
	meta := map[string]string{}
	for k, v := range d.Meta {
		meta[k] = v
	}
	return ContainerConfig{
		Policy: d.StoragePolicy, Meta: meta,
		ReadACL: d.ReadACL, WriteACL: d.WriteACL,
		Versioning: d.Versioning, Quota: d.QuotaBytes,
	}
}

// Diff reports human-readable differences ("" when identical).
func (c ContainerConfig) Diff(after ContainerConfig) string {
	var diffs []string
	if c.Policy != after.Policy {
		diffs = append(diffs, fmt.Sprintf("policy %q → %q", c.Policy, after.Policy))
	}
	if c.ReadACL != after.ReadACL || c.WriteACL != after.WriteACL {
		diffs = append(diffs, "ACLs changed")
	}
	if c.Versioning != after.Versioning {
		diffs = append(diffs, "versioning changed")
	}
	if c.Quota != after.Quota {
		diffs = append(diffs, "quota changed")
	}
	if len(c.Meta) != len(after.Meta) {
		diffs = append(diffs, "metadata changed")
	} else {
		for k, v := range c.Meta {
			if after.Meta[k] != v {
				diffs = append(diffs, "metadata changed")
				break
			}
		}
	}
	return strings.Join(diffs, "; ")
}
