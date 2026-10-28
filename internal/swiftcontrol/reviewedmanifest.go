package swiftcontrol

import (
	"context"
	"fmt"
)

// ReviewedEntry is one whole-object segment of a static large object as the
// manifest recorded it, the facts a segment job carries from the review to
// the execution (ADR 0095 decision 8): the segment's container and object,
// and the size and entity tag the manifest stated for it.
type ReviewedEntry struct {
	Container string
	Object    string
	Size      int64
	ETag      string
}

// ReviewedManifest is the graph a segment job reviewed and now acts on: the
// manifest's reviewed identity as a content fingerprint, its class, and, for
// a static large object, the exact whole-object entries as resolved, or, for
// a dynamic large object, the canonical namespace the manifest named. The job
// binds this to the live manifest under one credentials snapshot and refuses
// the deletion if the live graph differs, so an equal-tag graph swap between
// the review and the execution cannot widen what is deleted.
type ReviewedManifest struct {
	Class       ObjectClass
	Fingerprint string
	Entries     []ReviewedEntry // for a static large object
	Namespace   string          // "container/prefix/" for a dynamic large object
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

// reviewedGraph is the binding deleteShownBound checks under the snapshot: a
// static manifest's reviewed whole-object entries, read and compared
// immediately before the comparison HEAD, or a dynamic manifest's reviewed
// namespace, compared against the one the comparison HEAD itself reports.
type reviewedGraph struct {
	slo       []ReviewedEntry
	isStatic  bool
	namespace string
}

// DeleteReviewedManifest deletes ONE manifest bound to the graph the operator
// reviewed (ADR 0095 decision 8). It is the manifest-only protocol of ADR
// 0094 decision 3 with the reviewed identity as the comparison and a graph
// check added under the same credentials snapshot: a static manifest's
// entries, read immediately before the comparison HEAD, must still equal the
// reviewed membership, and a dynamic manifest's namespace, which the HEAD
// reports, must equal the reviewed one. Any difference refuses the manifest
// as changed since reviewed with nothing deleted. The segments are never
// touched here; the job deletes them only after this records a deleted
// manifest.
func (s *Session) DeleteReviewedManifest(ctx context.Context, container, name string, reviewed ReviewedManifest) (*ObjectDeleteResult, error) {
	if container == "" || name == "" {
		return nil, fmt.Errorf("deleting a reviewed manifest needs both its container and its name")
	}
	if reviewed.Fingerprint == "" {
		return nil, fmt.Errorf("deleting %s/%s needs the manifest identity the operator reviewed", container, name)
	}
	graph := &reviewedGraph{}
	switch reviewed.Class {
	case ClassSLO:
		graph.isStatic = true
		graph.slo = reviewed.Entries
	case ClassDLO:
		if reviewed.Namespace == "" {
			return nil, fmt.Errorf("deleting dynamic manifest %s/%s needs the reviewed namespace", container, name)
		}
		graph.namespace = reviewed.Namespace
	default:
		return nil, fmt.Errorf("%s/%s is not a manifest; the reviewed-manifest delete refuses it", container, name)
	}
	return s.deleteShownBound(ctx, container, name, reviewed.Fingerprint, targetManifest, graph)
}

// sameEntries reports whether a live static graph's whole-object entries equal
// the reviewed ones exactly, in order: a swap that keeps the aggregate entity
// tag still changes a segment's name, size or tag, so an ordered exact match
// is what binds the reviewed set to the one the manifest now points at.
func sameEntries(reviewed, live []ReviewedEntry) bool {
	if len(reviewed) != len(live) {
		return false
	}
	for i := range reviewed {
		if reviewed[i] != live[i] {
			return false
		}
	}
	return true
}

// differs reports whether the live manifest's graph has moved from the one
// the operator reviewed, and the expected and found descriptions for the
// refusal's record. For a static manifest the live whole-object entries must
// still equal the reviewed ones; a graph that is no longer whole-object has
// changed by definition. For a dynamic manifest the namespace the comparison
// HEAD reports must equal the reviewed one.
func (g *reviewedGraph) differs(before *ObjectDetail, liveEntries []ReviewedEntry, liveWhole bool) (bool, string, string) {
	if g.isStatic {
		if !liveWhole {
			return true, fmt.Sprintf("reviewed graph of %d segments", len(g.slo)), "a graph that is no longer whole-object"
		}
		if !sameEntries(g.slo, liveEntries) {
			return true, fmt.Sprintf("reviewed graph of %d segments", len(g.slo)), fmt.Sprintf("%d segments, changed since reviewed", len(liveEntries))
		}
		return false, "", ""
	}
	live := canonicalNamespace(before.Manifest)
	if live != g.namespace {
		return true, "reviewed namespace " + g.namespace, "namespace " + live
	}
	return false, "", ""
}

// canonicalNamespace turns a raw X-Object-Manifest value ("container/prefix")
// into the namespace a segment sweep bounds ("container/prefix/"), matching
// what the review recorded, so a header with or without a trailing slash
// compares equal.
func canonicalNamespace(manifest string) string {
	slash := indexByte(manifest, '/')
	if slash <= 0 {
		return manifest
	}
	container, prefix := manifest[:slash], manifest[slash+1:]
	if prefix == "" {
		return manifest
	}
	if prefix[len(prefix)-1] != '/' {
		prefix += "/"
	}
	return container + "/" + prefix
}

func indexByte(s string, b byte) int {
	for i := 0; i < len(s); i++ {
		if s[i] == b {
			return i
		}
	}
	return -1
}
