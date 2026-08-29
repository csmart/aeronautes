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
}

// NewFindService wires the service.
func NewFindService(connections *Connections) *FindService {
	return &FindService{connections: connections, scans: map[string]*findScan{}}
}

type findScan struct {
	cancel context.CancelFunc

	mu        sync.Mutex
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
}

const maxFindMatches = 1000

// StartFind begins one scan over the container (optionally under a
// prefix), matching a case-insensitive substring. Returns the scan id.
func (s *FindService) StartFind(ctx context.Context, container, prefix, query string) (string, error) {
	if container == "" || strings.TrimSpace(query) == "" {
		return "", errors.New("container and a non-empty query are required")
	}
	session, err := s.connections.Session()
	if err != nil {
		return "", err
	}
	var raw [8]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	id := hex.EncodeToString(raw[:])
	scanCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	scan := &findScan{cancel: cancel, started: time.Now()}
	s.mu.Lock()
	s.scans[id] = scan
	s.mu.Unlock()

	needle := strings.ToLower(query)
	go func() {
		defer func() {
			scan.mu.Lock()
			scan.done = true
			scan.finished = time.Now()
			scan.mu.Unlock()
		}()
		for entry, err := range session.Objects(scanCtx, container, swiftcontrol.ObjectListOptions{
			Deep: true, Prefix: prefix,
		}) {
			if err != nil {
				scan.mu.Lock()
				if !errors.Is(err, context.Canceled) {
					scan.err = err.Error()
				}
				scan.mu.Unlock()
				return
			}
			object, ok := entry.(swiftcontrol.ObjectEntry)
			if !ok {
				continue
			}
			scan.mu.Lock()
			scan.scanned++
			if strings.Contains(strings.ToLower(object.Name), needle) {
				if len(scan.matches) < maxFindMatches {
					scan.matches = append(scan.matches, FindMatchDTO{Name: object.Name, Bytes: object.Bytes})
				} else {
					scan.truncated = true
				}
			}
			scan.mu.Unlock()
		}
	}()
	return id, nil
}

// FindStatus polls one scan.
func (s *FindService) FindStatus(ctx context.Context, id string) (*FindStatusDTO, error) {
	s.mu.Lock()
	scan, ok := s.scans[id]
	s.mu.Unlock()
	if !ok {
		return nil, fmt.Errorf("no such scan %s", id)
	}
	scan.mu.Lock()
	defer scan.mu.Unlock()
	end := scan.finished
	if end.IsZero() {
		end = time.Now()
	}
	elapsed := end.Sub(scan.started).Seconds()
	status := &FindStatusDTO{
		Scanned: scan.scanned, Truncated: scan.truncated,
		Done: scan.done, Error: scan.err,
		Matches: append([]FindMatchDTO(nil), scan.matches...),
	}
	if elapsed > 0 {
		status.ObjectsPerSecond = float64(scan.scanned) / elapsed
	}
	return status, nil
}

// CancelFind stops a scan and forgets it (the UI keeps whatever it
// last polled).
func (s *FindService) CancelFind(ctx context.Context, id string) error {
	s.mu.Lock()
	scan, ok := s.scans[id]
	delete(s.scans, id)
	s.mu.Unlock()
	if !ok {
		return nil
	}
	scan.cancel()
	return nil
}
