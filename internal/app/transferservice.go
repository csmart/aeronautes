package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"iter"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/csmart/aeronautes/internal/applog"
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
	// Members, when given, are the full names of the selected objects the
	// copy acts on instead of its whole scope (ADR 0108); empty means the
	// scope.
	Members []string `json:"members,omitempty"`
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
	// MissingMembers counts selected objects the source listing no longer
	// holds; they are not copied (ADR 0108 decision 3).
	MissingMembers int `json:"missingMembers,omitempty"`
	// DestinationMissing is set, and nothing else is, when the destination
	// container does not exist: the dialog offers to create it, bound to
	// this scope (ADR 0115 decision 2).
	DestinationMissing *DestinationMissingDTO `json:"destinationMissing,omitempty"`
}

// DestinationMissingDTO names a copy destination container the preview
// found missing: the bookmark and generation of the session that answered
// not found, and the name it read. A create is bound to exactly this.
type DestinationMissingDTO struct {
	BookmarkID string `json:"bookmarkId"`
	Generation int64  `json:"generation"`
	Container  string `json:"container"`
}

// destinationMissingError is resolve's not-found answer for the destination
// container. A preview turns it into DestinationMissing; a start reports it
// as the error it always was.
type destinationMissingError struct {
	missing DestinationMissingDTO
	cause   error
}

func (e *destinationMissingError) Error() string {
	return fmt.Sprintf("destination container %q: %v", e.missing.Container, e.cause)
}

func (e *destinationMissingError) Unwrap() error { return e.cause }

// DestinationContainersDTO is the first page of a copy destination's
// container names, which the dialog suggests as the operator types.
type DestinationContainersDTO struct {
	Names []string `json:"names"`
	More  bool     `json:"more"`
}

// destinationNameLimit bounds the suggestions (ADR 0115 decision 1).
const destinationNameLimit = 1000

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
	engineFactory func(source transfercopy.RemoteSource, opts rclone.Options) (transfercopy.Engine, error)
	// downloadBoundary, when wired, is asked before each engine child of
	// a download starts (WireDownloadEngineBoundary).
	downloadBoundary func(context.Context, string) error

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

// maxActiveJobsCeiling is the §38 limit, not a default: at most four jobs
// at once, enforced by the runner whatever a caller or a stored setting
// asks for (ADR 0105). What keeps several running jobs apart is in
// jobclaims.go.
const maxActiveJobsCeiling = 4

// defaultMaxActiveJobs is what an unset or out-of-range setting falls back
// to. It cannot exceed the ceiling.
const defaultMaxActiveJobs = 2

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
			applog.Warnf("job %s: ownership record %s/%s: %v", jobID, p.Container, p.Prefix, err)
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
	if err := validateMembers(req.SourcePrefix, req.Members); err != nil {
		return err
	}
	if _, err := profileBase(Profile(req.Profile)); err != nil && req.Profile != string(ProfileCustom) {
		return err
	}
	return req.Settings.Validate()
}

// validateMembers refuses a selection that cannot be what the operator chose
// (ADR 0108 decisions 2 and 7): each member is a full name inside the scope's
// prefix, an object or a folder ending in "/", not the folder itself, named
// once, and not under another selected folder, which already takes it.
func validateMembers(prefix string, members []string) error {
	seen := make(map[string]struct{}, len(members))
	for _, name := range members {
		switch {
		case name == "":
			return errors.New("a selected object has no name")
		case name == prefix:
			// The folder's own marker has no name relative to the folder, so a
			// copy to a container's top would plan nothing for it and still
			// end complete; refused by name instead (ADR 0108 decision 2).
			return fmt.Errorf("selected object %q is the folder's own marker; select it from the folder above to copy or download it", name)
		case !strings.HasPrefix(name, prefix):
			return fmt.Errorf("selected object %q is not inside %q", name, prefix)
		}
		if _, repeated := seen[name]; repeated {
			return fmt.Errorf("selected object %q is named twice", name)
		}
		seen[name] = struct{}{}
	}
	if member, folder := plan.Nested(members); member != "" {
		return fmt.Errorf("selected %q is inside the selected folder %q, which already takes it; select one or the other", member, folder)
	}
	return nil
}

// selectedRows keeps only what a selection takes from a listing, recording
// in it the members it met; with no selection it is the listing itself.
func selectedRows(rows iter.Seq2[swiftcontrol.ListingEntry, error], selection *plan.Selection) iter.Seq2[swiftcontrol.ListingEntry, error] {
	if selection == nil {
		return rows
	}
	return func(yield func(swiftcontrol.ListingEntry, error) bool) {
		for row, err := range rows {
			if err == nil {
				object, ok := row.(swiftcontrol.ObjectEntry)
				if !ok {
					continue
				}
				if !selection.Take(object.Name) {
					continue
				}
			}
			if !yield(row, err) {
				return
			}
		}
	}
}

// describeOverlap says in plain words why two copy scopes in one container
// overlap; the operator found the identifiers and quoted empty prefixes of
// the earlier wording unreadable.
func describeOverlap(container, source, dest string, sameAccount bool) string {
	scope := func(prefix string) string {
		if prefix == "" {
			return fmt.Sprintf("the whole of container %q", container)
		}
		return fmt.Sprintf("prefix %q of container %q", prefix, container)
	}
	how := "both are " + scope(source)
	if dest != source {
		how = fmt.Sprintf("the destination, %s, is inside the source, %s", scope(dest), scope(source))
	}
	if sameAccount {
		return how + ", in the same account"
	}
	return how + ", and the two bookmarks cannot be shown to be different accounts"
}

// sharesNamespace reports whether a copy's source and destination may be one
// container of one account, where the nesting of their prefixes matters.
// Identity is the ACCOUNT, compared fail-closed: aliases, VIPs and DNS names
// for one account defeat a host-string compare (review R-04), so only
// different authoritative account identifiers prove two accounts distinct.
func sharesNamespace(req CopyRequestDTO, src, dst *swiftcontrol.Session) bool {
	return req.SourceContainer == req.DestContainer && !provenDistinctAccounts(src.StorageURL(), dst.StorageURL())
}

// sourceObjects lists a copy's source for planning: its scope, or only what
// its selection takes (ADR 0108), recording in it the members it met. For a copy of the
// whole scope in one namespace, copied up into a parent (DESIGN §65.9 item
// 9), it ends the listing with a refusal at the first name whose destination
// lands back inside the source folder, such as files/files/a copied up to
// files/a, which the same run may still read; a copy of members settles its
// own overlap in resolve, from the request. Every planning pass lists through
// here, so the passes cannot disagree.
func sourceObjects(ctx context.Context, req CopyRequestDTO, src, dst *swiftcontrol.Session, selection *plan.Selection) iter.Seq2[swiftcontrol.ListingEntry, error] {
	rows := selectedRows(src.Objects(ctx, req.SourceContainer, swiftcontrol.ObjectListOptions{Deep: true, Prefix: req.SourcePrefix}), selection)
	if len(req.Members) > 0 || !sharesNamespace(req, src, dst) {
		return rows
	}
	return func(yield func(swiftcontrol.ListingEntry, error) bool) {
		for row, err := range rows {
			if object, ok := row.(swiftcontrol.ObjectEntry); ok && err == nil {
				target := req.DestPrefix + strings.TrimPrefix(object.Name, req.SourcePrefix)
				if strings.HasPrefix(target, req.SourcePrefix) {
					yield(nil, fmt.Errorf("the copy would write %q back inside the source folder %q, where it could replace a source object the copy still reads; copy to a folder outside the source, or to another container", target, req.SourcePrefix))
					return
				}
			}
			if !yield(row, err) {
				return
			}
		}
	}
}

// resolve opens both sessions and fail-fast checks the destination
// container (copying into a void 404s on the first write — say it now).
func (s *TransferService) resolve(ctx context.Context, req CopyRequestDTO) (src, dst *swiftcontrol.Session, versioned bool, err error) {
	if err := validateCopyRequest(req); err != nil {
		return nil, nil, false, err
	}
	if ceiling := s.downloadObjectCeiling(ctx); int64(len(req.Members)) > ceiling {
		return nil, nil, false, fmt.Errorf("%d objects are selected, more than the selection ceiling of %d; copy the folder instead, or raise the ceiling in Settings", len(req.Members), ceiling)
	}
	src, err = s.connections.SessionFor(ctx, req.SourceBookmarkID)
	if err != nil {
		return nil, nil, false, fmt.Errorf("source: %w", err)
	}
	dstEntry, err := s.connections.sessionEntryFor(ctx, req.DestBookmarkID)
	if err != nil {
		return nil, nil, false, fmt.Errorf("destination: %w", err)
	}
	dst = dstEntry.session
	// A destination equal to or inside the source in ONE namespace is
	// refused outright (C-03, DESIGN §65.9 item 9): every name the copy
	// writes would land in the scope it reads. A copy into a parent folder
	// of its source is allowed, and sourceObjects refuses any one name that
	// would land back inside the source. Mirror refuses every nesting on
	// its own, as it refuses every start (ADR 0020). A copy of members reads
	// and writes only named objects, so for it sourceObjects refuses instead
	// a member whose destination is another member (ADR 0108 decision 4).
	if sharesNamespace(req, src, dst) && len(req.Members) == 0 && strings.HasPrefix(req.DestPrefix, req.SourcePrefix) {
		sameAccount := swiftcontrol.AccountIdentity(src.StorageURL()) == swiftcontrol.AccountIdentity(dst.StorageURL())
		return nil, nil, false, fmt.Errorf(
			"the source and destination overlap: %s, so the copy could copy its own output; choose another container, or a destination folder outside the source folder",
			describeOverlap(req.SourceContainer, req.SourcePrefix, req.DestPrefix, sameAccount))
	}
	// A write that could land on something the same run reads would replace
	// it while the run still reads it. The request alone says so, so it is
	// refused here, before any listing, never by a planning pass that may
	// already have written earlier members: an object member's destination
	// that is taken by the selection, and a folder member's destination that
	// lies in a selected folder or holds a member; a destination equal to
	// the source folder maps every member onto itself (ADR 0108 decisions 4
	// and 7).
	if selection := plan.NewSelection(req.Members); selection != nil && sharesNamespace(req, src, dst) {
		for _, name := range req.Members {
			target := req.DestPrefix + strings.TrimPrefix(name, req.SourcePrefix)
			switch member := selection.Member(target); {
			case member == target && strings.HasSuffix(target, "/"):
				return nil, nil, false, fmt.Errorf("the copy would write the selected folder %q onto itself, so it could replace an object the copy still reads; copy to another folder or container", target)
			case member == target:
				return nil, nil, false, fmt.Errorf("the copy would write %q, which is itself one of the selected objects, so it could replace an object the copy still reads; copy to another folder or container", target)
			case member != "":
				return nil, nil, false, fmt.Errorf("the copy would write %q inside the selected folder %q, so it could replace an object the copy still reads; copy to another folder or container", target, member)
			case strings.HasSuffix(target, "/") && selection.Holds(target):
				return nil, nil, false, fmt.Errorf("the copy would write the folder %q over what is selected inside it, so it could replace an object the copy still reads; copy to another folder or container", target)
			}
		}
	}
	detail, err := dst.ContainerDetail(ctx, req.DestContainer)
	if swiftcontrol.IsNotFound(err) {
		missing := DestinationMissingDTO{BookmarkID: req.DestBookmarkID, Generation: dstEntry.generation, Container: req.DestContainer}
		return nil, nil, false, &destinationMissingError{missing: missing, cause: err}
	}
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
	var missing *destinationMissingError
	if errors.As(err, &missing) {
		return &CopyPlanDTO{DestinationMissing: &missing.missing}, nil
	}
	if err != nil {
		return nil, err
	}
	largeConservative := false
	var overwriteBytes int64
	selection := plan.NewSelection(req.Members)
	summary, err := plan.Copy(ctx,
		sourceObjects(ctx, req, src, dst, selection),
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
		DataPath:             "copies pass through this workstation: each object is read from the source and written at its name in the destination",
		OverwriteBytes:       overwriteBytes,
		MissingMembers:       selection.Missing(),
	}, nil
}

// DestinationContainers lists the first page of a copy destination's
// containers, through the registry session the copy plan reads it with, so
// the destination need not be the active connection (ADR 0115 decision 1).
func (s *TransferService) DestinationContainers(ctx context.Context, bookmarkID string) (DestinationContainersDTO, error) {
	session, err := s.connections.SessionFor(ctx, bookmarkID)
	if err != nil {
		return DestinationContainersDTO{}, err
	}
	page := DestinationContainersDTO{Names: []string{}}
	for entry, err := range session.Containers(ctx, swiftcontrol.ListOptions{PageSize: destinationNameLimit}) {
		if err != nil {
			return DestinationContainersDTO{}, err
		}
		if len(page.Names) == destinationNameLimit {
			page.More = true
			break
		}
		page.Names = append(page.Names, entry.Name)
	}
	return page, nil
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
func (s *TransferService) buildEngine(ctx context.Context, req CopyRequestDTO, src, dst *swiftcontrol.Session, keepsSegments bool) (transfercopy.Engine, TransferSettingsDTO, map[string]string, error) {
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
		return nil, TransferSettingsDTO{}, nil, err
	}
	// §12.3: one child may serve both endpoints only when their TLS
	// trust identities are EQUAL — refuse here so the operator sees the
	// reason at start, not a child-spawn failure mid-job.
	srcTrust, err := rclone.TrustIdentity(remotes[0].InsecureTLS, remotes[0].CACertFile)
	if err != nil {
		return nil, TransferSettingsDTO{}, nil, err
	}
	dstTrust, err := rclone.TrustIdentity(remotes[1].InsecureTLS, remotes[1].CACertFile)
	if err != nil {
		return nil, TransferSettingsDTO{}, nil, err
	}
	if srcTrust != dstTrust {
		return nil, TransferSettingsDTO{}, nil, errors.New("the two bookmarks carry different TLS trust (custom CA or insecure): one transfer child cannot serve both safely (§12.3) — align their trust settings or move data via a location both trust")
	}
	// One recommendation read serves both the resolved values and their
	// recorded provenance (§45). A second, later lookup could disagree
	// with the values if a benchmark recommendation appeared or vanished
	// between the two, labelling a recommended value a profile default or
	// the reverse.
	recommendation := s.recommendationFor(req.SourceBookmarkID)
	effective, err := ResolveSettings(Profile(req.Profile), req.Settings, recommendation)
	if err != nil {
		return nil, TransferSettingsDTO{}, nil, err
	}
	origins := settingOrigins(Profile(req.Profile), req.Settings, recommendation)
	effective, err = effective.forDestination(ctx, dst)
	if err != nil {
		return nil, TransferSettingsDTO{}, nil, err
	}
	options := effective.options()
	// Source sizes and digests describe stored bytes, so Copy must not
	// decode content as it passes through the child.
	options.NoGzipEncoding = true
	// The engine writes each object at its name (ADR 0122 decision 4), and
	// removes a replaced large object's old segments only where
	// keepsReplaced allowed it.
	options.LiveDestination = true
	options.KeepReplacedSegments = keepsSegments
	// The engine's one size check after it writes an object in segments
	// reads the dynamic manifest's size through a listing that can lag the
	// last segment (F35). A copy learns which objects are that large only
	// as it runs, so the check is off for the job; each object and segment
	// is still checked against the store's entity tag as it is written.
	options.IgnoreSize = true
	if s.engineFactory != nil {
		engine, err := s.engineFactory(source, options)
		return engine, effective, origins, err
	}
	runner, err := rclone.NewRunner(options)
	if err != nil {
		return nil, TransferSettingsDTO{}, nil, err
	}
	return transfercopy.BindRunnerSource(runner, source), effective, origins, nil
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
	owner.hold(releasePins)
	src, dst, _, err := s.resolve(ctx, req)
	if err != nil {
		return "", err
	}
	// A copy writes under its destination folder; no sweep of ours may run
	// over that while this job is alive (ADR 0105 decision 3, ADR 0122
	// decision 4).
	if err := owner.claim(destinationClaims(swiftcontrol.AccountIdentity(dst.StorageURL()), req.DestContainer, req.DestPrefix)...); err != nil {
		return "", err
	}
	// One read of the destination's versioning decides whether the engine
	// may remove a replaced large object's old segments, for the whole job;
	// a read that fails keeps them.
	observed, versioningErr := s.readVersioning(ctx, req.DestBookmarkID, req.DestContainer)
	keepsSegments, segments := true, ""
	if versioningErr == nil {
		keepsSegments, segments = s.keepsReplaced(ctx, req.DestContainer, req.DestPrefix, observed)
	}
	// A copy that may replace raises the one native confirmation before
	// anything is written, naming both accounts. One that may not refuses
	// every name the destination already holds differently.
	if req.AcknowledgeOverwrite {
		if err := s.confirmCopyOverwrite(ctx, req, src, dst, observed, versioningErr, segments); err != nil {
			return "", err
		}
	}
	engine, effective, origins, err := s.buildEngine(ctx, req, src, dst, keepsSegments)
	if err != nil {
		return "", fmt.Errorf("transfer engine: %w", err)
	}
	chunkBytes, known := engineChunkBytes(effective.ChunkSize)
	if !known {
		return "", fmt.Errorf("transfer engine: the chunk size %q is not a size", effective.ChunkSize)
	}

	job, err := s.jobs.Create(ctx, jobs.Job{
		Type:               jobs.TypeCopy,
		ItemRecordsVersion: 1,
		Source:             req.SourceContainer + "/" + req.SourcePrefix,
		Destination:        req.DestContainer + "/" + req.DestPrefix,
		SourceBookmarkID:   req.SourceBookmarkID,
		DestBookmarkID:     req.DestBookmarkID,
		Engine:             "rclone+native",
		Settings:           effectiveJSONWithProfile(Profile(req.Profile), effective, origins),
		SelectedMembers:    len(req.Members),
		SelectedNames:      req.Members,
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
	owner.launch(job.ID, req.SourceBookmarkID, func(runCtx context.Context) {
		s.runCopy(runCtx, job.ID, req, src, dst, engine, chunkBytes)
	}, func(background context.Context) {
		s.finishCopy(background, job.ID, req, job.Counters, jobs.StateCancelled, "")
	})
	return job.ID, nil
}

// confirmCopyOverwrite raises the one native confirmation that authorises
// replacing what the destination holds, naming BOTH accounts — the one the
// bytes come from and the one they land in (ADR 0073, ADR 0122 decision 2).
//
// It asks about the names the two listings show as different and, of the
// pairs the listings cannot compare, those the store does not describe as the
// same: the copy makes the same two reads for such a pair and decides the
// same way. Nothing to replace asks nothing. Both accounts' generations are
// checked either side of the dialog, so an answer given about one pair of
// accounts cannot authorise a write under another.
func (s *TransferService) confirmCopyOverwrite(ctx context.Context, req CopyRequestDTO, src, dst *swiftcontrol.Session,
	observed swiftcontrol.ContainerVersioning, versioningErr error, segments string) error {
	type candidate struct {
		name    string
		unknown bool
	}
	var candidates []candidate
	if _, err := plan.Copy(ctx,
		sourceObjects(ctx, req, src, dst, plan.NewSelection(req.Members)),
		dst.Objects(ctx, req.DestContainer, swiftcontrol.ObjectListOptions{Deep: true, Prefix: req.DestPrefix}),
		req.SourcePrefix, req.DestPrefix, func(item plan.Item) error {
			if item.Verdict == plan.VerdictChanged || item.Verdict == plan.VerdictUnknown {
				candidates = append(candidates, candidate{name: item.Name, unknown: item.Verdict == plan.VerdictUnknown})
			}
			return nil
		}); err != nil {
		return err
	}
	var names []string
	for _, c := range candidates {
		if c.unknown {
			replaces, err := replacesUnknown(ctx, req, src, dst, c.name)
			if err != nil {
				return err
			}
			if !replaces {
				continue
			}
		}
		names = append(names, c.name)
	}
	if len(names) == 0 {
		return nil
	}
	if s.overwrites == nil {
		return errors.New("replacing an existing object needs the shell's confirmation, and this build has none")
	}
	if versioningErr != nil {
		return versioningErr
	}
	versioning, err := versioningStatement(observed)
	if err != nil {
		return err
	}
	srcBefore, err := s.connections.sessionEntryFor(ctx, req.SourceBookmarkID)
	if err != nil {
		return err
	}
	dstBefore, err := s.connections.sessionEntryFor(ctx, req.DestBookmarkID)
	if err != nil {
		return err
	}
	question, err := copyOverwriteQuestion(
		s.connections.displayName(ctx, req.SourceBookmarkID), accountSuffix(src.StorageURL()),
		s.connections.displayName(ctx, req.DestBookmarkID), accountSuffix(dst.StorageURL()),
		req.DestContainer, req.DestPrefix, versioning, segments, names)
	if err != nil {
		return err
	}
	yes, err := s.overwrites.ConfirmOverwrite(ctx, question)
	if err != nil {
		return err
	}
	if !yes {
		return errors.New("the replacement was not confirmed; nothing was written")
	}
	srcAfter, err := s.connections.sessionEntryFor(ctx, req.SourceBookmarkID)
	if err != nil {
		return err
	}
	dstAfter, err := s.connections.sessionEntryFor(ctx, req.DestBookmarkID)
	if err != nil {
		return err
	}
	if srcAfter.generation != srcBefore.generation || dstAfter.generation != dstBefore.generation {
		return errors.New("an account's credentials changed while you were answering; start again")
	}
	return nil
}

// replacesUnknown reads both sides of a pair the listings cannot compare, an
// empty row or a large object, and reports whether a copy would replace the
// destination: it would not when the name is no longer there, which is a
// first copy, nor when the store describes the two as the same.
func replacesUnknown(ctx context.Context, req CopyRequestDTO, src, dst *swiftcontrol.Session, name string) (bool, error) {
	there, err := dst.ObjectDetail(ctx, req.DestContainer, req.DestPrefix+name)
	if swiftcontrol.IsNotFound(err) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("reading %s before asking to replace it: %w", name, err)
	}
	from, err := src.ObjectDetail(ctx, req.SourceContainer, req.SourcePrefix+name)
	if err != nil {
		return false, fmt.Errorf("reading the source of %s before asking to replace it: %w", name, err)
	}
	return !transfercopy.SameByStore(from, there), nil
}

// copyOverwriteQuestion composes the two-account confirmation. It refuses to
// compose a statement missing either account, the container, its versioning,
// what happens to a replaced large object's old segments, or the names, so no
// caller can raise a question that authorises nothing.
func copyOverwriteQuestion(srcLabel, srcAccount, dstLabel, dstAccount,
	container, prefix, versioning, segments string, names []string) (OverwriteConfirmation, error) {
	switch {
	case srcAccount == "":
		return OverwriteConfirmation{}, errors.New("the source account could not be identified, so an overwrite is refused")
	case dstAccount == "":
		return OverwriteConfirmation{}, errors.New("the destination account could not be identified, so an overwrite is refused")
	case container == "":
		return OverwriteConfirmation{}, errors.New("the destination container could not be identified, so an overwrite is refused")
	case versioning == "":
		return OverwriteConfirmation{}, errors.New("the destination container's versioning could not be stated, so an overwrite is refused")
	case segments == "":
		return OverwriteConfirmation{}, errors.New("what happens to a replaced large object's old segments could not be stated, so an overwrite is refused")
	case len(names) == 0:
		return OverwriteConfirmation{}, errors.New("there is nothing live to ask about, so no overwrite is authorised")
	}
	return OverwriteConfirmation{
		Bookmark: titleText(dstLabel, 80), Account: titleText(dstAccount, 80),
		SourceBookmark: titleText(srcLabel, 80), SourceAccount: titleText(srcAccount, 80),
		Container: container, Prefix: prefix, Names: names, Versioning: versioning,
		Fragments: segments,
	}, nil
}

// joinNotes joins the notes that have something to say with "; ", so a
// message or an audit summary built from optional parts carries no
// separator bookkeeping at each part.
func joinNotes(notes ...string) string {
	kept := make([]string, 0, len(notes))
	for _, note := range notes {
		if note != "" {
			kept = append(kept, note)
		}
	}
	return strings.Join(kept, "; ")
}

func (s *TransferService) finishCopy(ctx context.Context, jobID string, req CopyRequestDTO, counters jobs.Counters, state jobs.State, message string) {
	totals, totalsErr := s.jobs.ReadItemTotals(ctx, jobID)
	if totalsErr != nil {
		message = joinNotes(message, totalsErr.Error())
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
		refusal := count + ": each refused name was left as it was, and its record says why"
		message = joinNotes(refusal, message)
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
		applog.Errorf("job %s terminal outcome was not saved: %v", jobID, err)
	}
}

// runCopy is the job goroutine: Planning → Running, with the plan streamed
// straight into the executor (the plan is advisory, §15.3, so the planned
// counters grow live and cost no second listing pass) → Verifying →
// terminal. Each object's outcome is what the engine or the store answered
// for it (ADR 0122 decision 4), so nothing is listed or read again afterwards.
func (s *TransferService) runCopy(ctx context.Context, jobID string, req CopyRequestDTO,
	src, dst *swiftcontrol.Session, engine transfercopy.Engine, chunkBytes int64) {

	background := context.WithoutCancel(ctx) // store writes survive cancel
	counters := jobs.Counters{}
	var countersMu sync.Mutex
	finish := func(state jobs.State, message string) {
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

	executor, err := transfercopy.New(transfercopy.Config{
		Source: src, Dest: dst, Engine: engine,
		SrcSpec: "aeronautes_src:" + req.SourceContainer, DstSpec: "aeronautes_dst:" + req.DestContainer,
		SrcContainer: req.SourceContainer, DstContainer: req.DestContainer,
		SrcPrefix: req.SourcePrefix, DstPrefix: req.DestPrefix,
		Replace:    req.AcknowledgeOverwrite,
		ChunkBytes: chunkBytes,
		Recorder:   s.copyItemRecorder(background, jobID, req),
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
	if !admit(jobs.StateRunning) {
		return
	}

	// How many members the listing met; a copy of the whole scope leaves it
	// unused.
	selection := plan.NewSelection(req.Members)
	_, planErr := plan.Copy(ctx,
		sourceObjects(ctx, req, src, dst, selection),
		dst.Objects(ctx, req.DestContainer, swiftcontrol.ObjectListOptions{Deep: true, Prefix: req.DestPrefix}),
		req.SourcePrefix, req.DestPrefix, func(item plan.Item) error {
			countersMu.Lock()
			switch item.Verdict {
			case plan.VerdictNew, plan.VerdictChanged, plan.VerdictUnknown:
				counters.ObjectsPlanned++
				counters.BytesPlanned += item.SourceBytes
			}
			countersMu.Unlock()
			return executor.Handle(ctx, item)
		})
	executor.Stop(planErr)
	report, finishErr := executor.Finish(ctx)
	planErr = finishErr

	countersMu.Lock()
	counters.ObjectsCopied = report.Copied
	counters.BytesCopied = report.BytesCopied
	counters.Overwrites = report.Overwrites
	counters.OverwritesRefused = report.OverwritesRefused
	counters.ObjectsSame = report.SkippedEqual
	counters.NativeRouted = report.NativeRouted
	counters.ObjectsVerified = report.Verified
	countersMu.Unlock()

	// These records preserve creation provenance, not deletion authority.
	s.recordOwnership(background, req.DestBookmarkID, jobID, report.CapturedPrefixes)

	if planErr != nil {
		if len(report.Failures) > 0 {
			finish(jobs.StatePartial, fmt.Sprintf("execution failed (example: %s: %s); %s", report.Failures[0].Name, report.Failures[0].Err, planErr.Error()))
		} else if report.Copied > 0 {
			finish(jobs.StatePartial, planErr.Error())
		} else if errors.Is(planErr, context.Canceled) {
			message := ""
			if report.OverwritesRefused > 0 {
				message = planErr.Error()
			}
			finish(jobs.StateCancelled, message)
		} else {
			finish(jobs.StateFailed, planErr.Error())
		}
		return
	}

	// The job passes through its verifying state, which the item ledger
	// keeps until the removal step of ADR 0122; every record was written
	// as its answer arrived.
	if err := s.jobs.Transition(background, jobID, jobs.StateVerifying); err != nil {
		message := "verification state could not be saved: " + err.Error()
		if len(report.Failures) > 0 {
			message += fmt.Sprintf("; execution failed (example: %s: %s)", report.Failures[0].Name, report.Failures[0].Err)
		}
		finish(jobs.StatePartial, message)
		return
	}

	missing := selection.Missing()
	switch {
	case len(report.Failures) > 0 || missing > 0:
		parts := []string{}
		if n := len(report.Failures); n > 0 {
			parts = append(parts, fmt.Sprintf("execution failed (example: %s: %s)", report.Failures[0].Name, report.Failures[0].Err))
		}
		if missing > 0 {
			parts = append(parts, fmt.Sprintf("%d of the selected objects and folders were no longer at the source", missing))
		}
		finish(jobs.StatePartial, strings.Join(parts, "; "))
	default:
		finish(jobs.StateComplete, "")
	}
}

// recoveryRecorder keeps where the engine put an object's segments, as the
// executor reports it. An error returns to the executor, which stops the run:
// a copy that could leave segments nobody can name does not go on.
func (s *TransferService) recoveryRecorder(ctx context.Context, jobID, bookmarkID string) func(kind, container, prefix, object string) error {
	return func(kind, container, prefix, object string) error {
		err := s.jobs.RecoveryAdd(ctx, jobs.RecoveryRecord{
			JobID: jobID, BookmarkID: bookmarkID, Kind: jobs.RecoveryKind(kind),
			Container: container, Prefix: prefix, Object: object,
		})
		if err != nil {
			applog.Errorf("job %s: recovery record (%s %s/%s): %v", jobID, kind, container, prefix, err)
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

// CancelJob requests graceful cancellation. The engine's own cleanup of what
// it was writing is best-effort, so a cancelled large transfer can leave
// segments, which the job's record names and nothing removes (ADR 0122
// decision 8).
func (s *TransferService) CancelJob(ctx context.Context, jobID string) error {
	cancel, ok := s.runner.cancel(jobID)
	if !ok {
		return fmt.Errorf("job %s is not running in this process", jobID)
	}
	cancel()
	s.record(ctx, "", "transfer.cancel-requested", "job "+jobID, "operator cancel", audit.ResultApplied)
	return nil
}

// PauseJob asks an upload, a download or a copy to pause at its next unit
// boundary (ADR 0104): the unit in hand finishes and is recorded, nothing is
// killed, and the job is then paused until it is resumed or cancelled. A
// deletion job is not paused, and neither is a job that is finishing.
func (s *TransferService) PauseJob(ctx context.Context, jobID string) error {
	job, err := s.jobs.Get(ctx, jobID)
	if err != nil {
		return err
	}
	switch job.Type {
	case jobs.TypeUpload, jobs.TypeDownload, jobs.TypeCopy:
	default:
		return fmt.Errorf("job %s cannot be paused: only an upload, a download or a copy pauses", jobID)
	}
	switch job.State {
	case jobs.StateQueued, jobs.StatePlanning, jobs.StateRunning:
	default:
		return fmt.Errorf("job %s is %s and cannot be paused", jobID, job.State)
	}
	if !s.runner.requestPause(jobID) {
		return fmt.Errorf("job %s is not running in this process", jobID)
	}
	return nil
}

// ResumeJob withdraws a pause: a job that has not reached its boundary
// carries on, and a paused one waits for a slot and runs its next unit.
// Nothing resumes a job this process is not holding.
func (s *TransferService) ResumeJob(ctx context.Context, jobID string) error {
	if !s.runner.requestResume(jobID) {
		return fmt.Errorf("job %s is not paused in this process and cannot be resumed", jobID)
	}
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
	// Pause is what is pending on the job beside its state (ADR 0104):
	// "pausing" while a pause waits for the unit in hand to finish,
	// "resuming" while a resumed job waits for a slot, and "" otherwise.
	Pause string `json:"pause"`
	// SelectedMembers counts the objects a copy or download of selected
	// objects named; zero means it acted on its whole scope (ADR 0108).
	SelectedMembers int `json:"selectedMembers,omitempty"`
}

// JobService is the read-only §47 drawer surface.
type JobService struct {
	jobs *jobs.Store
	// runner knows what is pending on a job that its stored state does not
	// say. It is nil for a service built only to read stored jobs.
	runner *jobRunner
}

// NewJobService builds the service over stored jobs alone, for a reader of
// stored jobs that holds no runner, such as the system test harness; it
// reports no pending pause or resume.
func NewJobService(jobStore *jobs.Store) *JobService {
	return &JobService{jobs: jobStore}
}

// NewJobServiceWithRunner builds the service the shell binds: it also reports
// a pause or a resume the running job has not yet reached.
func NewJobServiceWithRunner(jobStore *jobs.Store, runner *jobRunner) *JobService {
	return &JobService{jobs: jobStore, runner: runner}
}

// dto is one job as the drawer shows it.
func (s *JobService) dto(j jobs.Job) JobDTO {
	out := jobDTO(j)
	if s.runner != nil {
		out.Pause = s.runner.pauseStatus(j.ID)
	}
	return out
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
		out = append(out, s.dto(j))
	}
	return out, nil
}

// Get returns one job bound to the named bookmark (ADR 0059).
func (s *JobService) Get(ctx context.Context, bookmarkID, jobID string) (*JobDTO, error) {
	j, err := s.jobInAccount(ctx, bookmarkID, jobID)
	if err != nil {
		return nil, err
	}
	dto := s.dto(j)
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
		SelectedMembers: j.SelectedMembers,
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
	// A copy of selected objects is re-run as the same members, never as
	// its whole scope (ADR 0108 decision 5).
	if job.SelectedMembers > 0 {
		names, err := s.jobs.SelectedNames(ctx, jobID)
		if err != nil || len(names) != job.SelectedMembers {
			return "", fmt.Errorf("the %d objects job %s copied could not be read back; nothing was started", job.SelectedMembers, jobID)
		}
		request.Members = names
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

// provenDistinctAccounts reports whether two storage URLs are
// DEMONSTRABLY different accounts. It returns true ONLY when both
// account identifiers parse and differ — anything ambiguous (a missing
// identifier, an unparseable URL) is NOT proven distinct, so the
// overlap guard fails closed (review R-04).
func provenDistinctAccounts(a, b string) bool {
	return distinctAccounts(swiftcontrol.AccountIdentity(a), swiftcontrol.AccountIdentity(b))
}

// distinctAccounts is that rule on two account identifiers: different only
// when both are known and differ. An identifier that could not be read is ""
// and is compared as possibly any account.
func distinctAccounts(idA, idB string) bool {
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

// InterruptedItemFactsDTO is the acknowledged item ledger for one interrupted
// job (ADR 0088 decision 5). The counts are facts the records retain, never a
// verdict: an admitted copied or verified item stands, a conflict is its own
// fact, and an unresolved or unverified member is reported as such.
type InterruptedItemFactsDTO struct {
	Items       int64 `json:"items"`
	Admitted    int64 `json:"admitted"`
	Copied      int64 `json:"copied"`
	Verified    int64 `json:"verified"`
	CopiedBytes int64 `json:"copiedBytes"`
	Failed      int64 `json:"failed"`
	Conflicts   int64 `json:"conflicts"`
	Refused     int64 `json:"refused"`
	Unresolved  int64 `json:"unresolved"`
	Unverified  int64 `json:"unverified"`
	Deleted     int64 `json:"deleted"`
	Unsettled   int64 `json:"unsettled"`
}

// InterruptedDisclosureDTO is one undisclosed interrupted job for the ADR 0088
// notice. It reports only what the retained records prove.
type InterruptedDisclosureDTO struct {
	JobID         string `json:"jobId"`
	Type          string `json:"type"`
	Source        string `json:"source"`
	Destination   string `json:"destination"`
	CreatedAt     string `json:"createdAt"`
	InterruptedAt string `json:"interruptedAt"`
	// InterruptedState is the state the restart overwrote; "queued" is the one
	// value that proves nothing was attempted.
	InterruptedState string `json:"interruptedState"`
	// Attempted is true when the ledger records at least one execution
	// observation, whatever state the sweep overwrote. Only a job that is queued
	// AND attempted nothing proves nothing was attempted; a queued deletion that
	// logged outcomes is attempted.
	Attempted bool `json:"attempted"`
	// ItemsCaptured is false when the job kept no item ledger; the counts are
	// then zero because there is nothing to read, not because nothing happened.
	ItemsCaptured bool                    `json:"itemsCaptured"`
	Items         InterruptedItemFactsDTO `json:"items"`
	// RecoveryRecords may indicate remote residue where the route writes them.
	RecoveryRecords int64 `json:"recoveryRecords"`
	// InconclusiveCommits counts names whose commit ledger is inconclusive —
	// not failures (ADR 0031 line 75).
	InconclusiveCommits int64 `json:"inconclusiveCommits"`
	// PlacesLocalResidue is true for a route that places bytes in the operator's
	// own filesystem (download); what is there now is theirs to inspect, and ADR
	// 0020 forbids a sweep. The renderer uses it to explain a zero
	// recovery-record count rather than read it as nothing having happened; a
	// positive count is shown whatever the route, so a historical Mirror job's
	// retained records are never hidden.
	PlacesLocalResidue bool `json:"placesLocalResidue"`
}

// InterruptedDisclosuresDTO is the bounded page the ADR 0088 notice reads.
type InterruptedDisclosuresDTO struct {
	Entries []InterruptedDisclosureDTO `json:"entries"`
	Total   int64                      `json:"total"`
}

// InterruptedDisclosures returns the interruptions the operator has not been
// told about, newest first (ADR 0088 decision 4). An unavailable read is an
// error, never an empty page, so the renderer shows the status as unknown and
// never as "no interruptions".
func (s *JobService) InterruptedDisclosures(ctx context.Context, limit int) (InterruptedDisclosuresDTO, error) {
	page, err := s.jobs.InterruptedDisclosures(ctx, limit)
	if err != nil {
		return InterruptedDisclosuresDTO{}, errors.New("interrupted job disclosures are unavailable")
	}
	out := InterruptedDisclosuresDTO{Entries: make([]InterruptedDisclosureDTO, 0, len(page.Entries)), Total: page.Total}
	for _, e := range page.Entries {
		out.Entries = append(out.Entries, interruptedDisclosureDTO(e))
	}
	return out, nil
}

// DismissInterruptedDTO reports how many notices the dismissal recorded as
// disclosed — never a cleanup claim (ADR 0088 decision 3).
type DismissInterruptedDTO struct {
	Dismissed int64 `json:"dismissed"`
}

// DismissInterrupted records that the operator was told about these
// interruptions and dismisses their notices (ADR 0088 decision 3). It changes
// no job's state, counters or error and clears no residue. A write that cannot
// be made is a failure, not a silent success (decision 7).
func (s *JobService) DismissInterrupted(ctx context.Context, jobIDs []string) (DismissInterruptedDTO, error) {
	dismissed, err := s.jobs.DiscloseInterrupted(ctx, jobIDs)
	if err != nil {
		return DismissInterruptedDTO{}, errors.New("dismissing the interruption notice did not succeed; it remains")
	}
	return DismissInterruptedDTO{Dismissed: dismissed}, nil
}

func interruptedDisclosureDTO(e jobs.InterruptedDisclosure) InterruptedDisclosureDTO {
	formatTime := func(t time.Time) string {
		if t.IsZero() {
			return ""
		}
		return t.UTC().Format(time.RFC3339)
	}
	return InterruptedDisclosureDTO{
		JobID:            e.JobID,
		Type:             string(e.Type),
		Source:           e.Source,
		Destination:      e.Destination,
		CreatedAt:        formatTime(e.CreatedAt),
		InterruptedAt:    formatTime(e.InterruptedAt),
		InterruptedState: string(e.InterruptedState),
		Attempted:        e.Attempted,
		ItemsCaptured:    e.ItemsCaptured,
		Items: InterruptedItemFactsDTO{
			Items:       e.Items.Items,
			Admitted:    e.Items.Admitted,
			Copied:      e.Copied,
			Verified:    e.Verified,
			CopiedBytes: e.CopiedBytes,
			Failed:      e.Items.Failed,
			Conflicts:   e.Items.Conflicts,
			Refused:     e.Items.Refused,
			Unresolved:  e.Items.Unresolved,
			Unverified:  e.Items.Unverified,
			Deleted:     e.Items.Deleted,
			Unsettled:   e.Items.Unsettled,
		},
		RecoveryRecords:     e.RecoveryRecords,
		InconclusiveCommits: e.InconclusiveCommits,
		PlacesLocalResidue:  placesLocalResidue(e.Type),
	}
}

// placesLocalResidue says whether a job type puts bytes in the operator's own
// filesystem. Only download does; what is in that directory now is the
// operator's to inspect, and ADR 0020's containment forbids a sweep of it.
func placesLocalResidue(t jobs.Type) bool {
	return t == jobs.TypeDownload
}
