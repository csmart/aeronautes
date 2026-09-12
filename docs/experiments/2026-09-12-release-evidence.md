# Release evidence on Linux: advisory dispositions, an offline rescan and native journeys

## Scope and oracle

This experiment records the release-evidence pieces that can be produced on Linux amd64 with what the repository and its retained private evidence already hold, for the register rows SEC-10, SEC-11, SEC-12 and TEST-04.
The oracle is the audit register: SEC-11 and P31 require an evidence-backed disposition for every retained engine advisory and complete scan scopes; TEST-04 requires, per claimed platform, that the packaged application launches, connects, uses the native picker, completes and cancels a transfer, preserves exact local paths, restarts and exposes usable error behaviour.
Nothing here closes a finding, changes a register row, selects a candidate engine, executes an advisory trigger, contacts a package registry or advisory service, or observes any platform other than Linux amd64.
The [manifest](2026-09-12-release-evidence-manifest.json) records every producer, its inputs, exits, counts and digests.

## Advisory dispositions as a repository record

The private per-advisory disposition proposal prepared on 9 September for the 42 advisory identifiers reported on all six accepted rclone 1.74.3 sidecars had never been submitted, reviewed or referenced by the register.
[The record](2026-09-12-advisory-dispositions.md) and its [structured form](2026-09-12-advisory-dispositions.json) now carry that proposal into the repository unchanged in substance: 29 rows conditional on a prerequisite the inspected fixed route does not enable, 10 rows unknown for want of a complete transitive caller mapping, 2 rows unknown for want of shared-input closure, and 1 row whose 32-bit prerequisite is absent from every retained binary, with all 88 exact source citations, each keeping its retained file digest and line text.
The builder that produced both files from the frozen draft is archived as [an inert copy](probes/2026-09-12-release-evidence-build-record.py.txt); the manifest records the draft's digest, and the record's class counts and citation count equal the draft's own final validation.
Every row keeps its draft class; no row is an accepted disposition, an executed trigger result, an exposure assertion or a closure, and the twelve unknown rows remain the register's first SEC-11 obligation.

## Offline rescan covering the added text dependency

The application source scans recorded by the 10 September assessment ran at `f8811495`, before `golang.org/x/text v0.39.0` became a direct dependency, so no application scan covered that module.
The retained scanner and database were proved to be the ones that assessment recorded before any scan ran: the scanner binary hashes to `ef8bd4da…` and the database archive to `fa24a054…`, both as recorded in the assessment and the inventory, the flat database holds 4,378 entries, and the manifest records a digest over the sorted per-entry digests so the exact snapshot can be recognised again.
The snapshot's four entries naming `golang.org/x/text` fix at 0.3.3, 0.3.7, 0.3.8 and 0.39.0, so the added version sits at the fixed boundary of the newest, GO-2026-5970, which the disposition record keeps as an unknown shared-input row for the engine's own copy of the module.
The three documented scopes were rerun on the current source with the same scanner, the same flat database, the local module cache only, the proxy and checksum database switched off, and the Go 1.27.1 toolchain taken from the module cache: Linux amd64 `./internal/...`, Darwin arm64 `./internal/...` without cgo, and Windows amd64 `./...` without cgo.
Each scan loaded 174 advisory records from the database, listed `golang.org/x/text@v0.39.0` in its bill of materials, and reported zero findings with exit 0; the streams, their input records and their digests are in the manifest.
A zero result at this snapshot is not a statement about advisories published after 9 September, which need an authorised database refresh.

## What remains open

| Finding | Still open after this record | Needs |
| --- | --- | --- |
| SEC-10 | Native About and installed-package identity on every target, the tag-triggered release path, artifacts for the current master, the signing decision | Native runners, root-capable images, an operator tag or dispatch, an owner decision |
| SEC-11 | Independent review and register integration of the disposition record, closure of the twelve unknown rows, the candidate compatibility gate, the engine's modified-source metadata, the npm scope, the native-library scope, update ownership and latency | Review capacity, a selected-build analysis contract, candidate execution authority, registry permission, owner decisions |
| SEC-12 | Clean install, upgrade and rollback on Fedora and Ubuntu with the distribution engine, the distribution envelope and Flatpak decisions | Root-capable images and product decisions |
| TEST-04 | Every journey on Linux arm64, macOS and Windows, native keychain, assistive technology, zoom and forced colours, representative deployed Swift, the exact-commit gate | Native runners, a keyring daemon, an accessibility harness, an endpoint, workflow work |

SEC-10, SEC-11, SEC-12, TEST-04 and the valuable-data release hold remain blocking.
