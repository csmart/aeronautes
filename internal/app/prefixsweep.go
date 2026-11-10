package app

import (
	"context"
	"fmt"
	"sync"

	"github.com/csmart/aeronautes/internal/applog"
	"github.com/csmart/aeronautes/internal/jobs"
	"github.com/csmart/aeronautes/internal/swiftcontrol"
)

// sweepWorkers and sweepPasses are the bounded pool ADR 0085 kept and the
// re-list bound ADR 0095 decisions 2 and 3 run the per-name protocol under:
// eight members in flight at once, and at most three listing passes before
// the postcondition decides.
const (
	sweepWorkers = 8
	sweepPasses  = 3
)

// sweepMember is one listed row handed to the per-name protocol with the
// facts it was listed with (ADR 0095 decision 1).
type sweepMember struct {
	name  string
	bytes int64
	etag  string
	slo   bool
}

// prefixSweep runs a deletion job's listing passes over one scope: every
// object row listed is admitted to the job's item ledger a page at a time,
// marked issued before its final HEAD, deleted by the shared member protocol
// and settled with one outcome, on a bounded worker pool (ADR 0095 decisions
// 1 to 4). It keeps no run-wide set of names: a later pass asks the ledger
// which of a page's names the job already holds.
type prefixSweep struct {
	s          *DeleteService
	jobID      string
	bookmarkID string
	container  string
	prefix     string
	session    *swiftcontrol.Session

	mu    sync.Mutex
	lost  string
	tally sweepCounts
}

// sweepCounts is what the sweep knew when it ended: the members admitted,
// settled and deleted, how many names the final pass listed again after this
// job's only request for them, and whether a cancellation or a listing error
// ended it.
type sweepCounts struct {
	admitted, settled, deleted, heldAgain int64
	cancelled                             bool
	listErr                               error
}

func (r *prefixSweep) counts() sweepCounts {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.tally
}

// lostReason is the latch: once a ledger write failed, no further member is
// issued on any worker, while a member already past its issued mark finishes
// whole (ADR 0095 decision 4).
func (r *prefixSweep) lostReason() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.lost
}

func (r *prefixSweep) latch(reason string) {
	r.mu.Lock()
	if r.lost == "" {
		r.lost = reason
	}
	r.mu.Unlock()
}

// run executes up to sweepPasses listing passes. A pass that admits nothing
// new ends the sweep, as does a cancellation, a listing error or a lost
// record; what ended it is read from counts afterwards.
func (r *prefixSweep) run(ctx context.Context) {
	background := context.WithoutCancel(ctx)
	for pass := 1; pass <= sweepPasses; pass++ {
		admittedNew, cancelled, err := r.pass(ctx, background)
		r.mu.Lock()
		r.tally.cancelled = r.tally.cancelled || cancelled
		if err != nil {
			r.tally.listErr = err
		}
		r.mu.Unlock()
		if cancelled || err != nil || r.lostReason() != "" || admittedNew == 0 {
			return
		}
	}
}

// pass consumes one listing of the scope, page by page, admitting the names
// the ledger does not yet hold and feeding them to the workers. A listing
// that fails under a cancelled context reports the cancellation, not the
// error; a lost record ends the pass with nothing more queued.
func (r *prefixSweep) pass(ctx, background context.Context) (admittedNew int64, cancelled bool, listErr error) {
	r.mu.Lock()
	r.tally.heldAgain = 0
	r.mu.Unlock()
	queue := make(chan sweepMember, sweepWorkers)
	var wg sync.WaitGroup
	for range sweepWorkers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for m := range queue {
				r.member(ctx, background, m)
			}
		}()
	}
	defer func() {
		close(queue)
		wg.Wait()
	}()
	for page, err := range r.session.ObjectPages(ctx, r.container, swiftcontrol.ObjectListOptions{Deep: true, Prefix: r.prefix}) {
		if err != nil {
			if ctx.Err() != nil {
				return admittedNew, true, nil
			}
			return admittedNew, false, err
		}
		if r.lostReason() != "" {
			return admittedNew, false, nil
		}
		fresh, readErr, latched := r.admitPage(background, page.Entries)
		if readErr != nil {
			return admittedNew, false, readErr
		}
		if latched {
			return admittedNew, false, nil
		}
		admittedNew += int64(len(fresh))
		for i, m := range fresh {
			select {
			case queue <- m:
			case <-ctx.Done():
				// Members admitted and never issued are settled here as not
				// attempted; the listing is not consumed further.
				for _, rest := range fresh[i:] {
					r.notAttempted(background, rest)
				}
				return admittedNew, true, nil
			}
		}
	}
	return admittedNew, false, nil
}

// key is a member's ledger identity, qualified by its container so a segment
// job spanning the manifest's container and the segment container keeps every
// member distinct (ADR 0095 decision 8); for the single-container bulk sweep
// it is simply the container and the object.
func (r *prefixSweep) key(name string) string { return r.container + "/" + name }

// admitPage records one listing page's object rows the job does not yet
// hold, once and fail-closed: a page the ledger cannot record ends the sweep
// before any of its names is requested (ADR 0095 decision 2). The names the
// job already holds are the ones still listed after its only request for
// them; they are counted for the pass and never requested again.
func (r *prefixSweep) admitPage(ctx context.Context, entries []swiftcontrol.ListingEntry) (fresh []sweepMember, readErr error, latched bool) {
	rows := make([]sweepMember, 0, len(entries))
	keys := make([]string, 0, len(entries))
	for _, entry := range entries {
		if object, ok := entry.(swiftcontrol.ObjectEntry); ok {
			rows = append(rows, sweepMember{name: object.Name, bytes: object.Bytes, etag: object.ETag, slo: object.SLOListed})
			keys = append(keys, r.key(object.Name))
		}
	}
	fresh = rows[:0]
	var heldAgain int64
	for start := 0; start < len(rows); start += 1000 {
		end := min(start+1000, len(rows))
		// HeldKeys is a read: a failure here sent nothing and does not latch
		// the ledger, so it is reported as a listing-style error the caller
		// turns into failed or partial, never a false unavailable status.
		held, err := r.s.jobs.HeldKeys(ctx, r.jobID, keys[start:end])
		if err != nil {
			return nil, fmt.Errorf("the members the job holds could not be read, so no more were requested: %w", err), false
		}
		for _, row := range rows[start:end] {
			if held[r.key(row.name)] {
				heldAgain++
			} else {
				fresh = append(fresh, row)
			}
		}
	}
	intents := make([]jobs.ItemIntent, 0, len(fresh))
	for _, m := range fresh {
		intents = append(intents, jobs.ItemIntent{
			ItemIdentity: jobs.ItemIdentity{Key: r.key(m.name), Source: jobs.ItemLocation{
				Kind: jobs.ItemSwift, BookmarkID: r.bookmarkID, Container: r.container, Object: m.name}},
			Planned: true, Admitted: true, ListedBytes: m.bytes, ListedETag: m.etag, ListedSLO: m.slo,
		})
	}
	for start := 0; start < len(intents); start += 500 {
		end := min(start+500, len(intents))
		// AdmitItems is a write: a failure latches the ledger unavailable, so
		// the caller leaves the job open with that genuine status.
		if err := r.s.jobs.AdmitItems(ctx, r.jobID, intents[start:end]); err != nil {
			r.latch("the members could not be recorded, so no more were requested: " + err.Error())
			return nil, nil, true
		}
	}
	r.mu.Lock()
	r.tally.admitted += int64(len(fresh))
	r.tally.heldAgain += heldAgain
	r.mu.Unlock()
	if len(fresh) > 0 {
		r.publishCounters(ctx)
	}
	return fresh, nil, false
}

// member runs one listed row whole: the issued mark before its final HEAD,
// the shared protocol under one credentials snapshot, and one outcome. The
// job's cancellation lands between members, never inside one, and nothing
// is issued after a lost record (ADR 0095 decisions 3 and 4).
func (r *prefixSweep) member(ctx, background context.Context, m sweepMember) {
	if r.lostReason() != "" {
		// Admitted and never issued, which the reopen rule settles as not
		// attempted; nothing more is written to a ledger that lost a record.
		return
	}
	if ctx.Err() != nil {
		r.notAttempted(background, m)
		return
	}
	if err := r.s.jobs.MarkIssued(background, r.jobID, r.key(m.name)); err != nil {
		r.latch(fmt.Sprintf("the issued mark for %q could not be written, so it and every later member were not attempted: %v", m.name, err))
		return
	}
	result, err := r.session.DeleteMember(background, r.container, swiftcontrol.MemberRow{Name: m.name, Bytes: m.bytes, ETag: m.etag, SLOListed: m.slo})
	observation, successor, baseline := selectedMemberOutcome(result, err)
	if recErr := r.s.jobs.RecordDeletion(background, r.jobID, r.key(m.name), observation, successor, baseline); recErr != nil {
		r.latch(fmt.Sprintf("the outcome of %q could not be recorded after its request, so it is unresolved and no later member was issued: %v", m.name, recErr))
		return
	}
	r.mu.Lock()
	r.tally.settled++
	if observation.Result == jobs.ItemDeleted {
		r.tally.deleted++
	}
	r.mu.Unlock()
	r.publishCounters(background)
}

// notAttempted settles a member a cancellation leaves unissued; a record
// that cannot be written is logged, and the job is then left open by its
// caller (storeStoppedBeside), so that the reopen rule settles it.
func (r *prefixSweep) notAttempted(ctx context.Context, m sweepMember) {
	if r.lostReason() != "" {
		return
	}
	if err := r.s.jobs.RecordDeletion(ctx, r.jobID, r.key(m.name), jobs.ItemObservation{Result: jobs.ItemNotAttempted, Effect: jobs.EffectNotAttempted}, nil, nil); err != nil {
		applog.Errorf("job %s: member %q not attempted but unrecorded: %v", r.jobID, m.name, err)
	}
}

func (r *prefixSweep) publishCounters(ctx context.Context) {
	r.mu.Lock()
	counters := jobs.Counters{ObjectsPlanned: r.tally.admitted, ObjectsCopied: r.tally.settled, ObjectsDeleted: r.tally.deleted}
	r.mu.Unlock()
	_ = r.s.jobs.UpdateCounters(ctx, r.jobID, counters)
}
