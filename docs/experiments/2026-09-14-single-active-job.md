# One active transfer job — 14 September 2026

Follow-up to [ADR 0047](../adr/0047-single-active-job.md).
This record retains the original budget observations at `c9c8a38145b0501a`, the implemented ADR 0047 correction and the later queue follow-up below.

## What the shipped budget does

`defaultMaxActiveJobs` is two, the runner is seeded from it, and `setMaxActiveJobs`
replaces the runner's ceiling with whatever it is given, substituting the default only for
a value at or below zero.
The application-settings document validates `maxActiveJobs` between one and sixteen,
persists it, and applies it to the runner both at construction and on every save.

A probe against unmodified source at `c9c8a38145b0501a` records three observations:

| Observation | Result |
| --- | --- |
| A runner constructed the production way | seeded ceiling 2; two slots admitted at once |
| After a stored value of sixteen is applied | ceiling 16; five slots admitted at once |
| A second acquire while one slot is held | returned immediately without waiting for a release |

The probe is `probe-before.log` in the durable packet named below.
It asserts the current behaviour, so it will fail once the contract is implemented; it is
a reproduction and not a maintained regression.

## Contract

The ceiling becomes one and becomes a limit rather than a default.

Enforcement sits at the runner, which owns admission, and the runner keeps the ceiling as
its own value seeded from the constant.
The resize path clamps to that value, so no caller in the application can raise a runner
above the constant.
The queue is untouched: a second start waits and runs when the active job finishes, rather
than being refused.

Reading application settings reports the effective ceiling, so the document does not claim
a concurrency that will not happen.
Writing a value above the ceiling is refused and names the ceiling.
The persisted value is not rewritten by either, so a later revert restores what the
operator last asked for.

`TestJobCeilingSurvivesResize` keeps the ability to raise a runner's ceiling in-package,
because it is the only test proving the resize admits against a running count rather than
through a swapped channel.
Clamping it to the production ceiling would delete that proof rather than a duplicate.

## What the implementation shows

The ceiling is `maxActiveJobsCeiling`, one, in `internal/app/transferservice.go`.
The runner seeds its own `ceiling` from it and `setMaxActiveJobs` clamps to that value, so
a stored sixteen applied through the resize path leaves the capacity at one.
A second start queues and runs when the slot frees, which the regression asserts as an
admission rather than a refusal.
Reading settings reports the effective ceiling while leaving the stored row as written;
writing above the ceiling is refused with the ceiling named.

The probe archived with the plan no longer completes: its first case blocks on a second
admission that never arrives, and the run ends at the package timeout naming that case.
That is the behaviour change, recorded in `probe-after.log`.
The maintained regressions in `internal/app/single_active_job_test.go` are the positive
form of the same three observations.

Five deliberately broken controls, built by one producer, are each caught by their own
named case: the resize clamp removed, the ceiling returned to two, the settings write
refusal removed, the settings read reporting the stored value, and the runner ceiling
seeded above the constant.
The producer compiles the tests rather than relying on a build, because `go build` does
not compile test files, and it records a build failure as a build failure.
A retained self-test inserts a deliberate syntax error and confirms the producer reports
`BUILD_FAILED` and refuses to credit it.

`g05` first named the admits-one case and did not fire.
That was a selection error rather than a gap: seeding the ceiling high leaves the capacity
seeded from the constant, so admission is unchanged until something resizes.
Retargeted at the resize case it fails deterministically.
The miss is recorded because a control that appears not to fire is either a real finding
about the code or a defect in the harness, and the two are indistinguishable until
someone looks.

## The exemption in the resize regression

`TestJobCeilingSurvivesResize` raises a runner's ceiling in-package.
That exemption exists to preserve a detector, not to make a test pass.
It is the only case proving the correction that admission counts running jobs rather than
swapping a channel, and with a clamp against the package constant it could not admit two,
so the proof would go with the setup.
Holding the ceiling on the runner keeps the application unable to raise it while that case
still can.

## What the contract required



Two admissions before and one admission with the second queued after, from the same probe
shape.
A stored value above the ceiling applied at construction and still admitting one.
A settings write above the ceiling refused with the ceiling named, and a settings read
reporting the effective value rather than the stored one.
The existing resize regression still passing on its own raised ceiling.
Independent broken-guard controls, each failing its own named case, built by one producer
that refuses to credit a control whose package did not compile.

## Limits

This narrows concurrency; it establishes nothing about throughput, about whether one job
is sufficient for any workload, or about any refusal, verification, placement, cleanup or
accounting behaviour, none of which change.
No finding, gate, scenario credit or release acceptance moves.

## Evidence

Durable private packet `~/code/.aeronautes-work/20260914-claude/onejob/`, digests in
`SHA256SUMS` beside the files.

## Queued cancellation wake — 22 September 2026

L10/P24 requires cancellation to release ownership, and the existing scheduler and credential-generation regressions require a queued cancellation to finish before the active slot frees.
At protected `378a4fde6ebf7b4bf79d54f20f96f3aa6f7c9614`, the cancellation callback could broadcast after the context check but before `Cond.Wait` registered its waiter.
The new maintained `TestQueuedCancelWake` schedules that exact interval through the real runner, public `CancelJob`, SQLite completion and credential pin.
Before the correction, the job remained queued with its owner and cancellation registration retained, and an actual credential replacement was refused while the active slot stayed occupied.
Already-waiting cancellation and noncancelled queue controls passed.

The callback now holds the existing condition mutex around its broadcast.
The three unchanged race-tested cases pass, and removing only that mutex guard compiles and reproduces the named failure while both controls still pass.
The test wrappers schedule real context observations and mutex acquisition; they do not simulate a successful wake, and no production test hook is added.
Sixty-one focused queue, pin, shutdown, persistence and capacity cases pass, as does the unchanged mock system queued-download control with collateral and reopen assertions.
The first system attempt failed at setup because the accepted rclone binary was absent from PATH; that failure is retained separately from the successful run using the unchanged pin.
A deliberate syntax error is recorded as a build failure, with no behavioural catch credit.

The durable private packet is `~/code/.aeronautes-work/20260922-queued-cancel-wake/`.
Its precommit proof manifest `PROOF-SHA256SUMS` has SHA256 `0d606949555020ca9c16deaddedc4e95013c8f2420da9704bda1df801fde9f26` and binds the exact source snapshots, test bytes, actual process exits, raw streams, load and overlapping checks.
The deterministic runner/service test is separate from the existing end-to-end download compatibility case; no new public-start race hook is claimed.
The single-active-job ceiling, admission semantics and cancellation outcomes are unchanged.
Queue length, browser retention, child batches, aggregate resource bounds and release acceptance remain open.
