# ADR 0071: show container access and feature settings while browsing

**Status:** Accepted for implementation on 23 September 2026.
The contract was reviewed independently on 22 September and the operator confirmed the proposal on 23 September (finish line, operator decision 6); the implementation is a separate reviewed slice.
This records the design the operator asked for on 22 September 2026 (decision 2 in the [finish line](../audits/2026-09-05/linux-finish-line.md) operator decisions): something visual while browsing that shows a world-readable container and similar settings, with web listing and versioning named as examples.

## Context

The container column lists rows from the account listing, which carries only a container's name, object count and bytes (`swiftcontrol.ContainerEntry`); a row's §24 classification is folded in from the local evidence store without a request.
Read and write ACLs, versioning, quotas, CORS and static web are container headers that only a HEAD or GET of that container returns, and Swift offers no bulk read of them.
Today they appear only in the inspector's container section, as values: `InspectorService.Container` makes one HEAD when a container is selected and the inspector prints the read ACL string beside the others with no warning.
The §19.1 acknowledgement is the one place the application interprets an ACL: `MutateService` refuses a save that grants world read unless `ContainerEditDTO.AckPublic` is set, using `grantsPublicRead`, which recognises `.r:*` in any comma position.

Two rules bound the design.
DESIGN §14.2 forbids a HEAD per listed row: listing is cheap classification and selection triggers the authoritative request.
K-CON-001 makes a HEAD authoritative at the moment it was made and says nothing about later changes.
[ADR 0066](0066-named-read-session-snapshot.md) scopes every named read to the bookmark generation validated for it, so a fact learned under one connection cannot be shown under another.

## Decision

### Settings are facts observed by a HEAD, derived once, shown wherever the container appears

1. One derivation.
   A `ContainerSettingsDTO` is derived from a `swiftcontrol.ContainerDetail` by one function in package app and nowhere else: world read (`grantsPublicRead`, the function the acknowledgement gate uses: exact `.r:*` after trimming, in any comma position), public listing (`.rlistings` beside world read), referrer grants (a `.r:` entry that is not `.r:*` and does not begin with `.r:-`, since `.r:-` entries are referrer denials and grant nothing), shared write (a non-empty write ACL), static web, versioning mode and archive, quota (a byte or object quota above zero), CORS, the observation time and the listing scope generation.
   A denial is never a badge; it stays visible in the exact header value the tooltip shows.
   The container editor keeps its own check of the value being typed, because the acknowledgement must react before any save; the badges and banners never re-parse a header in the frontend.

2. Three sources, none of them the listing.
   A settings fact comes from the selection HEAD the inspector already makes, from the detail a successful container edit returns, or from a check the operator starts over the loaded rows (point 5).
   Listing a page never HEADs a row; §14.2 stands as written.

3. Retention.
   Facts are held in memory per connection generation, keyed by container name, and discarded when the generation changes; a row whose container has no fact under the current generation shows nothing.
   A badge is a positive observation.
   The absence of a badge means unchecked, never private, and the design makes no claim for an unchecked row.

4. Presentation.
   A row shows badges after its name in every container mode: `public` in the danger colour for world read, `public + listing` when object names are listable too, `referrer` in the warning colour for referrer grants, `shared write` in the warning colour, and `web`, `versioned`, `quota` and `cors` in the neutral chip style the §24 class chip uses.
   Each badge's tooltip carries the exact header value and the observation time.
   When the open container is world-readable, the working list header shows a danger banner: anyone with the URL can read objects in this container (read ACL `.r:*`), and can list its object names when listing is granted.
   The inspector's read ACL row carries the same sentence.
   All three come from the one derived fact.

5. Checking the loaded rows on request.
   The container column offers "Check settings".
   The frontend walks the loaded rows in listing order and asks for each one through the existing `InspectorService.Container` read, the same HEAD selection makes, with at most 4 requests in flight; each reply carries the derived facts under its scope's generation.
   No bridge action is added: a new action is required-set drift under [ADR 0069](0069-preserve-covered-scenario-cells.md) and would need that decision's admission route, which this indicator does not justify when the existing read already returns everything the derivation needs (amended 23 September 2026; the 22 September contract named a batched `ContainerSettings` action).
   The header shows "checked n of m" while it runs; the operator can stop it, and it stops itself when the scope changes.
   A HEAD that fails marks that row unchecked with the error in its tooltip and does not stop the sweep.
   The bound is the rows the operator has loaded, at most 5,000 per page, and nothing is checked that was not listed.

6. Freshness.
   Selecting a container refreshes its fact through the inspector HEAD it already makes; a saved edit replaces the fact; nothing refreshes on a timer.
   When the selection HEAD fails, the open container has no fact: the row stays unchecked with the error in its tooltip, and the working list header shows a neutral notice that the container's settings could not be checked, so the absence of a banner is never silent for the open container.
   The tooltip's observation time is how the operator judges age.

7. Capability gate.
   Every request here is a read.
   [ADR 0052](0052-v1-capability-gate.md) is unchanged, and so is the bridge action inventory: the facts travel in fields added to the existing container row and detail replies.

### Cost

No request is added to listing or to selection.
The check costs one HEAD per loaded row, one bridge call each, only when the operator asks, with a visible count and a stop.

## Consequences

An operator browsing an account sees a world-readable container marked in the row the moment it has been selected or checked, and the banner follows every successful fact for the open container; a failed selection HEAD leaves the container open with the could-not-check notice instead.
A container that has never been selected or checked carries no mark, and the column says so only through the check's count, so the design gives no false comfort about the rest of the listing.
Facts are as fresh as the HEAD that produced them; a container made public by someone else after that HEAD shows the old fact until it is selected or checked again.

## Maintained proof

- Derivation tests over the ACL forms: `.r:*` (world read), `.r:*,.rlistings` (world read and listing), ` .r:* , .rlistings ` with spaces (the same), `.r:.example.com` and `.r:*.example.com` (referrer grant, no world read), `.r:-.example.com` (no badge at all), `.r:-.example.com,.r:*` (world read only), `.r:*x` and a bare `.r:` (no badge), `account:user` (no badge; a write ACL of the same form is shared write), and empty; static web from either web listing or web index metadata; each versioning mode; byte and object quotas; CORS presence.
- Frontend tests: a badge per fact and none for an unobserved row; the banner for a world-readable open container and its listing variant; the could-not-check notice when the selection HEAD fails; badges gone after a generation change; the check's count, stop and scope-change stop; a failed HEAD in the check leaves the row unchecked.
- System tests on both fixtures: seed one container per setting; list the account and prove from the fixture's request log that no container was HEADed; select one and see its badges and banner; run the check and see every seeded badge; switch account and see none; save a world-read ACL with the acknowledgement and see the badge from the returned detail.
- Compiled mutants must be caught: a predicate that accepts `.r:*x` or a bare `.r:`; a referrer badge for a `.r:-` denial; a cache that survives a generation change; a check that HEADs names outside the loaded rows or continues after stop; a banner keyed to a stale container name; a failed selection HEAD that shows no notice.

## Open points for the operator

- The badge set above extends the operator's examples (world read, web listing, versioning) with referrer grants, shared write, quota and CORS; that extension is the author's proposal.
- Whether the on-request check over loaded rows is wanted in v1, or the selection-time facts alone suffice.

## Limits

This shows what a HEAD reported; it never proves a container private.
Account-level ACLs, TempURL key values and object-level settings are out of scope.
Nothing here changes a mutation, a gate or the valuable-data hold.
