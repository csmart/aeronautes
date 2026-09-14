# One active transfer job — 14 September 2026

Follow-up to [ADR 0047](../adr/0047-single-active-job.md).
This record holds the reproduction of the shipped behaviour and the contract for changing
it.
No correction is delivered by this record.

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
