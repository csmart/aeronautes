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

- Every container the run uses is one it created, named `aeronautes-journey-<random>`, with the oracle's rule: a create that finds the name already taken is not a creation, and that name is never written to or deleted.
- Large-object segments go to the exact sidecar `<container>_segments`, which is a second container. Before a step that can create it, the run reads that exact name; if it already exists the step does not start, and the sidecar is then never deleted. A sidecar the run's own step created is deleted with its container.
- Teardown deletes only the exact containers this run created and the exact sidecar of each that the run's steps created, and nothing else, even after a failure; it never lists the account and sweeps a prefix.
- No step lists, edits or deletes anything else on either account; a step that would is a bug in the suite.
- Account metadata is the one account-level write, and an account edit replaces the whole set: a key the edit does not carry is removed, and a quota of zero removes an existing quota. So every account edit the run makes, through the application or in teardown, starts from the account as just read: it carries every existing user-metadata key and the current quota unchanged and adds only the run's own key, `X-Account-Meta-Aeronautes-Journey-<random>`, which no earlier run or person can have set; teardown removes only that key, again carrying the rest and the quota through. TempURL keys are never written. A run that is killed before teardown can leave that one key behind; a later run does not touch it, and it can be removed by hand.
- The run never opens the operator's settings file, database or keyring. It creates one temporary directory before any service starts and gives the services explicit paths inside it for both `settings.json` and the database, with a session-only secret store for the bookmarks; nothing reads `XDG_CONFIG_HOME`, `XDG_DATA_HOME` or `AERONAUTES_STATE_DIR`.
- The harness has its own default-build tests, against the mock: a container name that already exists and a sidecar that already exists receive no destructive request, and teardown sends deletes only for what the run created.

## Harness

- The services are wired as `main.go` wires them (transfer, delete, mutate, browse, inspector, jobs, settings, remembered settings), over the temporary database.
- Native seams are stood in through the existing `Wire*` functions: the file picker returns a temporary folder; the overwrite and public-read confirmers answer as the step scripts.
- Every result is checked through an independent read: a separate `swiftcontrol.Session` lists, HEADs and downloads what a step claims, and compares sizes and digests with the local fixture; the code under test never marks its own work.

## Journeys (each on both clouds, and across them where it says so)

1. Upload a folder of small files and one file larger than the chunk size (segmented), then re-upload with overwrite acknowledged.
   The segmented file is chosen static in odd rounds and dynamic in even rounds, so two rounds or more cover both choices on both clouds (ADR 0111).
   A static choice is published static on the Swift cloud and dynamic on the Ceph RGW cloud, which does not return static manifests (decision 10).
2. Copy a whole container from one cloud to the other; copy a selection of two of N; copy within a container into a subfolder; copy a folder up to the container's top.
3. Download a folder, a selection and a large object; compare every file's digest with the source.
4. Edit object and container metadata and the run's own account metadata key, and read them back; set and clear a read ACL on the run's container with the acknowledgement.
5. Delete one object, a marker, a manifest with its segments, a selection, a prefix, a container's contents and the container.
6. Switch between the two bookmarks repeatedly; check that the listing and remembered settings follow the account and that no sign-in repeats.
7. Settings: save, restart the services over the same temporary files, read back; export and restore the backup, inside the temporary directory.

Each journey runs `REPEAT` times (default 3) so intermittent store faults, like the 502 on a large copy, show up as a count rather than a one-off.

## What it does not do

It does not drive the window, the native dialogs or the Svelte code; layer 2 does.
It does not measure performance.
