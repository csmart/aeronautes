package swiftcontrol

import (
	"context"
	"fmt"
)

// DLOSegmentPreview is a bounded first page of a DLO's segments (§23.1):
// what the manifest would assemble, in name order, capped for the
// inspector — never "all segments" (§15 bounded-memory rule; a DLO
// prefix can hold millions of objects).
type DLOSegmentPreview struct {
	// Container/Prefix are the manifest's split segment location.
	Container string
	Prefix    string
	Segments  []ObjectEntry
	// HasMore reports that segments beyond the preview exist.
	HasMore bool
	// PreviewBytes sums the previewed segments only — a floor, not the
	// logical size (the HEAD's Content-Length is the authoritative total).
	PreviewBytes int64
}

// DLOSegmentPreview lists the first limit segments under a DLO manifest
// value ("container/prefix"). The listing is DEEP: a DLO concatenates
// every object under the prefix in name order, delimiter grouping does
// not apply (§23.1).
func (s *Session) DLOSegmentPreview(ctx context.Context, manifest string, limit int) (*DLOSegmentPreview, error) {
	container, prefix, ok := DynamicSegments(manifest)
	if !ok {
		return nil, fmt.Errorf("manifest %q has no container/prefix split", manifest)
	}
	if limit <= 0 || limit > 500 {
		limit = 50
	}

	preview := &DLOSegmentPreview{Container: container, Prefix: prefix}
	for entry, err := range s.Objects(ctx, container, ObjectListOptions{
		Prefix:   prefix,
		Deep:     true,
		PageSize: limit + 1,
	}) {
		if err != nil {
			return nil, err
		}
		segment, ok := entry.(ObjectEntry)
		if !ok {
			continue
		}
		if len(preview.Segments) == limit {
			preview.HasMore = true
			break
		}
		preview.Segments = append(preview.Segments, segment)
		preview.PreviewBytes += segment.Bytes
	}
	return preview, nil
}
