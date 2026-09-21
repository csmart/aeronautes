# Later listing generations — 21 September 2026

## Oracle and source

Frozen Linux finish-line L11 requires account-specific results, paging, retries and errors.
[ADR 0063](../adr/0063-account-portrait-binding.md) binds the portrait, capabilities and initial container page and left later reply generations for follow-up.
The assessment ran on its approved head `2a91c3ba984b9b07c672ad4e61e30fe50b4f1186`, with tracked production source unchanged.
This basis is the portrait branch, not the later master; final integration needs its own verification.

## Observed result

An ordinary Vitest probe used the real `BrowserState`, shared fake bookmark save/connect operations and deferred page replies.
A separate fake API caller re-saved and reconnected the same bookmark, advancing its generation from one to two without changing this browser's retained portrait or navigation revision.
Generation-two pages then replaced rows through `loadContainers` and `goTo` through `#loadEntries`, and appended rows through `loadMoreContainers` and `loadMoreEntries`.
All four named rejection assertions failed, while all 16 controls passed: matching replies after unchanged-generation reconnect, correctly captured generation-one replies after an external re-save, and obsolete successes/errors after this browser's own re-save/reconnect.
The existing 20 portrait-binding tests also passed unchanged, for 36 passes and four failures with no skips.
The four failures were row assertions; later cursor assertions were not reached and are not credited as executed proof.
Type checking reported zero errors and warnings.
The 2.172-second run had start/end loads of 0.42/1.38/2.35 and 0.71/1.42/2.36; endpoint process snapshots saw no other Go or Vitest command but do not prove exclusive host use throughout.

The shared fake's container pages already use its generation-aware scope helper, while its object pages hard-code generation one on this basis.
Explicit page stand-ins avoided that blind spot in the assessment.
These are frontend contract results, not backend or real-Swift reproduction, and no mutation was exercised.

## Evidence and correction

The sealed private packet is `/home/csmart/code/.aeronautes-work/20260921-later-read-generation-probe/`.
Its `SHA256SUMS` digest is `346dbc57ae402ea21549b0b2e11ea44692cede1a0ffc5e106bb22db7109a14c6`.
It retains all 20 probe outcomes and 20 maintained portrait outcomes, exact sources, raw and structured results, runner metadata and checks.
[ADR 0067](../adr/0067-later-listing-generations.md) now binds only the four later listing paths to the retained portrait's bookmark and positive generation, with explicit failed-read behaviour and independent row/cursor assertions.
The maintained regression file ran against contract-only head `6001dcd4430daa017d4c7df48518da67c8f7f224`: 67 named cases failed by assertion while 45 controls and existing portrait cases passed, including separately executed cursor failures on all four paths.
The final before run type-checked and all its test failures were assertions; the earlier matcher diagnostics and a refused producer launch with a mistyped expected head remain retained without additional credit.
After the correction, all 88 new cases passed; the affected existing tests first exposed 122 failures from zero-generation success fixtures and missing fixture portraits.
Those raw failures are retained, and the fixtures now supply their captured scopes without changing any existing assertion or test title.
All 487 affected tests then passed, including the 24 unchanged portrait and portrait-fake cases.
Ten compiled omission controls were caught by their named assertions: generation comparison, each of the four request-lifetime gates, each path publishing its cursor before validation, and the fake object-generation mapper.
The unchanged-generation control passed in every variant; a separate syntax-error self-test was classified as a build failure and received no behavioural credit.
These maintained proofs, sources, command results and concurrency snapshots are in `/home/csmart/code/.aeronautes-work/20260921-later-listing-generations/`.
Its 835-file `PROOF-SHA256SUMS` digest is `63f689982e51015a74856353562bfcaec575f4a286f82f971d6f4b4d8c340dd7`.
Other authorised suites could run on the host; the packet records actual endpoint processes and load without claiming isolation or hosted timings.
Object, manifest, segment, prefix, Find-start and container-detail acceptance were only source-inspected and remain unproved/outside; historical jobs and audit have separate scope contracts.
No backend snapshot guarantee, mutation authority, L11/P28 completion or valuable-data clearance follows.
