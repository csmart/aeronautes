# ADR 0035 — download root authority through the held handle

**Status:** Proposed; the product choices marked below await the operator, and the contract awaits independent review before it is accepted.
**Scope:** Bind the identity of the picked directory from the picker through every download effect, so a root, parent or stage replaced after admission cannot redirect an effect through a pathname; native macOS and Windows acceptance remains TEST-04 work.
The before evidence was reproduced deterministically on both owned providers before this contract was proposed, and the correction's evidence is recorded in the same experiment.

## Problem and oracle

[ADR 0034](0034-download-destination-admission.md) admits the complete download selection against the existing state of the selected directory through a temporary handle and closes it.
Every later effect resolved the captured pathname again: the picker token stored a pathname, staging created the stage by pathname, the engine received the stage pathname, native writes created their temporaries by pathname, directory markers used a pathname, publication linked by pathname and cleanup walked the stage pathname.
The [experiment](../experiments/2026-09-11-download-root-authority.md) reproduces a root or parent replaced after admission redirecting native writes, engine placement and markers outside the selected directory with reported success, at three production boundaries on both owned providers.

The oracle is [K-XFER-011](../REQUIREMENTS.md), DESIGN §27.5, ADRs [0032](0032-download-placement-prerequisites.md) to [0034](0034-download-destination-admission.md) and the P18 acceptance: existing and raced parent or root replacement produces no outside delta for engine, native and marker downloads while final-leaf no-replace protection remains.
Containment is required, not detection: a route that cannot keep its writes inside the directory picked is refused rather than checked afterwards.
Exact picker spelling, the accepted initial selected-root alias, complete-selection admission refusal, checked closure, exact outcomes and conservative cleanup are preserved.

## Decision

1. Capture and lease.
   The picker boundary captures identity: the picked directory is opened as an `os.Root` at the pick and held behind the token beside the exact picker spelling.
   The handle is the authority; the spelling is display and record text only.
   Each preflight and each job leases a handle derived from the held one and owns it until it closes it, so one pick can start any number of jobs; eviction closes only the pick's own handle.
   A pick that cannot be held keeps its spelling with no handle, and the complete preflight report refuses it with its reason.
   Product choice awaiting the operator: whether a pick may be reused by later jobs, as delivered, or must be single use.
2. Renamed and deleted roots.
   Effects continue into the directory picked at its new location.
   The executor compares the captured pathname with the held identity before the first effect and after the last, and a mismatch is a job-level fact: the job ends partial with the displacement in its message and audit summary, because the item records keep the picked spelling which no longer names the directory the bytes went into.
   A directory deleted after the pick is refused before any effect.
   Product choice awaiting the operator: continue and report, as delivered, or refuse further effects once the pathname stops naming the directory.
3. Relocated descendants and planted parents.
   Every path beneath the root is opened one component at a time through the parent handle; a link, a non-directory or an entry on another filesystem is refused, the opened directory must be the inspected entry, and absent components are created only through the parent handle.
   A descendant handle is held only across the single effect it was opened for; the stage handle is the one descendant held longer, and decision 5 states what a relocation of it means.
   Nothing beneath the root is ever created through a pathname.
4. Mount boundaries and reparse points.
   A component on another device is refused everywhere; on Linux a component that is a mount root is refused by the `statx` attribute, which also covers a bind mount of the same filesystem; on Windows reparse points remain refused, which covers junctions and volume mount points.
   On other Unix targets a bind mount of the same filesystem is not distinguished, which is a recorded limit.
   Product choice awaiting the operator: refuse mounts inside a selected directory, as delivered, or follow them.
5. Engine destination authority.
   The stage is created and bound through the held root before the engine runs and its identity is compared after the engine returns; a stage that no longer sits at its name fails every engine item with that diagnosis, nothing is placed, and cleanup retains and reports it.
   Staged files are read only through the stage handle.
   On Linux the child receives a descriptor-anchored destination: it inherits the held stage directory and resolves every write through it, so a renamed ancestor cannot redirect the child, and a stage relocated during the run keeps receiving the child's bytes in the directory this executor created, never through a name another process controls.
   Where the platform provides no anchor, the engine route is refused before any stage exists, because a child given a pathname cannot be contained during its run.
   Product choice awaiting the operator: keep the engine route refused on macOS and Windows until an anchor exists there, as delivered, or accept the uncontained run there for a first milestone.
6. Publication.
   The verified file is linked into its inspected parent by directory descriptor on Unix and by root-relative names on Windows, with no-replace semantics, and the target must then name the verified file.
   A substituted staged entry fails the item, and the link this executor created to it is removed by name immediately after the identity check; the window between that check and the removal is recorded as a limit.
   An existing target is kept.
7. Cleanup.
   Cleanup removes only planned directories, deepest first, through handles bound to inspected directories, with a primitive that removes only an empty directory, and the stage itself by name only while the name still denotes the held stage; anything else is retained and reported.
8. Envelope.
   Local filesystems on the six targets through `os.Root`; native macOS and Windows acceptance evidence remains TEST-04 work; no claim for network filesystems.
9. Reporting.
   A placement that cannot find its staged file reports the stage identity mismatch when there is one; the job records displacement; cleanup never reports success for a directory it did not inspect.

## Consequences

Every download effect is bound to the directory the operator picked rather than to a spelling of it.
The picker token holds an open directory handle for the session; eviction and job completion close them.
A renamed selected directory produces a partial outcome that the operator must read; a deleted one refuses the job.
The engine route is available only where the child can be anchored, which is Linux today; other platforms keep the native route and refuse engine items with their reason until the operator decides otherwise or an anchor exists.
The recorded limits are the removal window after a publication mismatch, same-filesystem bind mounts on Unix targets other than Linux, and the absence of native macOS and Windows evidence.
P18, P19, P20 and the valuable-data hold stay open.
