# ADR 0126 — settings filter, reset, the jobs limit in one line, and refusals in words

**Status:** Proposed on 18 November 2026, from the operator's requests while testing the 0.5.0 candidate.

## Context

Testing the candidate on 18 November 2026, the operator asked for five small changes:

- The download dialog refused a start with the kind `destination-overlap` and a reason cut short by an ellipsis ("download job 08a5ab1c… is using /tmp; cance…"); they read it as the queue refusal they were testing and could not see why it was refused.
- With an active-job budget of 2 and a queue depth of 1, three downloads were admitted before a fourth was refused, and they found that confusing: "why the different settings?".
- "can we make settings window searchable by filter too?"
- "can you add an option to settings to RESET to the current app version defaults"
- The settings gear and the light/dark switch sit at opposite ends of the rail's foot: "someone might make that really wide which looks weird".

Their notes on the connection cap also showed its note reads "1 connections per host".

## Decision

1. **A refusal in a transfer dialog is a sentence.**
   Each problem row of the upload and download dialogs shows the name and the reason in full, wrapping onto further lines, and names its kind in words ("Overlaps a running download", "Already exists", "Name not allowed here") rather than the code; the code stays in the report and the job's record.
   An overlap refusal's reason names the running job by its source and folder, and says to wait for it, cancel it or choose another folder.
2. **The two job limits read as one.**
   In Settings the active-job budget and the queue depth stand together under "Jobs at once", with one line under them that adds them up: "Up to 2 running and 1 waiting, 3 at most; a further start is refused."
   The runner's refusal names both: "the job queue is full: 2 running and 1 waiting; wait for one to finish or cancel one" (with "or paused" and "resume one" as now when a waiting job is paused).
   The limits themselves, their ranges and their defaults are unchanged.
3. **Settings can be filtered.**
   A filter box at the top of the Settings view keeps the settings whose label, note or section heading holds the typed text, ignoring case, and the sections with none left are hidden; with nothing typed everything shows.
   A filter that matches nothing says so.
   Backup and restore stay at the foot and are not filtered.
4. **Settings can be reset to this version's defaults.**
   "Reset to defaults…" at the foot of Settings asks once, in the window, naming that every setting returns to this version's default and that bookmarks, saved keys, jobs and history are not touched; confirmed, it saves the defaults as one save does, through the settings service, and each setting takes effect as its note already says (the connection cap at the next connect, the graphics card at the next start).
   It is a settings save, so a restore of an earlier backup can undo it.
5. **The rail's two foot buttons sit together**, side by side at the start of the foot, however wide the rail is.
6. **The connection cap's note counts in the singular** for one connection.

## What does not change

The refusals themselves, which overlaps are refused, the job limits' values, what a settings save checks, and the settings file's format.

## Required proof (tier 3)

Frontend component tests: a refused download shows the kind in words and the whole reason, with no ellipsis; the jobs line adds the two limits; the filter keeps a matching setting and hides a section with none, and says when nothing matches; reset asks first and, confirmed, saves the defaults the service returns, and cancelled saves nothing; a connection cap saved as 1 reads "1 connection per host".
Go: the queue refusal names the running and waiting counts, with "or paused" and "resume one" while a waiting job is paused; a reset saves the defaults and applies the two job limits.
A rendered screenshot of the Settings view, the refused dialog and the rail's foot is read before review.
Journey: the settings journey filters to one setting, and resets after changing one, which then reads its default.
