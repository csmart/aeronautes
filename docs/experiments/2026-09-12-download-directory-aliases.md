# Directory-only aliases in a download selection

## Scope and oracle

This experiment settles the last DS-06 remainder item, directory-only aliases: planned directory names that differ only by case or by Unicode normalization form while no file collides with a file or with a directory.
It follows the delivered name policy of [PR78](2026-09-09-download-path-case.md), [PR81](2026-09-10-portable-download-names.md), [PR91](2026-09-10-download-name-normalization.md), [PR97](2026-09-11-download-name-encoding.md) and [PR101](2026-09-12-download-name-limits.md) and changes none of their refusals.

The oracle is K-XFER-011, DESIGN §27.5, the DS-06 row of the audit register, the one-namespace-policy stance of §65.9 item 13, and the operator's decision of 12 September 2026 for this item: do what other tools do.
The [path-case record](2026-09-09-download-path-case.md) left "directory-only aliases and a marker-only alias such as `Foo/` alongside `foo/child`" outside its correction and asserted no refusal policy for them.

The first part of this record establishes the current behaviour, what other tools do, and the bounded rule; the second part, from the section "Correction delivered" onward, records the correction and its evidence.

## Source finding

`PreflightDownload` in `internal/transfer/local/local.go` puts every listed name into the two twin groups, `byLower` and `byCaseless` (lines 218 and 219), directory markers included, and refuses any group of two or more as `case-fold` or `name-form` (lines 361 and 364).
The file-versus-prefix rule (lines 307 to 338) treats only files as ancestors, so a marker beside an implicit directory of the other spelling, or two implicit directories that differ only by spelling, is admitted.
`DownloadInto` in `download_execute.go` repeats both relations on the plan items at lines 83 and 84, markers included, and repeats the ancestor rule at line 96, so the executor refuses exactly what preflight refuses.

The result is an inconsistent policy for directories: two explicit markers `Foo/` and `foo/` are refused as twins, while `Foo/a.txt` beside `foo/b.txt`, which plans the same two directories, is admitted.
Files are routed to the pinned engine unless `plan.RcloneUnsafe` marks the name (line 296), and the engine receives the selected names through `copy --no-traverse --files-from-raw` (`internal/transfer/rclone/runner.go` line 340).

## What other tools do

These are readings of the frozen engine source, the local rsync manual and the OpenSSH scp manual, plus two vendor behaviours that are asserted and were not observed here.

The pinned engine, rclone v1.74.3, keys every directory entry it marches by the entry's NFC form unless `--no-unicode-normalization` is set (`fs/march/march.go` lines 70 to 72, `fs/config.go` line 610) and additionally by its lowercase form when the destination reports itself case-insensitive or `--ignore-case-sync` is set (`march.go` lines 80 to 82).
Its local backend reports case-insensitivity on Windows and macOS by default and case-sensitivity on Linux (`backend/local/local.go` lines 511 to 522).
When two entries share a key, the second is dropped with the notice "Duplicate directory found in source - ignoring" (`march.go` line 330) and its contents are never copied, while the copy still exits 0.
Its local backend creates directories with `MkdirAll` (`local.go` lines 779 to 782), so an existing directory of another spelling on a case-insensitive filesystem satisfies the creation and the contents merge.

rsync 3.5.0 has no name-collision policy of its own: its manual's section "Copying between case-preserving and case-insensitive filesystems" warns only that symbolic-link names may overlap other names once case is removed and that a full copy may then be impossible, and it copies directory names as given.
The OpenSSH 10.2 scp manual describes no case or normalization handling at all; scp copies names as given and the target filesystem decides.
Finder and Explorer, when a copied folder's name already exists at the destination, offer to replace or merge the folder; this is asserted from the vendors' user documentation and was not observed here, because no macOS or Windows machine is available.

So the tools that copy names as given let a case-insensitive target merge aliased directories into one, and the pinned engine, which is the only one of them the application invokes, silently drops one aliased directory instead.

## Deterministic boundary

The alias is decided by the two delivered relations, `lowercaseIdentity` and `caselessIdentity` in `name_identity.go` (lines 51 and 53), applied to each planned directory path rather than to each complete name.
A directory identity with more than one planned spelling is an alias, whether the spellings come from explicit markers, from the ancestors of files, or both.

## Before evidence on the preflight and executor boundaries

The [archived local probe](probes/2026-09-12-download-directory-aliases-local_test.go.txt) drives `PreflightDownload` against the in-process mock and then the executor itself with a direct plan built from the same listing and a session-backed test engine, on Linux.
Every row states the observed current behaviour; `Bär` is spelled with the precomposed letter (NFC) or with the combining mark (NFD) as noted.

| Case | Selection | Preflight today | Executor today | Linux result |
| --- | --- | --- | --- | --- |
| marker_twins | `Foo/`, `foo/` | refused, `case-fold` naming `Foo/` | refused, names collide on case or normalization form | nothing written |
| marker_form_twins | `Bär/` NFC, `Bär/` NFD | refused, `name-form` naming the NFD marker, which the listing orders first | refused, same | nothing written |
| marker_twins_with_children | `Foo/`, `foo/`, `Foo/a.txt`, `foo/b.txt` | refused, `case-fold` naming `Foo/` | refused, same | nothing written |
| marker_beside_implicit | `Foo/`, `foo/child.txt` | admitted, two items | admitted | `Foo` and `foo/child.txt` |
| implicit_twins | `Foo/a.txt`, `foo/b.txt` | admitted, two items | admitted | `Foo/a.txt` and `foo/b.txt` |
| implicit_form_twins | `Bär/a.txt` NFC, `Bär/b.txt` NFD | admitted, two items | admitted | two directories, one per form, each with its file |
| deep_implicit_twins | `top/Foo/a.txt`, `TOP/foo/b.txt` | admitted, two items | admitted | `top/Foo/a.txt` and `TOP/foo/b.txt` |
| three_spellings | `Foo/`, `foo/a.txt`, `FOO/b.txt` | admitted, three items | admitted | `Foo`, `foo/a.txt` and `FOO/b.txt` |
| file_twins | `Foo`, `foo` | refused, `case-fold` naming `Foo` | refused | nothing written |
| file_twins_inside_aliases | `Foo/x.txt`, `foo/x.txt` | refused, `case-fold` naming `Foo/x.txt` | refused | nothing written |
| file_vs_marker | `foo`, `Foo/` | refused, `file-vs-prefix` naming `foo` | refused, same path as a file and a directory | nothing written |
| file_vs_implicit | `foo`, `Foo/child.txt` | refused, `file-vs-prefix` naming `foo` | refused, same | nothing written |
| file_vs_marker_form | `Bär` NFC, `Bär/` NFD | refused, `file-vs-prefix` naming the NFC file | refused, same | nothing written |

## Before evidence on the service

The [archived service probe](probes/2026-09-12-download-directory-aliases-service_test.go.txt) drives the real service through the picker, preflight, start, the queued job and the durable records on the in-process mock and on the owned two-account real Swift fixture, with the pinned engine on the engine route, on Linux.

| Case | Mock | Real Swift |
| --- | --- | --- |
| marker_twins | refused at preflight, `case-fold`; start refused; nothing written | same |
| marker_form_twins | refused at preflight, `name-form`; start refused; nothing written | same |
| marker_twins_with_children | refused at preflight, `case-fold`; nothing written | same |
| marker_beside_implicit | admitted, complete, `Foo` and `foo/child.txt` with exact bytes | same |
| implicit_twins | admitted, complete, `Foo/a.txt` and `foo/b.txt` with exact bytes | same |
| implicit_form_twins | admitted, job ended partial: one of two objects copied, one failed as "missing after download (silent drop?)", only the NFD directory and its file on disk | same |
| deep_implicit_twins | admitted, complete, both directory chains with exact bytes | same |
| three_spellings | admitted, complete, `Foo`, `foo/a.txt` and `FOO/b.txt` with exact bytes | same |
| file_twins_inside_aliases | refused at preflight, `case-fold`; nothing written | same |
| file_vs_marker | refused at preflight, `file-vs-prefix`; nothing written | same |

The partial outcome is the executor's own verification catching a file the engine never placed; the job reports it and ends partial, so the drop is visible, but the selection that preflight admitted cannot complete on the engine route.

## Before evidence from the engine itself

The pinned engine was run directly against the real Swift fixture with the application's own remote environment and copy flags (`copy --no-traverse --ignore-times --use-json-log -vv --stats 1s --files-from-raw`, an empty config file) on a selection of four names: `Bär/a.txt` NFC, `Bär/b.txt` NFD, `Foo/a.txt` and `foo/b.txt`.
The engine logged the notice "Duplicate directory found in source - ignoring" for `Bär` from `march/march.go:330`, copied three of the four files, reported "Transferred: 3 / 3, 100%" with "Listed 4", and exited 0.
The destination held `Bär/b.txt` in the NFD spelling only, and both `Foo/a.txt` and `foo/b.txt` in separate directories, because the Linux local backend is case-sensitive.
The log with the token redacted and the selection list are retained with the evidence.

## What the evidence establishes

Two explicit markers that differ only by spelling are refused today while the same two directories implied by files are admitted, so the current policy is not one rule for directories.
The pinned engine drops one of two directories whose names differ only by normalization form on every platform, and by its source would do the same for case on a case-insensitive host, while exiting successfully; the application's verification turns that into a partial job rather than silent loss.
Admitted aliases materialize on Linux as separate directories with exact bytes when the engine is not involved, through the executor's own directory creation and native placement.

## What the evidence does not establish

Nothing here observes macOS or Windows: how APFS or NTFS merges the spellings, and whether the executor's handle-based placement and stage cleanup behave on a merged directory, remain assertions from the code (`materializeDownloadDirectory` ignores an existing directory at `download_execute.go` line 343, and `removeEmptyStageDirectory` treats an absent planned directory as needing no removal at `download_stage.go` lines 85 and 94).
A case-insensitive filesystem cannot be mounted in this container, so native target-filesystem acceptance stays open under P19.

## Contract

1. A directory marker is not a twin candidate: the twin groups at preflight and at the executor's admission contain files only, so two markers that differ only by case or normalization form are admitted, exactly like a marker beside an implicit directory and two implicit directories.
2. The file-versus-prefix rule and every other delivered refusal are unchanged: file twins, file twins beneath aliased directories, and a file whose identity equals a planned directory's identity refuse the whole selection at both boundaries with their existing kinds and wording.
3. A file beneath a directory whose lowercase or caseless identity has more than one planned spelling is routed natively on every platform, so the engine never receives an aliased directory pair; markers are materialized natively as before; every other file keeps its route.
4. The two boundaries admit and refuse identically; the executor keeps trusting the plan's route, as it does for engine-reserved names, and its verification keeps reporting any file the engine did not place.
5. The recorded consequence: on a case-insensitive target the spellings become one directory named by whichever was created first, as rsync, scp and the engine's own local backend leave it; no bytes are lost, because file twins and file-versus-directory pairs stay refused.

## Required proof

- Maintained unit regressions in `internal/transfer/local`: preflight admits the eight alias shapes above with the expected routes (native beneath aliased directories, engine elsewhere), refuses the five controls with unchanged kinds and names, and the executor's admission and a direct execution agree, creating separate directories on Linux with exact bytes.
- A maintained system test on both providers through the recorded service path: the admitted shapes complete with exact bytes and separate directories, `implicit_form_twins` included, the native-routed counter equals the files beneath aliased directories, and the refused controls stay refused before any write.
- Deliberately broken controls built as overlays and shown failing only their own cases: one restores markers to the twin groups, one admits file twins, one removes the native routing beneath aliased directories.
- The full race suite with the real Swift fixture on the final head, reconciled by leaf.

## Proposed shared-document text

For the DESIGN sequence, one paragraph after the deletion request authority paragraph:

> The [directory alias admission](experiments/2026-09-12-download-directory-aliases.md) delivered in PRNN admits directory names that differ only by case or Unicode normalization form, whether spelled as markers or implied by files, because rsync, scp and the pinned engine copy directory names as given and let the target filesystem decide.
> A file whose ancestor has more than one planned spelling is routed natively on every platform, because the pinned engine keys directory entries by their NFC form everywhere and by their lowercase form on case-insensitive hosts and ignores the second spelling as a duplicate, which both providers witnessed as a partial job before the change.
> File twins and file-versus-directory pairs stay refused at both boundaries, so no bytes are lost when a case-insensitive target merges the spellings into one directory.
> Native target-filesystem acceptance remains open under DS-06 and P19.

For the DS-06 disposition in `docs/audits/2026-09-05/data-safety.md`, replacing its closing sentence:

> PRNN then admits directory-only aliases as markers or implied directories, routing the files beneath them natively so the pinned engine's duplicate-directory drop never applies, and records that a case-insensitive target merges the spellings into one directory as rsync, scp and the engine's own local backend leave it.
> Native target-filesystem acceptance remains open, so DS-06/P19 and the valuable-data release hold are still blocking.

For the DS-06 row in `docs/audits/2026-09-05/remediation-plan.md`, replacing the sentence "P19 still needs directory-only aliases and real target filesystems rejecting the complete unsafe plan before any placement.":

> PRNN admits directory-only aliases with native routing beneath them and records the merge consequence. P19 still needs real target filesystems rejecting the complete unsafe plan before any placement.

## Correction delivered

The correction is one commit on top of the plan, `admit directory-only aliases at both boundaries`.
`PreflightDownload` in `internal/transfer/local/local.go` now enters only file names into the two twin groups, so a directory marker is never a twin, and `DownloadInto` in `download_execute.go` skips directory items when it repeats the two relations and keeps the ancestor rule unchanged, so both boundaries admit and refuse identically.
`plannedDirectories` lists every directory a name plans, the marker's own directory included; the preflight records each planned directory's spellings under both relations and routes every file beneath a directory with more than one spelling natively, exactly as engine-reserved names are routed, while markers stay native as before and every other file keeps its route.
The constructed-identity regression's two-directory case, which asserted the old refusal, moves into the alias regression as an admission; no other delivered test changed.

## After evidence

The maintained regressions `TestDownloadDirAliasesPreflight` and `TestDownloadDirAliasesDirect` in `internal/transfer/local` and `TestDownloadDirAliasesMock` and `TestDownloadDirAliasesRealSwift` in `internal/systemtest` replace the archived probes.
The unit regressions drive the preflight against the in-process mock and the executor with a direct plan and a session-backed engine; the service regressions drive the picker, preflight, start, the queued job and the durable records on both providers with the pinned engine.

| Case | Both boundaries | Native-routed files | Service on both providers |
| --- | --- | --- | --- |
| marker_twins | admitted, two directory items | none | complete, `Foo` and `foo` as separate directories |
| marker_form_twins | admitted, two directory items | none | complete, both spellings as separate directories |
| marker_twins_with_children | admitted, four items | `Foo/a.txt`, `foo/b.txt` | complete, four objects, exact bytes |
| marker_beside_implicit | admitted, two items | `foo/child.txt` | complete, `Foo` and `foo/child.txt` |
| implicit_twins | admitted, two items | both files | complete, exact bytes in separate directories |
| implicit_form_twins | admitted, two items | both files | complete on both providers, where it ended partial before the change |
| deep_implicit_twins | admitted, two items | both files | complete, both directory chains |
| three_spellings | admitted, three items | `foo/a.txt`, `FOO/b.txt` | complete, three objects |
| alias_beside_plain | admitted, four items; `bar/c.txt` and `d.txt` keep the engine route | `Foo/a.txt`, `foo/b.txt` | complete, four objects, two native and two through the engine |
| file_twins | refused, `case-fold` naming `Foo` | | unit only |
| file_twins_inside_aliases | refused, `case-fold` naming `Foo/x.txt` | | refused at preflight, start refused, nothing written |
| file_form_twins_inside_aliases | refused, `name-form` naming the NFD spelling the listing orders first | | refused at preflight, nothing written |
| file_vs_marker, file_vs_implicit, file_vs_marker_form | refused, `file-vs-prefix` naming the file | | `file_vs_marker` refused at preflight, nothing written |

Every admitted case ends `complete` with no error, the copied count equal to the selection, the native-routed counter equal to the files beneath aliased directories, no failed item, exact bytes at every file, every spelling as its own directory on this case-sensitive host, no remote write and no collateral change on either account.
The service stream for both providers holds 24 passing leaves and no failure.

## Deliberately broken controls

Each control is a one-line change applied to a detached copy of the implementation commit by a retained producer, compiled and run against the whole `internal/transfer/local` package and the alias service regression on both providers; the failing leaves are listed in the JSON twin.

| Control | Change | Fails | Everything else |
| --- | --- | --- | --- |
| g01 | markers back in the twin groups at both boundaries | only the three marker-twin cases at preflight and in the direct executor test, and the same three cases on both providers | 979 other local leaves and 18 service leaves pass |
| g02 | the file-twin refusal removed at both boundaries | only the file-twin assertions: the three alias cases at both boundaries, the delivered name-form twin cases, the constructed `case_fold` identity, and the two file-twin cases on both providers | 907 other local leaves and 20 service leaves pass |
| g03 | no native routing beneath aliased directories | only the seven routed cases at preflight and the seven admitted cases with files on both providers, where `implicit_form_twins` ends partial again with one object missing after the engine run | 978 other local leaves and 10 service leaves pass |

## Verification

Static: gofmt clean, `go vet` of the local and system-test packages, `GOOS=windows` vet of the local package and a `GOOS=darwin` build of `internal`.
The whole `internal/transfer/local` package passes under the race detector with the new regressions and the amended constructed-identity case.
The alias service regression passes on both providers under the race detector: 24 leaves, none failing.
Full race suite with the real Swift fixture on the implementation commit: two runs on the same head: the first reconciled to 4,178 distinct names and 3,873 leaves with 3,871 passing, one failing and the single expected matrix skip, the failing leaf being `TestCopyItemRecordsMock/failed_401`, the recorded [copy-binding intermittent](2026-09-11-copy-binding-release.md) on the copy path this change does not touch, which then passed three consecutive isolated runs under the race detector; the second reconciled to 4,178 distinct names and 3,873 leaves with 3,872 passing, none failing and the same skip, with the streams, reconciled texts and digests retained in the evidence.

## Remaining limits

Native target-filesystem acceptance stays open under DS-06 and P19: how APFS or NTFS merges the spellings, and whether the executor's handle-based placement and stage cleanup behave on a merged directory, are assertions from the code and not observations, because no macOS or Windows machine is available and no case-insensitive filesystem can be mounted in this container.
The merge on a case-insensitive target is the recorded consequence of the decision: aliased directories become one directory, named by whichever spelling is created first, with every planned file inside it and no bytes lost.
The engine's lowercase keying on case-insensitive hosts is cited from its frozen source and not observed; its NFC keying was observed on Linux.
The executor keeps trusting the plan's route: a direct caller that plans files beneath aliased directories on the engine route gets the engine's drop reported as a file missing after the run, as before, rather than a refusal.
DS-06/P19, the native journeys and the valuable-data release hold remain blocking.

## Protected delivery

[PR104](https://github.com/csmart/aeronautes/pull/104) delivered the three logical plan, correction and evidence commits from base `0b5079abf533060d` through reviewed head `7784692634b2dea0`.
It merged at 09:04:41 UTC on 12 September 2026 as `951c31031fc8445e`.
The reviewed and delivered ranges contain the same three corresponding trees, commit messages and author identities and dates.
All five required jobs passed on the first attempt in both the [PR run](https://github.com/csmart/aeronautes/actions/runs/34683690443) and the [resulting-master run](https://github.com/csmart/aeronautes/actions/runs/34684899485).
Those checks are Go with the race detector, frontend, headless smoke, containerized Swift integration and the aggregate gate.

The earlier failed full-suite run and the copy-binding intermittent described above remain part of the evidence; green CI does not resolve that separate failure.
This documentation integration records the delivered behaviour in DESIGN and the DS-06 register without closing native target-filesystem acceptance, P19 or the valuable-data hold.
The Linux checks do not establish how APFS or NTFS merges directory spellings.
