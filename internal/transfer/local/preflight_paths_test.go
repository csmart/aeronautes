package local

import (
	"context"

	"github.com/csmart/aeronautes/internal/swiftcontrol"
)

// The route's own tests select files by path, as a file pick does. The
// product names its sources at the pick and calls the two entry points
// below directly, so these stay with the tests that use them.

func PreflightUpload(ctx context.Context, dst *swiftcontrol.Session, container, prefix string, paths []string) (*UploadPreflight, error) {
	return PreflightUploadSources(ctx, dst, container, prefix, SourcesByBaseName(paths))
}

func PreflightUploadReplacing(ctx context.Context, dst *swiftcontrol.Session, container, prefix string, paths []string) (*UploadPreflight, error) {
	return PreflightUploadSourcesReplacing(ctx, dst, container, prefix, SourcesByBaseName(paths))
}
