# What evidence admits an upload replacement — 10 October 2026

[ADR 0085](../adr/0085-reobserved-destructive-identity.md)'s upload-overwrite amendment needs a baseline to compare against, and says the captured identity belongs in the replacement ledger without saying what the identity is.
That turns on a question about the preflight: when a name is admitted for replacement, what did the acknowledgement actually read?

`destinationLiveness` chooses between one HEAD per name and one listing of the prefix by what each costs in requests, so the answer depends on how many names the selection holds.
This measures it.

- No store was involved beyond the in-process mock: a probe drove `PreflightUploadReplacing` against `internal/swifttest` and counted the requests the mock logged, with every destination name holding a dynamic manifest so that its listing row is the zero-byte kind.
- **This measures the preflight alone, not the whole acknowledgement.** The public path calls `fragmentPlanFor` afterwards, which reads each replacement authoritatively so the operator's question can state what backs it; the table below says nothing about that, and the two must not be added together.
- The probe is retained in `.aeronautes-work/20261010-overwrite-baseline/` with `SHA256SUMS` and `HOST.md`. It is a probe rather than a committed test: it asserts nothing about behaviour that is meant to hold, it reports what the current path reads.

| Names in the selection | Admitted for replacement | HEADs of those names | Listings |
| --- | --- | --- | --- |
| 2 | 2 | 2 | 0 |
| 3 | 3 | **0** | 1 |
| 5 | 5 | **0** | 1 |

Two names sit at the listing floor, so each is settled by its own HEAD and an identity is already in hand, free.
Three and five are above it, and one listing settles all of them: every name was admitted for replacement without the preflight ever reading that name authoritatively.

Selection size alone does not decide this, and the table should not be read as though it did.
`destinationLiveness` stops listing once another page would cost at least what the names still open cost to ask for directly, and then HEADs those names; so a prefix the listing does not resolve produces HEADs above the floor too.
What these three cases show is the branch where one listing resolves every name, which is the branch in which the preflight reads nothing authoritatively.

Two consequences, both of which [ADR 0085](../adr/0085-reobserved-destructive-identity.md) now records as decisions rather than leaving open.

A listing row cannot stand in for the missing HEAD.
`ObjectClass`'s definition in this tree says listings only ever hint and an authoritative HEAD is the truth; a dynamic manifest's row is zero-byte and carries the manifest object's own hash rather than a digest of what it assembles.
A row therefore establishes neither the class nor what a manifest holds, which is exactly what a replacement baseline has to pin down.

And the baseline is a retention problem rather than a cost one.
A first reading of this table concluded that a HEAD-grade baseline costs an extra HEAD per name above the floor; that is wrong, because the preflight is not the whole acknowledgement.
`FragmentPlan`, reached through `fragmentPlanFor` before the operator is asked, takes an authoritative HEAD of every name admitted for replacement so the question can state what backs it — whether or not reclamation is enabled and whether or not the container keeps versions — and discards the detail apart from counting fragments.
So the reads the baseline needs are already being made on that path and are being thrown away; the two cases that obtain nothing are an acknowledgement composed without a readable destination and an individual name that could not be read, both of which the decision already refuses to let overwrite.
