package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/csmart/aeronautes/internal/audit"
	"github.com/csmart/aeronautes/internal/evidence"
	"github.com/csmart/aeronautes/internal/jobs"
	"github.com/csmart/aeronautes/internal/swiftcontrol"
	transfercopy "github.com/csmart/aeronautes/internal/transfer/copy"
	"github.com/csmart/aeronautes/internal/transfer/plan"
	"github.com/csmart/aeronautes/internal/transfer/rclone"
)

// CopyRequestDTO scopes one cross-account copy: bookmark + container +
// prefix on each end. Prefixes are "" or end with "/".
type CopyRequestDTO struct {
	SourceBookmarkID string `json:"sourceBookmarkId"`
	SourceContainer  string `json:"sourceContainer"`
	SourcePrefix     string `json:"sourcePrefix"`
	DestBookmarkID   string `json:"destBookmarkId"`
	DestContainer    string `json:"destContainer"`
	DestPrefix       string `json:"destPrefix"`
	// Profile is the §40 performance mode ("" = balanced).
	Profile string `json:"profile"`
	// Settings are the per-job §44.1 overrides layered over the profile
	// (§38); with Profile "custom" they are the whole configuration.
	Settings TransferSettingsDTO `json:"settings"`
	// AcknowledgeOverwrite says the operator asked for a copy that may
	// replace existing destinations (ADR 0087). It is an accident guard, not
	// the authorisation: the backend still raises the one native question
	// that names both accounts and captures the destination baseline the
	// ADR 0085 comparison rests on, and an unacknowledged copy behaves
	// exactly as it does today (ADR 0081 decision 1's shape).
	AcknowledgeOverwrite bool `json:"acknowledgeOverwrite"`
}

// CopyPlanDTO is the §65.4 preflight surface: honest conservative
// counts (§28.4) plus the disclosures the operator must see before
// execution.
type CopyPlanDTO struct {
	Summary plan.Summary `json:"summary"`
	// Versioning does not grant permission to overwrite an existing object.
	VersionedDestination bool `json:"versionedDestination"`
	// LargeObjectsAsDLO: large sources commit as destination DLOs whose
	// reads depend on listing consistency (§28.6 disclosure).
	LargeObjectsAsDLO bool `json:"largeObjectsAsDlo"`
	// DataPath names where the bytes physically travel (K-XFER-005):
	// v1 always hairpins through this workstation — stated, never
	// implied.
	DataPath string `json:"dataPath"`
	// OverwriteBytes counts listed source bytes in refused Changed rows;
	// keeping the bridge field does not promise an overwrite or transfer.
	OverwriteBytes int64 `json:"overwriteBytes"`
}

// TransferService is the bridge's transfer surface (ADR 0011): planning
// previews, job start, and cancellation. Execution itself runs in a
// background goroutine against the §46 job store; the drawer follows it
// through JobService.
type TransferService struct {
	connections *Connections
	jobs        *jobs.Store
	runner      *jobRunner
	evidence    *evidence.Store
	// settings supplies the download selection ceiling (ADR 0072); nil
	// means the default ceiling.
	settings *SettingsService

	// engineFactory builds the byte-moving engine for a job; nil uses
	// the pinned rclone. Tests inject fakes here. The source yields
	// remotes PER SPAWN — implementations must call it for every child
	// so token refresh reaches long jobs (§26.2).
	engineFactory         func(source transfercopy.RemoteSource, opts rclone.Options) (transfercopy.Engine, error)
	downloadStageBoundary func(context.Context, string) error
	// downloadPlacementBoundary, when wired, observes each progress step
	// of a download so a fixture can act between two placements.
	downloadPlacementBoundary func(ctx context.Context, jobID string, done, total int64)

	// recommendations is the §42 per-bookmark lookup feeding Auto.
	recommendations func(bookmarkID string) *TransferSettingsDTO
	// picker is the injected native-dialog seam (nil when headless).
	picker FilePicker
	// overwrites asks the operator, on a surface the page cannot draw,
	// whether an upload may replace what is already at the names it
	// names (ADR 0081 decision 2); nil refuses.
	overwrites OverwriteConfirmer
	// selections holds picker-issued path selections behind tokens.
	selections selectionStore
	// destinations holds every active download's destination (ADR 0078).
	destinations destinationRegistry
	// downloadStartBoundary, when set by a test, runs between a start's
	// preflight and its reservation, the window a concurrent start can
	// fill; production leaves it nil.
	downloadStartBoundary func(ctx context.Context) error
}

// maxActiveJobsCeiling is the §38 v1 limit, not a default: one transfer
// job at a time, enforced (ADR 0047). Concurrent jobs multiply the states
// this application can occupy while it moves and deletes other people's
// data. Raising it later is this one line.
const maxActiveJobsCeiling = 1

// defaultMaxActiveJobs is what an unset or out-of-range setting falls back
// to. It cannot exceed the ceiling.
const defaultMaxActiveJobs = maxActiveJobsCeiling

// NewTransferService wires the service over the shared runner.
func NewTransferService(connections *Connections, jobStore *jobs.Store, auditStore *audit.Store) *TransferService {
	return NewTransferServiceWithRunner(connections, jobStore, newJobRunner(jobStore, auditStore))
}

// NewTransferServiceWithRunner shares a runner across services.
func NewTransferServiceWithRunner(connections *Connections, jobStore *jobs.Store, runner *jobRunner) *TransferService {
	return &TransferService{
		connections: connections,
		jobs:        jobStore,
		runner:      runner,
	}
}

// setEvidenceStore wires §28.6(b) ownership recording: committed
// segment prefixes a job wrote become ExclusiveOwnershipProof rows.
func (s *TransferService) setEvidenceStore(store *evidence.Store) {
	s.evidence = store
}

func (s *TransferService) recordOwnership(ctx context.Context, bookmarkID, jobID string, prefixes []rclone.SegmentPrefix) {
	if s.evidence == nil {
		return
	}
	for _, p := range prefixes {
		if err := s.evidence.RecordOwnership(ctx, bookmarkID, jobID, p.Container, p.Prefix); err != nil {
			log.Printf("job %s: ownership record %s/%s: %v", jobID, p.Container, p.Prefix, err)
		}
	}
}

// setEngineFactory overrides engine construction (tests; the future
// compatibility matrix).
func (s *TransferService) setEngineFactory(f func(source transfercopy.RemoteSource, opts rclone.Options) (transfercopy.Engine, error)) {
	s.engineFactory = f
}

// tokenRefreshMargin is how close to a known expiry a session may get
// before an engine spawn forces re-authentication.
const tokenRefreshMargin = 2 * time.Minute

// setMaxActiveJobsHook resizes the §38 budget (the settings surface will
// drive this; tests too). Applies to jobs started afterwards.
func (s *TransferService) setMaxActiveJobsHook(n int) {
	s.runner.setMaxActiveJobs(n)
}

func (s *TransferService) launch(ctx context.Context, jobID, bookmarkID string, releasePins func(), body func(ctx context.Context)) {
	s.runner.launch(ctx, jobID, bookmarkID, releasePins, body)
}

func validateCopyRequest(req CopyRequestDTO) error {
	if req.SourceBookmarkID == "" || req.DestBookmarkID == "" {
		return errors.New("both bookmarks are required")
	}
	if req.SourceContainer == "" || req.DestContainer == "" {
		return errors.New("both containers are required")
	}
	for _, name := range []string{req.SourceContainer, req.DestContainer} {
		if strings.Contains(name, "/") {
			return fmt.Errorf("container name %q must not contain '/'", name)
		}
	}
	for _, p := range []string{req.SourcePrefix, req.DestPrefix} {
		if p != "" && !strings.HasSuffix(p, "/") {
			return fmt.Errorf("prefix %q must be empty or end with '/'", p)
		}
	}
	if _, err := profileBase(Profile(req.Profile)); err != nil && req.Profile != string(ProfileCustom) {
		return err
	}
	return req.Settings.Validate()
}

// resolve opens both sessions and fail-fast checks the destination
// container (copying into a void 404s on the first write — say it now).
func (s *TransferService) resolve(ctx context.Context, req CopyRequestDTO) (src, dst *swiftcontrol.Session, versioned bool, err error) {
	if err := validateCopyRequest(req); err != nil {
		return nil, nil, false, err
	}
	src, err = s.connections.SessionFor(ctx, req.SourceBookmarkID)
	if err != nil {
		return nil, nil, false, fmt.Errorf("source: %w", err)
	}
	dst, err = s.connections.SessionFor(ctx, req.DestBookmarkID)
	if err != nil {
		return nil, nil, false, fmt.Errorf("destination: %w", err)
	}
	// Overlapping scopes in ONE namespace are refused outright (C-03):
	// nested prefixes make a mirror delete its own source or ingest its
	// own output. Identity is the ACCOUNT, compared FAIL-CLOSED: when
	// the two containers+nested-prefixes overlap and account distinctness
	// is not PROVEN (aliases, VIPs, and DNS names for one account defeat
	// a host-string compare — review R-04), refuse. Two accounts prove
	// distinct only by different authoritative account identifiers in
	// the resolved storage URL.
	if req.SourceContainer == req.DestContainer &&
		(strings.HasPrefix(req.SourcePrefix, req.DestPrefix) || strings.HasPrefix(req.DestPrefix, req.SourcePrefix)) &&
		!provenDistinctAccounts(src.StorageURL(), dst.StorageURL()) {
		return nil, nil, false, fmt.Errorf(
			"source and destination overlap: container %q with nested prefixes (%q / %q), and the two endpoints are not PROVABLY different accounts (%s vs %s) — a run could consume its own output; use disjoint prefixes, another container, or endpoints with distinct account identifiers",
			req.SourceContainer, req.SourcePrefix, req.DestPrefix, accountIdentity(src.StorageURL()), accountIdentity(dst.StorageURL()))
	}
	detail, err := dst.ContainerDetail(ctx, req.DestContainer)
	if err != nil {
		return nil, nil, false, fmt.Errorf("destination container %q: %w", req.DestContainer, err)
	}
	// K-XFER-004: the chunk knob is capped against the DESTINATION
	// cluster's advertised limit at preview and start alike.
	effective, err := s.effectiveSettings(req)
	if err != nil {
		return nil, nil, false, err
	}
	if _, err := effective.forDestination(ctx, dst); err != nil {
		return nil, nil, false, err
	}
	versioned = detail.Versioning.Mode != swiftcontrol.VersioningOff
	return src, dst, versioned, nil
}

// PlanCopy runs the §15.2 merge-join for the preview — counts only, no
// writes, no audit (planning is a read).
func (s *TransferService) PlanCopy(ctx context.Context, req CopyRequestDTO) (*CopyPlanDTO, error) {
	if err := refuseIfDeferred("TransferService.PlanCopy"); err != nil {
		return nil, err
	}
	src, dst, versioned, err := s.resolve(ctx, req)
	if err != nil {
		return nil, err
	}
	largeConservative := false
	var overwriteBytes int64
	summary, err := plan.Copy(ctx,
		src.Objects(ctx, req.SourceContainer, swiftcontrol.ObjectListOptions{Deep: true, Prefix: req.SourcePrefix}),
		dst.Objects(ctx, req.DestContainer, swiftcontrol.ObjectListOptions{Deep: true, Prefix: req.DestPrefix}),
		req.SourcePrefix, req.DestPrefix, func(item plan.Item) error {
			if item.Reason == plan.ReasonLargeObject || item.Reason == plan.ReasonPossibleManifest {
				largeConservative = true
			}
			if item.Verdict == plan.VerdictChanged {
				overwriteBytes += item.SourceBytes
			}
			return nil
		})
	if err != nil {
		return nil, err
	}
	return &CopyPlanDTO{
		Summary:              summary,
		VersionedDestination: versioned,
		LargeObjectsAsDLO:    largeConservative,
		DataPath:             "eligible copies pass through this workstation: source download and destination upload",
		OverwriteBytes:       overwriteBytes,
	}, nil
}

// effectiveSettings resolves profile + overrides (§40/§38);
// recommendations arrive with the benchmark page.
func (s *TransferService) effectiveSettings(req CopyRequestDTO) (TransferSettingsDTO, error) {
	return ResolveSettings(Profile(req.Profile), req.Settings, s.recommendationFor(req.SourceBookmarkID))
}

// recommendationFor returns the §42 persisted recommendation, nil when
// none is recorded.
func (s *TransferService) recommendationFor(bookmarkID string) *TransferSettingsDTO {
	if s.recommendations == nil {
		return nil
	}
	return s.recommendations(bookmarkID)
}

// setRecommendationSource wires the benchmark store's per-bookmark
// recommendation lookup into Auto (§40/§42).
func (s *TransferService) setRecommendationSource(f func(bookmarkID string) *TransferSettingsDTO) {
	s.recommendations = f
}

// buildEngine constructs the pinned rclone engine for both endpoints
// with the job's effective options. Remotes resolve PER SPAWN: each
// child gets tokens refreshed against expiry first (§26.2 — the child
// cannot re-authenticate itself).
func (s *TransferService) buildEngine(ctx context.Context, req CopyRequestDTO, src, dst *swiftcontrol.Session) (transfercopy.Engine, TransferSettingsDTO, error) {
	source := func(spawnCtx context.Context) ([]rclone.Remote, error) {
		srcRemote, err := s.connections.EngineRemote(spawnCtx, src, "aeronautes_src")
		if err != nil {
			return nil, fmt.Errorf("source credentials: %w", err)
		}
		dstRemote, err := s.connections.EngineRemote(spawnCtx, dst, "aeronautes_dst")
		if err != nil {
			return nil, fmt.Errorf("destination credentials: %w", err)
		}
		return []rclone.Remote{srcRemote, dstRemote}, nil
	}
	remotes, err := source(ctx)
	if err != nil {
		return nil, TransferSettingsDTO{}, err
	}
	// §12.3: one child may serve both endpoints only when their TLS
	// trust identities are EQUAL — refuse here so the operator sees the
	// reason at start, not a child-spawn failure mid-job.
	srcTrust, err := rclone.TrustIdentity(remotes[0].InsecureTLS, remotes[0].CACertFile)
	if err != nil {
		return nil, TransferSettingsDTO{}, err
	}
	dstTrust, err := rclone.TrustIdentity(remotes[1].InsecureTLS, remotes[1].CACertFile)
	if err != nil {
		return nil, TransferSettingsDTO{}, err
	}
	if srcTrust != dstTrust {
		return nil, TransferSettingsDTO{}, errors.New("the two bookmarks carry different TLS trust (custom CA or insecure): one transfer child cannot serve both safely (§12.3) — align their trust settings or move data via a location both trust")
	}
	effective, err := s.effectiveSettings(req)
	if err != nil {
		return nil, TransferSettingsDTO{}, err
	}
	effective, err = effective.forDestination(ctx, dst)
	if err != nil {
		return nil, TransferSettingsDTO{}, err
	}
	options := effective.options()
	// Source sizes and digests describe stored bytes, so Copy must not
	// decode content while staging it through the child.
	options.NoGzipEncoding = true
	if s.engineFactory != nil {
		engine, err := s.engineFactory(source, options)
		return engine, effective, err
	}
	runner, err := rclone.NewRunner(options)
	if err != nil {
		return nil, TransferSettingsDTO{}, err
	}
	return transfercopy.BindRunnerSource(runner, source), effective, nil
}

// StartCopy creates the job and launches execution in the background;
// the returned job id is the handle for JobService and CancelJob.
func (s *TransferService) StartCopy(ctx context.Context, req CopyRequestDTO) (string, error) {
	if err := refuseIfDeferred("TransferService.StartCopy"); err != nil {
		return "", err
	}
	if err := validateCopyRequest(req); err != nil {
		return "", err
	}
	owner, ctx, err := s.runner.admit(ctx)
	if err != nil {
		return "", err
	}
	defer owner.finishStart()
	releasePins, err := s.connections.pinForJob(ctx, req.SourceBookmarkID, req.DestBookmarkID)
	if err != nil {
		return "", err
	}
	defer func() {
		if releasePins != nil {
			releasePins()
		}
	}()
	src, dst, _, err := s.resolve(ctx, req)
	if err != nil {
		return "", err
	}
	// An acknowledged copy raises the one native confirmation before anything
	// is created, naming both accounts and capturing the baseline the
	// fixed-point comparison rests on. An unacknowledged copy skips it and
	// refuses every overwrite in the executor, exactly as it does today.
	var acknowledged map[string]jobs.ItemBaseline
	if req.AcknowledgeOverwrite {
		acknowledged, err = s.confirmCopyOverwrite(ctx, req, src, dst)
		if err != nil {
			return "", err
		}
	}
	engine, effective, err := s.buildEngine(ctx, req, src, dst)
	if err != nil {
		return "", fmt.Errorf("transfer engine: %w", err)
	}

	job, err := s.jobs.Create(ctx, jobs.Job{
		Type:               jobs.TypeCopy,
		ItemRecordsVersion: 1,
		Source:             req.SourceContainer + "/" + req.SourcePrefix,
		Destination:        req.DestContainer + "/" + req.DestPrefix,
		SourceBookmarkID:   req.SourceBookmarkID,
		DestBookmarkID:     req.DestBookmarkID,
		Engine:             "rclone+native",
		Settings:           effectiveJSONWithProfile(Profile(req.Profile), effective),
	})
	if err != nil {
		return "", err
	}

	s.runner.register(job.ID, owner.cancel)

	if err := s.runner.audit.Append(ctx, audit.Event{
		BookmarkID: req.SourceBookmarkID, Operation: "transfer.copy.start",
		Resource: job.Source + " → " + job.Destination, Summary: "job " + job.ID + " · " + job.Settings,
		Result: audit.ResultApplied,
	}); err != nil {
		owner.cancel()
		s.finishCopy(context.WithoutCancel(ctx), job.ID, req, job.Counters, jobs.StateFailed, "start audit unavailable before first write: "+err.Error())
		return "", fmt.Errorf("start audit unavailable: %w", err)
	}
	owner.launch(job.ID, req.SourceBookmarkID, releasePins, func(runCtx context.Context) {
		s.runCopy(runCtx, job.ID, req, src, dst, engine, acknowledged)
	}, func(background context.Context) {
		s.finishCopy(background, job.ID, req, job.Counters, jobs.StateCancelled, "")
	})
	releasePins = nil
	return job.ID, nil
}

// confirmCopyOverwrite raises the one native confirmation that authorises
// replacing live destinations, naming BOTH accounts — the one the bytes come
// from and the one they land in (ADR 0073, ADR 0081 decision 2). It HEADs each
// candidate to capture the baseline ADR 0085 compares at the fixed point,
// refuses unless the answer is yes about exactly this plan, and re-checks both
// accounts' generations either side of the dialog so an answer given about one
// pair of accounts cannot authorise a write under another.
//
// It returns the baselines it captured, keyed by name, for the recorder to
// store once each item row exists. An empty map with no error means there was
// nothing live to replace, so the copy proceeds with every name conditional.
func (s *TransferService) confirmCopyOverwrite(ctx context.Context, req CopyRequestDTO,
	src, dst *swiftcontrol.Session) (map[string]jobs.ItemBaseline, error) {
	// Changed and Unknown are the join's two "a destination exists" verdicts;
	// New has no destination and Same needs no replacement. The plan is advisory
	// — the executor re-reads at the fixed point — so this pass only names the
	// candidates to ask about.
	var candidates []string
	if _, err := plan.Copy(ctx,
		src.Objects(ctx, req.SourceContainer, swiftcontrol.ObjectListOptions{Deep: true, Prefix: req.SourcePrefix}),
		dst.Objects(ctx, req.DestContainer, swiftcontrol.ObjectListOptions{Deep: true, Prefix: req.DestPrefix}),
		req.SourcePrefix, req.DestPrefix, func(item plan.Item) error {
			if item.Verdict == plan.VerdictChanged || item.Verdict == plan.VerdictUnknown {
				candidates = append(candidates, item.Name)
			}
			return nil
		}); err != nil {
		return nil, err
	}
	// HEAD each candidate to capture what the operator is about to be shown. A
	// name the plan called a candidate but that no longer identifies anything is
	// left out of both the baseline set and the question: it was not shown, so
	// it carries no acknowledgement, and the executor refuses it at the fixed
	// point as an unacknowledged overwrite rather than replacing it blind.
	baselines := map[string]jobs.ItemBaseline{}
	var names []string
	for _, name := range candidates {
		detail, err := dst.ObjectDetail(ctx, req.DestContainer, req.DestPrefix+name)
		if err != nil {
			if swiftcontrol.IsNotFound(err) {
				continue
			}
			return nil, fmt.Errorf("reading %s before asking to replace it: %w", name, err)
		}
		baseline, ok := copyShownFrom(detail)
		if !ok {
			continue
		}
		baselines[name] = baseline
		names = append(names, name)
	}
	if len(names) == 0 {
		return map[string]jobs.ItemBaseline{}, nil
	}
	if s.overwrites == nil {
		return nil, errors.New("replacing an existing object needs the shell's confirmation, and this build has none")
	}
	versioning, err := s.observedVersioning(ctx, req.DestBookmarkID, req.DestContainer)
	if err != nil {
		return nil, err
	}
	// Both generations are observed either side of the dialog: an answer given
	// about one account's credentials must not authorise a write once either
	// account's credentials have rotated under it.
	srcBefore, err := s.connections.sessionEntryFor(ctx, req.SourceBookmarkID)
	if err != nil {
		return nil, err
	}
	dstBefore, err := s.connections.sessionEntryFor(ctx, req.DestBookmarkID)
	if err != nil {
		return nil, err
	}
	question, err := copyOverwriteQuestion(
		s.connections.displayName(ctx, req.SourceBookmarkID), accountSuffix(src.StorageURL()),
		s.connections.displayName(ctx, req.DestBookmarkID), accountSuffix(dst.StorageURL()),
		req.DestContainer, req.DestPrefix, versioning, names)
	if err != nil {
		return nil, err
	}
	yes, err := s.overwrites.ConfirmOverwrite(ctx, question)
	if err != nil {
		return nil, err
	}
	if !yes {
		return nil, errors.New("the replacement was not confirmed; nothing was written")
	}
	srcAfter, err := s.connections.sessionEntryFor(ctx, req.SourceBookmarkID)
	if err != nil {
		return nil, err
	}
	dstAfter, err := s.connections.sessionEntryFor(ctx, req.DestBookmarkID)
	if err != nil {
		return nil, err
	}
	if srcAfter.generation != srcBefore.generation || dstAfter.generation != dstBefore.generation {
		return nil, errors.New("an account's credentials changed while you were answering; start again")
	}
	return baselines, nil
}

// copyShownFrom reads a destination HEAD into the baseline the fixed-point
// comparison needs, or reports that it identifies nothing. It mirrors the
// executor's own reading (internal/transfer/copy), so the baseline captured
// here and the fresh observation compared there describe an object the same
// way: a HEAD with no entity tag or no stated length is not an identity.
func copyShownFrom(detail *swiftcontrol.ObjectDetail) (jobs.ItemBaseline, bool) {
	tag := strings.Trim(detail.ETag, `"`)
	if tag == "" {
		return jobs.ItemBaseline{}, false
	}
	if _, stated := detail.RawHeaders["Content-Length"]; !stated {
		return jobs.ItemBaseline{}, false
	}
	return jobs.ItemBaseline{Class: string(detail.Class), Length: detail.ContentLength, ETag: tag}, true
}

// copyOverwriteQuestion composes the two-account confirmation. It refuses to
// compose a statement missing either account, the container, its versioning or
// the names, so no caller can raise a question that authorises nothing. Copy
// keeps the replaced objects' segments (ADR 0087 decision 6), so the question
// says so rather than offering a removal it will not perform.
func copyOverwriteQuestion(srcLabel, srcAccount, dstLabel, dstAccount,
	container, prefix, versioning string, names []string) (OverwriteConfirmation, error) {
	switch {
	case srcAccount == "":
		return OverwriteConfirmation{}, errors.New("the source account could not be identified, so an overwrite is refused")
	case dstAccount == "":
		return OverwriteConfirmation{}, errors.New("the destination account could not be identified, so an overwrite is refused")
	case container == "":
		return OverwriteConfirmation{}, errors.New("the destination container could not be identified, so an overwrite is refused")
	case versioning == "":
		return OverwriteConfirmation{}, errors.New("the destination container's versioning could not be stated, so an overwrite is refused")
	case len(names) == 0:
		return OverwriteConfirmation{}, errors.New("there is nothing live to ask about, so no overwrite is authorised")
	}
	return OverwriteConfirmation{
		Bookmark: titleText(dstLabel, 80), Account: titleText(dstAccount, 80),
		SourceBookmark: titleText(srcLabel, 80), SourceAccount: titleText(srcAccount, 80),
		Container: container, Prefix: prefix, Names: names, Versioning: versioning,
		Fragments: "the replaced objects' segments are kept, not removed: they are on the destination account, which was not enumerated",
	}, nil
}

func (s *TransferService) finishCopy(ctx context.Context, jobID string, req CopyRequestDTO, counters jobs.Counters, state jobs.State, message string) {
	totals, totalsErr := s.jobs.ReadItemTotals(ctx, jobID)
	if totalsErr != nil {
		if message != "" {
			message += "; "
		}
		message += totalsErr.Error()
		if state == jobs.StateComplete {
			state = jobs.StatePartial
		}
	} else if state == jobs.StateComplete && (totals.Failed != 0 || totals.Conflicts != 0 || totals.Refused != 0 || totals.Unresolved != 0 || totals.Unverified != 0 || totals.Problems != 0) {
		state = jobs.StatePartial
		message = "retained item outcomes contain failures or uncertainty"
	}
	auditSummary := "job " + jobID
	if counters.OverwritesRefused > 0 {
		state = jobs.StatePartial
		count := fmt.Sprintf("%d overwrites refused", counters.OverwritesRefused)
		if counters.OverwritesRefused == 1 {
			count = "1 overwrite refused"
		}
		refusal := count + ": overwriting existing destinations is unavailable while concurrent changes cannot be protected"
		if message == "" {
			message = refusal
		} else {
			message = refusal + "; " + message
		}
		auditSummary += " · " + count
	}
	// A completed body cannot accept cancellation even when its final
	// transaction fails and the shared store reports unavailability.
	s.runner.release(jobID)
	err := s.jobs.FinishFromItems(ctx, jobID, jobs.Completion{
		State: state, Counters: counters, Error: message,
		Audit: audit.Event{
			BookmarkID: req.SourceBookmarkID, Operation: "transfer.copy." + string(state),
			Resource: req.SourceContainer + "/" + req.SourcePrefix + " → " + req.DestContainer + "/" + req.DestPrefix,
			Summary:  auditSummary, Result: terminalAuditResult(state),
		},
	}, s.runner.audit)
	if err != nil {
		log.Printf("job %s terminal outcome was not saved: %v", jobID, err)
	}
}

// runCopy is the job goroutine: Planning → Running (single-pass plan
// streamed straight into the executor — the plan is advisory either way,
// §15.3, so the planned counters grow live rather than costing a second
// full listing pass) → Verifying → terminal, with reconciliation on
// every exit path.
func (s *TransferService) runCopy(ctx context.Context, jobID string, req CopyRequestDTO,
	src, dst *swiftcontrol.Session, engine transfercopy.Engine, acknowledged map[string]jobs.ItemBaseline) {

	background := context.WithoutCancel(ctx) // store/reconcile writes survive cancel
	var overwritesRefused int64
	counters := jobs.Counters{}
	var countersMu sync.Mutex
	var executor *transfercopy.Executor
	finish := func(state jobs.State, message string) {
		if executor != nil {
			if err := executor.Close(); err != nil {
				if message != "" {
					message += "; "
				}
				message += "refusal record closure: " + err.Error()
				if state == jobs.StateComplete {
					state = jobs.StatePartial
				}
			}
		}
		countersMu.Lock()
		snapshot := counters
		countersMu.Unlock()
		s.finishCopy(background, jobID, req, snapshot, state, message)
	}
	admit := func(state jobs.State) bool {
		if s.jobs.Available() != nil {
			s.runner.release(jobID)
			return false
		}
		if err := s.jobs.Transition(background, jobID, state); err != nil {
			finish(jobs.StateFailed, "job admission unavailable before first write: "+err.Error())
			return false
		}
		return true
	}

	if !admit(jobs.StatePlanning) {
		return
	}

	lastFlush := time.Time{}
	flush := func() {
		countersMu.Lock()
		snapshot := counters
		now := time.Now()
		flushable := now.Sub(lastFlush) >= 500*time.Millisecond
		if flushable {
			lastFlush = now
		}
		countersMu.Unlock()
		if flushable {
			_ = s.jobs.UpdateCounters(background, jobID, snapshot)
		}
	}

	var err error
	executor, err = transfercopy.New(transfercopy.Config{
		Source: src, Dest: dst, Engine: engine,
		SrcSpec: "aeronautes_src:" + req.SourceContainer, DstSpec: "aeronautes_dst:" + req.DestContainer,
		SrcContainer: req.SourceContainer, DstContainer: req.DestContainer,
		SrcPrefix: req.SourcePrefix, DstPrefix: req.DestPrefix,
		JobID:    jobID,
		Recorder: s.copyItemRecorder(background, jobID, req, acknowledged),
		OnItem: func(outcome transfercopy.ItemOutcome) {
			countersMu.Lock()
			switch outcome.Outcome {
			case "copied":
				counters.ObjectsCopied++
			case "failed":
				counters.ObjectsFailed++
			case "overwrite-refused":
				counters.OverwritesRefused++
			}
			countersMu.Unlock()
			flush()
		},
		OnAuthFailure: func() {
			src.ForceReauth()
			dst.ForceReauth()
		},
		OnRecovery: s.recoveryRecorder(background, jobID, req.DestBookmarkID),
	})
	if err != nil {
		finish(jobs.StateFailed, err.Error())
		return
	}
	defer func() {
		if err := executor.Close(); err != nil {
			log.Printf("job %s close refusal record: %v", jobID, err)
		}
	}()
	// Persist creation intent before writes so an interrupted transfer
	// retains visible recovery facts. This does not grant cleanup authority.
	if err := s.jobs.RecoveryAdd(background, jobs.RecoveryRecord{
		JobID: jobID, BookmarkID: req.DestBookmarkID, Kind: jobs.RecoveryStaging,
		Container: req.DestContainer, Prefix: executor.StagingPrefix(),
	}); err != nil {
		finish(jobs.StateFailed, "recovery store unavailable before first write: "+err.Error())
		return
	}
	if !admit(jobs.StateRunning) {
		return
	}

	planned := func(item plan.Item) error {
		countersMu.Lock()
		switch item.Verdict {
		case plan.VerdictNew, plan.VerdictChanged, plan.VerdictUnknown:
			counters.ObjectsPlanned++
			counters.BytesPlanned += item.SourceBytes
		}
		countersMu.Unlock()
		return executor.Handle(ctx, item)
	}

	_, planErr := plan.Copy(ctx,
		src.Objects(ctx, req.SourceContainer, swiftcontrol.ObjectListOptions{Deep: true, Prefix: req.SourcePrefix}),
		dst.Objects(ctx, req.DestContainer, swiftcontrol.ObjectListOptions{Deep: true, Prefix: req.DestPrefix}),
		req.SourcePrefix, req.DestPrefix, planned)
	executor.Stop(planErr)
	report, finishErr := executor.Finish(ctx)
	planErr = finishErr
	overwritesRefused = report.OverwritesRefused

	countersMu.Lock()
	counters.ObjectsCopied = report.Copied
	counters.BytesCopied = report.BytesCopied
	counters.Overwrites = report.Overwrites
	counters.OverwritesRefused = report.OverwritesRefused
	counters.NativeRouted = report.NativeRouted
	countersMu.Unlock()

	// These records preserve creation provenance, not deletion authority.
	s.recordOwnership(background, req.DestBookmarkID, jobID, report.CommittedPrefixes)

	// A completed transfer cannot prove uncertain residue absent.
	// Keep its recovery facts even when reconciliation reports no error.
	if reconcile, err := executor.Reconcile(background, report); err != nil {
		planErr = errors.Join(planErr, fmt.Errorf("reconciliation: %w", err))
	} else {
		if len(reconcile.Notes) != 0 {
			log.Printf("job %s reconcile kept records: %s", jobID, strings.Join(reconcile.Notes, "; "))
		}
	}

	if planErr != nil {
		if len(report.Failures) > 0 {
			finish(jobs.StatePartial, fmt.Sprintf("execution failed (example: %s: %s); %s", report.Failures[0].Name, report.Failures[0].Err, planErr.Error()))
		} else if report.Copied > 0 {
			finish(jobs.StatePartial, planErr.Error())
		} else if errors.Is(planErr, context.Canceled) {
			message := ""
			if overwritesRefused > 0 {
				message = planErr.Error()
			}
			finish(jobs.StateCancelled, message)
		} else {
			finish(jobs.StateFailed, planErr.Error())
		}
		return
	}

	if err := s.jobs.Transition(background, jobID, jobs.StateVerifying); err != nil {
		message := "verification state could not be saved: " + err.Error()
		if len(report.Failures) > 0 {
			message += fmt.Sprintf("; execution failed (example: %s: %s)", report.Failures[0].Name, report.Failures[0].Err)
		}
		finish(jobs.StatePartial, message)
		return
	}
	verify, err := executor.Verify(background,
		src.Objects(background, req.SourceContainer, swiftcontrol.ObjectListOptions{Deep: true, Prefix: req.SourcePrefix}),
		dst.Objects(background, req.DestContainer, swiftcontrol.ObjectListOptions{Deep: true, Prefix: req.DestPrefix}))
	countersMu.Lock()
	counters.ObjectsVerified = verify.Verified
	countersMu.Unlock()
	if err != nil {
		state := jobs.StateFailed
		if report.Copied > 0 || len(report.Failures) > 0 || len(verify.Failures) > 0 {
			state = jobs.StatePartial
		}
		finish(state, "verification: "+err.Error())
		return
	}

	switch {
	case len(report.Failures) > 0 || len(verify.Failures) > 0 || len(report.Conflicts) > 0:
		parts := []string{}
		if n := len(report.Failures); n > 0 {
			parts = append(parts, fmt.Sprintf("execution failed (example: %s: %s)", report.Failures[0].Name, report.Failures[0].Err))
		}
		if n := len(verify.Failures); n > 0 {
			parts = append(parts, fmt.Sprintf("verification failed (example: %s)", verify.Failures[0].Name))
		}
		if n := len(report.Conflicts); n > 0 {
			parts = append(parts, fmt.Sprintf("%d conflicts (destination changed externally)", n))
		}
		finish(jobs.StatePartial, strings.Join(parts, "; "))
	default:
		finish(jobs.StateComplete, "")
	}
}

// recoveryRecorder persists executor recovery facts durably. Errors
// return to the executor, which fails closed: no commit crosses a
// boundary whose facts could not be persisted (§65.9 item 5).
func (s *TransferService) recoveryRecorder(ctx context.Context, jobID, bookmarkID string) func(kind, container, prefix, object string) error {
	return func(kind, container, prefix, object string) error {
		err := s.jobs.RecoveryAdd(ctx, jobs.RecoveryRecord{
			JobID: jobID, BookmarkID: bookmarkID, Kind: jobs.RecoveryKind(kind),
			Container: container, Prefix: prefix, Object: object,
		})
		if err != nil {
			log.Printf("job %s: recovery record (%s %s/%s): %v", jobID, kind, container, prefix, err)
		}
		return err
	}
}

func terminalAuditResult(state jobs.State) audit.Result {
	switch state {
	case jobs.StateComplete:
		return audit.ResultApplied
	case jobs.StatePartial:
		return audit.ResultVerifyMismatch
	default:
		return audit.ResultFailed
	}
}

// CancelJob requests graceful cancellation. Child cleanup remains
// best-effort, so subsequent reconciliation retains uncertain residue.
func (s *TransferService) CancelJob(ctx context.Context, jobID string) error {
	cancel, ok := s.runner.cancel(jobID)
	if !ok {
		return fmt.Errorf("job %s is not running in this process", jobID)
	}
	cancel()
	s.record(ctx, "", "transfer.cancel-requested", "job "+jobID, "operator cancel", audit.ResultApplied)
	return nil
}

func (s *TransferService) record(ctx context.Context, bookmarkID, operation, resource, summary string, result audit.Result) {
	if bookmarkID == "" {
		bookmarkID = s.connections.ActiveBookmarkID()
	}
	s.runner.record(ctx, bookmarkID, operation, resource, summary, result)
}

// JobDTO is one §46 job for the drawer.
type JobDTO struct {
	ID               string        `json:"id"`
	Type             string        `json:"type"`
	State            string        `json:"state"`
	Source           string        `json:"source"`
	Destination      string        `json:"destination"`
	SourceBookmarkID string        `json:"sourceBookmarkId"`
	DestBookmarkID   string        `json:"destBookmarkId"`
	CreatedAt        string        `json:"createdAt"`
	StartedAt        string        `json:"startedAt"`
	FinishedAt       string        `json:"finishedAt"`
	Engine           string        `json:"engine"`
	Counters         jobs.Counters `json:"counters"`
	Error            string        `json:"error"`
	// Settings is the §45 effective-configuration JSON ("" when none).
	Settings        string `json:"settings"`
	RecoveryRecords int64  `json:"recoveryRecords"`
}

// JobService is the read-only §47 drawer surface.
type JobService struct {
	jobs *jobs.Store
}

// NewJobService builds the service.
func NewJobService(jobStore *jobs.Store) *JobService {
	return &JobService{jobs: jobStore}
}

type RecoveryStatusDTO struct {
	Jobs    int64 `json:"jobs"`
	Records int64 `json:"records"`
}

// RecoveryStatus counts retained records across all terminal jobs, even
// those outside the recent-job list. No remote residue is verified.
func (s *JobService) RecoveryStatus(ctx context.Context) (RecoveryStatusDTO, error) {
	status, err := s.jobs.RecoveryStatus(ctx)
	if err != nil {
		return RecoveryStatusDTO{}, errors.New("recovery status is unavailable")
	}
	return RecoveryStatusDTO{Jobs: status.Jobs, Records: status.Records}, nil
}

// List returns jobs newest first; bookmarkID "" means all, matching
// either end otherwise (§47 owner input: filter by account).
func (s *JobService) List(ctx context.Context, bookmarkID string, activeOnly bool, limit int) ([]JobDTO, error) {
	list, err := s.jobs.List(ctx, jobs.Filter{BookmarkID: bookmarkID, ActiveOnly: activeOnly, Limit: limit})
	if err != nil {
		return nil, err
	}
	out := make([]JobDTO, 0, len(list))
	for _, j := range list {
		out = append(out, jobDTO(j))
	}
	return out, nil
}

// Get returns one job bound to the named bookmark (ADR 0059).
func (s *JobService) Get(ctx context.Context, bookmarkID, jobID string) (*JobDTO, error) {
	j, err := s.jobInAccount(ctx, bookmarkID, jobID)
	if err != nil {
		return nil, err
	}
	dto := jobDTO(j)
	return &dto, nil
}

func (s *JobService) jobInAccount(ctx context.Context, bookmarkID, jobID string) (jobs.Job, error) {
	if bookmarkID == "" {
		return jobs.Job{}, fmt.Errorf("%w: name the bookmark the read is for", ErrAccountScopeChanged)
	}
	j, err := s.jobs.Get(ctx, jobID)
	if err != nil {
		if errors.Is(err, jobs.ErrItemJobNotFound) {
			return jobs.Job{}, jobs.ErrItemJobNotFound
		}
		return jobs.Job{}, err
	}
	if j.SourceBookmarkID != bookmarkID && j.DestBookmarkID != bookmarkID {
		return jobs.Job{}, fmt.Errorf("%w: job is not for that bookmark", ErrAccountScopeChanged)
	}
	return j, nil
}

func jobDTO(j jobs.Job) JobDTO {
	formatTime := func(t time.Time) string {
		if t.IsZero() {
			return ""
		}
		return t.UTC().Format(time.RFC3339)
	}
	return JobDTO{
		ID: j.ID, Type: string(j.Type), State: string(j.State),
		Source: j.Source, Destination: j.Destination,
		SourceBookmarkID: j.SourceBookmarkID, DestBookmarkID: j.DestBookmarkID,
		CreatedAt: formatTime(j.CreatedAt), StartedAt: formatTime(j.StartedAt),
		FinishedAt: formatTime(j.FinishedAt),
		Engine:     j.Engine, Counters: j.Counters, Error: j.Error,
		Settings: j.Settings, RecoveryRecords: j.RecoveryRecords,
	}
}

// RerunJob requires inspectable prior facts before normal fresh planning;
// a terminal state alone cannot establish that earlier effects are known.
func (s *TransferService) RerunJob(ctx context.Context, jobID string) (string, error) {
	if err := refuseIfDeferred("TransferService.RerunJob"); err != nil {
		return "", err
	}
	job, err := s.jobs.Get(ctx, jobID)
	if err != nil {
		return "", errRerunRecordsUnavailable
	}
	if !jobs.Terminal(job.State) {
		return "", fmt.Errorf("job %s is still %s — cancel it first if you want a fresh run", jobID, job.State)
	}
	if job.Type == jobs.TypeMirror {
		return "", errors.New("Mirror is unavailable while concurrent changes cannot be protected")
	}
	if job.Type != jobs.TypeCopy {
		return "", fmt.Errorf("re-run supports copy jobs; a %s needs its own dialog (destructive halves keep their typed confirmation)", job.Type)
	}
	if err := s.checkRerunItems(ctx, jobID); err != nil {
		return "", err
	}
	srcContainer, srcPrefix, ok := splitLocator(job.Source)
	if !ok {
		return "", fmt.Errorf("job %s carries no re-runnable source locator", jobID)
	}
	dstContainer, dstPrefix, ok := splitLocator(job.Destination)
	if !ok {
		return "", fmt.Errorf("job %s carries no re-runnable destination locator", jobID)
	}
	request := CopyRequestDTO{
		SourceBookmarkID: job.SourceBookmarkID, SourceContainer: srcContainer, SourcePrefix: srcPrefix,
		DestBookmarkID: job.DestBookmarkID, DestContainer: dstContainer, DestPrefix: dstPrefix,
	}
	// The recorded effective configuration re-applies as Custom: the
	// re-run uses exactly what the original ran with (§45).
	if job.Settings != "" {
		var recorded struct {
			TransferSettingsDTO
			Profile string `json:"profile"`
		}
		if err := json.Unmarshal([]byte(job.Settings), &recorded); err == nil {
			request.Profile = string(ProfileCustom)
			request.Settings = recorded.TransferSettingsDTO
		}
	}
	return s.StartCopy(ctx, request)
}

// splitLocator parses the job row's "container/prefix" display locator.
func splitLocator(locator string) (container, prefix string, ok bool) {
	if locator == "" {
		return "", "", false
	}
	if i := strings.Index(locator, "/"); i > 0 {
		return locator[:i], locator[i+1:], true
	}
	return locator, "", true
}

// accountIdentity extracts the authoritative account segment from a v1
// storage URL path (the ".../v1/AUTH_xxx" tail), lowercased — the part
// that stays equal across host aliases/VIPs for one Swift account and
// differs between accounts. "" when it cannot be found.
func accountIdentity(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	segments := strings.Split(strings.Trim(u.Path, "/"), "/")
	for i, seg := range segments {
		if seg == "v1" && i+1 < len(segments) {
			return strings.ToLower(segments[i+1])
		}
	}
	if n := len(segments); n > 0 {
		return strings.ToLower(segments[n-1])
	}
	return ""
}

// provenDistinctAccounts reports whether two storage URLs are
// DEMONSTRABLY different accounts. It returns true ONLY when both
// account identifiers parse and differ — anything ambiguous (a missing
// identifier, an unparseable URL) is NOT proven distinct, so the
// overlap guard fails closed (review R-04).
func provenDistinctAccounts(a, b string) bool {
	idA, idB := accountIdentity(a), accountIdentity(b)
	return idA != "" && idB != "" && idA != idB
}

// canonicalEndpoint normalizes a storage URL for identity comparison:
// scheme+host (lowercased, default ports dropped) + path without a
// trailing slash — two bookmarks naming one account through cosmetic
// URL differences still count as the same target.
func canonicalEndpoint(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return strings.TrimSuffix(raw, "/")
	}
	host := strings.ToLower(u.Host)
	host = strings.TrimSuffix(host, ":443")
	host = strings.TrimSuffix(host, ":80")
	return strings.ToLower(u.Scheme) + "://" + host + strings.TrimSuffix(u.Path, "/")
}
