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
	// OnFailure receives EVERY per-name reported failure (uncapped) —
	// the complete-set channel behind Result.Failures' bounded sample.
	OnFailure func(name, detail string)
	// OnUnresolved receives EVERY name whose effect this run cannot
	// state (uncapped), which is a different fact from a reported
	// failure and may describe the same name. Nothing here observes the
	// store, so a name the service reported an error for is also
	// unresolved.
	OnUnresolved func(name, detail string)
}

// UntrustedAggregate is one untrusted reply's own account of itself,
// retained so the diagnostic survives the caller boundary. It is
// evidence about a reply, never a deletion or verification credit, and
// it names nobody.
type UntrustedAggregate struct {
	// Requested is how many names that request carried.
	Requested int
	// Deleted and NotFound are the counts the reply claimed.
	Deleted, NotFound int64
	// Anomalies are why the reply was not dependable.
	Anomalies []string
	// MalformedRows are rows whose shape was not the documented pair,
	// retained whole.
	MalformedRows [][]string
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
	// Failed is the EXACT count of distinct requested names the service
	// reported an error for, each counted once — Failures above is a
	// bounded sample for display; every one also streams through
	// Options.OnFailure so callers can retain the complete set
	// (K-DEL-002).
	Failed int64
	// Unresolveds samples the names whose effect this run cannot state,
	// bounded for display exactly as Failures is.
	Unresolveds []Failure
	// Unresolved is the EXACT count of distinct requested names whose
	// effect is unknown, streamed uncapped through Options.OnUnresolved.
	// It overlaps Failed rather than extending it: a reported error is
	// a report about an attempt and witnesses no effect, so a reported
	// name is also unresolved. A run holding any unresolved name has
	// not completed.
	Unresolved int64
	// UntrustedReplies counts bulk replies that failed validation and
	// whose counts were therefore credited to nothing.
	UntrustedReplies int64
	// UntrustedAggregates retains what each of those replies claimed,
	// bounded for size. These are diagnostics and are credited to
	// nothing.
	UntrustedAggregates []UntrustedAggregate
	// Remaining counts names still listed after the final pass and
	// verification retries: eventual consistency or a concurrent writer.
	// Remaining > 0 means Partial, never silent success.
	Remaining int64
	// Passes is how many list+delete sweeps ran.
	Passes int
	// VerifyPolicy states the §32 postcondition this run checked.
	VerifyPolicy string

	// seenFailed and seenUnresolved make both counts what they are
	// documented to be: distinct requested identities across the whole
	// run. A sweep re-lists what is still there, so the same name can
	// be observed on more than one pass and in more than one batch, and
	// one identity must still be one record, one stream event and one
	// count.
	seenFailed     map[string]bool
	seenUnresolved map[string]bool
}

const maxFailures = 200

// blamedNames indexes the per-name errors a validated reply reported.
func blamedNames(bulk *swiftcontrol.BulkDeleteResult) map[string]string {
	if bulk == nil {
		return nil
	}
	blamed := make(map[string]string, len(bulk.Failures))
	for _, item := range bulk.Failures {
		if _, seen := blamed[item.Name]; !seen {
			blamed[item.Name] = item.Status
		}
	}
	return blamed
}

// recordBatchOutcome writes one batch's outcome into the ledger.
//
// Two facts are kept apart. A name the reply reported an error for is a
// reported failure carrying that status. A name whose effect this run
// cannot state is unresolved. They describe the same name whenever the
// service blamed it, because nothing here observes the store: a report
// is a report about an attempt, and an identical reply accompanies an
// object that survived and one that was destroyed.
//
// A reply that failed validation says nothing dependable about which
// names it handled, so every requested name in that batch is
// unresolved and its counts are credited to nothing (K-DEL-002).
func recordBatchOutcome(result *Result, onFailure, onUnresolved func(name, detail string),
	batch []string, bulk *swiftcontrol.BulkDeleteResult, err error) {

	validated := bulk != nil && bulk.Validated
	if bulk != nil && !validated && len(result.UntrustedAggregates) < maxFailures {
		// The reply is not dependable, but what it claimed is evidence
		// about the service and must survive this boundary.
		result.UntrustedAggregates = append(result.UntrustedAggregates, UntrustedAggregate{
			Requested:     len(batch),
			Deleted:       bulk.Deleted,
			NotFound:      bulk.NotFound,
			Anomalies:     append([]string(nil), bulk.Anomalies...),
			MalformedRows: bulk.MalformedRows,
		})
	}
	blamed := blamedNames(bulk)
	reason := "the reply is not dependable, so this name's effect is unknown"
	if err != nil {
		reason = err.Error()
	}
	for _, name := range batch {
		if status, ok := blamed[name]; ok {
			result.fail(onFailure, name, fmt.Errorf("%s", status))
			result.unresolvedEffect(onUnresolved, name,
				fmt.Sprintf("the service reported %s and no observation establishes whether the object was destroyed", status))
			continue
		}
		if !validated {
			result.unresolvedEffect(onUnresolved, name, reason)
		}
	}
}

func (r *Result) fail(onFailure func(name, detail string), name string, err error) {
	if r.seenFailed == nil {
		r.seenFailed = map[string]bool{}
	}
	if r.seenFailed[name] {
		return
	}
	r.seenFailed[name] = true
	r.Failed++
	if len(r.Failures) < maxFailures {
		r.Failures = append(r.Failures, Failure{Name: name, Err: err.Error()})
	}
	if onFailure != nil {
		onFailure(name, err.Error())
	}
}

// unresolvedEffect retains one name whose effect this run cannot state.
func (r *Result) unresolvedEffect(onUnresolved func(name, detail string), name, detail string) {
	if r.seenUnresolved == nil {
		r.seenUnresolved = map[string]bool{}
	}
	if r.seenUnresolved[name] {
		return
	}
	r.seenUnresolved[name] = true
	r.Unresolved++
	if len(r.Unresolveds) < maxFailures {
		r.Unresolveds = append(r.Unresolveds, Failure{Name: name, Err: detail})
	}
	if onUnresolved != nil {
		onUnresolved(name, detail)
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
		deletedThisPass, err := deletePass(ctx, session, container, prefix, bulkSize, workers, result, opts.OnProgress, opts.OnFailure, opts.OnUnresolved)
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
	bulkSize, workers int, result *Result, onProgress func(deleted, failed int64),
	onFailure, onUnresolved func(name, detail string)) (int64, error) {

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
			// Only a validated reply's counts are credited; an untrusted
			// one is a diagnostic and names nothing.
			if bulk != nil && bulk.Validated {
				result.Deleted += bulk.Deleted
				result.NotFound += bulk.NotFound
				passDeleted += bulk.Deleted
			} else if bulk != nil {
				result.UntrustedReplies++
			}
			recordBatchOutcome(result, onFailure, onUnresolved, batch, bulk, err)
			if err != nil {
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
			chunk := names[start:end]
			bulk, err := session.BulkDelete(ctx, container, chunk)
			mu.Lock()
			if bulk != nil && bulk.Validated {
				result.Deleted += bulk.Deleted
				result.NotFound += bulk.NotFound
			} else if bulk != nil {
				result.UntrustedReplies++
			}
			recordBatchOutcome(result, opts.OnFailure, opts.OnUnresolved, chunk, bulk, err)
			if err != nil {
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
