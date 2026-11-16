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
	"errors"
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
	// OnFailure receives every indexed per-name reported failure —
	// the complete-set channel behind Result.Failures' bounded sample.
	OnFailure func(name, detail string)
	// OnUnresolved receives every indexed bulk name whose effect this
	// run cannot state, including reported failures. The parallel
	// strategy currently records its reported-failure facet only.
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

// Failure is a reported error or unresolved effect, not proof of survival.
type Failure struct {
	Name string
	Err  string
}

// UnindexedAttempt preserves an admitted request after accounting fails.
// It may repeat a name and cannot supply a distinct failure count.
type UnindexedAttempt struct {
	Name   string `json:"name"`
	Detail string `json:"detail"`
}

// Result accounts for one run.
type Result struct {
	Strategy Strategy
	Deleted  int64
	NotFound int64
	Failures []Failure
	// Failed counts distinct indexed requested names the service
	// reported an error for, each counted once — Failures above is a
	// bounded sample for display; every one also streams through
	// Options.OnFailure so callers can retain the complete set
	// (K-DEL-002). It is a final exact total only when accounting is
	// complete; otherwise it is the last committed count.
	Failed int64
	// Unresolveds samples the names whose effect this run cannot state,
	// bounded for display exactly as Failures is.
	Unresolveds []Failure
	// Unresolved counts distinct indexed bulk names whose effect is
	// unknown, streamed uncapped through Options.OnUnresolved.
	// It overlaps Failed rather than extending it: a reported error is
	// a report about an attempt and witnesses no effect, so a reported
	// bulk name is also unresolved. A run holding any unresolved name
	// has not completed. With incomplete accounting this is only the
	// last committed count. Parallel deletion does not populate it yet.
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
	// NotRequestedAgain counts names a later listing offered that this
	// job had already sent a request for and therefore did not send
	// again; whatever now stands at such a name was never requested by
	// this job. NotRequestedAgainNames is a bounded sample of them.
	NotRequestedAgain      int64
	NotRequestedAgainNames []string
	// Passes is how many list+delete sweeps ran.
	Passes int
	// VerifyPolicy states the §32 postcondition this run checked.
	VerifyPolicy string
	// AccountingIncomplete means an admitted outcome could not be
	// indexed. All counters retain their last committed values; final
	// distinct totals are unavailable, even when no delete was credited.
	AccountingIncomplete bool
	// UnindexedAttempts contains only the current bulk request or the
	// bounded worker attempts admitted before accounting failed.
	UnindexedAttempts []UnindexedAttempt
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

func (r *Result) fail(onFailure func(name, detail string), name, detail string) {
	r.Failed++
	if len(r.Failures) < maxFailures {
		r.Failures = append(r.Failures, Failure{Name: name, Err: detail})
	}
	if onFailure != nil {
		onFailure(name, detail)
	}
}

// unresolvedEffect retains one name whose effect this run cannot state.
func (r *Result) unresolvedEffect(onUnresolved func(name, detail string), name, detail string) {
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
	container, prefix string, opts Options) (result *Result, err error) {

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

	result = &Result{Strategy: StrategyParallel}
	bulkSize := bulkBatch(capabilities, opts)
	if bulkSize > 0 {
		result.Strategy = StrategyBulk
	}
	result.VerifyPolicy = fmt.Sprintf("prefix limit=1 empty after ≤%d passes", passes)
	admissionCtx, stopAdmission := context.WithCancel(ctx)
	defer stopAdmission()
	accounting, err := newDeletionAccounting(ctx, result, opts, stopAdmission)
	if err != nil {
		return result, err
	}
	defer func() { err = errors.Join(err, accounting.close()) }()

	for pass := 1; pass <= passes; pass++ {
		result.Passes = pass
		before := result.Deleted
		if err := deletePass(ctx, admissionCtx, session, container, prefix, bulkSize, workers, accounting); err != nil {
			return result, err
		}
		if result.Deleted == before {
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
func deletePass(ctx, admissionCtx context.Context, session *swiftcontrol.Session, container, prefix string,
	bulkSize, workers int, accounting *deletionAccounting) error {

	if bulkSize > 0 {
		batch := make([]string, 0, bulkSize)
		flush := func() error {
			if len(batch) == 0 {
				return nil
			}
			if !accounting.admit(admissionCtx) {
				return errors.Join(accounting.problem(), admissionCtx.Err())
			}
			// One destructive request per name per job: the batch is
			// recorded as issued before it is sent, and names this job
			// already requested are dropped from it.
			fresh, ok := accounting.issue(admissionCtx, batch)
			batch = batch[:0]
			if !ok {
				return errors.Join(accounting.problem(), admissionCtx.Err())
			}
			if len(fresh) == 0 {
				return nil
			}
			bulk, err := session.BulkDelete(ctx, container, fresh)
			return accounting.recordBatchOutcome(ctx, fresh, bulk, err)
		}
		for entry, err := range session.Objects(admissionCtx, container, swiftcontrol.ObjectListOptions{Deep: true, Prefix: prefix}) {
			if err != nil {
				return err
			}
			if object, ok := entry.(swiftcontrol.ObjectEntry); ok {
				batch = append(batch, object.Name)
				if len(batch) == bulkSize {
					if err := flush(); err != nil {
						return err
					}
				}
			}
		}
		return flush()
	}

	// Parallel single DELETEs: LIST page → bounded queue → N workers.
	names := make(chan string, workers*2)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for name := range names {
				if !accounting.admit(admissionCtx) {
					return
				}
				fresh, ok := accounting.issue(admissionCtx, []string{name})
				if !ok {
					return
				}
				if len(fresh) == 0 {
					continue
				}
				err := session.DeleteObject(ctx, container, name)
				accounting.recordSingleOutcome(ctx, name, err)
			}
		}()
	}
	var listErr error
	for entry, err := range session.Objects(admissionCtx, container, swiftcontrol.ObjectListOptions{Deep: true, Prefix: prefix}) {
		if err != nil {
			listErr = err
			break
		}
		if object, ok := entry.(swiftcontrol.ObjectEntry); ok {
			select {
			case names <- object.Name:
			case <-admissionCtx.Done():
				listErr = admissionCtx.Err()
			}
			if listErr != nil {
				break
			}
		}
	}
	close(names)
	wg.Wait()
	return errors.Join(accounting.problem(), listErr, ctx.Err())
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

// bulkBatch is how many names one bulk-delete request may carry, or zero
// when the sweep deletes names one at a time: the operator forced the
// parallel strategy, or the cluster advertises no bulk delete. The requested
// batch is capped at 1000 and at what the cluster accepts.
func bulkBatch(capabilities *swiftcontrol.Capabilities, opts Options) int {
	if opts.ForceParallel || capabilities == nil {
		return 0
	}
	limit := capabilities.BulkDeleteMaxDeletes()
	if limit <= 0 {
		return 0
	}
	batch := opts.BulkSize
	if batch <= 0 || batch > 1000 {
		batch = 1000
	}
	return min(batch, limit)
}

// DeleteNames removes an explicit, already-decided name list (the
// Mirror delete half, §27.2): same native executors, manifest-only by
// construction, no listing pass — the caller's plan IS the scope.
// Verification is a bounded sample of HEADs expecting 404 (§32:
// operation-specific, never a namespace rescan).
func DeleteNames(ctx context.Context, session *swiftcontrol.Session, capabilities *swiftcontrol.Capabilities,
	container string, names []string, opts Options) (result *Result, err error) {

	workers := opts.Workers
	if workers <= 0 {
		workers = 8
	}
	if workers > 64 {
		workers = 64
	}
	result = &Result{Strategy: StrategyParallel, VerifyPolicy: "sampled HEAD=404 (≤10 names)"}
	bulkSize := bulkBatch(capabilities, opts)
	if bulkSize > 0 {
		result.Strategy = StrategyBulk
	}
	admissionCtx, stopAdmission := context.WithCancel(ctx)
	defer stopAdmission()
	accounting, err := newDeletionAccounting(ctx, result, opts, stopAdmission)
	if err != nil {
		return result, err
	}
	defer func() { err = errors.Join(err, accounting.close()) }()

	if bulkSize > 0 {
		for start := 0; start < len(names); start += bulkSize {
			if !accounting.admit(admissionCtx) {
				return result, errors.Join(accounting.problem(), admissionCtx.Err())
			}
			end := min(start+bulkSize, len(names))
			chunk := names[start:end]
			bulk, err := session.BulkDelete(ctx, container, chunk)
			if err := accounting.recordBatchOutcome(ctx, chunk, bulk, err); err != nil {
				return result, err
			}
		}
	} else {
		queue := make(chan string, workers*2)
		var wg sync.WaitGroup
		for i := 0; i < workers; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for name := range queue {
					if !accounting.admit(admissionCtx) {
						return
					}
					err := session.DeleteObject(ctx, container, name)
					accounting.recordSingleOutcome(ctx, name, err)
				}
			}()
		}
		var queueErr error
		for _, name := range names {
			select {
			case queue <- name:
			case <-admissionCtx.Done():
				queueErr = admissionCtx.Err()
			}
			if queueErr != nil {
				break
			}
		}
		close(queue)
		wg.Wait()
		if err := errors.Join(accounting.problem(), queueErr, ctx.Err()); err != nil {
			return result, err
		}
	}

	// Sampled postcondition.
	for i, name := range names {
		if i >= 10 {
			break
		}
		if err := ctx.Err(); err != nil {
			return result, err
		}
		if _, err := session.ObjectDetail(ctx, container, name); err == nil {
			result.Remaining++
		} else if !swiftcontrol.IsNotFound(err) && ctx.Err() == nil {
			return result, fmt.Errorf("verification HEAD %s: %w", name, err)
		}
	}
	return result, nil
}
