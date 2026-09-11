# Engine-reserved runes in download and copy names

## Scope and oracle

This experiment settles the encoding-collision item of the DS-06 remainder: names that contain a rune the pinned engine reserves as a substitute or a quote, which the engine may not carry unchanged.
It follows the delivered name policy of [ADR 0034](../adr/0034-download-destination-admission.md) and the case, portable-component and canonical-equivalence rules of the P19 sequence, and it changes none of them.

The oracle is K-XFER-011, DESIGN §14.3 and §27.5, the DS-06 acceptance in the audit register and the newline precedent recorded in docs/rclone.md: a name the engine cannot carry unchanged is routed natively, never handed to the engine, and a selection that the engine cannot serve faithfully is never reported as copied.
Exact bytes, the accepted relations, the portable-component refusals and the existing-destination inspection are preserved facts.

This record establishes the defect on both routes the engine serves and proposes a reviewable contract.
It changes no production code and makes no claim about compatibility equivalence, leading spaces or length limits, which stay separate DS-06 items.

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

## Proposed contract

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

## Remaining limits

This record proves the defect and proposes a contract; it delivers no correction.
Compatibility equivalence, leading spaces and length limits stay separate items with their own decisions.
Linux amd64 is the only observed platform for the engine's local backend.
DS-06, P19, P20 and the valuable-data hold remain open.
