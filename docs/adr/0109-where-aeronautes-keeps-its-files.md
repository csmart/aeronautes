# ADR 0109 — where Aeronautes keeps its files

**Status:** Proposed on 12 November 2026, from the operator's decisions of 11 and 12 November during the 0.5.0 test run.
**Scope:** where the application keeps its settings, its database and the files beside them; how `SettingsService` persists the settings; the settings backup's snapshot; the documentation that tells an operator where everything is.

## Context

Until now one SQLite database at `$XDG_STATE_HOME/aeronautes/state.sqlite` (by default `~/.local/state/aeronautes/`) held everything: the application settings, the bookmarks and their custom CA certificates and profiles, the jobs and their item records, the recovery records, the audit trail and the segment evidence.
Custom CA files sit in `ca/` beside it, the cache is `$XDG_CACHE_HOME/aeronautes/`, keys live in the OS keychain (or for the session only when none is reachable), and a log file is written only where `AERONAUTES_LOG_FILE` names.
None of this was written down where an operator would look.

The XDG Base Directory Specification, version 0.8 of 8 May 2021, defines `$XDG_CONFIG_HOME` (default `~/.config`) for "user-specific configuration files", `$XDG_DATA_HOME` (default `~/.local/share`) for "user-specific data files", and `$XDG_STATE_HOME` (default `~/.local/state`) for "state data that should persist between (application) restarts, but that is not important or portable enough to the user that it should be stored in `$XDG_DATA_HOME`", such as logs, history and view state.
The bookmarks, the recovery records and the audit trail are important to the operator, so the state directory was the wrong home for most of the database, and the application settings are configuration.

The settings tables (`app_settings`, and the unused `ui_preferences`) have no links to anything else.
The bookmarks are linked by enforced foreign keys to the segment evidence and ownership records, capability snapshots, per-bookmark profiles and jobs, and in the WAL mode the database uses SQLite does not commit a transaction atomically across two attached database files.
No release has shipped, so no installed copy has data at the old path.

## Decision

1. **Settings are a configuration file.**
   The application settings, the fields of `AppSettingsDTO`, live in `$XDG_CONFIG_HOME/aeronautes/settings.json` as one JSON object, readable and editable by hand.
   The directory is created with mode 0700 and the file is written with mode 0600, atomically: a temporary file in the same directory, flushed, renamed over the old one, and the directory flushed.
   A missing file means the defaults.
   Each value read is checked as `Set` checks it; a value outside its range, a wrong type, or a file that is not valid JSON makes the whole file unreadable: the application runs on the defaults, the Settings view says the file could not be read and why, and the file is not replaced until the operator saves, when the unreadable one is kept beside it as `settings.json.unreadable`.
   Keys the application does not know are ignored and are not written back.
2. **Everything else is one database in the data directory.**
   `$XDG_DATA_HOME/aeronautes/aeronautes.sqlite`, with its WAL files, the single-instance lock file and the `ca/` directory beside it, holds the bookmarks with their CA certificates and profiles, the jobs and items, the recovery records, the audit trail and the evidence, so no transaction spans two stores.
   A migration drops the `app_settings` and `ui_preferences` tables.
3. **Unchanged:** the cache stays in `$XDG_CACHE_HOME/aeronautes/`, keys stay in the OS keychain or the session, and a log file is still written only where `AERONAUTES_LOG_FILE` names.
4. **One override for development and tests.**
   When `AERONAUTES_STATE_DIR` is set, both `settings.json` and `aeronautes.sqlite` (with the lock and `ca/`) live in that one directory, so a scratch run never touches the real ones.
5. **The settings backup keeps one moment.**
   The export holds the settings lock while it reads the settings file and opens the read transaction for the bookmarks, so a backup never pairs settings and bookmarks that did not exist together; restore is unchanged.
6. **No migration.**
   Nothing reads `$XDG_STATE_HOME/aeronautes/state.sqlite`: no release used it.
   A development database there is left alone; the release notes say it can be moved by hand to the new database path, and that its settings return to their defaults.
7. **The operator is told.**
   The README and the release notes say where each kind of file lives, and DESIGN §51 points here.

## Consequences

- An operator finds and can back up their preferences in `~/.config/aeronautes/` and their data in `~/.local/share/aeronautes/`, where the specification and their tools expect them.
- Hand edits to `settings.json` take effect at the next start or the next read, and a bad edit cannot be half-applied.
- Unknown keys are dropped on save, so a setting written by a newer version is lost if an older version saves; acceptable before 1.0.0.

## Tests

- Go, settings: a save writes the file and a fresh service reads the same values; a missing file gives the defaults; invalid JSON, a wrong type and an out-of-range value each leave the defaults in force, report the reason, and keep the file untouched until a save, which keeps it as `settings.json.unreadable`; the written file and directory have modes 0600 and 0700; a write that fails before the rename leaves the old file whole.
- Go, paths: with `XDG_CONFIG_HOME` and `XDG_DATA_HOME` set, the settings and the database land under each; with `AERONAUTES_STATE_DIR` set, both land in that directory.
- Go, migration: a database created by the previous schema loses `app_settings` and `ui_preferences` and keeps every other table's rows.
- Go, backup: the export reads settings and bookmarks under one settings lock.
- Product: the shipped binary started with empty `XDG_CONFIG_HOME` and `XDG_DATA_HOME` creates `settings.json` on the first save and `aeronautes.sqlite` at start, and nothing under `XDG_STATE_HOME`.
