package swiftcontrol

import (
	"context"
	"fmt"
	"net/url"
	"sync"
)

// archiveReaders bounds how many container HEADs the owner scan has in
// flight: enough to keep a preview quick on a remote cluster, few enough not
// to look like a flood to it.
const archiveReaders = 8

// ArchiveOwners is who archives into a container: the containers of the
// account whose legacy versions or history location names it (ADR 0102
// decision 3). A legacy location is a container of the same account, so the
// account is the whole of where an owner can be.
type ArchiveOwners struct {
	// Owners names the containers whose location is the target, in listing
	// order.
	Owners []string
	// Unestablished says why the answer may be incomplete, in fixed words
	// that carry no identifier of the account; empty when every container
	// was read. It never says a container is not an archive, only that no
	// owner was found among what was read.
	Unestablished string
}

// ArchiveOwners reads every container of the account for a location naming
// target, up to bound containers, archiveReaders at a time. An account above
// the bound is not read at all, and a listing or HEAD that fails leaves the
// answer unestablished with whatever owners were found, because this is a
// disclosure for a dialog and not a gate: the caller states what it learnt.
func (s *Session) ArchiveOwners(ctx context.Context, target string, bound int) ArchiveOwners {
	portrait, err := s.AccountPortrait(ctx)
	if err != nil {
		return ArchiveOwners{Unestablished: "the account could not be read"}
	}
	if portrait.Containers > int64(bound) {
		return ArchiveOwners{Unestablished: fmt.Sprintf("the account has %d containers and this check reads at most %d", portrait.Containers, bound)}
	}
	var names []string
	for entry, err := range s.Containers(ctx, ListOptions{}) {
		if err != nil {
			return ArchiveOwners{Unestablished: "the account's containers could not be listed"}
		}
		names = append(names, entry.Name)
		if len(names) > bound {
			// The count said fewer; the listing is the truth and is not read
			// past the bound either.
			return ArchiveOwners{Unestablished: fmt.Sprintf("the account has more than %d containers, which is as many as this check reads", bound)}
		}
	}
	type reading struct {
		archive string
		err     error
	}
	readings := make([]reading, len(names))
	var wg sync.WaitGroup
	slots := make(chan struct{}, archiveReaders)
	for i, name := range names {
		wg.Add(1)
		go func() {
			defer wg.Done()
			slots <- struct{}{}
			defer func() { <-slots }()
			detail, err := s.ContainerDetail(ctx, name)
			if err != nil {
				readings[i].err = err
				return
			}
			readings[i].archive = detail.Versioning.Archive
		}()
	}
	wg.Wait()
	var owners ArchiveOwners
	for i, r := range readings {
		if r.err != nil {
			owners.Unestablished = "a container's versioning could not be read"
			continue
		}
		if r.archive == "" {
			continue
		}
		// A legacy location travels percent-encoded, as the client that set it
		// quoted it, while the container's own name does not; the match
		// decodes once, here, and the exact observation elsewhere keeps the
		// wire value. A plus stays a plus: this is a path, not a query.
		location, err := url.PathUnescape(r.archive)
		if err != nil {
			owners.Unestablished = "a container's versioning location could not be interpreted"
			continue
		}
		if location == target {
			owners.Owners = append(owners.Owners, names[i])
		}
	}
	return owners
}
