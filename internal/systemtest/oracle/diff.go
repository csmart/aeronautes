package oracle

import (
	"fmt"
	"reflect"
	"sort"
	"strings"
)

// Delta describes what changed between two account snapshots.
type Delta struct {
	AddedContainers   []string
	RemovedContainers []string
	AddedObjects      []string // "container/name"
	RemovedObjects    []string // "container/name"
	ChangedObjects    []string // "container/name: <what>"
	ChangedContainers []string // "container: <what>"
}

// Empty reports whether nothing changed at all.
func (d Delta) Empty() bool {
	return len(d.AddedContainers) == 0 && len(d.RemovedContainers) == 0 &&
		len(d.AddedObjects) == 0 && len(d.RemovedObjects) == 0 &&
		len(d.ChangedObjects) == 0 && len(d.ChangedContainers) == 0
}

func (d Delta) String() string {
	var b strings.Builder
	line := func(label string, xs []string) {
		if len(xs) > 0 {
			sort.Strings(xs)
			fmt.Fprintf(&b, "  %s: %s\n", label, strings.Join(xs, ", "))
		}
	}
	line("added containers", d.AddedContainers)
	line("removed containers", d.RemovedContainers)
	line("added objects", d.AddedObjects)
	line("removed objects", d.RemovedObjects)
	line("changed objects", d.ChangedObjects)
	line("changed containers", d.ChangedContainers)
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
	for name := range after.Containers {
		if _, ok := before.Containers[name]; !ok {
			d.AddedContainers = append(d.AddedContainers, name)
		}
	}
	for name, bc := range before.Containers {
		ac, ok := after.Containers[name]
		if !ok {
			d.RemovedContainers = append(d.RemovedContainers, name)
			continue
		}
		if why := containerConfigDiff(bc, ac); why != "" {
			d.ChangedContainers = append(d.ChangedContainers, name+": "+why)
		}
		for obj, ao := range ac.Objects {
			bo, ok := bc.Objects[obj]
			if !ok {
				d.AddedObjects = append(d.AddedObjects, name+"/"+obj)
				continue
			}
			if why := objectDiff(bo, ao); why != "" {
				d.ChangedObjects = append(d.ChangedObjects, name+"/"+obj+": "+why)
			}
		}
		for obj := range bc.Objects {
			if _, ok := ac.Objects[obj]; !ok {
				d.RemovedObjects = append(d.RemovedObjects, name+"/"+obj)
			}
		}
	}
	return d
}

func containerConfigDiff(a, b ContainerState) string {
	var why []string
	if a.ReadACL != b.ReadACL {
		why = append(why, "readACL")
	}
	if a.WriteACL != b.WriteACL {
		why = append(why, "writeACL")
	}
	if a.QuotaBytes != b.QuotaBytes {
		why = append(why, "quotaBytes")
	}
	if a.QuotaCount != b.QuotaCount {
		why = append(why, "quotaCount")
	}
	if a.Versioning != b.Versioning {
		why = append(why, "versioning")
	}
	if !reflect.DeepEqual(a.Meta, b.Meta) {
		why = append(why, "meta")
	}
	return strings.Join(why, ",")
}

func objectDiff(a, b ObjectState) string {
	var why []string
	if a.Class != b.Class {
		why = append(why, "class")
	}
	if a.Size != b.Size {
		why = append(why, "size")
	}
	if a.Digest != b.Digest {
		why = append(why, "content")
	}
	if a.ContentType != b.ContentType {
		why = append(why, "contentType")
	}
	if a.ContentEncoding != b.ContentEncoding || a.ContentDisposition != b.ContentDisposition {
		why = append(why, "contentHeaders")
	}
	if a.DeleteAt != b.DeleteAt {
		why = append(why, "deleteAt")
	}
	if !reflect.DeepEqual(a.Meta, b.Meta) {
		why = append(why, "meta")
	}
	if a.Manifest != b.Manifest {
		why = append(why, "manifest")
	}
	if a.SymlinkTarget != b.SymlinkTarget || a.SymlinkAccount != b.SymlinkAccount {
		why = append(why, "symlink")
	}
	if !reflect.DeepEqual(a.Segments, b.Segments) {
		why = append(why, "segments")
	}
	if a.RawSLOManifest != b.RawSLOManifest {
		why = append(why, "sloManifest")
	}
	return strings.Join(why, ",")
}
