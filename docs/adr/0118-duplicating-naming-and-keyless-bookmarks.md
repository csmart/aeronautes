# ADR 0118 — duplicating a bookmark, naming imports, and a bookmark with no saved key

**Status:** Accepted for 0.5.0 (13 November 2026), delivered with this record's series, from the operator's test run of the final candidate and decisions of the same day.
**Scope:** the bookmark editor, the rail, the welcome dialog, the import preview and its save, and the copy dialog's list of bookmarks.
**Amends:** [ADR 0114](0114-importing-bookmarks.md) decisions 1 and 7.

## Context

An operator often has several accounts on one cluster.
Each needed a bookmark typed from nothing, although only the user, the project and the key differ.

The import of ADR 0114 was reachable only from Settings, took each bookmark's name from its source without asking, and saved a source that held no secret as a bookmark without a credential.
Such a bookmark, like one restored from a configuration backup, looked like any other on the rail; choosing it tried to connect and failed with "no saved key is available; enter it again", and nothing led to where the key is entered.

## Decision

1. **A saved bookmark can be duplicated.**
   The editor of a saved bookmark offers "Duplicate…", which closes it and opens the editor on a new bookmark filled from the saved details, not from unsaved edits, and named "<name> copy".
   Three things are not carried.
   The key is not, because the window never holds one (K-SEC-002), so the new bookmark asks for its own.
   The CA bundle is not, because a read reports only that one is stored; a bookmark that used one asks for it again.
   The storage URL override is not, because it names the first account's storage and the editor has no field to correct it.
   The editor says so, and the copy is saved by the ordinary save; there is no new bridge action.
2. **The import is offered where a bookmark is first wanted.**
   "Import bookmarks…" stands beside "Add a bookmark" in the welcome dialog and beside "Add" on the rail, as well as in Settings.
   Each opens the same file picker and the same preview.
   The application still reads only the file the operator picks.
3. **The preview names each bookmark, and the operator may change the name.**
   Each row that can be imported carries a name field holding the source's name, and the name in the field when the import is pressed is the bookmark's display name.
   The save takes that name with each chosen row, and refuses an empty one before the token is spent, as it refuses a row that cannot be imported.
   The preview and the save still carry no secret.
4. **A bookmark with no saved key says so, and asks for the key where it is used.**
   This covers a bookmark imported from a source that held no secret and one restored from a backup, which never carries a key.
   The rail marks it "No saved key".
   Choosing it opens its editor with a note and the key field focused, in place of a connection that can only fail.
   The import's summary offers "Enter key" on such a row, which opens the same editor.
   The copy dialog lists it as "(no saved key)" and it cannot be chosen there.
   A key is typed only in the editor: the import dialog gains no secret field, so nothing new carries a secret across the bridge.
   A bookmark whose key was later erased from the credential store still looks keyed until it is used, and keeps the existing message.
5. **Not in this record.**
   Looking for configuration files without being asked; updating an existing bookmark from a file; the region as part of what makes an import "already a bookmark"; carrying a CA bundle into a duplicate.

## Consequences

- A second account on a cluster is a duplicate, two or three changed fields and a key.
- An import never changes an existing bookmark: each chosen row is still saved as a new one (ADR 0114 decision 7), now under the name the operator gave it.
- A duplicate of a bookmark with a custom CA needs the bundle pasted again.

## Tests

- Duplicate opens a new bookmark filled from the saved one with an empty, required key; saving it creates a second bookmark and leaves the first as it was; the storage URL override and the CA bundle are not carried.
- The welcome dialog and the rail each open the import that Settings opens.
- A name changed in the preview is the saved bookmark's name; an empty name is refused and the token is still good; an import of an entry that is already a bookmark leaves the existing bookmark as it was.
- A bookmark with no saved key is marked on the rail and opens its editor when chosen, with no connection attempted; the import summary's "Enter key" opens it; the copy dialog does not let it be chosen.
- Browser journeys: duplicate a bookmark and connect with the copy; import with a changed name, from the rail.
