package app

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/csmart/aeronautes/internal/swiftcontrol"
)

// FindService is §16's "Find in container": an explicit, cancellable
// server-side scan — never a per-keystroke search. It pages the whole
// namespace with bounded memory, streams matches into a bounded buffer
// the UI polls, and reports scanned count and throughput honestly.
// Scans are read-only and ephemeral: no job row, no audit.
type FindService struct {
	connections *Connections

	mu    sync.Mutex
	scans map[string]*findScan
	// A forgotten scan may still be returning from a cancelled request.
	workers map[*findScan]struct{}
	order   uint64
	closed  bool
	drained chan struct{}
	clock   findClock
}

// NewFindService wires the service.
func NewFindService(connections *Connections) *FindService {
	return newFindService(connections, findClock{
		now: time.Now,
		afterFunc: func(delay time.Duration, callback func()) findTimer {
			return time.AfterFunc(delay, callback)
		},
	})
}

type findTimer interface{ Stop() bool }

type findClock struct {
	now       func() time.Time
	afterFunc func(time.Duration, func()) findTimer
}

func newFindService(connections *Connections, clock findClock) *FindService {
	return &FindService{
		connections: connections, clock: clock,
		scans: map[string]*findScan{}, workers: map[*findScan]struct{}{},
		drained: make(chan struct{}),
	}
}

type findScan struct {
	id      string
	order   uint64
	cancel  context.CancelFunc
	timer   findTimer
	expires time.Time
	scope   ListingScopeDTO

	scanned   int64
	matches   []FindMatchDTO
	truncated bool
	done      bool
	err       string
	started   time.Time
	finished  time.Time
}

// FindMatchDTO is one matching object row.
type FindMatchDTO struct {
	Name  string `json:"name"`
	Bytes int64  `json:"bytes"`
}

// FindStatusDTO is a poll snapshot.
type FindStatusDTO struct {
	Scanned int64          `json:"scanned"`
	Matches []FindMatchDTO `json:"matches"`
	// Truncated: more matches exist than the buffer keeps (the scan
	// still counts them all in Scanned; narrow the query).
	Truncated bool   `json:"truncated"`
	Done      bool   `json:"done"`
	Error     string `json:"error"`
	// ObjectsPerSecond is the live scan throughput.
	ObjectsPerSecond float64 `json:"objectsPerSecond"`
	// Scope is the account the scan was bound to at start (ADR 0059).
	Scope *ListingScopeDTO `json:"scope,omitempty"`
}

const (
	maxFindMatches   = 1000
	maxFindWorkers   = 4
	maxFindRecords   = 16
	findIdleLease    = 5 * time.Minute
	findShutdownWait = 5 * time.Second
)

// StartFind begins one scan over the container (optionally under a
// prefix), matching a case-insensitive substring. Returns the scan id.
func (s *FindService) StartFind(ctx context.Context, bookmarkID, container, prefix, query string) (string, error) {
	if container == "" || strings.TrimSpace(query) == "" {
		return "", errors.New("container and a non-empty query are required")
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	s.mu.Lock()
	closed := s.closed
	s.mu.Unlock()
	if closed {
		return "", errors.New("Find service is shutting down")
	}
	session, scope, err := s.connections.scopedSession(ctx, bookmarkID)
	if err != nil {
		return "", err
	}
	var raw [8]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	id := hex.EncodeToString(raw[:])
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return "", errors.New("Find service is shutting down")
	}
	if len(s.workers) >= maxFindWorkers {
		return "", errors.New("Find worker limit reached; wait for a scan to finish")
	}
	if s.scans[id] != nil {
		return "", errors.New("could not allocate a unique Find scan id")
	}
	if len(s.scans) >= maxFindRecords {
		var oldest *findScan
		for _, scan := range s.scans {
			if scan.done && (oldest == nil || scan.finished.Before(oldest.finished) ||
				(scan.finished.Equal(oldest.finished) && scan.order < oldest.order)) {
				oldest = scan
			}
		}
		if oldest == nil {
			return "", errors.New("Find retained scan limit reached")
		}
		s.retireFind(oldest)
	}
	scanCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	s.order++
	scan := &findScan{id: id, order: s.order, cancel: cancel, started: s.clock.now(), scope: scope}
	s.scans[id] = scan
	s.workers[scan] = struct{}{}
	s.renewFind(scan)

	needle := strings.ToLower(query)
	go func() {
		defer func() {
			s.mu.Lock()
			defer s.mu.Unlock()
			scan.done = true
			scan.finished = s.clock.now()
			scan.cancel()
			delete(s.workers, scan)
			if s.closed && len(s.workers) == 0 {
				close(s.drained)
			}
		}()
		for entry, err := range session.Objects(scanCtx, container, swiftcontrol.ObjectListOptions{
			Deep: true, Prefix: prefix,
		}) {
			if err != nil {
				s.mu.Lock()
				if !errors.Is(err, context.Canceled) {
					scan.err = err.Error()
				}
				s.mu.Unlock()
				return
			}
			object, ok := entry.(swiftcontrol.ObjectEntry)
			if !ok {
				continue
			}
			s.mu.Lock()
			if s.scans[id] != scan || scanCtx.Err() != nil {
				s.mu.Unlock()
				return
			}
			scan.scanned++
			if strings.Contains(strings.ToLower(object.Name), needle) {
				if len(scan.matches) < maxFindMatches {
					scan.matches = append(scan.matches, FindMatchDTO{Name: object.Name, Bytes: object.Bytes})
				} else {
					scan.truncated = true
				}
			}
			s.mu.Unlock()
		}
	}()
	return id, nil
}

// FindStatus polls one scan.
func (s *FindService) FindStatus(ctx context.Context, bookmarkID, id string) (*FindStatusDTO, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if bookmarkID == "" {
		return nil, fmt.Errorf("%w: name the bookmark the read is for", ErrNotConnected)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	scan, ok := s.scans[id]
	if ok && !s.clock.now().Before(scan.expires) {
		s.retireFind(scan)
		ok = false
	}
	if !ok {
		return nil, fmt.Errorf("no such scan %s", id)
	}
	if scan.scope.BookmarkID != bookmarkID {
		return nil, ErrAccountScopeChanged
	}
	s.renewFind(scan)
	end := scan.finished
	if end.IsZero() {
		end = s.clock.now()
	}
	elapsed := end.Sub(scan.started).Seconds()
	status := &FindStatusDTO{
		Scanned: scan.scanned, Truncated: scan.truncated,
		Done: scan.done, Error: scan.err,
		Matches: append([]FindMatchDTO(nil), scan.matches...),
		Scope:   &scan.scope,
	}
	if elapsed > 0 {
		status.ObjectsPerSecond = float64(scan.scanned) / elapsed
	}
	return status, nil
}

// CancelFind stops a scan and forgets it (the UI keeps whatever it
// last polled).
func (s *FindService) CancelFind(ctx context.Context, bookmarkID, id string) error {
	if bookmarkID == "" {
		return fmt.Errorf("%w: name the bookmark the read is for", ErrNotConnected)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	scan, ok := s.scans[id]
	if !ok {
		return nil
	}
	if scan.scope.BookmarkID != bookmarkID {
		return ErrAccountScopeChanged
	}
	s.retireFind(scan)
	return nil
}

func (s *FindService) renewFind(scan *findScan) {
	if scan.timer != nil {
		scan.timer.Stop()
	}
	scan.expires = s.clock.now().Add(findIdleLease)
	scan.timer = s.clock.afterFunc(findIdleLease, func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		// A callback already queued when a poll renews the lease must not
		// retire that newer lease or a different record with the same id.
		if s.scans[scan.id] == scan && !s.clock.now().Before(scan.expires) {
			s.retireFind(scan)
		}
	})
}

func (s *FindService) retireFind(scan *findScan) {
	delete(s.scans, scan.id)
	scan.timer.Stop()
	scan.matches = nil
	scan.cancel()
}

// ServiceShutdown is Wails' reserved lifecycle hook, not a bridge action.
// Waiting for actual exit distinguishes cleanup from a cancellation request.
func (s *FindService) ServiceShutdown() error {
	drained := s.stopFind()
	select {
	case <-drained:
		return nil
	default:
	}
	timedOut := make(chan struct{})
	timer := s.clock.afterFunc(findShutdownWait, func() { close(timedOut) })
	defer timer.Stop()
	select {
	case <-drained:
		return nil
	case <-timedOut:
		return errors.New("Find workers did not exit within the shutdown wait")
	}
}

func (s *FindService) stopFind() <-chan struct{} {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.closed {
		s.closed = true
		for _, scan := range s.scans {
			s.retireFind(scan)
		}
		for scan := range s.workers {
			scan.cancel()
		}
		if len(s.workers) == 0 {
			close(s.drained)
		}
	}
	return s.drained
}
