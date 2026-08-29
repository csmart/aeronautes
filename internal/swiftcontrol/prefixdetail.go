package swiftcontrol

import (
	"context"
	"errors"
)

// PrefixDetail is what a delimiter listing cannot say about a prefix
// (§14.1): whether an explicit marker object exists at the name, and
// whether the name is occupied by a real object wearing a folder suit.
// Fetched on selection (§14.2), one HEAD.
type PrefixDetail struct {
	Prefix string
	// MarkerBacked: a zero-byte application/directory object exists at
	// the prefix name. Implied prefixes (children only) report false.
	MarkerBacked bool
	// SelfObject is the HEAD detail of whatever object sits at the prefix
	// name — the marker itself, or a folder-ish real object that the
	// delimiter view folded into the group row. Nil when nothing exists.
	SelfObject *ObjectDetail
}

// PrefixDetail HEADs the exact prefix name and classifies the prefix.
func (s *Session) PrefixDetail(ctx context.Context, container, prefix string) (*PrefixDetail, error) {
	d, err := s.ObjectDetail(ctx, container, prefix)
	if err != nil {
		var se *SwiftError
		if errors.As(err, &se) && se.Kind == KindNotFound {
			// Children only: an implied prefix (§14.1) — a real state,
			// not an error.
			return &PrefixDetail{Prefix: prefix}, nil
		}
		return nil, err
	}
	return &PrefixDetail{
		Prefix:       prefix,
		MarkerBacked: d.ContentLength == 0 && d.ContentType == "application/directory",
		SelfObject:   d,
	}, nil
}
