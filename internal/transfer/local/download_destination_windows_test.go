package local

import (
	"fmt"
	"os"
	"syscall"
	"testing"

	"golang.org/x/sys/windows"
)

type downloadDestinationWindowsInfo struct {
	os.FileInfo
	mode os.FileMode
	sys  any
}

func (i downloadDestinationWindowsInfo) Mode() os.FileMode { return i.mode }
func (i downloadDestinationWindowsInfo) Sys() any          { return i.sys }

func TestDownloadDestinationWindowsErrors(t *testing.T) {
	for _, code := range []syscall.Errno{syscall.ERROR_FILE_NOT_FOUND, syscall.ERROR_PATH_NOT_FOUND, windows.ERROR_BAD_NETPATH, syscall.ERROR_ACCESS_DENIED, windows.ERROR_INVALID_HANDLE} {
		t.Run(fmt.Sprint(code), func(t *testing.T) {
			err := &os.PathError{Op: "lstat", Path: "selected", Err: code}
			if absent := downloadChildAbsent(err); absent != (code == syscall.ERROR_FILE_NOT_FOUND) {
				t.Fatalf("ambiguous Windows inspection result became creation authority: %v absent=%v", err, absent)
			}
		})
	}
}

func TestDownloadDestinationWindowsReparse(t *testing.T) {
	ordinary := &syscall.Win32FileAttributeData{FileAttributes: syscall.FILE_ATTRIBUTE_DIRECTORY}
	reparse := &syscall.Win32FileAttributeData{FileAttributes: syscall.FILE_ATTRIBUTE_DIRECTORY | syscall.FILE_ATTRIBUTE_REPARSE_POINT}
	for _, entry := range []struct {
		name string
		mode os.FileMode
		sys  any
		want bool
	}{
		{"ordinary", os.ModeDir, ordinary, true},
		{"symlink", os.ModeSymlink, reparse, false},
		{"junction", os.ModeIrregular, reparse, false},
		{"regular_reparse", 0, reparse, false},
		{"directory_reparse", os.ModeDir, reparse, false},
		{"missing_metadata", os.ModeDir, nil, false},
		{"unexpected_metadata", os.ModeDir, struct{}{}, false},
		{"nil_metadata", os.ModeDir, (*syscall.Win32FileAttributeData)(nil), false},
		{"non_directory", 0, ordinary, false},
	} {
		t.Run(entry.name, func(t *testing.T) {
			info := downloadDestinationWindowsInfo{mode: entry.mode, sys: entry.sys}
			if actual := downloadOrdinaryDirectory(info); actual != entry.want {
				t.Fatalf("Windows descendant type policy differs: accepted=%v want=%v", actual, entry.want)
			}
		})
	}
}
