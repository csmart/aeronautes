# ADR 0108 — copy and download what is selected

**Status:** Proposed on 12 November 2026, from the operator's report during the 0.5.0 test run and the decision to hold 0.5.0 for it.
**Scope:** `TransferService.PlanCopy`, `StartCopy`, the copy job's planning passes, its verification and `RerunJob`; `LocalTransferService.PreflightDownload` and `StartDownload`; the working list's Copy and Download buttons and the two dialogs.
**Amends:** DESIGN §65.9 item 9 (as amended by its 11 November paragraph) for a copy of named objects.

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
   A multi-selection of folder rows alone has no members, and an empty member list means the whole scope, so it is refused where the choice is made: both buttons are disabled and say to select objects, and each dialog, should the selection change to folder rows alone while it is open, can neither preview nor check it, so no request is sent and no job row or listing follows.
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

## Consequences

- Copying or downloading a few files out of a large folder plans and verifies only those files, though each pass still lists the folder.
- A selection of more objects than the ceiling is refused before anything is listed.
- Re-running a selected copy needs the recorded names: one row per member in a new `job_selected_names` table, written in the job's own transaction, and the count in a `selected_members` column of the job.

## Tests

- Go, copy: a copy of two members of a folder of ten writes exactly those two and verifies two; a missing member makes the preview count it and the run end partial with the reason; a failed listing, at the preview or in the run, is that failure and reports no member missing; empty, marker, repeated, outside-prefix, staging-area and over-ceiling members refuse with no job; a copy of members into a subfolder of their own folder writes them there; a member mapping onto another member refuses, naming it; Re-run of a members copy plans the same names; whole-scope behaviour unchanged.
- Go, download: a preflight of two members of ten plans two and the download writes two files; a missing member refuses the preflight naming it; a failed listing is that failure; the ceiling counts members.
- System: a selected copy and a selected download against the owned Swift with the engine, with nothing else written.
- Frontend: each button's label for no selection, one object, one folder and several rows; a multi-selection of folder rows alone disables both buttons, and both open dialogs refuse it; the dialog's source panel names the members and the excluded folder rows; the request carries the members, and a selected folder downloads as its own scope; a change of selection disarms a preview; every toolbar action either acts on the selection or says it acts on the scope.
