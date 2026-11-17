package local

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/csmart/aeronautes/internal/transfer/rclone"
)

// AnchoredEngine can start the child's destination resolution at a held
// directory instead of a pathname where the platform provides it. The
// dst pathname is display text for wrappers; anchor is the authority.
type AnchoredEngine interface {
	Engine
	CopyInto(ctx context.Context, src, dst string, anchor *os.File, names []string, onEvent func(rclone.Event)) (*rclone.Result, error)
}

// DestinationRoot is the held identity of a picker-selected directory.
// The handle is the authority for every download effect; the captured
// pathname is display and record text only. A directory renamed after
// the pick keeps receiving effects through the handle, and a pathname
// that stops naming it is reported rather than resolved again.
type DestinationRoot struct {
	root     *os.Root
	path     string
	identity os.FileInfo
	// entry is what the captured pathname named at capture without
	// following links, so an accepted initial link spelling is not a
	// displacement while it still reaches the held directory.
	entry os.FileInfo
}

// OpenDestinationRoot captures the selected directory behind a handle.
// The pathname must be usable unchanged, exactly as admission requires.
func OpenDestinationRoot(path string) (*DestinationRoot, error) {
	if !validDownloadRoot(path) {
		return nil, errors.New(downloadPathRefusal)
	}
	root, err := openDownloadDestination(path)
	if err != nil {
		return nil, err
	}
	entry, err := os.Lstat(path)
	if err != nil {
		return nil, errors.Join(err, root.Close())
	}
	return bindDestinationRoot(root, path, entry)
}

func bindDestinationRoot(root *os.Root, path string, entry os.FileInfo) (*DestinationRoot, error) {
	identity, err := root.Stat(".")
	if err == nil && !identity.IsDir() {
		err = errors.New("the selected destination is not a directory")
	}
	if err != nil {
		return nil, errors.Join(err, root.Close())
	}
	if entry == nil {
		entry = identity
	}
	return &DestinationRoot{root: root, path: path, identity: identity, entry: entry}, nil
}

// Path is the exact picker spelling, for display and records.
func (r *DestinationRoot) Path() string { return r.path }

// SameDirectory reports whether both held roots name one directory,
// whatever spelling each pick used (ADR 0078).
func (r *DestinationRoot) SameDirectory(other *DestinationRoot) bool {
	return r != nil && other != nil && os.SameFile(r.identity, other.identity)
}

// Close releases the handle. Effects through a closed root fail.
func (r *DestinationRoot) Close() error { return r.root.Close() }

// Derive opens a second handle on the same directory through the held
// one, so a job owns a handle of its own whatever later happens to the
// pick that produced it.
func (r *DestinationRoot) Derive() (*DestinationRoot, error) {
	derived, err := r.root.OpenRoot(".")
	if err != nil {
		return nil, err
	}
	bound, err := bindDestinationRoot(derived, r.path, r.entry)
	if err != nil {
		return nil, err
	}
	if !os.SameFile(bound.identity, r.identity) {
		return nil, errors.Join(errors.New("derived handle does not name the held directory"), bound.Close())
	}
	return bound, nil
}

// Removed reports whether the held directory has been deleted since the
// pick. A deleted directory is not the directory picked in any
// namespace, so no effect goes into it.
func (r *DestinationRoot) Removed() (bool, error) {
	current, err := r.root.Stat(".")
	if err != nil {
		return false, err
	}
	return directoryUnlinked(current), nil
}

// Displaced reports whether the captured pathname no longer names the
// held directory the way it did at capture. A link planted at the name
// of a plain directory counts as displacement even when it points back
// at the directory; an accepted initial link spelling counts only once
// the link itself changes or stops reaching the held directory.
func (r *DestinationRoot) Displaced() (bool, error) {
	current, err := os.Lstat(r.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return true, nil
		}
		return false, err
	}
	if !os.SameFile(current, r.entry) {
		return true, nil
	}
	if current.Mode()&os.ModeSymlink == 0 {
		return false, nil
	}
	resolved, err := os.Stat(r.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return true, nil
		}
		return false, err
	}
	return !os.SameFile(resolved, r.identity), nil
}

// openDirectory opens the directory at rel beneath the held root one
// component at a time. Every component is inspected through the parent
// handle before it is opened, links and non-directories are refused,
// a component on another filesystem is refused, and the opened
// directory must be the inspected entry. With create set, an absent
// component is created; a component that appears between the check
// and the creation is inspected like any other. The returned handle is
// bound to the final directory; the caller closes it. The second result
// names, in order and relative to the held root, the components this
// walk actually created: an entry that already existed or that another
// writer won is not one of them, and a walk that fails part way still
// reports the prefix it created before failing (ADR 0043).
func (r *DestinationRoot) openDirectory(rel string, create bool) (*os.Root, []string, error) {
	current, err := r.root.OpenRoot(".")
	if err != nil {
		return nil, nil, err
	}
	if rel == "" || rel == "." {
		return current, nil, nil
	}
	if filepath.IsAbs(rel) || filepath.VolumeName(rel) != "" || filepath.Clean(rel) != rel {
		return nil, nil, errors.Join(fmt.Errorf("destination path %q is not a clean relative path", rel), current.Close())
	}
	display := r.path
	relative := ""
	var created []string
	for _, component := range strings.Split(rel, string(os.PathSeparator)) {
		display = filepath.Join(display, component)
		relative = filepath.Join(relative, component)
		next, made, err := r.openComponent(current, component, display, create)
		if made {
			created = append(created, relative)
		}
		closeErr := current.Close()
		if err != nil {
			return nil, created, errors.Join(err, closeErr)
		}
		if closeErr != nil {
			return nil, created, errors.Join(closeErr, next.Close())
		}
		current = next
	}
	return current, created, nil
}

// openComponent inspects and opens one component beneath parent. Errors
// name the entry by its captured pathname, display, so reports keep
// naming what the operator can find, while the effect itself never
// resolves that pathname.
// componentCreated reports whether this call created the component: the
// entry was absent when it was inspected and this call's own Mkdir
// succeeded. A component another writer created in the same window
// fails that Mkdir with ErrExist and is not a creation by this
// executor, so creation is observed and never inferred from the
// directory existing afterwards (ADR 0043).
func componentCreated(absent bool, mkdirErr error) bool {
	return absent && mkdirErr == nil
}

// The second result is true only when this call created the component:
// the entry was absent and the Mkdir succeeded. A component another
// writer created in the same window returns ErrExist and is not a
// creation by this executor, so creation is observed rather than
// inferred from the directory existing afterwards (ADR 0043).
func (r *DestinationRoot) openComponent(parent *os.Root, component, display string, create bool) (*os.Root, bool, error) {
	if component == "" || component == "." || component == ".." {
		return nil, false, fmt.Errorf("destination component %q is not a plain name", display)
	}
	made := false
	info, err := parent.Lstat(component)
	if absent := err != nil && create && downloadChildAbsent(err); absent {
		mkdirErr := parent.Mkdir(component, 0o755)
		if mkdirErr != nil && !errors.Is(mkdirErr, os.ErrExist) {
			return nil, false, atPath(mkdirErr, "mkdir", display)
		}
		made = componentCreated(absent, mkdirErr)
		info, err = parent.Lstat(component)
	}
	if err != nil {
		return nil, made, atPath(err, "lstat", display)
	}
	if !downloadOrdinaryDirectory(info) {
		return nil, made, fmt.Errorf("destination component %q is not an inspectable ordinary directory; descendant links and reparse entries are refused", display)
	}
	if !sameDevice(r.identity, info) {
		return nil, made, fmt.Errorf("destination component %q is on another filesystem; mount points inside the selected directory are refused", display)
	}
	if err := refuseMountRoot(parent, component); err != nil {
		return nil, made, atPath(err, "statx", display)
	}
	opened, err := parent.OpenRoot(component)
	if err != nil {
		return nil, made, atPath(err, "open", display)
	}
	identity, err := opened.Stat(".")
	if err == nil && !os.SameFile(identity, info) {
		err = fmt.Errorf("destination component %q changed between inspection and open", display)
	}
	if err != nil {
		return nil, made, errors.Join(atPath(err, "stat", display), opened.Close())
	}
	return opened, made, nil
}

// atPath renames a handle-relative path error to the entry's captured
// pathname, keeping the underlying cause for errors.Is.
func atPath(err error, op, display string) error {
	var pathErr *os.PathError
	if errors.As(err, &pathErr) {
		return &os.PathError{Op: op, Path: display, Err: pathErr.Err}
	}
	return err
}
