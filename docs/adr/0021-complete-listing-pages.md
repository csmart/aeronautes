# ADR 0021: complete native listing pages

**Status:** Accepted after independent design review; response implementation precedes continuation under P22.
**Scope:** Audit DS-13 / W-13 / R-16, native container and object listings, DESIGN §§14–16 and 43.5, K-PERF-001 and K-CON-001.

## Evidence and decision

The native iterators currently stop when a response contains fewer rows than the requested limit.
Four deterministic cases on the PR42 source reproduce two problems: short nonterminal container and object pages hide a second name, and malformed object pages can return a row without an error.
The malformed cases use a complete JSON value with a truncated HTTP body, and an invalid numeric field beside a valid timestamp.
The library reads one JSON value and ignores errors while draining the rest; object timestamp parsing can also overwrite an earlier decode error.
These are local HTTP reproductions against the real session and pinned library, not observations of a deployed provider.

The [Swift API reference](https://docs.openstack.org/api-ref/object-store/) describes a short page as exhaustion for conforming deployments and JSON empty listings as HTTP 200 containing an empty array.
This decision deliberately uses an extra terminal request to support the audit's nonstandard short-page case without a separate opt-in.
A short nonempty page is not sufficient evidence of exhaustion under Aeronautes' supported provider contract.
This refines the deferred compatibility policy in §43.5; it does not claim that standard Swift requires short nonterminal pages.

A native listing succeeds only after every consumed page is a complete valid response and the stream reaches a complete empty array, unless its consumer deliberately stops early.
Earlier valid pages remain advisory observations; this is not a transactionally consistent namespace snapshot.
Listings still cannot replace authoritative HEAD or exclude external writers.

## Complete response contract

1. Container and object JSON listing reads require HTTP 200, no Content-Range and exactly one top-level JSON array.
   Reject null, objects, scalars, an empty body, partial status, malformed fields, a second JSON value and non-whitespace trailing bytes.
   Preserve a valid empty array as the terminal response.
2. Decode through the actual end of the HTTP body before accepting the page.
   Propagate body read, truncation, context and close errors; do not treat a successfully decoded prefix as a complete message.
   On an already known invalid status or shape, close promptly rather than waiting to drain an untrusted remainder.
   No row from a failed page may be yielded.
3. Preserve the original error before object date/subdirectory conversion.
   Invalid dates also refuse the page.
   Keep the existing successful object and container field mappings and the literal object-name representation.
4. Keep the change inside the pinned Swift adapter boundary.
   Add a narrow listing-response patch to the existing checked upstream source, retaining the credential-pairing patch unchanged.
   Extend the recorded patch sequence and its checker so the imported directory must match the verified upstream archive plus both ordered patches exactly.
   Missing, reordered, altered or unrecorded changes must fail the existing source gate.
   Run the nested library's relevant tests and its existing mock suite.
   Do not change rclone, authentication, retry policy or unrelated JSON endpoints merely to fix listing responses.
5. Keep memory proportional to a decoded page; do not accumulate the whole namespace or add HEAD calls per row.
   This slice does not establish the wider response-byte and process memory budgets assigned to P24.

## Continuation contract

1. Continue after every nonempty accepted page, including one shorter than requested.
   Only an accepted empty JSON array ends an exhausted stream.
   Preserve the existing page-size defaults, prefix and starting-marker API.
2. Validate every raw row's marker name before filtering self rows or publishing any row from that page.
   Names must be nonempty and strictly increasing in UTF-8 byte order, both within a page and after the requested marker.
   Repeated, backward or otherwise nonprogressing pages fail explicitly.
   Validate the whole page before yielding so a malformed later row cannot make an earlier row from that same page look accepted.
3. Keep literal prefix scope and delimiter semantics.
   Refuse rows outside the requested prefix and pseudo-directory rows in a deep object listing.
   Preserve a delimiter listing's own-prefix self-row exclusion, but use that raw row when advancing the marker even if it produces no child row.
   A page containing only a filtered self row must not look like exhaustion.
4. Preserve lazy consumption: stopping a consumer causes no next-page request.
   Cancellation is observed before another request and while emitting a page.
   Do not add prefetch workers, automatic restarts, silent sorting, deduplication or retries to repair a malformed stream.
5. Browse pages, Find, preview/planning and fresh native verification consume the same corrected iterator contract.
   A later listing failure may follow already observed valid pages or already completed work; callers must report the failure through their existing error/outcome paths.
   This change does not promise rollback or an atomic whole-job outcome and does not reopen Mirror or overwrite admission.

## Verification and delivery

Preserve the four failing parent cases separately from implementation evidence.
Add permanent response tests for both listing kinds, valid and invalid date fields, split/truncated bodies, JSON suffixes, wrong status, cancellation and close failures.
Keep valid empty-array, prefix, delimiter, field-mapping and encoding controls.
Correct mocks that emit null instead of an empty array, documenting that fixture correction without weakening malformed-response assertions.

Continuation tests cover short pages with a hidden tail, repeated/backward and within-page disorder, raw self-only pages, starting markers, literal hostile names, consumer early break and cancellation.
Independently remove response-completeness, error-preservation, continuation and marker guards.
Reject compilation failures, timeouts and broken fixtures as proof.

Use the actual bound Browse and Find methods against two owned accounts on both mock Swift and SAIO.
For provider compatibility, an owned proxy may cap the upstream page size while retaining the requested marker and returning real Swift responses.
For malformed responses, inject the precisely recorded fault after an owned listing response.
State clearly that these are controlled provider/fault scenarios, not a claim that SAIO naturally returns malformed or nonstandard pages.
Independent snapshots bypass the modifying proxy and witness both account namespaces before and after.

Add explicit Browse container/object refusal requirements and invocation-specific read/refusal receipts without changing the renderer surface.
For Find, use its matching terminal status and existing cancellation receipts; a failed asynchronous scan cannot receive successful-start credit.
Verify complete mixed-name preview and eligible native-routed new-name Copy/verification across forced short pages, with expected destination bytes and unchanged source/collateral state.
Keep the provider cap active for every selected source and destination JSON listing, including requests from rclone.
The [mixed-engine experiment](../experiments/2026-09-07-short-page-copy.md) found a separate pinned-rclone limitation: the corrected native plan sees three names, but rclone omits one planned file under the cap and the job reports partial.
Retain that mixed-engine case as an explicit partial-outcome characterisation, paired with the same mixed Copy succeeding without the cap; it earns no successful-Copy credit.
Native-only success does not establish complete mixed-engine provider compatibility or close P22/DS-13.
Keep source, system, frontend and real-engine evidence separately accounted and reconcile only final passing scenario witnesses.

Deliver two separately reviewed implementation commits: complete listing responses and their upstream-source verification, then continuation/marker validation and its affected consumers.
Each carries its system reproduction and relevant full tests.
After native continuation, investigate how the pinned engine can execute the complete selected set despite short pages.
Record and independently review any required engine/flag decision before changing that boundary; preserve staging, credentials, encoding, conditional commit, verification and refusal rules.
The separate follow-up is [ADR 0022](0022-complete-rclone-selection.md): after its design review, correct selected-set execution and update the maintained capped mixed Copy to require completeness while preserving the earlier partial experiment as historical evidence.
The wider P22 acceptance remains complete execution and verification of the intended namespace, with both engines tested against the same provider scenario.
Require the five latest-head/current-master PR checks, normal protected merge and all five master checks.
P10/P11 authority, P12/P13 durability/outcomes, P14 writers, P15 ownership, P24 wider budgets and the valuable-data release hold remain open.
