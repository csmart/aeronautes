# Portable component refusal for download names

## Scope and oracle

This is a bounded prerequisite under the existing portable namespace policy, [K-XFER-011](../REQUIREMENTS.md), [DESIGN §27.5 and §65.9 item 13](../DESIGN.md), with the admission boundaries preserved by [ADR 0034](../adr/0034-download-destination-admission.md).
Windows reserves device base names such as `CON` and `COM1`, opens a console for `CONIN$` and `CONOUT$`, and forbids `<`, `>`, `:`, `"`, `|`, `?` and `*` inside a component.
It also directs applications not to end a name with a space or a dot: the underlying filesystem may accept such a name while the Windows shell and user interface do not.
The accepted policy is one portable rejection rule for every target, so a Swift name carrying any of those spellings must refuse the complete selection instead of materializing differently per platform.
The refusal must happen before service job and engine admission and before the direct executor's first item or effect, even though the current Linux filesystem can represent every one of those spellings.

The scoped correction adds one lexical component predicate shared by the preflight and direct item boundaries.
Selected object names, local paths, diagnostic names and recorded identities keep their exact spelling; nothing is trimmed, normalized, escaped, encoded or renamed.
The existing nonempty remote root marker with an empty local suffix, the originally selected root spelling and its symlink policy stay unchanged.
The correction touches `internal/transfer/local/local.go` for the shared predicate and the preflight boundary, and `internal/transfer/local/download_recording.go` for direct item validation; `download_execute.go` gains the rule through its existing call and is not edited.
This is a conservative refusal on every host, not a claim that each refused spelling is a device or an invalid name on every Windows release and API.

## Source finding

The inspected source is `36e731fe149526d5ff760b23b9caf7bd7b1e4370`.
[PreflightDownload](../../internal/transfer/local/local.go) refuses a leading slash, dot segments, backslashes, empty segments and control characters before its directory-marker branch, and has no reserved-component, restricted-character or trailing-form check.
[validDownloadItem](../../internal/transfer/local/download_recording.go) repeats exactly the same list for direct items.
[Download](../../internal/transfer/local/download_execute.go) validates every planned item through that function before destination inspection, recording and effects, so both boundaries gain the rule from one predicate.

| File | SHA-256 |
| --- | --- |
| `internal/transfer/local/local.go` | `872769b173bc58823467d3f99976bf991a3acdb12952b5bf622521c693a476fb` |
| `internal/transfer/local/download_recording.go` | `66195f642ced50598fe88d2e91a3a704a24aa0f4af7e31e0602fa1699ac4dd36` |
| `internal/transfer/local/download_execute.go` | `0e16d1b1bc86af50793198837180c8d34b06f8dc666d3a44cce0ab85f41b4227` |

The refused families follow [the Microsoft file and directory naming conventions](https://learn.microsoft.com/en-us/windows/win32/fileio/naming-a-file) and its [console name documentation](https://learn.microsoft.com/en-us/windows/win32/api/fileapi/nf-fileapi-createfilea#consoles): the device bases, the console identifiers, the restricted characters, and the instruction not to end a name with a space or a dot.
The reserved-base helper in the pinned Go 1.27.1 `internal/filepathlite` recognises the device and console families the same way, which is source evidence for those two families only and not a native observation from this machine.
Its other path checks share part of this policy, rejecting every colon when deciding whether a path is local and colon, backslash or NUL when localizing a path, but they do not enforce the complete restricted-character and trailing-form rule applied here.

## Refusal rule

Apply the predicate to every slash-separated component of the relative path below the selected prefix, after removing one trailing `/` from a directory marker.
An empty relative path is the existing root marker and has no component to judge, so it stays admissible.
The three families are independent, and one component may be refused by more than one of them.

The reserved-base test cuts the component at its first `.` or `:`, removes trailing ASCII spaces from the remainder, and refuses when that remainder equals, under ASCII-only case folding, `CON`, `PRN`, `AUX` or `NUL`, or `COM` or `LPT` followed by exactly one of `1` to `9`, `¹`, `²` or `³`, or `CONIN$` or `CONOUT$`.

| Family | Refused examples | Passing near-miss controls |
| --- | --- | --- |
| Reserved bases, any case | `CON`, `aux`, `NuL`, `PRN`, `COM1`, `lpt9`, `COM¹`, `LPT³` | `NULx`, `CONSOLE`, `_CON`, `COM10`, `COM1x`, `A¹` |
| Reserved bases with an extension or trailing space | `NUL.txt`, `con.tar.gz`, `AUX .txt`, `conout$.log` | `x.NUL`, `.CON`, `console.txt`, `CONIN`, `xCONOUT$` |
| Restricted characters | `bad:name`, `a<b`, `a>b`, `q?`, `star*`, and the pipe and double-quote forms | `a-b_c.d`, `(x)`, `100%`, `#1`, `a'b` |
| Trailing ASCII space or dot | `file.`, `parent /child`, `mark./`, `deep./a/child` | `.hidden`, `a b`, `a.b`, a trailing non-ASCII `。` |

The pinned Go source comment records that since Windows 11 a reserved name with an extension is no longer reserved, and that helper then asks the native device-name API for those forms instead of deciding statically.
That is a source comment and a host-dependent fallback, not an independently established rule for every Windows release and API.
One portable rule cannot depend on the host it runs on, so this slice keeps the extension forms refused on every target and records that conservative decision here.
Trailing spaces before an extension are removed for the reserved-base comparison only; the original component is never rewritten.

Each family must be exercised at the leaf, at an immediate ancestor, at a deeper ancestor and in a directory-marker component, so a rule that only inspects the last component fails the tests.
Refusal applies to the whole selection: a safe first member must not be materialized before the later refused member is reached.

## Private preparation evidence

A private probe archive ran on `e298c40d3860419fdc85bb0702db248fdba6bc69`, whose tree `9492442db07d31c4b7666ccd1641b7de4bc1b428` is the tree delivered as `36e731fe149526d5ff760b23b9caf7bd7b1e4370`.
It is retained as `portable_components_e298_before_v1/before-evidence.tar.gz`, 2,886,010 bytes over 1,466 indexed files, SHA-256 `f636a98bf0fd18f019521dedab47ffeec052dabcf943dd558a52830a325bfaef`, so the numbers below stay traceable without publishing a private path.
It appended unmaintained probe functions through two Go overlays, retained every maintained test in the virtual files, and left all 353 recorded Go and SQL inputs unchanged.
These are preparation probes rather than maintained tests, and the regressions required below replace them.
The probed shapes were `CON`, `NUL.txt`, `parent./child`, `parent /child`, `bad:name` and `parent/CON/child`, with `CON/` and `mark./` markers in the local matrix, each placed after a safe first selection member.

| Producer | Actual exit | Intended failing refusal leaves | Other failing leaves | Passing leaves | Seconds |
| --- | ---: | ---: | ---: | ---: | ---: |
| Local and direct | 1 | 80 | 2 fixture-order mistakes | 18 | 1.654 |
| Local controls, second version | 0 | 0 | 0 | 2 | 1.987 |
| Mock service | 1 | 18 | 0 | 6 | 13.881 |
| Real Swift service | 1 | 18 | 0 | 6 | 24.759 |

The four producers hold 116 intended failing refusal leaves and 32 passing control leaves.
Every probed preflight returned a plan with no problems and left the local directory unchanged, which is no-effect evidence and not whole-selection refusal.
All 64 unsafe direct leaves admitted the constructed plan and created the selected entries across both routes, both input orders and both recorder modes, while the 16 unsafe preflight leaves failed their separate refusal assertions.
Each of the 36 unsafe service cases on the two providers created a complete job with two planned and admitted items, four retained observations and two transfer audit events, an exact `[1, 0, 2, 4, 2]` job, recovery, item, observation and audit delta.
Both the safe leading name and the unsafe later name were materialized, with the full expected bytes for file cases and both directories for marker cases.
All 48 service cases, including the twelve normal controls, preserved both owned remote namespace snapshots and reported no transfer remote writes.
Two shared-parent controls in the first local producer expected fixture insertion order although the source listing is sorted; only those two controls were corrected and rerun, and the original overlay is preserved.
An initial reader expected a missing JSON staging key for markers where Go emits an explicit null; that reader failure is preserved and the corrected reader requires the observed representation.

## Required observations

Service cases call both PreflightDownload and StartDownload through an actual picker-issued token.
A refusal requires rejecting preflight output, an empty returned job id and refusal error, unchanged job, recovery, item, observation and transfer-audit counts, no selected content GETs, no local delta, and unchanged owned remote namespaces and write trace.
Listings and marker-classification metadata reads remain permitted planning work.
When a baseline or broken control unexpectedly returns a job id, wait for it and independently read public and durable job state, complete item pages and the matching audit, preserving the failing safety assertion.
A truthful partial result or a late failure is not successful preflight refusal.

Direct cases construct exact plans with real mock source payloads and the existing local engine harness, with and without recorders, in both input orders.
They require refusal before a non-nil report, admission, execution, verification or progress callbacks, source requests, Copy invocation and any local effect.
The existing [app engine-construction spy](../../internal/app/download_root_admission_test.go) separately proves service refusal before engine setup, with a safe control that still reaches the configured sentinel.
The system stage callback runs after Copy returns and remains a returned-Copy witness, not a direct child-start counter.
Remote snapshots cover the two owned namespace scopes, not entire accounts.

Passing controls preserve every near-miss spelling in the table above, an internal ASCII space, a leading dot, a trailing non-ASCII character kept literally, two distinct children under one exact parent, a compatible directory marker with its child, and the existing nonempty root marker.
For accepted controls, inspect complete file bytes and permitted local deltas independently of the exact final job, item and audit facts.
Preserve the existing whole-leaf case-fold, case-folded ancestor, file-versus-prefix, control-character, backslash and selected-root-symlink regressions.

## Verification and retained evidence

First run the maintained safety assertions against unchanged production through a Go overlay of this parent's two production files, and retain their actual failing exits alongside the passing controls.
Record the exact source and test hashes, commands, complete raw output and provider configuration without secrets.
After the bounded correction, require the same assertions to pass, and require at least three narrowly broken guards to fail their relevant boundary while the other controls keep passing: the predicate omitted at preflight only, omitted at direct validation only, and a reserved-base comparison without the extension cut.
Then run the compatibility checks and the complete inventory, scenario and coverage verification under the existing finite budgets, reconciling every prior terminal name against the delivered inventory and listing the additions.
Any existing fixture that this rule newly refuses must be surfaced by that reconciliation rather than adjusted silently.
Independent local review is required for this plan and for every implementation commit, before protected PR delivery and all five resulting master checks.
No maintained runtime result, test count, coverage figure, native platform outcome or delivery is claimed by this plan; the private probe section above is preparation evidence and is labelled as such.

## Remaining limits

The rule is lexical and ASCII-only: it is not full Unicode case folding, normalization or equivalence, and it decides nothing about non-ASCII homoglyphs.
Leading spaces, component and path length limits, full-width substitutes and rclone's own restricted-character encoding collisions stay outside this slice.
Directory-only aliases, and the question of whether a given refused spelling is a device on a given Windows release, remain undecided.
No Windows device input or output is attempted anywhere in this work, and a broken guard is never proved through a real device.
Native macOS and Windows filesystem acceptance is a separate claim that source inspection and cross-compilation cannot supply.
Selected-root lifetime, concurrent parent or root replacement, exact-file publication, cleanup authority and child quiescence remain separate P18, P20 and P30 work.
P18, P19, P20, P30 and the valuable-data hold remain open.
