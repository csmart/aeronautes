# Connection reset and bookmark lifetime

This records P07's third implementation under §65.11 and audit UI-05.
The oracle is a truthful disconnected UI after active-bookmark save/delete admission or a reconnect attempt, with at most one frontend Connect operation admitted until its bridge call and dependent reads settle.
Late work cannot repopulate that retired connection or continue a closed editor's Save & connect action.

## Implementation boundary

One reset clears the account, capabilities, container and listing state, markers, errors, selection, inspector details, segment previews, backstage context and scope-bound dialogs.
It advances the existing UI revisions and synchronously detaches the previous Find id before requesting asynchronous cancellation.
Active-bookmark saves and deletions reset at admission, including failed or uncertain results; inactive-bookmark changes preserve the active view.
Credential cleanup status still refreshes on both success and failure without replacing the mutation's outcome.

A private pending bookmark id holds Connect admission independently of the displayed connection.
Editing or deleting its target invalidates publication while keeping that slot held through the already-started bridge call and dependent read.
Each subsequent read rechecks the original scope and optional editor lifetime before starting the next step.
The account, capabilities and container page publish together only when the complete current attempt settles.
Optional capability, container-list and audit failures retain their existing behavior; a failed connection remains disconnected and reports its original error.
Per-bookmark reference counts also refuse reconnect while any save or deletion of that target remains pending.
These frontend guards do not serialize other clients or establish Go credential authority.

BookmarkEditor captures its original bookmark and lifetime, freezes pending fields and refuses duplicate or obsolete submissions.
Save & connect captures connection intent after its own synchronous save invalidation, so a current editor can reconnect but a later external connection intent cannot be replaced by its delayed save.
Closing or unmounting also retires publication from an already-admitted Connect without releasing its private slot early.
After a new bookmark saves but connection fails, retry uses the saved id rather than creating another bookmark.
Save-only still reports its own completion and leaves an edited active bookmark disconnected.

Initial reads and load-more replies crossing reset cannot restore old rows or details.
Obsolete page errors retire with their scope; current page errors still reach their existing caller.
Find startup, polling, errors, finalisation and match navigation recheck connection ownership across reset.
A late startup id is cancelled without being installed or polled, and a delayed old cancellation cannot clear a newer scan's id.
Backstage navigation rechecks scope after both its container and prefix loads.

## UI scenario matrix

The maintained harness is `frontend/src/features/browser/connectionBinding.test.ts`.
It exercises production BrowserState and mounted BookmarkEditor/FindPanel components with fixture-derived DTOs, a recording Api boundary and deferred replies.
Bounded microtask settling covers the sequential save and connection workflows without a wall-clock sleep.
These are separate UI-to-bridge scenarios under §65.10 rule 4 and earn no Go service-scenario credit.

| Scenario | Maintained test or group |
| --- | --- |
| Archived UI-05 account deletion assertion | `clear account information when the active bookmark is deleted` |
| Complete active reset before success or failure | `bookmark save/delete invalidation` |
| Inactive success/failure preserves current connection | `an inactive bookmark ... preserves the active view` |
| Reconnect cannot race a pending target mutation | `cannot reconnect to a bookmark whose change has not settled` |
| Overlapping saves retain the target gate until both finish | `keeps the bookmark change gate until every overlapping mutation settles` |
| Single Connect through auth, capability, container and audit awaits | `Connect at ... holds one bridge slot` |
| Save/delete during every Connect stage retires publication and further reads | `an active ... retires publication but cannot release the pending slot` |
| Current connection failure and optional-read outcomes | `handles a current failure without inventing a successful optional read` |
| Old initial lists and inspector HEAD replies cannot repopulate reset | `reset-crossing reads` |
| Old page success/error retires; current errors survive | `retires pending ... pages` and `preserves a current ... page failure` |
| Both awaited backstage steps retain original scope | `retires backstage navigation and its label` |
| Delayed Find startup is cancelled; pending cancellation cannot start a scan after reset | `Find across connection reset` |
| Old startup/poll success/error/finalisation cannot alter a new scan | `an obsolete ... cannot alter a new connection scan` |
| Late Find match click cannot navigate or close a later connection | `a pending match click cannot navigate a later connection` |
| Current Save-only, Save & connect and deletion; frozen fields and duplicate refusal | `bookmark editor save/connect/delete` |
| Close/unmount retires success/error callbacks | `... retires the pending callback` |
| Changed bookmark props cannot borrow the old form | `a changed bookmark prop cannot borrow the existing form` |
| External connection intent retires automatic Connect | `a different connection intent stops automatic Connect` |
| Close/unmount during admitted Connect retires publication while holding its slot | `editor ... during Connect retires publication` |
| Failed connection after creation retries the saved identity | `a failed Connect after creation retries the saved bookmark` |

## Evidence

With Node 22.23.2 and unchanged locked dependencies, all **554 tests in 17 frontend files pass**, with zero failures or skips.
Whole-source statement coverage is **70.93%** (2,741 of 3,864 statements), not an audit completion percentage.
Frontend type checking reports zero errors and warnings, the production build completes with 205 modules, and `go build ./...` passes with the final assets.
No Go implementation, bridge model, dependency or workflow changed.
Independent local review and all five current-head/current-master PR checks, followed by the resulting master checks, remain required.

All **74 new cases** ran against the parent production files at `6ed96705326da1f4`: **65 assertion failures and nine passing controls**, with zero skips, setup errors or unhandled rejections.
The identical cases pass after this implementation.
The first four scratch reproductions separately failed their intended assertions before implementation and passed afterwards.
An intermediate full run exposed 14 inherited P06 expectations that retained the old location or re-enabled preview after failed reconnect.
They now require a complete disconnected state and disabled preview/picking; manually restoring the old names still cannot revive the old preview or preflight.
The original stale-operation refusal assertions remain intact.
Three new positive Save & connect cases initially checked their callback before both sequential workflows had settled; the bounded microtask helper was extended without changing production code to satisfy those assertions.

The archived UI-02 and UI-05 bodies are also run separately, with only their import locations changed.
On this slice's parent, UI-02 passes and UI-05 fails at its account assertion; after the reset both pass, with the other ten archived cases unselected.
That is two selected probes, not a passing claim for the entire archived audit.

The scratch control runner loads all 74 cases for each independently broken variant and requires assertion failures, zero skips and no import, transform or unhandled-promise failures.
All 34 variants are caught:

| Removed or broken safeguard | Failing cases |
| --- | ---: |
| Active-bookmark reset | 31 |
| Pending Connect target invalidation | 8 |
| Pending bookmark change admission gate | 3 |
| Overlapping bookmark change count | 1 |
| Single Connect admission slot | 15 |
| Connect scope ownership | 8 |
| Connect caller lifetime | 2 |
| Post-auth current check | 2 |
| Post-capability current check | 2 |
| Post-container-list current check | 2 |
| Post-audit current check | 2 |
| Complete snapshot before account publication | 7 |
| Reset account and capabilities | 39 |
| Reset selection and inspector state | 18 |
| Reset dialog state | 17 |
| Reset Find status and cancellation | 21 |
| Container page result scope | 1 |
| Object page result scope | 1 |
| Obsolete page error retirement | 2 |
| Backstage follow-up scope | 2 |
| Find scope after previous cancellation | 1 |
| Late Find startup cancellation | 2 |
| Find poll result ownership | 1 |
| Find error ownership | 2 |
| Find finalisation ownership | 3 |
| Synchronous old Find id detachment | 2 |
| Find match navigation scope | 1 |
| Bookmark editor lifetime | 12 |
| Bookmark editor duplicate admission | 3 |
| Post-save connection intent | 1 |
| Editor lifetime passed through Connect | 2 |
| Saved bookmark identity on retry | 1 |
| Intent capture after the editor's own reset | 4 |
| Original bookmark prop identity | 1 |

Local artifacts are under `/tmp/aeronautes-remediation-20260905`: `p07-reset-final-*`, the reproducible `p07-reset-controls.py`, complete `p07-reset-controls-v1/results.json` with per-variant reports, and `p07-reset-archived.py` / `p07-reset-archived/` for the selected original probes.
The earlier four-case reproduction is retained as `p07-reset-before.*`.

## Remaining work

This completes the planned P07 frontend connection-reset scope when independently reviewed and delivered.
It does not cancel already-admitted writes, establish backend authority, or prove storage contents or native platform behavior.
P08 still owns ordinary page deduplication/error presentation and the full Find start/stop/navigation lifecycle, including overlapping starts within one unchanged connection.
P10 native authority and cleanup, P11 backend capability/replay/expiry, P13 outcome presentation, P14 external writers and P15 ownership remain open.
The valuable-data release hold remains.
