package plan

import (
	"sort"
	"strings"
)

// Selection is what a copy or a download of selected rows acts on (ADR 0108
// decisions 2 and 7): each object member by its full name, and each folder
// member, a name that ends in "/", with everything under it. It records which
// members a listing met, so a member with nothing at the source is known.
// A nil Selection is the whole scope: it takes every name and misses none.
type Selection struct {
	members []string
	objects map[string]bool
	folders map[string]bool
	sorted  []string
	met     map[string]bool
}

// NewSelection returns nil for no members, which means the whole scope.
func NewSelection(members []string) *Selection {
	if len(members) == 0 {
		return nil
	}
	s := &Selection{members: members, objects: map[string]bool{}, folders: map[string]bool{}, met: map[string]bool{}}
	for _, name := range members {
		if strings.HasSuffix(name, "/") {
			s.folders[name] = true
		} else {
			s.objects[name] = true
		}
	}
	s.sorted = append([]string(nil), members...)
	sort.Strings(s.sorted)
	return s
}

// Member is the member that takes name: the object member of that name, or
// the folder member it lies under, the folder's own marker included; "" when
// none does. Members do not nest, so at most one folder holds a name.
func (s *Selection) Member(name string) string {
	if s.objects[name] {
		return name
	}
	for i := 0; i < len(name); i++ {
		if name[i] == '/' && s.folders[name[:i+1]] {
			return name[:i+1]
		}
	}
	return ""
}

// Holds reports whether any member is prefix itself or lies under it.
func (s *Selection) Holds(prefix string) bool {
	i := sort.SearchStrings(s.sorted, prefix)
	return i < len(s.sorted) && strings.HasPrefix(s.sorted[i], prefix)
}

// Take reports whether a listed name belongs to the selection, and records
// its member as met.
func (s *Selection) Take(name string) bool {
	if s == nil {
		return true
	}
	member := s.Member(name)
	if member == "" {
		return false
	}
	s.met[member] = true
	return true
}

// Missing is how many members no taken name met.
func (s *Selection) Missing() int {
	if s == nil {
		return 0
	}
	return len(s.objects) + len(s.folders) - len(s.met)
}

// FirstMissing is the first member, in the order given, that no taken name
// met, or "" when every member was met.
func (s *Selection) FirstMissing() string {
	if s == nil {
		return ""
	}
	for _, name := range s.members {
		if !s.met[name] {
			return name
		}
	}
	return ""
}

// Nested is the first member that lies under a folder member other than
// itself, with that folder, or two empty strings: such a member names
// nothing the folder does not (ADR 0108 decision 7).
func Nested(members []string) (member, folder string) {
	folders := map[string]bool{}
	for _, name := range members {
		if strings.HasSuffix(name, "/") {
			folders[name] = true
		}
	}
	for _, name := range members {
		for i := 0; i < len(name)-1; i++ {
			if name[i] == '/' && folders[name[:i+1]] {
				return name, name[:i+1]
			}
		}
	}
	return "", ""
}
