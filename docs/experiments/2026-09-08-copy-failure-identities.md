# Copy failure identity reproduction — 8 September 2026

## Scope and oracle

This revalidates DS-08 on the current delivered implementation before the P12/P13 item-record design.
The complete actual-service probe ran on independently approved source `852a3564f23a3ec1254baed415f8a71adffb0a22`, whose exact tree was preserved by [PR57](https://github.com/csmart/aeronautes/pull/57) at master `87df5495aaa1711ad2bcc506104495087580dca8`.
The [executed probe source](probes/2026-09-08-copy-failure-identity_test.go.txt) is archived verbatim and is intentionally outside the normal passing test suite.
Its SHA-256 is `9f3c9d2aa9344bcd6fefce52959879d080bc9d573936e6c3ccc0986f60f51c5a`, matching the complete producer's captured overlay input.
A Go overlay adds only that test file; application, executor, engine, database and shared harness sources remain unchanged.

The oracle is the actual distinct set of selected source identities whose reads the fixture rejects, together with DS-08's exact accounting and complete failure-access requirement.
Execution and later verification of the same failed name must not count two failed objects.
Display samples must not determine an exact failed-name total or replace retained identities.
The already delivered atomic summaries and interruption corrections do not satisfy that requirement by themselves.

## Actual-service fixture

Each case starts the real Copy service on mock Swift or two-account SAIO with Go 1.27.1, race detection and official pinned rclone 1.74.3.
The destination bookmark is active in the browser while the job captures its source authority.
The source contains zero, one, 201 or 401 selected newline names, plus one engine-safe name.
A fixture proxy rejects GET for precisely those selected native names with HTTP 403 and records each rejected identity.
Every selected failing GET is observed exactly once; listing, HEAD, planning, execution, verification and engine behaviour remain intact.
This fault is deterministic and requires no timing assumption or new barrier implementation.
The engine-safe name must still be copied and verified with exactly ten expected bytes.

Both accounts have full snapshots of their owned fixture namespaces before and after the job.
The destination includes another job's staging sentinel and shared DLO/SLO aliases outside the selected container, with independently checked backing bytes.
The comparison permits only the one selected successful live object; every other name, alias, derived container and source byte must remain unchanged.
These snapshots cover containers under the unique harness prefix, including its derived sidecars, and do not observe arbitrary containers outside that prefix.
The source proxy must observe no writes.

The fixture waits for publication of the captured bindings to acknowledge the whole job body's completion.
It compares the public and durable job, all counters, terminal state and timestamps, source-bound terminal audit, destination-bound retained recovery identity and cancellation deregistration.
Then it closes the database and reopens it in a child process, requiring unchanged records and no remote requests.
All eight cases reach those observations and orderly child-reopen checks.

## Results

The same result occurs on both providers:

| Distinct failed names | Reported failed count | Names in existing job/audit summaries | Other results |
| --- | --- | --- | --- |
| 0 | 0 | 0 | Passing complete control; one copied and verified name |
| 1 | 2 | 1 | Partial; one copied and verified name |
| 201 | 400 | 1 | Partial; one copied and verified name |
| 401 | 400 | 1 | Partial; one copied and verified name |

The complete producer has **eight leaf cases: six intended failures, two passing controls and zero skips**.
It exits 1 because the six nonzero-failure cases reject the incorrect final counter.
The two failing parent tests are not additional leaf cases.
There are no unexpected fixture, collateral, audit, recovery or child-reopen diagnostics.
All other counters and terminal states match their independently derived expectations.
Source and overlay hashes match the captured inputs after execution.

The cause remains visible in `internal/transfer/copy/executor.go`, `internal/transfer/copy/verify.go` and `internal/app/transferservice.go`.
Execution and verification each retain at most 200 failure examples, and Copy sets its final failed count to the sum of those two sample lengths.
This both double-counts repeated identities and eventually truncates the count.
Only the first failure from each phase reaches the existing job error, and the terminal audit names the job rather than every failed object.
The probe measures those existing public summaries; source inspection confirms that JobService currently has no complete per-identity outcome query.
No future query contract is invented by this experiment.

## Reproduction

Run only against owned disposable fixtures configured as in [the system-test harness](../../internal/systemtest/harness.go), with the official pinned rclone available to the test process.
From a checkout containing the archived probe, create an isolated checkout of the measured master and an overlay outside both trees:

```sh
git worktree add --detach /tmp/aeronautes-failure-identities 87df5495aaa1711ad2bcc506104495087580dca8
python3 - <<'PY'
import json
from pathlib import Path
root = Path('/tmp/aeronautes-failure-identities')
probe = Path('docs/experiments/probes/2026-09-08-copy-failure-identity_test.go.txt').resolve()
overlay = {'Replace': {str(root / 'internal/systemtest/copy_failure_identity_probe_test.go'): str(probe)}}
Path('/tmp/aeronautes-failure-identities-overlay.json').write_text(json.dumps(overlay))
PY
GOTOOLCHAIN=go1.27.1 go -C /tmp/aeronautes-failure-identities test -json -race -count=1 -timeout=10m \
  -overlay /tmp/aeronautes-failure-identities-overlay.json ./internal/systemtest \
  -run '^TestCopyFailureIdentity(Mock|RealSwift)$' -v
```

Set `AERONAUTES_SAIO_AUTH_URL` to the owned SAIO endpoint and require both accounts with `AERONAUTES_REQUIRE_TWO_ACCOUNTS=1`.
Put the verified engine on PATH and set `AERONAUTES_OFFICIAL_RCLONE_DIR` and `AERONAUTES_MATRIX_RCLONE` to its directory and executable respectively.
Absent real-Swift configuration can skip that provider and is not the complete evidence recorded here.

Raw commands, exits, source/overlay hashes, event streams and all eight parsed observations are retained under `/home/csmart/code/.aeronautes-work/20260908/copy-failure-identity-*`.
The complete producer is `copy-failure-identity-before-v1`; the earlier four-case `copy-failure-identity-smoke-v1` check is not added to its case total.

## Next contract and limits

Review the smallest durable logical item-admission and outcome contract before changing storage or migrating callers, as required by the [remediation order](../audits/2026-09-05/remediation-plan.md#execution-order-for-valuable-data-safety).
It must keep exact identities beyond samples, preserve execution and verification observations without double-counting, expose unavailable recording or reads, and retain unresolved facts.
Specify immutable identity representation, typed diagnostics, bounded queries, completion accounting, restart behaviour and retention protection before implementing the shared storage and separate Copy/local caller migrations.
An item record is not a remote version identity, native approval, exclusive ownership or permission to retry an uncertain effect.

This is an executed reproduction and a pointer to the required design work, not an accepted ledger design or a fix.
It does not reproduce every destructive bulk-response, spill, crash, unknown-acceptance or local-transfer case in DS-08.
Orderly child reopening is not full-app restart, process-kill, fsync or power-loss evidence.
The executed evidence is Linux evidence; the remote newline names do not require newline-containing local files, but no six-platform claim follows.
Mirror starts, changed-destination overwrites and automatic recovery reclamation remain refused.
The P12/P13/P28 and wider valuable-data gates remain open.
