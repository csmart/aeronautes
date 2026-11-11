package app

import (
	"testing"
)

// ADR 0074 decision 3: every job runs with an explicit buffer size, the
// engine's own default unless overridden, within a bounded range.
func TestBufferSizeDefaultsAndBounds(t *testing.T) {
	for _, profile := range []Profile{ProfileBalanced, ProfileAuto, ProfileAggressive, ProfileCustom} {
		settings, err := ResolveSettings(profile, TransferSettingsDTO{}, nil)
		if err != nil || settings.BufferSize != DefaultBufferSize {
			t.Fatalf("%s buffer size = %+v %v", profile, settings, err)
		}
		settings, err = ResolveSettings(profile, TransferSettingsDTO{BufferSize: "32Mi"}, nil)
		if err != nil || settings.BufferSize != "32Mi" {
			t.Fatalf("%s override = %+v %v", profile, settings, err)
		}
	}
	for _, size := range []string{"512k", "2Gi", "abc", "0"} {
		if err := (TransferSettingsDTO{BufferSize: size}).Validate(); err == nil {
			t.Fatalf("buffer size %q accepted", size)
		}
	}
	for _, size := range []string{"1Mi", "16Mi", "1Gi", "256M"} {
		if err := (TransferSettingsDTO{BufferSize: size}).Validate(); err != nil {
			t.Fatalf("buffer size %q refused: %v", size, err)
		}
	}
}
