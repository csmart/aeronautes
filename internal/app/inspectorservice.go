package app

import (
	"context"
	"time"
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
	return dto, nil
}
