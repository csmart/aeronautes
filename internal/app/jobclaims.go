package app

import (
	"errors"
	"fmt"
	"strings"
)

// errScopeClaimed refuses a start whose scope overlaps what another job of
// this application holds (ADR 0105 decision 3); the message names that job.
var errScopeClaimed = errors.New("another job is using that part of the container")

// scopeClaim is what a job writes into or sweeps, held by its owner from the
// start until the job's whole body has returned (ADR 0105 decision 3). What a
// transfer writes is ordinary objects, and a deletion sweep deletes every row
// it lists, so the two must not overlap while both jobs are alive; until
// several jobs could run, the single job was what kept them apart.
type scopeClaim struct {
	// sweep is a deletion sweep's claim on what it lists; otherwise the claim
	// is a transfer's, on where it writes.
	sweep bool
	// account is the account identifier the session had resolved when the
	// claim was taken, compared by the copy overlap guard's rule
	// (distinctAccounts), so one that could not be read fails closed.
	account   string
	container string
	// path is the folder a transfer writes under, or the prefix a sweep
	// lists ("" for a whole container).
	path string
}

// destinationClaims are the two claims of an upload or a copy: the folder it
// writes under in the destination container and the same path in that
// container's segments container, where the engine puts a large object's
// segments (ADR 0122 decisions 3 and 4).
func destinationClaims(account, container, destPrefix string) []scopeClaim {
	return []scopeClaim{
		{account: account, container: container, path: destPrefix},
		{account: account, container: container + "_segments", path: destPrefix},
	}
}

func sweepClaim(account, container, prefix string) scopeClaim {
	return scopeClaim{sweep: true, account: account, container: container, path: prefix}
}

// overlaps reports whether a sweep and a transfer could meet: one account, one
// container, and one path inside the other. Two claims of one kind never
// overlap: two transfers to one name are two writers, where the last one
// wins as the store defines, and two sweeps harm nothing of each other's.
func (c scopeClaim) overlaps(held scopeClaim) bool {
	return c.sweep != held.sweep && c.container == held.container && !distinctAccounts(c.account, held.account) &&
		(strings.HasPrefix(c.path, held.path) || strings.HasPrefix(held.path, c.path))
}

// holder names a job in a refusal. A start that has not yet created its row
// has no id to give.
func (o *jobOwner) holder() string {
	if o.jobID == "" {
		return "a job that is starting"
	}
	return "job " + o.jobID
}

// claim records what this job writes into or sweeps, or refuses it when it
// overlaps a claim of the other kind that another job holds. The check and the
// record are one step under the runner's lock, so two starts that conflict
// cannot both win, and the claim leaves with the owner, after the job's whole
// body has returned, with no release of its own to forget.
func (o *jobOwner) claim(claims ...scopeClaim) error {
	r := o.runner
	r.mu.Lock()
	defer r.mu.Unlock()
	for other := range r.owners {
		if other == o {
			continue
		}
		for _, held := range other.claims {
			for _, wanted := range claims {
				if !wanted.overlaps(held) {
					continue
				}
				if wanted.sweep {
					return fmt.Errorf("%w: %s is writing into container %s; wait for it to finish or cancel it before deleting there",
						errScopeClaimed, other.holder(), held.container)
				}
				return fmt.Errorf("%w: %s is deleting from container %s; wait for it to finish or cancel it before transferring there",
					errScopeClaimed, other.holder(), held.container)
			}
		}
	}
	o.claims = append(o.claims, claims...)
	return nil
}

// refuseStagedName refuses a deletion of named objects when one of its names
// lies under the folder a transfer is writing into. Such a deletion asks at
// its start and holds no claim of its
// own: its names are fixed before it runs, so it cannot reach a transfer
// that begins later.
func (r *jobRunner) refuseStagedName(account, container, name string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for owner := range r.owners {
		for _, held := range owner.claims {
			if !held.sweep && held.container == container && !distinctAccounts(account, held.account) && strings.HasPrefix(name, held.path) {
				return fmt.Errorf("%w: %s/%s is where %s is writing; wait for it to finish or cancel it before deleting there",
					errScopeClaimed, container, name, owner.holder())
			}
		}
	}
	return nil
}

// stagedByLiveJob reports the live job that is writing where an object name
// lies. A sweep that takes segments asks it for each segment: an ordinary
// live object can name segments a job is still writing, and those are kept
// (ADR 0105 decision 3).
func (r *jobRunner) stagedByLiveJob(account, container, name string) (string, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for owner := range r.owners {
		if owner.jobID == "" {
			continue
		}
		for _, held := range owner.claims {
			if !held.sweep && held.container == container && !distinctAccounts(account, held.account) && strings.HasPrefix(name, held.path) {
				return owner.holder(), true
			}
		}
	}
	return "", false
}
