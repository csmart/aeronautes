package local

import (
	"errors"
	"os"
	"syscall"
)

func downloadChildAbsent(err error) bool {
	// Windows also calls bad network paths and ambiguous parent failures
	// "not exist". Neither establishes a missing child we can create.
	return errors.Is(err, syscall.ERROR_FILE_NOT_FOUND)
}

func downloadOrdinaryDirectory(info os.FileInfo) bool {
	attributes, ok := info.Sys().(*syscall.Win32FileAttributeData)
	// Mode bits alone miss some reparse kinds and depend on winsymlink.
	if !ok || attributes == nil || attributes.FileAttributes&syscall.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
		return false
	}
	return info.Mode().Type() == os.ModeDir
}
