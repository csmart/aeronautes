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
	// OnFailure receives EVERY per-name failure (uncapped) — the
	// complete-set channel behind Result.Failures' bounded sample.
	OnFailure func(name, detail string)
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
	// Failed is the EXACT failure count — Failures above is a bounded
	// sample for display; every failure also streams through
	// Options.OnFailure so callers can retain the complete set
	// (K-DEL-002).
	Failed int64
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

func (r *Result) fail(onFailure func(name, detail string), name string, err error) {
	r.Failed++
	if len(r.Failures) < maxFailures {
		r.Failures = append(r.Failures, Failure{Name: name, Err: err.Error()})
	}
	if onFailure != nil {
		onFailure(name, err.Error())
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
		deletedThisPass, err := deletePass(ctx, session, container, prefix, bulkSize, workers, result, opts.OnProgress, opts.OnFailure)
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
	bulkSize, workers int, result *Result, onProgress func(deleted, failed int64), onFailure func(name, detail string)) (int64, error) {

	var passDeleted int64
	var mu sync.Mutex
	// Progress snapshots are taken AND delivered under the mutex:
	// callers see monotonic, consistent pairs (§65.9 item 11).
	progress := func() {
		if onProgress != nil {
			onProgress(result.Deleted, result.Failed)
		}
	}

	if bulkSize > 0 {
		batch := make([]string, 0, bulkSize)
		flush := func() error {
			if len(batch) == 0 {
				return nil
			}
			bulk, err := session.BulkDelete(ctx, container, batch)
			mu.Lock()
			defer mu.Unlock()
			if bulk != nil {
				result.Deleted += bulk.Deleted
				result.NotFound += bulk.NotFound
				passDeleted += bulk.Deleted
				for _, item := range bulk.Errors {
					result.fail(onFailure, item[0], fmt.Errorf("%s", item[1]))
				}
			}
			if err != nil {
				// EVERY name the response did not account for is a
				// recorded failure — a failed request must never lose
				// its batch (K-DEL-002).
				accounted := int64(0)
				if bulk != nil {
					accounted = bulk.Deleted + bulk.NotFound + int64(len(bulk.Errors))
				}
				for i := accounted; i < int64(len(batch)); i++ {
					result.fail(onFailure, batch[i], err)
				}
				batch = batch[:0]
				progress()
				return err
			}
			batch = batch[:0]
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
					result.fail(onFailure, name, err)
				}
				progress()
				mu.Unlock()
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

// DeleteNames removes an explicit, already-decided name list (the
// Mirror delete half, §27.2): same native executors, manifest-only by
// construction, no listing pass — the caller's plan IS the scope.
// Verification is a bounded sample of HEADs expecting 404 (§32:
// operation-specific, never a namespace rescan).
func DeleteNames(ctx context.Context, session *swiftcontrol.Session, capabilities *swiftcontrol.Capabilities,
	container string, names []string, opts Options) (*Result, error) {

	workers := opts.Workers
	if workers <= 0 {
		workers = 8
	}
	if workers > 64 {
		workers = 64
	}
	result := &Result{Strategy: StrategyParallel, VerifyPolicy: "sampled HEAD=404 (≤10 names)"}
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
	var mu sync.Mutex
	progress := func() {
		if opts.OnProgress != nil {
			opts.OnProgress(result.Deleted, result.Failed)
		}
	}

	if bulkSize > 0 {
		for start := 0; start < len(names); start += bulkSize {
			if err := ctx.Err(); err != nil {
				return result, err
			}
			end := min(start+bulkSize, len(names))
			bulk, err := session.BulkDelete(ctx, container, names[start:end])
			mu.Lock()
			if bulk != nil {
				result.Deleted += bulk.Deleted
				result.NotFound += bulk.NotFound
				for _, item := range bulk.Errors {
					result.fail(opts.OnFailure, item[0], fmt.Errorf("%s", item[1]))
				}
			}
			if err != nil {
				accounted := int64(0)
				if bulk != nil {
					accounted = bulk.Deleted + bulk.NotFound + int64(len(bulk.Errors))
				}
				chunk := names[start:end]
				for i := accounted; i < int64(len(chunk)); i++ {
					result.fail(opts.OnFailure, chunk[i], err)
				}
				progress()
				mu.Unlock()
				return result, err
			}
			progress()
			mu.Unlock()
		}
	} else {
		queue := make(chan string, workers*2)
		var wg sync.WaitGroup
		for i := 0; i < workers; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for name := range queue {
					err := session.DeleteObject(ctx, container, name)
					mu.Lock()
					switch {
					case err == nil:
						result.Deleted++
					case swiftcontrol.IsNotFound(err):
						result.NotFound++
					default:
						result.fail(opts.OnFailure, name, err)
					}
					progress()
					mu.Unlock()
				}
			}()
		}
		var queueErr error
		for _, name := range names {
			select {
			case queue <- name:
			case <-ctx.Done():
				queueErr = ctx.Err()
			}
			if queueErr != nil {
				break
			}
		}
		close(queue)
		wg.Wait()
		if queueErr != nil {
			return result, queueErr
		}
	}

	// Sampled postcondition.
	for i, name := range names {
		if i >= 10 {
			break
		}
		if _, err := session.ObjectDetail(ctx, container, name); err == nil {
			result.Remaining++
		} else if !swiftcontrol.IsNotFound(err) && ctx.Err() == nil {
			return result, fmt.Errorf("verification HEAD %s: %w", name, err)
		}
	}
	return result, nil
}
