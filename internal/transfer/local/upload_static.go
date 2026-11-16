package local

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"mime"
	"os"
	"path"
	"strings"

	"github.com/csmart/aeronautes/internal/swiftcontrol"
	transfercopy "github.com/csmart/aeronautes/internal/transfer/copy"
	"github.com/csmart/aeronautes/internal/transfer/rclone"
)

// makeStatic writes the static manifest over the dynamic one the engine
// wrote for a file it uploaded in segments: one request, naming the same
// segments by the path the engine reported and by each chunk's size and MD5
// from the local file (ADR 0122 decision 3, ADR 0111). The store checks
// every entry against the segment it names and refuses the whole manifest
// when one does not match, which is this route's check of the bytes that
// arrived. A manifest that cannot be composed, or that the store refuses,
// fails the item, and the object stays the dynamic one the engine wrote.
//
// The first static manifest of a connection is read back once, at its own
// name, to learn whether the store returns static manifests (ADR 0111
// decision 9). A store that does not gets the dynamic manifest back at that
// name and dynamic large objects for the rest of the job (decision 10).
func (u *uploadRun) makeStatic(ctx context.Context, dst *swiftcontrol.Session, container string, item UploadItem,
	segments rclone.SegmentPrefix, chunkBytes int64) uploadResult {
	if u.static.known && !u.static.returned {
		return uploadDone(item)
	}
	stays := func(why string, err error) uploadResult {
		return uploadFailed(transfercopy.EffectUnresolved, transfercopy.StageCommit, transfercopy.CodeIO, "static-manifest",
			fmt.Errorf("%s, so the object at the name is the dynamic one the engine wrote: %w", why, err))
	}
	if segments.Prefix == "" {
		return stays("the engine did not say where it put this file's segments", errors.New("no segment path was reported"))
	}
	chunks, err := fileChunks(item.Path, item.Size, chunkBytes)
	if err != nil {
		return stays("the file could not be read again for its segments' digests", err)
	}
	entries := make([]swiftcontrol.StaticSegment, len(chunks))
	for i, c := range chunks {
		entries[i] = swiftcontrol.StaticSegment{Container: segments.Container, Name: fmt.Sprintf("%s%08d", segments.Prefix, i), ETag: c.md5, Bytes: c.size}
	}
	contentType := engineContentType(item.Name)
	// No content tag is declared: the file's MD5 would bind the manifest's
	// JSON body, not the segments it names (ADR 0111 decision 4).
	if _, err := dst.PutStaticManifest(ctx, container, item.Name, entries, contentType, nil); err != nil {
		return stays("the store did not accept the static manifest", err)
	}
	if u.static.known {
		return uploadDone(item)
	}
	read, readErr := dst.SLOManifestEntries(ctx, container, item.Name, MaxManifestEntries, MaxManifestBytes)
	switch {
	case errors.Is(readErr, swiftcontrol.ErrManifestNotReturned):
		// The store took a static manifest it does not give back, so nothing
		// could later read which segments this object uses. The dynamic
		// manifest names them by their prefix instead.
		dynamic := map[string]string{"X-Object-Manifest": segments.Container + "/" + segments.Prefix}
		if _, err := dst.PutObject(ctx, container, item.Name, strings.NewReader(""), contentType, dynamic); err != nil {
			return uploadFailed(transfercopy.EffectUnresolved, transfercopy.StageCommit, transfercopy.CodeIO, "static-manifest",
				fmt.Errorf("the store does not return static manifests and the dynamic manifest could not be put back, so the name holds a static manifest: %w", err))
		}
		if err := u.learnStatic(false); err != nil {
			return uploadFailed(transfercopy.EffectUnresolved, transfercopy.StageRecording, transfercopy.CodeIO, "static-manifest", err)
		}
	case readErr == nil && namesExactly(read, entries):
		if err := u.learnStatic(true); err != nil {
			return uploadFailed(transfercopy.EffectUnresolved, transfercopy.StageRecording, transfercopy.CodeIO, "static-manifest", err)
		}
	}
	// Any other answer to the read leaves the question open for the next
	// file: the store accepted this manifest, which is this file's outcome.
	return uploadDone(item)
}

// learnStatic keeps the answer for the rest of the run and hands it to the
// recorder, whose failure stops the run.
func (u *uploadRun) learnStatic(returned bool) error {
	u.static = staticManifests{known: true, returned: returned}
	if u.recorder == nil || u.recorder.StaticManifests == nil {
		return nil
	}
	if err := u.recorder.StaticManifests(returned); err != nil {
		return u.stop(fmt.Errorf("recording whether the store returns static manifests: %w", err))
	}
	return nil
}

// engineContentType is the content type the pinned engine gives an object it
// uploads from a local file: the type registered for the name's extension,
// or the generic one. The static manifest is sent with the same type, so
// writing it over the engine's manifest does not change what the object
// says it is.
func engineContentType(name string) string {
	if contentType := mime.TypeByExtension(path.Ext(name)); strings.ContainsRune(contentType, '/') {
		return contentType
	}
	return "application/octet-stream"
}

// namesExactly reports whether a manifest read back lists exactly these
// whole segments, in order, with their tags and sizes.
func namesExactly(entries []swiftcontrol.SLOEntry, segments []swiftcontrol.StaticSegment) bool {
	if len(entries) != len(segments) {
		return false
	}
	for i, entry := range entries {
		got, ok := entry.(swiftcontrol.ObjectSegment)
		want := segments[i]
		if !ok || got.Range != nil || got.NestedSLO || got.ETag == nil ||
			got.Path != (swiftcontrol.ObjectRef{Container: want.Container, Object: want.Name}) ||
			!strings.EqualFold(strings.Trim(*got.ETag, `"`), want.ETag) || got.Size != uint64(want.Bytes) {
			return false
		}
	}
	return true
}

// chunk is one piece of the local file as the engine split it.
type chunk struct {
	size int64
	md5  string
}

// fileChunks reads the file once and returns each chunk's size and MD5,
// split by the chunk size the engine was given, the last chunk short when
// the file is not a multiple of it (ADR 0111 decision 3). A file whose
// size is no longer the planned size is refused: the segments were made
// from what the engine read, and this is the expectation they answer.
func fileChunks(filePath string, size, chunkBytes int64) ([]chunk, error) {
	if chunkBytes <= 0 {
		return nil, errors.New("no chunk size to pair the segments by")
	}
	file, err := os.Open(filePath)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	var chunks []chunk
	var total int64
	for {
		sum := md5.New()
		n, err := io.CopyN(sum, file, chunkBytes)
		if n > 0 {
			chunks = append(chunks, chunk{size: n, md5: hex.EncodeToString(sum.Sum(nil))})
			total += n
		}
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, err
		}
	}
	if total != size {
		return nil, fmt.Errorf("the file is %d bytes now and was %d bytes when the upload was planned", total, size)
	}
	return chunks, nil
}
