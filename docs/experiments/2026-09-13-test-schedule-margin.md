# Restoring margin to the two-fixture Swift test schedule

The `internal/systemtest` package runs under `go test -timeout=25m` in the `Swift tests — main` lane.
On 13 September 2026 pull request 115 reported `FAIL github.com/csmart/aeronautes/internal/systemtest 1500.095s` on a range that changes only `docs/DESIGN.md`, one experiment record and one `internal/app` test.
The package exhausted its budget without a preceding assertion failure; nineteen packages passed and no test failed.
The tests named in the timeout header, `TestChildLifetimeRealSwift` and `TestCopyCompletionMock`, had started 18 and 28 seconds earlier, so the budget was not consumed by those two.
That says where the time did not go, not that nothing was stuck.

This record measures the current lane split and proposes rebalancing the existing two-fixture partition, without removing a test, relaxing an assertion or shortening a deadline.

## Before evidence

Package wall clock for `internal/systemtest` on both lanes, every master run since the partition landed:

| master | main | heavy |
| --- | --- | --- |
| `31c6c3d5` | 1115.603 | 332.991 |
| `2e580b7b` | 1159.923 | 186.913 |
| `80367243` | 1019.861 | 386.442 |
| `7af01ad4` | 1078.126 | 308.209 |
| `76c30e90` | 1166.776 | 312.609 |
| `3f663a7a` | 1042.138 | 353.208 |

The main lane has stayed between 1019.9 and 1166.8 seconds against a 1500 second limit, and the worst of those leaves 333 seconds.
A run needs to be 29 per cent slower than that worst case to breach the budget, and pull request 115 was.
Its second attempt then passed at 1197.681 seconds, above every master run, so the same tree produced both a breach and the largest passing figure.

The imbalance is not new and there is no growth trend in this data.
The first master run after the partition already measured 1115.603 against 332.991, and the latest measures 1042.138 against 353.208, with the intervening runs moving in both directions.
The heavy lane's own spread, 186.9 to 386.4, is more than half its mean.
So the observed variation on both lanes is large relative to the main lane's margin, and the partition's split has been carrying this imbalance since it landed rather than drifting into it.

## What varies between two runs of the same tree

Pull request 115's head became master `3f663a7a` unchanged, so its second attempt and that
master run executed an identical tree on the same test set.
They differ by 155.5 seconds of package wall clock, 1197.681 against 1042.138.
Its first attempt on that same tree did not complete within 1500 seconds, which is a lower
bound on what that run would have taken rather than a measured figure.
The 155.5 second difference alone is larger than the whole 1019.9 to 1166.8 range of master
runs, so the margin has to absorb differences between runs of unchanged code and not only
whatever future tests add.

Comparing those two runs test by test, the difference is spread across the suite rather than
sitting in a few tests: 142 of the 196 top-level tests took longer, the ten largest
contributors account for half of that, and across the 62 tests of at least ten seconds the
median ratio is 1.11 and the upper quartile 1.25 against an aggregate of 1.151, with four
running more than five per cent faster.
Two runs do not establish why, and this record does not claim a cause.
What they do show is that no small group carries the difference, so trimming a few tests is
not indicated by this data, and rebalancing is the bounded response chosen rather than the
only possible one.

Between the same two runs the heavy lane moved the other way, 328.528 against 353.208 seconds,
with all three of its tests faster at an aggregate ratio of 0.940.
The lanes are separate jobs on separate runners.
Two paired observations do not establish that their excursions are statistically independent,
but they do show the lanes moving in opposite directions on one occasion, and work placed on
the heavy lane is not subject to whatever the main lane's runner is doing at the time.

## Where the time goes

Attribution comes from the retained CI artifacts of master run 34766363502 at `76c30e90`, `saio-tests-main` and `saio-tests-heavy`, rather than from a workstation run.
A hosted budget question is answered by hosted measurements.

| lane | package wall | top-level tests | summed test body | packing against three slots |
| --- | --- | --- | --- | --- |
| main | 1166.78 s | 196 | 2869.1 s | 82 per cent |
| heavy | 312.61 s | 3 | 845.6 s | 90 per cent |

The heavy fixture is close to idle.
It spends 312 seconds of the same 1500 second budget while the main lane spends 1167, so the main lane holds 79 per cent of the two lanes' combined wall clock and 77 per cent of their summed test elapsed.

The twelve most expensive main-lane tests:

| seconds | test |
| --- | --- |
| 155.00 | `TestUploadRecoveryRealSwift` |
| 153.93 | `TestCopyItemRecordsRealSwift` |
| 104.61 | `TestDownloadDestinationRealSwift` |
| 99.71 | `TestDownloadRootGuardRealSwift` |
| 98.67 | `TestDownloadNameFormRealSwift` |
| 97.65 | `TestCopyCompletionRealSwift` |
| 86.74 | `TestCopyInterruptionRealSwift` |
| 81.64 | `TestDownloadRecordRefusalRealSwift` |
| 79.41 | `TestLocalCompletionRealSwift` |
| 68.53 | `TestCopyUnchangedRecordsRealSwift` |
| 68.09 | `TestDownloadNameEncodingRealSwift` |
| 67.62 | `TestCopyStagedContentRealSwift` |

Seventy-eight of these tests exist as a `Mock` and a `RealSwift` twin asserting the same behaviour against a local mock and against the containerized Swift.
The `RealSwift` halves take 2722.4 seconds against their twins' 803.0, a ratio of 3.4.
That is an observed difference between two elapsed sums, not a measurement of network round trips, and this record does not attribute it to any single cause.
It is reported because it bounds what the expensive tests have in common with their cheaper twins.
Nothing here rules out cheaper execution, and a future attempt at it owes evidence that the coverage is preserved rather than an account of every timing difference between a test and its twin.
It is simply not the direction this contract takes.

## Why the heavy lane can absorb the work

`scripts/saio-partition.py` refuses a heavy lane that earns scenario credits, because combined-report handling for two credit-producing lanes was deliberately left out:

    if plan["lane"] == "heavy" and report.get("totalCovered") != 0:
        raise ValueError("heavy scenario credits need deliberate combined-report handling")

That requirement stays explicit and stays enforced.
Any rebalancing has to respect it or take on the combined-report work, and this one respects it.

Of the 196 main-lane systemtest tests, 94 earn a scenario credit and together cost 183.9 seconds; the other 102 cost 2685.2 seconds.
The credited tests are cheap and the expensive tests are uncredited, so a rebalance can move a large amount of time while leaving every scenario credit on the main lane.
None of the twelve tests listed above earns a credit.

## Proposal

Extend the closed `HEAVY` list in `scripts/saio-partition.py` from three named tests to fifteen, adding the twelve above by name.
Those twelve names are the change; the selection is fixed, not a rule the schedule re-evaluates.
They were chosen once, from the `76c30e90` measurements, as the main-lane systemtest tests that earn no scenario credit and took at least 65 seconds.
That threshold is the rationale for this selection and carries no instruction to retune when timings move.

Nothing else changes: the same tests run, with the same assertions, the same per-operation deadlines, the same three slots, the same fixture ownership and cleanup, the same five required contexts and the same aggregate that aborts unless both lanes succeed.
All twelve already carry the outer `t.Parallel()` call the lane scheduling assumes.

Projected against the `76c30e90` measurements, by applying each lane's own measured packing efficiency to its new body time:

| lane | body before | body after | wall measured | wall projected | projected headroom |
| --- | --- | --- | --- | --- | --- |
| main | 2869.1 s | 1707.5 s | 1166.78 s | about 694 s | 54 per cent |
| heavy | 845.6 s | 2007.2 s | 312.61 s | about 742 s | 51 per cent |

These are projections, not results.
They assume each lane keeps its measured packing efficiency at a different test mix, which is the weakest assumption in this record.

The worse projected lane would hold 757 seconds below the budget, against a main lane that
today holds 333 seconds below it at its worst passing run.
No claim is made here about what slowdown that survives: the one run that did breach reports
only a lower bound, so this data does not bound the excursions the schedule must absorb.

Alternatives considered: a fourth parallel slot, already rejected on the earlier evidence that summed body time increased; and a third fixture, which adds an aggregate dependency for a need the two existing fixtures do not yet demonstrate.

Raising `-timeout=25m` is separately declined.
The Go timeout is what produced the header naming the active tests; the job's own 35 minute limit would have killed the runner without it.
Its value should stay below the job limit.

## Acceptance

This changes only the test schedule and no product behaviour, so it is a tier two change under [ADR 0040](../adr/0040-proportionate-remediation.md): ordinary reliability work, recorded in about a page, with no decision record of its own.
The prior partition was decided the same way.

Implementation verification is the existing partition-helper coverage rather than a separate mutant campaign.
`scripts/saio-partition-test.py` already covers missing names, overlapping or missing outcomes, bad producer exits and heavy scenario credit; extend it only where the longer list needs coverage, and reconcile the complete selection and outcomes for both lanes.
A moved test matching the main lane's skip pattern is correct behaviour, not a fault; the fault to guard against is a skip pattern inconsistent with the heavy list.

Acceptance is the measured package and whole-job margins on the reviewed head's own pull-request run and on the resulting master run, with every current budget and gate preserved.
The projections above are not acceptance evidence.

## Local reconciliation of the delivered list

Both lanes were run locally the way the workflow runs them, listing the inventory, preparing
the plan, running the selection and then reconciling with `saio-partition.py check`.
Both passed with a producer exit of zero, a writer exit of zero, a scenario reader exit of
zero and a partition check exit of zero, and both wrote their receipts.

The heavy lane selected all fifteen tests and reported `totalCovered` of zero, so the longer
list still satisfies the requirement that fixes it, which is the thing most at risk from
adding twelve names.
The main lane selected 955 tests and reported 42 covered cells with 38 gapped actions, which
is exactly the CI baseline at `76c30e90`, so no scenario credit moved or was lost.

Local wall clock was 436.438 seconds for the heavy lane and 580.707 seconds for the main lane,
with another suite sharing the host at load 4 to 11.
Those figures reconcile selections and outcomes and are not comparable to hosted timings on
different hardware; the acceptance timings remain the ones named above.
The heavy lane ran at `0bd042136734a25e` and the main lane at `6dbcc379054ee4ef`; the only
changes after those points are this record's own prose, so neither selection nor outcome
depends on them.

## Evidence

Durable private packet `~/code/.aeronautes-work/20260913-claude/sched/`, digests in `SHA256SUMS` beside the files, that file hashing to `cd5b177f25be06a9e68b2ff862537c2e99737e350c25afd5878b431add46322c`.
It holds the unpacked lane artifacts of runs 34766363502, 34781972618 attempt 2 and 34785741219, the `attribute.py` producer for the per-test attribution, `lane-history.sh` for the six-run two-lane table, `variance.py` for the same-tree comparison, `run-lane.sh` with both local lane directories, and every producer's saved output.
Every figure above comes from one of those producers.
The upstream pipeline artifacts of all three runs, 34766363502, 34781972618 and 34785741219, expire on 2026-12-12, so the packet is the durable copy rather than a pointer to them.

## What this does not establish

It does not reduce total test time, and it does not stop either lane growing.
It grants no scenario, audit, native-platform or valuable-data acceptance, and it changes no finding disposition or release gate.
