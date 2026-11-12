package local

import (
	"context"
	"errors"
	"time"
)

// listingWait bounds how long a commit waits for a segment listing that
// lags behind the upload (F35). The lag measured on a genuine Swift cloud
// was short; the bound is long enough to ride that out and short enough
// that a segment that never appears fails the item rather than the job.
var listingWait = struct{ first, longest, total time.Duration }{
	first: 250 * time.Millisecond, longest: 4 * time.Second, total: 30 * time.Second,
}

// untilListed repeats attempt while it reports that it is waiting for the
// listing, with a doubling delay, and returns its last answer once the
// bound is spent. An attempt that is not waiting ends the loop at once.
func untilListed(ctx context.Context, attempt func() (waiting bool, err error)) error {
	delay := listingWait.first
	deadline := time.Now().Add(listingWait.total)
	for {
		waiting, err := attempt()
		if !waiting || time.Now().Add(delay).After(deadline) {
			return err
		}
		select {
		case <-ctx.Done():
			return errors.Join(err, ctx.Err())
		case <-time.After(delay):
		}
		delay = min(2*delay, listingWait.longest)
	}
}
