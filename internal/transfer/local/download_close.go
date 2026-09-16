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
func finishNativeDownload(ctx context.Context, body io.ReadCloser, temp nativeDownloadFile, parent *os.Root, publish publication, wantSize int64, ladder transfercopy.Ladder) (result downloadResult) {
	var verified os.FileInfo
	cancelled := false
	// A comparison failure retains its rejected bytes (ADR 0045), and it
	// does so whatever else went wrong afterwards. That is not only about
	// keeping the bytes: it keeps this path out of removeNativeTemporary
	// altogether, so no identity check is load-bearing here. The check
	// there compares device and inode, which a replacement can reuse after
	// an unlink, so it cannot tell our own temporary from somebody else's
	// file at the same name. Never reaching it is the only version of this
	// that cannot be reopened by a filesystem, a close reordering or a
	// change to that comparison.
	contentMismatch := false
	// An identity we could not establish is never authority to unlink:
	// cleanup would fall back to "any regular file will do", which is
	// exactly how another writer's replacement gets destroyed. Unlike a
	// content mismatch, this is not overridden by a close failure.
	identityFailed := false
	defer func() {
		if identityFailed || contentMismatch {
			result.Effect = transfercopy.EffectUnresolved
			return
		}
		if cleanupErr := removeNativeTemporary(parent, publish.stagedBase, publish.stagedDisplay, verified); cleanupErr != nil {
			result.addIssue(transfercopy.StageCleanup, "temporary-cleanup", fmt.Errorf("remove download temporary file: %w", cleanupErr))
			if cancelled {
				// The interrupted temporary is still there: residue.
				result.Effect = transfercopy.EffectUnresolved
			}
		}
	}()
	// The body already streams into the temporary, so the content
	// comparison rides that stream and costs no second read (ADR 0045).
	digest := transfercopy.NewLadderDigest(ladder)
	written, err := io.Copy(io.MultiWriter(temp, digest), body)
	verificationAttempted := err == nil
	// A cancel that ends the read leaves a partial temporary and an
	// untouched target; once the temporary is gone there is no effect.
	cancelled = errors.Is(err, context.Canceled) && ctx.Err() != nil
	if err == nil && written != wantSize {
		err = fmt.Errorf("wrote %d bytes, object says %d — refusing to place a short file", written, wantSize)
	}
	// The comparison decides before anything is published: a mismatch
	// leaves the temporary as retained residue and places nothing.
	//
	// Its own code is deliberately not propagated. The digester
	// distinguishes shapes of mismatch internally — content that ended
	// early reads as incomplete — but at this boundary the fact is that
	// the content could not be verified, and ADR 0045 keeps incomplete
	// for a ladder that could not be built. Propagating it reclassified
	// a source that advertised more bytes than it served, which the
	// close-fault regressions already pin as unverified.
	if err == nil {
		err = temp.Sync()
	}
	// The identity of the bytes this call wrote, taken while the handle
	// that wrote them is still open, and taken on EVERY path rather than
	// only the successful one. Cleanup uses it to refuse to unlink an
	// entry that is no longer ours; without it a failing path hands
	// cleanup a nil identity, which unlinks whatever regular file holds
	// the name -- including a replacement another writer put there.
	// Protecting somebody else's file cannot depend on why we failed.
	if info, statErr := temp.Stat(); statErr == nil {
		verified = info
	} else {
		identityFailed = true
		err = errors.Join(err, fmt.Errorf("cannot establish the identity of the downloaded temporary: %w", statErr))
	}
	if verificationAttempted {
		if _, contentErr := digest.Result(); contentErr != nil {
			contentMismatch = true
			err = errors.Join(err, fmt.Errorf("downloaded content does not match the source: %w", contentErr))
		}
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
	result = downloadVerified(wantSize)
	result.deferVerify = true
	result.published = verified
	result.publishedRel = publish.targetRel
	return result
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
