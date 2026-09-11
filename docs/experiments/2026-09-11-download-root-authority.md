# Download root authority after admission

## Scope and oracle

This experiment settles the bounded P18 question that [ADR 0034](../adr/0034-download-destination-admission.md) left open: what authority the download path holds over the selected directory once admission has finished.
PR77 refuses evidenced existing destination hazards before Start and before the executor's first effect.
The remaining gap is authority through the effect itself, from the picker's answer to the last cleanup step.

The oracle is K-XFER-011, DESIGN §27.5, ADRs 0032 to 0034 and the P18 acceptance row: existing and raced parent symlinks or reparse points produce no outside delta for engine, native and marker downloads, and final-leaf no-replace protection remains.
The exact picker spelling, the accepted initial selected-root alias, complete-selection admission refusal, checked closure, exact outcomes and conservative cleanup are preserved facts that any correction must keep.

The first part of this record establishes the defect and proposes a reviewable contract; the second part, from the section "Correction delivered" onward, records the correction and its evidence.
It makes no claim about native macOS or Windows behaviour, the valuable-data hold or any DS-06 remainder.

## Source finding

The selection store in `internal/app/selections.go` keeps the picker's answer as a pathname string behind an opaque token, evicted after sixty-four selections, with no handle and no recorded identity.
`PreflightDownload` and `StartDownload` in `internal/app/localtransferservice.go` each resolve the token to that pathname again, and Start passes the pathname to the executor.

`checkDownloadDestination` in `internal/transfer/local/download_destination.go` opens an `os.Root` on the pathname, inspects every planned component beneath it, and closes the root before returning.
The handle therefore lives only for admission.
Every later effect in `internal/transfer/local/download_execute.go` resolves the pathname afresh: the stage directory is created with `os.Mkdir`, the engine receives the stage pathname, placement uses `os.Stat`, `os.MkdirAll` and `os.Link` on pathnames, markers use `os.MkdirAll`, and native writes use `os.MkdirAll`, `os.OpenFile` and `os.Link` on pathnames.
Stage cleanup in `download_stage.go` walks the stage pathname and reports success when that pathname names nothing, even if the real stage directory exists elsewhere.

`os.OpenRoot` follows symbolic links in the directory name it is given, and the delivered admission cannot tell a pick-time alias from a later replacement of the same pathname.
The item records keep pathnames as well, so a redirected placement is recorded at the captured path while the bytes are elsewhere.

## Deterministic boundaries

No timing race is needed; each case replaces exactly one entry at one boundary the production code already exposes.

- The executor's progress callback runs between two items, after the first item's observation and before the second item's first effect.
- The engine boundary runs after the child stages and before the executor places, through the executor's engine interface and, at the service, through the existing stage boundary.
- A held Swift response, served by the fixture proxy's response barrier, delays a native item's source read until the test has acted, before that item's first local effect.
- The gap between the operator's preflight call and start call is two separate bridge calls and needs no fixture at all.

Two replacements are exercised: the selected root is renamed away and a symbolic link to an outside directory is left at the selected pathname, or a symbolic link to the outside directory is planted at a parent path that admission saw as absent or as an ordinary directory.

## Before evidence on the executor

The [archived executor probe](probes/2026-09-11-download-root-authority_test.go.txt) drives `local.Download` with a filesystem-backed test engine and the in-process mock, asserting only the invariants: nothing outside the selected directory changes, every reported success exists inside the real selected directory without any link, and stage or temporary residue is reported.
It ran under the race detector against unchanged production, source `1b520ba2a80f281d5945250548137be7498a1f02`, and finished in 0.098 seconds.

| Case | Replacement | Reported | Observed |
| --- | --- | --- | --- |
| engine_parent_after_stage | parent planted after staging | done 1, no failures | `outside/payload.bin` written, nothing under the selected parent |
| engine_existing_parent_after_stage | existing parent moved away and replaced | done 1, no failures | `outside/payload.bin` written, the original parent and its contents moved aside |
| engine_root_after_stage | root renamed and replaced | done 0, one failure "missing after download (silent drop?)", cleanup issues 0 | staged bytes orphaned under the renamed root's stage directory, no outside delta |
| native_root_between_items | root replaced after the first item | done 2, no failures | `outside/b.bin` written, the first item inside the renamed root |
| native_parent_between_items | parent planted after the first item | done 2, no failures | `outside/b.bin` written |
| marker_root_between_items | root replaced after the first item | done 2, no failures | `outside/m` created |
| marker_parent_between_items | parent planted after the first item | done 2, no failures | `outside/m` created |
| engine_control, native_control, marker_control | none | complete | everything inside the selected directory |

Seven replacement leaves failed the invariants and three controls passed.

## Before evidence on the service

The [archived service probe](probes/2026-09-11-download-root-authority-service_test.go.txt) drives the real service through the picker token, preflight, start, the queued job, the durable records and both owned remote namespaces.
It ran under the race detector against unchanged production on the in-process mock in 4.57 seconds and on the owned two-account real Swift fixture in 10.80 seconds, with the pinned rclone 1.74.3 on the engine route.
The [manifest](2026-09-11-download-root-authority-probes.json) records the archived probe hashes, the producer commands, exits and log digests.

| Case | Boundary and replacement | Job | Observed |
| --- | --- | --- | --- |
| engine_parent_after_stage | stage boundary, parent planted | complete, 1 copied and verified | `outside/payload.bin` |
| engine_root_after_stage | stage boundary, root replaced | partial, 1 failed and unresolved, "missing after download (silent drop?)" | stage directory and bytes orphaned under the renamed root, no cleanup problem reported |
| native_parent_during_get | held read, parent planted | complete, 1 copied and verified | `outside/b.bin` |
| native_root_during_get | held read, root replaced | complete, 1 copied and verified | `outside/b.bin` |
| marker_parent_during_get | held read, parent planted | complete, 2 copied and verified | `outside/m` |
| marker_root_during_get | held read, root replaced | complete, 2 copied and verified | `outside/a.bin` and `outside/m` |
| engine_root_before_start | between preflight and start, root replaced | complete, 1 copied and verified | `outside/payload.bin` |
| native_root_before_start | between preflight and start, root replaced | complete, 1 copied and verified | `outside/b.bin` |
| engine_control, native_control, marker_control | none | complete | everything inside the selected directory |

Both providers agree on the verdict, state, counters, outside delta and residue of every case.
Eight replacement leaves failed and three controls passed on each provider, so the complete before evidence is twenty-three failing leaves and nine passing controls.
Every held read was served exactly once, no case wrote to either remote account, and no case changed unrelated remote objects.

## What the evidence establishes

The download path's authority over the selected directory is a pathname from the picker's answer to the last cleanup step.
A root or parent replaced after admission redirects native writes, engine placement and marker creation outside the selected directory, and the job, its counters, its item records and its audit report success.

The renamed-root engine case is a truthful-reporting failure as well as a placement failure.
The engine wrote the staged bytes into the real directory at its new location, placement looked for them through the replaced pathname, the item was diagnosed as a silent drop, and cleanup reported no problem for a stage directory it never reached.

The two bridge cases show that the operator-visible token binds no identity across the two bridge calls, so a replacement between preflight and start is admitted by the re-run preflight and executed.
The same shapes reproduce on the mock and on real Swift, so the defect lives entirely in the local effect path and does not depend on the source.

## What the evidence does not establish

These are Linux amd64 observations with an injected backend picker.
They do not exercise reparse points, mount boundaries, network filesystems, or native macOS and Windows dialogs and filesystems.
The replacing actor is a same-user process with write access to the selected directory or its parent; nothing here estimates how likely that is, and nothing here changes the DS-06 namespace remainder.

## Authority contract

The contract below was proposed for the design sequence and an ADR and reviewed before any production change.
Each decision states the proposed answer and marks where an operator or platform decision is needed; the section "Decisions taken" records how the delivered correction resolves each one and which confirmations are still owed.

### 1. Capture and lease

The picker boundary captures identity.
When the picker returns a directory, the service opens it as an `os.Root`, verifies it is a directory, and stores the handle together with the display pathname behind the token.
The handle is the authority; the pathname is display and record text only.
Preflight through a token uses the token's handle, Start transfers the handle to the job, and the job owns it until its terminal outcome and cleanup complete.
Eviction closes only handles that no job owns, and a token whose handle a job owns cannot start a second job.
Decision to confirm: one selection starts one job, and a new job needs a new pick.

### 2. Renamed roots

With a held handle, effects continue into the real directory at its new location, which is the directory the operator picked.
Before the first effect and after the last, the executor opens the captured pathname afresh and compares its identity with the held handle.
A mismatch is recorded as a job-level fact, the selected directory was relocated or replaced during the job, and item records keep the captured spelling.
Nothing is ever written through the pathname.
Decision to confirm: continue into the held directory and report, rather than refuse further effects, because a benign rename would otherwise produce a partial outcome.

### 3. Relocated descendants and planted parents

Every effect beneath the root re-inspects each component through the held handle at effect time and refuses links and non-directories, preserving the delivered descendant-link policy.
Because `os.Root` follows symbolic links that stay within the root, the implementation descends one component at a time, opens each ordinary directory by handle, and compares the opened directory's identity with the inspected entry.
A mismatch refuses the effect.
Directory creation, temporary creation, linking and removal use the handle, never `os.MkdirAll` on a pathname.

### 4. Mount boundaries and reparse points

`os.Root` does not stop traversal across mount points.
Proposed: a component whose device differs from the root's device is refused as not an ordinary directory, and Windows reparse points stay refused.
Decision to confirm: bind mounts inside a selected directory are refused rather than followed.

### 5. Engine destination authority

The child engine receives a pathname and cannot use a handle.
Proposed on every platform: the stage directory is created and opened through the held root before the engine runs, its identity is compared afterwards, staged files are read only through the held root, and placement links only what the root can see.
A stage redirected during the engine run therefore yields a failed item with a truthful diagnosis and no placement outside, never a false success.
Proposed additionally on Linux: the engine is given a descriptor-anchored destination so its own path resolution starts at the held stage directory, which must be proved by an explicit ancestor-rename experiment against the pinned engine before it is relied on.
Residual on platforms without an anchored destination: the engine may still write the transferred bytes into a redirected stage location.
Decision required: whether that residual is acceptable on macOS and Windows for the first milestone, with the anchor on Linux, or whether the engine route stays gated until an anchor exists there.

A private stage outside the selected directory was considered and rejected, because publication by hard link needs the same filesystem and a copy would break atomic no-replace publication.

### 6. Publication of the verified file

Publication opens the staged file through the held root, verifies it, and links it to the target through the root with no-replace semantics.
After a successful link, the linked target's identity is compared with the verified file; a mismatch means the staged file was substituted inside the root between verification and link, and the executor removes the link it created and reports failure.
A link refused because the target exists keeps the existing target, exactly as today.

### 7. Cleanup authority

Cleanup removes only entries the executor created, only through the held handle, and only after inspecting each entry through the handle.
Directory removal uses a remove-directory-only primitive relative to the handle where the platform provides one, so a file placed at a stage directory's name can never be unlinked.
A stage the executor cannot reach through the handle is reported as retained with its location relative to the held directory, never as cleaned.

### 8. Supported filesystem envelope

The contract covers local filesystems on the six targets through `os.Root`, whose documented guarantees this correction relies on.
Native macOS and Windows acceptance evidence is TEST-04 work and stays a gap until produced.
No claim is made for network filesystems, WASI or plan9.

### 9. Truthful reporting

A placement that cannot find its staged file must report the stage identity mismatch when there is one, not a silent drop.
The job records when the captured pathname no longer names the held directory.
Cleanup never reports success for a directory it did not inspect.

## Required proof

The archived probes become maintained regressions at the executor and service boundaries on both providers, and must pass after the correction.
Each authority component needs a deliberately broken control that fails only its own case: a pathname fallback for the root, skipped per-component re-inspection for planted parents, a skipped stage identity comparison for the engine, and pathname cleanup for residue.
The Linux engine anchor needs its own experiment showing the pinned engine writes into the held stage directory after an ancestor rename, retained as a failed producer if it does not.
Exact item records, public pages, audit facts, the complete owned local tree with outside and unrelated sentinels, and both remote namespaces are compared in every case.
The full race suite runs with inventory reconciliation, and the change carries vet, gofmt, the Windows and macOS vets and internal builds, and tidy.

## Correction delivered

The correction lands in two commits on top of the plan.
The first, `hold the selected download directory by handle`, changes the local package and the engine runner.
The second, `own the picked directory handle per download job`, changes the selection store and the service and adds the service regression.

`DestinationRoot` in `internal/transfer/local/download_root.go` binds the picked directory as an `os.Root` beside its exact spelling, derives further handles from the held one, reports when the spelling no longer names it, and reports when the directory has been deleted.
Every path beneath the root is opened one component at a time through the parent handle: each component is inspected without following links, a link, a non-directory or an entry on another device is refused, on Linux a mount root is refused by attribute, an absent component is created only through the parent handle, and the opened directory must be the inspected entry.
The stage is created and bound through the held root before the engine runs and its identity is compared afterwards; staged files are read only through the stage handle; publication links by directory descriptor on Unix and by root-relative names on Windows and then confirms that the target names the verified file; cleanup removes only planned empty directories through handles with a primitive that removes only a directory, and the stage itself by name only while the name still denotes it.
Native writes create their temporary through the parent handle, take the verified file's identity before the handles close, publish it the same way, and remove the temporary only while the entry is still that file.
Directory markers are created through the parent handle and observed without following links.

On Linux the runner's `CopyInto` hands the child an inherited descriptor for the held stage directory as its destination, so the child's own path resolution starts at the held directory at every open.
Where the platform provides no anchor, the engine route is refused before any stage exists, because a child given a pathname cannot be contained during its run and K-XFER-011 asks for containment rather than detection; the native route is unaffected.
On Linux an in-process test engine that takes a pathname is still refused a pathname that has stopped naming the directory picked.

The selection store in `internal/app/selections.go` opens the handle at the pick and keeps the spelling; `leaseDir` derives a handle for each preflight and each job, and eviction closes only the pick's own handle.
A pick that cannot be held keeps its spelling with no handle, so the complete preflight report refuses it with its reason exactly as before.
The service passes the held handle to the preflight and to the job, closes it together with the job's pins after the whole body has returned, keeps the stage boundary on the anchored route, and turns a displaced pathname into a partial outcome with an audit summary.
Item records keep the picker spelling for the destination and staging paths.

## After evidence on the executor

The archived executor probe became the maintained regression `internal/transfer/local/download_root_authority_test.go`, with exact expected outcomes per case and four cases added: a stage replaced while the engine ran, the root replaced or deleted before the first effect, and the engine route replaced before start.
It runs under the race detector with a filesystem-backed test engine that takes a pathname, like the child on platforms without a descriptor anchor.

| Case | Replacement | Outcome | Observed |
| --- | --- | --- | --- |
| engine_parent_after_stage | parent planted after staging | the item fails with the component refusal, cleanup reports the retained stage | staged bytes retained under the stage, nothing outside |
| engine_existing_parent_after_stage | existing parent moved away and replaced | as above | the moved parent keeps its contents |
| engine_root_after_stage | root renamed and replaced after staging | done 1, displacement recorded after the last effect, cleanup clean | `moved/payload.bin`, no stage residue, nothing outside |
| engine_stage_replaced | stage directory moved away and replaced while the engine ran | the item fails with the stage replacement diagnosis, cleanup reports the retained stage | staged bytes retained where the stage was moved, nothing outside |
| native_root_between_items | root replaced after the first item | done 2, displacement recorded | `moved/a.bin` and `moved/b.bin` |
| native_parent_between_items | parent planted after the first item | done 1, the second item fails with the component refusal | nothing outside, no temporary residue |
| marker_root_between_items | root replaced after the first item | done 2, displacement recorded | `moved/a.bin` and `moved/m` |
| marker_parent_between_items | parent planted after the first item | done 1, the marker fails with the component refusal | nothing outside |
| engine_root_before_start | root replaced before the first effect | the item fails because the engine can only be given a pathname, displacement recorded before the first effect | no stage created, nothing outside |
| native_root_before_start | root replaced before the first effect | done 2, displacement recorded before the first effect | `moved/a.bin` and `moved/b.bin` |
| deleted_root_before_start | root deleted before the first effect | refused before any effect, the engine never ran | nothing written anywhere |
| engine_control, native_control, marker_control | none | complete | everything inside the selected directory |

Fourteen leaves pass.
The root helpers, the publication identity check, the removal primitives and the Linux mount-root refusal have their own tests in `download_root_test.go` and `download_root_linux_test.go`.

## Deliberately broken guards

Each authority component was broken on its own in a private copy and the maintained executor regression rerun; every guard failed only the cases its component protects and compiled cleanly.

| Guard | Mutation | Failing cases |
| --- | --- | --- |
| pathname root | the walk opens the captured pathname again instead of deriving from the held handle | engine_root_after_stage, marker_root_between_items, native_root_before_start, native_root_between_items |
| skipped component inspection | the ordinary-directory refusal is removed | engine_parent_after_stage, engine_existing_parent_after_stage, native_parent_between_items, marker_parent_between_items |
| skipped stage identity | the stage identity comparison after the engine is removed | engine_stage_replaced |
| pathname cleanup | the stage is removed by pathname and an absent name counts as cleaned | engine_root_after_stage |

The engine bridge case does not appear under the pathname-root guard because the pathname engine is refused before that guard's code runs.

## After evidence on the service

The archived service probe became the maintained regression `internal/systemtest/download_root_authority_test.go` with exact outcomes per case, on the in-process mock and on the owned two-account real Swift fixture with the pinned rclone 1.74.3 on the engine route.

| Case | Boundary and replacement | Job | Observed |
| --- | --- | --- | --- |
| engine_parent_after_stage | stage boundary, parent planted | partial, 0 copied, the item failed with the component refusal, stage cleanup reported | staged bytes retained under the stage, nothing outside |
| engine_root_after_stage | stage boundary, root replaced | partial, 1 copied and verified, displacement reported after the last effect | `moved/payload.bin`, no residue |
| engine_stage_relocated_during_get | held engine read, the stage this executor created moved out and replaced | partial, 0 copied, the item failed with the stage replacement diagnosis, stage cleanup reported the retained stage | the child's bytes in `moved-stage/payload.bin`, the directory this executor created, nothing outside |
| native_parent_during_get | held read, parent planted | partial, 0 copied, the item failed with the component refusal | nothing outside |
| native_root_during_get | held read, root replaced | partial, 1 copied and verified, displacement reported | `moved/b.bin` |
| marker_parent_during_get | held read, parent planted | partial, 1 copied and verified, the marker failed with the component refusal | `selected/a.bin`, nothing outside |
| marker_root_during_get | held read, root replaced | partial, 2 copied and verified, displacement reported | `moved/a.bin` and `moved/m` |
| engine_root_before_start | between preflight and start, root replaced | partial, 1 copied and verified through the descriptor anchor, displacement reported before the first effect | `moved/payload.bin` |
| native_root_before_start | between preflight and start, root replaced | partial, 1 copied and verified, displacement reported before the first effect | `moved/b.bin` |
| engine_control, native_control, marker_control | none | complete | everything inside the selected directory |

Both providers agree on every case, twelve leaves pass on each, every held read was served exactly once, no case wrote to either remote account, and no case changed unrelated remote objects.
The relocated-stage case records what the anchor means: the child keeps writing into the directory this executor created wherever a same-user process moved it, never through a name that process controls, and the executor fails the items, retains the stage and reports it.
Every engine case expects the descriptor anchor; on a platform without it the regression expects the engine route to be refused with its reason before any stage exists, controls included, and the boundary never to run.

## Engine anchor experiment

Before the anchor was relied on, the pinned engine was run locally against a destination given as the inherited descriptor of a held directory, with the directory's ancestor renamed and a link to an outside directory planted at the old name.
With a pathname destination the same rename sent the whole copy into the outside directory.
With the descriptor destination and the rename before the copy, every file landed in the held directory.
With a rate-limited eight-second copy and the rename at two seconds, the partial file was already growing in the held directory at four seconds and the outside directory stayed empty.
With two files copied one after the other and the rename between them, the second file, opened after the rename, landed in the held directory and the outside directory stayed empty, which rules out a single resolution of the descriptor path at start.
The commands, outputs and hashes are retained privately as the engine-anchor producer.

## Decisions taken

1. Capture and lease: delivered with derivation rather than a single job per pick.
   One pick can start any number of jobs, each owning a derived handle, and eviction never touches a running job.
   Operator confirmation is still owed that a pick may be reused.
2. Renamed roots: delivered as continue and report.
   The job ends partial with the displacement in its message and audit summary, because the records keep the picked spelling.
   A deleted directory is refused before any effect.
   Operator confirmation is still owed that partial is the right outcome for a benign rename.
3. Relocated descendants and planted parents: delivered as proposed.
4. Mount boundaries and reparse points: delivered as device comparison everywhere, mount-root refusal by attribute on Linux, and the existing reparse refusal on Windows.
   Operator confirmation is still owed that mounts inside a selected directory are refused rather than followed.
5. Engine destination authority: delivered with the descriptor anchor on Linux, stage identity verification everywhere, and the engine route refused where no anchor exists.
   A child given a pathname cannot be contained during its run, and K-XFER-011 asks for containment rather than detection, so macOS and Windows keep the native route and refuse engine items with their reason until the operator decides otherwise or an anchor exists there.
   Operator confirmation is still owed for that gate; lifting it is a one-line platform decision with the residual recorded.
6. Publication, 7. cleanup, 8. envelope and 9. reporting: delivered as proposed.

## Verification

Focused producers on the committed source, all under the race detector: the executor regression with the root unit tests (23 leaves passing), the service regression on the in-process mock (12 leaves) and on the real two-account Swift fixture (12 leaves), and the selection lifetime and engine construction guard tests in the app package (6 leaves).
The four deliberately broken guards were run against the executor regression before the commits and are recorded above.
The full race suite ran once on the committed source with the pinned engine directory and the real Swift fixture configured, exit 0: 3800 distinct names, 3531 leaves of which 3530 passing, 0 failing and 1 skipped, 3799 passing events including parents, 22 packages passing.
A leaf is a recorded name that is not a slash-separated prefix of another recorded name in the same package.
The change carries gofmt, vet on Linux, Windows and macOS, internal builds for Windows, macOS on both architectures and FreeBSD, and no module change.
The [manifest](2026-09-11-download-root-authority-probes.json) records every producer command, exit, count and digest, the broken-guard outcomes and the engine anchor producers.

## Proposed shared-document text

The following text is proposed for integration into the design sequence list by its integrator, after the sequence that delivered PR81 and PR91, and for an ADR whose number is allocated at creation.

The next bounded selected-root authority correction binds the identity of the picked directory from the picker through every download effect.
The picker token records a pathname today, admission opens and closes a temporary handle, and staging, placement, native writes, markers and cleanup resolve the pathname again, so a root or parent replaced after admission redirects effects outside the selected directory with reported success; the reproduction is deterministic at three production boundaries on both owned providers.
K-XFER-011, §27.5, ADRs 0032 to 0034 and the P18 acceptance require no outside delta for existing and raced parent or root replacement across the engine, native and marker routes while final-leaf no-replace protection remains.

1. Review this sequence and the dedicated experiment, including the observed before matrix and the decisions it surfaces, before any production change.
2. Record the authority contract in an ADR: identity captured at the picker boundary and owned by the job, effects only through the held handle with per-component re-inspection and no link following, engine staging anchored to the held stage directory where the platform allows and verified through the handle everywhere, publication and cleanup through the handle, and truthful reporting when the captured pathname no longer names the held directory.
3. Implement capture and ownership in the selection store and the service, then handle-based staging, placement, native writes, markers and cleanup in the local package, keeping every delivered admission rule.
4. Add maintained regressions from the archived probes at the executor and service boundaries on both providers, with a deliberately broken control for each authority component that fails only its own case, and prove the engine anchor on Linux with an explicit ancestor-rename experiment before relying on it.
5. Record the complete compatibility, inventory and scenario verification, obtain independent review, and deliver only the reviewed current-base source through all five protected checks.

Reparse points, mount boundaries and native macOS and Windows acceptance remain separate P18 and TEST-04 work; the engine residual on platforms without a handle-anchored destination is recorded, not cleared.
This bounded correction does not close P18, P19, P20 or the valuable-data hold.

## Remaining limits

Linux amd64 with an injected picker is the only observed environment; the Windows and macOS code paths are compiled and vetted, not executed, and the engine route is refused there until an anchor exists.
The anchor binds the child to the directory this executor created, not to a position beneath the selected directory: a same-user process that moves the stage out during the run moves the child's bytes with it, the items fail and the stage is retained and reported, but those bytes are then outside the selected directory's current tree.
A descendant handle is held only across one effect, so a directory moved out between its inspection and that effect receives at most that one effect; the window is the same one every handle-based implementation has and is not closed here.
The removal of a link after a publication identity mismatch targets the name this executor linked immediately before, and a replacement landing between the identity check and the removal would be unlinked instead; the window is recorded, not closed.
A bind mount of the same filesystem is refused only on Linux kernels that report mount roots; elsewhere only a mount of another filesystem is refused.
The replacing actor is a same-user process with write access to the selected directory or its parent, and nothing here estimates how likely that is.
The confirmations listed under "Decisions taken" are owed by the operator; the delivered behaviour is the proposed answer in each case.
P18, P19, P20, P30, TEST-04 and the valuable-data hold remain open.
