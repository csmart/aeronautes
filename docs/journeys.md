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

1. Upload a folder of small files and one file larger than the chunk size (segmented) to one cloud (`TestCopyJourney`).
   The segmented file is chosen static in odd rounds and dynamic in even rounds, so two rounds or more cover both choices on both clouds (ADR 0111).
   A static choice is published static on the Swift cloud and dynamic on the Ceph RGW cloud, which does not return static manifests (decision 10).
2. Copy that container to the other cloud, with metadata of their own that has to arrive with the objects, and the same copy again, which has to write nothing; copy two selected files (`TestCopyJourney`); within a cloud, copy a folder to a sibling folder, up to the container's top, and two selected files into a folder inside their own (`TestCopyWithinJourney`).
3. Download the copy back and compare every file's digest with the source, and check the folder holds those files and nothing else (`TestCopyJourney`).
4. Edit object and container metadata and the run's own account metadata key, and read them back; set and clear a read ACL with the acknowledgement (`TestMetadataJourney`).
5. Delete one object, a selection, a prefix, a container's contents and the container, checking after each that exactly what was meant is gone and everything else is still there (`TestDeleteJourney`).
6. Switch between the two bookmarks repeatedly; check that the listing and remembered settings follow the account (`TestSwitchJourney`).

Settings saved, read back after a restart, backed up and restored are tested by the browser journeys on the mock and by the Go tests, not here.
Re-uploading with the overwrite acknowledged, downloading a selection or a large object alone, and deleting a marker, a manifest or segments are browser journeys.

Each journey runs `REPEAT` times (default 3) so intermittent store faults, like the 502 on a large copy, show up as a count rather than a one-off.

## What it does not do

It does not drive the window, the native dialogs or the Svelte code; layer 2 drives the Svelte code.
It does not measure performance.

## Layer 2: UI journeys in a browser

- Run locally: `make uijourneys [CLOUDS=/path/to/clouds.yaml]`. Without `CLOUDS` it runs against the mock only.
- It needs Go, Node with npm, python3, the pinned rclone on `PATH` (as layer 1 does) and network access to fetch Playwright's Chromium on the first run.
  The build it makes runs in a browser, so it needs no C compiler and none of the desktop's GTK or WebKitGTK packages.
  When a build step fails, for want of one of these or otherwise, the run says so and names the `build.log` in its private directory.
- Beyond scripting the pickers and questions, the control listener reads back each native title and question (`asked`), writes into one of the run's own containers behind the application's back (`put`), closes a chooser with nothing chosen (`cancelPick`), creates one of the run's containers under the same exact name on a second account (`sameNameAs`), and quits or stops the application (`quit`, `crash`), which the runner starts again over the same state only when a journey asked; those last two run in a mock-only run.
- Arguments go to Playwright: `make uijourneys ARGS="--project=mock tests/slash.spec.ts"` runs one journey on the mock, and `ARGS=--headed` shows the browser, which needs a display.
- `scripts/uijourneys.sh` builds the application with the tags `server` and `uijourney`, starts it and the mock on 127.0.0.1 with `HOME` and the XDG directories in a fresh directory, and runs the Playwright suite in `uijourneys/` against it.
- The `uijourney` build is the shipped application with only its native seams replaced: the pickers and the two confirmations answer what the suite scripted over a token-guarded control listener, and an unscripted pick or answer fails; the secret store is session-only.
- Real-cloud bookmarks are made in Go from clouds.yaml, so no password reaches the page; the bookmark editor is driven only against the mock.
- Every container is one the run created, through layer 1's ownership record, and teardown removes only those and their claimed sidecars.
  What a deletion left is read by the run's own session, not by the application: the control listener lists a container the run created, or a sidecar it claimed, and refuses any other.
  Teardown stops a container keeping versions before it deletes from it, because a store archives each object deleted from one that does.
- Output names only each journey, its target (`mock`, `ceph` or `swift`) and the private directory under the user cache directory, which keeps the failure details, a screenshot and the page's text, and the application's log.

The journeys, on each target: create a bookmark and connect (mock only); duplicate it and connect with the copy (mock only); import bookmarks from an rclone.conf, one verified and one flagged, through Settings (mock only); import from the rail under a typed name, and give a bookmark that was imported without a key its key (mock only); upload a folder with a segmented file, check the large-object disclosure against the uploaded object, browse it, and see a copy of the folder start under the folder's name; upload it again with the overwrite confirmed; download it and compare digests with the second upload; download the folder alone and find it under its own name; edit an object's metadata and read it back after a reload; make the container public and private again; delete an object, then the prefix with the segments of its large object, then the container, and follow the dialog to the segments container, find it empty and delete that; delete a static and a dynamic large object, each from its own dialog with its segments, and then a container with the segments of its large object, leaving the option ticked each time; where the store keeps versions, which of the three targets is the Swift cloud, give a container a history location, delete an object and a large object from it, then the container, then its archive; open a copy from a container's context menu; see the "/" folder at the top of a container that holds names starting with "/"; find a container named like a segment store behind the "Possible backstage" heading, with the caution above its listing and in its delete dialog; see a container made outside the window once the column's Refresh is pressed; scan every container in the All view; and, with both clouds, switch between them.

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
| Bookmarks | delete a bookmark that has run a job | delete a bookmark that has run a job, keeping the job (mock only) | the bookmark leaves the rail and the job stays in the history |
| Bookmarks | switch between accounts from the rail | switch between the two clouds (both clouds) | each account lists its own container and not the other's |
| Bookmarks | verify a bookmark; Keystone discovery from a URL without /v3; user and project IDs; import from a clouds.yaml; the import from the welcome dialog | not yet | |
| Bookmarks | create a Keystone v3 bookmark in the editor; an Identity URL with no v3 refused | make a Keystone v3 bookmark in the editor, and refuse a URL with no v3 (mock only) | the account's containers are listed; the editor shows "does not offer Keystone v3 at the same address" |
| Bookmarks | edit a connected bookmark's user | change a bookmark's user: a wrong one is refused, the right one connects (mock only) | the editor shows the refusal; restored, it connects, and a fresh page connects with the saved user |
| Bookmarks | a store that does not answer | a bookmark whose store is unreachable is refused promptly (mock only) | the editor shows "connection refused" within 15 s and stays usable |
| Containers | list an account's containers | every journey that opens a container | the run's own container is in the list |
| Containers | read the list again with the column's refresh | refresh the container list after a change made elsewhere | a container made by the run's own session, outside the window, appears |
| Containers | the list read again when the window is focused, after 30 seconds | coming back to the window after 30 seconds lists a change made elsewhere | with the page clock moved on and a focus event, a container the run made is listed and one deleted in a second window leaves, without pressing Refresh; an earlier focus changes nothing; the desktop delivering focus is test plan step C5 |
| Containers | create a container | find a container named like a segment store behind its heading | its row is listed |
| Containers | the closed "Possible backstage" group and the segment-store caution | find a container named like a segment store behind its heading | the row is absent until the group is opened; the caution is read as a mark on the path's row and in the delete dialog |
| Containers | the All view, and "Scan" over every loaded container | scan every container in the All view | the count the scan reaches is the number of rows, and no row is left marked "not read" |
| Containers | a settings badge and its tooltip, across a bookmark switch | give a container CORS origins and keep its badge across a switch | the "cors" badge's tooltip names CORS and when it was observed, before and after switching away and back (mock: a second bookmark of the same account) |
| Containers | load more containers; stopping a scan | not yet: read-only, and container listing pages are tested in Go | |
| Listing | open a folder and go up | upload a folder with a segmented file and browse it | the uploaded names are listed |
| Listing | the "/" folder at a container's top, and an upload into a typed prefix | see the slash folder at the top of a container | the folder, and the upload inside it, are listed |
| Listing | the next page past the retention ceiling, and back to the start (mock only) | page a folder past the retention ceiling and back | the 1,001st name shows in place of the first thousand with nothing selected, a folder of exactly 2,000 offers no further page, and the first name returns |
| Listing | selecting rows leaves the toolbar and the list where they were | a selection of a file and a folder is copied, downloaded and deleted with the folder's contents | the list and three of the toolbar's buttons are in the same place with nothing selected, with one object selected and read, and with two rows selected |
| Listing | "Count objects" beside the refresh, for one selected folder | a selection of a file and a folder is copied, downloaded and deleted with the folder's contents | the answer on the path's row names the folder and its one object, and the list and the toolbar's buttons have not moved |
| Listing | "New folder…" by name, and the Containers column's filter | create a folder by name and filter the containers (all targets) | the folder is listed, and the filter finds the run's container by part of its name in another case |
| Listing | "Find in container" by part of a name; "#" counting the folder being browsed | find a name by part of it, and count a folder | one match, which opens selected; "3 objects in <folder>/" |
| Listing | the "Public" mark's place and bubble; the list's height for public and private containers | the public mark, and the list's height beside it | the mark lies between the path and the tools; the bubble is the whole notice; the list's top is equal |
| Listing | pressing the list's empty space clears a selection | refuse the wrong words for each container deletion, and empty a container | no row is selected, and the folder's or container's deletions are back |
| Inspector | the actions under an item's name; copying a value; a suggested metadata key found by part of its name | edit an object's metadata; make the container public and private again | the edit button is above the first card, the ETag's copy says "Copied", and the picked key is read back |
| Listing | load more objects | not yet: read-only, beside the paging journey | |
| Listing | the filter with Ctrl+A, and the Delete key opening the selection's deletion; Escape closing it | delete the rows a filter shows, selected with Ctrl+A and the Delete key | the dialog lists only the shown rows and asks for "delete 3"; after Escape the run's own listing is unchanged; confirmed, it holds exactly the filtered-out rows |
| Inspector | an object's large-object kind | upload a folder with a segmented file and browse it | it is the kind the job recorded |
| Inspector | an object's user metadata; a container's public notice | edit an object's metadata; make the container public and private again | read after connecting again |
| Inspector | the account's metadata | edit the account's metadata and find the write in Recent changes (mock only) | read back after connecting again; Recent changes holds an applied account.metadata.update |
| Inspector | the account card's storage policies; raw headers starting closed, with TempURL key values redacted | read the account's cards and every pane's raw headers | the policies card lists a policy with its counts; account, container and object raw headers start closed; on the mock an account key shows only as "[redacted]" |
| Inspector | an ordinary container has no segment classification card; the TempURL card's line and "How to rotate a key" | an ordinary container's inspector | the card is absent; the line is read, and the rotation steps show only once opened |
| Inspector | segment tables; "Open segments backstage" | not yet | |
| Edit | add a key to an object's metadata | edit an object's metadata | read back after connecting again |
| Edit | make a container public, with the native confirmation answered by the run, and private again | make the container public and private again | the inspector's sentence after connecting again |
| Edit | the public-read question's words, answered no | the public-read question names the container and account, and no leaves it private | the question read through the control listener; after connecting again there is no public sentence or mark |
| Edit | a container's TempURL keys, set and rotated to the second slot | set a container's TempURL key and rotate it to the second slot (mock only) | after connecting again only the second slot is set, shown as "[redacted]", with neither secret in the raw headers |
| Edit | a container's CORS allowed origins | give a container CORS origins and keep its badge across a switch | read back after connecting again in the inspector, the raw headers and the editor |
| Edit | give a container a history location and its archive | a history location keeps what is deleted, and every segment (Swift cloud only) | the delete dialogs that follow name the archive, and what is deleted arrives there |
| Edit | the static website preset and the quota fields greyed, with the reason, where the store's `/info` does not list them | make the container public and private again (mock only) | each control is disabled and the sentence naming `staticweb` is shown |
| Edit | container quotas; a versions location | not yet: quotas are greyed on the mock and enforced by the store; the history location is journeyed on Swift | |
| Edit | a large object's metadata, static and dynamic | edit a large object's metadata, keeping its kind and its bytes | after connecting again the inspector shows the recorded kind and the key; a download's SHA-256 matches the upload |
| Edit | container saves one after another keep the other settings | each save of a container's settings keeps the others; set a container's TempURL key and rotate it to the second slot | after connecting again the container is private and still holds its Web-Listings-Label; on the mock the second TempURL slot survives a CORS save |
| Edit | an object edit that conflicts with another writer: refused, "Reload theirs", "Apply mine anyway" | an edit that conflicts with another writer is refused, then applied on purpose | the banner's words; after connecting again, no key after reloading and the key after applying; a download's SHA-256 is the other writer's bytes |
| Upload | a folder holding a file that uploads in segments, with a per-job chunk size | upload a folder with a segmented file and browse it | the job completes, the uploaded names are listed, and the inspector shows the large-object kind the job recorded |
| Upload | replace existing objects that differ, with the native confirmation answered by the run | upload it again and confirm the overwrite | the download that follows returns the second upload's bytes |
| Upload | the kind of large object chosen for one upload | delete a static and a dynamic large object, each with its segments | each object's delete dialog names the kind of manifest the job recorded |
| Upload | the folder and file choosers' titles; chosen files instead of a folder | the choosers' titles name the bookmark, its account, the container and the prefix | the titles read through the control listener; the file is listed under the prefix by the run's own session |
| Upload | a chooser closed with nothing chosen | closing the folder chooser leaves nothing chosen | the dialog returns to its hint with no Start upload and no error; both choosers read through the control listener; the run's session lists nothing |
| Upload, Download, Delete | names with a space, %, #, ?, +, a decomposed non-ASCII name and a 203-character name | upload, browse, download and delete names that are hard to encode | the run's own listing holds each name; each opens in the inspector; the download is refused naming "what?.bin"; that object deleted by name leaves exactly the others; downloaded, the folder holds exactly those names with each SHA-256 |
| Upload, Download | one visible name in its composed and decomposed forms | upload both Unicode forms of one name as two names | the run's own listing holds both names; a download of the pair is refused before anything is written, as some filesystems would give them one place |
| Upload | a folder with a link and an empty directory: the summary and the report | upload a folder with nested files, a link and an empty directory | the summary and the skipped row are read; the run's own session lists exactly the regular files under their relative paths |
| Upload | a folder of more than 10,000 files refused at the pick | a folder of more than 10,000 files is refused at the pick | the refusal is shown and the run's own session lists nothing |
| Upload | the same folder again: identical files, then one changed | upload the same folder again: the same files stay, one changed file is replaced | the question read through the control listener; the job says 3, then 2, already the same; the changed bytes come back by SHA-256 |
| Upload | one file onto an existing name: refused, declined, replaced | upload one file onto an existing name: refused, declined, then replaced | the question's words; a download's SHA-256 is unchanged after declining and is the new file's after replacing |
| Download | a whole container into a local folder | download it back | each file's SHA-256 is that of the file uploaded |
| Download | replace existing files, with the native confirmation answered by the run, over files that are already the same | download it again over the same files and confirm the replacement | the job says every file is already the same, and each file's SHA-256 is unchanged |
| Download | one folder, into a folder of its name inside the chosen directory | download the folder into a folder of its name | each file's SHA-256 under the folder's name is that of the file uploaded, and the job names the folder picked |
| Download | the directory chooser's title, for one object | the choosers' titles name the bookmark, its account, the container and the prefix | the title read through the control listener; the file's SHA-256 is that of the file uploaded |
| Download | into a folder that holds the files: refused, declined, one changed file replaced, a file that appears after the preview | download into a folder: refused, declined, one changed file replaced, one that appears kept | the question's words; each file's SHA-256; the start is refused with the appearing file unchanged and nothing else written |
| Copy | open the copy dialog from a container's menu | open a copy from a container's context menu | the dialog opens for that container; nothing is sent |
| Copy | a folder's copy starts under the folder's own name | upload a folder with a segmented file and browse it | for the store's own folder row, the copy dialog's destination path starts as the folder's name; nothing is sent |
| Copy | a selection of a file and a folder, taken with everything in the folder | a selection of a file and a folder is copied, downloaded and deleted with the folder's contents | the copy's folder holds the file and the folder's file, and each downloaded file's SHA-256 is that of the file uploaded |
| Download | a selection of a file and a folder | the same journey | the same |
| Copy | a copy across two accounts, chosen in "Destination bookmark", which starts on the account the window is on | copy a folder from one cloud to the other (both clouds) | the run's session lists the names on the destination and the source unchanged; each downloaded file's SHA-256 matches; the dialog starts on the current account on each side of a switch |
| Copy | run a copy within an account of objects, one in segments, with custom metadata; the same copy again | copy a container with a large object and its metadata, then again | the run's own session lists the names, each downloaded file's SHA-256 matches, the key is read after connecting again; the second preview has 0 new and nothing is copied |
| Copy | a changed object: refused unasked, declined, replaced | copy over a changed object: refused unasked, declined, then replaced | the job is partial with one overwrite refused; the question's words; no new job after declining; the destination's SHA-256 unchanged until confirmed |
| Copy | a folder copied up to the container's top; a copy into its own source refused | copy a folder up to the top, and refuse a copy into its own source | the run's session lists the files at the top and the originals in the folder; the exact overlap message, Start copy disabled, nothing written |
| Copy | two selected folders copied whole; a selection copied into its own folder refused | copy two selected folders, and refuse a selection copied into itself | the run's session lists both folders under the new one; the exact refusal naming the first listed row, no new job, nothing written |
| Copy | destination container suggestions; create the missing destination container (mock only) | suggest destination containers, and create a missing one | the suggestions hold a run container; Start copy is disabled until the container is created, which a fresh connection lists |
| Jobs | follow a job to its end and read its details | the upload and download journeys, and the prefix and container deletes of the delete journey (an object delete is not a job) | the job's state is "complete" |
| Jobs | "Recent changes" for each kind of job | not yet: one kind is journeyed, the rest are tested in Go | |
| Jobs | a refused name's item record | copy over a changed object: refused unasked, declined, then replaced | the job's Item details show Refused 1 and the name's record "refused · not attempted" |
| Jobs | pause and resume an upload and a copy | pause an upload of two folders between them and resume it; pause a copy of 501 names between its batches and resume it | while paused the run's own listing holds only the first unit; after resuming every name, each once in the item records, and every download's SHA-256 matches |
| Jobs | cancel a running upload and a running download from the drawer (mock only) | a cancelled upload leaves only whole objects; a cancelled download leaves only whole files | each reads "cancelled, incomplete" with its stopped file counted incomplete and nothing failed; every object left downloads with its file's SHA-256; every local file left has its source's SHA-256 or ends ".partial" |
| Jobs | quitting during a metadata save (mock-only run) | quitting during a metadata save keeps the store and Recent changes in step | at three timings, a fresh read after the restart agrees with exactly one applied event in Recent changes |
| Jobs | re-run a completed copy; no re-run on a running job | re-run a completed copy as a fresh job | a new job id completes, and the run's session lists the file added to the source before the re-run |
| Jobs | the queue-depth refusal; the overlapping-download refusal | refuse a start past the queue, and a download inside a running one | the exact refusal naming 1 running and 1 waiting, with no new job; "Overlaps a running download" naming the running job and its folder |
| Jobs | pause and resume, with jobs running at once | pause, resume and run downloads at once | two running and one queued; the paused one parks and the queued one starts; all three complete and every file's SHA-256 matches |
| Jobs | "Show in jobs" with another account filtered (mock only) | show a copy in jobs with another account filtered | the Jobs tab is selected, the filter is on all accounts, and the job's row is expanded |
| Jobs | the recovery notice, row tints, a failed job's error | read the drawer's notice, tints and a failed job | the notice is one line with the rest under Details; neighbouring rows differ in background; the error is whole in the details and one line in the row |
| Jobs | per-job profile and transfers for an upload and a download | record per-job settings for an upload and a download | the job details show each setting and where its value came from; chunk size calculated for the upload, absent for the download |
| Jobs | a job running when the application quits, and when a run ends with no shutdown (mock-only run) | quitting cancels the running job and keeps its record; a run ended with no shutdown is disclosed at the next start | the job is cancelled with nothing disclosed; the disclosure names the job, Dismiss clears it, and the history keeps the job |
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
| Delete | the segments option in another container's archive | not yet: deleting an archive with its segments is journeyed | |
| Delete | a selection's segments option, ticked by hand where no folder is selected | delete a selected large object with its segments, and keep another's | the option starts unticked with the sentence saying why; the run's own listing of the container holds only the other large object, and of the segments container exactly that object's segments |
| Delete | what stays beside a deleted selection, prefix and object | the selection, prefix and object deletion journeys | the run's own listing holds exactly the originals and the neighbours: a name starting as the deleted object's, a folder starting as the deleted prefix's, and a top-level object |
| Delete | a prefix of 2,000 objects, past a listing page (mock only) | page a folder past the retention ceiling and back, and delete it | the preview reads 2000 objects; the run's own listing then holds exactly the two neighbours |
| Delete | emptying or deleting a container while an upload writes into it (mock only) | refuse to empty or delete a container an upload is writing into | each start is refused naming the upload's job, no job starts, and the run's own listing is unchanged |
| Delete | an object deleted after switching to an account holding a container of the same name | delete from a container of the same name on the other cloud (both clouds) | the run's sessions list it gone from that account's container only |
| Delete | a marker | delete a marker and a manifest, and keep what they hold | the run's own listing holds the objects under it and not the marker |
| Delete | one large object's manifest with the segments option unticked | delete a marker and a manifest, and keep what they hold | the run's own listing of the segments container is unchanged |
| Delete | the wrong words refused by the prefix, empty and delete-container dialogs; "Empty container…" carried through; counts after emptying | refuse the wrong words for each container deletion, and empty a container | each button stays disabled; the run's own listing is empty and the container stays; inspector and row counts read 0 within 30 s without refresh |
| Delete | a prefix with the segments option unticked | delete a prefix with the segments option unticked | the words drop "and segments", what was typed is cleared, "manifest-only" is shown; the run's own segments listing is unchanged |
| Delete | a prefix whose object another program wrote over after the preview | delete a prefix whose object changed after the preview | the dialog says what a changed name means; the run's own listing is empty afterwards |
| Delete | one large object with its segments from Backstage, armed by its whole name | delete a large object with its segments from Backstage | the run's own listing holds neither the manifest nor any segment |
| Menus | a container row's menu | open a copy from a container's context menu; find a container named like a segment store behind its heading | the chosen entry opens its dialog |
| Menus | an object's and a folder's menu | an object's and a folder's context menus | exactly the plan's items; each dialog says what the toolbar's does; "Copy name" is chosen but not read back: the server build's clipboard does nothing |
| Settings | open Settings and import from it | import bookmarks from an rclone config (mock only) | as for the import above |
| Settings | filter the settings; change and save a setting, with the jobs-at-once line adding the limits; keep a log file; reset to the defaults after asking | filter the settings and reset them to the defaults (mock only) | the filter leaves only the matching setting, the line names the saved queue depth; the log file at the state folder's path exists, grows after a sign-in warning, and stops once reset turns it off; after the reset the queue depth reads its default |
| Settings | a refused key reaches no log, no export and no page (mock only) | a refused key reaches no log, no export and no page | the application's log, the kept log file, the exported audit file and the reloaded page's HTML do not hold a random key used in a refused sign-in |
| Settings | back up and restore the configuration | back up the settings with no key, and restore a bookmark without one (mock only) | the picker's title is checked; the file holds the bookmark and not its key; the deleted bookmark returns with "No saved key" |
| Settings | where the settings file and the database are kept | the settings file and the database hold no key (mock only) | settings.json in the run's XDG config folder holds the change as JSON; the database exists in the data folder; neither holds the key (this build keeps keys in memory, so this checks the bookmark's own record, not a keyring) |
| Settings | the default profile reaches the upload dialog | start the upload dialog at the default profile | a fresh upload dialog's Profile is balanced |
| Settings | connection cap 1 (mock only) | browse and inspect under a connection cap of one | a session opened under the cap lists the container and inspects each object |
| Window | the welcome dialog; credential status; use by keyboard alone | not yet | |
| Jobs | the benchmark | not offered in this release (v1Offers.benchmark is off); tested in Go | |
| Recent changes | search the audit trail and "Export log…" | not yet | |
| Window | the status bar's bookmark, the About link and the About dialog | the status bar and the About dialog | "connected to" and the bookmark; the version line's twelve characters and its hover text are the checkout's revision |

The selected-delete browser journey checks the rendered count and the eventual deletion.
The service tests additionally check that a multi-folder preview shares one archive observation, and held HTTP tests check the eight-reader bound and cancellation.
Mounted dialog tests check cancellation on close, navigation and selection changes, and keep failed counts unknown with the segment option unticked.
Those concurrency and cancellation checks are not claims made by the browser journey itself.
