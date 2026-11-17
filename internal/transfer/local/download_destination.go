package local

import (
	"errors"
	"fmt"
	"os"
	"strings"
)

func openDownloadDestination(path string) (*os.Root, error) {
	root, err := os.OpenRoot(path)
	if err != nil {
		return nil, err
	}
	info, err := root.Stat(".")
	if err == nil && !info.IsDir() {
		err = errors.New("the selected destination is not a directory")
	}
	if err != nil {
		return nil, errors.Join(err, root.Close())
	}
	return root, nil
}

func inspectDownloadPath(root *os.Root, item DownloadItem) *Problem {
	if item.Dir && item.RelPath == "" {
		return nil
	}
	refuse := func(kind, detail string) *Problem {
		return &Problem{Name: item.Name, Kind: kind, Detail: detail, Reject: true}
	}
	components := strings.Split(item.RelPath, string(os.PathSeparator))
	current := ""
	for index, component := range components {
		if index != 0 {
			current += string(os.PathSeparator)
		}
		current += component
		info, err := root.Lstat(current)
		if err != nil {
			if downloadChildAbsent(err) {
				return nil
			}
			return refuse("local-inspection", fmt.Sprintf("cannot inspect destination component %q: %v", current, err))
		}
		final := index == len(components)-1
		if final && !item.Dir {
			if info.IsDir() {
				return refuse("local-exists", item.RelPath+" is an existing DIRECTORY under the destination; a file cannot land on it")
			}
			if !info.Mode().IsRegular() {
				return refuse("local-exists", item.RelPath+" already exists under the destination and is not a regular file; a download replaces only a file")
			}
			problem := refuse("local-exists", item.RelPath+" already exists under the destination; a download replaces a file only when you ask it to")
			problem.replaceable = true
			return problem
		}
		if !downloadOrdinaryDirectory(info) {
			kind := "local-parent"
			if final {
				kind = "local-exists"
			}
			return refuse(kind, fmt.Sprintf("destination component %q is not an inspectable ordinary directory; descendant links and reparse entries are refused", current))
		}
	}
	return nil
}
