package oracle

import (
	"fmt"
	"reflect"
	"sort"
	"strings"
)

// Field names one changed attribute of an object or container.
type Field string

// ObjectChange identifies one object that was added, removed, or changed,
// as a TYPED tuple — never a formatted string. A policy allowlist matches
// on the exact (Container, Object) identity, so an object whose name
// contains the display separator ": " cannot masquerade as a different,
// allowed object (codex war-room F-01).
type ObjectChange struct {
	Container string
	Object    string
	Fields    []Field // nil for a pure add/remove; the changed attributes otherwise
}

// ContainerChange identifies one container whose configuration changed.
// Added and removed containers are plain names on the Delta.
type ContainerChange struct {
	Container string
	Fields    []Field
}

// Delta describes what changed between two account snapshots, as typed
// records so identity is never recovered by parsing a formatted string.
type Delta struct {
	AddedContainers   []string
	RemovedContainers []string
	AddedObjects      []ObjectChange // Fields nil
	RemovedObjects    []ObjectChange // Fields nil
	ChangedObjects    []ObjectChange
	ChangedContainers []ContainerChange
}

// Empty reports whether nothing changed at all.
func (d Delta) Empty() bool {
	return len(d.AddedContainers) == 0 && len(d.RemovedContainers) == 0 &&
		len(d.AddedObjects) == 0 && len(d.RemovedObjects) == 0 &&
		len(d.ChangedObjects) == 0 && len(d.ChangedContainers) == 0
}

// Ref is the canonical "container/object" identity, built from the typed
// fields (container names never contain "/", so this is unambiguous for
// matching, and it is CONSTRUCTED, never parsed back).
func (o ObjectChange) Ref() string { return o.Container + "/" + o.Object }

func fieldsString(fs []Field) string {
	parts := make([]string, len(fs))
	for i, f := range fs {
		parts[i] = string(f)
	}
	sort.Strings(parts)
	return strings.Join(parts, ",")
}

// String renders the delta for HUMAN DISPLAY only — after policy has been
// evaluated on the typed records, never before.
func (d Delta) String() string {
	var b strings.Builder
	names := func(label string, xs []string) {
		if len(xs) > 0 {
			sort.Strings(xs)
			fmt.Fprintf(&b, "  %s: %s\n", label, strings.Join(xs, ", "))
		}
	}
	objs := func(label string, xs []ObjectChange) {
		if len(xs) == 0 {
			return
		}
		var lines []string
		for _, x := range xs {
			if len(x.Fields) > 0 {
				lines = append(lines, x.Ref()+" ["+fieldsString(x.Fields)+"]")
			} else {
				lines = append(lines, x.Ref())
			}
		}
		sort.Strings(lines)
		fmt.Fprintf(&b, "  %s: %s\n", label, strings.Join(lines, ", "))
	}
	names("added containers", d.AddedContainers)
	names("removed containers", d.RemovedContainers)
	objs("added objects", d.AddedObjects)
	objs("removed objects", d.RemovedObjects)
	objs("changed objects", d.ChangedObjects)
	if len(d.ChangedContainers) > 0 {
		var lines []string
		for _, c := range d.ChangedContainers {
			lines = append(lines, c.Container+" ["+fieldsString(c.Fields)+"]")
		}
		sort.Strings(lines)
		fmt.Fprintf(&b, "  changed containers: %s\n", strings.Join(lines, ", "))
	}
	if b.Len() == 0 {
		return "  (no change)"
	}
	return b.String()
}

// Diff computes the exact delta from before to after. Object identity is
// compared field-by-field (digest, size, headers, metadata, large-object
// inventory, symlink target), so a same-size content swap or a segment
// change is a CHANGE, and a stray deletion anywhere is a removal.
func Diff(before, after *AccountSnapshot) Delta {
	var d Delta
	for name, ac := range after.Containers {
		if _, ok := before.Containers[name]; !ok {
			d.AddedContainers = append(d.AddedContainers, name)
			// Enumerate the new container's objects too: a bare-container
			// allowance must not blanket-allow arbitrary contents appearing
			// inside it (codex war-room H-05).
			for obj := range ac.Objects {
				d.AddedObjects = append(d.AddedObjects, ObjectChange{Container: name, Object: obj})
			}
		}
	}
	for name, bc := range before.Containers {
		ac, ok := after.Containers[name]
		if !ok {
			d.RemovedContainers = append(d.RemovedContainers, name)
			for obj := range bc.Objects {
				d.RemovedObjects = append(d.RemovedObjects, ObjectChange{Container: name, Object: obj})
			}
			continue
		}
		if fields := containerFields(bc, ac); len(fields) > 0 {
			d.ChangedContainers = append(d.ChangedContainers, ContainerChange{Container: name, Fields: fields})
		}
		for obj, ao := range ac.Objects {
			bo, ok := bc.Objects[obj]
			if !ok {
				d.AddedObjects = append(d.AddedObjects, ObjectChange{Container: name, Object: obj})
				continue
			}
			if fields := objectFields(bo, ao); len(fields) > 0 {
				d.ChangedObjects = append(d.ChangedObjects, ObjectChange{Container: name, Object: obj, Fields: fields})
			}
		}
		for obj := range bc.Objects {
			if _, ok := ac.Objects[obj]; !ok {
				d.RemovedObjects = append(d.RemovedObjects, ObjectChange{Container: name, Object: obj})
			}
		}
	}
	return d
}

func containerFields(a, b ContainerState) []Field {
	var fields []Field
	if a.ReadACL != b.ReadACL {
		fields = append(fields, "readACL")
	}
	if a.WriteACL != b.WriteACL {
		fields = append(fields, "writeACL")
	}
	if a.QuotaBytes != b.QuotaBytes {
		fields = append(fields, "quotaBytes")
	}
	if a.QuotaCount != b.QuotaCount {
		fields = append(fields, "quotaCount")
	}
	if a.Versioning != b.Versioning {
		fields = append(fields, "versioning")
	}
	if !reflect.DeepEqual(a.Meta, b.Meta) {
		fields = append(fields, "meta")
	}
	return fields
}

func objectFields(a, b ObjectState) []Field {
	var fields []Field
	if a.Class != b.Class {
		fields = append(fields, "class")
	}
	if a.Size != b.Size {
		fields = append(fields, "size")
	}
	if a.Digest != b.Digest {
		fields = append(fields, "content")
	}
	if a.ContentType != b.ContentType {
		fields = append(fields, "contentType")
	}
	if a.ContentEncoding != b.ContentEncoding || a.ContentDisposition != b.ContentDisposition {
		fields = append(fields, "contentHeaders")
	}
	if a.DeleteAt != b.DeleteAt {
		fields = append(fields, "deleteAt")
	}
	if !reflect.DeepEqual(a.Meta, b.Meta) {
		fields = append(fields, "meta")
	}
	if a.Manifest != b.Manifest {
		fields = append(fields, "manifest")
	}
	if a.SymlinkTarget != b.SymlinkTarget || a.SymlinkAccount != b.SymlinkAccount {
		fields = append(fields, "symlink")
	}
	if !reflect.DeepEqual(a.Segments, b.Segments) {
		fields = append(fields, "segments")
	}
	if a.RawSLOManifest != b.RawSLOManifest {
		fields = append(fields, "sloManifest")
	}
	return fields
}
