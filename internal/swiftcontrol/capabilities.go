package swiftcontrol

import (
	"context"
	"errors"
	"time"
)

// CapabilityState is the three-valued answer to "does this cluster support
// X?" (§10). Unknown is not Unsupported: with no readable /info, features
// are offered with a "capability unknown" label and validated by attempting
// the operation, never hard-disabled.
type CapabilityState string

const (
	CapabilitySupported   CapabilityState = "supported"
	CapabilityUnsupported CapabilityState = "unsupported"
	CapabilityUnknown     CapabilityState = "unknown"
)

// CapabilitySource records where capability knowledge came from (§10).
type CapabilitySource string

const (
	// SourceAdvertised: read from the cluster's /info.
	SourceAdvertised CapabilitySource = "advertised"
	// SourceObserved: learned from real operation results.
	SourceObserved CapabilitySource = "observed"
	// SourceSafelyProbed: learned from non-destructive probes (or probes
	// confined to an Aeronautes-owned test container).
	SourceSafelyProbed CapabilitySource = "safely_probed"
	// SourceOperatorDeclared: the operator told us.
	SourceOperatorDeclared CapabilitySource = "operator_declared"
	// SourceUnknown: /info was disabled, filtered, or unreachable.
	SourceUnknown CapabilitySource = "unknown"
)

// Capabilities is one bookmark's capability snapshot.
type Capabilities struct {
	Source    CapabilitySource
	FetchedAt time.Time
	// Raw is the /info payload when Source is SourceAdvertised, else nil.
	Raw map[string]any
	// Unavailable says why /info could not be read when Source is
	// SourceUnknown; nil otherwise.
	Unavailable error
}

// StoragePolicy is one advertised storage policy (§20).
type StoragePolicy struct {
	Name    string
	Aliases string
	Default bool
}

// FetchCapabilities reads GET /info, derived from the storage URL by
// stripping the account path (§10). An unreadable /info — disabled,
// filtered, unreachable — is not an error: it returns the
// Unknown-capabilities mode with the cause in Unavailable, because a
// tenant without /info must still be able to operate. Only context
// cancellation is returned as an error.
func (s *Session) FetchCapabilities(ctx context.Context) (*Capabilities, error) {
	info, err := s.conn.QueryInfo(ctx)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return nil, err
		}
		return &Capabilities{
			Source:      SourceUnknown,
			FetchedAt:   time.Now().UTC(),
			Unavailable: Normalize(err),
		}, nil
	}
	return &Capabilities{
		Source:    SourceAdvertised,
		FetchedAt: time.Now().UTC(),
		Raw:       map[string]any(info),
	}, nil
}

// State reports whether the cluster supports a top-level /info feature key
// such as "slo", "bulk_delete", "tempurl", "staticweb", "versioned_writes"
// or "container_quotas".
func (c *Capabilities) State(feature string) CapabilityState {
	if c.Source != SourceAdvertised {
		return CapabilityUnknown
	}
	if _, ok := c.Raw[feature]; ok {
		return CapabilitySupported
	}
	return CapabilityUnsupported
}

// Feature returns a feature's advertised settings map, or nil when absent
// or unknown.
func (c *Capabilities) Feature(name string) map[string]any {
	if c.Source != SourceAdvertised {
		return nil
	}
	m, _ := c.Raw[name].(map[string]any)
	return m
}

// MaxFileSize returns swift.max_file_size — the cap on the large-object
// chunk size (§25, K-XFER-004) — or 0 when unknown.
func (c *Capabilities) MaxFileSize() int64 {
	v, _ := c.Feature("swift")["max_file_size"].(float64)
	return int64(v)
}

// BulkDeleteMaxDeletes returns bulk_delete.max_deletes_per_request, or 0
// when unknown.
func (c *Capabilities) BulkDeleteMaxDeletes() int {
	v, _ := c.Feature("bulk_delete")["max_deletes_per_request"].(float64)
	return int(v)
}

// StoragePolicies returns the advertised policies (§20), or nil when
// unknown.
func (c *Capabilities) StoragePolicies() []StoragePolicy {
	raw, _ := c.Feature("swift")["policies"].([]any)
	var out []StoragePolicy
	for _, entry := range raw {
		m, ok := entry.(map[string]any)
		if !ok {
			continue
		}
		p := StoragePolicy{}
		p.Name, _ = m["name"].(string)
		p.Aliases, _ = m["aliases"].(string)
		p.Default, _ = m["default"].(bool)
		if p.Name != "" {
			out = append(out, p)
		}
	}
	return out
}
