package swiftcontrol

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/ncw/swift/v2"
)

// ErrAlreadyExists is returned when creating something that is already
// there — Swift's idempotent PUT would silently accept, which hides
// state from an operator who thinks they created a fresh container.
var ErrAlreadyExists = errors.New("already exists")

// ContainerCreateRequest describes a new container (§20).
type ContainerCreateRequest struct {
	Name string
	// Policy "" uses the cluster default; anything else must come from
	// the advertised storage policies (§10) — the service gates that.
	Policy string
	// Meta is initial plain user metadata.
	Meta map[string]string
}

// CreateContainer creates a container and verifies the result. An
// existing container is an error, never a silent success.
func (s *Session) CreateContainer(ctx context.Context, create ContainerCreateRequest) (*ContainerDetail, error) {
	if create.Name == "" || strings.Contains(create.Name, "/") {
		return nil, fmt.Errorf("container name %q is invalid", create.Name)
	}
	if err := validateMetadata(create.Meta, false); err != nil {
		return nil, err
	}
	if _, err := s.ContainerDetail(ctx, create.Name); err == nil {
		return nil, fmt.Errorf("container %q: %w", create.Name, ErrAlreadyExists)
	} else {
		var se *SwiftError
		if !errors.As(err, &se) || se.Kind != KindNotFound {
			return nil, err
		}
	}

	headers := swift.Headers{}
	if create.Policy != "" {
		headers["X-Storage-Policy"] = create.Policy
	}
	for key, value := range create.Meta {
		headers[containerMetaPrefix+key] = value
	}
	if err := s.conn.ContainerCreate(ctx, create.Name, headers); err != nil {
		return nil, Normalize(err)
	}

	detail, err := s.ContainerDetail(ctx, create.Name)
	if err != nil {
		return nil, fmt.Errorf("container created but verify read failed: %w", err)
	}
	// Policy is create-time-only (§20): a race that created the container
	// first means our policy silently did not apply — surface it.
	if create.Policy != "" && !strings.EqualFold(detail.StoragePolicy, create.Policy) {
		return detail, fmt.Errorf("container created with policy %q, wanted %q (raced creation?)",
			detail.StoragePolicy, create.Policy)
	}
	return detail, nil
}

// CreateMarker PUTs the explicit §14.1 marker object (zero bytes,
// application/directory) at the prefix name and verifies it.
func (s *Session) CreateMarker(ctx context.Context, container, prefix string) (*PrefixDetail, error) {
	if !strings.HasSuffix(prefix, "/") {
		return nil, fmt.Errorf("marker prefix %q must end with the delimiter", prefix)
	}
	existing, err := s.PrefixDetail(ctx, container, prefix)
	if err != nil {
		return nil, err
	}
	if existing.SelfObject != nil {
		if existing.MarkerBacked {
			return nil, fmt.Errorf("marker %s/%s: %w", container, prefix, ErrAlreadyExists)
		}
		// A real object wearing a folder suit sits at this name (§14.1) —
		// overwriting it with a marker would destroy data.
		return nil, fmt.Errorf("an object with real data occupies %s/%s; refusing to overwrite it with a marker", container, prefix)
	}

	if _, err := s.conn.ObjectPut(ctx, container, prefix, strings.NewReader(""), true, "",
		"application/directory", nil); err != nil {
		return nil, Normalize(err)
	}
	detail, err := s.PrefixDetail(ctx, container, prefix)
	if err != nil {
		return nil, fmt.Errorf("marker created but verify read failed: %w", err)
	}
	if !detail.MarkerBacked {
		return detail, fmt.Errorf("marker created but verify does not show a marker-backed prefix")
	}
	return detail, nil
}

// DeleteMarker removes ONLY an explicit marker object: the one delete
// that is Phase-3-safe, because a marker is a plain zero-byte object and
// children are independent objects. Anything else at the name refuses.
func (s *Session) DeleteMarker(ctx context.Context, container, prefix string) error {
	detail, err := s.PrefixDetail(ctx, container, prefix)
	if err != nil {
		return err
	}
	if detail.SelfObject == nil {
		return &SwiftError{Kind: KindNotFound, Message: fmt.Sprintf("no marker object at %s/%s (implied prefix)", container, prefix)}
	}
	if !detail.MarkerBacked {
		// Classification never authorizes destruction of data it does not
		// prove (§24.4 spirit): a folder-suit real object is not a marker.
		return fmt.Errorf("the object at %s/%s is not a directory marker (%s, %d bytes); refusing to delete it through the marker path",
			container, prefix, detail.SelfObject.ContentType, detail.SelfObject.ContentLength)
	}
	if err := s.conn.ObjectDelete(ctx, container, prefix); err != nil {
		return Normalize(err)
	}
	after, err := s.PrefixDetail(ctx, container, prefix)
	if err != nil {
		return fmt.Errorf("marker deleted but verify read failed: %w", err)
	}
	if after.SelfObject != nil {
		return fmt.Errorf("marker delete verified still present")
	}
	return nil
}
