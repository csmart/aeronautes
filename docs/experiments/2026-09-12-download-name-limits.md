# Name length limits, leading spaces and compatibility look-alikes

## Scope and oracle

This experiment settles three of the DS-06 remainder items in one bounded slice: component length limits, leading spaces and compatibility equivalence.
It follows the delivered name policy of [PR78](2026-09-09-download-path-case.md), [PR81](2026-09-10-portable-download-names.md) and [PR91](2026-09-10-download-name-normalization.md) and changes none of it; the encoding-collision item was settled separately by the [engine-reserved rune routing](2026-09-11-download-name-encoding.md).

The oracle is K-XFER-011, DESIGN §27.5, the DS-06 acceptance in the audit register and the one-namespace-policy stance of §65.9 item 13: a selection that cannot be materialized on every supported target is refused as a whole before any write, at preflight and again at the executor's item admission, with the same lexical rule at both boundaries.
The operator settled the three product choices on 12 September 2026: refuse path components longer than 255 bytes with a named reason, admit leading spaces and record why, and admit compatibility look-alikes and record why.

The first part of this record establishes the current behaviour and proposes the bounded rule; the second part, from the section "Correction delivered" onward, records the correction and its evidence.

## Source finding

`portableNameRefusal` in `internal/transfer/local/local.go` refuses reserved device bases, the seven characters Windows forbids and components ending in an ASCII space or dot.
`PreflightDownload` applies it to every listed name before the marker branch, and `validDownloadItem` in `download_recording.go` applies it again at the executor's item admission, so the two boundaries carry exactly one lexical policy.
That policy has no length rule, no leading-space rule and no compatibility rule.

The only length refusal today is incidental: `inspectDownloadPath` in `download_destination.go` inspects each planned component beneath the picked directory through the held handle and refuses any inspection error other than absence as kind `local-inspection`, so an over-long first component surfaces the host's own `file name too long` text.
The inspection stops at the first absent component, so an over-long component beneath an absent parent is never inspected and the selection is admitted; the executor's admission has no length rule either, and the failure then comes from the engine or the host at effect time, after the job has started.
On the engine route one such name fails the whole batch, so a safe name in the same job fails with it.

## Platform facts

These are readings of platform documentation and of the pinned engine's frozen source, not native observations on the other platforms.
ext4, XFS and btrfs limit a name to 255 bytes and a pathname passed to one system call to 4,096 bytes; APFS reports a 255-byte name limit; NTFS limits a name to 255 UTF-16 code units and, unless long paths are enabled, a path to 260 units, which the Go runtime and the pinned engine bypass with the extended-length prefix.
For UTF-8 input, 255 bytes is the strictest common component bound, because a UTF-8 sequence never needs fewer bytes than its UTF-16 encoding needs code units.
Swift refuses an object name longer than 1,024 bytes at creation with HTTP 400, observed on the owned real fixture, so a relative path selected through Swift can never approach the Linux pathname limit unless the picked directory itself is thousands of bytes long; the in-process mock accepts longer names.
Win32 strips trailing spaces and dots from a component and keeps leading spaces; APFS and ext4 keep them byte for byte; the pinned engine's default local encodings on Linux, macOS and Windows (`Base` plus the Windows set) never rewrite a leading space, only a trailing one on Windows.
No supported filesystem folds compatibility equivalents: APFS folds canonical equivalence only, NTFS folds case through its upcase table and never normalizes, and ext4 compares bytes; rsync, scp, rclone, Finder and Explorer all keep a ligature or full-width letter distinct from its plain counterpart.

## Before evidence on the preflight and executor boundaries

The [archived local probe](probes/2026-09-12-download-name-limits-local_test.go.txt) drives `PreflightDownload` against the in-process mock and `validDownloadItem` directly; every expectation states the observed current behaviour.

| Case | Selection | Today |
| --- | --- | --- |
| component_255_bytes | one 255-byte component | admitted |
| component_256_bytes | one 256-byte component | refused as `local-inspection` with the host text `file name too long` |
| component_300_bytes | one 300-byte component | refused as `local-inspection` with the host text |
| component_400_bytes_200_units | 200 two-byte letters, 200 UTF-16 units | refused as `local-inspection` on Linux although NTFS would accept it |
| nested_component_300_bytes | a 300-byte component beneath an absent parent | admitted |
| long_component_beside_safe | a safe name and a 300-byte component | the safe name admitted, the long name refused as `local-inspection` |
| leading_space | ` a.txt` beside `a.txt` | both admitted |
| leading_space_device | ` CON` | admitted, because the device rule sees the leading space |
| compatibility_ligature | `ﬁle.txt` beside `file.txt` | both admitted |
| compatibility_fullwidth_letter | `Ａ.txt` beside `A.txt` | both admitted |
| compatibility_superscript | `x².txt` beside `x2.txt` | both admitted |
| executor_admits_long_component | a 300-byte component and a leading space at item admission | both admitted |

## Before evidence on the service

The [archived service probe](probes/2026-09-12-download-name-limits-service_test.go.txt) drives the real service through the picker, preflight, start, the queued job and the durable records on the in-process mock and on the owned two-account real Swift fixture with the pinned engine on the engine route.

| Case | Mock | Real Swift |
| --- | --- | --- |
| leading_space | admitted, complete, exact bytes at ` a.txt` and `a.txt` | same |
| compatibility_ligature | admitted, complete, exact bytes at both spellings | same |
| compatibility_fullwidth_letter | admitted, complete, exact bytes at both spellings | same |
| component_255_bytes | admitted, complete, exact bytes | same |
| component_300_bytes beside a safe name | refused at preflight as `local-inspection` with the host text, nothing written | same |
| nested_component_300_bytes beside a safe name | admitted, then the engine failed the whole batch with `file name too long`; the job ended partial with two failed items and nothing placed | same |
| path_4096_bytes, seventeen 240-byte components beside a safe name | admitted, then the engine failed the whole batch and left a partial stage tree that cleanup reported as retained | the object cannot be created: Swift answers HTTP 400, name length 4,102 longer than 1,024 |

The [manifest](2026-09-12-download-name-limits-probes.json) records the archived probe hashes, the producer commands, exits and log digests.

## What the evidence establishes

The current length refusal is the host's, not the policy's: it names no limit, it varies by platform, it misses any over-long component beneath an absent parent, and the executor does not repeat it, so a selection that preflight admits can fail after the job has started and take a safe name down with it on the engine route.
Leading spaces and compatibility look-alikes are admitted today and complete with exact bytes on both providers, and no supported target or the pinned engine mishandles them; refusing them would remove selectable names without a materialization hazard behind the refusal.
A whole-path bound exists on Linux for the engine route, but it is unreachable through a default Swift because the object name limit of 1,024 bytes is far below the pathname limit; the mock reproduction shows what would happen if it were reached.

## Contract

1. Component length.
   A path component longer than 255 bytes is refused at preflight and at the executor's item admission as one lexical rule with the problem kind `name-length` and a detail that names the component's byte length and the 255-byte limit, before any marker or filesystem effect and across the whole selection.
   The rule is measured in bytes of the UTF-8 name, the strictest common bound, so a component that NTFS could hold in 255 code units but that exceeds 255 bytes is refused everywhere, exactly as the delivered portable rules are conservative on every host.
   The host's own refusal through the existing-destination inspection stays as the backstop.
2. Whole-path bound.
   The bound is stated, not enforced: the picked directory's pathname, the longest relative path, the stage directory name and the temporary name must together fit the platform's pathname limit for the engine route, which on Linux is 4,096 bytes and on macOS is 1,024 bytes; the native route opens one component at a time through the held handle and has no such bound.
   Because Swift refuses object names over 1,024 bytes, the bound can only be reached with a very long picked directory pathname, and the failure is then the engine's own truthful error after admission, with the stage retained and reported; a numeric rule is a product decision left open here.
3. Leading spaces.
   Admitted; the register records that no supported target and no pinned engine encoding mishandles a leading space and that only the trailing-space rule exists for Windows, with a maintained control on both providers.
4. Compatibility look-alikes.
   Admitted; the register records that every supported filesystem keeps them distinct, that only canonical equivalence is folded by APFS, and that the two delivered relations stay side by side and are not joined with a compatibility relation, with a maintained control on both providers.

## Required proof

The archived probes become maintained regressions: the local table expects `name-length` refusals for the 256-byte, 300-byte, 400-byte and nested components at both boundaries and admission for the 255-byte component, the leading-space names and the look-alike pairs; the service regression expects the same refusals before any write on both providers and exact bytes for every admitted case.
One deliberately broken control removes the length rule and must fail only the length cases at both boundaries.
The full race suite runs with inventory reconciliation, and the change carries gofmt, vet and the cross-platform builds.

## Proposed shared-document text

For the register, the DS-06 row: components longer than 255 bytes are refused at both boundaries with a named reason; leading spaces and compatibility look-alikes are admitted, because no supported target or the pinned engine mishandles them; the whole-path bound is recorded and unreachable through a default Swift; directory-only aliases and native target-filesystem acceptance remain open.

For the design sequence, after the engine-reserved rune sequence: the next bounded name-policy correction refuses over-long path components before any write and records the two admitted forms.
1. Review this sequence and the dedicated experiment, including the observed before matrix on both boundaries and both providers.
2. Add the 255-byte component rule to the shared lexical policy so preflight and executor admission refuse it identically, with the named kind and the measured length in the detail.
3. Keep the host refusal as the backstop and record the whole-path bound without enforcing it.
4. Turn the archived probes into maintained regressions with a deliberately broken control for the length rule, and record the verification.
5. Obtain independent review and deliver only the reviewed current-base source through all five protected checks.

## Correction delivered

The correction is one commit on top of the plan, `refuse path components longer than 255 bytes`.
`componentLengthRefusal` in `internal/transfer/local/local.go` reports the first component of a relative name longer than `maxComponentBytes`, 255, with a detail naming the component's byte length and the limit.
`PreflightDownload` applies it after the portable component rule and before the marker branch, refusing the name with the kind `name-length` across the whole selection, and `validDownloadItem` applies it again at the executor's item admission, so a plan carrying an over-long component is refused before any marker or filesystem effect whether it arrives through preflight or directly.
The host's own refusal through the existing-destination inspection is unchanged and remains the backstop; no whole-path rule is enforced; leading spaces and compatibility look-alikes are admitted exactly as before.

## After evidence

The local probe became the maintained regression `TestDownloadNameLimits`, twelve preflight cases including a marker whose component is over-long, plus `TestDownloadNameLimitsExecutorAdmission`, six item-admission cases and one executor run that refuses a plan carrying a 300-byte component with nothing written and the engine never invoked.
The service probe became `TestDownloadNameLimitsMock` and `TestDownloadNameLimitsRealSwift`, eight cases on each provider.

| Case | Before, both providers | After, both providers |
| --- | --- | --- |
| component_255_bytes | admitted, complete, exact bytes | same |
| component_300_bytes beside a safe name | refused at preflight as `local-inspection` with the host text | refused at preflight as `name-length` naming 300 bytes and the 255-byte limit; the safe name admitted; nothing written |
| nested_component_300_bytes beside a safe name | admitted, then the engine failed the whole batch after start | refused at preflight as `name-length`; nothing written |
| marker_component_300_bytes beside a safe name | not probed | refused at preflight as `name-length`; nothing written |
| leading_space, compatibility_ligature, compatibility_fullwidth_letter | admitted, complete, exact bytes | same |
| path_4096_bytes, seventeen 240-byte components | mock: admitted, failed at effect time with a retained stage; Swift: refused at creation, name longer than 1,024 bytes | same on both, recorded as the unenforced whole-path bound |

## Deliberately broken controls

Two controls were run on private copies with the files restored from the commit afterwards, and each failed only the cases its change protects while compiling cleanly.

| Control | Mutation | Failing cases |
| --- | --- | --- |
| no length rule | the helper never reports a component as too long | the six preflight length cases and the three executor length cases; on the mock the three service length cases; every admitted case still passes |
| no executor length check | the executor's item admission omits the helper | the three executor length cases only; preflight and the service refusals still hold |

## Verification

Focused producers on the committed source, all under the race detector: the preflight and executor regressions pass 18 leaves; the service regression passes 8 leaves on the in-process mock and 8 on the real two-account Swift fixture.
The two deliberately broken controls were run on the committed source before the documents were recorded and are listed above.
The full race suite ran once on the committed source with the pinned engine directory and the real Swift fixture configured, exit 0: 4105 distinct names, 3806 leaves of which 3805 passing, 0 failing and 1 skipped, 4104 passing events including parents, 22 packages passing.
A leaf is a recorded name that is not a slash-separated prefix of another recorded name in the same package.
The change carries gofmt, vet on Linux, Windows and macOS, builds for Windows and macOS on arm64, and no module change.
The [manifest](2026-09-12-download-name-limits-probes.json) records every producer command, exit, count and digest and the broken-control outcomes.

## Remaining limits

Linux amd64 is the only observed platform; the NTFS and APFS limits are readings, and the rule is conservative on NTFS, which could hold some multi-byte components this rule refuses.
The whole-path bound stays recorded and unenforced; a numeric rule would be a product decision.
Directory-only aliases and native target-filesystem acceptance stay open under DS-06 and P19.
DS-06, P19, P20 and the valuable-data hold remain open.
