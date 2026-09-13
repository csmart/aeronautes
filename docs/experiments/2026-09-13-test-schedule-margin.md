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

The main lane has stayed between 1019.9 and 1166.8 seconds against a 1500 second limit, and the worst of those leaves 333 seconds.
A run needs to be 29 per cent slower than that worst case to breach the budget, and pull request 115 was.
Its second attempt then passed at 1197.681 seconds, the highest figure yet recorded, so the same range on the same test set produced both a breach and the largest passing value.

The imbalance is not new and there is no growth trend in this data.
The first master run after the partition already measured 1115.603 against 332.991, close to the latest pair, and the intervening runs move in both directions.
The heavy lane's own spread, 186.9 to 386.4, is more than half its mean.
So the observed variation on both lanes is large relative to the main lane's margin, and the partition's split has been carrying this imbalance since it landed rather than drifting into it.

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

## Evidence

Durable private packet `~/code/.aeronautes-work/20260913-claude/sched/`, digests in `SHA256SUMS` beside the files, that file hashing to `66aaae9b99f28d690cd1cb1d119e0aeb8a800ede489e459214a36847cf2a8710`.
It holds the unpacked lane artifacts of run 34766363502, the `attribute.py` producer and its saved output for the per-test attribution, and `lane-history.sh` with its saved output for the five-run two-lane table.
Every figure above comes from one of those two producers.
The upstream pipeline artifacts of run 34766363502 expire on 2026-12-12, so the packet is the durable copy rather than a pointer to them.

## What this does not establish

It does not reduce total test time, and it does not stop either lane growing.
It grants no scenario, audit, native-platform or valuable-data acceptance, and it changes no finding disposition or release gate.
