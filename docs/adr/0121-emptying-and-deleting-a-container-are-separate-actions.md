# ADR 0121 — emptying a container and deleting it are separate actions, each with its own words

**Status:** Accepted on 15 November 2026 for 0.5.0, on the operator's decision of that day during the candidate's test run: "three separate is good, do it for 0.5.0".
Tier 1 under DESIGN §65.12, because it changes the words that arm a destructive job.
**Scope:** how the bulk deletion dialog is opened and what it offers, and the confirmation `DeleteService.StartDelete` requires for the container deletion.
The three modes, the preview, the per-name protocol, the segments option and the archive disclosure do not change ([ADR 0095](0095-prefix-deletion-by-the-per-name-protocol.md), [ADR 0102](0102-versioning-matrix-fixed-point-and-archives.md), [ADR 0119](0119-a-bulk-deletion-may-take-segments.md), [ADR 0120](0120-segments-go-by-default-and-versions-keep-them.md)).

## What exists

One dialog serves three jobs: deleting a prefix, emptying a container, and deleting a container.
Every way in opens that same dialog, and the job is a choice of three inside it.
At a container's top the toolbar's one button reads "Empty or delete container…" and the dialog starts on "Empty container"; inside a folder the button reads "Delete prefix…" and the dialog starts on the prefix, with the two container jobs one click below it.

The words that arm the job are the scope (ADR 0095 decision 7): the container, a slash and the prefix for a prefix, and the container's name for both container jobs.
So a prefix deletion cannot be armed by a container's words, but emptying and deleting a container are armed by the same typing, and only the choice inside the dialog and the label on its button tell them apart.
Both destroy the objects.
Deleting also destroys the container and what it carries: its access lists, metadata, quota and versioning location.
An operator who meant to empty a container can delete it, with nothing they typed saying otherwise.

## The behaviour oracle

- The operator's decision: three separate actions, in 0.5.0.
- ADR 0095 decision 7: the typed confirmation names what goes, and `StartDelete` refuses any other before it admits the job, for every caller.
- ADR 0119 decision 2: a deletion of one kind is armed by other words than a deletion of another, so words typed for one never start the other.

## Decision

1. **Three actions, each opening the dialog on one job.**
   "Delete prefix…", "Empty container…" and "Delete container…" are separate actions.
   The dialog opens on the job the action names and offers no choice of job: its heading names the job, and one sentence under it says what goes and what stays, the sentence the choice used to carry.
   To do a different job the operator closes the dialog and takes the other action.
2. **Where the actions are.**
   Inside a folder, or with exactly one folder selected alone at a container's top, the toolbar shows "Delete prefix…" for that folder, as today (ADR 0095 decision 7), and not the container's two.
   Otherwise at a container's top, with nothing selected, several rows selected or only a file selected, there is no single prefix to name, and the toolbar shows "Empty container…" and "Delete container…".
   A container's own menu shows "Empty container…" and "Delete container…" wherever the operator is, a folder being open included.
   A folder row's menu keeps "Delete prefix…" and does not gain the container's two.
   So a prefix deletion is never one click away from a container's deletion.
3. **Deleting a container has its own words.**
   The confirmation for the container deletion is `delete ` followed by the container's name: `delete media` for the container `media`.
   Emptying keeps the container's name, and a prefix keeps its scope.
   With the segments option each is followed by ` and segments`, as ADR 0119 decision 2 set: `delete media and segments`.
   `StartDelete` compares the confirmation with the words for the request it was given before it admits the job, as it does today, so the name alone never starts a container deletion and `delete media` never starts an emptying of `media`, for any caller.
4. **What follows in the contract.**
   ADR 0095 decision 7 is amended: the container deletion's words, and the two container jobs no longer on offer below a prefix.
   DESIGN §31 to §33, the release notes, the test plan and the journeys table follow.

## Why these shapes

**Keeping one dialog and changing only the words** was considered and not taken.
It would close the gap in what is typed and leave the confusion the operator named: a choice of destructive jobs made inside a destructive dialog, starting on a job the operator may not have asked for.

**Giving emptying a verb too**, `empty media`, was considered and not taken.
One of the two has to differ for neither to arm the other; the deletion is the one whose consequence is wider, so it carries the extra word, and every confirmation an operator has learned for a prefix or an emptying stays as it is.

**`media and the container`** was considered for the words and not taken: with the segments option it reads `media and the container and segments`, and a leading verb says the job before the name.

## Required proof before implementation is accepted

Tier 1.

- Go, service, on the mock, each refusal with no job row and nothing deleted: a container deletion is refused when the confirmation is the container's name alone, and starts with `delete ` and the name; an emptying is refused with the deletion's words, and starts with the name. The same holds with the segments option: `media and segments` does not delete the container `media`, `delete media and segments` does not empty it, the plain deletion's words are refused with the option, and `delete media and segments` deletes it.
- Frontend, dialog: each action opens the dialog on its job, with the job named in the heading and no choice of job offered; the words asked for are each job's own, with and without the segments option.
- Frontend, where the actions are: inside a folder the toolbar offers the prefix action alone; exactly one folder selected at a container's top offers only the prefix action; any other selection there, none, several rows or only a file, keeps the two container actions; a container's menu offers both container actions while a folder is open; a folder row's menu offers the prefix action and neither container action.
- Browser journeys: the existing deletions of a prefix, a container and an archive take their own action and type the words the dialog asks for.
- Retained mutants, each failing a case above: a container deletion armed by the name alone; an emptying armed by the deletion's words; the dialog opened on a job other than the action's; a choice of job offered inside the dialog; the toolbar offering a container action inside a folder.

## What this does not do

It does not change what either job does, only how each is reached and armed.
A container named `delete x` is emptied by typing its name, which is also what deletes a container named `x`; the two are different containers, and each dialog is opened on one container and one job.
The selected-set, object, marker and manifest dialogs are not changed.
