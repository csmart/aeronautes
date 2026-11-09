# ADR 0101: TempURL key slots have their own operation, and CORS is editable metadata

**Status:** Accepted for 0.5.0 (9 November 2026).
The roadmap the operator accepted as finish-line decision 31 names the feature, and DESIGN §22 names its priorities.
**Scope:** How the inspector sets and clears the TempURL key slots of an account and a container, how the Go model and the container editor treat the CORS metadata family, and what the durable facts and the audit trail record about a key change.
It changes neither the metadata editors' per-key contract (§17.2, [ADR 0090](0090-container-presets.md)), the public-read acknowledgement, the settings badges ([ADR 0071](0071-container-settings-indicators.md)), nor the v1 capability gate ([ADR 0052](0052-v1-capability-gate.md)).

## Context

§22 lists the v1 priorities for TempURL: show whether key slots are present, replace or rotate, clear, redact values, and never log secret material.
URL signing is a later feature, and key management belongs in the inspector.
Today the model reports presence only.
`buildAccountPortrait` and `buildContainerDetail` list the slots a HEAD returns in `TempURLKeySlots`, withhold the values everywhere including the raw header view, and keep the slots out of `Meta`, so an edit's removal loop never removes one.
Only the account edit refuses a slot name a caller puts into its working copy: `UpdateAccount` refuses before any I/O (K-SEC-006), while `UpdateContainer` validates ordinary metadata syntax and then POSTs the supplied rows, so a container working copy that names a slot sets the key today and reports a `verify_mismatch` after the stored key has changed.
That gap is pre-existing, and decision 1 closes it.
The inspector shows "TempURL keys: 2 set" and offers nothing to do about it.

CORS is the other family the editors exclude.
Swift configures it with three container metadata keys under `X-Container-Meta-`: `Access-Control-Allow-Origin`, `Access-Control-Max-Age` and `Access-Control-Expose-Headers`, which the proxy reads to answer a browser's preflight request.
`buildContainerDetail` folds any `Access-Control-*` key into a `CORS` presence flag, counts it in the editable fingerprint and keeps it out of `Meta`.
`ContainerEdit.Meta` excludes the family by contract, so the per-key delta never sets or removes one, and the inspector says "CORS: configured".
ADR 0090 kept that exclusion when it made the `Web-*` family ordinary metadata, because the CORS values are header-shaped and multi-valued.

The roadmap puts both management surfaces in 0.5.0.

## Decision

### TempURL key slots

1. A key slot is changed by one operation of its own, `MutateService.UpdateTempURLKey`, which either sets a slot to a secret the operator supplies or clears it.
   The target is the connected account or one container, and one of the two slots Swift reads, `Temp-URL-Key` and `Temp-URL-Key-2`; any other name is refused before any request.
   The metadata editors keep the slots out: an account or container edit cannot remove one, `UpdateAccount` keeps refusing a slot name in its `Meta`, and `UpdateContainer` gains the same refusal before any I/O, for both slot names in any letter case.
2. The secret is accepted once and sent only as this operation's POST; the library's authentication retry may repeat that same POST after a re-authentication, and nothing else carries it.
   It must be non-empty and at most 256 bytes, which is what a Swift metadata value may hold, and it must keep its literal identity under the rule the metadata validator already applies: no control bytes, and no leading or trailing ASCII space or tab, because HTTP does not preserve them and an empty header value deletes the slot, so a secret of spaces would clear the slot it meant to set.
   The operation refuses anything else before any request, never trims a secret, and treats the clear flag as the only way to request a deletion.
   The secret is never written to the mutation facts, the audit trail, the application log, any DTO the operation returns, or any error, mismatch or log line, and the raw header views keep redacting it.
   The audit summary names the resource, the slot and whether it was set or cleared, nothing more.
   The operator who sets a key is its only holder in this product, and the editor says so before saving; a key the editor generates comes from the platform's cryptographic random source, is shown once, before it is sent, and never again.
3. The operation follows the metadata protocol: a preflight HEAD, the POST, a verify HEAD, an intent recorded before the POST and an outcome with its audit event recorded after it ([ADR 0062](0062-container-metadata-durable-facts.md)), under the caller-named binding of [ADR 0079](0079-caller-named-mutation-binding.md).
   Its conflict basis is the slot presence the operator was shown: the request carries the slots the inspector listed, and a preflight that lists a different set is a `conflict` with no POST.
   A value that changed behind an unchanged presence is invisible by design, since values are withheld; a set over a present slot is a replacement by intent.
4. Verification reads the slot back.
   A set is `applied` when the verify HEAD carries the slot with the value that was sent, compared in memory and then dropped; a clear is `applied` when the slot is absent.
   Anything else is a `verify_mismatch` whose description names the slot and the kind of difference, never the value sent or the value read back; the unverified and residue rules of ADR 0062 apply unchanged.
5. The operation is part of the metadata-writing scope v1 keeps (ADR 0052), since §22 lists it among the v1 priorities.
   It joins the scenario inventory as a mutate requiring `happy`, `refusal` and `conflict`, and its baseline change lands through the drift route of [ADR 0069](0069-preserve-covered-scenario-cells.md).

### CORS

6. The `Access-Control-*` family is ordinary editable container metadata, exactly as ADR 0090 made the `Web-*` family.
   `ContainerDetail.Meta` carries the keys, `ContainerEdit.Meta` sets and removes them through the per-key delta, and the `CORS` presence flag stays derived, now from the three keys the proxy reads.
   The contract for callers changes with it: a working copy built from the detail carries the CORS rows along, and an edit whose `Meta` omits them removes them, as for any key.
   A container whose only `Access-Control-*` key is one the proxy does not read shows that key as a plain row and no badge, where it showed a badge before.
7. The container editor offers a CORS section over those rows, separated from the metadata rows as the presets are: allowed origins, a maximum age in seconds and exposed headers, each derived from the row it stands for.
   Editing a field rewrites only its own row, and the rows stay in view in the metadata editor, as §19.2 asks of the ACL editor's structured surface.
   The section checks the shape the proxy expects only for a field the operator changed: origins are `*`, `null` or `scheme://host[:port]` separated by spaces, the maximum age is a whole number of seconds, and exposed headers are header names separated by spaces.
   A row the operator did not touch is submitted verbatim whatever it holds, so a quota-only save is never blocked by an existing CORS row, and the raw rows stay the escape path §19.2 requires: a value the section would refuse can still be written as a row.
   The cluster decides the rest, as it does for the static web keys, because the capabilities read does not say whether CORS is served.

### Why these shapes

A typed Go surface for CORS was considered and not taken.
Three verbatim header values need no typing to be set, removed, fingerprinted or verified, the per-key delta already does all four, and the editor is where an operator benefits from structure.
Typing them in Go would add a struct, a delta branch, a verify branch, two DTOs and their bindings for no behaviour the rows do not already have.

Two bridge methods, one to set and one to clear, were considered and not taken.
One operation with an explicit clear flag is one inventory row, one scenario triple and one classification, and an empty secret is refused rather than read as a clear.

Putting the slots into the metadata editors was not considered.
An editor that shows every value cannot hold a value it must never show, and a complete-set edit would remove a slot the operator did not see.

## Maintained proof

- Go, adapter: setting and clearing each slot at both levels on the mock; refusal before any request of a wrong slot name, an empty secret, a secret of spaces, a secret with a leading or trailing space or tab, a control byte and an over-long secret; a preflight that lists different slots is a conflict with no POST; a verify HEAD that returns another value, or keeps a cleared slot, is a verify mismatch whose description carries neither value; an account edit and a container edit that name a slot in `Meta`, in either letter case, are refused before any I/O, and an unrelated edit leaves both hidden slots in place; the CORS rows round-trip through the container edit and their omission removes them, on the mock and on the owned Swift, with the literal round-trip test carrying the CORS row in its working copy as it carries `Web-Index`.
- Go, service: intent before POST, outcome and audit after, the residue error when the outcome cannot be recorded after a POST, and the secret absent from the mutation facts, the audit trail, the returned DTO and the captured application log, by searching each for the literal value.
- System: the three war-room scenarios on the mock and the owned Swift, with a slot set then cleared (`happy`), a wrong slot refused before any request (`refusal`) and a stale slot list refused (`conflict`), each proving nothing else in the namespace changed.
- Frontend: the slot rows render presence, set and clear from the detail; the set dialog refuses an empty secret, shows a generated key once and sends it once; the CORS section reads the three rows, rewrites only its own row, refuses a malformed origin or age in a field the operator changed, and lets an unrelated change save over an untouched row it would have refused.

## Limits

Key management does not sign URLs; §22 keeps that a later feature.
Whether a cluster's proxy honours container-level keys, or CORS at all, is the cluster's to decide, and the editor cannot know it from the capabilities read.
A key is a metadata value on the store: anyone holding the account's credentials can read it with a HEAD, so this record redacts it in this product, not on the wire or in the cluster.
Nothing here changes a mutation's admission, a gate, or the valuable-data hold.
