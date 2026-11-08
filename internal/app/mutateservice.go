package app

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/csmart/aeronautes/internal/audit"
	"github.com/csmart/aeronautes/internal/evidence"
	"github.com/csmart/aeronautes/internal/mutationfacts"
	"github.com/csmart/aeronautes/internal/swiftcontrol"
)

// MutateService is the ONLY mutating bridge surface (ADR 0011): every
// method runs preflight → execute → verify → audit, takes complete
// intended outcomes (working copies, never header grab-bags), and gates
// on connect-time capabilities where the cluster is KNOWN to reject a
// request — Unknown never gates (§10).
type MutateService struct {
	connections *Connections
	audit       *audit.Store
	evidence    *evidence.Store
	facts       *mutationfacts.Store
	writers     *writerGate
	// confirmer asks the operator outside the webview before a container
	// becomes world-readable (ADR 0073 option B); nil refuses.
	confirmer PublicACLConfirmer
}

// PublicACLConfirmer asks the operator, on a surface the page cannot draw,
// whether a container may become readable by anyone (ADR 0073 option B).
// It is an injected seam like FilePicker so internal/app stays plain Go
// (ADR 0011): main.go supplies the shell's native dialog, tests supply
// answers, and a service with none refuses rather than assumes.
type PublicACLConfirmer interface {
	ConfirmPublicRead(ctx context.Context, container, account string) (bool, error)
}

// setConfirmer wires the shell's native confirmation.
func (s *MutateService) setConfirmer(confirmer PublicACLConfirmer) {
	s.confirmer = confirmer
}

// errMetadataResidue is returned when a store POST succeeded and the
// outcome transaction did not. Remote metadata is an independent
// snapshot; the identity is not applied (ADR 0058).
var errMetadataResidue = errors.New("object metadata write residue: the store POST succeeded and the outcome could not be recorded")

// NewMutateService builds the service.
func NewMutateService(connections *Connections, auditStore *audit.Store, evidenceStore *evidence.Store) *MutateService {
	return &MutateService{
		connections: connections, audit: auditStore, evidence: evidenceStore,
		facts:   mutationfacts.NewStore(auditStore.Database()),
		writers: newWriterGate(),
	}
}

// shutdownWriters closes writer admission and waits for entered writes
// to leave after their final record (ADR 0053). Unexported so it is not
// a bound Wails bridge method.
func (s *MutateService) shutdownWriters(ctx context.Context) error {
	return s.writers.Shutdown(ctx)
}

func (s *MutateService) occupy() error {
	return s.writers.enter()
}

// UpdateObjectMetadata applies a complete metadata working copy to one
// object (§17, §18).
func (s *MutateService) UpdateObjectMetadata(ctx context.Context, scope ListingScopeDTO, container, name, baseFingerprint string, edit ObjectMetadataEditDTO) (MutationResultDTO, error) {
	if err := s.occupy(); err != nil {
		return MutationResultDTO{}, err
	}
	defer s.writers.leave()
	entry, bookmarkID, _, err := s.boundSnapshot(ctx, scope)
	if err != nil {
		return MutationResultDTO{}, err
	}
	adapterEdit := swiftcontrol.ObjectMetadataEdit{
		Meta:        edit.Meta,
		ContentType: edit.ContentType,
	}
	if edit.DeleteAt != "" {
		deleteAt, err := time.Parse(time.RFC3339, edit.DeleteAt)
		if err != nil {
			return MutationResultDTO{}, fmt.Errorf("deleteAt %q is not RFC3339: %w", edit.DeleteAt, err)
		}
		adapterEdit.DeleteAt = deleteAt
	}

	resource := container + "/" + name
	identity, err := mutationfacts.NewIdentity()
	if err != nil {
		return MutationResultDTO{}, err
	}
	if err := s.facts.RecordIntent(ctx, mutationfacts.Intent{
		Identity:   identity,
		BookmarkID: bookmarkID,
		Operation:  "object.metadata.update",
		Resource:   resource,
	}); err != nil {
		return MutationResultDTO{}, err
	}

	result, err := entry.session.UpdateObjectMetadata(ctx, container, name, baseFingerprint, adapterEdit)
	outcome, auditRes, summary, transIDs := objectMetadataFact(result, err, edit)
	if recErr := s.recordMutationOutcome(ctx, identity, bookmarkID, "object.metadata.update", resource, outcome, auditRes, summary, transIDs); recErr != nil {
		if objectMetadataPosted(result, err) {
			return MutationResultDTO{}, fmt.Errorf("%w: identity %s: %v", errMetadataResidue, identity, recErr)
		}
		return MutationResultDTO{}, recErr
	}
	if err != nil {
		if errors.Is(err, swiftcontrol.ErrUnverifiedWrite) {
			return MutationResultDTO{}, fmt.Errorf("%w: identity %s", swiftcontrol.ErrUnverifiedWrite, identity)
		}
		return MutationResultDTO{}, err
	}

	dto := MutationResultDTO{
		Outcome:    string(result.Outcome),
		Mismatch:   result.Mismatch,
		TransIDs:   result.TransIDs,
		Limitation: result.Limitation,
	}
	if result.After != nil {
		after := toObjectDetailDTO(result.After)
		dto.ObjectAfter = &after
	}
	return dto, nil
}

// ErrMutationScopeChanged refuses a write whose caller's view was built
// under a connection that is no longer the active one (ADR 0079): the
// bookmark differs, the generation moved, or no scope was named. Nothing
// is recorded and nothing is sent.
var ErrMutationScopeChanged = errors.New("the connected account changed; nothing was written")

// boundSnapshot is activeSnapshot bound to the caller's scope: the active
// connection must be the named bookmark at the named generation, and the
// store must still hold that generation, before any intent is recorded.
func (s *MutateService) boundSnapshot(ctx context.Context, scope ListingScopeDTO) (cachedSession, string, *swiftcontrol.Capabilities, error) {
	return s.connections.boundSnapshot(ctx, scope)
}

// boundSnapshot is activeSnapshot bound to the caller's scope, for any
// service that starts a write or a job from a view (ADR 0079).
func (c *Connections) boundSnapshot(ctx context.Context, scope ListingScopeDTO) (cachedSession, string, *swiftcontrol.Capabilities, error) {
	if scope.BookmarkID == "" || scope.Generation <= 0 {
		return cachedSession{}, "", nil, fmt.Errorf("%w: name the bookmark and generation the view was built under", ErrMutationScopeChanged)
	}
	entry, bookmarkID, caps, err := c.activeSnapshot()
	if err != nil {
		return cachedSession{}, "", nil, err
	}
	if bookmarkID != scope.BookmarkID || entry.generation != scope.Generation {
		return cachedSession{}, "", nil, ErrMutationScopeChanged
	}
	if err := c.store.WithCurrentGeneration(ctx, bookmarkID, scope.Generation, nil); err != nil {
		return cachedSession{}, "", nil, fmt.Errorf("%w: %v", ErrMutationScopeChanged, err)
	}
	return entry, bookmarkID, caps, nil
}

// UpdateContainer applies a complete container edit (§18, §19, §21).
func (s *MutateService) UpdateContainer(ctx context.Context, scope ListingScopeDTO, name, baseFingerprint string, edit ContainerEditDTO) (MutationResultDTO, error) {
	if err := s.occupy(); err != nil {
		return MutationResultDTO{}, err
	}
	defer s.writers.leave()
	entry, bookmarkID, caps, err := s.boundSnapshot(ctx, scope)
	if err != nil {
		return MutationResultDTO{}, err
	}
	// A world-readable ACL is a public-exposure decision requiring an
	// explicit acknowledgement. This guards against an operator
	// ACCIDENT (an omitted checkbox); it is NOT a hostile-webview
	// control, since a compromised page can supply ackPublic itself —
	// closing that requires a server-issued one-shot capability, which
	// is §65.10 / R-12. Documented honestly rather than overclaimed.
	if grantsPublicRead(edit.ReadACL) && !edit.AckPublic {
		return MutationResultDTO{}, fmt.Errorf(
			"read ACL %q makes %s publicly readable: set ackPublic to confirm the exposure", edit.ReadACL, name)
	}
	adapterEdit := swiftcontrol.ContainerEdit{
		Meta:     edit.Meta,
		ReadACL:  edit.ReadACL,
		WriteACL: edit.WriteACL,
		Versioning: swiftcontrol.ContainerVersioning{
			Mode:    swiftcontrol.VersioningMode(edit.VersioningMode),
			Archive: edit.VersioningArchive,
		},
		QuotaBytes: edit.QuotaBytes,
		QuotaCount: edit.QuotaCount,
	}
	if adapterEdit.Versioning.Mode == "" {
		adapterEdit.Versioning.Mode = swiftcontrol.VersioningOff
	}

	// Capability gate (§10, §21): refuse only what the cluster is KNOWN
	// to reject.
	if caps != nil &&
		!caps.AllowsVersioningMode(adapterEdit.Versioning.Mode) {
		return MutationResultDTO{}, fmt.Errorf(
			"cluster does not advertise %s in versioned_writes allowed_flags", adapterEdit.Versioning.Mode)
	}

	// A world-readable ACL is confirmed outside the webview's control
	// (ADR 0073 option B): the backend raises the shell's native dialog
	// and writes nothing unless it answers yes. A container that already
	// grants public read exposes nothing new and is not asked again.
	if grantsPublicRead(edit.ReadACL) {
		if err := s.confirmPublicRead(ctx, entry, name); err != nil {
			return MutationResultDTO{}, err
		}
	}

	identity, err := mutationfacts.NewIdentity()
	if err != nil {
		return MutationResultDTO{}, err
	}
	if err := s.facts.RecordIntent(ctx, mutationfacts.Intent{
		Identity: identity, BookmarkID: bookmarkID, Operation: "container.update", Resource: name,
	}); err != nil {
		return MutationResultDTO{}, err
	}

	result, err := entry.session.UpdateContainer(ctx, name, baseFingerprint, adapterEdit)
	outcome, auditRes, summary, transIDs := containerMetadataFact(result, err, adapterEdit)
	if recErr := s.recordMutationOutcome(ctx, identity, bookmarkID, "container.update", name, outcome, auditRes, summary, transIDs); recErr != nil {
		if containerMetadataPosted(result, err) {
			return MutationResultDTO{}, fmt.Errorf("%w: identity %s: %w", errContainerMetadataResidue, identity, recErr)
		}
		return MutationResultDTO{}, recErr
	}
	if err != nil {
		if errors.Is(err, swiftcontrol.ErrUnverifiedContainerWrite) {
			return MutationResultDTO{}, fmt.Errorf("%w: identity %s", swiftcontrol.ErrUnverifiedContainerWrite, identity)
		}
		return MutationResultDTO{}, err
	}
	dto := MutationResultDTO{
		Outcome:  string(result.Outcome),
		Mismatch: result.Mismatch,
		TransIDs: result.TransIDs,
	}
	if result.After != nil {
		after := s.containerDetailForBookmark(ctx, bookmarkID, result.After)
		// The verified post-write state is a fresh observation of the
		// container's settings under this connection (ADR 0071).
		after.Settings = containerSettings(result.After, entry.generation, time.Now())
		s.connections.settings.remember(bookmarkID, entry.generation, name, after.Settings)
		dto.ContainerAfter = &after
	}
	return dto, nil
}

// CreateContainer creates a container (§20), gating the policy on the
// advertised list when it is known.
func (s *MutateService) CreateContainer(ctx context.Context, scope ListingScopeDTO, create ContainerCreateDTO) (ContainerDetailDTO, error) {
	if err := s.occupy(); err != nil {
		return ContainerDetailDTO{}, err
	}
	defer s.writers.leave()
	// The admitted account is captured once, as the metadata writes do
	// (ADR 0065): the record and the detail name the account that created
	// the container, not whichever connection is active when they are
	// written.
	entry, bookmarkID, caps, err := s.boundSnapshot(ctx, scope)
	if err != nil {
		return ContainerDetailDTO{}, err
	}
	if caps != nil && !caps.AllowsStoragePolicy(create.Policy) {
		return ContainerDetailDTO{}, fmt.Errorf("cluster does not advertise storage policy %q", create.Policy)
	}

	// The attempt is a durable fact before the PUT and after it (ADR
	// 0077): an intent that fails issues no PUT, and a PUT that may have
	// landed is never reported without its recorded outcome.
	identity, err := mutationfacts.NewIdentity()
	if err != nil {
		return ContainerDetailDTO{}, err
	}
	if err := s.facts.RecordIntent(ctx, mutationfacts.Intent{
		Identity: identity, BookmarkID: bookmarkID, Operation: "container.create", Resource: create.Name,
	}); err != nil {
		return ContainerDetailDTO{}, err
	}
	detail, err := entry.session.CreateContainer(ctx, swiftcontrol.ContainerCreateRequest{
		Name: create.Name, Policy: create.Policy, Meta: create.Meta,
	})
	outcome, auditRes, summary, transIDs := containerCreateFact(detail, err)
	if recErr := s.recordMutationOutcome(ctx, identity, bookmarkID, "container.create", create.Name, outcome, auditRes, summary, transIDs); recErr != nil {
		if outcome == mutationfacts.OutcomeApplied || outcome == mutationfacts.OutcomeUnverified {
			return ContainerDetailDTO{}, fmt.Errorf("%w: identity %s: %w", errContainerCreateResidue, identity, recErr)
		}
		return ContainerDetailDTO{}, recErr
	}
	if err != nil {
		if outcome == mutationfacts.OutcomeUnverified {
			return ContainerDetailDTO{}, fmt.Errorf("%w: identity %s", err, identity)
		}
		return ContainerDetailDTO{}, err
	}
	return s.containerDetailForBookmark(ctx, bookmarkID, detail), nil
}

// CreateMarker creates the explicit §14.1 marker object at a prefix.
func (s *MutateService) CreateMarker(ctx context.Context, scope ListingScopeDTO, container, prefix string) (PrefixDetailDTO, error) {
	if err := refuseIfDeferred("MutateService.CreateMarker"); err != nil {
		return PrefixDetailDTO{}, err
	}
	if err := s.occupy(); err != nil {
		return PrefixDetailDTO{}, err
	}
	defer s.writers.leave()
	// Captured once, as for container creation (ADR 0065).
	entry, bookmarkID, _, err := s.boundSnapshot(ctx, scope)
	if err != nil {
		return PrefixDetailDTO{}, err
	}
	resource := container + "/" + prefix
	// Durable facts around the PUT, as for container creation (ADR 0077).
	identity, err := mutationfacts.NewIdentity()
	if err != nil {
		return PrefixDetailDTO{}, err
	}
	if err := s.facts.RecordIntent(ctx, mutationfacts.Intent{
		Identity: identity, BookmarkID: bookmarkID, Operation: "marker.create", Resource: resource,
	}); err != nil {
		return PrefixDetailDTO{}, err
	}
	detail, err := entry.session.CreateMarker(ctx, container, prefix)
	outcome, auditRes, summary := markerCreateFact(err)
	if recErr := s.recordMutationOutcome(ctx, identity, bookmarkID, "marker.create", resource, outcome, auditRes, summary, nil); recErr != nil {
		if outcome == mutationfacts.OutcomeApplied || outcome == mutationfacts.OutcomeUnverified {
			return PrefixDetailDTO{}, fmt.Errorf("%w: identity %s: %w", errMarkerCreateResidue, identity, recErr)
		}
		return PrefixDetailDTO{}, recErr
	}
	if err != nil {
		if outcome == mutationfacts.OutcomeUnverified {
			return PrefixDetailDTO{}, fmt.Errorf("%w: identity %s", err, identity)
		}
		return PrefixDetailDTO{}, err
	}
	return toPrefixDetailDTO(detail), nil
}

func (s *MutateService) containerDetailForBookmark(ctx context.Context, bookmarkID string, d *swiftcontrol.ContainerDetail) ContainerDetailDTO {
	class := ""
	// Classification is display context; a failure here must not fail the
	// mutation result that carries it.
	if classes, err := s.evidence.ClassifyContainers(ctx, bookmarkID, []string{d.Name}); err == nil {
		class = string(classes[d.Name])
	}
	return containerDetailDTO(d, class)
}

// record appends the audit event under whichever connection is active
// when it is written and surfaces the error. The deletions still use it
// and ignore the error (ADR 0058); the creations and the metadata writes
// record their fact and audit in one required transaction.
func (s *MutateService) record(ctx context.Context, operation, resource, summary string, result audit.Result, transIDs []string) error {
	return s.recordFor(ctx, s.connections.ActiveBookmarkID(), operation, resource, summary, result, transIDs)
}

// recordFor appends the audit event attributed to the account the caller
// captured when it admitted the operation.
func (s *MutateService) recordFor(ctx context.Context, bookmarkID, operation, resource, summary string, result audit.Result, transIDs []string) error {
	return appendAudit(ctx, s.audit, audit.Event{
		BookmarkID: bookmarkID, Operation: operation, Resource: resource,
		Summary: summary, Result: result, TransIDs: transIDs,
	})
}

// recordMutationOutcome commits the attempt's outcome and its audit event
// in one transaction, the required record of ADR 0058, 0062 and 0077.
func (s *MutateService) recordMutationOutcome(ctx context.Context, identity, bookmarkID, operation, resource string, outcome mutationfacts.Outcome, result audit.Result, summary string, transIDs []string) error {
	return s.facts.RecordOutcome(ctx, identity, outcome, summary, transIDs, func(tx *sql.Tx) error {
		return s.audit.AppendTx(ctx, tx, audit.Event{
			BookmarkID: bookmarkID, Operation: operation, Resource: resource,
			Summary: summary, Result: result, TransIDs: transIDs,
		})
	})
}

// objectMetadataPosted says whether the write may have reached the store,
// which is what turns a recording failure into residue.
func objectMetadataPosted(result *swiftcontrol.ObjectUpdateResult, err error) bool {
	return errors.Is(err, swiftcontrol.ErrUnverifiedWrite) ||
		(result != nil && (result.Outcome.Posted() || result.Limitation != ""))
}

func objectMetadataFact(result *swiftcontrol.ObjectUpdateResult, err error, edit ObjectMetadataEditDTO) (mutationfacts.Outcome, audit.Result, string, []string) {
	summary := fmt.Sprintf("set %d metadata keys", len(edit.Meta))
	var transIDs []string
	if result != nil {
		transIDs = result.TransIDs
		if result.Limitation != "" {
			summary += "; " + result.Limitation
		}
	}
	if errors.Is(err, swiftcontrol.ErrUnverifiedWrite) || (result != nil && result.Outcome == swiftcontrol.UpdateUnverified) {
		return mutationfacts.OutcomeUnverified, audit.ResultUnverified, "metadata write unverified; " + summary, transIDs
	}
	if result == nil {
		return mutationfacts.OutcomeFailed, audit.ResultFailed, "metadata update failed", nil
	}
	switch result.Outcome {
	case swiftcontrol.UpdateApplied:
		return mutationfacts.OutcomeApplied, audit.ResultApplied, summary, transIDs
	case swiftcontrol.UpdateConflict:
		return mutationfacts.OutcomeConflict, audit.ResultConflict, summary, transIDs
	case swiftcontrol.UpdateVerifyMismatch:
		return mutationfacts.OutcomeVerifyMismatch, audit.ResultVerifyMismatch, summary, transIDs
	}
	if objectMetadataPosted(result, err) {
		return mutationfacts.OutcomeUnverified, audit.ResultUnverified, "metadata write unverified; " + summary, transIDs
	}
	return mutationfacts.OutcomeFailed, audit.ResultFailed, "metadata update failed", transIDs
}

func auditResult(outcome swiftcontrol.UpdateOutcome) audit.Result {
	switch outcome {
	case swiftcontrol.UpdateApplied:
		return audit.ResultApplied
	case swiftcontrol.UpdateConflict:
		return audit.ResultConflict
	case swiftcontrol.UpdateVerifyMismatch:
		return audit.ResultVerifyMismatch
	case swiftcontrol.UpdateUnverified:
		return audit.ResultUnverified
	}
	return audit.ResultFailed
}

// AuditService exposes the §50 trail: reads for the viewer, and the §51
// export that archives the trail to a local file before retention
// compacts it. It never modifies the trail itself.
type AuditService struct {
	audit  *audit.Store
	picker FilePicker
}

// NewAuditService builds the service.
func NewAuditService(auditStore *audit.Store) *AuditService {
	return &AuditService{audit: auditStore}
}

// AuditQueryDTO narrows a §50 viewer read; zero values mean "any".
type AuditQueryDTO struct {
	OperationPrefix string `json:"operationPrefix"`
	Result          string `json:"result"`
	BookmarkID      string `json:"bookmarkId"`
	// BeforeID pages backwards from a previous page's last id.
	BeforeID int64 `json:"beforeId"`
	Limit    int64 `json:"limit"`
}

// Search reads the trail under filters, newest first.
func (s *AuditService) Search(ctx context.Context, q AuditQueryDTO) ([]AuditEventDTO, error) {
	events, err := s.audit.List(ctx, audit.Query{
		OperationPrefix: q.OperationPrefix,
		Result:          q.Result,
		BookmarkID:      q.BookmarkID,
		BeforeID:        q.BeforeID,
		Limit:           q.Limit,
	})
	if err != nil {
		return nil, err
	}
	out := make([]AuditEventDTO, 0, len(events))
	for _, e := range events {
		out = append(out, auditEventDTO(e))
	}
	return out, nil
}

// Recent returns the newest audit events, newest first.
func (s *AuditService) Recent(ctx context.Context, limit int) ([]AuditEventDTO, error) {
	events, err := s.audit.Recent(ctx, limit)
	if err != nil {
		return nil, err
	}
	out := make([]AuditEventDTO, 0, len(events))
	for _, e := range events {
		out = append(out, auditEventDTO(e))
	}
	return out, nil
}

func auditEventDTO(e audit.Event) AuditEventDTO {
	return AuditEventDTO{
		ID:         e.ID,
		At:         e.At.UTC().Format(time.RFC3339),
		BookmarkID: e.BookmarkID,
		Operation:  e.Operation,
		Resource:   e.Resource,
		Summary:    e.Summary,
		Result:     string(e.Result),
		TransIDs:   e.TransIDs,
	}
}

// confirmPublicRead refuses unless the operator confirmed, on the shell's
// native dialog, that name may become readable by anyone. The current ACL
// is read first: one that already grants public read needs no new
// consent. A shell without the dialog refuses, so a headless build never
// makes a container public on the page's word alone.
func (s *MutateService) confirmPublicRead(ctx context.Context, entry cachedSession, name string) error {
	if current, err := entry.session.ContainerDetail(ctx, name); err == nil && grantsPublicRead(current.ReadACL) {
		return nil
	}
	if s.confirmer == nil {
		return fmt.Errorf("making %s publicly readable needs a native confirmation this shell cannot show; nothing was written", name)
	}
	confirmed, err := s.confirmer.ConfirmPublicRead(ctx, name, accountSuffix(entry.session.StorageURL()))
	if err != nil {
		return fmt.Errorf("the native confirmation for making %s public did not complete: %w; nothing was written", name, err)
	}
	if !confirmed {
		return fmt.Errorf("making %s publicly readable was not confirmed; nothing was written", name)
	}
	return nil
}

// grantsPublicRead reports whether a read ACL opens the container to
// the world (.r:* referrer grant, in any comma position).
func grantsPublicRead(acl string) bool {
	for _, part := range strings.Split(acl, ",") {
		if strings.TrimSpace(part) == ".r:*" {
			return true
		}
	}
	return false
}

// errAccountMetadataResidue is returned when the outcome could not be
// recorded after a write whose effect on the store is not known to be
// absent: the POST either applied or was left unverified. The wording
// stays uncertain because accountMetadataPosted treats an unverified POST
// as residue too, so claiming the POST succeeded would overstate an
// unknown effect (§49, ADR 0062).
var errAccountMetadataResidue = errors.New("account metadata write residue: the account may have changed and the outcome could not be recorded")

// UpdateAccountMetadata applies a complete account metadata edit (§53),
// mirroring the container and object metadata editors: a bound snapshot,
// a recorded intent, the verified adapter write, and a recorded outcome.
// The reserved-key and TempURL-secret guards live in the adapter.
func (s *MutateService) UpdateAccountMetadata(ctx context.Context, scope ListingScopeDTO, baseFingerprint string, edit AccountEditDTO) (MutationResultDTO, error) {
	if err := s.occupy(); err != nil {
		return MutationResultDTO{}, err
	}
	defer s.writers.leave()
	entry, bookmarkID, _, err := s.boundSnapshot(ctx, scope)
	if err != nil {
		return MutationResultDTO{}, err
	}
	adapterEdit := swiftcontrol.AccountEdit{Meta: edit.Meta, QuotaBytes: edit.QuotaBytes}

	identity, err := mutationfacts.NewIdentity()
	if err != nil {
		return MutationResultDTO{}, err
	}
	if err := s.facts.RecordIntent(ctx, mutationfacts.Intent{
		Identity:   identity,
		BookmarkID: bookmarkID,
		Operation:  "account.metadata.update",
		Resource:   "account",
	}); err != nil {
		return MutationResultDTO{}, err
	}

	result, err := entry.session.UpdateAccount(ctx, baseFingerprint, adapterEdit)
	outcome, auditRes, summary, transIDs := accountMetadataFact(result, err, edit)
	if recErr := s.recordMutationOutcome(ctx, identity, bookmarkID, "account.metadata.update", "account", outcome, auditRes, summary, transIDs); recErr != nil {
		if accountMetadataPosted(result, err) {
			return MutationResultDTO{}, fmt.Errorf("%w: identity %s: %v", errAccountMetadataResidue, identity, recErr)
		}
		return MutationResultDTO{}, recErr
	}
	if err != nil {
		if errors.Is(err, swiftcontrol.ErrUnverifiedAccountWrite) {
			return MutationResultDTO{}, fmt.Errorf("%w: identity %s", swiftcontrol.ErrUnverifiedAccountWrite, identity)
		}
		return MutationResultDTO{}, err
	}
	dto := MutationResultDTO{
		Outcome:  string(result.Outcome),
		Mismatch: result.Mismatch,
		TransIDs: result.TransIDs,
	}
	if result.After != nil {
		after := accountDTO(result.After, scope, entry.session.StorageURL())
		dto.AccountAfter = &after
	}
	return dto, nil
}

func accountMetadataPosted(result *swiftcontrol.AccountUpdateResult, err error) bool {
	return errors.Is(err, swiftcontrol.ErrUnverifiedAccountWrite) || (result != nil && result.Outcome.Posted())
}

func accountMetadataFact(result *swiftcontrol.AccountUpdateResult, err error, edit AccountEditDTO) (mutationfacts.Outcome, audit.Result, string, []string) {
	summary := fmt.Sprintf("set %d account metadata keys", len(edit.Meta))
	if edit.QuotaBytes > 0 {
		summary += fmt.Sprintf(", quota %d bytes", edit.QuotaBytes)
	}
	var transIDs []string
	if result != nil {
		transIDs = result.TransIDs
	}
	if errors.Is(err, swiftcontrol.ErrUnverifiedAccountWrite) || (result != nil && result.Outcome == swiftcontrol.UpdateUnverified) {
		return mutationfacts.OutcomeUnverified, audit.ResultUnverified, "account write unverified; " + summary, transIDs
	}
	if result == nil {
		return mutationfacts.OutcomeFailed, audit.ResultFailed, "account metadata update failed", nil
	}
	switch result.Outcome {
	case swiftcontrol.UpdateApplied:
		return mutationfacts.OutcomeApplied, audit.ResultApplied, summary, transIDs
	case swiftcontrol.UpdateConflict:
		return mutationfacts.OutcomeConflict, audit.ResultConflict, summary, transIDs
	case swiftcontrol.UpdateVerifyMismatch:
		return mutationfacts.OutcomeVerifyMismatch, audit.ResultVerifyMismatch, summary, transIDs
	}
	// Every posted outcome (applied, verify-mismatch) and the unverified
	// case are returned above, so anything left is a write that did not
	// reach the store.
	return mutationfacts.OutcomeFailed, audit.ResultFailed, "account metadata update failed", transIDs
}
