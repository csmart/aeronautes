# ADR 0103: a folder is uploaded as a folder, through the same selection and the same route

**Status:** Accepted for 0.5.0 (10 November 2026), delivered with this record's series.
Remediation item P27 names directory upload among the remaining accepted v1 scope, and the roadmap puts it in this release.
**Scope:** How the operator selects a local directory for upload, which object names its files take, what the walk includes and refuses, and what the dialog shows.
It changes neither the upload route, its preflight liveness and overwrite acknowledgement ([ADR 0081](0081-upload-overwrite-acknowledgement.md)), its records ([ADR 0031](0031-upload-item-recording.md)), its fixed point ([ADR 0085](0085-reobserved-destructive-identity.md), [ADR 0102](0102-versioning-matrix-fixed-point-and-archives.md)), nor the scoped selection token ([ADR 0041](0041-scoped-selection-capabilities.md)).

## Context

Upload takes a selection of files from the OS file chooser.
`TransferService.PickUploadFiles` opens the chooser under a title that names the resolved binding, holds the chosen paths behind a token bound to the bookmark's generation, the container and the prefix, and returns their base names for display.
The preflight stats each path, names it `prefix + basename`, refuses a path that is not a regular file, a duplicate name or a spelling the host would clean, resolves which names are live at the destination in one pass, and the start consumes the token.
Every later step keys on the item's name: the acknowledgement baseline, the predecessor record, the fixed-point comparison, the reclaim decision and the ledger.

The shell already had a directory chooser, which download uses to pick its destination, and the dialog manager offers no recursive file chooser.
Before this record a folder of files could not be uploaded without selecting its files one level at a time, and a folder with subfolders could not be uploaded at all.

## Decision

1. **One folder per selection, through the directory chooser, bound as a file pick is.**
   `TransferService.PickUploadDirectory` opens the directory chooser under the same binding title as the file pick, walks the chosen directory at the pick, and holds the files it found with the names they will take behind a token of the upload operation, so the preflight and the start consume it exactly as they consume a file selection.
   It returns the picked-files shape with the names for display and, new beside them, the folder's own name and its counts: files found, entries skipped and empty directories.
   A folder in which the walk finds no regular file returns that summary with no token and no names, and the dialog shows the folder and its counts with nothing to start, where today it returns before the preflight when no file was picked.
   The picked-files shape carries names and counts and no local path; the preflight report strips item paths as it does today, and a problem's detail may name a path as it does today.
2. **The folder arrives as a folder.**
   Each file's name is the prefix, the chosen folder's own name, a slash, and the file's path relative to that folder with the host's separator rewritten as a slash.
   The chooser picked a folder and not its contents, so a folder named `photos` uploaded into `albums/` is `albums/photos/...`, and two sibling folders picked into one prefix cannot collide at the root.
3. **The walk includes regular files and nothing else, and it never leaves the chosen tree.**
   The chosen path must pass the file pick's spelling rule: absolute, already clean, valid UTF-8; it is accepted as the chooser gave it, an alias included, as [ADR 0034](0034-download-destination-admission.md) accepts a selected download root.
   Within it, a symbolic link, to a file or to a directory, and any other non-regular entry is skipped and disclosed, because a tree commonly holds them and the operator cannot take them out of a folder pick; following a link could leave the tree the operator chose, which is the hazard the download root rule refuses.
   The walk descends through handles opened beneath the chosen root, as the download root does, and never through a pathname, so an entry replaced by a link between its listing and its inspection is skipped rather than followed; it reads each directory in bounded chunks and holds two handles per level, the directory and its listing, so what it keeps is the names it will upload and the counts, not the tree, and a tree deeper than its handle budget is refused.
   Hidden files are files.
   An empty directory produces nothing, because the store has no directories, and empty directories are counted.
   A directory that cannot be read, or any other failure of the walk, rejects the selection with the path named, so no partial tree is uploaded by accident.
   A file or a directory whose name is not valid UTF-8 rejects the selection the same way, since the store holds only UTF-8 names and leaving the entry out would be a silent hole in the folder; a link or special entry with such a name is skipped like any other.
   Everything the report retains is bounded ([ADR 0011](0011-typed-service-surface.md) admits no unbounded result array): a folder holding more than 10,000 files is refused at the pick with the count, because the preflight report crosses the bridge whole and the acknowledgement question names the collisions, 10,000 being the store's own listing page, and a larger folder is uploaded in parts; skipped entries are counted in full and named only up to the sample limit of [ADR 0056](0056-download-preflight-view.md), 200, with the count stated beside the sample and the dialog saying how many are not shown, and empty directories are counted and never named, so a folder of one file and ten thousand links yields one file, one count and two hundred names.
   The containment this gives is the walk's: the files are reopened when they are published, and a path replaced between the walk and that open uploads what the open finds, as [ADR 0080](0080-upload-content-binding.md) already states for a file pick.
4. **Nothing downstream changes but the staging name, which becomes the member's relative name.**
   An item is a path, a name and a size, and the walked files take the preflight's liveness pass, its duplicate rule, the overwrite acknowledgement, the publication routes, the fixed point, the records and the reclaim decision unchanged.
   The preflight takes explicit path-and-name sources; a file pick names its sources by base name through one stated rule, and the earlier path-based entry points remain only for the route's own tests.
   The engine route stages each source directory's files under the job's stage prefix by base name, so two members called `image.jpg` in different folders would stage to one object, and a retained stage from a failed or refused group would be overwritten by the next; the stage object is therefore the member's relative name under the stage prefix, each directory group staging under its own relative directory, which for a file pick is the base name it is today.
   The recorded staging location is the mapped one, carried through admission, publication and recovery as [ADR 0031](0031-upload-item-recording.md) records it.
5. **Surface and gate.**
   `PickUploadDirectory` joins the scenario inventory as a session row beside `PickUploadFiles`, with the same v1 classification, and its baseline change lands through the drift route of [ADR 0069](0069-preserve-covered-scenario-cells.md).
   Upload stays in the release as it is; no capability token is involved.
6. **The dialog offers both picks.**
   A "Choose folder" button stands beside "Choose files"; the picked summary names the folder and the number of files; the report renders as it does today, with the skipped entries and the empty-directory count among its warnings.

### Why these shapes

A flag on the existing pick method, choosing a folder instead of files, was considered and not taken: it would spare the drift route of decision 5 and misname the action, and the inventory exists to name actions.
Walking at the start instead of at the pick was considered and not taken: the report must say what will happen before anything is written, as it does for a file pick, and a walk at the pick is what lets it.
Following symbolic links was considered and not taken, for the reason the download root rule gives: a link followed by a parent component selects another tree than the one shown.
Creating directory markers for empty folders was considered and not taken: the product treats a marker as an object the operator made on purpose, and a folder pick is not that.

## Maintained proof

- Go, walk: the names and slashes for a nested tree; hidden files included; a link to a file and a link to a directory skipped and disclosed, with nothing outside the tree read; an empty directory counted and nothing produced; an unreadable directory rejecting with its path; a file whose name is not valid UTF-8 rejecting with its path; the file bound refused with the count; a folder of few files and many links reporting the full count with a bounded sample; the spelling rule applied to the chosen path; a folder with no regular file returning its summary alone.
- Go, route: two members with one base name in different folders, the first group's stage retained by a failed or refused publication and the later group run, with the retained bytes untouched and both recorded staging identities distinct and true.
- Go, service: pick, preflight and start with the fake picker answering a directory, the token bound to the scope and consumed by the start, the report's items carrying the folder's name, and the admission recording the mapped staging object.
- System: a small tree uploaded on the mock and the owned Swift, names and bytes checked by the oracle snapshot, with one name already live and acknowledged, so the replacement protocol is seen to apply unchanged.
- Frontend: the second button, the summary naming the folder and its counts, the skipped sample rendered among the warnings with the count of those not shown, and the no-file folder shown with nothing to start.

## Limits

One folder per selection; another folder is another pick.
The spelling and separator rules are the Linux ones this release ships; the Windows release owns its own.
The walk retains the names it will upload, bounded by the file count, and counts the rest; nothing here changes what a file pick does beyond the staging name, which for a file pick is unchanged.
