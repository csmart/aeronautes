package local

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

// UploadSource is one file an upload selection names: where it is on the
// local disk, and the object name it takes under the request's prefix. A
// file pick names its base name; a folder pick names the folder and the
// file's path below it, slash-separated (ADR 0103 decision 2).
//
// The name's last component is the file's own base name. The engine stages a
// file under its base name, so two sources staging apart (decision 4) rests
// on it, and the preflight refuses a source that breaks it.
type UploadSource struct {
	Path string
	Name string
}

// DirectorySelection is what a walk found under a chosen folder (ADR 0103
// decision 3): the files it will upload, bounded by the file limit, and the
// counts of what it left out, with a bounded sample of the skipped entries'
// names relative to the folder.
type DirectorySelection struct {
	// Folder is the chosen directory's own name as the chooser spelt it,
	// the top level of every source Name.
	Folder string
	// Sources are in directory order, as each directory listed them: the
	// walk reads in bounded chunks and never holds a directory whole to
	// sort it, and nothing downstream depends on the order.
	Sources []UploadSource
	// Skipped counts what the walk leaves out: links and other non-regular
	// entries, which it never follows, and a download's own stage directory
	// and partial files. SkippedSample names the first maxFailures of them.
	Skipped       int
	SkippedSample []string
	// EmptyDirectories counts directories with no entry at all, which
	// produce nothing because the store has no directories.
	EmptyDirectories int
}

// MaxDirectoryFiles is the most files one folder pick may hold (ADR 0103
// decision 3): the preflight report crosses the bridge whole and the
// acknowledgement question names the collisions, and 10,000 is the store's
// own listing page.
const MaxDirectoryFiles = 10000

const (
	// walkReadChunk bounds the entries one directory read returns, so a
	// directory of any size costs one chunk of memory at a time.
	walkReadChunk = 256
	// maxFolderDepth bounds the open handles a descent holds, two per level,
	// the directory and its listing; a tree deeper than this is refused
	// rather than walked.
	maxFolderDepth = 128
)

// errNotOrdinaryDirectory marks an entry that was listed as a directory and
// is a link or other special entry when inspected by handle.
var errNotOrdinaryDirectory = errors.New("not an ordinary directory")

// WalkUploadDirectory enumerates the regular files under root for an
// upload. The root must pass the file pick's spelling rule and is opened as
// the chooser gave it, an alias included; every descent below it goes
// through a handle beneath the opened root and never through a pathname, so
// an entry replaced by a link between its listing and its inspection is
// skipped rather than followed. A link, to a file or a directory, and any
// other non-regular entry is skipped and counted. Directories are read in
// bounded chunks and the walk holds one handle per level, so the memory it
// keeps is the upload names it will return and nothing proportional to the
// tree. A directory that cannot be read, a tree deeper than maxFolderDepth,
// or more than limit files is an error rather than a partial selection, and
// a cancelled context ends the walk where it stands.
func WalkUploadDirectory(ctx context.Context, root string, limit int) (*DirectorySelection, error) {
	if !validUploadPath(root) {
		return nil, errors.New(uploadPathRefusal)
	}
	// The spelling is taken as the chooser gave it, an alias followed once
	// here; everything below goes through the opened handle.
	info, err := os.Stat(root)
	if err != nil {
		return nil, fmt.Errorf("the selected folder could not be read: %w", err)
	}
	if !info.IsDir() {
		return nil, errors.New("the selected path is not a folder")
	}
	opened, err := os.OpenRoot(root)
	if err != nil {
		return nil, fmt.Errorf("the selected folder could not be opened: %w", err)
	}
	defer opened.Close()
	w := &folderWalk{root: root, limit: limit, selection: &DirectorySelection{Folder: filepath.Base(root)}}
	if err := w.descend(ctx, opened, ""); err != nil {
		return nil, err
	}
	return w.selection, nil
}

type folderWalk struct {
	root      string
	limit     int
	selection *DirectorySelection
	depth     int
}

// sourcePath is an entry's pathname under the chosen root. The walk itself
// never resolves it: it descends by handle and uses this only to name an
// entry in an error. It is also the path each source carries out of the walk,
// which the upload opens later; that later open is the limit ADR 0103
// decision 3 states.
func (w *folderWalk) sourcePath(rel string) string {
	return filepath.Join(w.root, filepath.FromSlash(rel))
}

// descend reads the directory the handle is bound to in bounded chunks and
// visits each entry; subdirectories are opened beneath the handle.
func (w *folderWalk) descend(ctx context.Context, dir *os.Root, rel string) error {
	if w.depth >= maxFolderDepth {
		return fmt.Errorf("the folder nests more than %d levels deep at %s", maxFolderDepth, w.sourcePath(rel))
	}
	w.depth++
	defer func() { w.depth-- }()
	listing, err := dir.Open(".")
	if err != nil {
		return fmt.Errorf("the folder could not be read in full: %w", atPath(err, "open", w.sourcePath(rel)))
	}
	defer listing.Close()
	seen := false
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		entries, err := listing.ReadDir(walkReadChunk)
		for _, entry := range entries {
			seen = true
			if err := w.visit(ctx, dir, rel, entry); err != nil {
				return err
			}
		}
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return fmt.Errorf("the folder could not be read in full: %w", atPath(err, "readdir", w.sourcePath(rel)))
		}
	}
	if !seen {
		w.selection.EmptyDirectories++
	}
	return nil
}

func (w *folderWalk) visit(ctx context.Context, parent *os.Root, rel string, entry fs.DirEntry) error {
	childRel := path.Join(rel, entry.Name())
	// A download the application writes itself keeps a partial file beside
	// the file it will become until it has it whole. A folder that holds
	// one is uploaded without it, disclosed with the other skipped entries:
	// it is this application's own unfinished work, and with several jobs
	// at once the download may still be writing it (ADR 0105 decision 5).
	if strings.HasPrefix(entry.Name(), downloadPartPrefix) {
		w.skip(childRel)
		return nil
	}
	if (entry.IsDir() || entry.Type().IsRegular()) && !utf8.ValidString(entry.Name()) {
		// The store holds only UTF-8 names, and a file left out for its name
		// would be a silent hole in the folder, so the pick is refused with the
		// entry named, its undecodable bytes shown as replacement characters.
		return fmt.Errorf("%s has a name that is not valid UTF-8, which the store cannot hold; rename it and choose the folder again",
			strings.ToValidUTF8(w.sourcePath(childRel), "\uFFFD"))
	}
	switch {
	case entry.IsDir():
		// Listed as a directory; the open inspects it again by handle, so a
		// link that took the name meanwhile is skipped, not followed.
		child, err := openFolderComponent(parent, entry.Name(), w.sourcePath(childRel))
		if errors.Is(err, errNotOrdinaryDirectory) {
			w.skip(childRel)
			return nil
		}
		if err != nil {
			return fmt.Errorf("the folder could not be read in full: %w", err)
		}
		return errors.Join(w.descend(ctx, child, childRel), child.Close())
	case entry.Type().IsRegular():
		if len(w.selection.Sources) == w.limit {
			return fmt.Errorf("the folder holds more than %d files; choose a smaller folder or upload it in parts", w.limit)
		}
		w.selection.Sources = append(w.selection.Sources, UploadSource{
			Path: w.sourcePath(childRel), Name: w.selection.Folder + "/" + childRel})
	default:
		w.skip(childRel)
	}
	return nil
}

func (w *folderWalk) skip(rel string) {
	w.selection.Skipped++
	if len(w.selection.SkippedSample) < maxFailures {
		w.selection.SkippedSample = append(w.selection.SkippedSample, rel)
	}
}

// openFolderComponent opens one listed subdirectory beneath its parent's
// handle, the way the download root opens a destination component: it is
// inspected without following, refused as errNotOrdinaryDirectory when it
// is a link or other special entry, opened beneath the parent, and the
// opened directory must be the inspected one, so a substitution between
// the two is refused instead of followed.
func openFolderComponent(parent *os.Root, name, display string) (*os.Root, error) {
	info, err := parent.Lstat(name)
	if err != nil {
		return nil, atPath(err, "lstat", display)
	}
	if !downloadOrdinaryDirectory(info) {
		return nil, errNotOrdinaryDirectory
	}
	opened, err := parent.OpenRoot(name)
	if err != nil {
		return nil, atPath(err, "open", display)
	}
	identity, err := opened.Stat(".")
	if err == nil && !os.SameFile(identity, info) {
		err = fmt.Errorf("folder component %q changed between inspection and open", display)
	}
	if err != nil {
		return nil, errors.Join(atPath(err, "stat", display), opened.Close())
	}
	return opened, nil
}
