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

## Limits

The faults are injected by the probe's callbacks at the executor's seams, which is a scheduled reproduction, not an observation of the engine misbehaving on its own.
No rclone defect is claimed: the child copied the correct bytes in every case, and the controls prove it.
Nothing here changes code, closes L05, or lifts the valuable-data hold.
