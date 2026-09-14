# ADR 0047: One active transfer job

## Status

Accepted for v1 by operator decision on 14 September 2026, and deliberately reversible.

## Context

Section 38 describes whole-application budgets, and the shipped budget admits two
transfer jobs at once.
The application-settings document carries `maxActiveJobs`, documented and validated as
1 to 16, persisted as `max_active_jobs`, and applied to the runner at construction and on
every save.
So the shipped ceiling is a default rather than a limit: a stored value raises it, and the
probe recorded in the
[single-active-job record](../experiments/2026-09-14-single-active-job.md) shows a
freshly constructed runner admitting two slots at once and admitting five after a stored
value of sixteen is applied.

Concurrent jobs multiply the states this application can be in while it is moving and
deleting other people's data.
Every interleaving of two jobs is a state some contract has to hold in, and the
remaining safety work is easier to reason about and to prove when only one job runs.
The operator's decision is therefore to run one job at a time for v1, and to revisit it
rather than to settle it permanently.

## Decision

1. A whole-application ceiling of one active transfer job is enforced, not defaulted.
   It is a hard-coded constant, so raising it later is a one-line change.

2. The ceiling is enforced at the runner, because the runner owns admission.
   Enforcing it only where settings are applied would leave any future direct caller of
   the resize path admitting more.

3. The runner holds the ceiling as its own value, seeded from the constant at
   construction, and the resize path clamps to it.
   Nothing in the application can raise a runner's ceiling above the constant.

4. The queue, the resize path, the settings field and its persistence are unchanged.
   A job beyond the ceiling queues exactly as it does today and runs when the active one
   finishes.

5. The settings surface states what is true rather than what is configurable.
   Reading settings reports the effective ceiling, so the document never claims a
   concurrency the application will not provide.
   Writing a value above the ceiling is refused with a message naming the ceiling, rather
   than being clamped silently or stored as a value with no effect.
   The stored value itself is left as written, so a later revert restores the operator's
   earlier intent rather than a value this decision overwrote.

6. The existing resize regression keeps a ceiling it can raise.
   That test is the only thing proving the resize machinery admits against a count rather
   than a swapped channel, and a ceiling it cannot exceed would remove the only detector
   of that defect rather than a duplicate.

## Consequences

Two transfer jobs can no longer run at once, whatever the stored settings say.
A second start queues rather than refusing, so nothing the operator can do is rejected;
it waits.
A stored value above the ceiling stops being reported back as though it applied.
No refusal, verification, placement, cleanup or accounting behaviour changes, and no
finding, gate, scenario credit or release acceptance is affected.

This does not establish that one job is sufficient for any workload, and it makes no
claim about throughput.
It narrows the states the application can occupy while the remaining safety work is done.
