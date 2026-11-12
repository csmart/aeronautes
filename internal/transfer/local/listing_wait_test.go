package local

import "time"

// The commit's listing wait is sized in seconds for a real store. The
// mock's lag is counted in reads, so these tests wait milliseconds, and a
// test of a listing that never settles does not wait half a minute.
func init() {
	listingWait.first, listingWait.longest, listingWait.total = time.Millisecond, 5*time.Millisecond, 200*time.Millisecond
}
