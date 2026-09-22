# How storage browsers handle very large namespaces — 22 September 2026

On 22 September 2026 the operator directed that very large namespaces are v1 scope and that the approach follow how established object-storage clients handle containers with hundreds of millions of objects, researched before anything is implemented ([finish line](../audits/2026-09-05/linux-finish-line.md) operator decisions, decision 4).
This record is that research.
It proposes nothing; [ADR 0072](../adr/0072-large-namespace-handling.md) takes the proposal from it.

## Method

The survey read the public documentation, source and issue trackers of ten tools on 22 September 2026, looking for four things: how a tool lists a namespace it cannot hold, what "select all" means in it, how it enumerates for a bulk download or delete, and what limits or guidance it publishes.
Sources are listed at the end.
Where a behaviour could not be confirmed from a source it is marked unverified rather than guessed.

## Findings

| Tool | Listing | Select all | Bulk download or delete | Published limits and guidance |
| --- | --- | --- | --- | --- |
| Cyberduck | One prefix level, fetched in server chunks (S3 1,000; Swift 10,000) pushed to the browser as they arrive; at 5,000 files in a folder a prompt asks whether to continue listing, and cancelling keeps the partial listing | The loaded rows; a folder row stands for its prefix | Enumerates first: delete compiles the full recursive list then deletes in partitions of 1,000; transfers run a cancellable prepare phase that lists everything before moving bytes | Maintainer advice for a 400,000-object folder: stop at the prompt, filter or search, or split into folders; a Mountain Duck sync of a whole bucket was reported at 10 GB of RAM |
| OpenStack Horizon | One delimiter level; with the default full-listing setting the server fetches every page and returns at most 1,000 rows, and the panel has no pagination, so a folder shows at most 1,000 entries | The loaded rows | Delete only; a discovery phase re-lists each folder with the same capped call and counts, cancellable, then one delete per object | The settings documentation warns the panel retrieves all containers and objects and can affect Horizon's resource consumption |
| AWS S3 console | Prefix view, paged (page size unverified); versions beyond 300 need the CLI | Checkboxes on loaded rows; a folder checkbox deletes everything under the prefix | One object at a time for download; multi-object download goes to the CLI; emptying a bucket runs as a background task with a status page, and lifecycle rules are recommended for large buckets; Batch Operations take a manifest or an auto-generated prefix list, four billion objects per job by default | Folder size calculation counts current versions only |
| Azure Storage Explorer | Batches with Load more; the batch size is a setting (5,000 observed) unless "Load all" is on | "Select All in Page" and "Select All Cached" mean the loaded rows; "Select All Results" is an open request since 2018 | Folder upload, download and delete are delegated to AzCopy, which enumerates during the operation with its own log | A 1,000-item cached download once hung |
| MinIO Console | Websocket listing of one prefix level streamed in batches of 1,000 until the end, cancellable; every record is held in the browser with no cap found | The loaded rows | Delete streams list → channel → remove on the server; download of a prefix lists recursively while streaming a zip, with no progress while zipping | None documented; the object browser repository is no longer public |
| S3 Browser | Optional bucket pagination with a page size and next and previous pages, offered for hundreds of thousands of files | Rows | Multi-object delete in requests of up to 1,000 | Pagination recommended for very large buckets |
| rclone | Lists one directory at a time by default; recursive listing holds the whole listing in memory; since 1.70 `--list-cutoff` sorts on disk above one million entries | Filters; a file list still traverses unless `--no-traverse` is set | Delete and purge per directory with configurable checkers; sync and copy per directory | About 1 GB of RAM per million entries, measured at 279 bytes per object; a 100 million object directory ran out of memory before `--list-cutoff` |
| MSP360 Explorer | Unverified; a forum report of incomplete listing above 1,000 subfolders | Unverified | Task queue with pause and resume | None found |
| Transmit 5 | No paging documented (unverified) | Unverified | Multi-object delete; a hang when cancelling large transfers was fixed | None |
| WinSCP S3 | Whole listing loaded before display; loading by parts is an open request; more than a million objects is called difficult to work with | Rows | Unverified | None |
| gsutil and gcloud storage | Pages streamed; recursive delete parallel by default | Prefix or wildcard | Recursive delete; the console bulk-deletes several million objects in the background | For a hundred thousand or more deletes the CLI is discouraged; millions or billions go to storage batch operations fed by a manifest or prefix list |

## What the tools have in common

Every tool lists one delimiter level and none enumerates a namespace in order to browse it.
Two patterns appear: lazy batches with a Load more control and a batch size (Storage Explorer, S3 Browser, the cloud consoles), or streaming the level with progressive rendering and a ceiling (Cyberduck's prompt at 5,000, MinIO's batches of 1,000, Horizon's silent cut at 1,000).
The one tool that blocks until the whole level has loaded (WinSCP) carries an open request to stop doing so.

Select all means the loaded rows everywhere; Storage Explorer names it "Select All Cached".
Whole-prefix intent is expressed by choosing the folder, and the operation, not the selection, enumerates the prefix.
No tool puts an object-count ceiling on selection itself.

Bulk operations divide into enumerate-then-act with a cancellable prepare phase and running counts (Cyberduck, Horizon, rclone's in-memory listing with disk spill above a million entries), and streamed enumeration during the operation (MinIO's list-to-remove channel and zip-while-listing, AzCopy, the cloud batch services fed by inventories or prefix lists).
All of them can be cancelled; counts are known before the run in the first family and absent during it in the second.

Published guidance converges on about 0.3 to 1 KB of memory per listed object, on redirecting operations above roughly a hundred thousand objects to background or batch services, and on lifecycle rules rather than a client for emptying very large buckets.

## Limits

This is a reading of documentation, source and issue trackers on one day; none of the tools was run.
Page sizes and internals marked unverified were not confirmed.
Nothing here changes code, a gate or the valuable-data hold.

## Sources

- Cyberduck: `defaults/src/main/resources/default.properties`, `S3ObjectListService.java`, `SwiftObjectListService.java`, `LimitedListProgressListener.java`, `ListWorker.java`, `DeleteWorker.java`, `AbstractTransferWorker.java` in <https://github.com/iterate-ch/cyberduck>; issues 8274 and 10087; discussion 15870.
- Horizon: <https://docs.openstack.org/horizon/latest/configuration/settings.html>; `openstack_dashboard/api/swift.py`, `api/rest/swift.py`, the containers model, delete-objects controller and batch-actions services in <https://github.com/openstack/horizon>; python-swiftclient `client.py`; Launchpad bug 1775002.
- AWS S3 console: the S3 user guide pages on downloading objects, deleting multiple objects, using folders, emptying a bucket, Batch Operations and listing versioned buckets.
- Azure Storage Explorer: <https://github.com/microsoft/AzureStorageExplorer> issues 1527, 9075, 7290, 315, 7911 and 2528; release notes 1.32.0 and 1.45.0; the AzCopy integration announcement and the troubleshooting guide.
- MinIO Console v1.7.6 module source (`api/ws_objects.go`, `api/admin_objects.go`, `api/user_objects.go`, `ListObjectsTable.tsx`, `DeleteMultipleObjects.tsx`, `objectBrowserThunks.ts`); console issue 3269; <https://github.com/minio/minio/discussions/21316>.
- S3 Browser: the bucket pagination, multi-object delete and bucket deletion help pages at <https://s3browser.com>.
- rclone: <https://rclone.org/docs/> (`--fast-list`, `--list-cutoff`, `--no-traverse`), <https://rclone.org/filtering/>, the `ncdu`, `delete` and `purge` command pages, the 1.70.0 and 1.72.0 changelog entries, issue 7974, and the forum threads on fast-list and ncdu memory.
- MSP360: the forum thread on incomplete folder listing and the getting-started guide.
- Transmit 5: <https://help.panic.com/releasenotes/transmit5/>.
- WinSCP: forum topic 32789, tracker issue 2308 and the raw settings page.
- Google Cloud: the deleting-objects and batch-operations documentation, the `gcloud storage rm` reference and gsutil's `rm.py`.
