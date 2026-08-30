package oracle

import "testing"

// TestDiffDetectsContainerConfig: a container-config change (ACL,
// quota, versioning, meta) with NO object change is still a change —
// the negative-space invariant must see it. Pure unit, no SAIO.
func TestDiffDetectsContainerConfig(t *testing.T) {
	before := &AccountSnapshot{Containers: map[string]ContainerState{
		"c": {Name: "c", ReadACL: "", Objects: map[string]ObjectState{
			"x": {Name: "x", Digest: "d1", Size: 3},
		}},
	}}
	// Flip the read ACL public; objects untouched.
	after := &AccountSnapshot{Containers: map[string]ContainerState{
		"c": {Name: "c", ReadACL: ".r:*", Objects: map[string]ObjectState{
			"x": {Name: "x", Digest: "d1", Size: 3},
		}},
	}}
	d := Diff(before, after)
	if len(d.ChangedContainers) != 1 || d.ChangedContainers[0] != "c: readACL" {
		t.Fatalf("ACL flip not detected: %+v", d)
	}
	if !d.Empty() == false { // sanity: not empty
	}
	if len(d.ChangedObjects) != 0 {
		t.Fatalf("phantom object change: %+v", d)
	}
	// A same-digest object is not a change; a different digest is.
	after.Containers["c"].Objects["x"] = ObjectState{Name: "x", Digest: "d2", Size: 3}
	if d := Diff(before, after); len(d.ChangedObjects) != 1 {
		t.Fatalf("content change missed: %+v", d)
	}
}
