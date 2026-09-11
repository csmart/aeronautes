package deletion

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/csmart/aeronautes/internal/swiftcontrol"
)

// ErrIdentityAccounting prevents a caller from treating failed local
// accounting as permission to start another destructive cleanup sweep.
var ErrIdentityAccounting = errors.New("deletion accounting storage unavailable")

type deletionAccounting struct {
	mu            sync.Mutex
	index         *identityIndex
	result        *Result
	opts          Options
	err           error
	stopAdmission context.CancelFunc
}

type pendingIdentity struct {
	facet  identityFacet
	detail string
}

func newDeletionAccounting(ctx context.Context, result *Result, opts Options, stopAdmission context.CancelFunc) (*deletionAccounting, error) {
	cache, err := os.UserCacheDir()
	if err != nil {
		return nil, fmt.Errorf("%w: locate cache: %w", ErrIdentityAccounting, err)
	}
	index, err := newIdentityIndex(ctx, filepath.Join(cache, "aeronautes", "deletion-identities"))
	if err != nil {
		return nil, fmt.Errorf("%w: setup: %w", ErrIdentityAccounting, err)
	}
	return &deletionAccounting{index: index, result: result, opts: opts, stopAdmission: stopAdmission}, nil
}

func (a *deletionAccounting) admit(ctx context.Context) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.err == nil && ctx.Err() == nil
}

func (a *deletionAccounting) problem() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.err
}

func (a *deletionAccounting) close() error {
	if err := a.index.close(); err != nil {
		return fmt.Errorf("%w: cleanup: %w", ErrIdentityAccounting, err)
	}
	return nil
}

// Both admission and accounting use the same guard, so a queued name
// cannot become an issued request after an accounting failure is latched.
func (a *deletionAccounting) record(ctx context.Context, pending []pendingIdentity) bool {
	if a.err != nil {
		return false
	}
	facets := make([]identityFacet, len(pending))
	for i, item := range pending {
		facets[i] = item.facet
	}
	// These requests were already admitted. Caller cancellation must not
	// discard their replies; the index still applies its own deadline.
	inserted, err := a.index.add(context.WithoutCancel(ctx), facets)
	if err != nil {
		a.err = fmt.Errorf("%w: record admitted outcomes: %w", ErrIdentityAccounting, err)
		a.result.AccountingIncomplete = true
		a.stopAdmission()
		return false
	}
	for i, fresh := range inserted {
		if !fresh {
			continue
		}
		item := pending[i]
		if item.facet.kind == reportedFailureIdentity {
			a.result.fail(a.opts.OnFailure, item.facet.name, item.detail)
		} else {
			a.result.unresolvedEffect(a.opts.OnUnresolved, item.facet.name, item.detail)
		}
	}
	return true
}

func (a *deletionAccounting) progress() {
	if a.opts.OnProgress != nil {
		a.opts.OnProgress(a.result.Deleted, a.result.Failed)
	}
}

func (a *deletionAccounting) recordBatchOutcome(ctx context.Context, batch []string, bulk *swiftcontrol.BulkDeleteResult, callErr error) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	validated := bulk != nil && bulk.Validated
	blamed := blamedNames(bulk)
	reason := "the reply is not dependable, so this name's effect is unknown"
	if callErr != nil {
		reason = callErr.Error()
	}
	pending := make([]pendingIdentity, 0, 2*len(batch))
	for _, name := range batch {
		if status, ok := blamed[name]; ok {
			pending = append(pending,
				pendingIdentity{identityFacet{reportedFailureIdentity, name}, status},
				pendingIdentity{identityFacet{unresolvedIdentity, name},
					fmt.Sprintf("the service reported %s and no observation establishes whether the object was destroyed", status)})
		} else if !validated {
			pending = append(pending, pendingIdentity{identityFacet{unresolvedIdentity, name}, reason})
		}
	}
	if !a.record(ctx, pending) {
		for _, name := range batch {
			detail := "this bulk request was admitted but not indexed; reply counts do not identify this name's effect"
			if status, ok := blamed[name]; ok {
				detail += "; service reported " + status
			} else if !validated {
				detail += "; " + reason
			}
			a.result.UnindexedAttempts = append(a.result.UnindexedAttempts, UnindexedAttempt{Name: name, Detail: detail})
		}
		return a.err
	}
	if validated {
		a.result.Deleted += bulk.Deleted
		a.result.NotFound += bulk.NotFound
	} else if bulk != nil {
		a.result.UntrustedReplies++
		if len(a.result.UntrustedAggregates) < maxFailures {
			a.result.UntrustedAggregates = append(a.result.UntrustedAggregates, UntrustedAggregate{
				Requested: len(batch), Deleted: bulk.Deleted, NotFound: bulk.NotFound,
				Anomalies: append([]string(nil), bulk.Anomalies...), MalformedRows: bulk.MalformedRows,
			})
		}
	}
	a.progress()
	return callErr
}

func (a *deletionAccounting) recordSingleOutcome(ctx context.Context, name string, callErr error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	var pending []pendingIdentity
	if callErr != nil && !swiftcontrol.IsNotFound(callErr) {
		pending = []pendingIdentity{{identityFacet{reportedFailureIdentity, name}, callErr.Error()}}
	}
	if !a.record(ctx, pending) {
		detail := "the service returned success for this admitted request, but its outcome was not indexed"
		if callErr != nil {
			detail = "this request was admitted but not indexed: " + callErr.Error()
		}
		a.result.UnindexedAttempts = append(a.result.UnindexedAttempts, UnindexedAttempt{Name: name, Detail: detail})
		return
	}
	switch {
	case callErr == nil:
		a.result.Deleted++
	case swiftcontrol.IsNotFound(callErr):
		a.result.NotFound++
	}
	a.progress()
}
