# Download preflight bounds — 16 September 2026

Finish-line item L10 asks for reviewed bounds on namespace and browser retention, queued work, child batches and aggregate resources, where overload refuses without truncating correctness.
This record searches for silent truncation first, then scopes one slice.
The contract is [ADR 0056](../adr/0056-download-preflight-view.md).
Nothing in it is implemented yet.

## Truncation search, on current master

Head `e770f8a98c8ad67b`.
Source reading, not a new fixture run.

Native listing in `internal/swiftcontrol/objects.go` continues until a complete empty page.
A short nonempty page advances the marker, which is ADR 0021, not a dropped tail.
`BrowseService` returns one page with `NextMarker` and clamps a single reply at 5 000 rows; the frontend asks for 1 000.

Find already bounds retained matches at 1 000 with `Truncated` set while `Scanned` continues, and it bounds workers and retained scans.
That is the P08 delivery, not a silent loss.

Job-item reads refuse a page larger than 200, return `NextCursor`, and expose totals separately.
The drawer shows one page and the totals together.

The download preflight does not truncate.
`PreflightDownloadInto` appends every yielded name into `names`, `candidates`, collision maps and `report.Items`, then returns that struct.
`TransferService.PreflightDownload` returns it across the bridge.
`StartDownload` calls that same method and `DownloadInto` iterates `preflight.Items` as the transfer set.
Progress total is `len(preflight.Items)`.

The dialog counts objects as `report?.items?.length ?? 0` and rejection as `problems.some((p) => p.reject)`.
It does not render the item list; it does use the length as the count.

No silent truncation of a download set was found.
The product grows, and the shared value is what would make a later bound truncate correctness.

## Inherited measurement, not re-run

A prior probe on this item seeded `l10-bounds-probe` on the SAIO at 127.0.0.1:18080 and called `local.PreflightDownload`.
It was not re-run here, and the 20 000 objects were not re-seeded.

Recorded in `~/code/.aeronautes-work/20260915-claude/l10/STATE.md`, sha256 `f9c83c23d991e5c15096cffb53ff5fd190ada3273e7a41f310e99d5d14fc46a7`:

| objects | items | problems | totalAlloc | JSON | elapsed |
| --- | --- | --- | --- | --- | --- |
| 1 000 | 1 000 | 0 | 4.3 MiB | 0.1 MiB | 75 ms |
| 5 000 | 5 000 | 0 | 22.6 MiB | 0.6 MiB | 219 ms |
| 20 000 | 20 000 | 0 | 86.0 MiB | 2.3 MiB | 723 ms |

Items equalled the seeded count at every point.
That is the fact used here: the preflight does not drop names where it was measured.
Peak RSS was not measured, and arithmetic to a million names is not a measurement.

## Why the download view is the slice

Bounding `Items` on the value `StartDownload` already executes would copy fewer objects than the collision check admitted.
Bounding `Problems` without a first-class rejected flag would let Start enable when the hidden tail held the refusal.
Rebuilding the displayed count from sample length would tell the operator the sample size.

Upload already splits bridge copy from execution.
ADR 0023 already bounds samples while preserving counts.
Download has the first pattern's missing split and the second pattern's missing bound, on the v1 transfer set.

Browser accumulation remains unbounded (`entries` and `containers` append every continued page).
Queued work still waits rather than refusing, per ADR 0047.
A download still hands every engine name to one rclone `files-from-raw` batch.
Those are remaining L10 surface, not this slice.

## Proposed correction

A distinct bridge view carries complete `Objects`, `TotalBytes`, `ProblemCount` and `Rejected`, and samples of items and problems capped at the existing 200.
`StartDownload` uses the complete internal report.
The dialog reads the first-class fields.
A collision detail keeps its true name count and bounds the names it quotes.

It is not yet implemented.

## What this does not claim

It does not bound Go RSS, browser retention, queues, child batches or control-plane latency.
It does not refuse an oversized namespace.
It does not close L10 or P24.
It does not re-measure the 20 000-object fixture or the webview's handling of a large payload.
