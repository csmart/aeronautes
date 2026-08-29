package app

import (
	"fmt"

	"github.com/csmart/aeronautes/internal/transfer/rclone"
)

// Profile is the §40 user-facing performance mode.
type Profile string

const (
	ProfileAuto       Profile = "auto"
	ProfileBalanced   Profile = "balanced"
	ProfileAggressive Profile = "aggressive"
	ProfileCustom     Profile = "custom"
)

// profileBase returns the typed §44.1 settings a profile stands for.
// Auto starts from Balanced and is upgraded by per-bookmark benchmark
// recommendations when they exist (§42; the benchmark page records
// them).
func profileBase(p Profile) (TransferSettingsDTO, error) {
	switch p {
	case ProfileBalanced, "":
		return TransferSettingsDTO{Transfers: 4, Checkers: 8}, nil
	case ProfileAggressive:
		return TransferSettingsDTO{Transfers: 16, Checkers: 32}, nil
	case ProfileAuto:
		return TransferSettingsDTO{Transfers: 4, Checkers: 8}, nil
	case ProfileCustom:
		return TransferSettingsDTO{}, nil
	default:
		return TransferSettingsDTO{}, fmt.Errorf("unknown performance profile %q", p)
	}
}

// ResolveSettings turns (profile, per-job overrides, recommendation)
// into the effective §44.1 settings: Custom is the overrides verbatim;
// otherwise the profile's base with any non-zero override winning
// (§38: per-job requested overrides are the innermost layer), and Auto
// preferring a recorded recommendation over its fallback base.
func ResolveSettings(p Profile, overrides TransferSettingsDTO, recommendation *TransferSettingsDTO) (TransferSettingsDTO, error) {
	if err := overrides.Validate(); err != nil {
		return TransferSettingsDTO{}, err
	}
	if p == ProfileCustom {
		return overrides, nil
	}
	base, err := profileBase(p)
	if err != nil {
		return TransferSettingsDTO{}, err
	}
	if p == ProfileAuto && recommendation != nil {
		base = *recommendation
	}
	if overrides.ChunkSize != "" {
		base.ChunkSize = overrides.ChunkSize
	}
	if overrides.Transfers != 0 {
		base.Transfers = overrides.Transfers
	}
	if overrides.Checkers != 0 {
		base.Checkers = overrides.Checkers
	}
	if overrides.TPSLimit != 0 {
		base.TPSLimit = overrides.TPSLimit
	}
	if overrides.BWLimit != "" {
		base.BWLimit = overrides.BWLimit
	}
	return base, nil
}

var _ = rclone.Options{} // the settings map onto the runner's options
