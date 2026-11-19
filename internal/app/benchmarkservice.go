package app

import (
	"bytes"
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/csmart/aeronautes/internal/applog"
	"github.com/csmart/aeronautes/internal/audit"
	"github.com/csmart/aeronautes/internal/db"
	"github.com/csmart/aeronautes/internal/jobs"
	"github.com/csmart/aeronautes/internal/swiftcontrol"
)

// BenchmarkService is §42: a consent-gated measurement job against a
// clearly-named temporary Aeronautes-owned container. A benchmark
// writes real objects, so it never runs on the strength of a container
// name alone — the operator types the target account back. Results
// persist per bookmark and the recommendation feeds the Auto profile.
type BenchmarkService struct {
	connections *Connections
	jobs        *jobs.Store
	runner      *jobRunner
	db          *db.DB

	// objects/concurrency are test-tunable; zero means the defaults.
	objects       int
	concurrencies []int
}

const benchmarkObjectLimit = 200

type benchmarkObject struct {
	name         string
	putIssued    bool
	deleteIssued bool
	deleteErr    error
}

// NewBenchmarkService wires the service.
func NewBenchmarkService(connections *Connections, jobStore *jobs.Store, runner *jobRunner, database *db.DB) *BenchmarkService {
	return &BenchmarkService{connections: connections, jobs: jobStore, runner: runner, db: database}
}

// setScale overrides the workload for tests.
func (s *BenchmarkService) setScale(objects int, concurrencies []int) {
	s.objects = objects
	s.concurrencies = concurrencies
}

// BenchmarkRequestDTO names the target and carries the typed consent.
type BenchmarkRequestDTO struct {
	BookmarkID string `json:"bookmarkId"`
	// Confirm must equal the target account name (the storage-URL
	// suffix shown in the UI) — the §42 typed cluster confirmation.
	Confirm string `json:"confirm"`
}

// BenchmarkPhaseDTO is one measured phase.
type BenchmarkPhaseDTO struct {
	Phase       string  `json:"phase"`
	Concurrency int     `json:"concurrency"`
	OpsPerSec   float64 `json:"opsPerSec"`
}

// BenchmarkResultDTO is the persisted document.
type BenchmarkResultDTO struct {
	RanAt          string              `json:"ranAt"`
	Phases         []BenchmarkPhaseDTO `json:"phases"`
	Recommendation TransferSettingsDTO `json:"recommendation"`
}

// Latest returns the stored result for a bookmark, nil when none.
func (s *BenchmarkService) Latest(ctx context.Context, bookmarkID string) (*BenchmarkResultDTO, error) {
	var ranAt, results, recommendation string
	err := s.db.Reader().QueryRowContext(ctx, `
		SELECT ran_at, results_json, recommendation_json FROM benchmark_results WHERE bookmark_id=?`,
		bookmarkID).Scan(&ranAt, &results, &recommendation)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	out := &BenchmarkResultDTO{RanAt: ranAt}
	if err := json.Unmarshal([]byte(results), &out.Phases); err != nil {
		return nil, err
	}
	if err := json.Unmarshal([]byte(recommendation), &out.Recommendation); err != nil {
		return nil, err
	}
	return out, nil
}

// RecommendationFor is the §40 Auto hook (nil when no run recorded).
func (s *BenchmarkService) recommendationFor(bookmarkID string) *TransferSettingsDTO {
	result, err := s.Latest(context.Background(), bookmarkID)
	if err != nil || result == nil {
		return nil
	}
	recommendation := result.Recommendation
	return &recommendation
}

// StartBenchmark validates the typed consent and launches the job.
func (s *BenchmarkService) StartBenchmark(ctx context.Context, req BenchmarkRequestDTO) (string, error) {
	if err := refuseIfDeferred("BenchmarkService.StartBenchmark"); err != nil {
		return "", err
	}
	if req.BookmarkID == "" {
		return "", errors.New("bookmark is required")
	}
	owner, ctx, err := s.runner.admit(ctx)
	if err != nil {
		return "", err
	}
	defer owner.finishStart()
	releasePins, err := s.connections.pinForJob(ctx, req.BookmarkID)
	if err != nil {
		return "", err
	}
	owner.hold(releasePins)
	session, err := s.connections.SessionFor(ctx, req.BookmarkID)
	if err != nil {
		return "", err
	}
	account := accountSuffix(session.StorageURL())
	if req.Confirm != account {
		return "", fmt.Errorf("confirmation %q does not match the target account %q — type it exactly (§42: a benchmark writes real objects)", req.Confirm, account)
	}
	capabilities, err := session.FetchCapabilities(ctx)
	if err != nil {
		return "", err
	}
	var raw [4]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	container := "aeronautes-benchmark-" + hex.EncodeToString(raw[:])

	objects := s.objects
	if objects <= 0 || objects > benchmarkObjectLimit {
		objects = benchmarkObjectLimit
	}
	concurrencies := s.concurrencies
	if len(concurrencies) == 0 {
		concurrencies = []int{4, 16}
	}

	job, err := s.jobs.Create(ctx, jobs.Job{
		Type:             jobs.Type("benchmark"),
		Source:           account,
		Destination:      container,
		SourceBookmarkID: req.BookmarkID,
		Engine:           "native",
	})
	if err != nil {
		return "", err
	}
	s.runner.register(job.ID, owner.cancel)
	s.runner.record(ctx, req.BookmarkID, "benchmark.start", account, "job "+job.ID+" in "+container, audit.ResultApplied)
	owner.launch(job.ID, req.BookmarkID, func(runCtx context.Context) {
		s.runBenchmark(runCtx, job.ID, req.BookmarkID, session, capabilities, container, objects, concurrencies)
	}, nil)
	return job.ID, nil
}

func accountSuffix(storageURL string) string {
	if i := strings.LastIndex(storageURL, "/"); i >= 0 {
		return storageURL[i+1:]
	}
	return storageURL
}

// timed runs n operations at the given concurrency and returns ops/s.
func timed(ctx context.Context, n, concurrency int, op func(i int) error) (float64, error) {
	admission, stop := context.WithCancel(ctx)
	defer stop()
	if concurrency < 1 {
		concurrency = 1
	}
	start := time.Now()
	queue := make(chan int, concurrency)
	var wg sync.WaitGroup
	var firstErr error
	var mu sync.Mutex
	for w := 0; w < concurrency; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range queue {
				mu.Lock()
				admitted := firstErr == nil && admission.Err() == nil
				mu.Unlock()
				if !admitted {
					return
				}
				if err := op(i); err != nil {
					mu.Lock()
					if firstErr == nil {
						firstErr = err
						stop()
					}
					mu.Unlock()
				}
			}
		}()
	}
	for i := 0; i < n; i++ {
		if admission.Err() != nil {
			break
		}
		select {
		case queue <- i:
		case <-admission.Done():
		}
	}
	close(queue)
	wg.Wait()
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if firstErr != nil {
		return 0, firstErr
	}
	elapsed := time.Since(start).Seconds()
	if elapsed <= 0 {
		elapsed = 1e-6
	}
	return float64(n) / elapsed, nil
}

func (s *BenchmarkService) runBenchmark(ctx context.Context, jobID, bookmarkID string,
	session *swiftcontrol.Session, capabilities *swiftcontrol.Capabilities,
	container string, objects int, concurrencies []int) {

	background := context.WithoutCancel(ctx)
	counters := jobs.Counters{}
	workload := make([]benchmarkObject, objects)
	for i := range workload {
		workload[i].name = fmt.Sprintf("bench/%06d", i)
	}
	finish := func(state jobs.State, message string) {
		var unresolved []deletionAttempt
		for _, object := range workload {
			if object.deleteErr != nil {
				unresolved = append(unresolved, deletionAttempt{Name: object.name, Detail: object.deleteErr.Error()})
			}
		}
		if len(unresolved) != 0 {
			state = jobs.StatePartial
			message += fmt.Sprintf("; unresolved deletion outcomes: %d; benchmark container retained", len(unresolved))
			path, err := retainDeletionAttempts(unresolved)
			if err != nil {
				message += "; issued identities could not all be retained: " + err.Error()
			} else {
				message += "; unresolved deletion attempts: " + path
			}
		}
		// Deregister before the terminal state is visible — a terminal
		// benchmark must not stay cancel-addressable (review F-11).
		s.runner.release(jobID)
		summary := "job " + jobID
		if err := s.jobs.Finish(background, jobID, jobs.Completion{
			State: state, Counters: counters, Error: message,
			Audit: audit.Event{BookmarkID: bookmarkID, Operation: "benchmark." + string(state),
				Resource: container, Summary: summary, Result: terminalAuditResult(state)},
		}, s.runner.audit); err != nil {
			applog.Errorf("job %s: finish benchmark: %v", jobID, err)
		}
	}
	deleteOne := func(deleteCtx context.Context, i int) error {
		object := &workload[i]
		if !object.putIssued || object.deleteIssued {
			return nil
		}
		object.deleteIssued = true
		object.deleteErr = session.DeleteObject(deleteCtx, container, object.name)
		if swiftcontrol.IsNotFound(object.deleteErr) {
			object.deleteErr = nil
		}
		return object.deleteErr
	}
	cleanup := func() error {
		for _, object := range workload {
			if object.deleteErr != nil {
				return errors.New("an issued deletion is unresolved")
			}
		}
		// A listing cannot grant ownership, even inside this container.
		if _, err := timed(background, len(workload), 8, func(i int) error {
			return deleteOne(background, i)
		}); err != nil {
			return err
		}
		return session.DeleteContainer(background, container)
	}
	fail := func(stage string, err error) {
		reason := stage + ": " + err.Error()
		if cleanupErr := cleanup(); cleanupErr != nil {
			finish(jobs.StatePartial, reason+"; cleanup: "+cleanupErr.Error())
			return
		}
		if errors.Is(err, context.Canceled) {
			finish(jobs.StateCancelled, reason)
			return
		}
		finish(jobs.StateFailed, reason)
	}

	_ = s.jobs.Transition(background, jobID, jobs.StatePlanning)
	if _, err := session.CreateContainer(ctx, swiftcontrol.ContainerCreateRequest{Name: container}); err != nil {
		finish(jobs.StateFailed, "create container: "+err.Error())
		return
	}
	_ = s.jobs.Transition(background, jobID, jobs.StateRunning)

	// Planned = the operations that actually bump: uploads (~objects
	// across the levels), one listing pass, HEADs (objects × levels),
	// and deletes (objects). The §42 result is an ENDPOINT
	// MICROBENCHMARK — small native PUT/HEAD/DELETE ops — not a
	// transfer-path prediction.
	counters = jobs.Counters{ObjectsPlanned: int64(objects * (3 + len(concurrencies)))}
	bump := func(n int) {
		counters.ObjectsCopied += int64(n)
		_ = s.jobs.UpdateCounters(background, jobID, counters)
	}
	payload := bytes.Repeat([]byte{0x42}, 8*1024)
	var phases []BenchmarkPhaseDTO
	record := func(phase string, concurrency int, ops float64) {
		phases = append(phases, BenchmarkPhaseDTO{Phase: phase, Concurrency: concurrency, OpsPerSec: ops})
	}

	// Upload at each concurrency (fresh names per level).
	bestUpload, bestUploadOps := concurrencies[0], 0.0
	total := 0
	for _, c := range concurrencies {
		base := total
		ops, err := timed(ctx, objects/len(concurrencies), c, func(i int) error {
			object := &workload[base+i]
			object.putIssued = true
			_, err := session.PutObject(ctx, container, object.name, bytes.NewReader(payload), "application/octet-stream", nil)
			return err
		})
		if err != nil {
			fail("upload", err)
			return
		}
		total += objects / len(concurrencies)
		record("upload", c, ops)
		if ops > bestUploadOps {
			bestUpload, bestUploadOps = c, ops
		}
		bump(objects / len(concurrencies))
	}

	// Listing throughput (whole-container deep pages).
	listStart := time.Now()
	listed := 0
	for entry, err := range session.Objects(ctx, container, swiftcontrol.ObjectListOptions{Deep: true, PageSize: 1000}) {
		if err != nil {
			fail("listing", err)
			return
		}
		if _, ok := entry.(swiftcontrol.ObjectEntry); ok {
			listed++
		}
	}
	record("listing", 1, float64(listed)/max(time.Since(listStart).Seconds(), 1e-6))
	bump(listed)

	// HEAD at each concurrency.
	bestHead, bestHeadOps := concurrencies[0], 0.0
	for _, c := range concurrencies {
		ops, err := timed(ctx, total, c, func(i int) error {
			_, err := session.ObjectDetail(ctx, container, workload[i].name)
			return err
		})
		if err != nil {
			fail("head", err)
			return
		}
		record("head", c, ops)
		if ops > bestHeadOps {
			bestHead, bestHeadOps = c, ops
		}
		bump(total)
	}

	nativeCount := total
	bulkSize := min(capabilities.BulkDeleteMaxDeletes(), 1000)
	if bulkSize > 0 {
		nativeCount = total / 2
	}
	ops, err := timed(ctx, nativeCount, bestHead, func(i int) error {
		return deleteOne(ctx, i)
	})
	if err != nil {
		fail("delete", err)
		return
	}
	record("delete", bestHead, ops)
	bump(nativeCount)
	if bulkSize > 0 {
		bulkStart := time.Now()
		if err := deleteBenchmarkBatches(ctx, session, container, workload[nativeCount:total], bulkSize); err != nil {
			fail("bulk-delete", err)
			return
		}
		record("bulk-delete", 1, float64(total-nativeCount)/max(time.Since(bulkStart).Seconds(), 1e-6))
		bump(total - nativeCount)
	}

	// Cleanup: the container must be gone.
	if err := cleanup(); err != nil {
		finish(jobs.StatePartial, "benchmark container could not be removed: "+err.Error())
		return
	}

	recommendation := TransferSettingsDTO{Transfers: bestUpload, Checkers: bestHead}
	resultsJSON, _ := json.Marshal(phases)
	recommendationJSON, _ := json.Marshal(recommendation)
	err = s.db.WriteTx(background, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(background, `
			INSERT INTO benchmark_results (bookmark_id, ran_at, results_json, recommendation_json)
			VALUES (?, ?, ?, ?)
			ON CONFLICT(bookmark_id) DO UPDATE SET
				ran_at=excluded.ran_at, results_json=excluded.results_json,
				recommendation_json=excluded.recommendation_json`,
			bookmarkID, time.Now().UTC().Format(time.RFC3339), string(resultsJSON), string(recommendationJSON))
		return err
	})
	if err != nil {
		finish(jobs.StatePartial, "results not persisted: "+err.Error())
		return
	}
	document, _ := json.Marshal(BenchmarkResultDTO{Phases: phases, Recommendation: recommendation})
	_ = s.jobs.SetSettings(background, jobID, string(document))
	_ = s.jobs.Transition(background, jobID, jobs.StateVerifying)
	finish(jobs.StateComplete, "")
}

func deleteBenchmarkBatches(ctx context.Context, session *swiftcontrol.Session, container string, objects []benchmarkObject, size int) error {
	for start := 0; start < len(objects); start += size {
		if err := ctx.Err(); err != nil {
			return err
		}
		batch := objects[start:min(start+size, len(objects))]
		names := make([]string, len(batch))
		for i := range batch {
			batch[i].deleteIssued = true
			names[i] = batch[i].name
		}
		result, err := session.BulkDelete(ctx, container, names)
		if err != nil {
			for i := range batch {
				batch[i].deleteErr = err
			}
			return err
		}
		for i := range batch {
			for _, failure := range result.Failures {
				if batch[i].name == failure.Name {
					batch[i].deleteErr = errors.New(failure.Status)
				}
			}
		}
		if len(result.Failures) != 0 {
			return fmt.Errorf("bulk-delete reported %d failed names", len(result.Failures))
		}
	}
	return nil
}
