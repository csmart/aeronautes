# ADR 0056: download preflight execution is not the bridge view

**Status:** Accepted for the download preflight bridge view, which is delivered with this decision.
It does not bound Go RSS, browser accumulation, queued work or child batches, and it does not close L10, P24 or the valuable-data hold.

## Context

`TransferService.PreflightDownload` returned `local.DownloadPreflight` across the bridge.
That struct is the complete collision inventory: every planned item and every problem, plus `Objects` and `TotalBytes`.
`StartDownload` called the same method and passed the result to `DownloadInto`, which iterates `preflight.Items` as the transfer set.
The download dialog then counted objects as `items.length` and treated rejection as `problems.some(p => p.reject)`.

The [download preflight bounds record](../experiments/2026-09-16-download-preflight-bounds.md) searched for silent truncation first, because a dropped tail outranks growth.
Listing continuation, Find matches, job-item pages and the measured 20 000-object preflight all keep the names they saw; they grow, or they page with a count beside a sample.
The download preflight is the growth that can become a correctness fault: bounding `Items` on that shared value would drop names from the job and from the displayed count, while a bounded problem list would let Start enable if the hidden tail held the only rejection.

Upload already splits the two uses.
`PreflightUpload` returns a copy safe for the webview; `StartUpload` calls `uploadPreflight` and executes the full report.
Download has no such split.

[ADR 0023](0023-upload-post-commit-errors.md) already bounds displayed samples without rebuilding the total from the sample length.
That is the pattern this slice copies, not a new one.

## Decision

1. The collision preflight still walks the entire selected namespace.
   K-XFER-011 is unchanged: collisions are found before any write, and a name the listing yielded is not skipped.

2. Execution uses a complete internal report.
   `StartDownload` obtains it from a Go-side helper analogous to `uploadPreflight`, never from the bridge return.
   `DownloadInto` keeps iterating that complete `Items` slice.

3. The bridge returns a distinct view type, not a truncated copy of the execution report.
   Changing what `DownloadPreflight.Items` means would leave `StartDownload` and `DownloadInto` reading a sample as the plan.
   The view carries first-class `Objects`, `TotalBytes`, `ProblemCount` and `Rejected` from the complete report, and bounded `ItemSamples` and `ProblemSamples`.
   The sample limit is the existing `maxFailures` of 200.
   Counts are never rebuilt from sample length.

4. `Rejected` is computed from the complete problem list.
   A sample that happens to contain a rejecting row is not a substitute for the field.

5. The dialog reads those first-class fields.
   Object count comes from `Objects`, not from sample length.
   Start is armed from `Rejected`, not from whether a rejecting row is among the samples.

6. A problem detail that currently interpolates every colliding name keeps the true count in the text and bounds the names it quotes to the same sample limit.

## Consequences

The webview no longer receives the whole inventory in one reply, and a later bound cannot silently shorten the job by editing the wrong value.

The Go process still holds the complete inventory for the check and the run, so RSS still scales with the selected namespace rather than with the sample limit.
Browser accumulation, queued work, child `files-from-raw` batches and aggregate endpoint resources are untouched.

Upload preflight is unchanged: its set is the picked files, and this slice does not claim that path.

L10, P24 and the valuable-data hold stay open.
A later slice may refuse a namespace that cannot be held; this one does not.
