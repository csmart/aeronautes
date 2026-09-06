# Copy overwrite refusal validation

This is the second P05 implementation under [ADR 0020](../adr/0020-recovery-containment.md) and DESIGN §65.11.
The oracle is refusal before staging an existing changed destination, conditional creation for eligible new names, preserved outside data and honest partial outcomes.
It does not establish a protocol for safely overwriting an existing object.

## Behaviour

`Changed` items are refused without calling Swift or the engine, on both engine and native-name routes.
`Unknown` items still resolve through HEAD: proven equality skips, a missing destination uses conditional creation, and an unequal existing destination is refused.
Native, staged plain, DLO and symlink commits retain `If-None-Match: *`; the unconditional overwrite branches are removed.
Local upload retains its existing preflight rejection and conditional live PUTs.
Versioning does not enable overwrites.

Every job with a refused overwrite ends `partial`, including zero-copy, cancelled and later-failed jobs.
The error retains the refusal count and reason plus later cancellation or error detail.
`overwritesRefused` counts refused plan items separately from the bounded failure examples; `overwrites` remains zero and actual copied-object/byte totals are preserved.
The terminal audit operation is `transfer.copy.partial`, with the job identity and refusal count in its summary.
Its existing `verify_mismatch` result is a coarse incomplete-transfer category and does not prove that any write succeeded.
P13 still owns a more precise audit vocabulary.

Verification excludes exact plan-relative names recorded when execution refused them, under that job's source/destination prefix pair.
A sorted private temporary file uses length-framed raw names, an expected count, size and SHA-256 digest.
Memory retains one bounded name at a time; disk usage grows with the number and length of refusals.
Creation/write failures, unavailable records, count/size changes, malformed lengths, ordering failures and substituted names refuse verification instead of producing successful evidence.
Verification drains the record even when the original names disappear from fresh listings.
The file is closed and removed after verification or execution failure; this is a runtime decision record, not a durable restart journal.

The preview distinguishes changed objects that will be refused from unknown objects that need comparison, and labels listed bytes as comparison candidates rather than promised transfer bytes.
The jobs drawer displays refusals separately from copied-object failures.
Historical Mirror reruns report Mirror unavailable.

## Reproductions and preservation

The archived DS-02 and DS-03 mechanisms both failed before containment.
The archived application probe needed only two cached-session field accesses adapted to the current cache structure; its initial compile failure is excluded from behavioural evidence.
Early refusal makes their old final-HEAD overwrite/delete boundary unreachable, so no unchanged-after PASS is claimed for those probes.
The initial 16-case preservation/refusal regression compiled against the previous Copy executor and failed all 16 leaves before production changes.
The new refusal-counter and verification-record assertions were added with the implementation and checked with deliberate regressions below.

| Maintained test | Evidence |
| --- | --- |
| `TestCopyRefusesExistingDestinationsBeforeStaging` | 16 cases: engine/native × plain/DLO/SLO/symlink × Changed/Unknown; no staging, copied bytes or engine calls; no Swift request for Changed; independent snapshots of both scopes, shared backing data and outside aliases |
| `TestRefusalVolumeCannotHideCopiedMismatch` | 261 exact refusals, including empty relative names, newline, literal percent and slash distinctions; two actual copies; a later copied-object mismatch remains the sole failure; a refusal that later becomes equal never earns verification credit |
| `TestVerificationRefusesDamagedRefusalRecords` | Missing/extra bytes, same-size substitution, oversized framing, closed descriptor, count mismatch, failed writes and unordered decisions all reject verification |
| `TestVerificationChecksRefusalsAbsentFromFreshScope` | Empty fresh scopes cannot conceal a corrupt original refusal record; the intact control produces zero verified objects |
| `TestRefusalRecordErrorsStayVisible`, `TestRefusalRecordClosesAfterVerification` | Failed recording remains visible through Finish and Verify; closed executors cannot issue verification evidence and remove their own temporary file |
| `TestCopyRefusalJobOutcomes` | All-refused, mixed, later cancellation, refusal-file creation failure and verification-file damage all remain partial, with independent storage preservation, exact copied/refused counters and terminal audit detail |
| `TestCopyRefusesOverwritesRealSwift` | Public StartCopy against two owned Swift accounts: 210 refusals, two copied objects/16 bytes, one pre-existing equal object, three verified objects, zero overwrites/failures; plain/DLO/SLO/symlink/native/empty-relative refusals and readable shared outside aliases preserved |
| `TestVersionedOverwriteRefusedRealSwift` | Existing versioned destination remains unchanged and its archive remains empty after refusal |
| `TestCopyExecutorRealSwift` | Supported new-name large copy and metadata preservation still pass; a later changed source is refused; restored source equality remains skippable |
| `TestUploadRefusesExistingRepresentations` | Eight local-upload cases across engine-safe/native prefixes and four destination representations preserve existing objects, backing data and a readable outside alias without engine admission |

Mock SLO fixtures carry a logical body and separately observed manifest/segment data because the mock does not assemble SLOs.
The real-Swift test supplies actual DLO/SLO assembly evidence.
Independent witnesses use separate listeners/clients over the fixture storage, outside the product's fault endpoint.
Real-Swift snapshots cover the entire harness-owned namespace, including generated sidecars, not every container in the account.

## Acknowledged conditional-write controls

`TestPlainCommitRacerSurvives` retains the P03 staged-plain final-HEAD barrier.
`TestConditionalCreationPreservesRacers` adds staged DLO, staged symlink, native plain and native symlink routes.
Each barrier identifies the exact method, escaped path and occurrence and acknowledges the selected response before the competing writer is planted.
The direct native plain path holds the source GET because it has no final destination HEAD.
Every run reports conflict and preserves the competing destination and all observed backing bytes; supported staged cleanup may remove only the exact staged manifest allowed by the test.
These tests do not grant broader cleanup authority.

`TestNativeUploadPreservesPostPreflightWriter` plants a destination after successful local preflight and proves native upload cannot replace it.
`TestStagedUploadPreservesFinalHeadRacer` exercises both plain and DLO local commit functions after an acknowledged final HEAD, with a separate storage witness.
The local DLO test seeds the staged representation and tests the native commit boundary; real-rclone large staging remains covered separately by integration tests.

## Deliberately broken variants

Temporary Go overlays and frontend copies keep the submitted source unchanged.
Every variant below compiles or loads its tests and fails an intended behavioural assertion; setup failures and panics are excluded.
Counts are failing leaf tests in the selected scope, not an estimate of every test that could catch the defect.

| Removed or restored behaviour | Failing leaves |
| --- | ---: |
| Admit Changed destinations | 8 |
| Admit unequal Unknown destinations | 8 |
| Remove native Copy conditional header | 1 |
| Remove symlink Copy conditional header | 2 |
| Remove DLO Copy conditional header | 1 |
| Remove plain Copy conditional header | 1 |
| Include refusals in verification | 1 |
| Infer exclusions from fresh Changed rows | 1 |
| Remove partial-result coercion | 5 |
| Discard refused counter | 5 |
| Omit audit refusal count | 5 |
| Ignore refusal digest | 2 |
| Omit refusal-record drain | 1 |
| Ignore refusal-record count | 1 |
| Hide sticky record error from Finish | 1 |
| Remove native local-upload conditional header | 1 |
| Remove DLO local-upload conditional header | 1 |
| Remove plain local-upload conditional header | 1 |
| Admit a rejected local-upload preflight | 8 |
| Restore preceding transfer dialog | 2 |
| Restore preceding jobs drawer | 1 |
| Restore preceding fake Copy cancellation | 1 |

The system receipt helper rejects seven false-credit cases: a start error, missing job, read action, unknown action, or a complete/failed/cancelled terminal job.
The real-Swift mixed refusal test credits only StartCopy's partial scenario after its assertions; Mirror continues to receive refusal credit only.

## Verification and limits

The final required two-account Swift run with official rclone 1.74.3 and the private Secret Service fixture exits zero: 484 top-level Go tests across 20 packages, zero failures and zero skips under the race detector, with 84.0% statement coverage.
The scenario report is valid at 13/106 cells, with 47/49 actions still having gaps; the added credit is StartCopy partial, not a completed audit or release guarantee.
Frontend coverage exits zero with 75 tests in 11 files and 31.26% statements; Svelte reports zero errors/warnings and the production build passes.
Go formatting, vet, full build and the CGO-disabled internal build pass.
The required Wails binding task was repeated after staging the generated models and produced no binding drift; its known unrelated module-tidy changes were inspected and restored.
The first full run found a coverage-marker hygiene false positive in the false-credit test's output assertion.
The assertion now checks the claim payload without referencing an emission marker, and the unchanged hygiene rule and full suite pass together.
Earlier compile/setup failures and that failed run are retained separately and are not counted as successful evidence.

The existing terminal-job write precedes the separate audit append; tests wait boundedly for the exact operation and job identity rather than weakening that assertion.
P12/P13 durability, ambiguous remote results, cancellation quiescence and authoritative restart outcomes remain open.
P06 verification depth, P14 overwrite/Mirror concurrency, P15/P21 transfer-time cleanup and the broader platform/performance/release gates remain open.
The private file's POSIX mode check does not establish Windows ACL or native desktop proof.
The valuable-data release hold remains.
