# ADR 0123 — the working list stays where it is

**Status:** Accepted on 18 November 2026 for 0.5.0, on the operator's decisions of that day during the candidate's test run.
Tier 3 under DESIGN §65.12: it moves and restyles controls and notices, and changes no request, refusal or typed word.
**Scope:** the head of the working list, above its column titles: the path, the notices, the listing tools and the action rows.
What each action does, its dialog and its words do not change.

## What exists

The head of the working list grows and shrinks with what it has to say.

- A public container adds a red banner above the path; a container whose settings could not be read adds a line there instead.
- A backstage listing, or a container that may be a segment store, adds a banner under them.
- Counting objects adds a line of its own under the action rows when the count starts, and the line stays.
- The refresh is a small button at the far right of the path's row, where the operator did not find it.
- "Count objects" is a full button among the transfers, and counts the folder being browsed, not a folder that is selected.
- The deletions have a row of their own, pushed to the right.

Each banner or line that comes or goes moves the list down or up under the pointer.
The operator, clicking from one container to the next, found the rows moving between the moment they aimed and the moment they clicked, and said a list where people may click the wrong thing must not move.

## The behaviour oracle

- The operator's decisions of 18 November 2026: "the layout should be pretty static, or done in a way that it doesn't move the things that people need to select, esp in a list"; the deletions on their own row, "left aligned, keep different colour"; the count as "a button at the top next to refresh", and "the line that prints number of objects is another example of something that moves the list down"; the refresh "next to the container title, button with a hover".
- The accepted practice for layout shift: space for what can appear is kept, or what appears is drawn where it displaces nothing, so content the user may be about to press does not move without their own action.

## Decision

1. **Nothing that the application shows by itself moves the list.**
   Between two listings, the head of the list keeps its height whatever the container's settings, the listing's kind or a count say.
   The path's row is one line: a long container name or a deep folder ends in an ellipsis, its whole path in its hover text, and gives way before the marks do.
   Only the operator's own actions change the head's height: opening Find, or a narrower window, so the action rows wrap.
2. **Notices are marks on the path's row.**
   "Public", "Settings not read", "Backstage" and "Possible segment store" are short marks after the path, each in its colour as today: red for public, amber for backstage and a possible segment store, quiet for unread settings.
   Each mark's hover text and accessible name are the whole sentence its banner says today, so nothing it tells is lost.
   The container list's own marks are not changed.
3. **Refresh and count are small buttons at the start of the path's row, beside the container's name.**
   Each is named by its hover text, as the Containers column's tools are.
   They come before the path, so nothing that appears on the row, a count's answer or a mark, moves them; after the path, they moved left whenever a count's answer took room from it (measured in the selection journey).
   The count button counts the one folder selected, when exactly one folder is selected, and otherwise the folder being browsed or the container's top; its hover text names which, and while it counts it stops the count.
   The count's answer is a short text beside the button, "1,204 objects in logs/", with "so far" while it counts and the time it was counted in its hover text, and goes when the operator moves to another container or folder.
4. **The deletions keep their own row, at the left.**
   The row is the one they have today, with the same slots, so a button's label following the selection moves nothing; it starts at the left like the rows above it, and its buttons keep the danger colour.

## Why these shapes

**Keeping room for every banner** was considered and not taken.
An empty band the height of the longest notice, most of the time empty, pushes the list down for nothing, and the public notice, the longest, would set its height.

**Drawing notices over the list** was considered and not taken: a notice drawn over rows hides the rows the operator is about to press.

**A mark with the sentence in its hover text** keeps what the notice says one hover away, and its colour keeps the warning in sight.
The public mark is red, beside the container's name, which is where the operator reads which container they are in.

**The refresh at the far right**, where it is, was a stable place but not one the operator found; beside the path is where they looked.

## Required proof before implementation is accepted

Tier 3.

- Frontend, component: for a public container, a container whose settings were not read, a backstage listing and a possible segment store, the list's head is the same height as for an ordinary container, and the mark's accessible name is the sentence; a count leaves the head's height unchanged, and names the folder it counted.
- Frontend, component: with one folder selected the count asks for that folder; with nothing, a file or several rows selected it asks for the folder being browsed.
- Browser journey: the count is started from the button beside the refresh for a selected folder and its answer read on the path's row, with the list and the toolbar's buttons where they were; the existing journeys that read a banner read the mark instead.
- The rendered list head looked at for each notice, and for the deletions' row, before review.

## What this does not do

It does not change the Containers column, the inspector or any dialog.
