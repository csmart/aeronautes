package app

import (
	"context"
	"errors"
	"strconv"
	"time"

	"github.com/csmart/aeronautes/internal/applog"
	"github.com/csmart/aeronautes/internal/evidence"
	"github.com/csmart/aeronautes/internal/swiftcontrol"
)

// InspectorService serves selection-triggered detail (§14.2, §53): the
// authoritative HEAD, fetched only when something is selected. Every
// manifest reference it observes is recorded as segment evidence (§24)
// so the container modes learn from ordinary inspection.
type InspectorService struct {
	connections *Connections
	evidence    *evidence.Store
}

// NewInspectorService builds the service over the connection registry
// and the evidence store.
func NewInspectorService(connections *Connections, ev *evidence.Store) *InspectorService {
	return &InspectorService{connections: connections, evidence: ev}
}

// Object HEADs one object and returns its inspector detail; a DLO's
// manifest reference becomes Confirmed evidence.
func (s *InspectorService) Object(ctx context.Context, bookmarkID, container, name string) (ObjectDetailDTO, error) {
	if container == "" || name == "" {
		// A HEAD of "container/" is answered by the container, and an object
		// detail built from it would carry an identity a destructive caller
		// could hand back; neither part may be empty.
		return ObjectDetailDTO{}, errors.New("an object read needs both its container and its name")
	}
	session, scope, err := s.connections.scopedSession(ctx, bookmarkID)
	if err != nil {
		return ObjectDetailDTO{}, err
	}
	d, err := session.ObjectDetail(ctx, container, name)
	if err != nil {
		return ObjectDetailDTO{}, err
	}
	s.record(ctx, scope.BookmarkID, evidence.FromObjectDetail(d))
	dto := toObjectDetailDTO(d)
	dto.Scope = scope
	return dto, nil
}

// Prefix classifies a prefix on selection (§14.1): marker-backed vs
// implied, surfacing any folder-ish real object at the name.
func (s *InspectorService) Prefix(ctx context.Context, bookmarkID, container, prefix string) (PrefixDetailDTO, error) {
	session, scope, err := s.connections.scopedSession(ctx, bookmarkID)
	if err != nil {
		return PrefixDetailDTO{}, err
	}
	d, err := session.PrefixDetail(ctx, container, prefix)
	if err != nil {
		return PrefixDetailDTO{}, err
	}
	dto := toPrefixDetailDTO(d)
	dto.Scope = scope
	return dto, nil
}

// Container HEADs one container for the §53 inspector section, folding
// in its §24 classification.
func (s *InspectorService) Container(ctx context.Context, bookmarkID, name string) (ContainerDetailDTO, error) {
	session, scope, err := s.connections.scopedSession(ctx, bookmarkID)
	if err != nil {
		return ContainerDetailDTO{}, err
	}
	d, err := session.ContainerDetail(ctx, name)
	if err != nil {
		return ContainerDetailDTO{}, err
	}
	classes, err := s.evidence.ClassifyContainers(ctx, scope.BookmarkID, []string{name})
	if err != nil {
		return ContainerDetailDTO{}, err
	}
	dto := containerDetailDTO(d, string(classes[name]))
	dto.Scope = scope
	dto.Settings = containerSettings(d, scope.Generation, time.Now())
	s.connections.settings.remember(scope.BookmarkID, scope.Generation, name, dto.Settings)
	return dto, nil
}

// SLOManifest fetches the exact manifest for the inspector's segment
// table (§23.2); every referenced location becomes Confirmed evidence.
func (s *InspectorService) SLOManifest(ctx context.Context, bookmarkID, container, name string) (SLOManifestDTO, error) {
	session, scope, err := s.connections.scopedSession(ctx, bookmarkID)
	if err != nil {
		return SLOManifestDTO{}, err
	}
	m, err := session.SLOManifest(ctx, container, name)
	if err != nil {
		return SLOManifestDTO{}, err
	}
	s.record(ctx, scope.BookmarkID, evidence.FromSLOManifest(container+"/"+name, m))

	dto := SLOManifestDTO{Entries: make([]SLOSegmentDTO, 0, len(m.Entries))}
	for _, entry := range m.Entries {
		switch v := entry.(type) {
		case swiftcontrol.ObjectSegment:
			row := SLOSegmentDTO{
				Kind:      "object",
				Container: v.Path.Container,
				Object:    v.Path.Object,
				Size:      int64(v.Size),
				NestedSLO: v.NestedSLO,
			}
			if v.ETag != nil {
				row.ETag = *v.ETag
			}
			if v.Range != nil {
				row.Range = strconv.FormatInt(v.Range.Start, 10) + "-" + strconv.FormatInt(v.Range.End, 10)
			}
			dto.Entries = append(dto.Entries, row)
		case swiftcontrol.InlineDataSegment:
			dto.Entries = append(dto.Entries, SLOSegmentDTO{Kind: "data", DataSize: int64(len(v.Data))})
		default:
			dto.Entries = append(dto.Entries, SLOSegmentDTO{Kind: "unknown"})
		}
	}
	dto.Scope = scope
	return dto, nil
}

// record stores observations against the named bookmark; evidence is a
// side effect of inspection and must never fail the inspection itself.
func (s *InspectorService) record(ctx context.Context, bookmarkID string, observations []evidence.Observation) {
	if bookmarkID == "" {
		return
	}
	for _, obs := range observations {
		if err := s.evidence.Record(ctx, bookmarkID, obs); err != nil {
			// Diagnostic only: the HEAD/manifest the user asked for
			// succeeded; a bookkeeping failure must not mask it.
			applog.Warnf("evidence record failed: %v", err)
			return
		}
	}
}

func toPrefixDetailDTO(d *swiftcontrol.PrefixDetail) PrefixDetailDTO {
	dto := PrefixDetailDTO{Prefix: d.Prefix, MarkerBacked: d.MarkerBacked}
	if d.SelfObject != nil {
		self := toObjectDetailDTO(d.SelfObject)
		dto.SelfObject = &self
	}
	return dto
}

func containerDetailDTO(d *swiftcontrol.ContainerDetail, class string) ContainerDetailDTO {
	return ContainerDetailDTO{
		Name:                d.Name,
		ObjectCount:         d.ObjectCount,
		BytesUsed:           d.BytesUsed,
		StoragePolicy:       d.StoragePolicy,
		ReadACL:             d.ReadACL,
		WriteACL:            d.WriteACL,
		VersioningMode:      string(d.Versioning.Mode),
		VersioningArchive:   d.Versioning.Archive,
		QuotaBytes:          d.QuotaBytes,
		QuotaCount:          d.QuotaCount,
		CORS:                d.CORS,
		StaticWeb:           d.StaticWeb,
		TempURLKeySlots:     d.TempURLKeySlots,
		Meta:                d.Meta,
		RawHeaders:          d.RawHeaders,
		TransID:             d.TransID,
		EditableFingerprint: d.EditableFingerprint,
		Class:               class,
	}
}

func toObjectDetailDTO(d *swiftcontrol.ObjectDetail) ObjectDetailDTO {
	dto := ObjectDetailDTO{
		Name:                d.Name,
		Class:               string(d.Class),
		ContentLength:       d.ContentLength,
		ContentType:         d.ContentType,
		ETag:                d.ETag,
		LastModified:        d.LastModified.UTC().Format(time.RFC3339),
		Manifest:            d.Manifest,
		FollowedSymlink:     d.FollowedSymlink,
		SymlinkTarget:       d.SymlinkTarget,
		Meta:                d.Meta,
		RawHeaders:          d.RawHeaders,
		TransID:             d.TransID,
		EditableFingerprint: d.EditableFingerprint,
		ContentFingerprint:  d.ContentFingerprint(),
	}
	if !d.DeleteAt.IsZero() {
		dto.DeleteAt = d.DeleteAt.UTC().Format(time.RFC3339)
	}
	return dto
}

// DLOSegments returns the bounded §23.1 segment preview for a DLO
// manifest value ("container/prefix").
func (s *InspectorService) DLOSegments(ctx context.Context, bookmarkID, manifest string) (DLOSegmentsDTO, error) {
	session, scope, err := s.connections.scopedSession(ctx, bookmarkID)
	if err != nil {
		return DLOSegmentsDTO{}, err
	}
	preview, err := session.DLOSegmentPreview(ctx, manifest, 50)
	if err != nil {
		return DLOSegmentsDTO{}, err
	}
	dto := DLOSegmentsDTO{
		Container:    preview.Container,
		Prefix:       preview.Prefix,
		Segments:     make([]ListingRowDTO, 0, len(preview.Segments)),
		HasMore:      preview.HasMore,
		PreviewBytes: preview.PreviewBytes,
	}
	for _, segment := range preview.Segments {
		dto.Segments = append(dto.Segments, ListingRowDTO{
			Kind:         "object",
			Name:         segment.Name,
			Bytes:        segment.Bytes,
			ETag:         segment.ETag,
			LastModified: segment.LastModified.UTC().Format(time.RFC3339),
			ContentType:  segment.ContentType,
			SLOListed:    segment.SLOListed,
		})
	}
	dto.Scope = scope
	return dto, nil
}
