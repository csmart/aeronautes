# Engine-reserved runes in download and copy names

## Scope and oracle

This experiment settles the encoding-collision item of the DS-06 remainder: names that contain a rune the pinned engine reserves as a substitute or a quote, which the engine may not carry unchanged.
It follows the delivered name policy of [ADR 0034](../adr/0034-download-destination-admission.md) and the case, portable-component and canonical-equivalence rules of the P19 sequence, and it changes none of them.

The oracle is K-XFER-011, DESIGN §14.3 and §27.5, the DS-06 acceptance in the audit register and the newline precedent recorded in docs/rclone.md: a name the engine cannot carry unchanged is routed natively, never handed to the engine, and a selection that the engine cannot serve faithfully is never reported as copied.
Exact bytes, the accepted relations, the portable-component refusals and the existing-destination inspection are preserved facts.

The first part of this record establishes the defect on both routes the engine serves and proposes a reviewable contract; the second part, from the section "Correction delivered" onward, records the correction and its evidence.
It makes no claim about compatibility equivalence, leading spaces or length limits, which stay separate DS-06 items.

## Source finding

`RcloneUnsafe` in `internal/transfer/plan/plan.go` routes a name to the native path only for a leading or trailing slash, an empty segment, a newline or a dot segment.
`PreflightDownload` in `internal/transfer/local/local.go` and the copy planner in `plan.go` both call it, so every other admitted name reaches the engine through the raw selected-set list.

The pinned engine, rclone 1.74.3, decodes every listed remote name through its backend encoding and encodes the names it is asked for in the same way.
Its encoder quotes a literal quote rune by doubling it and prefixes a literal substitute with the quote rune; on the Swift backend the substitutes it treats this way include the control pictures for the C0 range and DEL and the full-width full stop as a whole component, and on the local backend the substitute set depends on the platform default: Slash and Dot everywhere, plus the Windows set of the seven forbidden characters, the backslash, control characters and trailing spaces and dots on Windows.
A raw list entry whose decoded spelling differs from the raw name matches nothing the engine listed, so the engine copies nothing for it and exits successfully; the application then reports the item as a silent drop.
These facts were read from the frozen engine source retained for the P31 sidecar work at `lib/encoder/encoder.go`, `lib/encoder/standard.go`, `lib/encoder/os_darwin.go`, `lib/encoder/os_windows.go`, `lib/encoder/os_other.go` and `backend/swift/swift.go`, whose digests the [manifest](2026-09-11-download-name-encoding-probes.json) records, and confirmed by running the pinned binary.

## Engine characterization

The engine was run directly against the owned real Swift fixture with objects created through the Swift API under their raw names, using the application's own selected-set flags and, for comparison, a plain traversal; the commands, outputs and digests are retained privately as the engine-encoding producer.

| Raw object name | Engine's listed spelling | Selected-set copy with the application's flags | Traversal copy |
| --- | --- | --- | --- |
| `p‛q` (U+201B) | `p‛‛q` | not copied, exit 0 | not found, exit 1 |
| `s␀t` (U+2400) | unchanged | copied, exact bytes | copied, exact bytes |
| `s␁t` (U+2401) | `s‛␁t` | not copied, exit 0 | copied under its raw spelling |
| `s␡t` (U+2421) | `s‛␡t` | not copied, exit 0 | copied under its raw spelling |
| `t␠` (U+2420 trailing) | unchanged | copied, exact bytes | copied, exact bytes |
| `a／b` (U+FF0F) | unchanged | copied, exact bytes | copied, exact bytes |
| `a：b` (U+FF1A) | unchanged | copied, exact bytes | copied, exact bytes |
| `a＼b` (U+FF3C) | unchanged | copied, exact bytes | copied, exact bytes |
| `d/．/e` (U+FF0E component) | `d/‛．/e` | not copied, exit 0 | copied under the changed spelling `d/‛．/e` |
| `f．` (U+FF0E trailing) | unchanged | copied, exact bytes | copied, exact bytes |

On Linux, four spellings are altered by the engine's decoding and are dropped from a selected-set copy with a successful exit: the quote rune anywhere, a control picture for the C0 range, the DEL picture and the full-width full stop as a whole component.
The full-width substitutes of the slash, colon and backslash, the NUL picture and the trailing space picture round-trip unchanged on Linux because both backends treat them the same way; on Windows the local backend quotes a literal full-width colon, question mark, quotation mark, asterisk, angle bracket, pipe or backslash and a literal trailing space or full-stop picture, so the same drop or respelling is expected there for those runes, which is a reading of the pinned source and not an observation.

## Before evidence on the service

The [archived service probe](probes/2026-09-11-download-name-encoding-service_test.go.txt) drives the real service through the picker, preflight, start, the queued job, the durable records and both owned remote namespaces for the download route, and through the copy planner and executor between the two accounts for the copy route, on the in-process mock and on the owned two-account real Swift fixture with the pinned engine.
The [archived plan probe](probes/2026-09-11-download-name-encoding-plan_test.go.txt) records that every rune below is routed to the engine today.
Both providers agree on every case.

| Case | Download route | Copy route |
| --- | --- | --- |
| quote_rune `p‛q` | partial, the item failed as "missing after download (silent drop?)", the control copied | partial, the item failed as "engine reported success but the staged object is missing (silent drop)", the control copied |
| null_picture `s␀t` | complete, exact bytes | complete, exact bytes |
| control_picture `s␁t` | partial, silent drop | partial, silent drop |
| fullwidth_slash `a／b` | complete, exact bytes | complete, exact bytes |
| fullwidth_slash_beside_real `a／b` and `a/b` | complete, both placed with exact bytes | complete, both landed |
| fullwidth_colon `a：b` | complete, exact bytes | complete, exact bytes |
| fullwidth_stop_component `d/．/e` | partial, the engine exited 1 with "error reading source root directory: Operation forbidden" and the safe control failed with it | partial, the same engine failure and the control failed with it |
| space_picture_trailing `t␠` | complete, exact bytes | complete, exact bytes |
| control `plain.txt` | complete | complete |

No case changed the source account, wrote outside the job's own scope or placed anything outside the selected directory.
The copy cases of the archived probe end with a scenario-credit call the probe used wrongly, so those leaves are reported as failed by the harness; every observation and invariant above comes from the recorded outcomes, and the manifest says so.

## What the evidence establishes

A name containing the quote rune, a C0 control picture, the DEL picture or a full-width full stop component is admitted on the engine route today, the engine never selects it, and the job reports a silent drop with a diagnosis that blames the transfer rather than the name.
A full-width full stop component is worse: through the application's gateway the engine fails the whole batch, so every engine item in the job fails because of one name.
The same shapes reproduce on the download route and on the copy route, on the mock and on real Swift, so the defect lives in the engine's name handling and the application's routing, not in either store.
The bytes are never lost at the source and no residue remains, so the harm is a false diagnosis and a lost selection, and with the full-width full stop a lost batch.

## What the evidence does not establish

Windows and macOS local encodings are a reading of the pinned source; the full-width forms of the Windows-forbidden characters and the trailing pictures round-trip on Linux and were not observed on those platforms.
Names that are not valid UTF-8 never reach this policy because the listing decoder refuses them.
Nothing here measures how often such names occur.

## Contract

1. Reserved rune set.
   The application pins, per engine version, the set of runes whose presence makes a name unsafe for the engine: the quote rune U+201B; the control pictures U+2400 through U+241F and U+2421; the full-width full stop U+FF0E; and the full-width substitutes the pinned engine uses on any supported platform, U+FF0F, U+FF3C, U+FF1A, U+FF1F, U+FF02, U+FF0A, U+FF1C, U+FF1E, U+FF5C and the space picture U+2420.
   The set is recorded in docs/rclone.md beside the existing newline and dot-segment facts, with the frozen-source digests that justify it.
2. Routing.
   `RcloneUnsafe` reports a name containing any rune of the set as unsafe, so the download planner, the upload planner and the copy planner route it natively on every platform, exactly as newline-bearing names are routed today.
   One policy applies everywhere; a rune that is harmless on Linux but quoted on Windows is routed natively on Linux too, because the native route carries exact bytes and such names are rare.
   Decision to confirm: native routing rather than refusal, and the union set rather than a per-platform set.
3. Limits kept truthful.
   The copy route's native fallback carries a single object up to the Swift single-object limit and refuses larger objects with a named unsupported outcome; that refusal is truthful and stays.
   The set is pinned to rclone 1.74.3 and must be re-derived when the engine pin changes.
4. Reporting.
   A name routed natively for this reason is recorded with the native route in the plan and the item records, as newline names are today; no new diagnosis is needed because the engine never sees the name.

## Required proof

The archived probes become maintained regressions: the plan-level probe flips to expecting native routing for every rune of the set, and the service probe expects every case to complete with exact bytes on both routes and both providers, with the engine invoked only for the safe control.
A deliberately broken control removes one class of the set and must fail exactly that class's cases.
The full race suite runs with inventory reconciliation, and the change carries vet, gofmt and the cross-platform builds.

## Proposed shared-document text

For docs/rclone.md, beside the newline and dot-segment facts: the pinned engine decodes listed names through its backend encoding and quotes literal substitutes with U+201B, so a raw selected-set entry containing U+201B, a C0 control picture, U+2421 or a full-width full stop component matches nothing and is silently not copied with exit 0, and a full-width full stop component through the gateway fails the whole batch; measured on v1.74.3 against real Swift and the mock; names containing any rune of the pinned reserved set are routed natively.

For the design sequence, after the sequence that delivered the root authority correction: the next bounded name-policy correction routes names containing an engine-reserved rune natively on every platform.
1. Review this sequence and the dedicated experiment, including the engine characterization and the before matrix on both routes and both providers.
2. Pin the reserved rune set to the engine version in docs/rclone.md with the frozen-source digests.
3. Extend the shared engine-safety predicate so the download, upload and copy planners route such names natively, keeping every delivered admission rule.
4. Turn the archived probes into maintained regressions with a deliberately broken control per rune class, and record the verification.
5. Obtain independent review and deliver only the reviewed current-base source through all five protected checks.

For the register, the DS-06 encoding-collision item: names containing an engine-reserved rune are routed natively on both routes on every platform; the Windows and macOS local encodings remain a reading of the pinned source until native evidence exists.

## Correction delivered

The correction is one commit on top of the plan, `route engine-reserved names natively`, bounded to the shared engine-safety predicate and its regressions.
`RcloneUnsafe` in `internal/transfer/plan/plan.go` now reports a name containing any rune of the pinned set as unsafe, through `engineReservedRune`: the quote rune, the control pictures U+2400 to U+241F and U+2421, the trailing space picture U+2420, and the full-width forms of the slash, the backslash, the full stop and the seven characters Windows forbids.
The download planner, the upload planner and the copy planner already route every unsafe name natively, so no other production file changes; the copy route's native fallback and its single-object limit are unchanged.
The union set is the delivered default; the operator's choice between the union set and a per-platform set, and between native routing and refusal, remains owed.

## After evidence

The plan-level probe became the maintained table `TestRcloneUnsafeEngineReservedRunes`, which requires every reserved rune to route natively wherever it sits in the name and keeps look-alike full-width letters, the blank symbol outside the engine's picture range, accented letters, ordinary spaces, ordinary full stops and ordinary curly quotes on the engine route.
The service probe became the maintained regression `TestDownloadNameEncodingMock` and `TestDownloadNameEncodingRealSwift`, thirteen cases per route on each provider: the eleven reserved-rune names of the plan's matrix plus the DEL picture, the full-width backslash and a trailing full-width full stop, and two controls, a full-width letter and a plain name.
Every case now completes on both routes and both providers with the reserved name routed natively, every byte landing under its exact spelling, the item totals recording no failure, the engine boundary observed exactly once for the safe control, the source account unchanged and no remote write outside the job's scope.
The wrongly used scenario-credit call of the archived probe is gone from the maintained regression.

| Case | Before, both routes | After, both routes |
| --- | --- | --- |
| quote_rune `p‛q` | silent drop | native, exact bytes |
| null_picture `s␀t` | exact bytes through the engine | native, exact bytes |
| control_picture `s␁t` | silent drop | native, exact bytes |
| del_picture `s␡t` | not probed through the service; dropped in the engine characterization | native, exact bytes |
| fullwidth_slash `a／b`, beside `a/b` | exact bytes through the engine | native, exact bytes, both placed |
| fullwidth_colon `a：b` | exact bytes through the engine on Linux | native, exact bytes |
| fullwidth_backslash `a＼b` | exact bytes through the engine on Linux | native, exact bytes |
| fullwidth_stop_component `d/．/e` | the whole batch failed | native, exact bytes, the control through the engine |
| fullwidth_stop_trailing `f．` | exact bytes through the engine | native, exact bytes |
| space_picture_trailing `t␠` | exact bytes through the engine | native, exact bytes |
| fullwidth_letter_control `Ａbc`, control `plain.txt` | complete | complete through the engine |

## Deliberately broken controls

Each rune class was dropped from the predicate on its own in a private copy and both maintained regressions rerun on the mock; every control failed only the names and cases of its class and compiled cleanly.

| Control | Mutation | Plan names failing | Service cases failing on the mock |
| --- | --- | --- | --- |
| no quote rune | U+201B removed | 2 | quote_rune on both routes |
| no control pictures | the picture range and U+2421 removed | 4 | null_picture, control_picture and del_picture on both routes |
| no full-width full stop | U+FF0E removed | 3 | fullwidth_stop_component and fullwidth_stop_trailing on both routes |
| no Windows substitutes | the seven forbidden-character forms removed | 7 | fullwidth_colon on both routes |

The Windows-substitute control shows what the union set buys on Linux: those runes round-trip through the engine here, so dropping them fails only the maintained expectation that they are routed natively, not a byte-level outcome; on Windows the same drop would let the engine respell them.

## Verification

Focused producers on the committed source, all under the race detector: the plan regression table passes, and the service regression passes 26 leaves on the in-process mock and 26 leaves on the real two-account Swift fixture, both routes included.
The four deliberately broken controls were run on the mock before the commit and are recorded above.
The full race suite ran once on the committed source with the pinned engine directory and the real Swift fixture configured, exit 0: 3799 distinct names, 3532 leaves of which 3531 passing, 0 failing and 1 skipped, 3798 passing events including parents, 22 packages passing.
A leaf is a recorded name that is not a slash-separated prefix of another recorded name in the same package.
The change carries gofmt, vet on Linux, Windows and macOS, internal builds for Windows and macOS on arm64, and no module change.
The [manifest](2026-09-11-download-name-encoding-probes.json) records every producer command, exit, count and digest and the broken-control outcomes.

## Remaining limits

Compatibility equivalence, leading spaces and length limits stay separate items with their own decisions.
Linux amd64 is the only observed platform for the engine's local backend.
DS-06, P19, P20 and the valuable-data hold remain open.

## Protected delivery

The delivered range is the nine logical commits between master d561eaac17f1839e and the reviewed head ba7b65c47d224d20, cherry-picked patch for patch from three independently approved branches: this slice's three commits (the plan, the native routing of engine-reserved names, and this record's after evidence), together with the two commits of the container mode tab keyboard correction and the four commits of the download root authority correction.
Independent review approved each slice on its own base and then the combined range on its final base, with every patch marked identical by range-diff and no shared document in the range.

Its check run 34652123581 completed at the first attempt with all five required jobs successful: the frontend job in one minute and forty-three seconds, the headless smoke job in two minutes and three seconds, the race-detector Go job in eight minutes and fifty-nine seconds, the containerized Swift integration job in thirty minutes and two seconds, and the aggregate job.
The run retained the two coverage badges, the 473,665-byte scenario coverage artifact, the 6,112,982-byte smoke artifact and a 9,964-byte Swift diagnostics artifact.
The retained badges report combined Go statement coverage of 88.1 per cent and frontend coverage of 91.07 per cent.
On the same head the full race suite with the real Swift fixture reconciled to 3,772 leaves with 3,771 passing, none failing and the single expected matrix skip, and the frontend suite passed 1,150 tests in thirty files with svelte-check reporting no errors and no warnings.

The pull request then left draft state after thirty-one premerge guards passed and was merged by rebase pinned to the reviewed head object at 22:32:55 UTC on 11 September 2026.
Master became bf5bd6b3d491bbb4, whose tree cf2548534276a07a is identical to the reviewed head's tree, and each of the nine commits kept its patch identity, message and author identity.
The resulting master check run 34654485439 concluded success at the first attempt with the same five required jobs.
The pipeline exercised Linux runners only, so native acceptance on the other platforms remains unproven, and the shared design, engine-pin and register text for this slice follows in the integrator's own range.
