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
	// The large-object choice belongs to the upload that applies it, which
	// records what it published; a copy or a download must not record it.
	overrides.LargeObjects = ""
	if p == ProfileCustom {
		return overrides.withDefaults(), nil
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
	if overrides.BufferSize != "" {
		base.BufferSize = overrides.BufferSize
	}
	return base.withDefaults(), nil
}

// settingOrigins classifies where each resolved §44.1 knob's value came
// from, mirroring ResolveSettings' precedence: a non-zero per-job
// override wins; otherwise the value is the profile base, or a benchmark
// recommendation under Auto; otherwise a filled default, or — for the
// chunk size, which no profile sets — a value calculated against the
// destination. The §45 record stores this so a profile or calculated
// value does not read as one the operator set (UI-09). It is a
// provenance view only; ResolveSettings stays the one place that decides
// the values.
func settingOrigins(p Profile, overrides TransferSettingsDTO, recommendation *TransferSettingsDTO) map[string]string {
	base, _ := profileBase(p)
	baseToken := "profile"
	if p == ProfileAuto && recommendation != nil {
		base = *recommendation
		baseToken = "recommendation"
	}
	str := func(override, baseValue, fallback string) string {
		switch {
		case override != "":
			return "override"
		case baseValue != "":
			return baseToken
		default:
			return fallback
		}
	}
	num := func(override, baseValue int) string {
		switch {
		case override != 0:
			return "override"
		case baseValue != 0:
			return baseToken
		default:
			return "default"
		}
	}
	flt := func(override, baseValue float64) string {
		switch {
		case override != 0:
			return "override"
		case baseValue != 0:
			return baseToken
		default:
			return "default"
		}
	}
	return map[string]string{
		"transfers":  num(overrides.Transfers, base.Transfers),
		"checkers":   num(overrides.Checkers, base.Checkers),
		"tpsLimit":   flt(overrides.TPSLimit, base.TPSLimit),
		"bwLimit":    str(overrides.BWLimit, base.BWLimit, "default"),
		"bufferSize": str(overrides.BufferSize, base.BufferSize, "default"),
		"chunkSize":  str(overrides.ChunkSize, base.ChunkSize, "calculated"),
	}
}

var _ = rclone.Options{} // the settings map onto the runner's options
