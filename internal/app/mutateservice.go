package app

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/csmart/aeronautes/internal/audit"
	"github.com/csmart/aeronautes/internal/evidence"
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
}

// NewMutateService builds the service.
func NewMutateService(connections *Connections, auditStore *audit.Store, evidenceStore *evidence.Store) *MutateService {
	return &MutateService{connections: connections, audit: auditStore, evidence: evidenceStore}
}

// UpdateObjectMetadata applies a complete metadata working copy to one
// object (§17, §18).
func (s *MutateService) UpdateObjectMetadata(ctx context.Context, container, name, baseFingerprint string, edit ObjectMetadataEditDTO) (MutationResultDTO, error) {
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
	result, err := session.UpdateObjectMetadata(ctx, container, name, baseFingerprint, adapterEdit)
	if err != nil {
		s.record(ctx, "object.metadata.update", resource,
			"metadata update failed", audit.ResultFailed, nil)
		return MutationResultDTO{}, err
	}

	dto := MutationResultDTO{
		Outcome:  string(result.Outcome),
		Mismatch: result.Mismatch,
		TransIDs: result.TransIDs,
	}
	if result.After != nil {
		after := toObjectDetailDTO(result.After)
		dto.ObjectAfter = &after
	}
	s.record(ctx, "object.metadata.update", resource,
		fmt.Sprintf("set %d metadata keys", len(edit.Meta)),
		auditResult(result.Outcome), result.TransIDs)
	return dto, nil
}

// UpdateContainer applies a complete container edit (§18, §19, §21).
func (s *MutateService) UpdateContainer(ctx context.Context, name, baseFingerprint string, edit ContainerEditDTO) (MutationResultDTO, error) {
	session, err := s.connections.Session()
	if err != nil {
		return MutationResultDTO{}, err
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
	if caps := s.connections.Capabilities(); caps != nil &&
		!caps.AllowsVersioningMode(adapterEdit.Versioning.Mode) {
		return MutationResultDTO{}, fmt.Errorf(
			"cluster does not advertise %s in versioned_writes allowed_flags", adapterEdit.Versioning.Mode)
	}

	result, err := session.UpdateContainer(ctx, name, baseFingerprint, adapterEdit)
	if err != nil {
		s.record(ctx, "container.update", name, "container update failed", audit.ResultFailed, nil)
		return MutationResultDTO{}, err
	}
	dto := MutationResultDTO{
		Outcome:  string(result.Outcome),
		Mismatch: result.Mismatch,
		TransIDs: result.TransIDs,
	}
	if result.After != nil {
		after := s.toContainerDetailDTO(ctx, result.After)
		dto.ContainerAfter = &after
	}
	s.record(ctx, "container.update", name,
		fmt.Sprintf("set %d metadata keys, versioning %s", len(edit.Meta), adapterEdit.Versioning.Mode),
		auditResult(result.Outcome), result.TransIDs)
	return dto, nil
}

// CreateContainer creates a container (§20), gating the policy on the
// advertised list when it is known.
func (s *MutateService) CreateContainer(ctx context.Context, create ContainerCreateDTO) (ContainerDetailDTO, error) {
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
		s.record(ctx, "container.create", create.Name, "container create failed", audit.ResultFailed, nil)
		return ContainerDetailDTO{}, err
	}
	s.record(ctx, "container.create", create.Name,
		fmt.Sprintf("created with policy %q", detail.StoragePolicy),
		audit.ResultApplied, []string{detail.TransID})
	return s.toContainerDetailDTO(ctx, detail), nil
}

// CreateMarker creates the explicit §14.1 marker object at a prefix.
func (s *MutateService) CreateMarker(ctx context.Context, container, prefix string) (PrefixDetailDTO, error) {
	session, err := s.connections.Session()
	if err != nil {
		return PrefixDetailDTO{}, err
	}
	resource := container + "/" + prefix
	detail, err := session.CreateMarker(ctx, container, prefix)
	if err != nil {
		s.record(ctx, "marker.create", resource, "marker create failed or refused", audit.ResultFailed, nil)
		return PrefixDetailDTO{}, err
	}
	s.record(ctx, "marker.create", resource, "created directory marker", audit.ResultApplied, nil)
	return toPrefixDetailDTO(detail), nil
}

// DeleteMarker deletes ONLY a verified directory marker (§14.1) — the
// one Phase 3 delete.
func (s *MutateService) DeleteMarker(ctx context.Context, container, prefix string) error {
	session, err := s.connections.Session()
	if err != nil {
		return err
	}
	resource := container + "/" + prefix
	if err := session.DeleteMarker(ctx, container, prefix); err != nil {
		s.record(ctx, "marker.delete", resource, "marker delete failed or refused", audit.ResultFailed, nil)
		return err
	}
	s.record(ctx, "marker.delete", resource, "deleted directory marker (children untouched)", audit.ResultApplied, nil)
	return nil
}

func (s *MutateService) toContainerDetailDTO(ctx context.Context, d *swiftcontrol.ContainerDetail) ContainerDetailDTO {
	class := ""
	// Classification is display context; a failure here must not fail the
	// mutation result that carries it.
	if classes, err := s.evidence.ClassifyContainers(ctx, s.connections.ActiveBookmarkID(), []string{d.Name}); err == nil {
		class = string(classes[d.Name])
	}
	return containerDetailDTO(d, class)
}

// record appends the audit event; a mutation that cannot be accounted
// for is loud in the log, but the mutation's own result is preserved.
func (s *MutateService) record(ctx context.Context, operation, resource, summary string, result audit.Result, transIDs []string) {
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
}

func auditResult(outcome swiftcontrol.UpdateOutcome) audit.Result {
	switch outcome {
	case swiftcontrol.UpdateApplied:
		return audit.ResultApplied
	case swiftcontrol.UpdateConflict:
		return audit.ResultConflict
	case swiftcontrol.UpdateVerifyMismatch:
		return audit.ResultVerifyMismatch
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

// Recent returns the newest audit events, newest first.
func (s *AuditService) Recent(ctx context.Context, limit int) ([]AuditEventDTO, error) {
	events, err := s.audit.Recent(ctx, limit)
	if err != nil {
		return nil, err
	}
	out := make([]AuditEventDTO, 0, len(events))
	for _, e := range events {
		out = append(out, AuditEventDTO{
			ID:        e.ID,
			At:        e.At.UTC().Format(time.RFC3339),
			Operation: e.Operation,
			Resource:  e.Resource,
			Summary:   e.Summary,
			Result:    string(e.Result),
			TransIDs:  e.TransIDs,
		})
	}
	return out, nil
}

// DeleteManifest removes ONLY a DLO/SLO manifest object (§60 Phase 4's
// low-risk delete): segments survive by construction, a plain object
// refuses, and the outcome is audited either way.
func (s *MutateService) DeleteManifest(ctx context.Context, container, name string) (ObjectDetailDTO, error) {
	session, err := s.connections.Session()
	if err != nil {
		return ObjectDetailDTO{}, err
	}
	resource := container + "/" + name
	before, err := session.DeleteManifestObject(ctx, container, name)
	if err != nil {
		s.record(ctx, "manifest.delete", resource, "manifest delete failed or refused", audit.ResultFailed, nil)
		return ObjectDetailDTO{}, err
	}
	summary := fmt.Sprintf("deleted %s manifest; segments remain", before.Class)
	if before.Manifest != "" {
		summary = fmt.Sprintf("deleted %s manifest; segments remain at %s", before.Class, before.Manifest)
	}
	s.record(ctx, "manifest.delete", resource, summary, audit.ResultApplied, []string{before.TransID})
	return toObjectDetailDTO(before), nil
}
