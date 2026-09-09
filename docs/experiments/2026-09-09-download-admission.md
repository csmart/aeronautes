# Existing download destination hazards

## Scope and oracle

The before probe exercises actual preflight and Start services on immutable source `cafe784aee6aa8f4dd2dfa2c5dca03b8a7f5631a`, the reviewed download-recording implementation integrated with PR71–74.
All 343 committed Go/SQL input hashes remained unchanged during both accepted producers.
The [archived probe](probes/2026-09-09-download-admission_test.go.txt) is byte-identical to the executed overlay, and the [manifest](2026-09-09-download-admission-probes.json) records its source and producer identities.
These are before-fix failures, not passing CI, new scenario credit or valuable-data clearance.

The oracle is complete preflight refusal of an already evidenced invalid destination before any job, item, transfer audit, selected content GET, child invocation or local effect.
Safe absent subtrees and the accepted initial selected-root symlink remain passing controls.
[ADR 0034](../adr/0034-download-destination-admission.md) proposes the corresponding bounded correction and separately defines the conservative descendant-link policy.
This original probe does not exercise contained descendant aliases, mixed selections, permission failures or concurrent replacement.

## Observations

Mock Swift and two-account SAIO each produce nineteen complete case records: thirteen failing safety leaves and six passing controls.
Each producer exits `1`, with its failing parent and no skipped or unrelated failing cases.
The mock package takes 7.897 seconds; SAIO takes 16.734 seconds.
Every case returns an accepted one-item preflight and admits a nonempty job ID on this before source.
Preflight itself changes neither local snapshots nor transfer-record counts.

| Existing fixture | Native file | Real engine file | Directory marker |
| --- | --- | --- | --- |
| Descendant symlink to the owned outside sibling | Writes the complete 55-byte file outside; reports complete and verified | Stages the complete file, then application placement writes it outside; reports complete and verified | Creates the outside directory; reports complete and verified |
| Dangling final symlink | Admits and reads content, then reports partial/conflict | Admits and reads content, then reports partial/conflict with retained staging | Admits, then reports partial/failed |
| Regular file at a needed parent | Admits and reads content, then reports partial/failed | Admits and reads content, then reports partial/failed with retained staging | Admits, then reports partial/failed |
| Selected root removed after picker capture | Recreates the root and places the file | Admits a failed job before child dispatch | Recreates the root |
| Regular file at a marker name | Not applicable | Not applicable | Admits, then reports partial/failed |
| Safe absent subtree, control | Correct full bytes and expected directories | Correct full bytes and expected directories, no stage residue | Expected directories |
| Initially selected root symlink, control | Correct full bytes in the selected directory | Correct full bytes in the selected directory, no stage residue | Existing selected directory remains unchanged |

The real engine observation runs after its synchronous Copy return and records the complete selected payload in the private stage.
The later snapshot records that payload in the owned outside sibling after parent-side placement.
This identifies the application's placement as the observed escape; it does not claim that the child itself followed the descendant link.
All mutations occur inside disposable owned fixtures, including the deliberately distinct outside sibling.

## Evidence and limits

Each case retains the report, Start result, actual public and stored job, every item identity and observation, item totals, terminal audit and transfer-record counts.
The counts cover jobs, recovery rows, item identities, item observations and transfer audit events.
The probe compares public item pages with the retained records and waits for the job body and account pin release before its final observation.
Both complete owned remote namespaces retain their independently observed content and modelled metadata, with no remote PUT, POST or DELETE.
Complete local snapshots retain full file bytes and hashes, types, modes and symlink targets across the selected, outside and unrelated paths.
They do not model inode identity, timestamps, ACLs or extended attributes.

The first mock attempt failed because its sandbox could not open the local test listener and produced no case records; it remains excluded from application evidence.
The accepted producers use the same probe, overlay, runner, pinned rclone 1.74.3 bytes and Go 1.27.1 toolchain.
The SAIO producer started after the separate integrated full suite had finished successfully.
The archive validator checks producer exits, all cases, immutable source and probe hashes, full local byte/digest consistency and the expected provider-equivalent typed outcomes.
Its successful exit establishes evidence consistency only; it does not turn the failed product assertions into passes or supply independent approval.
Raw streams, full case records, excluded setup attempt and validation results are retained outside the repository.

This is Linux amd64 evidence with an injected backend picker.
It does not certify native dialogs, Windows/macOS filesystem behaviour, child process-group quiescence, crash/reopen behaviour or physical durability.
The current retained-record reads use fresh services over the existing database; they do not simulate a restarted application.
Stable root capabilities, raced ancestors and all wider release gates remain open.

## Disposable replay

Create a separate checkout at the exact immutable source above and add the inert probe through a Go overlay mapped to `internal/systemtest/p18_admission_probe_test.go`.
Set `AERONAUTES_P18_EVIDENCE_DIR` to a new empty directory outside the checkout so every case retains its full observations.
With the project's owned two-account Swift fixture, verified rclone pin and normal integration environment available, run the two focused producers sequentially:

```sh
go test -json -race -count=1 -parallel=1 -timeout=10m \
  -overlay /tmp/aeronautes-destination-overlay.json \
  -run '^TestP18ExistingAdmissionMock$' ./internal/systemtest
go test -json -race -count=1 -parallel=1 -timeout=10m \
  -overlay /tmp/aeronautes-destination-overlay.json \
  -run '^TestP18ExistingAdmissionRealSwift$' ./internal/systemtest
```

Record the real producer exit and both streams; the expected before-source result is `1` for each command.
Do not invert or suppress the safety failures, and do not reuse an earlier evidence directory.
The maintained implementation must replace these failures with whole-plan refusal assertions and retain the positive controls under its separately reviewed contract.
