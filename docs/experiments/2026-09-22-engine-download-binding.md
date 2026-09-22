# Engine download binding — 22 September 2026

The oracle is the L05 content-binding requirement in the frozen finish line, with [ADR 0045](../adr/0045-route-content-binding.md) recording the engine route as an open gap and [ADR 0060](../adr/0060-native-download-content-binding.md) binding the native route and leaving the engine route unbound.
The download-engine assessment stopped on 21 September at its checkpoint with only its controls run; the operator resumed it on 22 September.
This record reruns the retained probe unchanged on current master and states what it shows.
[ADR 0070](../adr/0070-engine-download-content-binding.md) proposes the correction; this record does not claim it is implemented.

## Source and method

Source: master `a4dcd7891d2337aaea8ee168b9cbbde7df35d643`, clean, in a detached worktree.
The probe is the inert `engine_probe_test.go.txt` retained in the private assessment packet of 21 September, applied as a Go build overlay onto `internal/systemtest`; no repository file changed.
It seeds a plain object, a dynamic large object of two runs, a static large object of two runs and a symlink to a plain target, plus a control object and sentinels in both owned namespaces, then runs the production `Runner.CopyInto` with the pinned rclone into the held stage and calls the unchanged local executor with every recorder callback.
Each fault mode acts after the child has written the stage and before or during placement: `corrupt` rewrites the staged bytes at the same size; `reordered` and `missing_with_duplicate` rearrange a large object's runs at the same total length; `short` truncates; `stage_replaced` replaces the staged entry with a different inode; `source_replaced` replaces the source object after planning; `published_replaced` replaces the published file before the final check.
The probe reports `L05_ENGINE_FALSE_VERIFIED` when the executor reports the item verified after a fault, and `L05_ENGINE_PUBLISHED_MISMATCH` when the published digest differs from the planned one.
It makes no digest comparison for published replacement, where it replaced the published file itself; that mode shows only whether the executor reports the item verified after the replacement.

Runs: `go test -race -count=1 -parallel=3 -json -overlay=... ./internal/systemtest` for the mock case with the Swift environment unset, and for the real case against the owned two-account fixture at `127.0.0.1:18081`, Go 1.27.1, pinned rclone v1.74.3, load 1.26 at start.
Both runs exit 1 by design.
The streams, the overlay and the probe copy are retained privately in `.aeronautes-work/20260922-engine-binding-assessment/` with `SHA256SUMS`.

## Results

| Class | control | short | corrupt | reordered | missing with duplicate | stage replaced | source replaced | published replaced |
| --- | --- | --- | --- | --- | --- | --- | --- | --- |
| plain, mock and real Swift | passes | refused by size | falsely verified | falsely verified | falsely verified | falsely verified | falsely verified | falsely verified |
| dynamic large, mock and real Swift | passes | refused by size | falsely verified | falsely verified | falsely verified | falsely verified | falsely verified | falsely verified |
| symlink, mock and real Swift | passes | refused by size | falsely verified | falsely verified | falsely verified | falsely verified | falsely verified | falsely verified |
| static large, real Swift | passes | refused by size | falsely verified | falsely verified | falsely verified | falsely verified | falsely verified | falsely verified |
| static large, mock | fails at planning | fails at planning | fails at planning | fails at planning | fails at planning | fails at planning | fails at planning | fails at planning |

The five content and source faults, corrupt, reordered, missing with duplicate, stage replaced and source replaced, also report a published digest that differs from the planned one: 15 cases on the mock (plain, dynamic large and symlink) and 20 on real Swift (those three and static large).
Published replacement is reported verified without that digest line for plain, dynamic large and symlink on the mock and for those three and static large on real Swift.
On the mock, every static large object case fails before the fault with "class slo planned size 249, want logical 64": the mock lists the manifest's own bytes where a deployed Swift lists the logical size, so the probe's planning check refuses the fixture.
That is a fixture gap, recorded here and corrected by the maintained proof, not an engine observation.

Mock: 26 leaves fail and 6 pass; real Swift: 24 fail and 8 pass.
The controls confirm the executor, runner and both namespaces behave as before: the control object and the sentinels are unchanged after every case.

## What this establishes

The engine route publishes and reports verified whatever bytes sit in the stage at the planned size.
The size check is the only comparison it makes, so a same-size difference introduced after planning, in the stage, or at publication is invisible to it.
This is the gap ADR 0045 deferred for want of a fixture that alters the staged file after the engine has written it; the retained probe is that fixture, and the observation of the time, that an altered body ended with nothing staged, does not hold for these cases.

## Limits of the assessment

The faults are injected by the probe's callbacks at the executor's seams, which is a scheduled reproduction, not an observation of the engine misbehaving on its own.
No rclone defect is claimed: the child copied the correct bytes in every case, and the controls prove it.
The assessment itself changed no code.

## Implementation — 23 September 2026

[ADR 0070](../adr/0070-engine-download-content-binding.md) was accepted after independent contract review and implemented in `internal/transfer/local`:

- `placeDownloaded` now opens the staged entry through the held stage with the same non-following, non-blocking open the native route pins its publication with, confirms it is the regular file the size check inspected, builds the expectation from the identity the preflight captured (`engineExpectation`: a plain object's captured length and entity tag through `PlainLadder`; a dynamic or static large object's live HEAD matched against the capture, then `SourceLadder` as the native route builds it), digests the staged bytes through the existing `LadderDigest`, and only then links.
- The link is made from the digested descriptor (`linkDescriptorAt`, Linux only, through the process's own descriptor table) and the published name is confirmed to identify that inode; a mismatch leaves the occupant in place.
- A size mismatch, a content mismatch, a substituted source, an expectation that cannot be built or a failed confirmation reports the item unverified with effect unresolved and marks the staged entry retained; `cleanupDownloadStage` leaves retained entries and their directories, counts them as `Refused`, and reports the stage retained without an issue.
- Engine placements keep their descriptor for the final-publication fact the run already records for native placements, and a failed confirmation names the `place` stage; item admission in `internal/app` now requires that fact for every planned file rather than for native files only, so an engine item copied and verified without it cannot complete (ADR 0061).
- The job message says when a stage was retained only by refused entries, and `internal/app` tests pin the three wordings.
- The mock's static large object PUT now stores the logical size, the manifest entity tag, the assembled body and the manifest in the form a deployed Swift serves (`swifttest.staticLargeObject`), which is the fixture gap the assessment recorded.

Tests that build a plan by hand in the local package now capture identities the way the preflight does (`plannedWithIdentities`), so their engine items verify against the mock they stage from.

### Maintained proof

`internal/systemtest/engine_binding_linux_test.go` runs the probe's thirty-two cases on both fixtures with the pinned engine, as maintained tests with the corrected expectations: the controls place, verify and carry a final-publication fact with the stage removed; `short`, `corrupt`, `reordered`, `missing_with_duplicate`, `stage_replaced` and `source_replaced` publish nothing, report unverified and unresolved, and retain the refused bytes in the stage with cleanup reporting one refused entry and no issue; `published_replaced` verifies, then the final-publication fact reports the replaced name failed and unresolved, the occupant untouched and the verified count reduced.

Final runs on the delivered tree, retained in `.aeronautes-work/20260922-engine-binding-implementation/` with `SHA256SUMS`: `final-mock.jsonl` and `final-real.jsonl`, thirty-two passes each (plain, dynamic large, static large and symlink, eight modes), with the race detector, Go 1.27.1, pinned rclone v1.74.3, the owned Swift fixture at `127.0.0.1:18081`.

### Mutants

Three compiled mutants of the production source, applied as Go build overlays (`mutants/m1..m3`, streams beside them), each fail the mock cases they must:

| Mutant | Deleted | Cases failing | What fails |
| --- | --- | --- | --- |
| m1 | the digest comparison | 18 of 32 | corrupt, reordered, missing with duplicate and staged replacement on every class, and source replacement on plain and symlink; the large classes' source replacement is still refused by the HEAD match |
| m2 | the publication confirmation (no descriptor kept) | 8 of 32 | every control lacks its final-publication fact and every published replacement goes unreported |
| m3 | the cleanup exemption | 24 of 32 | every refusal loses its retained bytes and reports no refused entry |

### Limits

The binding covers the engine download route only; copy, native upload and staged upload commit remain the named deferred gap.
Descriptor publication is implemented on Linux, the only platform where the engine route runs; elsewhere it is refused before placement as before.
L05 stays open for the deferred routes, and the valuable-data hold stays on.
