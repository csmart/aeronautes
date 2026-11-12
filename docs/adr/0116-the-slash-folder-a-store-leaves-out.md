# ADR 0116 — showing the "/" folder a store leaves out at the top

**Status:** Accepted for 0.5.0 (13 November 2026), delivered with this record's series, from finding F31 and the final-candidate plan's item 7 ("the '/' folder some stores omit at the top").
**Scope:** the browse view's listing at the top of a container.

## Context

A copy whose destination folder began with "/" wrote objects named "/name" (F31); the dialog now warns about a leading "/", but such objects exist and can be made by other tools.
At the top of a container the browse view lists with the "/" delimiter, and the store groups names into folders.
Measured on 13 November 2026 on a container the run owned, holding `/a.txt`, `/b/c.txt`, `plain.txt` and `dir/d.txt`:

| Listing | OpenStack Swift | Ceph RGW |
| --- | --- | --- |
| top, delimiter "/" | folder `/`, folder `dir/`, `plain.txt` | folder `dir/`, `plain.txt` |
| prefix "/", delimiter "/" | `/a.txt`, folder `/b/` | `/a.txt`, folder `/b/` |
| no delimiter | all four names | all four names |

Ceph RGW leaves the "/" folder out of the top listing, so on that store every object whose name starts with "/" is invisible in the browse view, and so are the actions on it.
Listing inside it with the prefix "/" works on both stores, and jobs list without the delimiter, so they already see every name.

## Decision

1. **At the top of a container, a page that covers where "/" sorts and shows no "/" folder asks once whether one exists.**
   A page covers it when its marker sorts before "/" and either it is the last page or its last row sorts after "/".
   The browse service then lists with the prefix "/" and a limit of one; when anything is there, it adds a "/" folder row in its sorted place on that page.
   A page that already shows the "/" folder, as Swift's does, asks nothing.
   A failure of that listing fails the page, as a failure of its own listing does, rather than hiding the folder.
2. **The row is the same folder row a store that lists it would give.**
   Opening it lists the prefix "/", which both stores answer, and every action on a folder (download, copy, the prefix deletion) takes the prefix "/" exactly as it would on Swift.
   Nothing else is synthesised: inside a folder, and in every listing without the delimiter, the store's own answer is shown.
3. **Only the browse view changes.**
   Jobs, counts, Find and the deletion planners list without the delimiter or with a prefix, and already see these names.

## Consequences

- Objects whose names start with "/" can be seen and acted on at the top of a container on a store that leaves their folder out.
- Opening a container whose first page shows no "/" folder costs one extra small listing on every store, since the store's leaving it out cannot be told apart from there being no such names without asking.
- A page that gains the row holds one row more than its limit.

## Tests

- On a store that leaves the "/" folder out, the top page of a container holding "/" names shows the "/" folder in its sorted place, and opening it lists those names; on one that lists it, no extra request is made.
- A container with no "/" names shows no "/" folder.
- A page whose marker is past "/", or whose last row sorts before "/" with more pages to come, asks nothing.
