# ADR 0072: bounded handling of very large namespaces

**Status:** Accepted; implemented on 23 September 2026.
The contract was reviewed independently on 22 September, the operator confirmed the proposal on 23 September with the ceiling to be configurable (finish line, operator decision 7), and the implementation followed the same day: `PreflightDownloadBounded` in `internal/transfer/local` refuses a selection past the ceiling and stops listing, the ceiling is `downloadObjectCeiling` in the application settings (default 250,000, range 1,000 to 5,000,000), the count is a count-only Find scan, and the browser carries the count control, the ceiling in the download dialog and the setting in the jobs drawer; the [implementation record](../experiments/2026-09-23-large-namespace-implementation.md) holds the evidence.
This records the v1 approach the operator asked for on 22 September 2026 (decision 4 in the [finish line](../audits/2026-09-05/linux-finish-line.md) operator decisions): follow how established object-storage clients handle containers with hundreds of millions of objects, researched before anything is implemented.

## Context

The [tool survey](../experiments/2026-09-22-large-namespace-tool-survey.md) found the same shape in every surveyed client: one delimiter level is listed lazily and never enumerated in order to browse; select all means the loaded rows and whole-prefix intent is the folder itself; bulk operations either enumerate in a cancellable prepare phase with running counts or stream the enumeration through the operation; the published guidance is about 0.3 to 1 KB of memory per listed object, with operations above roughly a hundred thousand objects redirected to background or batch services.

v1 keeps browse, find and download, and blocks deletes (operator decision 3).
What those do with a large namespace today:

- The browser lists one delimiter level in marker pages of at most 5,000 rows (`maxPageRows`) with a Load more control and never loads a level on its own; that is already the surveyed pattern, and DESIGN §15 forbids the alternative.
- Find scans with four workers, keeps at most 1,000 matches per scan and retains at most 16 scans.
- A download's scope is the current prefix, and `PreflightDownload` lists that scope deep, keeps every name with two case-folded indexes for the portable-namespace policy, and refuses nothing for size: it grows rather than truncates, and [ADR 0056](0056-download-preflight-view.md) bounded only the bridge view of it.
  The [peak-memory record](../experiments/2026-09-22-download-inventory-peak-memory.md) measured the inventory at 110 MiB and 0.9 s for 100,000 objects, 352 MiB and 3.2 s for 250,000, 635 MiB and 10.1 s for 500,000, and about 1.24 GiB for a million by arithmetic, on the mock.
  The complete item list then goes to the pinned engine in one batch, and rclone's own guidance is about 1 GB per million entries in its process.

So browsing and finding are bounded already, and the one unbounded path a container of a hundred million objects can reach in v1 is a download of a wide prefix.

## Decision

### Browse lazily, count on request, bound the download selection, and defer streaming batches

1. Browsing keeps one delimiter level, marker pages and Load more, with no automatic full load and no object-count ceiling; nothing changes here beyond the column saying how many rows are loaded and that more exist.

2. Counting is a separate, explicit action.
   "Count objects under this prefix" streams prefix listings of up to 10,000 names per request, keeps only the running count and the last marker, shows the count as it grows with the time it was taken, and stops when the operator stops it or the scope changes.
   The container's own object count comes from the HEAD the inspector already makes and costs nothing.
   No name is retained by a count.

3. Select all, when multi-selection arrives, means the loaded rows, as in every surveyed tool; whole-prefix intent stays the prefix scope, and the operation enumerates it.
   v1 has single-row selection and scope operations, so this is a rule for later work, recorded now.

4. A download selection has a ceiling, counted during preflight.
   `PreflightDownload` keeps listing the scope in order and refuses the selection as soon as the count passes the ceiling, naming the count reached and the ceiling and pointing at the count action and at narrowing the prefix; it never returns a truncated plan.
   The ceiling bounds the inventory, its two indexes, the bridge view and the engine batch.
   The listing arrives in pages and the refusal fires when the count passes the ceiling, which can be part-way through the page that crossed it, so an oversize selection costs the ceiling plus at most one page of listing before it is refused, and no page is requested after the one that crossed.
   The download dialog states the ceiling before Start, and shows the scope's counted size when a count has been taken.
   The ceiling's value is the operator's decision under L10; the peak-memory record's candidates are 100,000 objects at 110 MiB, 250,000 at 352 MiB and 500,000 at 635 MiB, and its recommendation, kept here, is 250,000 objects per selection.
   On 23 September 2026 the operator asked for the ceiling to be configurable: it becomes a setting the operator can change, shown with its current value wherever the ceiling is stated, with 250,000 as the default the author proposes.

5. Streaming batches beyond the ceiling are the follow-on, not v1.
   The direction recorded for it: the preflight becomes a streaming pass that spills the inventory and its indexes to a temporary on-disk index, the engine receives consecutive batches of at most the ceiling, verification and publication complete per batch, the job shows planned and transferred counts as it runs, and cancellation lands at batch boundaries.
   That is the AzCopy and batch-service shape from the survey, and rclone's own `--list-cutoff` disk spill.
   It changes the collision policy's scope (the case-folded indexes must span batches) and the engine handoff, which is why it waits for its own contract.

### Cost

A count is one listing request per 10,000 objects: a container of a hundred million objects is about ten thousand requests, minutes to tens of minutes against a deployed endpoint, cancellable, with the rate visible.
A refused download costs the ceiling plus at most one page of listing.
Browsing and find cost what they cost today.

## Consequences

An operator can browse and search a container of any size, learn how much sits under a prefix before committing to it, and download any prefix under the ceiling; a wider prefix is refused with the numbers and the way to narrow it, never silently cut.
Downloads of more than the ceiling in one selection wait for the streaming follow-on.

## Maintained proof

- Preflight refusal at the ceiling: a seeded mock scope of ceiling plus one refuses with the count and ceiling named and no plan, and a scope of exactly the ceiling plans; no page is requested after the one that crossed the ceiling, proven from the mock's request count.
- Inventory bound: the peak resident set of a refused oversize preflight stays within the ceiling's measured figure, using the peak-memory probe's method.
- Count action: a seeded scope counts exactly, retains no names (allocation and retained-heap checks), stops mid-way on cancel, and is discarded on a scope change (ADR 0066).
- Dialog wording: the ceiling shown before Start; the refusal text; the counted size shown when known.
- Compiled mutants must be caught: a ceiling off by one; a refusal that returns the first ceiling items instead of refusing; a count that retains names; a count that survives a scope change.

## Open points for the operator

- The ceiling's value; 250,000 objects per selection is the recommendation carried from the peak-memory record.
- Whether the count action is v1 or follows with the streaming batches.
- Whether the streaming follow-on should start before v1 ships; the author's recommendation is after.

## Limits

Browser retention, queued-job resources and aggregate endpoint resources remain the other open L10 items and are not decided here.
Find's caps are unchanged.
No code changes, no gate moves and the valuable-data hold stays on.
