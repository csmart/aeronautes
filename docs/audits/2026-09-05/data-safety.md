# Aeronautes data-safety audit

Audit date: 2026-09-05.
Inspected checkout: `<repo>`.
Inspected product revision: `cc6465ac69ec00756d9976bbf6cf7531b07da66d`.
This is a bounded domain assessment within the [full project audit](README.md).
No tracked source was edited and no real Swift account or user files were mutated.

## Assessment

The project has substantial safety architecture and useful regression tests, but it is not yet suitable for claiming safe unattended operation against valuable data in a namespace with other writers.
Current code still permits deletion of an unrelated live object's shared segments, destruction of a concurrent replacement during Mirror, overwrite of a concurrent writer, and successful verification of corrupted large-object content.
The known W/R architecture backlog is therefore still justified by executable evidence at the current revision.
The fault/barrier harness belongs in the remediation plan because it supplies the repeatable evidence these fixes need; it should be paired with early containment rather than treated as a prerequisite to acknowledging or containing the hazards.

## Scope and evidence method

Read the project `AGENTS.md`, `docs/REQUIREMENTS.md`, relevant `docs/DESIGN.md` sections, the Go copy/planner/local-transfer/deletion/segment-evidence implementations, Swift metadata/listing/bulk-delete adapters, application service orchestration, and associated tests.
Applied the local coding-review skill and ECC Go-testing guidance to the review method: interface-level probes, public service calls where practical, behavioural assertions, temporary state, and deterministic event boundaries instead of sleeps to plant races.
The existing app test helper still polls job completion; it is not used to choose the interleaving in these probes.

Behaviour oracles are the stable K-* requirements, the detailed Swift invariants in `AGENTS.md`, the intended before/after data state, and the repository's recorded real-Swift seam evidence.
Conflicting design prose is identified below rather than silently resolved.
Findings distinguish current-code reproductions, static revalidation, and matters still requiring real-server/platform evidence.

Eleven targeted regression assertions were added only through a Go source overlay under `data-probes/` in this temporary audit directory.
The final combined run executed 11 audit tests: **0 passed, 11 failed at the intended safety assertion**, reproducing the behaviours described below.
These are intentionally red scratch probes, not failures introduced into the repository's normal test suite.
An earlier broad `TestAudit` app run also ran the existing `TestAuditSearch`, which passed; the final recorded run excludes that unrelated existing test.
The final [data probe log](evidence/data-probes.log) and [probe sources](probes/README.md) are preserved with this audit.
All mock requests stayed on loopback and all filesystem writes stayed in temporary test directories.
The normal sandbox initially prevented `httptest` from listening; a narrowly escalated mock-only test command was approved and executed.
Go 1.27.1 was supplied by the root audit task, with caches under this temporary audit directory.

Use the portable reproduction instructions in [probes/README.md](probes/README.md).
The original data log precedes scratch-source formatting, so its probe line numbers can differ from the archived source.
Production references identify the audited revision.

## Findings

### DS-01 — Critical: recovery deletes segments referenced by an unrelated live object

Disposition: revalidated W-04; directly reproduced, high confidence.
Code: `internal/transfer/copy/verify.go:152`, `:187`, `:200`, `:247`; application recovery entry point `internal/app/transferservice.go:592`.
Oracle: K-XFER-002, K-SEG-004, K-SEG-001, and the explicit no-collateral-destruction requirement.

`SweepLitter` preserves a captured prefix when a committed row exists or selected recorded/derived live names still reference it.
It does not prove that another manifest references the same prefix before `sweepSegmentPrefix` deletes its objects.
An external live alias to a job's captured-but-not-committed prefix is therefore destroyed by otherwise routine reconciliation or startup recovery.
The probe created `external-alias` referencing a captured prefix whose derived staging name was absent.
The sweep returned `SweptSegments:1`, `KeptPrefixes:[]`, `Notes:[]`, and the alias's logical bytes changed from `LIVEDATA` to empty.
The zero-note result is particularly significant because callers treat it as permission to clear recovery records.

There is another fail-open edge in the same decision: `objectFromGeneratedPrefix` returning false does not keep the orphan despite the helper's comment; execution falls through to deletion.
In-process `Reconcile` also supplies no `Committing` map, whereas startup recovery does, so ambiguous live-PUT responses deserve a separate durable-boundary regression before safety is asserted.
That latter response-loss sequence was not reproduced during this bounded audit.

Suggested remedy: immediately retain captured segment residue rather than automatically deleting it until the ownership contract can actually establish exclusive deletion authority.
Resolve the conflicting ownership design before implementing a reverse-reference scan; a scan alone does not exclude a writer creating a new reference after it.
Keep durable records and an operator-visible orphan report on uncertainty.
Acceptance: both live in-process reconciliation and second-process crash recovery preserve the content digest of every outside DLO/SLO alias, including a new alias created at each decision boundary; malformed provenance causes retention, never deletion.
Probe: `TestAuditSweepKeepsExternallySharedCapturedPrefix`.

### DS-02 — Critical: Mirror deletes a replacement created after the final identity check

Disposition: revalidated W-02; directly reproduced through `StartMirror`, high confidence.
Code: `internal/app/mirrorservice.go:250`, `:258`, `:372`; `internal/deletion/deletion.go:349`.
Oracle: the plan's destination identity, K-XFER-014, DESIGN §28.6's identity-dependent mutation rules, and the existing `TestMirrorSkipsReplacedDestination` contract.

The service re-HEADs destination identity and source absence for a chunk, then passes only names to the unconditional deletion engine.
Even the last name in a one-object chunk can be replaced after its HEAD and before DELETE.
For a 2,000-name chunk, early checks are additionally separated from deletion by all other checks.
The probe replaced `victim` with `NEW RACER DATA` in the mock's after-HEAD hook.
The replacement was deleted; the job ended `complete`, with one deletion and no errors.
The source-absence side has the same fundamental final-hop window.
The repository records that Swift ignores `If-Match` on DELETE, so adding that header is not a fix.

The related known Grok P1-6 is also visible in the same pipeline: no staging/job-ownership filter excludes another active job's `.aeronautes/stage/` objects from destination-only deletion.
That active-job case was statically revalidated, not independently exercised here.

Suggested remedy: contain destructive Mirror where writer exclusion cannot be established, define exactly what concurrent-operation guarantees the product supports, and obtain an enforceable server/workflow boundary for those guarantees.
Introduce scope reservations between local jobs and protect their staging namespace while designing the external-writer policy.
Plan tokens are necessary to bind the operator's review, but do not by themselves fix this final remote race.
Acceptance: deterministic barriers after the last source HEAD and destination HEAD preserve the raced identity; overlap with another job's staging space must refuse or coordinate safely; full-account snapshots establish no collateral deletion.
Probe: `TestAuditMirrorPreservesLastHopConcurrentWriter`.

### DS-03 — Critical: overwrite commits clobber a final-hop concurrent writer

Disposition: revalidated W-03; directly reproduced, high confidence.
Code: `internal/transfer/copy/executor.go:458`, `:481`, `:635`, `:683`, `:726`, `:867`.
Oracle: the recorded destination identity and stage-and-commit conflict contract in DESIGN §28.6.

Planned-new PUT commits correctly use `If-None-Match: *`, but existing-object overwrites remain unconditional after the final HEAD.
The plain path uses server-side COPY; the DLO and native/symlink paths use unconditional PUT.
A write after the re-HEAD is overwritten and subsequent verification reports Aeronautes' own data as correct.
The deterministic plain-overwrite probe changed `OLD` to `RACER` after the second destination HEAD.
The result was `NEW`, `Copied:1`, `Overwrites:1`, and no conflict.
This is a data-loss window even though the staging boundary correctly prevents rclone from deleting the old object's segments.

Suggested remedy: contain overwrite in externally writable namespaces until the accepted concurrency policy has an enforceable commit boundary.
Do not substitute another best-effort HEAD or unsupported conditional header for evidence.
A server-supported version/conditional primitive, exclusive writer workflow, or explicit refusal may be required.
Acceptance: final-hop competing writes survive for plain, DLO, native-name, and symlink commits, both unversioned and versioned; response loss after a landed commit does not cause an unreviewed second overwrite.
Probe: `TestAuditOverwritePreservesLastHopConcurrentWriter`.

### DS-04 — High: large-object verification accepts corruption, and a stale destination fingerprint preserves it

Disposition: revalidated W-07/R-05; two direct reproductions, high confidence.
Code: `internal/transfer/copy/executor.go:43`, `:256`, `:396`, `:790`, `:816`; `internal/transfer/copy/verify.go:52`.
Oracle: K-CON-001, K-XFER-009, DESIGN §28.4's strong equality evidence and logical replication semantics.

`verifyCopied` compares ETags only for a plain/plain pair.
If either side is a large object, equal logical length suffices.
`verifyStagedSegments` counts entries but accepts solely when their summed bytes equal the source size; it compares no ordered names, per-segment digests, or content digest.
The first probe changed a staged segment from `GOOD` to `EVIL`, preserving its length and generating an honest changed segment ETag.
Aeronautes committed `EVILDATA` while the source was `GOODDATA`, returning `Copied:1` and no failures.

`provenEqual` then trusts the destination's persisted source fingerprint plus equal logical length, without binding that fingerprint to the current destination segment graph/content identity.
The second probe first performed a correct copy, changed a destination segment to different same-length content, and re-ran an Unknown comparison.
It returned `SkippedEqual:1` and performed no repair.

Local transfers have weaker parallel checks: `internal/transfer/local/local.go:447`, `:505`, `:531`, `:637`, and `:676` rely on byte counts; local DLO upload lacks even Copy's bounded segment-listing gate.
That local-DLO omission is the already recorded Grok P1-3, not a new finding.
Successful engine status and right length do not prove that the intended version of a file arrived.

Suggested remedy: define a typed verification-strength contract shared by Copy, Mirror, upload, download, and retries.
Bind equality records to both current source and destination identities; use an ordered inventory where valid and logical byte digests where resegmentation makes manifest identity insufficient.
Same-size mutations must invalidate equality.
Do not claim strong verification when only size evidence exists.
Acceptance: same-size segment replacement, permutation, stale source identity, source mutation during transfer, missing/duplicate inventory entries, and resegmentation are checked end to end; Mirror deletion stays blocked for unverified transfers.
Probes: `TestAuditCopyRejectsSameSizeCorruptSegments`, `TestAuditSkipRejectsChangedDestinationSegments`.

Current disposition: [PR93’s bounded destination change detection](../../experiments/2026-09-11-copy-verification-strength.md#what-this-slice-claims-and-what-it-does-not) narrows the existing skip predicate for segmented destinations.
A DLO destination additionally needs a usable staged aggregate stamp matching its live aggregate; an SLO destination is refused by this predicate.
The optional stamp uses the existing conditional publication only when it fits the complete advertised metadata allowance, preserving every user value; it does not strengthen post-copy content verification.
A skip means only that no aggregate difference was observed against the staged observation this executor recorded when it published the name; it never means that the destination equals the source, because a wrong initial copy still skips and a stale segment listing can still conceal a substitution.
The staged-content half, current source/destination binding, large-source/plain-destination case and local-transfer verification remain open under P16.
Logical-content verification must accommodate legitimate resegmentation, with its content-read cost and policy reviewed separately; comparing source and destination segment layouts is not a substitute.
DS-04 remains blocking, Mirror starts remain refused under ADR 0020, and this slice grants no destructive follow-up authority.

### DS-05 — High: downloads escape the selected directory through parent symlinks

Disposition: newly specified reproducible defect within the broad W-11/R-09 local-filesystem backlog; high confidence.
Code: `internal/transfer/local/local.go:218`, `:566`, `:611`, `:640`, `:648`, `:661`, `:690`.
Oracle: K-XFER-011, DESIGN §27.5, and the picker-issued destination boundary in `internal/app/localtransferservice.go:35`.

Lexical traversal checks do not validate filesystem parent identity.
Preflight follows `os.Stat`, and placement uses ordinary path-based `MkdirAll` and `os.Link`.
If an existing child of the picked directory is a symlink to another directory, a new remote `redirect/payload` lands outside the selected root.
The probe created such a parent symlink, ran actual preflight and Download, and observed the outside file contain `DATA` with `Done:1`, `Bytes:4`, and no failures.
No destination replacement race or hostile local process was needed; an ordinary existing symlink was enough.
Native downloads and directory markers use the same unchecked parent traversal.

Suggested remedy: make local destinations root-relative, bind the selected directory's filesystem identity, and perform traversal/creation through handles that prevent escaping that root.
Reject unsupported symlinks/reparse points before any write, and prevent parent replacement from reopening the escape after preflight.
Preserve no-replace placement at the final leaf.
Acceptance: pre-existing symlink, symlink planted after preflight, directory marker, native download, engine download, and Windows reparse-point variants produce no outside filesystem delta.
Probe: `TestAuditDownloadDoesNotEscapePickedDirectory`.

Current disposition: [PR77](../../experiments/2026-09-09-download-admission.md#protected-delivery) delivers the ADR 0034 refusal of evidenced existing destination hazards before Start and before the executor's first effect.
Both owned providers, direct callers, queued jobs and witnessed inspection errors have maintained regressions; the accepted initial selected-root alias remains supported.
[PR97](../../experiments/2026-09-11-download-root-authority.md#protected-delivery) then delivers [ADR 0035](../../adr/0035-download-root-authority.md): the picked directory is held by handle from the pick through every effect, every path beneath it is opened one component at a time through the parent handle with links, non-directories and foreign devices refused, the stage is bound and re-identified around the engine run, publication links by directory descriptor and confirms the target names the verified file, cleanup removes only planned empty directories through handles, the Linux engine child is anchored to the held stage by an inherited descriptor, and the engine route is refused where no anchor exists.
A renamed selected directory keeps receiving effects and the job ends partial with the displacement named; a deleted one refuses the job; the operator settled the pick-reuse, rename, mount and engine-gate choices on 12 September 2026 as delivered.
The archived probes are maintained regressions on both providers with four deliberately broken guards, and the anchor is shown by three retained engine experiments.
Native Windows and macOS acceptance, reparse points and the engine route on those platforms remain open, so DS-05/P18 stays blocking for that native evidence only, and the valuable-data release hold remains.

### DS-06 — High: the cross-platform collision preflight misses case-folded ancestor conflicts

Disposition: newly specified reproducible case within W-11; high confidence for the current policy violation, actual platform materialization still needs native runners.
Code: `internal/transfer/local/local.go:165`, `:250`, `:267`, `:277`.
Oracle: K-XFER-011 and this package's stated single cross-platform rejection policy.

Whole-path lowercasing catches `Foo` versus `foo`, while the file-versus-parent check compares only the original case-sensitive names.
It consequently accepts `Foo` as a file and `foo/child` as another file.
Those require one case-insensitive filesystem location to be both a file and a directory.
The probe's complete preflight returned two items and zero problems.
On Linux the layout can be created, so a Linux-only execution test would miss the policy and portability failure.

Suggested remedy: use the target materialization identity consistently for complete paths and every ancestor, including normalization and filesystem-specific reserved forms.
Use an external/scratch-backed index to preserve bounded memory for large namespaces.
Acceptance: case-folded ancestor conflicts, Unicode-normalized ancestor conflicts, reserved device names, trailing dots/spaces, and existing local ancestors reject the entire plan before any write on each supported platform.
Probe: `TestAuditPreflightRejectsCaseFoldAncestorConflict`.

Current disposition: [PR78](../../experiments/2026-09-09-download-path-case.md#protected-delivery) delivers the case-folded file and ancestor refusal at preflight and at the executor's item admission, before any marker or filesystem effect.
Both spellings of each conflicting pair are refused across the whole selection on the owned providers, with a safe first member and passing shared-parent controls.
[PR81](../../experiments/2026-09-10-portable-download-names.md#protected-delivery) then delivers one lexical portable component rule at the same two boundaries, refusing Windows device and console bases, the seven restricted characters and components ending in an ASCII space or dot, across the whole selection and without trimming or renaming.
That rule is conservative on every host rather than a claim about any particular Windows release, and no Windows device is opened to prove it.
[PR91](../../experiments/2026-09-10-download-name-normalization.md#protected-delivery) then checks a canonical caseless relation beside the established lowercase relation at the same two boundaries, side by side and never joined transitively, so planned names that differ only by Unicode normalization form are refused as one collision while every single spelling stays selectable.
The helper walks the canonical decomposition tables directly rather than calling the library's stream-safe normal forms, which would equate a long combining run with the same run broken by a real joiner and so refuse two distinct names.
One consequence is recorded rather than hidden: a plain letter beside the same letter with a combining mark stays admitted, because only a chain through the precomposed character would join them.
[PR97](../../experiments/2026-09-11-download-name-encoding.md#protected-delivery) then routes any name containing a rune the pinned engine reserves as a substitute or quote natively on every platform through the shared engine-safety predicate, on the download, upload and copy routes; before it such names were admitted to the engine route and silently dropped with a successful engine exit, and a full-width full stop component failed the whole batch.
The reserved set is pinned to the frozen v1.74.3 encoder source and recorded in the engine pin document, with maintained regressions on both routes and both providers and one broken control per rune class; the operator settled native routing and the union set on 12 September 2026 as delivered.
For the remainder the operator settled on the same day: components longer than 255 bytes are to be refused at both boundaries with a named reason, and leading spaces and compatibility look-alikes are admitted and recorded, as rsync, scp, rclone, Finder and Explorer treat them.
Component and path length limits, directory-only aliases and native target-filesystem acceptance remain open, so DS-06/P19 and the valuable-data release hold are still blocking.

### DS-07 — High: cancellation after staging still places files and can report completion

Disposition: concrete reproduction of deferred W-11 cancellation coverage; high confidence.
Code: `internal/transfer/local/local.go:611`, `:632`; terminal mapping `internal/app/localtransferservice.go:318`.
Oracle: the cancellable job contract, DESIGN §46 and §65.10's cancellation rows.

After a successful engine return, Download iterates and places every staged file without checking the context again.
`placeDownloaded` accepts no context.
The probe cancelled synchronously after the engine staged the file but before returning control to Download.
Download still created the final file and returned `Done:1`, no failures, and a nil error.
The normal `runLocal` caller maps that result to `complete`.
With many files this allows a substantial placement phase to continue after the operator cancels.

Suggested remedy: check cancellation before each placement and preserve exact already-placed outcomes; apply the same contract to native writes and directory creation.
Treat cancellation after any irreversible result as an explicit partial/cancelled outcome with resumable identity records rather than silently complete or an uninformative cancelled label.
Acceptance: cancel before staging, after staging, between placements, during native copy, and during cleanup; only pre-cancel committed names may exist, and the durable job state must match the filesystem snapshot.
Probe: `TestAuditCancelledDownloadStopsBeforePlacement`.

Current disposition: that archived reproduction [no longer reproduces on master](../../experiments/2026-09-11-archived-probe-status.md#result), and maintained tests cover cancellation between items, cancelled body closure and the engine cancellation boundaries in the local transfer package.
This records coverage of the reproduced scenario on Linux and nothing more.
Cancellation at native writes and placements, exact partial outcomes, child quiescence and the native parent-kill evidence on all six targets are untouched, so DS-07/SEC-06 stays blocking.

### DS-08 — High: outcomes are event counts and samples, not an exact identity ledger

Disposition: revalidated W-08/R-10; direct Copy-service reproduction plus static destructive-path evidence.
Code: `internal/transfer/copy/executor.go:209`; `internal/transfer/copy/verify.go:73`; `internal/app/transferservice.go:472`, `:519`; `internal/app/mirrorservice.go:229`, `:285`; `internal/deletion/deletion.go:182`, `:396`; `internal/app/segmentservice.go:260`.
Oracle: K-DEL-002, exact job accounting, and the ability to retry the actual failed identities.

Copy counts the same name once as an execution failure and again as a verification failure.
The service probe dropped one object and obtained `ObjectsPlanned:1`, `ObjectsFailed:2`.
Conversely, each failure slice truncates at 200, and final counters are rebuilt from slice length, so sufficiently large jobs undercount failures.
Local transfers have the same 200-record cap with no complete identity channel.

For malformed or incomplete bulk-delete responses, the deletion layer treats an aggregate accounted count as an index into the original request to choose unaccounted names.
Aggregate counts do not identify which names succeeded; with out-of-order explicit errors that fallback can omit or duplicate failed identities.
`DeleteManifestWithSegments` returns only failed counts for SLO members after deleting the manifest, leaving no durable exact set that a retry can reconstruct.
The DLO force flow supplies no `OnFailure` sink.
Mirror discards the error returned by `closeSpill`, despite exact failed-name persistence being part of its safety claim.
These destructive subcases were statically inspected, not re-run with a malformed provider during this bounded audit.

Suggested remedy: persist one outcome per stable object identity and operation attempt, distinguish unknown from confirmed failure, keep bounded UI samples separate from complete records, and fail closed or retain an unresolved state when that record cannot be written.
Never derive an identity from an aggregate response count.
Acceptance: 1, 201, and multi-chunk failure sets; out-of-order/duplicated/unrequested bulk errors; lost response after some successes; failing spill storage; cancellation after a subset; and segment retry after the manifest is gone.
Probe: `TestAuditCopyCountsOneFailedIdentityOnce`.

Current disposition: that archived reproduction [no longer reproduces on master](../../experiments/2026-09-11-archived-probe-status.md#result), and the maintained copy and upload item-record tests plus the interrupted-record reopen test cover one outcome per identity on both the mock and the owned service.
[PR94’s reply-identity correction](../../experiments/2026-09-11-bulk-delete-identities.md#identity-validation-on-every-reply) replaces the aggregate-position fallback with validation of every bulk reply’s requested membership, uniqueness, status shape and counts.
Only validated deleted/not-found aggregates are credited, as service-reported totals rather than independently observed or individually identifiable successful deletions; a reported error never proves that its named object survived.
With the healthy [ADR 0036 index](../../adr/0036-delete-identity-accounting.md), reported-failure names and bulk unresolved-effect names are each counted once across batches and passes, independently of the 200-record display samples.
An untrusted bulk reply credits no aggregate and leaves every requested name unresolved.
Index setup refuses either executor before its requests; an unindexable admitted outcome instead stops new admission, drains the bounded already-issued work and exposes the last committed counters as progress with final identity totals unavailable.
That public outcome is partial even when no deletion was credited.
The affected bulk request or admitted worker requests form a separate bounded attempt record that may repeat names, not a distinct failure set or retry authority; failure of its checked retention explicitly discloses that the issued identities could not all be retained.
An ordinary retained-list open/write/flush/close failure with a healthy index preserves exact counts but offers no complete available list, while checked index close/removal failure after all outcomes were recorded reports cleanup failure without invalidating the known totals.
Deletion and benchmark terminal counters, qualifier, reason, state and audit now use the existing atomic completion path; absent qualifiers on older jobs do not retrospectively prove complete accounting.
The forced DLO error path discloses its already-deleted manifest and any incomplete segment accounting, but does not supply a complete ordinary DLO/SLO reported-failure ledger.
The [existing sweep still re-attempts unresolved names](../../experiments/2026-09-11-bulk-delete-identities.md#the-sweep-re-attempts-an-unresolved-name), so fresh authority through replacement races remains a blocking follow-up; healthy parallel deletion still lacks the separate unresolved stream.
Those hazards, complete per-effect/durable outcomes, ordinary segment-failure retention and the wider P12/P13/P28 acceptance work keep DS-08/SEC-08 and the valuable-data hold blocking.

### DS-09 — High: metadata-only changes never propagate through Copy/Mirror

Disposition: revalidated W-12/R-15; directly reproduced through StartCopy.
Code: `internal/transfer/plan/plan.go:270`; `internal/transfer/copy/executor.go:179`, `:256`; `internal/transfer/copy/verify.go:30`.
Oracle: K-XFER-010, DESIGN §27.4 and §17.1's writable-header preservation contract.

Listing equality establishes only content equality, but a Same verdict skips all work and verification without comparing the required metadata surface.
The probe gave source and destination identical `DATA` bytes but changed source `Purpose` metadata from `old` to `updated`.
Copy ended `complete`, with one verified object and no transfer; the destination kept `Purpose:old`.
Content headers and scheduled-expiry-only changes have the same structural omission.
This can leave an apparently current replica with old retention or serving behaviour.

Symlink fidelity is also partial: the copy path starts by following normal HEAD/GET, so a dangling or inaccessible-target link cannot reach the no-follow link copy path; copied-link verification checks target text but not the full target-account/content-header/metadata surface.
These symlink edge cases were statically traced, not independently reproduced here.

Suggested remedy: distinguish content equality from replication equality and add a metadata-only action that preserves the destination manifest under the established update protocol.
Resolve symlinks without dereferencing before deciding to materialize logical bytes, and specify account/container remapping semantics.
Acceptance: same bytes with changed user metadata, Content-Type, Content-Encoding, Content-Disposition, expiry, and symlink fields converge without unnecessary data transfer; dangling/cross-account links retain their declared semantics.
Probe: `TestAuditCopyPropagatesMetadataOnlyChange`.

### DS-10 — High: successful local uploads can clear recovery while cleanup is unresolved

Disposition: statically revalidated Grok P1-2; high confidence in the code path, no new fault-injection execution here.
Code: `internal/transfer/local/local.go:417`, `:425`, `:479`, `:508`; `internal/app/localtransferservice.go:250`, `:256`.
Oracle: K-XFER-002, durable cleanup/recovery intent in DESIGN §28.6 and §65.9.

Upload's staging cleanup breaks on listing errors and ignores DELETE errors.
It does not add cleanup notes to its Report.
If all live uploads otherwise verify, StartUpload clears the recovery rows without checking whether staging residue actually disappeared.
The committed-record write is also ignored in the local path.
The outcome can therefore be `complete` with unrecoverable-from-records staging residue, unlike Copy's explicit note-free reconciliation gate.

Suggested remedy: reuse one cleanup result and durable-state policy across local upload and Copy; clear intent only after each created identity is accounted for or retained with a durable reason.
Acceptance: fail the first and last staging DELETE, truncate the staging listing, reject the committed-record write, and crash between commit and cleanup; the next process retains and reports every unresolved identity.

Current remediation status: P04 has removed the final staging sweep and stopped successful uploads from clearing their recovery rows.
The [8 September bound-service reproductions](../../experiments/2026-09-08-upload-recovery.md) verify retained facts on mock Swift and SAIO, including an in-process database reopen.
Before the follow-up fix, they also reproduced ignored committed-record and staging-delete errors: the job was incorrectly `complete`, and a rejected committed record did not prevent staging deletion.
[ADR 0023](../../adr/0023-upload-post-commit-errors.md) defines the independently reviewed error-reporting and cleanup-admission follow-up.
Its [maintained implementation and regressions](../../experiments/2026-09-08-upload-recovery.md#maintained-implementation-and-evidence) now retain staging after committed-record refusal and report post-commit issues visibly without inventing failed transfers.
Independent implementation review and all five PR/master checks passed for [PR52](https://github.com/csmart/aeronautes/pull/52); the [delivery record](../../experiments/2026-09-08-upload-recovery.md#delivery) verifies the exact reviewed/merged tree.
DS-10 and the broader P12/P21 gates remain open, including interruption recovery, complete durable identities, stronger verification and shared cleanup policy.
The original record-clearing description above is the audit-time defect, not the current implementation.

### DS-11 — Medium: local upload bypasses the destination chunk-size cap

Disposition: new explicit parity finding, directly reproduced through StartUpload.
Code: `internal/app/localtransferservice.go:210`, `:213`; compare `internal/app/transfersettings.go:124` and Copy's engine validation.
Oracle: K-XFER-004 and the destination `/info` contract.

StartUpload only calls the syntax/range `Validate` method before creating its local engine.
It does not apply Copy/Mirror's destination maximum-file-size checks or default cap.
The service probe advertised a 1 MiB cluster maximum and requested an 8 MiB chunk.
StartUpload accepted the request and recorded `chunkSize:"8Mi"`; the tiny test file completed, proving the invalid setting passed admission without using a large fixture.
Real uploads large enough to segment can then issue requests the cluster cannot store.
The same omission admits explicit very small chunk values which Copy's semantic validation rejects.

Suggested remedy: share resolved settings and destination capability admission across every upload-producing path before preview and start, and record the actual capped default.
Acceptance: explicit above-max chunks refuse before job creation; an unset default is capped for a small-limit cluster; the visible effective settings match engine options for local upload, Copy, and Mirror.
Probe: `TestAuditUploadChecksDestinationChunkLimit`.

Current disposition: [PR73](https://github.com/csmart/aeronautes/pull/73) delivered shared chunk admission and truthful resolved settings for supported local-upload and Copy preview/start paths.
Explicit values below the minimum or above the destination maximum refuse before engine/job creation; unset values use the capped default.
The [maintained regression and delivery record](../../experiments/2026-09-09-upload-limits.md#protected-delivery) preserves the original failing probes, passing controls, independent review and all five PR/master checks.
Mirror starts remain refused, and P21's corrupt/incomplete DLO checks plus P15/P16 verification and cleanup authority remain blocking.
The preceding defect description remains the audit-time before evidence.

### DS-12 — High at scale: several high-volume paths accumulate job-wide namespaces

Disposition: statically revalidated K-PERF-001 gap; no memory benchmark was run.
Code: `internal/transfer/local/local.go:152`, `:153`, `:250`, `:371`, `:561`; `internal/transfer/copy/executor.go:106`, `:111`, `:120`, `:137`; `internal/transfer/copy/verify.go:248`.
Oracle: K-PERF-001 and DESIGN §15/§37.

The core comparison is a good streaming merge-join, but surrounding execution is not uniformly bounded.
Download preflight retains all names, items, case-fold groups, and a file index, then execution retains another name slice and map.
A whole selected download runs as one child rather than bounded batches.
Copy retains every captured/committed large-object prefix, old overwritten DLO manifest, and conflict for the job's lifetime.
Segment recovery materializes every name in a prefix before deleting.
These grow with namespace/job size even though per-request and per-worker bounds are present.
A sufficiently large operator job can exhaust the workstation, create an abrupt interruption, or outlive token assumptions.

Suggested remedy: use durable streaming plan/identity indexes and paged result views, external sorting where complete collision detection requires it, bounded child batches, and streaming recovery work.
Acceptance: measure peak RSS and recoverable state for large synthetic namespaces, many large-object overwrites, all-conflict jobs, and large recovery prefixes; memory should follow configured page/batch bounds rather than total names.

### DS-13 — High: short provider pages can silently truncate plans and verification

Disposition: resolved by P22 on 7 September after independent review and protected PR/master delivery; the original audit statically revalidated W-13/R-16 without a live provider test.
Code: `internal/swiftcontrol/objects.go:112`, `internal/swiftcontrol/containers.go:63`.
Oracle: K-PERF-001, K-CON-001, provider pagination compatibility and completeness.

Both iterators stop when a page has fewer rows than the requested limit.
A deployment that caps or filters a nonterminal page below that requested limit silently hides the tail.
Copy's execution and fresh verification use the same iterator, so the missing tail can be absent from both and a job can incorrectly complete.
The independent system-test oracle already has stronger paging validation, but that does not fix the production iterator.

Suggested remedy: implement the accepted provider-compatible continuation contract with monotonic-marker and nonprogress guards, rather than treating a requested page size as proof of exhaustion.
Acceptance: short nonempty pages followed by data, eventual empty termination, repeated pages, backward markers, cancellation, and truncated HTTP bodies; both production iterator and independent oracle must agree on the complete namespace.

Follow-up reproduction on 7 September, before P22 implementation: four local HTTP cases on the PR42 source fail their intended assertions under the race detector.
Both container and object iterators return only the first of two names when the provider returns one name per page despite a larger requested limit.
Two object-response cases also return a row with no error: a complete JSON value inside a truncated HTTP body, and an invalid numeric field beside a valid timestamp.
The pinned library ignores errors while draining after its first JSON value, and date conversion can overwrite a prior decode error.
These are controlled local reproductions, not live provider observations.

P22's first response slice now refuses incomplete or malformed pages before publication, with [public-session, dependency and two-account system evidence](../../experiments/2026-09-07-listing-responses.md).
The subsequent [native continuation slice](../../experiments/2026-09-07-listing-continuation.md) also passes both original short-page probes, validates strict raw marker progress and literal scope, and preserves lazy stopping and cancellation.
Its controlled Browse, Find, preview and native-routed Copy/verification cases cover the previously hidden tail; all four original listing probes now pass.
At native-slice delivery, DS-13 remained open until mixed-engine execution/verification also covered the complete intended namespace across short pages.
The proposed correction and its separately reviewed implementation sequence are recorded in [ADR 0021](../../adr/0021-complete-listing-pages.md).

The continuation prototype exposed an additional [controlled mixed-engine omission](../../experiments/2026-09-07-short-page-copy.md).
With all selected listing requests capped to one row, the native plan sees three names but pinned rclone omits one of its two planned files; Aeronautes detects the missing stage and reports a partial job.
Removing only the proxy cap lets the same mixed Copy complete on both mock Swift and SAIO.
The native continuation slice must preserve this limitation as explicit evidence, alongside complete native-routed Copy and preview cases; it must not silently restrict the cap to native requests or credit the mixed partial as success.
Investigate and review the engine decision separately before changing it; native-only success does not satisfy the full P22 acceptance.
The [selected-set follow-up experiment](../../experiments/2026-09-07-selected-set-copy.md) passes both original complete-Copy assertions when only `--no-traverse` is added, and fails both again when that option is removed.
[ADR 0022](../../adr/0022-complete-rclone-selection.md) records the shared-runner correction and its required remote/local regression evidence.
After independent design review, that correction now has [maintained complete mixed-engine, large-object and local-transfer evidence](../../experiments/2026-09-07-selected-set-copy.md#maintained-implementation-and-evidence), with eight parent/removed-option assertion failures and twelve corrected passes.
The engine implementation passed independent local review and all five [PR46 checks](https://github.com/csmart/aeronautes/actions/runs/34086202150) before its protected merge; all five [resulting master checks](https://github.com/csmart/aeronautes/actions/runs/34086884737) passed.
Master `c7b669e422465754e9674f51e3e364986c905277` exactly matches the reviewed source tree.
DS-13 is closed for the defined pagination/completeness contract; the DLO planned-byte estimate remains P13 work, and the wider safety and valuable-data release hold remains.

## Product-contract and evidence gaps

The authoritative design needs reconciliation before architecture work.
DESIGN §28.6(b) says account-wide reference scans are not required and are forbidden by the performance rules, while the later §65.9/§65.10 status requires live reverse-reference proof before automatic segment deletion.
The current force-only inspector cleanup is deliberately stricter than the old §34 body, and should remain so until the governing decision is explicit.
A scan still cannot establish permanence against uncoordinated future writers.
Record the accepted guarantees and containment in an ADR before implementing a new ownership model.

Modern `object_versioning` is intentionally display-only, so lack of a full editor is a supported-scope gap rather than a newly discovered defect.
Its mode is omitted from the editable fingerprint at `internal/swiftcontrol/containerhead.go:136`, and `currentVersioningMode` treats a failed preliminary read as unknown/editable at `internal/app/mutateservice.go:333`.
Those are concrete facts the future versioning mutation design must handle fail-closed.
Existing SAIO tests subsequently passed in the completed local run.
The complete legacy `versioned_writes`, hidden archive, delete/restore, versioned-stage cleanup and interruption matrix remains unvalidated; this audit added no real-server versioning fault variants.
Do not extend a green mock suite into a versioned-destination safety claim.

The bounded task did not independently assess the credential tuple, returned-endpoint policy, native bridge capability issuance, release attestation, or frontend dialog/selection identity; those are separate audit workstreams.
The findings here do not imply that those known W/R issues have been resolved.

## Positive evidence

The engine boundary is unusually explicit and already enforced below callers: rclone writes staging destinations, while Aeronautes performs live commits and deletion.
The current planned-new commit path uses conditional PUT even for plain staged content, avoiding the previously documented source-conditional COPY trap.
The tests `TestEngineNeverReceivesLiveNames`, `TestPlannedNewCommitsAreConditional`, and `TestPlainCommitRacerSurvives` directly encode those guarantees.

Recursive deletion uses bounded native bulk/parallel executors and does not request recursive SLO deletion.
`TestSharedSegmentsSurviveManifestDeletion`, `TestPlanToDeleteRaceIsManifestOnly`, and `TestSegmentDeleteNeverUsesRecursiveMultipart` cover preservation of shared backing objects across manifest-only deletion.
Force is required for segment cleanup even when local ownership evidence exists, and `TestProvenSegmentsStillNeedForce` prevents restoring the weaker authorization by mistake.

The plan uses a streaming sorted merge-join and explicitly rejects non-deep or out-of-order inputs.
Listings do not HEAD every row merely to render them.
Unsafe rclone object names have an explicit native route with hostile-name and real-engine integration tests.
Metadata updates assemble the complete remaining object metadata set, preserve DLO headers, and return conflict/verification outcomes.
Native local placement uses hard-link no-replace, which protects an existing final leaf even though parent traversal still needs fixing.

Copy persists staging before writes, streams captured-prefix records, and refuses DLO commit when committing-intent persistence fails.
The independent raw-HTTP system oracle checks content and collateral state separately from the production parsers.
Those are useful foundations for fixes rather than reasons to discard the design.
The initial shared baseline reported 299 passing top-level tests, 33 skipped and 75.8% statements without SAIO.
The completed local two-account run then passed all 332 top-level tests with zero skips and 81.7% statements, including the explicit pinned-engine measurement lane.
Those existing-suite results do not close the separate defect reproductions or missing system scenarios; see [verification.md](verification.md).

## Sequenced remediation recommendations

1. **Contain the evidenced irreversible paths and settle the contract.**
   Keep the product unreleased for valuable-data safety claims; retain orphan segments automatically, preserve force-only cleanup, and define an enforceable concurrency policy for overwrite and Mirror deletion.
   Reconcile the conflicting ownership paragraphs and distinguish data safety from operator acknowledgement in an ADR.
2. **Land the minimum deterministic fault/barrier substrate with actual reproductions.**
   Start with DS-01–04 plus the parent-directory and cancellation cases above, not an unconstrained generic framework.
   Expose typed barriers before/after final HEAD, live commit, segment cleanup, filesystem placement, and durable writes.
   Promote the scratch probes into independent whole-account/filesystem assertions through the real service boundary.
3. **Build a durable typed intent/outcome protocol and bind reviewed plans.**
   Carry bookmark/session generation, source identity, destination identity, operation scope, expected postcondition, and verification strength.
   Persist exact outcomes and unresolved response states; make all callers use the same cleanup/persistence policy.
   Server-issued plan capabilities address stale preview/force issues from the other audit streams, while the remote concurrency policy addresses DS-02/03.
4. **Implement strong content verification and replication fidelity.**
   Fix current-state binding of fingerprints and ordered inventory/digest verification, then cover local upload/download and metadata-only/symlink paths with the same contract.
   Mirror deletion must depend on genuinely verified copy outcomes.
5. **Complete local namespace, cancellation, and settings parity.**
   Bind filesystem root identity, handle parent symlinks/reparse points safely, apply one chosen normalization policy consistently, stop at cancellation boundaries, preserve exact partial results, and share destination chunk admission.
   Run actual Linux/macOS/Windows tests on supported architectures and filesystems.
6. **Finish provider/versioning/scale coverage and enable hard evidence gates.**
   Test short-page providers and both versioning modes, remove job-wide memory accumulation, and prove bounded child/token behaviour.
   Require the real two-account Swift suite, crash/restart subprocess tests, platform tests, and scenario nonregression in CI before making a valuable-data release claim.

The fault/barrier harness is therefore part of this work, specifically as the verification backbone for the first architecture fixes.
It does not replace those fixes, and the larger full-app scenario matrix should continue alongside bounded product improvements rather than delay all remediation until the matrix is complete.
