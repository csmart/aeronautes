package swiftcontrol

// ReviewedEntry is one whole-object segment of a static large object as the
// manifest recorded it: the segment's container and object, and the size and
// entity tag the manifest stated for it. They are what a deletion that takes
// segments reads a manifest for, once (ADR 0122 decision 6).
type ReviewedEntry struct {
	Container string
	Object    string
	Size      int64
	ETag      string
}

// WholeObjectEntries flattens a static manifest to its whole-object entries
// and reports whether the whole graph is whole-object. A nested sub-manifest,
// a ranged entry, inline data or an unrecognised entry makes it not
// whole-object: the reviewed membership a segment job acts on is the
// whole-object set, and a graph that is not purely whole-object is not one
// this job resolves (§28.6(b)).
func (m *SLOManifest) WholeObjectEntries() ([]ReviewedEntry, bool) {
	entries := make([]ReviewedEntry, 0, len(m.Entries))
	for _, raw := range m.Entries {
		segment, ok := raw.(ObjectSegment)
		if !ok || segment.NestedSLO || segment.Range != nil || segment.ETag == nil {
			return nil, false
		}
		entries = append(entries, ReviewedEntry{
			Container: segment.Path.Container, Object: segment.Path.Object,
			Size: int64(segment.Size), ETag: *segment.ETag,
		})
	}
	return entries, true
}
