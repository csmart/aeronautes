package copy

import (
	"bytes"
	"context"
	"crypto/md5"
	"encoding/hex"
	"errors"
	"fmt"
	"io"

	"github.com/csmart/aeronautes/internal/swiftcontrol"
)

func (e *Executor) verifyStagedContent(ctx context.Context, source *swiftcontrol.ObjectDetail, name string) (Code, error) {
	if source.ContentLength < 0 || len(source.ETag) != md5.Size*2 {
		return CodeIncomplete, errors.New("source has no complete plain-object length and MD5 expectation")
	}
	want, err := hex.DecodeString(source.ETag)
	if err != nil {
		return CodeIncomplete, errors.New("source has no valid plain-object MD5 expectation")
	}
	body, err := e.cfg.Dest.OpenObjectContent(ctx, e.cfg.DstContainer, name)
	if err != nil {
		return CodeIncomplete, fmt.Errorf("open complete staged content: %w", err)
	}
	return compareStagedContent(ctx, body, source.ContentLength, [md5.Size]byte(want))
}

func compareStagedContent(ctx context.Context, body io.ReadCloser, length int64, want [md5.Size]byte) (code Code, err error) {
	defer func() {
		closeErr := body.Close()
		if closeErr != nil || ctx.Err() != nil {
			if err == nil {
				code = CodeIO
			}
			err = errors.Join(err, closeErr, ctx.Err())
		}
	}()
	digest := md5.New()
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
		if _, err := digest.Write(buffer[:n]); err != nil {
			return CodeIO, errors.Join(err, readErr)
		}
		count += int64(n)
		if readErr != nil {
			if readErr != io.EOF {
				return CodeIO, readErr
			}
			if count != length {
				return CodeIncomplete, fmt.Errorf("staged content ended after %d bytes, source has %d", count, length)
			}
			if !bytes.Equal(digest.Sum(nil), want[:]) {
				return CodeUnverified, errors.New("staged content MD5 does not match the plain source")
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
