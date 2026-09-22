# Hosted-runner budget margins — 22 September 2026

Two test-harness time budgets failed on a slow hosted runner: one job wait expired before a 205-object download had finished, and one package watchdog aborted the last protocol test after the other thirteen had passed.
This record measures both against the same source on a healthy runner, sizes them from that evidence, and demonstrates that each still catches a stuck fixture or job.
No product code, assertion, scenario requirement or CI budget changes.

## What failed

The first hosted run of the queued-cancellation wake change, PR 152 attempt 1 on source `ebc5626dfb311a741b123d5a953040eea7dbb7d0` (protected base `378a4fde`), failed only in the `Swift tests — main` lane.
Every other job passed, and inside that lane every package passed except two:

| Package | Failure | Budget |
| --- | --- | --- |
| `internal/app` | `TestDownloadPreflightViewDoesNotBecomeThePlan`: "job never reached a terminal state" after 10.84 s | `awaitTerminal` ten-second deadline |
| `internal/secrets/protocoltest` | "private Secret Service fixture exceeded 45 seconds" during the last test, `TestReadBackDoesNotBlockBookmarkAccess` | forty-five second whole-package watchdog |

Neither test exercises the changed queued-cancellation callback; the queued-cancel and scheduler regressions passed in the same lane and in the Go race job.

## The runner, not the source

The passing master lane for the same base, run 35644877445 attempt 1, gives the healthy reference.
Every package on the failing runner took roughly twice as long, whether or not it was near the changed code:

| Package | Master lane (s) | PR 152 attempt 1 (s) |
| --- | --- | --- |
| `internal/app` | 239.4 | 500.9 |
| `internal/jobs` | 114.7 | 245.3 |
| `internal/bookmarks` | 36.6 | 85.4 |
| `internal/integration` | 38.3 | 69.3 |
| `internal/systemtest` | 918.4 | 1374.4 |

The five slowest `internal/app` tests doubled in the same way; for example `TestHeldSessionRefusesNewRequestsAfterBookmarkChange` took 19.4 s on master and 38.9 s on the failing runner.

The two failing budgets had no room for that:

| Measurement | Master lane | PR 152 attempt 1 | Local (24-core host, race) |
| --- | --- | --- | --- |
| `TestDownloadPreflightViewDoesNotBecomeThePlan` | 4.07 s pass | 10.84 s fail | 6.56 s and 7.97 s pass |
| `protocoltest` top-level test time, summed | 40.66 s of 45 s | 44.39 s before the last test | 34.82 s |
| `TestFailedKeychainPublicationPreservesAuthority` | 8.52 s | 12.55 s | 3.88 s |

The protocol-test package deliberately spends thirty seconds in bounded waits: `TestOpenDefaultBoundsCollectionSelection` (two ten-second `openDefaultTimeout` probes) and `TestOpenDefaultDoesNotWaitForExpiredProbeCleanup` (one more).
The other twelve tests took about 10.6 s on the master lane, so the forty-five second watchdog had about 4.3 s unused after the whole package; a half-speed runner used that up before the last test finished.

## The correction

- `internal/secrets/protocoltest/main_linux_test.go`: the whole-package watchdog is `fixtureBudget`, three minutes.
  The watchdog exists to stop a prompt wait with no context from hanging the suite (ADR 0055); three minutes still aborts such a hang long before the lane's twenty-five minute package budget, while leaving the thirty seconds of deliberate waits room to run at a quarter speed.
- `internal/app/transferservice_test.go`: `awaitTerminal` waits `terminalWait`, thirty seconds.
  Its slowest caller needs about four seconds hosted and eight seconds locally; thirty seconds still fails a stuck job well inside the package budget, and no test depends on the deadline expiring.
- ADR 0055, the keychain-writes record and the P10 credential paragraph in DESIGN §65.11 now describe the timer as a whole-package watchdog with its history, instead of naming forty-five seconds as current.

The systemtest package took 1374 s of its 1500 s budget on the same slow runner.
That budget is not changed here: the scheduling record requires a diagnosis before any change to it, and this run finished inside it.

## Evidence

Attempt-1 job records, both raw lane logs, the master-lane log and the timing comparison are retained privately under `.aeronautes-work/20260921-v1-root/` (`pr152-attempt1-analysis.md`, `pr152-attempt1-jobs.json`, `pr152-attempt1-job-*.log`, `master378-main-job-*.log`).
The unchanged run was repeated once as attempt 2 after that analysis, as the delivery discipline requires; the retry is not the correction.

## Controls

Each budget was exercised with the same injected delay against the former and the corrected bound, through private `go test -overlay` copies of the test files, on the local host with the private Secret Service fixture required and the race detector on.

| Control | Former bound | Corrected bound |
| --- | --- | --- |
| Twelve-second sleep at the start of `TestReadBackDoesNotBlockBookmarkAccess` | package fails, "private Secret Service fixture exceeded 45 seconds", 45.017 s | package passes, 47.75 s |
| Twelve-second sleep inside the fake engine's `Copy` before `TestDownloadPreflightViewDoesNotBecomeThePlan` waits | test fails, "job never reached a terminal state", 10.36 s | test passes, 18.18 s |

The two failing cases reproduce the hosted failure messages exactly; the two passing cases show the corrected bounds absorbing a delay larger than the one the slow runner added.
Without the injected delay the package takes 35.8 s locally and the test 6.6 to 8.0 s, so the corrected bounds are not near either healthy figure.
The overlays and raw event streams are retained in the private packet `.aeronautes-work/20260922-ci-budget-margins/`.

## Limits

The measurements are two hosted runs and one local host; they establish that the budgets were within ordinary runner variance, not a runner-speed guarantee.
No finding, gate, scenario credit or release acceptance moves.
