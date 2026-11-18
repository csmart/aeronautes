# ADR 0127 — the local folder in a job's record, and the log in Settings

**Status:** Proposed on 18 November 2026, from the operator's requests while testing the 0.5.0 candidate.

## Context

Testing candidate 9b5d7127 on 18 November 2026, the operator asked for two things.

- "when I choose something in the picker, it would be good if that was updated in the worker tray - I downloaded something but forgot where and I can't see where from the log".
  A download job records its destination as "local directory (picker-selected)", and an upload its source as "30 local files", so neither the drawer, Copy details, nor the history says which folder.
  [ADR 0078](0078-overlapping-download-destinations.md) gives the reason as [ADR 0056](0056-download-preflight-view.md) keeping paths off the bridge.
  That rule is about what the page may send: the page never hands Go a path to write to, only a token the Go picker issued.
  A path Go shows back to the operator who picked it is a different direction, and the held destination already keeps its pathname as "display and record text only" (`internal/transfer/local/download_root.go`).
- "is there an option to enable debug/info/logging location from the app? that'd be handy to be able to turn it on and off dynamically (as well as support env flags)".
  [ADR 0099](0099-application-log.md) gave the log four levels and two environment switches, `AERONAUTES_LOG_LEVEL` and `AERONAUTES_LOG_FILE`, read once at start; nothing in the application shows or changes them, and a desktop launch has no environment the operator can easily set.

## Decision

1. **A job names the local folder.**
   A download records its destination as "local directory" followed by the path of the directory picked, as the held destination captured it.
   An upload records its source as the count of files followed by "from" and the deepest folder that holds them all, which is the folder picked for a folder upload with files at its top and the files' own folder for a pick of files.
   The drawer, Copy details and the history show what the job recorded, so each names the folder; nothing else changes, and the job still writes through the held directory, never by that path.
   Jobs recorded before this keep their old words.
2. **Settings has a Diagnostics section with the log level.**
   "Log level" offers debug, info, warn and error, and the default is info, as ADR 0099 set.
   A change applies at once, to every line written after it, and is saved with the other settings.
3. **Settings can keep a log file.**
   "Keep a log file" writes the log to `aeronautes.log` in the state folder [ADR 0109](0109-where-aeronautes-keeps-its-files.md) names (on Linux `$XDG_STATE_HOME/aeronautes/`, the place it gives for logs), as well as to standard error.
   The section shows the file's full path with a copy button, so the operator can find it.
   It applies at once: turning it off stops writing and keeps the file.
   The default is off, so an ordinary launch keeps no log on disk, as ADR 0099 decided.
   When the file passes 10 MiB it is renamed `aeronautes.log.1`, replacing an older one, and a new file is started, so a debug session left on cannot fill the disk; at most about 20 MiB is kept.
4. **The environment switches still work, and win at start.**
   When `AERONAUTES_LOG_LEVEL` or `AERONAUTES_LOG_FILE` is set, it is in force from launch over the saved setting, and Settings says so beside that setting ("set by AERONAUTES_LOG_LEVEL at launch").
   A change made in Settings during that session still applies at once, because it is the operator's latest word, and it is saved; the switch wins again at the next launch.
   A file named by `AERONAUTES_LOG_FILE` is the one Settings shows and turns on or off, and it is not rotated, since the operator chose that path.
5. **Nothing secret is added to the log.**
   The level only lets through lines the code already writes at debug; ADR 0099's rule that no credential reaches a log call is unchanged.
   A Reset to defaults (ADR 0126) puts the level back to info and the file off.

## What does not change

The four levels, the line format, and what each line says.
Every bound action: the two settings ride on the existing settings document and its save, and the file's path and the launch switches are reported in that document, never written to the settings file.

## Required proof (tier 3)

Backend: a download job started on the mock records the picked directory's path, and an upload of a folder records that folder; a level change applies to the next line without a restart; the file turns on and off, and rotates at its size; a launch switch wins over the saved setting at start, and a later save still applies.
Frontend: the Diagnostics section shows the level, the file switch with its path, and the launch note when a switch is set.
Journey, on the mock: a download's drawer entry names the folder picked; in Settings the log file's path is shown once it is on.
