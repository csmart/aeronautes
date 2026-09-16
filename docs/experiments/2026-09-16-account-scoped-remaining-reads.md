# Account-scoped remaining reads — 16 September 2026

Finish-line item L11, safety package P28, the surfaces [ADR 0049](../adr/0049-account-scoped-reads.md) left open.
The completion test is that identical names in different accounts remain distinguishable, and that results, paging, retry and error state stay bound to the selected account.
This record is the source survey of those remaining methods on `e770f8a9`.
It is not a new runtime probe and it does not close L11 or P28.
The correction in [ADR 0059](../adr/0059-account-scoped-remaining-reads.md) is implemented against that survey.

## What was asked

Whether inspector detail, find matches, audit-recent rows, job Get and the remaining metadata HEADs are bound to the account they are for, or merely to the most recent request and the active connection; and whether a page, a poll, a retry or an error on those surfaces can silently become another account's.

## Method

Read the service methods and reply types on GitHub master `e770f8a9`.
The mechanism is the one the [account scope record](2026-09-14-account-scoped-results.md) already reproduced for listings: `connections.Session()` at call time, a reply that names no account, and `BrowseService.Connect` reachable from the bridge.
No production source was changed to produce this survey.

## What remains unbound

| Surface | Request names an account | Reply states the account |
| --- | --- | --- |
| `InspectorService.Object` / `Prefix` / `Container` / `SLOManifest` / `DLOSegments` | No | No |
| `FindService.StartFind` / `FindStatus` | No | No |
| `AuditService.Recent` | No | No (`AuditEventDTO` omits stored `BookmarkID`) |
| `JobService.Get` / `Items` / `ItemTotals` | No | `JobDTO` already names both ends; Get does not check them |
| `BrowseService.Containers` / `Objects` | Yes (ADR 0049) | Yes |

`InspectorService.Container` classifies evidence with `connections.ActiveBookmarkID()`, which is the active connection rather than a caller-named bookmark.

`FindService.StartFind` captures `Session()` once and the worker keeps that session, so an in-flight scan does not retarget when the connection moves.
The snapshot still names no account, so a poll displayed after a silent `Connect` is indistinguishable from a scan of the new account.

`AuditService.Search` already accepts `bookmarkId` and treats empty as every account.
The frontend doorway currently passes an empty one.

`JobService.List` and `History` already take a bookmark identifier.
`Get` does not, so a job id from one account's list still loads another account's row.

## What this does not establish

It does not re-run the two-account listing probe.
That probe remains the reproduction of the live-session mechanism; these remaining methods share it by construction, or they are local store reads that omit the filter `List` already has.

It does not measure how often a connection moves under an inspector HEAD or a Find poll in ordinary use.

It does not claim P28 or L11 closed, and it does not authenticate which account the operator approved.

## Correction

[ADR 0059](../adr/0059-account-scoped-remaining-reads.md) applies ADR 0049's binding to the remaining live Swift reads, binds a Find scan at start, and filters the local audit-recent and job-Get reads by the named bookmark.
