package local

import (
	"errors"
	"fmt"
	"io"
	"os"
)

type nativeDownloadFile interface {
	io.Writer
	Sync() error
	Close() error
}

func (r *Report) nativeDownloaded(item DownloadItem, placed bool, err error) {
	if placed {
		r.Done++
		r.Bytes += item.Size
		if err != nil {
			r.postCommitIssue(item.Name, "temporary-cleanup", err)
		}
	} else if err != nil && len(r.Failures) < maxFailures {
		r.Failures = append(r.Failures, Failure{Name: item.Name, Stage: "native-get", Err: err.Error()})
	}
}

func finishNativeDownload(body io.ReadCloser, temp nativeDownloadFile, temporary, target string, wantSize int64) (placed bool, err error) {
	defer func() {
		if cleanupErr := os.Remove(temporary); cleanupErr != nil {
			err = errors.Join(err, fmt.Errorf("remove download temporary file: %w", cleanupErr))
		}
	}()
	written, err := io.Copy(temp, body)
	if err == nil && written != wantSize {
		err = fmt.Errorf("wrote %d bytes, object says %d — refusing to place a short file", written, wantSize)
	}
	if err == nil {
		err = temp.Sync()
	}
	// Swift can report corruption only on Close after a successful read.
	// Both handles must close successfully before the final name appears.
	err = errors.Join(err, temp.Close(), body.Close())
	if err != nil {
		return false, err
	}
	// A concurrent creator keeps its target; rename would overwrite it.
	if err := os.Link(temporary, target); err != nil {
		return false, err
	}
	return true, nil
}
