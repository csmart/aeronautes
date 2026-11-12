# Real-cloud journeys

During the 0.5.0 test run the operator found many bugs by hand, and manual testing does not scale (12 November 2026).
The existing suites test the Go services and the Svelte components separately, against the mock or SAIO; nothing walks the operator's journeys between the two real accounts.
This suite is layer 1 of 2: the same application services the buttons call, against the two real clouds.
Layer 2 (browser-driven UI journeys with Playwright) follows as its own slice.

## Where and how it runs

- Package `internal/journeys`, build tag `realcloud`, never in the default build, never in CI, never a required check.
- Run locally: `make journeys CLOUDS=/path/to/clouds.yaml [REPEAT=3] [ONLY=copy]`.
- Accounts: the clouds.yaml entries `aeronautes-ceph` and `aeronautes-swift`, read with the oracle's existing selection, validation and redaction rules; the loader moves from `internal/swiftcontrol`'s test files into a small shared internal package both use, unchanged in behaviour.
- Output never names the provider or the account and never prints a credential, a token, a body or server text: steps are labelled `ceph` and `swift`, failures report the error kind (the oracle's `oracleFailure`), and a JSON report of step outcomes is written to a local folder outside the repository.

## Safety

- Every container the run creates is named `aeronautes-journey-<random>`; the run reads and writes only containers it created, and teardown deletes only those, with their segment containers, even after a failure.
- No step lists, edits or deletes anything else on either account; a step that would is a bug in the suite.
- Bookmarks live in a temporary database with a session-only secret store; nothing is written to the operator's keyring or settings.

## Harness

- The services are wired as `main.go` wires them (transfer, delete, mutate, browse, inspector, jobs, settings, remembered settings), over the temporary database.
- Native seams are stood in through the existing `Wire*` functions: the file picker returns a temporary folder; the overwrite and public-read confirmers answer as the step scripts.
- Every result is checked through an independent read: a separate `swiftcontrol.Session` lists, HEADs and downloads what a step claims, and compares sizes and digests with the local fixture; the code under test never marks its own work.

## Journeys (each on both clouds, and across them where it says so)

1. Upload a folder of small files and one file larger than the chunk size (segmented), then re-upload with overwrite acknowledged.
2. Copy a whole container from one cloud to the other; copy a selection of two of N; copy within a container into a subfolder; copy a folder up to the container's top.
3. Download a folder, a selection and a large object; compare every file's digest with the source.
4. Edit object, container and account metadata and read them back; set and clear a read ACL with the acknowledgement.
5. Delete one object, a marker, a manifest with its segments, a selection, a prefix, a container's contents and the container.
6. Switch between the two bookmarks repeatedly; check that the listing and remembered settings follow the account and that no sign-in repeats.
7. Settings: save, restart the services over the same files, read back; export and restore the backup.

Each journey runs `REPEAT` times (default 3) so intermittent store faults, like the 502 on a large copy, show up as a count rather than a one-off.

## What it does not do

It does not drive the window, the native dialogs or the Svelte code; layer 2 does.
It does not measure performance.
