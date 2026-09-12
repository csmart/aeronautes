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
	Stat() (os.FileInfo, error)
	Close() error
}

// finishNativeDownload writes the body into the temporary, verifies it,
// closes both ends, and publishes exactly the file it verified into the
// inspected parent. The temporary is removed only while it is still that
// file.
func finishNativeDownload(ctx context.Context, body io.ReadCloser, temp nativeDownloadFile, parent *os.Root, publish publication, wantSize int64) (result downloadResult) {
	var verified os.FileInfo
	cancelled := false
	defer func() {
		if cleanupErr := removeNativeTemporary(parent, publish.stagedBase, publish.stagedDisplay, verified); cleanupErr != nil {
			result.addIssue(transfercopy.StageCleanup, "temporary-cleanup", fmt.Errorf("remove download temporary file: %w", cleanupErr))
			if cancelled {
				// The interrupted temporary is still there: residue.
				result.Effect = transfercopy.EffectUnresolved
			}
		}
	}()
	written, err := io.Copy(temp, body)
	verificationAttempted := err == nil
	// A cancel that ends the read leaves a partial temporary and an
	// untouched target; once the temporary is gone there is no effect.
	cancelled = errors.Is(err, context.Canceled) && ctx.Err() != nil
	if err == nil && written != wantSize {
		err = fmt.Errorf("wrote %d bytes, object says %d — refusing to place a short file", written, wantSize)
	}
	if err == nil {
		err = temp.Sync()
	}
	if err == nil {
		// The identity of the verified bytes, taken while the handle that
		// wrote them is still open.
		verified, err = temp.Stat()
	}
	// Swift can report corruption only on Close after a successful read.
	// Both handles must close successfully before the final name appears.
	closeErr := errors.Join(temp.Close(), body.Close())
	err = errors.Join(err, closeErr)
	if err != nil {
		if verificationAttempted {
			return downloadVerificationFailed(transfercopy.StageVerification, transfercopy.CodeUnverified, "native-get", err)
		}
		effect := transfercopy.EffectUnresolved
		if cancelled {
			effect = transfercopy.EffectNotAttempted
		}
		result := downloadFailed(effect, transfercopy.StageSourceRead, transfercopy.CodeIO, "native-get", err)
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
	if err := publish.publishVerified(verified); err != nil {
		return downloadPlacementFailed("native-get", err)
	}
	return downloadVerified(wantSize)
}

// removeNativeTemporary unlinks the temporary while the entry at its name
// is still the file this executor created. A substituted entry is
// retained and reported rather than removed.
func removeNativeTemporary(parent *os.Root, base, display string, verified os.FileInfo) error {
	current, err := parent.Lstat(base)
	if err != nil {
		return atPath(err, "lstat", display)
	}
	if verified != nil && !os.SameFile(current, verified) {
		return fmt.Errorf("temporary %q was replaced by another entry; it was retained", display)
	}
	if verified == nil && !current.Mode().IsRegular() {
		return fmt.Errorf("temporary %q is no longer a regular file; it was retained", display)
	}
	return unlinkFileAt(parent, base, display)
}
