# ADR 0034 — download destination admission

**Status:** Proposed for independent local review under the authorised audit remediation plan.
**Scope:** Refuse evidenced existing destination hazards before any download effect; stronger filesystem authority remains P18 work.
Production changes depend on approval of this contract.

## Problem and oracle

The [before-fix evidence](../experiments/2026-09-09-download-admission.md) reproduces the same thirteen unsafe admissions on mock Swift and real Swift.
An ordinary existing descendant symlink redirects complete file bytes or a directory marker outside the selected directory, and the job reports copied and verified success.
The real engine first writes its selected stage correctly; the application's later pathname-based placement causes the observed escape.
Missing roots, dangling final links, regular-file ancestors and marker collisions also pass preflight and become admitted jobs, with some failures discovered only after reads or staging.
Six controls per provider retain safe absent subtrees and the accepted initial selected-root symlink spelling.

The oracle is [K-XFER-011](../REQUIREMENTS.md), DESIGN §27.5, DS-05 and the whole-plan refusal boundary, with exact local and remote effects checked independently of job counters.
An existing destination hazard must be refused before creating a transfer job, item or audit event, reading selected content, starting the child or changing the local filesystem.
Listings and the read-only metadata needed to classify selected markers remain permitted planning operations.
Late failure or a truthful partial job cannot substitute for this admission refusal.
Preserve ADRs [0032](0032-download-placement-prerequisites.md) and [0033](0033-download-item-recording.md): exact captured spelling, final no-replace, checked closure, temporary intent before effects, sticky recording refusal and conservative cleanup.

## Selected directory and compatibility

Keep the valid UTF-8, absolute and unchanged-by-cleaning spelling guard before joining names or inspecting the destination.
Acquire one directory root for the duration of each complete local preflight and confirm that the acquired object is inspectable as a directory.
An absent, non-directory or uninspectable selected root rejects even an empty remote selection.
Preflight must never recreate a missing root or create a missing subtree.
Release this temporary inspection root on every return path; its acquisition is not picker-time or job-lifetime identity authority.

Preserve the already accepted initial selected-root symlink behaviour: open the captured spelling and inspect its selected directory, without rewriting the spelling or applying a descendant-link ban to the picker path itself.
Within that directory, conservatively refuse every existing descendant symbolic link and every Windows reparse entry required by the selection.
This deliberately also refuses a contained descendant alias or otherwise usable reparse entry.
Supporting aliases needs a separate complete-path identity policy; this correction must not silently infer one.
An operator can select the intended directory directly and repeat planning after resolving the reported destination problem.

## Complete component inspection

Keep the logical-name, route, collision and file-versus-parent guards before treating any relative name as a filesystem path.
Inspect each existing component without following its final link, using the same acquired inspection root.
Do not pass a trailing separator to a final-entry inspection: that can change Windows link semantics.
An existing ancestor must be an ordinary directory under the descendant-entry policy before its child is considered.
An ordinary file's final target must be absent; an existing file, directory, link or other entry is a collision, including a dangling link.
An existing directory-marker target may be an accepted ordinary directory; an existing non-directory or prohibited link is a refusal.
The real remote root marker retains its nonempty object name and maps its empty local suffix to the acquired directory only at this inspection boundary.

Only an unambiguous missing child beneath an inspected directory establishes an absent subtree.
Access denial, I/O failure, unsupported metadata, bad network paths and ambiguous path errors must remain refusals.
Do not treat every stat error, or a generic missing-path classification on an unresolved full path, as evidence of an absent creatable file.
In particular, Windows distinguishes file-not-found from path-not-found, and its general not-exist classification also includes bad network paths.
Inspect component types first and use a narrow platform-specific missing-child classification where necessary.
Reliable Windows reparse detection must use the actual file attributes; checking only `ModeSymlink` or `IsDir` does not identify all reparse entries.
Missing or unexpected platform metadata is uncertainty, not permission to continue.

Collect destination problems across the complete selection before the service accepts a start.
Preserve the existing Start preflight rerun before engine construction, job creation and start audit.
Apply the same complete current-state destination check at executor entry after logical-plan validation and before the first item admission or effect, including for direct unrecorded callers and an empty plan.
An executor-entry refusal can occur after the service has admitted a job; retain that failed job honestly, without claiming that the earlier service admission never happened.
This check does not close the later check/use interval or grant cleanup authority.

## Implementation and proof sequence

1. Review this contract and the byte-identical archived before probe before changing production code.
   Preserve the original failing safety assertions, actual exits, full-byte snapshots and the distinct scope of the real-child observation.
2. Implement complete preflight and matching executor-entry admission as one logical fix with maintained regressions.
   Share the component policy, preserve existing diagnostics where their meaning remains accurate, and keep the acquired inspection root local to the check.
   Do not bundle a new engine, root-token lifetime, placement primitive or cleanup protocol into this correction.
3. On both owned Swift providers, require whole-plan refusal for existing descendant links, dangling leaves, regular-file ancestors, marker collisions and unavailable selected roots.
   Add a safe first selection member followed by an invalid member so early materialisation or per-item admission cannot pass unnoticed.
   Exercise native, real-engine and directory-marker plans, empty and root-marker selections, existing ordinary directories, new subtrees and the accepted selected-root symlink.
   Include the deliberately refused contained descendant alias under this policy.
4. Prove faithful inspection errors and direct-executor refusal, with no selected content read, child dispatch, item admission or local effect when the error is already observable at that boundary.
   Native permission and platform error/type controls must report their actual environment and result; inability to produce an error is not a passing fault test.
   Native Windows/macOS reparse and error evidence is required before those platform claims can be cleared; source inspection, synthetic metadata and cross-compilation do not replace it.
5. Compare complete permitted local deltas and independently read full contents, both owned remote namespaces, public and durable jobs, all item pages and matching audits.
   Preserve earlier ADR 0033 accounting, no-replace, closure, recording-stop and cleanup regressions.
   Remove each relevant guard in deliberate broken controls and require the corresponding maintained refusal or no-effect assertion to fail, with passing normal controls alongside it.
6. Run focused validation before the complete relevant race, inventory, scenario and build checks.
   Obtain independent local review for each logical commit, then all five latest PR jobs on current master, a normal protected merge of the reviewed head and all five resulting master jobs.
   Keep the existing finite CI budgets, full inventories and release gates.

## Remaining authority boundary

This correction addresses already observable destination hazards only.
The picker token still records a pathname, and later native, engine, marker, publication and cleanup operations still need stable authority through their effects.
A temporary `os.Root`, an immediate recheck or a final no-replace link does not establish that complete boundary.
Concurrent root replacement, parent-link insertion, relocation of opened descendants, mount boundaries and source-leaf substitution remain unproved.
Directory-only cleanup and unlinking the verified file also need authority over the actual entry, not just its recorded path.

Before stronger implementation, separately decide root capture and lease lifetime, rename/relocation semantics, the content producer's destination authority, exact-file publication, cleanup ownership and platform support.
Do not replace a missing guarantee with a pathname fallback under a stronger safety claim.
P18/P19/P20/P30, stronger content verification, native platform acceptance and the [valuable-data release hold](../audits/2026-09-05/remediation-plan.md#acceptance-gates-and-completion) remain open.
Mirror starts, changed existing Copy overwrites and automatic reclamation of uncertain data remain refused.
