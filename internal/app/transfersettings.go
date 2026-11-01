package app

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"

	"github.com/csmart/aeronautes/internal/swiftcontrol"
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
	// BufferSize drives --buffer-size, the child's per-transfer read
	// buffer (e.g. "16Mi"); every job runs with an explicit value, the
	// engine's own default unless overridden, so transfers × buffer is
	// the child memory the record shows (ADR 0074 decision 3).
	BufferSize string `json:"bufferSize"`
}

// DefaultBufferSize is the engine's own per-transfer buffer, emitted
// explicitly so the record names it (ADR 0074 decision 3).
const DefaultBufferSize = "16Mi"

// withDefaults fills the values every job runs with when the caller
// left them unset; the record then shows what the child was given.
func (s TransferSettingsDTO) withDefaults() TransferSettingsDTO {
	if s.BufferSize == "" {
		s.BufferSize = DefaultBufferSize
	}
	return s
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
	if s.BufferSize != "" {
		bytes, err := ParseSize(s.BufferSize)
		if err != nil {
			return fmt.Errorf("buffer size %q is not a size (like 16Mi)", s.BufferSize)
		}
		if bytes < 1<<20 || bytes > 1<<30 {
			return fmt.Errorf("buffer size %s out of range 1Mi..1Gi", s.BufferSize)
		}
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
		ChunkSize:  s.ChunkSize,
		Transfers:  s.Transfers,
		Checkers:   s.Checkers,
		TPSLimit:   s.TPSLimit,
		BWLimit:    s.BWLimit,
		BufferSize: s.BufferSize,
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

// effectiveRecord is the §45 record stored on a job: the resolved §40
// profile, the typed settings the child ran with, and where each knob's
// value came from (origins). The origins let the operator tell a value
// they chose on this job from one the profile, a benchmark
// recommendation, or a default supplied — the distinction the flat
// record used to lose (UI-09).
type effectiveRecord struct {
	Profile string `json:"profile"`
	TransferSettingsDTO
	Origins map[string]string `json:"origins,omitempty"`
}

// effectiveJSONWithProfile records the resolved §40 profile, the typed
// settings, and the per-knob origins (§45).
func effectiveJSONWithProfile(profile Profile, s TransferSettingsDTO, origins map[string]string) string {
	name := profile
	if name == "" {
		name = ProfileBalanced
	}
	encoded, err := json.Marshal(effectiveRecord{
		Profile:             string(name),
		TransferSettingsDTO: s,
		Origins:             origins,
	})
	if err != nil {
		return ""
	}
	return string(encoded)
}

// ParseSize converts the SizeSuffix grammar to bytes (K-XFER-004: the
// chunk knob is CAPPED against the destination's advertised
// max_file_size, not just syntax-checked).
func ParseSize(v string) (int64, error) {
	if !sizePattern.MatchString(v) {
		return 0, fmt.Errorf("%q is not a size", v)
	}
	multipliers := map[string]float64{
		"": 1, "b": 1, "B": 1,
		"k": 1024, "K": 1024, "Ki": 1024,
		"M": 1 << 20, "Mi": 1 << 20,
		"G": 1 << 30, "Gi": 1 << 30,
		"T": 1 << 40, "Ti": 1 << 40,
		"P": 1 << 50, "Pi": 1 << 50,
	}
	num := v
	suffix := ""
	for i := len(v) - 1; i >= 0; i-- {
		c := v[i]
		if c >= '0' && c <= '9' || c == '.' {
			num, suffix = v[:i+1], v[i+1:]
			break
		}
	}
	m, ok := multipliers[suffix]
	if !ok {
		return 0, fmt.Errorf("unknown size suffix %q", suffix)
	}
	f, err := strconv.ParseFloat(num, 64)
	if err != nil {
		return 0, err
	}
	return int64(f * m), nil
}

// CheckChunkAgainst refuses a chunk size the destination cannot store
// as one segment, and a zero/absurd value outright.
func (s TransferSettingsDTO) CheckChunkAgainst(capabilities *swiftcontrol.Capabilities) error {
	if s.ChunkSize == "" {
		return nil
	}
	bytes, err := ParseSize(s.ChunkSize)
	if err != nil {
		return err
	}
	if bytes < 1<<20 {
		return fmt.Errorf("chunk size %s is below 1Mi — segments that small drown the cluster in requests", s.ChunkSize)
	}
	if capabilities != nil {
		if max := capabilities.MaxFileSize(); max > 0 && bytes > max {
			return fmt.Errorf("chunk size %s exceeds the destination's max_file_size (%d bytes): a single segment could not be stored", s.ChunkSize, max)
		}
	}
	return nil
}

// forDestination resolves one chunk value for admission, execution and
// recording. Unknown capabilities retain the standard default; an
// advertised smaller maximum constrains even an unset chunk setting.
func (s TransferSettingsDTO) forDestination(ctx context.Context, destination *swiftcontrol.Session) (TransferSettingsDTO, error) {
	if err := s.Validate(); err != nil {
		return TransferSettingsDTO{}, err
	}
	capabilities, err := destination.FetchCapabilities(ctx)
	if err != nil {
		return TransferSettingsDTO{}, err
	}
	if err := s.CheckChunkAgainst(capabilities); err != nil {
		return TransferSettingsDTO{}, err
	}
	if s.ChunkSize == "" {
		s.ChunkSize = "5Gi"
		if max := capabilities.MaxFileSize(); max > 0 && max < 5<<30 {
			s.ChunkSize = fmt.Sprintf("%db", max)
		}
	}
	return s, nil
}
