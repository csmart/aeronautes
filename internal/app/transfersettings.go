package app

import (
	"encoding/json"
	"fmt"
	"regexp"

	"github.com/csmart/aeronautes/internal/transfer/rclone"
)

// TransferSettingsDTO is the per-job override surface: exactly the
// typed knobs §44.1 enumerates, nothing free-form. Zero values mean
// "use the default" (rclone's own, or the pinned default noted in
// docs/rclone.md).
type TransferSettingsDTO struct {
	// ChunkSize drives --swift-chunk-size (e.g. "64Mi", "5Gi").
	ChunkSize string `json:"chunkSize"`
	// Transfers/Checkers drive --transfers/--checkers.
	Transfers int `json:"transfers"`
	Checkers  int `json:"checkers"`
	// TPSLimit drives --tpslimit (requests/second).
	TPSLimit float64 `json:"tpsLimit"`
	// BWLimit drives --bwlimit (e.g. "10M").
	BWLimit string `json:"bwLimit"`
}

// sizePattern is rclone's SizeSuffix grammar, conservatively: digits
// with an optional binary/decimal suffix.
var sizePattern = regexp.MustCompile(`^[0-9]+(\.[0-9]+)?([bBkKMGTP]|[KMGTP]i)?$`)

// Validate enforces the typed surface (inputs are hostile, ADR 0011).
func (s TransferSettingsDTO) Validate() error {
	if s.ChunkSize != "" && !sizePattern.MatchString(s.ChunkSize) {
		return fmt.Errorf("chunk size %q is not a size (like 64Mi or 5Gi)", s.ChunkSize)
	}
	if s.BWLimit != "" && !sizePattern.MatchString(s.BWLimit) {
		return fmt.Errorf("bandwidth limit %q is not a size (like 10M)", s.BWLimit)
	}
	if s.Transfers < 0 || s.Transfers > 256 {
		return fmt.Errorf("transfers %d out of range 0..256", s.Transfers)
	}
	if s.Checkers < 0 || s.Checkers > 256 {
		return fmt.Errorf("checkers %d out of range 0..256", s.Checkers)
	}
	if s.TPSLimit < 0 || s.TPSLimit > 10000 {
		return fmt.Errorf("tps limit %v out of range", s.TPSLimit)
	}
	return nil
}

// options maps the DTO onto the runner's typed option set.
func (s TransferSettingsDTO) options() rclone.Options {
	return rclone.Options{
		ChunkSize: s.ChunkSize,
		Transfers: s.Transfers,
		Checkers:  s.Checkers,
		TPSLimit:  s.TPSLimit,
		BWLimit:   s.BWLimit,
	}
}

// effectiveJSON is the §45 record: what this job actually ran with.
func (s TransferSettingsDTO) effectiveJSON() string {
	encoded, err := json.Marshal(s)
	if err != nil {
		return ""
	}
	return string(encoded)
}

// effectiveJSONWithProfile prepends the resolved §40 profile to the
// record.
func effectiveJSONWithProfile(profile Profile, s TransferSettingsDTO) string {
	encoded := s.effectiveJSON()
	if encoded == "" {
		return ""
	}
	name := profile
	if name == "" {
		name = ProfileBalanced
	}
	return `{"profile":` + fmt.Sprintf("%q", string(name)) + `,` + encoded[1:]
}
