# ADR 0125 — the inspector's actions at its top, metadata key suggestions, copying a value, and further requests from the 0.5.0 test run

**Status:** Accepted on 18 November 2026 for 0.5.0, on the operator's requests of that day during the candidate's test run.
Tier 3 under DESIGN §65.12: it moves buttons, suggests text for a field the operator still types and saves, and copies what is shown; no request, refusal or saved value changes.
**Scope:** the inspector's object, container and account views, and the key field of the metadata editor in the object, container and account editors.
Extended the same day by decisions 5 to 10, from more of the operator's requests: a new folder by name, a filter for the Containers column, which deletion the working list offers while rows are selected, clearing a selection by pressing the list's empty space, reading a container's counts again after a job, and a setting for the graphics card.

## What exists

- The inspector's actions sit below its cards: "Edit metadata" and "Delete object…" under an object's details, "Configure container" under a container's cards and TempURL keys, "Edit metadata" under the account's.
  They move up and down with what the cards hold, and the operator found "Configure container" in the middle of the pane.
- A metadata key is typed into an empty field with nothing to say which keys mean something to the store, or which the operator already uses.
- A value in the inspector, such as an object's ETag or the transaction id of the read, can be read but has to be selected by hand to be copied.

## The behaviour oracle

- The operator's requests of 18 November 2026: "does the configure button being in the middle a good location for it?"; "for adding keys to metadata, we should auto-complete as people start typing.. but we also need a way to help people know what the options are... so it can't be just the start, it has to be anywhere in the string"; "in the object metadata, it'd be good if we can copy the result, like I see the transaction id or the sha, click a little copy button on the right".
- Swift's own meaning for container metadata keys: the static website keys read by `staticweb` and the cross-origin keys read by the proxy.
- The rclone convention the application already writes: an object's `Mtime`.

## Decision

1. **Each view's actions are one row under its name.**
   An object's "Edit metadata" and "Delete object…", a container's "Configure container", and the account's "Edit metadata" are a row directly under the view's title, before any card, so they are in the same place whatever the cards hold.
   An editor opened from that row opens in the same place, under the row.
2. **A metadata key field suggests keys, matching anywhere in the key.**
   When the field has focus its suggestions are listed under it, all of them while it is empty, so the operator can see what there is before typing.
   Typing keeps those that contain what was typed anywhere in the key, ignoring case, with the matching part marked.
   Arrow keys move through the list, Enter or a click puts the key in the field, and Escape closes the list; the operator can always type a key that is not suggested.
   The list is drawn by the application, as the hover text is (ADR 0123), not left to the webview's own suggestion list.
3. **What is suggested.**
   Keys that mean something to the store for that kind of item, each with one line saying what it does: for a container the static website keys `Web-Index`, `Web-Error`, `Web-Listings`, `Web-Listings-CSS`, `Web-Listings-Label` and `Web-Directory-Type`, and the cross-origin keys `Access-Control-Allow-Origin`, `Access-Control-Max-Age` and `Access-Control-Expose-Headers`; for an object `Mtime`.
   Then the keys the operator has already used on items of the same kind that this session has read, so their own conventions come back.
   A key the item already has is not suggested again, and a TempURL key or a quota, which have editors of their own, is never suggested.
4. **A value in the inspector can be copied.**
   Every value in the inspector's cards has a small copy button at the right end of its row, drawn faintly and always there, so nothing appears or moves when the pointer arrives; a row that shows no value has none.
   Its hover text names what it copies ("Copy ETag"), and after a click it says "Copied" for a moment, or "Not copied" with the reason when the clipboard refused it.
   It copies the value as shown, through the clipboard path the row menu's "Copy name" uses.

5. **A folder can be created by name.**
   "New folder…" beside "Upload…" asks for a name and creates the folder's marker object, the empty object at the folder's name that "Create marker object" makes for a folder objects already imply, in the folder being browsed.
   The name may hold "/" to create a folder inside a new folder; it may not start with "/" or hold "//"; a trailing "/" is the one the marker adds.
   The listing is then read again, so the folder is listed.
   The store refuses it, as for "Create marker object", where a marker or an object with data is already at that name.
   The operator found no way to add a folder; there was only the marker for a folder that already had objects under it.
6. **The Containers column can be filtered.**
   A field under the Logical, All and Backstage tabs keeps the loaded containers whose name holds what is typed, anywhere and ignoring case, in every view and group; when none does it says so, and says that more can be loaded where they can.
   It filters what was loaded and reads nothing.
7. **With rows selected, the working list offers only their deletion.**
   Amends [ADR 0121](0121-emptying-and-deleting-a-container-are-separate-actions.md) decision 2.
   When an object or several rows are selected, the deletions' row offers only the selection's own deletion, the object's, marker's or manifest's, or "Delete selected…", and not the prefix deletion of the folder being browsed or the container's two, which stay in the container's menu.
   With one folder selected alone its prefix deletion is offered, as before; with nothing selected the folder's, or at a container's top the container's two.
   They share one slot as wide as its widest content, so a selection changes the words in it and moves nothing.
   The operator found "Delete prefix…" beside the selection's own deletion, naming the folder being browsed while rows in it were selected.
8. **A press in the list's empty space clears the selection,** as Escape does; a press on a row selects it as before.

9. **A container's counts are read again after a job that changed it.**
   The view is already read again when the job ends, but the store updates a container's object count and bytes, and the account's, in the background, so that read can show the old numbers.
   The container's details and the container list are read twice more, about 5 and 30 seconds after the job ends, while the operator is still in that container, and not at all once they have moved on.
10. **The window's use of the graphics card is a setting.**
    "Use the graphics card" in Settings, on by default, gives the webview WebKitGTK's "always" hardware acceleration policy, as it had; off gives it "never", drawing in software, for a host whose driver makes the window slow or blank.
    The webview takes its policy when it is made, so the setting is read at start and a change applies at the next start, which the setting says.

## Why these shapes

**Matching only the start of a key** was not taken: the operator asked to find keys by any part, and `listings` should find `Web-Listings` and `Web-Listings-CSS`.

**Reading other objects' metadata to suggest keys** was not taken: it needs a HEAD per object, which DESIGN §14.2 refuses; the session's own reads cost nothing.

**A copy button that appears on hover** was not taken: it would move under the pointer, which ADR 0123 removed elsewhere.

## Required proof before implementation is accepted

Tier 3.

- Frontend, component: the actions row is above the first card in each view; a key field lists every suggestion while empty, filters by any part of the key ignoring case, takes a suggestion by Enter and by click, closes on Escape, leaves out keys the item has and TempURL and quota keys; a copy button copies its row's value and a row with no value has none.
- Frontend, component: the copy button hands the row's exact value to the clipboard, says "Copied" when that succeeded and says so when it did not.
- Browser journey on the mock: open an object's editor from the row under its name, pick a suggested key by typing part of it, save, and read it back; press the ETag's copy button and read its "Copied". The journeys run without a desktop clipboard, so the value itself is checked by the component test.
- Frontend, component: "New folder…" creates the marker at the folder being browsed and the folder is listed; the container filter keeps names holding the text anywhere and says when none does; the deletions' row offers only the selection's deletion while rows other than one folder are selected, and the container's two or the folder's with nothing selected; a press below the rows clears the selection.
- Frontend, component: a deletion that ended reads the container's details again after 5 seconds and not after the operator moved on; the graphics card setting saves off and says it applies at the next start. Go: the setting defaults to on for a file without it, survives a save, and gives the webview the "never" policy when off.
- Browser journey on the mock: create a folder by name and see it listed; filter the containers; the selection journey reads the selection's deletion in place of "Delete prefix…", with nothing moving.
- Each changed surface looked at in the rendered window.

## What this does not do

It does not suggest values, check a key's spelling against the store, or change what a save sends.
