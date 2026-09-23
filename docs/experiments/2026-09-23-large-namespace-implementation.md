# Large namespace handling — implementation, 23 September 2026

[ADR 0072](../adr/0072-large-namespace-handling.md) was accepted by the operator on 23 September 2026 with the download ceiling to be configurable (finish line, decision 7).
This record says what landed and what proves it; it decides nothing.

## What landed

- `PreflightDownloadBounded` and `PreflightDownloadIntoBounded` in `internal/transfer/local` take an object ceiling; the existing entry points call them unbounded.
  The listing counts every object row and, once the count passes the ceiling, refuses the selection with a `selection-ceiling` problem naming the ceiling and the count reached, clears the plan, and returns without requesting another page.
- `AppSettingsDTO.DownloadObjectCeiling` (`download_object_ceiling`), default 250,000, refused outside 1,000 to 5,000,000; a document that omits it keeps the default.
  `TransferService` reads it at every preflight through `WireSettings`, so `StartDownload` sees the same refusal as the dialog.
- `FindService.StartFind` takes `countOnly`: the scan lists the same way, keeps no match, and reports `Scanned` page by page; a plain scan still needs a query.
- The browser offers "Count objects" beside "Find in container", showing the running count with its rate and a stop, then the count with the time it was taken; the download dialog states the ceiling before Start; the jobs drawer carries the ceiling beside the active-job budget, refused rather than clamped outside its range.

## Proof

- `TestPreflightDownloadCeiling` (local package, mock with 10,001 objects): ceiling 5 refuses with "more than 5 objects" and "6 counted", no plan, and exactly one listing request; the ceiling equal to the count plans all 10,001 with more requests; zero is unbounded; a negative ceiling is an error.
- `TestSettingsServicePersistsAndApplies`: the default, four out-of-range refusals, a saved value read back, and an omitted value kept at the default.
- `TestPrefixCountMock` and `TestPrefixCountRealSwift`: 37 seeded objects under the prefix and two beside it count to 37 with no matches; a plain scan without a query is still refused.
- Browser tests: the count control's running and finished states, the dialog's ceiling sentence, and the drawer's ceiling save.

## Limits

The streaming batches beyond the ceiling remain their own contract, as the ADR says.
Browser retention, queued-job resources and aggregate endpoint resources remain the other open L10 items.
