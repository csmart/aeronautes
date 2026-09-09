package local

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"

	transfercopy "github.com/csmart/aeronautes/internal/transfer/copy"
)

type nativeDownloadFile interface {
	io.Writer
	Sync() error
	Close() error
}

func finishNativeDownload(ctx context.Context, body io.ReadCloser, temp nativeDownloadFile, temporary, target string, wantSize int64) (result downloadResult) {
	defer func() {
		if cleanupErr := os.Remove(temporary); cleanupErr != nil {
			result.addIssue(transfercopy.StageCleanup, "temporary-cleanup", fmt.Errorf("remove download temporary file: %w", cleanupErr))
		}
	}()
	written, err := io.Copy(temp, body)
	verificationAttempted := err == nil
	if err == nil && written != wantSize {
		err = fmt.Errorf("wrote %d bytes, object says %d — refusing to place a short file", written, wantSize)
	}
	if err == nil {
		err = temp.Sync()
	}
	// Swift can report corruption only on Close after a successful read.
	// Both handles must close successfully before the final name appears.
	closeErr := errors.Join(temp.Close(), body.Close())
	err = errors.Join(err, closeErr)
	if err != nil {
		if verificationAttempted {
			return downloadVerificationFailed(transfercopy.StageVerification, transfercopy.CodeUnverified, "native-get", err)
		}
		result := downloadFailed(transfercopy.EffectUnresolved, transfercopy.StageSourceRead, transfercopy.CodeIO, "native-get", err)
		if closeErr != nil {
			result.verification = &transfercopy.Observation{Result: transfercopy.ResultFailed,
				Effect:     transfercopy.EffectNotAttempted,
				Diagnostic: transfercopy.ClassifyDiagnostic(transfercopy.StageVerification, transfercopy.CodeUnverified, closeErr)}
		}
		return result
	}
	if err := ctx.Err(); err != nil {
		return downloadFailed(transfercopy.EffectUnresolved, transfercopy.StageCommit, transfercopy.CodeIncomplete, "native-get", err)
	}
	// A concurrent creator keeps its target; rename would overwrite it.
	if err := os.Link(temporary, target); err != nil {
		return downloadPlacementFailed("native-get", err)
	}
	return downloadVerified(wantSize)
}
