package app

import (
	"context"
	"time"

	"github.com/csmart/aeronautes/internal/swiftcontrol"
)

// InspectorService serves selection-triggered detail (§14.2, §53): the
// authoritative HEAD, fetched only when something is selected.
type InspectorService struct {
	connections *Connections
}

// NewInspectorService builds the service over the connection registry.
func NewInspectorService(connections *Connections) *InspectorService {
	return &InspectorService{connections: connections}
}

// Object HEADs one object and returns its inspector detail.
func (s *InspectorService) Object(ctx context.Context, container, name string) (ObjectDetailDTO, error) {
	session, err := s.connections.Session()
	if err != nil {
		return ObjectDetailDTO{}, err
	}
	d, err := session.ObjectDetail(ctx, container, name)
	if err != nil {
		return ObjectDetailDTO{}, err
	}
	return toObjectDetailDTO(d), nil
}

// Prefix classifies a prefix on selection (§14.1): marker-backed vs
// implied, surfacing any folder-ish real object at the name.
func (s *InspectorService) Prefix(ctx context.Context, container, prefix string) (PrefixDetailDTO, error) {
	session, err := s.connections.Session()
	if err != nil {
		return PrefixDetailDTO{}, err
	}
	d, err := session.PrefixDetail(ctx, container, prefix)
	if err != nil {
		return PrefixDetailDTO{}, err
	}
	dto := PrefixDetailDTO{Prefix: d.Prefix, MarkerBacked: d.MarkerBacked}
	if d.SelfObject != nil {
		self := toObjectDetailDTO(d.SelfObject)
		dto.SelfObject = &self
	}
	return dto, nil
}

func toObjectDetailDTO(d *swiftcontrol.ObjectDetail) ObjectDetailDTO {
	dto := ObjectDetailDTO{
		Name:          d.Name,
		Class:         string(d.Class),
		ContentLength: d.ContentLength,
		ContentType:   d.ContentType,
		ETag:          d.ETag,
		LastModified:  d.LastModified.UTC().Format(time.RFC3339),
		Manifest:      d.Manifest,
		Meta:          d.Meta,
		RawHeaders:    d.RawHeaders,
		TransID:       d.TransID,
	}
	if !d.DeleteAt.IsZero() {
		dto.DeleteAt = d.DeleteAt.UTC().Format(time.RFC3339)
	}
	return dto
}
