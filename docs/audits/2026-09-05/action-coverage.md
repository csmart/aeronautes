# Bound-action inventory and scenario coverage

This inventory comes from the production service contract at `cc6465ac69ec00756d9976bbf6cf7531b07da66d` and the [recorded local run](evidence/local-scenario-coverage.json).
The 47 actions below are the explicit bridge review surface, not an assertion that every internal function or every UI variant was executed.
All required cells were uncredited in this local run because the system scenarios skipped without SAIO.
The last successful master CI run on 2026-08-31 credited 8/102 cells on product-identical code; its [retrieved artifact](evidence/github-master-scenario.json) supplies the separate CI column.
Existing unit tests still ran and passed as recorded in [verification.md](verification.md).
The current per-kind floor of 102 cells is deliberately smaller than the full design baseline; required/N/A expansion and joined UI/native evidence are P34 work.

| Bound action | Kind | Current required scenarios | Credited locally | Credited in 31 August CI |
| --- | --- | --- | --- | --- |
| `AppService.About` | read | happy | 0/1 | 0/1 |
| `AppService.Info` | read | happy | 0/1 | 0/1 |
| `AuditService.Recent` | read | happy | 0/1 | 0/1 |
| `AuditService.Search` | read | happy | 0/1 | 0/1 |
| `BenchmarkService.Latest` | read | happy | 0/1 | 0/1 |
| `BenchmarkService.StartBenchmark` | job | happy, refusal, conflict, cancel, partial, retry | 0/6 | 0/6 |
| `BookmarkService.Delete` | destructive | happy, refusal, conflict | 0/3 | 0/3 |
| `BookmarkService.List` | read | happy | 0/1 | 0/1 |
| `BookmarkService.Save` | mutate | happy, refusal | 0/2 | 0/2 |
| `BrowseService.Account` | read | happy | 0/1 | 0/1 |
| `BrowseService.Capabilities` | read | happy | 0/1 | 0/1 |
| `BrowseService.Connect` | session | happy | 0/1 | 0/1 |
| `BrowseService.Containers` | read | happy | 0/1 | 0/1 |
| `BrowseService.Objects` | read | happy | 0/1 | 0/1 |
| `DeleteService.CheckSegmentOwnership` | read | happy | 0/1 | 0/1 |
| `DeleteService.DeleteManifestWithSegments` | destructive | happy, refusal, conflict, partial, retry, cancel | 0/6 | 1/6 |
| `DeleteService.PreviewDelete` | read | happy | 0/1 | 0/1 |
| `DeleteService.StartDelete` | job | happy, refusal, conflict, cancel, partial, retry | 0/6 | 2/6 |
| `FindService.CancelFind` | session | happy | 0/1 | 0/1 |
| `FindService.FindStatus` | read | happy | 0/1 | 0/1 |
| `FindService.StartFind` | read | happy, cancel | 0/2 | 0/2 |
| `InspectorService.Container` | read | happy | 0/1 | 0/1 |
| `InspectorService.DLOSegments` | read | happy | 0/1 | 0/1 |
| `InspectorService.Object` | read | happy | 0/1 | 0/1 |
| `InspectorService.Prefix` | read | happy | 0/1 | 0/1 |
| `InspectorService.SLOManifest` | read | happy | 0/1 | 0/1 |
| `JobService.Get` | read | happy | 0/1 | 0/1 |
| `JobService.List` | read | happy | 0/1 | 0/1 |
| `MutateService.CreateContainer` | mutate | happy, refusal | 0/2 | 0/2 |
| `MutateService.CreateMarker` | mutate | happy, refusal | 0/2 | 0/2 |
| `MutateService.DeleteManifest` | destructive | happy, refusal, conflict | 0/3 | 0/3 |
| `MutateService.DeleteMarker` | destructive | happy, refusal, conflict | 0/3 | 0/3 |
| `MutateService.UpdateContainer` | mutate | happy, refusal, conflict | 0/3 | 0/3 |
| `MutateService.UpdateObjectMetadata` | mutate | happy, refusal, conflict | 0/3 | 2/3 |
| `SettingsService.Get` | read | happy | 0/1 | 0/1 |
| `SettingsService.Set` | mutate | happy, refusal | 0/2 | 0/2 |
| `TransferService.CancelJob` | session | happy | 0/1 | 0/1 |
| `TransferService.PickDownloadDir` | session | happy | 0/1 | 0/1 |
| `TransferService.PickUploadFiles` | session | happy | 0/1 | 0/1 |
| `TransferService.PlanCopy` | read | happy | 0/1 | 0/1 |
| `TransferService.PreflightDownload` | read | happy | 0/1 | 0/1 |
| `TransferService.PreflightUpload` | read | happy | 0/1 | 0/1 |
| `TransferService.RerunJob` | job | happy, refusal, conflict, cancel, partial, retry | 0/6 | 0/6 |
| `TransferService.StartCopy` | job | happy, refusal, conflict, cancel, partial, retry | 0/6 | 2/6 |
| `TransferService.StartDownload` | job | happy, refusal, conflict, cancel, partial, retry | 0/6 | 0/6 |
| `TransferService.StartMirror` | job | happy, refusal, conflict, cancel, partial, retry | 0/6 | 1/6 |
| `TransferService.StartUpload` | job | happy, refusal, conflict, cancel, partial, retry | 0/6 | 0/6 |

The architecture/feature matrix in the [assessment](README.md), state/touchpoint map in the [UI report](ui-product.md), and finding-to-work mapping in the [plan](remediation-plan.md) join this surface to the product review.
Future contract changes must name their behaviour oracle and required scenarios rather than inheriting a kind default as proof of completeness.
