# ADR 0029 — preserve listing identity during decoding

**Status:** Proposed; implementation requires independent local design review.
**Scope:** A native listing prerequisite for the P12/P13 Copy item-record migration, alongside the P22 complete-page contract.

## Problem and oracle

[ADR 0028](0028-transfer-item-records.md) requires lossless UTF-8 identities before admission or a dependent effect.
The existing session validates names after the pinned Swift library decodes them into Go strings.
Go's [legacy JSON decoder](https://pkg.go.dev/encoding/json#Unmarshal) substitutes replacement characters for malformed UTF-8 and unpaired UTF-16 escapes.
Validating the resulting string cannot recover the original identity.

The [actual-service reproduction](../experiments/2026-09-08-listing-fidelity.md) supplies controlled malformed listing responses over owned mock and real-Swift objects.
Malformed names are decoded to an existing replacement-character name, copied by the native route and reported complete with two copied/verified objects and no failures.
Duplicate identity fields are also accepted.
The valid-Unicode controls must remain successful.
These are fixture-injected responses, not evidence that a deployed Swift service naturally returns malformed names.

The behaviour oracle is the exact captured logical identity required by ADR 0028, the no-publication-from-a-failed-page rule in [ADR 0021](0021-complete-listing-pages.md), and these unchanged-service reproductions.
No item recorder can repair an identity already changed by the listing decoder.

## Decision

Validate each raw JSON listing row before decoding its fields into application strings.
Use the existing Go 1.27 standard library's [`jsontext.Value.IsValid`](https://pkg.go.dev/encoding/json/jsontext#Value.IsValid) with its strict defaults.
It checks UTF-8, Unicode escape validity and duplicate JSON member names, including equivalent escaped member names.
Reject a row that fails these checks with a constant diagnostic that contains none of its provider-supplied text.
Do not allow invalid UTF-8 or duplicate names through validation options.

Keep the existing top-level array, HTTP-body completeness, cancellation, close-error, timestamp, continuation and marker checks.
Decode one raw row at a time inside the existing page reader, then apply the existing typed field mapping only after that row passes validation.
If any row fails, return no rows from that page, including valid rows before the fault.
Earlier pages remain advisory observations and may already have led to work; this is not a whole-namespace transaction or rollback guarantee.

Preserve valid Unicode verbatim, including a legitimate U+FFFD, a valid surrogate pair, combining/precomposed distinctions, escaped backslashes, newlines and embedded zero characters within the existing name contract.
Do not reject all replacement characters, normalise names, invent an escape scheme or silently omit malformed rows.
Do not add retries, per-row HEAD requests, namespace accumulation or a permissive fallback.

The strict check applies to raw member names and string values throughout the row, including unknown fields, so later typed conversion cannot silently replace malformed Unicode in accepted row data.
Other established typed-field mappings remain unchanged.
This does not certify every possible semantic ambiguity, including differently cased field aliases in legacy struct matching, or the independent rclone parser and unrelated JSON endpoints.
The complete item-record adapter, resource budgets and wider provider response contracts remain separate gates.

## Pinned source and compatibility

Add `listing-fidelity.patch` after the existing credential-pairing and listing-response patches.
Preserve both earlier patch files byte for byte.
Record the third patch in `UPSTREAM.json` and the source checker's explicit ordered sequence.
The copied source, including its new tests, must equal the verified upstream archive plus all three patches exactly.
Extend the source-checker tests to reject missing, reordered, altered and unrecorded patches.

The application already requires Go 1.27.0 and the tested toolchain is Go 1.27.1.
Declare Go 1.27.0 in this application-owned local Swift fork because its new standard-library import requires that version; include the module-file change in the new patch.
The upstream version, revision and archive checksums remain unchanged.
This is an application-local patch, not an assertion that upstream Swift supports the change or has raised its own compiler requirement.
No application dependency, workflow, binding or platform scope changes are needed.

The extra raw value is bounded by a row rather than the namespace, and decoded results remain bounded by the page.
Maximum response/row bytes and process memory are still P24 requirements; do not claim this change closes them.

## Implementation and evidence

1. Preserve the exact executed test-only probes and their expected before-fix failures, including valid controls, provider limits and archive hashes.
   Review this decision before changing the decoding boundary.
2. Add the narrow raw-row validation and third checked source patch in one implementation commit.
   Include nested-library lifecycle tests, session-level encoding cases and the actual Copy regression on both owned providers.
3. Require malformed UTF-8, overlong/truncated sequences, encoded surrogate bytes, lone/reversed/unpaired escaped surrogates, ordinary and escaped duplicate members to fail before publication.
   Cover malformed later rows, unknown fields, pseudo-directory names, container names, split readers, cancellation and body close errors.
   Keep the valid Unicode, marker and complete-page controls.
4. The actual Copy test must retain zero destination mutations for a failed first page, an honest terminal job/audit result, source audit authority, destination recovery identities, the complete owned fixture namespace and shared DLO/SLO aliases.
   Valid-name controls must still copy and verify their exact expected bytes.
   A separate child reopening SQLite must retain the witnessed job/audit/recovery state without contacting the proxy.
5. Independently remove strict validation, substitute the legacy JSON-validity check, enable malformed Unicode or duplicate names, reject legitimate replacement characters and suppress the failure to require maintained assertions to detect each broken rule.
   Keep source-gate mutation checks separate from runtime checks.
   Compilation errors, test timeouts, skipped cases and broken fixtures are not successful negative controls.
6. Run relevant nested-library tests, source-gate tests, the full application race suite with both owned providers, pinned rclone and required Secret Service, frontend checks, builds and scenario reconciliation.
   Obtain independent implementation review, all five latest-head/current-master PR jobs, a protected exact-head merge and all five master jobs.

## Following Copy work

This prerequisite does not opt Copy into item recording or fix the existing one/201/401 failed-name accounting gap.
The separately reviewed Copy adapter/UI/restart plan must still document that every required record-write refusal, including validation and conflicting/late writes, latches the shared store and blocks new jobs until restart.
It must validate complete identity construction before calling admission, define comparison-only failure/conflict mapping, project retained interrupted outcomes and refuse reruns with unresolved effects.
The checked execution hooks, complete paginated UI and local sibling migrations remain required.

No crash, fsync, power-loss, native child-lifetime, immutable remote version, exclusive ownership or safe-retry evidence is supplied by these response and orderly-reopen tests.
Mirror starts, existing-destination Copy overwrites and automatic recovery reclamation remain refused.
The valuable-data release hold remains in force.
