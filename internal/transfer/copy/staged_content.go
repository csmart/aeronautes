package copy

import (
	"context"
	"crypto/md5"
	"errors"
	"fmt"
	"io"

	"github.com/csmart/aeronautes/internal/swiftcontrol"
)

func (e *Executor) verifyStagedContent(ctx context.Context, source *swiftcontrol.ObjectDetail, name string) (Code, error) {
	if source.ContentLength < 0 || len(source.ETag) != md5.Size*2 {
		return CodeIncomplete, errors.New("source has no complete plain-object length and MD5 expectation")
	}
	digest, ok := runDigest(source.ETag)
	if !ok {
		return CodeIncomplete, errors.New("source has no valid plain-object MD5 expectation")
	}
	return e.verifyStagedLadder(ctx, contentLadder{{length: source.ContentLength, digest: digest}}, name)
}

// verifyStagedLadder reads the complete assembled staged content once and
// digests it along the source ladder's boundaries. The staged object's own
// boundaries never enter the comparison, so a legitimately resegmented
// destination passes on its content.
func (e *Executor) verifyStagedLadder(ctx context.Context, ladder contentLadder, name string) (Code, error) {
	body, err := e.cfg.Dest.OpenObjectContent(ctx, e.cfg.DstContainer, name)
	if err != nil {
		return CodeIncomplete, fmt.Errorf("open complete staged content: %w", err)
	}
	return compareLadderContent(ctx, body, ladder)
}

func compareStagedContent(ctx context.Context, body io.ReadCloser, length int64, want [md5.Size]byte) (Code, error) {
	return compareLadderContent(ctx, body, contentLadder{{length: length, digest: want}})
}

// compareLadderContent drives the one comparison implementation over a
// staged object's body. The digesting itself lives in LadderDigest, so
// the staged route and the local routes cannot drift apart.
func compareLadderContent(ctx context.Context, body io.ReadCloser, ladder contentLadder) (Code, error) {
	return CompareContent(ctx, body, Ladder{runs: ladder})
}

func ladderMismatch(index, runs int) error {
	if runs == 1 {
		return errors.New("staged content MD5 does not match the plain source")
	}
	return fmt.Errorf("staged content MD5 does not match source segment %d of %d", index+1, runs)
}
