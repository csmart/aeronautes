# ADR 0108 — copy and download what is selected

**Status:** Accepted for 0.5.0 (12 November 2026), from the operator's report during the 0.5.0 test run and the decision to hold 0.5.0 for it, delivered with this record's series.
Amended on 15 November 2026, during the 0.5.0 test run: the operator selected a container's files and a folder, pressed "Copy selected…", and the folder was not copied, with one quiet line in the dialog to say so. Their decision: in 0.5.0 a selection that holds a folder beside other rows, or more than one folder, opens a warning before anything else, and the behaviour itself has to change after that, so that a selection copies its folders as it copies its files. Decision 1 and the frontend proof are amended below for the warning; copying a selection's folders is owed and is not decided here.
**Scope:** `TransferService.PlanCopy`, `StartCopy`, the copy job's planning passes, its verification and `RerunJob`; `LocalTransferService.PreflightDownload` and `StartDownload`; the working list's Copy and Download buttons and the two dialogs.
**Amends:** DESIGN §65.9 item 9 (as amended by its 11 November paragraph) for a copy of named objects.
Amended on 16 November 2026 by [ADR 0122](0122-the-stores-answer-is-the-result.md) decision 4: a name under `.aeronautes/stage/` is an ordinary name, so it may be a member, and the refusal of a source that holds the copy's working area is gone with the stage.
Amended on 18 November 2026 by decision 8: "Download folder…" keeps the folder's name, as a selected folder does.
Amended on 17 November 2026 by decision 7, on the operator's decision of that day that a transfer follows rclone's semantics ("if I tell rclone to copy one container to another, it will copy everything from it to another, whether files or virtual folders or what") and their choice that in 0.5.0 a selected folder is copied or downloaded with everything in it: folder rows in a selection are members, and the warning of 15 November is withdrawn.

## Context

The operator selected two objects with Ctrl-click and pressed "Copy to…"; the copy took the whole folder they were in.
Copy and Download act on a scope, a container and a prefix: `CopyRequestDTO` and `DownloadRequestDTO` carry no names, and the dialogs build their requests from the browsed container and prefix.
Selection arrived later, for deletion only ([ADR 0093](0093-selected-set-deletion.md)), and nothing went back over the other actions, so a selection was silently ignored by both.
[ADR 0072](0072-large-namespace-handling.md) decision 3 already says a selection names loaded rows and whole-prefix intent stays the prefix scope.

The engine copies only the names a plan lists, through `--files-from-raw` with `--no-traverse` ([ADR 0022](0022-complete-rclone-selection.md)), and the native route builds each name from the plan, so restricting the plan restricts what is written.
A download already plans and executes from its preflight's item list and verifies each item as it places it.
A copy also verifies by a fresh join of the whole source and destination scopes, which would report every unselected source object as missing.

## Decision

1. **What an action acts on follows the selection, and its button says so.**
   With two or more rows chosen, Copy and Download act on the chosen objects; with one object row selected, on that object; with one folder row selected, on that folder, as Delete does since ADR 0095 decision 7; with nothing selected, on the folder being browsed, or the container at its top.
   The buttons read "Copy selected…", "Copy object…", "Copy folder…" or "Copy container…", and "Download selected…" and so on, and each dialog's source panel names what it will act on: the container and folder, the number of selected objects with the first names, and the folder rows that are left out.
   Folder rows in a multi-selection are not members, as in ADR 0093.
   A multi-selection of folder rows alone has no members, and an empty member list means the whole scope, so it is refused where the choice is made, and each dialog, should the selection change to folder rows alone while it is open, can neither preview nor check it, so no request is sent and no job row or listing follows.
   **A selection that leaves a folder out says so before the dialog opens (15 November 2026).**
   When a multi-selection holds at least one folder row, pressing "Copy selected…" or "Download selected…", in the toolbar or a row's menu, opens a warning first, and the buttons are no longer disabled for it.
   It always states how many selected folders are left out, names at most five of them, and says so when more remain, so that a long selection never reads as a complete list.
   For folders beside objects it says they and everything in them will not be copied or downloaded, and offers to go on with the selected objects only or to cancel; going on opens that action's dialog as it is today.
   For folders alone it says a selection of several folders cannot be copied or downloaded in one go yet, and offers only to close.
   Both say how to take everything: select one folder alone, or clear the selection, and use the folder's or the container's own action.
   Nothing is sent by the warning, and cancelling it changes nothing.
   The request alone cannot tell such a selection from the folder, which is why the refusal is the window's; an empty member list is only ever sent for no selection or one selected folder row.
2. **A request may name its members.**
   `CopyRequestDTO` and `DownloadRequestDTO` gain `Members`, full object names; empty means the whole scope, as before.
   Before any job row or listing, the request is refused when a member is empty, repeated, not under the source prefix, in a staging area, or when the members outnumber the selection ceiling (the download object ceiling setting, as for ADR 0093).
   A member named exactly the source prefix, the folder's own marker, is refused by name: it has no name relative to the folder, and DESIGN §65.9 item 9's skip of such a row would let a copy of it to a container's top plan nothing and still end complete.
   That skip still applies to a marker the operator did not name, in a whole-scope copy.
3. **The plan is the members.**
   Every planning pass lists the source scope as before and keeps only the members.
   A member is not at the source only when a listing finished without error and did not contain it; a listing that fails ends the pass with that failure, as for any copy or download, and counts no member as missing.
   A copy's preview counts such members; a copy's run copies the members it found and ends partial, saying how many selected objects were no longer at the source; a download's preflight refuses, naming the first missing member, because it is the last check before the download starts.
   A copy's verification keeps only the members on the source side and the whole destination listing as before, whose destination-only names it already ignores, so it judges only the members; filtering the destination by the source names would drop every copied object, which is named by the destination prefix.
4. **Overlap for a copy of members.**
   In one namespace a destination inside the source folder is allowed for a copy of members, because the run writes and reads only named objects.
   A member whose destination name is itself a member would replace an object the same run reads; that is known from the request alone, so it is refused with the decision 2 checks, before any listing or job row, naming the member, and never by a planning pass, which for an unacknowledged copy streams and could already have written earlier members.
   A destination equal to the source folder maps every member onto itself and is refused by that same check.
   The scratch-area refusal does not apply to a copy of members: its listing keeps only the members and no member can be in a staging area, so the run never reads the scratch area, which for a destination inside the source folder lies inside it.
   Everything for a whole-scope copy stays as DESIGN §65.9 item 9 says.
5. **A copy of members is re-run as the same members.**
   The member names are written in the same transaction as the job row, so a job that records a selection can never lose its names and be re-run as its whole scope; Re-run plans those names again under the current state, a member gone since is not at the source as above, and a job whose names cannot all be read back is not re-run.
   The job's source stays the folder's locator, for example `testing/files/`, which Re-run parses as before; the count is a separate field, and the jobs drawer and history show it beside the source, as `testing/files/ (2 selected)`.
6. **Overwrite and every other rule are unchanged.**
   A member whose destination exists and differs is a collision under [ADR 0087](0087-copy-overwrite-on-upload-terms.md); the download's collision, path and ceiling checks run over the members alone.

7. **A selected folder is taken with everything in it** (17 November 2026).
   It amends decisions 1 to 5, and withdraws decision 1's warning of 15 November with its frontend tests.
   **A folder row in a selection is a member.**
   With two or more rows chosen, Copy and Download act on every chosen row: each object, and each folder with everything under it, whatever else is chosen and however many folders.
   The buttons keep their labels, "Copy selected…" and "Download selected…", and each dialog's source panel names the number of selected objects and folders with the first names.
   Nothing about a selection's rows is refused in the window any more, and no warning opens first.
   **A folder member is its name with a trailing slash.**
   `Members` carries full names; one that ends in `/` is a folder member and means that folder and everything under it, its own marker object included where there is one.
   The checks of decision 2 apply to every member: not empty, not repeated, under the source prefix, not the source prefix itself, and no more members than the selection ceiling.
   A member that lies under a folder member is refused as repeated, because it names nothing the folder does not.
   **The plan is the members and everything under the folder members.**
   Every planning pass lists the source scope as before and keeps a row when it is an object member or lies under a folder member.
   A folder member is met when the finished listing holds at least one row under it, and is otherwise not at the source, as an object member can be; a listing that fails is that failure and counts nothing missing.
   A file under a selected folder keeps its path from the source prefix, so the folder's name is kept at the destination, as when the folder is copied or downloaded alone.
   A download's object ceiling counts the planned objects, folder contents included, as it counts any download's.
   **Overlap for a copy of members, in one namespace.**
   The run reads the object members and everything under the folder members, and writes each at its destination name.
   It is refused, with decision 2's checks and naming the member, before any listing or job row, when a write could land on something the same run reads: an object member's destination is an object member or lies under a folder member; an object member lies under a folder member's destination folder; or a folder member's destination folder and a folder member's source folder are the same or lie one inside the other.
   A destination inside the source folder stays allowed wherever none of these holds.
   **Re-run** records and plans folder members by the same names, trailing slash included, under the current state.
   **Delete** was not changed by this decision; on the operator's further decision of 17 November a deletion's selection takes a selected folder whole as well ([ADR 0093](0093-selected-set-deletion.md) decision 8).
8. **"Download folder…" keeps the folder's name** (18 November 2026).
   Testing the 0.5.0 candidate, the operator pressed "Download folder…" and found the folder's contents written straight into the directory they chose, where "Copy folder…", an uploaded folder and a selected folder all keep the folder's name: "it kinda seems opposite of copy and upload folder".
   Decision 7's last sentence said a folder downloaded alone keeps its name; the window did not, because it sent the folder as the request's prefix with no members.
   **The request.** A download of one folder, the folder browsed with nothing selected or one folder row selected alone, is sent as the folder's parent prefix with the folder as its one folder member, so it is planned, checked and written exactly as a selected folder is, under `<chosen directory>/<folder name>/`.
   No backend rule changes.
   **What comes with the selected-folder plan.** The rules of decision 7 now apply to a folder downloaded alone, and three of them differ from the prefix request it replaces:
   - The parent prefix is listed in full to find the folder's objects, so a folder inside a large parent takes as long to plan as the parent does, and a listing of the parent that fails refuses the download; a selection already pays this.
   - A folder with nothing under it is refused as no longer at the source, where the prefix request completed with nothing written.
   - The job records the parent as its source with one selected member, so the jobs drawer names the parent and "1 selected"; a download is not re-run (Re-run replays only a copy), so nothing reads the member later.
   **What does not change.** "Download container…" still writes the container's contents into the chosen directory, as "Copy container…" writes into its destination; a selection is unchanged; a count of the folder is still offered as the download's size, because it counts the same objects.
   **Required proof (tier 2).** Frontend: the download dialog for a browsed folder and for one selected folder sends the parent prefix and the folder as its one member, and for the container sends no members. Journey, on the mock and both clouds: "Download folder…" writes the files under a directory of the folder's name in the chosen directory, compared by digest.

## Consequences

- Copying or downloading a few files out of a large folder plans and verifies only those files, though each pass still lists the folder.
- A selection of more objects than the ceiling is refused before anything is listed.
- Re-running a selected copy needs the recorded names: one row per member in a new `job_selected_names` table, written in the job's own transaction, and the count in a `selected_members` column of the job.

## Tests

- Go, copy: a copy of two members of a folder of ten writes exactly those two and verifies two; a missing member makes the preview count it and the run end partial with the reason; a failed listing, at the preview or in the run, is that failure and reports no member missing; empty, marker, repeated, outside-prefix, staging-area and over-ceiling members refuse with no job; a copy of members into a subfolder of their own folder writes them there; a member mapping onto another member refuses, naming it; Re-run of a members copy plans the same names; whole-scope behaviour unchanged.
- Go, download: a preflight of two members of ten plans two and the download writes two files; a missing member refuses the preflight naming it; a failed listing is that failure; the ceiling counts members.
- System: a selected copy and a selected download against the owned Swift with the engine, with nothing else written.
- Withdrawn by decision 7 on 17 November 2026, with the warning; kept here as the record of what was tested. Frontend, the warning (15 November 2026): with a folder beside objects in the selection, each of the two actions, from the toolbar and from a row's menu, opens the warning and not the dialog; it names the folder and the number of objects, says the folder and everything in it will not be copied or downloaded, and says that one folder selected alone or a cleared selection takes everything; with more than five folders beside an object it states their number and that not every name is listed; going on opens that action's dialog with the objects as members, and cancelling opens nothing; with folders alone the warning offers only to close and no dialog can be reached; neither the warning nor cancelling it sends a copy or a download request; a selection of objects alone, one folder alone, and no selection open their dialogs with no warning. A browser journey selects a file and a folder, presses "Copy selected…", reads the warning and cancels.
- Decision 7 (17 November 2026), Go, copy and download: a selection of two files and a folder of three plans and writes five, the folder's name kept; a folder member with nothing under it is not at the source; a member under a folder member refuses as repeated; each overlap shape refuses naming the member, and a destination inside the source folder with no overlap is allowed; Re-run plans the same members. System: a selected copy and a selected download of files and folders against the owned Swift with the engine, with nothing else written. Frontend: a selection of files and folders, and of several folders alone, opens the dialog directly, the source panel names both counts, and the request carries each folder with a trailing slash; the warning and its tests are gone. Journeys: a selection of a file and a folder is copied and downloaded with the folder's contents, on the mock and both real stores.
- Frontend: each button's label for no selection, one object, one folder and several rows; the dialog's source panel names the members; (withdrawn by decision 7 on 17 November 2026: both open dialogs refuse a multi-selection of folder rows alone, and the source panel names the excluded folder rows;) the request carries the members, and a selected folder downloads as its own scope; a change of selection disarms a preview; every toolbar action either acts on the selection or says it acts on the scope.
