//go:build !windows

package local

import (
	"errors"
	"os"
)

func downloadChildAbsent(err error) bool {
	return errors.Is(err, os.ErrNotExist)
}

func downloadOrdinaryDirectory(info os.FileInfo) bool {
	return info.Mode().Type() == os.ModeDir
}
