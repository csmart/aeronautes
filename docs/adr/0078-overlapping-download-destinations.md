# ADR 0078: refuse overlapping download destinations

**Status:** Proposed on 24 September 2026 as the contract for the operator's decision 16 of the same day in the [Linux finish line](../audits/2026-09-05/linux-finish-line.md) ("b": a record adding a preflight refusal for a download whose destination directory overlaps another active download's); reviewed before its implementation, which follows as its own commit.
**Scope:** `TransferService.PreflightDownload` and `TransferService.StartDownload`, the download route v1 keeps. Uploads and copies are deferred routes and are not changed.

## Context

- [ADR 0047](0047-single-active-job.md) made one active transfer job the default. The active-job budget in the jobs drawer (1, 2, 4 or 8) lets the operator run more, and nothing distinguishes accounts: two downloads from different accounts run at once whenever the budget allows. The operator asked on 24 September whether downloads from different accounts should be blocked, the author answered that nothing requires it, and the operator chose this record over leaving the question open.
- A download's destination is the directory the operator picked, held open for the job as a `DestinationRoot` (`internal/transfer/local/download_root.go`) from the start to the end of the job. The job stages engine-routed files under `.aeronautes-stage-<job id>` inside it ([ADR 0074](0074-workload-bounds.md) decision 3, one stage per job) and places every file without replacing an existing one ([ADR 0070](0070-engine-download-content-binding.md)).
- Two downloads into the same directory, or into directories one inside the other, cannot corrupt each other: each has its own stage and neither replaces a file. What they do is confuse the outcome. The later job fails placement on every name the earlier one placed, each refused as `local-exists` ("already exists under the destination; v1 download never overwrites"); a job whose destination contains another job's destination finds that job's stage directory and placed files inside its own tree, so its collision check refuses names or its cleanup counts foreign entries as issues. The operator then reads two partial results that were one intent, and the records say so only item by item.
- The bridge holds no registry of active destinations today. The job row records the destination as "local directory (picker-selected)", because [ADR 0056](0056-download-preflight-view.md) keeps paths off the bridge, and the held root lives only in the job's closure.

## Decision

1. The transfer service keeps a registry of active download destinations: for every download job, from its start until its pins are released, the held root and its cleaned absolute path, keyed by job id. A queued job counts, since its destination is held from the start.
2. `PreflightDownload` reports, and `StartDownload` refuses, a download whose picked directory overlaps an active download's destination: the same directory (`os.SameFile` over the two held roots, so two spellings of one directory are one), a directory inside it, or a directory containing it (cleaned-path ancestry). The preflight problem has `Kind: "destination-overlap"` and `Reject: true`, and its detail names the other job's id and its directory; the start refuses with an error naming the same job.
3. The start's check and the registry insert happen under one lock, after the selection is consumed and before the job row is created, so two concurrent starts into overlapping directories cannot both win, and a refused start leaves no job row, no credential pin and no recovery record.
4. A job's destination leaves the registry when its pins are released, so a cancelled or finished job frees its directory for the next start. The refusal names the job, so the operator can cancel it or pick elsewhere.
5. Nothing else changes: the active-job budget still decides how many downloads run, and the download ceiling, the batching and every recorder contract stay as they are.

## Required proof

Tier 2 under DESIGN §65.12: a refusal at the boundary with no effect. On the mock and the owned Swift, with one download held running by the engine barrier and another queued behind it: a preflight and a start into the running job's directory, into a subdirectory of it, into a parent of it, and into the queued job's directory are each refused with the problem kind and the job id named, leaving no job row, pin or recovery record; a start into a sibling directory is admitted; after the held job is cancelled the same request is admitted; a symlink to the running job's directory refuses as the same directory. Mutants that drop the ancestry check, move the check after the job row is created, and never release the registry must each be caught.

## Limits

The registry is in-process: a second application instance is refused by the instance lock, not by this record. A directory renamed or replaced after the pick is caught by the held root's own displacement checks, not by this record. Uploads and copies keep their own destination contracts. This record does not serialise downloads and does not change what queues behind the active-job budget.
