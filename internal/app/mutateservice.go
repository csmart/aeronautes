package app

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
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
func (s *MutateService) UpdateObjectMetadata(ctx context.Context, container, name, baseFingerprint string, edit ObjectMetadataEditDTO) (MutationResultDTO, error) {
	if err := s.occupy(); err != nil {
		return MutationResultDTO{}, err
	}
	defer s.writers.leave()
	session, err := s.connections.Session()
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
		BookmarkID: s.connections.ActiveBookmarkID(),
		Operation:  "object.metadata.update",
		Resource:   resource,
	}); err != nil {
		return MutationResultDTO{}, err
	}

	result, err := session.UpdateObjectMetadata(ctx, container, name, baseFingerprint, adapterEdit)
	outcome, auditRes, summary, transIDs := objectMetadataFact(result, err, edit)
	if recErr := s.recordObjectOutcome(ctx, identity, resource, outcome, auditRes, summary, transIDs); recErr != nil {
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

// UpdateContainer applies a complete container edit (§18, §19, §21).
func (s *MutateService) UpdateContainer(ctx context.Context, name, baseFingerprint string, edit ContainerEditDTO) (MutationResultDTO, error) {
	if err := s.occupy(); err != nil {
		return MutationResultDTO{}, err
	}
	defer s.writers.leave()
	entry, bookmarkID, caps, err := s.connections.activeSnapshot()
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
	if recErr := s.recordContainerOutcome(ctx, identity, bookmarkID, name, outcome, auditRes, summary, transIDs); recErr != nil {
		if outcome == mutationfacts.OutcomeApplied || outcome == mutationfacts.OutcomeVerifyMismatch || outcome == mutationfacts.OutcomeUnverified {
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
		dto.ContainerAfter = &after
	}
	return dto, nil
}

// CreateContainer creates a container (§20), gating the policy on the
// advertised list when it is known.
func (s *MutateService) CreateContainer(ctx context.Context, create ContainerCreateDTO) (ContainerDetailDTO, error) {
	if err := s.occupy(); err != nil {
		return ContainerDetailDTO{}, err
	}
	defer s.writers.leave()
	session, err := s.connections.Session()
	if err != nil {
		return ContainerDetailDTO{}, err
	}
	if caps := s.connections.Capabilities(); caps != nil && !caps.AllowsStoragePolicy(create.Policy) {
		return ContainerDetailDTO{}, fmt.Errorf("cluster does not advertise storage policy %q", create.Policy)
	}

	detail, err := session.CreateContainer(ctx, swiftcontrol.ContainerCreateRequest{
		Name: create.Name, Policy: create.Policy, Meta: create.Meta,
	})
	if err != nil {
		_ = s.record(ctx, "container.create", create.Name, "container create failed", audit.ResultFailed, nil)
		return ContainerDetailDTO{}, err
	}
	_ = s.record(ctx, "container.create", create.Name,
		fmt.Sprintf("created with policy %q", detail.StoragePolicy),
		audit.ResultApplied, []string{detail.TransID})
	return s.toContainerDetailDTO(ctx, detail), nil
}

// CreateMarker creates the explicit §14.1 marker object at a prefix.
func (s *MutateService) CreateMarker(ctx context.Context, container, prefix string) (PrefixDetailDTO, error) {
	if err := refuseIfDeferred("MutateService.CreateMarker"); err != nil {
		return PrefixDetailDTO{}, err
	}
	if err := s.occupy(); err != nil {
		return PrefixDetailDTO{}, err
	}
	defer s.writers.leave()
	session, err := s.connections.Session()
	if err != nil {
		return PrefixDetailDTO{}, err
	}
	resource := container + "/" + prefix
	detail, err := session.CreateMarker(ctx, container, prefix)
	if err != nil {
		_ = s.record(ctx, "marker.create", resource, "marker create failed or refused", audit.ResultFailed, nil)
		return PrefixDetailDTO{}, err
	}
	_ = s.record(ctx, "marker.create", resource, "created directory marker", audit.ResultApplied, nil)
	return toPrefixDetailDTO(detail), nil
}

// DeleteMarker deletes ONLY a verified directory marker (§14.1) — the
// one Phase 3 delete.
func (s *MutateService) DeleteMarker(ctx context.Context, container, prefix string) error {
	if err := refuseIfDeferred("MutateService.DeleteMarker"); err != nil {
		return err
	}
	if err := s.occupy(); err != nil {
		return err
	}
	defer s.writers.leave()
	session, err := s.connections.Session()
	if err != nil {
		return err
	}
	resource := container + "/" + prefix
	if err := session.DeleteMarker(ctx, container, prefix); err != nil {
		_ = s.record(ctx, "marker.delete", resource, "marker delete failed or refused", audit.ResultFailed, nil)
		return err
	}
	_ = s.record(ctx, "marker.delete", resource, "deleted directory marker (children untouched)", audit.ResultApplied, nil)
	return nil
}

func (s *MutateService) toContainerDetailDTO(ctx context.Context, d *swiftcontrol.ContainerDetail) ContainerDetailDTO {
	return s.containerDetailForBookmark(ctx, s.connections.ActiveBookmarkID(), d)
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

// record appends the audit event and surfaces the error. Create, marker
// and manifest still ignore it (ADR 0058). Object and container metadata
// record their fact and audit in one required transaction.
func (s *MutateService) record(ctx context.Context, operation, resource, summary string, result audit.Result, transIDs []string) error {
	err := s.audit.Append(ctx, audit.Event{
		BookmarkID: s.connections.ActiveBookmarkID(),
		Operation:  operation,
		Resource:   resource,
		Summary:    summary,
		Result:     result,
		TransIDs:   transIDs,
	})
	if err != nil {
		log.Printf("AUDIT WRITE FAILED for %s %s: %v", operation, resource, err)
	}
	return err
}

func (s *MutateService) recordObjectOutcome(ctx context.Context, identity, resource string, outcome mutationfacts.Outcome, result audit.Result, summary string, transIDs []string) error {
	return s.facts.RecordOutcome(ctx, identity, outcome, summary, transIDs, func(tx *sql.Tx) error {
		return s.audit.AppendTx(ctx, tx, audit.Event{
			BookmarkID: s.connections.ActiveBookmarkID(),
			Operation:  "object.metadata.update",
			Resource:   resource,
			Summary:    summary,
			Result:     result,
			TransIDs:   transIDs,
		})
	})
}

func objectMetadataPosted(result *swiftcontrol.ObjectUpdateResult, err error) bool {
	if errors.Is(err, swiftcontrol.ErrUnverifiedWrite) {
		return true
	}
	if result == nil {
		return false
	}
	switch result.Outcome {
	case swiftcontrol.UpdateApplied, swiftcontrol.UpdateVerifyMismatch, swiftcontrol.UpdateUnverified:
		return true
	}
	return result.Limitation != ""
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

// AuditService exposes the §50 trail read-only.
type AuditService struct {
	audit *audit.Store
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
		ID:        e.ID,
		At:        e.At.UTC().Format(time.RFC3339),
		Operation: e.Operation,
		Resource:  e.Resource,
		Summary:   e.Summary,
		Result:    string(e.Result),
		TransIDs:  e.TransIDs,
	}
}

// DeleteManifest removes ONLY a DLO/SLO manifest object (§60 Phase 4's
// low-risk delete): segments survive by construction, a plain object
// refuses, and the outcome is audited either way.
func (s *MutateService) DeleteManifest(ctx context.Context, container, name string) (ObjectDetailDTO, error) {
	if err := refuseIfDeferred("MutateService.DeleteManifest"); err != nil {
		return ObjectDetailDTO{}, err
	}
	if err := s.occupy(); err != nil {
		return ObjectDetailDTO{}, err
	}
	defer s.writers.leave()
	session, err := s.connections.Session()
	if err != nil {
		return ObjectDetailDTO{}, err
	}
	resource := container + "/" + name
	before, err := session.DeleteManifestObject(ctx, container, name)
	if err != nil {
		_ = s.record(ctx, "manifest.delete", resource, "manifest delete failed or refused", audit.ResultFailed, nil)
		return ObjectDetailDTO{}, err
	}
	summary := fmt.Sprintf("deleted %s manifest; segments remain", before.Class)
	if before.Manifest != "" {
		summary = fmt.Sprintf("deleted %s manifest; segments remain at %s", before.Class, before.Manifest)
	}
	_ = s.record(ctx, "manifest.delete", resource, summary, audit.ResultApplied, []string{before.TransID})
	return toObjectDetailDTO(before), nil
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
