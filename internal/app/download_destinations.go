package app

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"

	"github.com/csmart/aeronautes/internal/transfer/local"
)

// errDestinationOverlap refuses a download whose picked directory is the
// same as, inside, or contains the destination of another active
// download (ADR 0078); the message names that job.
var errDestinationOverlap = errors.New("the destination overlaps another download")

// destinationOverlapKind is the preflight problem kind for that refusal.
const destinationOverlapKind = "destination-overlap"

// activeDestination is one download's held destination while the job is
// active: the root for identity and the cleaned path for ancestry.
type activeDestination struct {
	root *local.DestinationRoot
	path string
}

// destinationRegistry holds the destinations of every active download,
// from the start's reservation until the job's pins are released. The
// check and the insert share the lock, so two starts into overlapping
// directories cannot both win.
type destinationRegistry struct {
	mu     sync.Mutex
	active map[string]activeDestination
}

// cleanDestinationPath is the comparable spelling of a picked directory.
func cleanDestinationPath(path string) string {
	if abs, err := filepath.Abs(path); err == nil {
		path = abs
	}
	return filepath.Clean(path)
}

// overlapsLocked reports the active job whose destination is the same
// directory as, an ancestor of, or a descendant of the candidate.
func (r *destinationRegistry) overlapsLocked(root *local.DestinationRoot, path string) (string, string, bool) {
	sep := string(filepath.Separator)
	for id, held := range r.active {
		if held.root.SameDirectory(root) ||
			strings.HasPrefix(path+sep, held.path+sep) ||
			strings.HasPrefix(held.path+sep, path+sep) {
			return id, held.path, true
		}
	}
	return "", "", false
}

// overlap reports the active download, if any, that the candidate would
// overlap; preflight names it as a rejecting problem.
func (r *destinationRegistry) overlap(root *local.DestinationRoot, path string) (string, string, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.overlapsLocked(root, cleanDestinationPath(path))
}

// reserve refuses the candidate when it overlaps an active download and
// otherwise records it under key, in one critical section.
func (r *destinationRegistry) reserve(key string, root *local.DestinationRoot, path string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	path = cleanDestinationPath(path)
	if id, dir, ok := r.overlapsLocked(root, path); ok {
		return fmt.Errorf("%w: download job %s is using %s; cancel it or pick another directory", errDestinationOverlap, id, dir)
	}
	if r.active == nil {
		r.active = map[string]activeDestination{}
	}
	r.active[key] = activeDestination{root: root, path: path}
	return nil
}

// rekey moves a reservation to the job id once the job row exists.
func (r *destinationRegistry) rekey(from, to string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if held, ok := r.active[from]; ok {
		delete(r.active, from)
		r.active[to] = held
	}
}

// release frees a reservation; the job's pins call it.
func (r *destinationRegistry) release(key string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.active, key)
}

// activeCount reports how many destinations are held, for tests.
func (r *destinationRegistry) activeCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.active)
}
