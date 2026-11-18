# Real-cloud journeys

During the 0.5.0 test run the operator found many bugs by hand, and manual testing does not scale (12 November 2026).
The existing suites test the Go services and the Svelte components separately, against the mock or SAIO; nothing walks the operator's journeys between the two real accounts.
This suite is layer 1 of 2: the same application services the buttons call, against the two real clouds.
Layer 2, at the end of this page, drives the real frontend in a browser (ADR 0112).

## Where and how it runs

- Package `internal/journeys`, build tag `realcloud`, never in the default build, never in CI, never a required check.
- Run locally: `make journeys CLOUDS=/path/to/clouds.yaml [REPEAT=3] [ONLY=copy]`.
- Accounts: the clouds.yaml entries `aeronautes-ceph` and `aeronautes-swift`, read with the oracle's existing selection, validation and redaction rules; the loader moves from `internal/swiftcontrol`'s test files into a small shared internal package both use, unchanged in behaviour.
- Output never names the provider or the account and never prints a credential, a token, a body or server text: steps are labelled `ceph` and `swift`, failures report the error kind (the oracle's `oracleFailure`), and a JSON report of step outcomes is written to a local folder outside the repository.

## Safety

- Every container the run uses is one it created, named `aeronautes-journey-<random>`, with the oracle's rule: a create that finds the name already taken is not a creation, and that name is never written to or deleted.
- Large-object segments go to the exact sidecar `<container>_segments`, which is a second container. Before a step that can create it, the run reads that exact name; if it already exists the step does not start, and the sidecar is then never deleted. A sidecar the run's own step created is deleted with its container.
- Teardown deletes only the exact containers this run created and the exact sidecar of each that the run's steps created, and nothing else, even after a failure; it never lists the account and sweeps a prefix.
- No step lists, edits or deletes anything else on either account; a step that would is a bug in the suite.
- Account metadata is the one account-level write, and an account edit replaces the whole set: a key the edit does not carry is removed, and a quota of zero removes an existing quota. So every account edit the run makes, through the application or in teardown, starts from the account as just read: it carries every existing user-metadata key and the current quota unchanged and adds only the run's own key, `X-Account-Meta-Aeronautes-Journey-<random>`, which no earlier run or person can have set; teardown removes only that key, again carrying the rest and the quota through. TempURL keys are never written. A run that is killed before teardown can leave that one key behind; a later run does not touch it, and it can be removed by hand.
- The run never opens the operator's settings file, database or keyring. It creates one temporary directory before any service starts and gives the services explicit paths inside it for both `settings.json` and the database, with a session-only secret store for the bookmarks; nothing reads `XDG_CONFIG_HOME`, `XDG_DATA_HOME` or `AERONAUTES_STATE_DIR`.
- The harness has its own default-build tests, against the mock: a container name that already exists and a sidecar that already exists receive no destructive request, and teardown sends deletes only for what the run created.

## Harness

- The services are wired as `main.go` wires them (transfer, delete, mutate, browse, inspector, jobs, settings, remembered settings), over the temporary database.
- Native seams are stood in through the existing `Wire*` functions: the file picker returns a temporary folder; the overwrite and public-read confirmers answer as the step scripts.
- Every result is checked through an independent read: a separate `swiftcontrol.Session` lists, HEADs and downloads what a step claims, and compares sizes and digests with the local fixture; the code under test never marks its own work.

## Journeys (each on both clouds, and across them where it says so)

1. Upload a folder of small files and one file larger than the chunk size (segmented), then re-upload with overwrite acknowledged.
   The segmented file is chosen static in odd rounds and dynamic in even rounds, so two rounds or more cover both choices on both clouds (ADR 0111).
   A static choice is published static on the Swift cloud and dynamic on the Ceph RGW cloud, which does not return static manifests (decision 10).
2. Copy a whole container from one cloud to the other, with two objects, one of them written in segments, carrying metadata of their own that has to arrive with them, and run the same copy again, which has to write nothing; copy a selection of two of N; copy within a container into a subfolder; copy a folder up to the container's top.
3. Download a folder, a selection and a large object; compare every file's digest with the source, and check that the folder the copy is downloaded into holds the files and nothing else.
4. Edit object and container metadata and the run's own account metadata key, and read them back; set and clear a read ACL on the run's container with the acknowledgement.
5. Delete one object, a marker, a manifest with its segments, a selection, a prefix, a container's contents and the container.
6. Switch between the two bookmarks repeatedly; check that the listing and remembered settings follow the account and that no sign-in repeats.
7. Settings: save, restart the services over the same temporary files, read back; export and restore the backup, inside the temporary directory.

Each journey runs `REPEAT` times (default 3) so intermittent store faults, like the 502 on a large copy, show up as a count rather than a one-off.

## What it does not do

It does not drive the window, the native dialogs or the Svelte code; layer 2 drives the Svelte code.
It does not measure performance.

## Layer 2: UI journeys in a browser

- Run locally: `make uijourneys [CLOUDS=/path/to/clouds.yaml]`. Without `CLOUDS` it runs against the mock only.
- It needs Go, Node with npm, python3, the pinned rclone on `PATH` (as layer 1 does) and network access to fetch Playwright's Chromium on the first run.
  The build it makes runs in a browser, so it needs no C compiler and none of the desktop's GTK or WebKitGTK packages.
  When a build step fails, for want of one of these or otherwise, the run says so and names the `build.log` in its private directory.
- Arguments go to Playwright: `make uijourneys ARGS="--project=mock tests/slash.spec.ts"` runs one journey on the mock, and `ARGS=--headed` shows the browser, which needs a display.
- `scripts/uijourneys.sh` builds the application with the tags `server` and `uijourney`, starts it and the mock on 127.0.0.1 with `HOME` and the XDG directories in a fresh directory, and runs the Playwright suite in `uijourneys/` against it.
- The `uijourney` build is the shipped application with only its native seams replaced: the pickers and the two confirmations answer what the suite scripted over a token-guarded control listener, and an unscripted pick or answer fails; the secret store is session-only.
- Real-cloud bookmarks are made in Go from clouds.yaml, so no password reaches the page; the bookmark editor is driven only against the mock.
- Every container is one the run created, through layer 1's ownership record, and teardown removes only those and their claimed sidecars.
  What a deletion left is read by the run's own session, not by the application: the control listener lists a container the run created, or a sidecar it claimed, and refuses any other.
  Teardown stops a container keeping versions before it deletes from it, because a store archives each object deleted from one that does.
- Output names only each journey, its target (`mock`, `ceph` or `swift`) and the private directory under the user cache directory, which keeps the failure details, a screenshot and the page's text, and the application's log.

The journeys, on each target: create a bookmark and connect (mock only); duplicate it and connect with the copy (mock only); import bookmarks from an rclone.conf, one verified and one flagged, through Settings (mock only); import from the rail under a typed name, and give a bookmark that was imported without a key its key (mock only); upload a folder with a segmented file, check the large-object disclosure against the uploaded object, browse it, and see a copy of the folder start under the folder's name; upload it again with the overwrite confirmed; download it and compare digests with the second upload; edit an object's metadata and read it back after a reload; make the container public and private again; delete an object, then the prefix with the segments of its large object, then the container, and follow the dialog to the segments container, find it empty and delete that; delete a static and a dynamic large object, each from its own dialog with its segments, and then a container with the segments of its large object, leaving the option ticked each time; where the store keeps versions, which of the three targets is the Swift cloud, give a container a history location, delete an object and a large object from it, then the container, then its archive; open a copy from a container's context menu; see the "/" folder at the top of a container that holds names starting with "/"; find a container named like a segment store behind the "Possible backstage" heading, with the caution above its listing and in its delete dialog; see a container made outside the window once the column's Refresh is pressed; scan every container in the All view; and, with both clouds, switch between them.

## What the browser journeys cover

This table is the list the browser suite is built up against: one row per thing the window offers, with the journey that drives it, or "not yet".
A row is covered only when a journey does it through the window against a store; a component test against a fake does not count here.
A change that adds, changes or removes something the window offers changes its row in the same pull request ([AGENTS.md](../AGENTS.md)).
Where a row says "not yet" and layer 1 runs the same service against the real clouds, the row names that layer 1 journey by its number in the list above.

| Area | Through the window | Journey | How the outcome is checked |
| --- | --- | --- | --- |
| Bookmarks | create a TempAuth bookmark in the editor and connect | create a bookmark in the editor and connect (mock only) | the account's containers are listed |
| Bookmarks | import bookmarks from an rclone.conf | import bookmarks from an rclone config (mock only) | the dialog reports one verified and one that did not verify, the rail flags the second, and the first connects |
| Bookmarks | start the import from the rail and name a bookmark in its preview | import from the rail, name a bookmark and give a keyless one its key (mock only) | the rail shows the bookmark under the typed name and not under its source's |
| Bookmarks | a bookmark with no saved key: its mark, its editor and its key | import from the rail, name a bookmark and give a keyless one its key (mock only) | choosing it opens the editor on the key field; with the key entered it connects and the mark is gone |
| Bookmarks | duplicate a bookmark | duplicate a bookmark and connect with the copy (mock only) | the copy holds the first's details and no key, and connects once its key is entered |
| Bookmarks | switch between accounts from the rail | switch between the two clouds (both clouds) | each account lists its own container and not the other's |
| Bookmarks | edit, verify or delete a bookmark; the Keystone fields; import from a clouds.yaml; the import from the welcome dialog | not yet | |
| Containers | list an account's containers | every journey that opens a container | the run's own container is in the list |
| Containers | read the list again with the column's refresh | refresh the container list after a change made elsewhere | a container made by the run's own session, outside the window, appears |
| Containers | the list read again when the window is focused | not journeyed: a browser cannot show what the desktop window does; test plan step 19k | |
| Containers | create a container | find a container named like a segment store behind its heading | its row is listed |
| Containers | the closed "Possible backstage" group and the segment-store caution | find a container named like a segment store behind its heading | the row is absent until the group is opened; the caution is read as a mark on the path's row and in the delete dialog |
| Containers | the All view, and "Scan" over every loaded container | scan every container in the All view | the count the scan reaches is the number of rows, and no row is left marked "not read" |
| Containers | the Backstage mode; load more containers; what each settings badge says; stopping a scan | not yet | |
| Listing | open a folder and go up | upload a folder with a segmented file and browse it | the uploaded names are listed |
| Listing | the "/" folder at a container's top, and an upload into a typed prefix | see the slash folder at the top of a container | the folder, and the upload inside it, are listed |
| Listing | selecting rows leaves the toolbar and the list where they were | a selection of a file and a folder is copied, downloaded and deleted with the folder's contents | the list and three of the toolbar's buttons are in the same place with nothing selected, with one object selected and read, and with two rows selected |
| Listing | "Count objects" beside the refresh, for one selected folder | a selection of a file and a folder is copied, downloaded and deleted with the folder's contents | the answer on the path's row names the folder and its one object, and the list and the toolbar's buttons have not moved |
| Listing | load more objects; the filter; "Find in container"; marker folders; Escape clearing a selection | not yet | |
| Inspector | an object's large-object kind | upload a folder with a segmented file and browse it | it is the kind the job recorded |
| Inspector | an object's user metadata; a container's public notice | edit an object's metadata; make the container public and private again | read after connecting again |
| Inspector | the account card; segment tables; "Open segments backstage"; raw headers | not yet | |
| Edit | add a key to an object's metadata | edit an object's metadata | read back after connecting again |
| Edit | make a container public, with the native confirmation answered by the run, and private again | make the container public and private again | the inspector's sentence after connecting again |
| Edit | give a container a history location and its archive | a history location keeps what is deleted, and every segment (Swift cloud only) | the delete dialogs that follow name the archive, and what is deleted arrives there |
| Edit | the static website preset and the quota fields greyed, with the reason, where the store's `/info` does not list them | make the container public and private again (mock only) | each control is disabled and the sentence naming `staticweb` is shown |
| Edit | container metadata, quotas, a versions location, CORS and TempURL keys; account metadata; a large object's metadata; an edit that conflicts | not yet; layer 1 journey 4 edits object, container and account metadata | |
| Upload | a folder holding a file that uploads in segments, with a per-job chunk size | upload a folder with a segmented file and browse it | the job completes, the uploaded names are listed, and the inspector shows the large-object kind the job recorded |
| Upload | replace existing objects that differ, with the native confirmation answered by the run | upload it again and confirm the overwrite | the download that follows returns the second upload's bytes |
| Upload | the kind of large object chosen for one upload | delete a static and a dynamic large object, each with its segments | each object's delete dialog names the kind of manifest the job recorded |
| Upload | chosen files instead of a folder | not yet | |
| Download | a whole container into a local folder | download it back | each file's SHA-256 is that of the file uploaded |
| Download | replace existing files, with the native confirmation answered by the run, over files that are already the same | download it again over the same files and confirm the replacement | the job says every file is already the same, and each file's SHA-256 is unchanged |
| Download | one object, a folder or a selection | not yet; layer 1 journey 3 | |
| Copy | open the copy dialog from a container's menu | open a copy from a container's context menu | the dialog opens for that container; nothing is sent |
| Copy | a folder's copy starts under the folder's own name | upload a folder with a segmented file and browse it | for the store's own folder row, the copy dialog's destination path starts as the folder's name; nothing is sent |
| Copy | a selection of a file and a folder, taken with everything in the folder | a selection of a file and a folder is copied, downloaded and deleted with the folder's contents | the copy's folder holds the file and the folder's file, and each downloaded file's SHA-256 is that of the file uploaded |
| Download | a selection of a file and a folder | the same journey | the same |
| Copy | run a copy, within an account or across two; create the destination container; replace existing objects | not yet; layer 1 journey 2 runs the copies | |
| Jobs | follow a job to its end and read its details | the upload and download journeys, and the prefix and container deletes of the delete journey (an object delete is not a job) | the job's state is "complete" |
| Jobs | pause, resume and cancel a job; per-object records; history and "Recent changes" | not yet | |
| Delete | an object, confirmed by typing `delete` and its name | delete an object, the prefix and the container | its row is gone |
| Delete | a selection of a file and a folder, the folder with everything in it, typed as `delete` and the number of rows | a selection of a file and a folder is copied, downloaded and deleted with the folder's contents | the dialog counts what the folder holds, and the listing is empty after the job |
| Delete | a prefix and a container, each from its own action and with its own typed words | delete an object, the prefix and the container | each dialog is headed by its one job; the listing is empty, and the container is gone after connecting again |
| Delete | a container named like the open one's segments container is named before a deletion and offered after it | delete an object, the prefix and the container | the notice is read in the dialog, its button opens that container, and neither container is listed once the run has deleted that one too |
| Delete | a prefix with the segments of its large objects, the option left ticked and its words typed | delete an object, the prefix and the container | a fresh preview of the segments container, once the dialog has led there, counts no objects |
| Delete | one large object with its segments, the option left ticked and its name typed | delete a static and a dynamic large object, each with its segments | the run's own session lists the other object's segments and none of the deleted one's |
| Delete | a container with the segments of its large objects, the option left ticked | delete the container with the segments of its large object | the run's own session lists no segment, and the container is gone after connecting again |
| Delete | an object and a large object where the container keeps history, with no segments option offered | a history location keeps what is deleted, and every segment (Swift cloud only) | the run's own session finds each in the archive with its deletion marker, and every segment still listed |
| Delete | a container that keeps history, then its archive with the option ticked | a history location keeps what is deleted, and every segment; deleting the archive takes the versions and their segments (Swift cloud only) | the archive holds what the container held and every segment is still listed; then neither container is listed and no segment is |
| Delete | "Empty container…" from a container's menu | find a container named like a segment store behind its heading | the dialog opens with the caution; nothing is sent |
| Delete | the segments option unticked by hand, or in another container's archive; emptying a container carried through; a selection's segments option; a marker | not yet; layer 1 journey 5 | |
| Menus | a container row's menu | open a copy from a container's context menu; find a container named like a segment store behind its heading | the chosen entry opens its dialog |
| Menus | an object's and a folder's menu; "Copy name" | not yet | |
| Settings | open Settings and import from it | import bookmarks from an rclone config (mock only) | as for the import above |
| Settings | change and save a setting; back up and restore the configuration | not yet; layer 1 journey 7 | |
| Window | the About and welcome dialogs; credential status; use by keyboard alone | not yet | |
