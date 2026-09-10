# Bulk delete failure identities

## Scope and oracle

This is the bulk-delete half of DS-08 in [the data safety audit](../audits/2026-09-05/data-safety.md) and its [remediation row](../audits/2026-09-05/remediation-plan.md), which requires one outcome per identity rather than an arithmetic count.
The applicable contract is K-DEL-002 with the §31.2 strategy engine and the §32 postcondition: `Options.OnFailure` receives every per-name outcome uncapped, `Result.Failures` is a bounded display sample of the same set, and the run's counters are published as public job facts.

The bounded question is only this: for each name a bulk request asked to delete, what does the run know about its effect afterwards, and how is that stated.
Everything else in DS-08 stays open, including the 200-record display cap, the segment-deleting helper that returns only counts, and the force flow with no failure sink.

The protocol constrains the answer and must be stated plainly.
A Swift bulk-delete reply reports deleted and not-found as counts alone and names identities only in its error rows, so the identities of successful deletions are never knowable from the reply.
Everything below therefore distinguishes what a reply establishes from what it merely claims.

## Source finding

The primary defect is that a bulk reply's identities are never validated at all.
`BulkDelete` checks arithmetic only: it returns an error when deleted plus not-found plus the row count differs from the requested count, or when the response status is not 200 with no rows at all.
Nothing checks that a row names something this request sent, that no name is named twice, or that a row's status even denotes a failure.
A reply can therefore reconcile numerically while naming the wrong names, and it is then accepted in silence with no error raised anywhere.
For a request of `a` and `b`, two rows both naming `a` reconcile at two accounted names while `b` is never mentioned and is silently treated as handled; one foreign row plus one claimed deletion reconciles the same way.
Both callers record the rows before ever reaching an error branch, so on these replies the wrong identities are recorded and a requested name disappears without a signal.
This is materially worse than a mis-assignment on a failing reply, and it is not a variant of one.
An earlier draft of this document claimed these defects could only bite once arithmetic had already failed; that claim was wrong and is withdrawn.

Two narrower mechanisms sit on top of it.

The first is positional assignment on the path where arithmetic does fail.
Both call sites, the streaming flush in `deletePass` and the explicit-name loop in `DeleteNames`, compute an accounted total as deleted plus not-found plus the number of error rows, then use that total as an index into the requested batch and record everything from that index onward as failed.
The reply's counts carry no order and no identity, so the index is a guess about which names the server handled.

The second is that identities the reply does name are not resolved back to the objects that were requested.
The request body is built as one percent-encoded `/container/object` line per name, and the mock echoes that line verbatim in its error row.
The code records the echoed line as the failure name, so the retained set and the uncapped stream carry wire paths rather than object names, and a row naming a path that was never requested enters the ledger as though it were one of ours.

## Observed before evidence

A probe drove `DeleteNames` against a server that answers authentication, capabilities and containers normally while returning a crafted bulk reply.
It ran on unchanged production at head 08b0d125ace4944c702720f4c08336253127a36c, modified no production file, and exited zero.
The probe source, its output and the producer record are retained under the deletion evidence directory of this work as `probe_bulk_identity_test.go.txt`, `before-probe.txt` and `before-producer.txt`.

The probe returns a canned reply and does not seed or delete the submitted names, so it establishes what the run records and what the reply claimed, and nothing about which objects actually survived.

| Reply | Requested | Recorded identities | What the run got wrong |
| --- | ---: | --- | --- |
| One row naming the last name, no counts | 5 | `/media/n04`, n01, n02, n03, n04 | n00 recorded nowhere; the named one recorded twice, once as a wire path |
| The same row twice | 5 | `/media/n03` twice, n02, n03, n04 | n00 and n01 recorded nowhere |
| One row naming a path never requested | 4 | `/media/never-requested`, n01, n02, n03 | n00 recorded nowhere; a foreign path entered the ledger |
| Three deletions claimed and one row | 5 | `/media/n00`, n04 | three names recorded nowhere while the reply established no effect for any of them; n04 named on no evidence |
| One name, nothing accounted | 1 | n00 | nothing lost: the passing control |

The fourth row is the clearest statement of the defect.
The reply claims three deletions it does not identify and names one failure, so it establishes an effect for no individual name beyond the one it blamed.
The run nevertheless records exactly one other name and reports the remaining three as though they were handled.

A second probe, run the same way on the same unchanged head and retained as `before-reconciling-probe.txt`, drove the replies that reconcile numerically.
These are the shapes that raise no error at all, so nothing downstream can tell that anything went wrong.

| Reply for a request of `a` and `b` | Error raised | Recorded identities | What the run got wrong |
| --- | --- | --- | --- |
| Two rows both naming `a` | none | `/media/a` twice | neither `a` nor `b` recorded by name; `b` never mentioned and silently treated as handled |
| One foreign row and one claimed deletion | none | `/media/foreign` | a path never requested recorded as this run's failure; neither requested name recorded |
| One claimed deletion and a real row for `b` | none | `/media/b` | the correct failure, but recorded under a wire path rather than the name |

The third of these is a valid partial reply, and its single failure is genuine.
It is the reason the passing control cannot be that a reconciling reply records no failure.

## The outcome contract

The correction needs a contract before it needs code, because the present result type cannot express what a bad reply leaves behind.

Two facts about a requested name are independent of each other, and the earlier draft of this document wrongly fused them.
What the reply *reported* about the name is one fact: nothing, or an execution error with a status.
What the run *knows about the effect* is another: whether that object was destroyed or survives.
A reported execution error is a report about an attempt, not a witness to its outcome, so it establishes no effect at all.
A controlled substitution shows this directly: the same one-name server-error reply can accompany a deletion that landed and one that never reached the service, and an authoritative check afterwards finds the object absent in the first case and present in the second.
The repository already holds this principle for an attempted deletion in [ADR 0023](../adr/0023-upload-post-commit-errors.md), which records that a failed response after a deletion may have landed remains unresolved and grants no retry, so adopting it here invents no new storage guarantee.

Three kinds of fact are therefore retained.

A **reported execution error** is a requested identity the reply named, with the status it reported.
It is retained with that status and counted once in the failure channel, exactly as a failure is counted today.

An **unresolved effect** is a requested identity whose effect the run cannot state.
It is retained by identity and counted separately from failures.
These two facts may describe the same identity and usually will: a named error carries an unresolved effect unless something independent of the reply establishes the outcome.

A **reported aggregate** is the reply's deleted and not-found counts, which name nobody.
It is credited to the run's counters only from a reply that passed the validation below, and otherwise retained as a diagnostic of that request and credited to nothing.

The effect rules that follow are these.
No reply alone establishes that a named object survived, so no identity is ever classified as surviving on the strength of a row.
On a validated reply, the requested names that no row named are covered by the credited aggregate, which is the service's report rather than an independent observation, and the record says so.
On an untrusted reply, every requested identity has an unresolved effect, including the ones the reply named.

The minimal public projection is defined here rather than deferred.
The existing failure counter keeps its meaning and counts each requested identity with a reported execution error once, never twice.
Unresolved identities are published as their own count with their own retained set, and are never presented as verified, as deletions, or as part of the failure count.
The verified counter is credited only from a validated aggregate; a diagnostic aggregate credits nothing, which is the specific change that stops an untrusted reply publishing claimed deletions as verified alongside the same names as failures.
The processed display is credited on the same condition.
A run holding any unresolved identity is not complete: it ends partial and its summary states the unresolved count alongside any failure summary, rather than hiding one behind the other, following the same pattern ADR 0023 sets for a partial local job.

An unresolved record is not authorization to delete again.
A lost or inconsistent reply does not prove that a later request would find nothing there, and a replacement object may have been created under the same name after a deletion that did in fact succeed, so a second destructive request could destroy something the operator never selected.
The accepted final-hop concurrency and authority constraints continue to apply, any future retry needs the independently accepted fresh authority and identity protocol, and no retry is introduced anywhere by this slice.
The reason to retain uncertainty is truthful reporting, not an assumption that repeating a destruction is safe.

## Identity validation on every reply

Validation applies to every reply, not only to one whose arithmetic failed, and path membership alone never makes a row authoritative.
Before a reply is used, it must satisfy all of the following.

1. Membership: every error row names a line this request sent. A row that does not is an anomaly, is retained as such, and never becomes a requested identity.
2. Uniqueness: no requested identity is named by more than one row. A repeat, or two rows that disagree about one identity, is an anomaly.
3. Status shape: every row carries a status that is present, well formed, and denotes a failure. A missing status, a status that cannot be parsed, or a status that denotes success is an anomaly, so a row cannot become an authoritative failure merely because its path matches.
4. Counts: the counts are non-negative, and deleted plus not-found plus the number of distinct validly named identities equals the number of names requested.
5. Overall status: the reply carries the success the middleware documents, or a valid row explains the failure. An overall status that is neither, with no row explaining it, is an anomaly.

A reply satisfying all five is validated: its named identities are reported execution errors, and its aggregate is credited.
A reply failing any of them is untrusted: its validly named identities are still retained as reported execution errors, every requested identity takes an unresolved effect, and its aggregate becomes a diagnostic.
This is what makes the two numerically reconciling shapes above safe, and it is why the passing control cannot be that a reconciling reply records no failure: a reply with one claimed deletion and one valid row for the second name is a valid partial reply that must retain exactly one reported error, without inventing certainty about that object's effect.

## Required proof

The implementation must show, on unchanged production first and then on the correction:

1. The crafted replies above, asserting the exact retained identity set and its kind for each, with the defective shapes failing before the correction and passing after it.
2. Rows that are out of request order, duplicated, conflicting, and naming a path never requested, each asserted separately rather than in combination.
3. Separately asserted controls for a valid all-success reply, a valid named partial failure, a numerically reconciling reply with duplicate rows, and a numerically reconciling reply with a foreign row.
4. Object-name encoding cases carried end to end without normalising the selected bytes: a space, a literal percent and percent-like text, a non-ASCII name, a newline-bearing name, and a name containing a slash.
5. Batch sizes of one, two hundred and one, and a size that crosses a chunk boundary, so the chunk arithmetic is exercised rather than assumed, including the boundary where the display sample caps and the stream does not.
6. Actual-service regressions on the owned mock and the owned Swift fixture, asserting the requested mutations that actually reached the service, the retained outcome for each requested identity, and unchanged collateral in both namespaces.
7. A service-level assertion for the mixed-count reply over the public job counters, the terminal state, the complete outcome stream and the audit trail, so the contract is proven where the operator reads it.
8. The streaming site in `deletePass` covered as well as the explicit-name site, since each defect exists once per site.
9. Controlled effect substitution through the public service and both callers: the same named-error reply where the deletion did reach the service and where it did not, proving the run classifies both identically as a reported error with an unresolved effect and never asserts survival in either.
10. Rows whose status is missing, malformed, or itself a success, and two rows disagreeing about one identity, each rejected as an anomaly rather than trusted because the path matched.
11. At least three deliberately broken variants, each failing its own check: one restoring the count-as-index assignment, one recording the echoed wire path as the name, and one dropping the foreign-row anomaly in silence. A variant that fails to compile is not a broken-guard result and does not count.
12. The full race suite with inventory reconciliation against the delivered name count, vet, gofmt, the Windows and macOS vets and internal builds on both architectures, and a clean module tidy.

## Remaining limits

This slice settles what a run knows about each requested name and how that is stated, and nothing else.
It does not make successful deletions individually identifiable, because the protocol does not report them, so a validated reply is still trusted by count.
It does not change the 200-record display cap, the segment-deleting helper that returns only counts, or the force flow that has no failure sink, which are the rest of DS-08.
The wire spelling of error rows is established here only against the owned mock and fixture; the real middleware's exact spelling on a production cluster is not established by this work, and the validation is written to treat an unrecognised row as an anomaly rather than to assume a spelling.
No native platform behaviour is claimed, since the pipeline is Linux only.
The design sequence paragraph and the audit register update for this finding are sequenced separately by the main session, so this document edits neither.
