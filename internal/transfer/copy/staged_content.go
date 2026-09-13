package copy

import (
	"bytes"
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

func compareLadderContent(ctx context.Context, body io.ReadCloser, ladder contentLadder) (code Code, err error) {
	defer func() {
		closeErr := body.Close()
		if closeErr != nil || ctx.Err() != nil {
			if err == nil {
				code = CodeIO
			}
			err = errors.Join(err, closeErr, ctx.Err())
		}
	}()
	length := ladder.bytes()
	digest := md5.New()
	index := 0
	var runCount int64
	// settle closes every run whose bytes are complete, zero-length runs
	// included, so a run is compared the moment its last byte arrives and
	// never after another run's bytes have been folded in.
	settle := func() error {
		for index < len(ladder) && runCount == ladder[index].length {
			if !bytes.Equal(digest.Sum(nil), ladder[index].digest[:]) {
				return ladderMismatch(index, len(ladder))
			}
			digest.Reset()
			runCount = 0
			index++
		}
		return nil
	}
	if err := settle(); err != nil {
		return CodeUnverified, err
	}
	buffer := make([]byte, 64*1024)
	var count int64
	emptyReads := 0
	for {
		if err := ctx.Err(); err != nil {
			return CodeIO, err
		}
		n, readErr := body.Read(buffer)
		if n < 0 || n > len(buffer) {
			return CodeIO, errors.Join(errors.New("staged content reader returned an invalid byte count"), readErr)
		}
		// Subtract before adding so even a maximal source length cannot
		// overflow and turn an overrun into an accepted comparison.
		if int64(n) > length-count {
			return CodeIncomplete, errors.Join(fmt.Errorf("staged content exceeds the source length of %d bytes", length), readErr)
		}
		for chunk := buffer[:n]; len(chunk) > 0; {
			if index >= len(ladder) {
				return CodeIncomplete, errors.Join(fmt.Errorf("staged content exceeds the source length of %d bytes", length), readErr)
			}
			take := ladder[index].length - runCount
			if int64(len(chunk)) < take {
				take = int64(len(chunk))
			}
			if _, err := digest.Write(chunk[:take]); err != nil {
				return CodeIO, errors.Join(err, readErr)
			}
			runCount += take
			count += take
			chunk = chunk[take:]
			if err := settle(); err != nil {
				if readErr != nil && readErr != io.EOF {
					err = errors.Join(err, readErr)
				}
				return CodeUnverified, err
			}
		}
		if readErr != nil {
			if readErr != io.EOF {
				return CodeIO, readErr
			}
			if count != length {
				return CodeIncomplete, fmt.Errorf("staged content ended after %d bytes, source has %d", count, length)
			}
			if index != len(ladder) {
				return CodeIncomplete, fmt.Errorf("staged content ended inside run %d of %d", index+1, len(ladder))
			}
			return "", nil
		}
		if n == 0 {
			emptyReads++
			if emptyReads == 100 {
				return CodeIO, io.ErrNoProgress
			}
		} else {
			emptyReads = 0
		}
	}
}

func ladderMismatch(index, runs int) error {
	if runs == 1 {
		return errors.New("staged content MD5 does not match the plain source")
	}
	return fmt.Errorf("staged content MD5 does not match source segment %d of %d", index+1, runs)
}
