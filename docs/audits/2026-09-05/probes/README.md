# Preserved audit reproductions

These are exploratory reproductions against `cc6465ac69ec00756d9976bbf6cf7531b07da66d`, archived as text so they do not change the normal test suite.
They assert the desired safe result and are expected to fail at the audited revision.
They are evidence of specific defects, not a completed fault/barrier harness or production-ready regression suite.
Promote each relevant reproduction into the maintained test layer when implementing its fix, with stronger setup/error checks and the required system-state oracle.

The Go probes use temporary files, synthetic credentials, the repository's mock server, and loopback HTTP endpoints.
The child redirect probe executes the real, checksum-verified rclone 1.74.3 binary against two local test servers.
The UI probes mount production components or exercise production state/helpers with deferred fake API replies.
No live Swift account is required.

## Preparation

Use the audited revision, Go 1.27.1, the locked frontend dependencies, and the verified rclone 1.74.3 binary described in [verification.md](../verification.md).
Run from the repository root; choose a fresh empty output directory and substitute the real binary path.

```sh
python3 docs/audits/2026-09-05/probes/prepare.py \
  --out /tmp/aeronautes-audit-repro \
  --rclone /path/to/verified/rclone

go test -overlay=/tmp/aeronautes-audit-repro/overlay.json -count=1 \
  -run '^TestAudit(Copy|Skip|Sweep|Overwrite|Download|Preflight|Cancelled|Mirror|Upload|PowerLoss|FailedCredential|DatabaseDurability|Retarget|Loopback|PinnedChild|Oracle)' \
  -v ./internal/transfer/copy ./internal/transfer/local ./internal/app \
  ./internal/swiftcontrol ./internal/transfer/rclone ./internal/systemtest/oracle

frontend/node_modules/.bin/vitest run \
  --config /tmp/aeronautes-audit-repro/vitest.config.mts
```

`prepare.py` only materializes probe files/configuration in the chosen temporary directory.
It substitutes checkout and rclone paths; it does not download dependencies, start services, or edit production sources.
Go's overlay imports the archived files alongside the existing package tests without copying them into the repository.
Local socket creation must be permitted by the execution environment.

## Expected results and limits

| Suite | Expected at the audited revision | Evidence |
| --- | --- | --- |
| Data safety | 11 intended assertion failures | [Log](../evidence/data-probes.log); [findings](../data-safety.md) |
| Security/durability | 5 intended assertion failures; 1 passing PRAGMA observation | [Log](../evidence/security-probes.log); [findings](../security-release.md) |
| Snapshot oracle | 1 intended assertion failure, with two diagnostics | [Log](../evidence/oracle-probe.log); [TEST-01](../testing-performance.md#test-01--high-the-independent-oracle-hides-stored-content-encoding-changes) |
| UI | 12 intended assertion failures | [Log](../evidence/ui-probes.log); [findings](../ui-product.md) |

The combined Go command should therefore run 18 audit tests: 17 failing safety assertions and one passing configuration observation.
A missing binary, unavailable socket, timeout, import error, or fixture setup failure is not reproduction evidence.
Read the individual assertion messages rather than treating any nonzero exit as confirmation.
The archived logs normalize local checkout/scratch paths and contain only synthetic fixture values.

The WAL probe models a surviving earlier journal state after losing later commit facts.
It is not a power-cut experiment and is not proof that an ordinary process kill loses a committed SQLite transaction.
The UI probes use jsdom, so they establish application state/DOM behaviour rather than native webview accessibility or pixel layout.
Native platform, real two-account Swift, process-kill, and power-loss tests remain separate acceptance work.
