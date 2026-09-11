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

A third probe established the control pair directly, on the same unchanged head, retained as `before-effect-pair.txt` with its source as `probe_effect_pair_test.go.txt`.
One name was requested twice under identical conditions, and the crafted reply was the same 144 bytes in both runs: no counts, and one row naming that name with a server error.
The two runs differed only in whether the request was replayed into the owned mock before the crafted reply was returned.

| Run | Reply | Run error | Recorded outcome | Target after an authoritative check | Collateral |
| --- | --- | --- | --- | --- | --- |
| Not forwarded | 144 bytes, one server-error row | none | one failure, that row | present | present |
| Forwarded to the mock | the same 144 bytes | none | one failure, the same row | absent | present |

The reply, the run error and the recorded outcome are identical while the object survived in one run and was destroyed in the other.
A reported execution error therefore carries no knowledge of the effect, which is what forces the contract below to keep the two apart.
The reviewer established the same result independently by controlled response substitution, and neither run is a claim about any production incident.

## Owned-service evidence for the reply shape

Two questions could only be settled against the owned Swift fixture, because the mock echoes whatever it was sent.
Direct wire probes answered both, and are retained with their exact requests and replies.

The returned error path names the requested object, but not necessarily in the spelling the request used.
A first probe suggested otherwise and an earlier draft of this document generalised it into a byte-for-byte echo rule, which is false.
That probe used a name carrying a space, non-ASCII and a literal percent, which both encoders escape the same way, so it could not distinguish the two rules.

    line sent:      /bulk%20probe-2462880%20h%C3%A9llo%25pct
    error row:      /bulk%20probe-2462880%20h%C3%A9llo%25pct

A second probe used punctuation that the request encoder leaves literal in a path segment and the service's reply encoder does not.

    stored name:    bulkprobe3-2696204 plus+and&equals=at@colon:dollar$
    line sent:      /bulkprobe3-2696204%20plus+and&equals=at@colon:dollar$
    error row:      /bulkprobe3-2696204%20plus%2Band%26equals%3Dat%40colon%3Adollar%24

The service decodes the line it was sent and quotes the name again more strictly for its reply, so one object has two spellings.
Comparing spellings therefore rejects legitimate rows: a reported failure for such a name would be discarded as foreign and the whole reply falsely called untrusted, losing both the reported facet and the credit for a valid partial aggregate.

The rule is consequently on the bytes a row names, not on how it is spelled.
A row is split at the first separator after the container, each part is percent-decoded exactly once, and the result is compared to the requested container and object.
Decoding one layer keeps percent-like text in a name intact, and a literal plus stays a plus because a path segment is not form data.
A row that does not decode, names another container, names something the request never sent, or arrives with whitespace around it is not one of ours and is an anomaly.

A partial failure carries a non-200 overall status explained by a row.
An earlier probe requested one existing object, one missing object and one non-empty container:

    reply:
      Response Status: "400 Bad Request"
      Number Deleted: 1
      Number Not Found: 1
      Errors: [["/bulkprobe-2461009", "409 Conflict"]]

A validated reply therefore cannot require an overall 200, and the accepted outcomes are the documented success or a failure a valid row explains.

One thing the fixture could not be made to do is return a per-object error row.
An over-long object name was answered as not found rather than as an error, and the account owns its own containers, so no authorisation failure is reachable.
The reliable error row there is a non-empty container, which is a container-level path and outside the deletion package's object API.
The returned-spelling evidence is consequently a wire probe rather than a package test, and that is stated rather than papered over.

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
The retained set is durable only as far as its retention path is: the complete list is written to a file opened before any destructive request, and the run keeps the exact identity count separately from any storage failure.
Where the file cannot be opened, or where any write, flush or close fails, the run offers no list at all and says the retained list is unavailable or incomplete.
It does not state how many identities were lost, because a buffered write that fails at the flush loses records the encoder already accepted and the recorder cannot establish that number.
Making that path dependable in every environment, or refusing to begin a destructive run without it, is a policy decision this slice does not take.
This retained-list failure promise assumes the identity index itself remains available.
The later [bounded identity-accounting contract](../adr/0036-delete-identity-accounting.md) preserves that promise and explicitly defines unavailable final totals when an index failure prevents accounting for issued requests.
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

## The sweep re-attempts an unresolved name

A prefix run sweeps in passes: it lists, deletes, and lists again while a pass still deletes something, and it re-submits whatever is still listed.
A name the service blamed is therefore submitted again on the next pass, and this slice records that name as unresolved, which is a destructive request issued for a name whose effect the run cannot state.

This is pre-existing behaviour, not something the ledger introduces, and the evidence says so plainly.
A probe on unchanged production at head 08b0d125, retained as `before-reattempt.txt` with its source, drove a valid partial reply claiming one deletion and blaming one of two listed names.
The unchanged sweep sent three bulk requests and submitted the blamed name in all three, raised no error, and retained three entries for that one name, each spelled as the echoed wire path.
What the correction changes is the record, not the requests: the same three submissions now leave one entry, under the object's own name, marked as an unresolved effect.

This slice deliberately does not change sweep semantics.
Suppressing a name from later passes, or stopping the sweep when an effect becomes unresolved, would alter when a deletion run stops and what a pass may retry, which the approved contract does not cover and which reaches beyond a reporting correction.
Recording the behaviour truthfully is within this slice; deciding the authority question is not.

Review ruled on this directly: the re-attempt is a pre-existing destructive-authority hazard rather than a retry this ledger introduces, and it may remain a separately recorded audit blocker while this bounded reporting correction is delivered.
That ruling is not permission to retry and not a claim that the sweep is safe.
An explicit audit follow-up is therefore carried for fresh authority and identity across replacement races, valuable-data acceptance is not cleared until it is resolved, and any change to sweep semantics needs its own approved contract.

## Required proof

1. The crafted replies above, asserting the exact retained identity set and its kind for each, with the defective shapes failing before the correction and passing after it.
2. Rows that are out of request order, duplicated, conflicting, and naming a path never requested, each asserted separately rather than in combination.
3. Separately asserted controls for a valid all-success reply, a valid named partial failure, a numerically reconciling reply with duplicate rows, and a numerically reconciling reply with a foreign row.
4. Object-name encoding cases carried end to end without normalising the selected bytes: a space, a literal percent and percent-like text, a non-ASCII name, a newline-bearing name, and a name containing a slash.
5. Batch sizes of one, two hundred and one, and a size that crosses a chunk boundary, so the chunk arithmetic is exercised rather than assumed, including the boundary where the display sample caps and the stream does not.
6. Actual-service regressions on the owned mock and the owned Swift fixture, asserting the requested mutations that actually reached the service, the retained outcome for each requested identity, and unchanged collateral in both namespaces.
Absence is established only by an authoritative not-found, and a survivor by reading its bytes back, so a transport or authentication failure cannot be read as a witnessed deletion.
7. A service-level assertion for the mixed-count reply over the public job counters, the terminal state, the complete outcome stream and the audit trail, so the contract is proven where the operator reads it.
The audit assertion names this job's exact terminal operation, resource, summary and result, so a start event does not satisfy it and a result outside the audit vocabulary cannot pass by accident.
8. The streaming site in `deletePass` covered as well as the explicit-name site, since each defect exists once per site.
9. Every deliberately broken variant rebuilt from its current source whenever a file it targets is rewritten. A variant whose subject has moved passes for the wrong reason and is indistinguishable from one that works, which is how two regressions were silently lost from this slice before a rebuild caught it.
10. Rows whose status is missing, malformed, or itself a success, and two rows disagreeing about one identity, each rejected as an anomaly rather than trusted because the path matched.
11. Controlled effect substitution through the public service and both callers, on both owned providers: the same substituted reply, once with the deletion forwarded and witnessed and once suppressed, proving the run classifies both identically and asserts neither outcome, with absence and survival witnessed authoritatively by an observer that never passed through the fault boundary.
The two worlds are compared on state, counters, summary, the terminal audit fact and the complete retained records, not on presence alone.
The buffered retention failure is carried through a public job as well, so the operator-visible result of a list that cannot be written is proven where it is read.
12. The complete retained identity set read back from where the run said it is and compared beyond the display cap, so a run that advertises a list it never wrote fails.
Each identity must appear exactly once, and each reason must be drawn from the reply that produced it: a blamed identity carries the status its own row returned and still refuses to state an effect, and every other identity carries what was wrong with the reply.
13. At least three deliberately broken variants, each failing its own check: one restoring the count-as-index assignment, one requiring the reply path to echo the sent spelling, and one dropping the foreign-row anomaly in silence.
Two further variants are maintained as controls for the assertions themselves: one that emits no terminal audit event and one that replaces every retained reason with a claim that the object survived.
Every variant is rebuilt from the current sources by one producer rather than kept as a copy, and a variant that fails to compile is not a broken-guard result and does not count.
14. The full race suite with inventory reconciliation against the delivered name count, vet, gofmt, the Windows and macOS vets and internal builds on both architectures, and a clean module tidy.

## Final review found unbounded identity storage

The rebased implementation at `2aeb9b8de2c684e402e930984d73860071c381bd`, based on delivered master `859ec53c53dbb00851bfbcc796b1a89706d07310`, retains every distinct failed and unresolved name in run-wide maps.
A generated sorted listing of N distinct names can receive a validated all-failure bulk reply for each request: zero deleted, zero not found and one valid failure row per requested name.
That reply does not return a request error, so the executor consumes the entire listing before its zero-deletion pass stops the sweep.
Both maps then contain N entries even though the displayed samples each stop at 200.
The parallel failure path also retains every distinct failed name.
This is a source-level growth proof, not an RSS measurement or a claimed out-of-memory execution.

The completed checks on that head include 13 intended negative controls, static/native/cross-build checks and a full race suite with 3,576 passing leaf tests, one documented `TestRcloneMatrix` skip and no failures.
The actual Go process exited 0 after 801.405 seconds, and all three required real-Swift deletion cases passed.
Those checks do not exercise or clear this resource-bound defect.
The range remains unpushed and requires the [ADR 0036 correction](../adr/0036-delete-identity-accounting.md), its new resource/storage-failure proof and renewed final-range review before delivery.

The subsequent [executed before probe](probes/2026-09-11-delete-memory-before_test.go.txt) exercises `DeletePrefix` on that same production parent using a generated HTTP provider.
The provider keeps scalar counts and an ordered deletion watermark, generates listing rows incrementally and buffers only the current request of at most 1,000 names.
The callback oracle compares each identity with its ordinal and retains no namespace or outcome set.

| Generated run | Names | Identities in each result map | Held Go heap delta | Outcome |
| --- | ---: | ---: | ---: | --- |
| All-success control | 50,000 | 0 | 52,040 bytes | Pass |
| All reported failures | 1,000 | 1,000 | 275,072 bytes | Below the generous witness bound |
| All reported failures | 10,000 | 10,000 | 2,199,536 bytes | Below the generous witness bound |
| All reported failures | 50,000 | 50,000 | 10,137,472 bytes | Intended bound assertion fails |

The structural witness allows 11,400 retained identities per map, more than a listing page, request and both display samples combined; it is not a proposed product memory budget.
Smaller passing cases do not establish a bound, because every failure case retains N entries in each map.
Both callback streams still carry all N exact identities and both display samples remain capped at 200.
The 50,000-failure run consumes five full pages and 50 validated all-failure bulk replies, followed by the bounded non-empty verification checks.
The all-success control models removal through the provider's watermark; this is generated HTTP evidence rather than actual Swift storage.

The pinned Go 1.27.1 race invocation exits 1 after 13.123 seconds solely because of the 50,000-failure assertion, with three passing leaves, one failing leaf, no skips and no race diagnostic.
The first invocation failed at sandbox socket setup and receives no behavioural-failure credit; the identical source then ran with permission for its owned loopback listeners.
Heap and sampled RSS are supplemental measurements rather than the pass/fail oracle: releasing the result reduces the 50,000-failure sample's heap to 284,496 bytes while RSS remains 104,724 KiB.
That release is still a measurement of the old code, not an after-fix result.
The archived source SHA-256 is `ac0c29bec220fab2d694ad640fc7311249d9d07eab273bf76f0a408ab12f56e8`, and the complete raw Go event stream SHA-256 is `f7f71cf2a4d27fb2bc43e9d4e0feb001172b4e401c7354b5d85946a2086cfa97`.
To reproduce it, use a disposable checkout of the named parent, copy the archived text to `internal/deletion/memory_before_probe_test.go`, and run `go test -mod=readonly -race -count=1 -parallel=1 -p=2 -timeout=110s -run '^TestDeletePrefixBeforeGenerated' ./internal/deletion` with the pinned toolchain and permission for its owned loopback listeners.
The expected behavioural exit is 1; an inability to build or open the listeners is not that result.

## Private index prerequisite

The new `internal/deletion/identity_index.go` implements ADR 0036's private storage prerequisite.
It is not yet called by either executor: the old maps, job completion and public interface remain unchanged at this step.
The parent range remains blocked until the coherent executor and public failure-path correction is implemented and reviewed.

The index binds exact name bytes and the outcome facet to a binary primary key, on one reserved SQLite connection in an invocation-owned private directory.
Each call accepts at most 2,000 facets and returns insertion marks only after a checked transaction commit.
Any accounting error discards the pending marks and stops future accounting on that index.
The executor integration must additionally enforce at most 1,000 names per bulk request or one parallel request's outcome; the facet cap alone is not that admission rule.

Twenty focused leaf cases pass with pinned Go 1.27.1 using `go test -mod=readonly -race -count=1 -parallel=3 -timeout=3m -run '^TestIdentityIndex' ./internal/deletion`.
They cover exact binary identity and separate facets, repeated names within and across transactions, bounded batches, effective connection settings, invalid cache locations, private files, checked cleanup and refusal after a lost connection.
Real SQLite failures include a constraint error after a successful insert, an owned database page limit producing `SQLITE_FULL`, a missing table and a reader lock that permits insertion but prevents commit.
The page-limit fixture exercises SQLite allocation failure without filling the host filesystem; it is not a host ENOSPC or power-loss experiment.
Removal failure uses permissions on the fixture's own directory and is explicitly skipped on Windows or when root would bypass those permissions; neither skip occurred on this Linux run.

A per-index test barrier cancels after successful inserts have been read back inside the transaction and before commit.
The cancelled case returns no insertion marks, retains only the previously committed row and refuses later accounting, while the uncancelled control commits both rows.
Both cases also passed 100 race-instrumented repetitions.
The barrier is unset by default and does not replace the driver, transaction or filesystem with a mock.

The focused run exits 0 after 3.328 seconds with 20 passing leaves and no failures or skips.
The repeated cancellation run exits 0 after 10.384 seconds with 200 passing leaves.
The separate race/coverage run exits 0 and covers 96 of the index file's 116 statements, or 82.8 percent; setup, setting-readback and some driver close/error branches remain uncovered.
`go vet -mod=readonly ./internal/deletion` and `git diff --check` also pass.
These are prerequisite checks, not new full-suite or integrated-engine evidence.

Three private mutants were rebuilt from these final source files, changing only the indicated guard.
The unmodified baseline passes all three selected tests with exit 0, and each mutant exits 1 solely at its intended assertion, without a build, setup, timeout, race or skip failure.

| Deliberately broken guard | Catching test | Observed assertion |
| --- | --- | --- |
| Mark every insertion new | `TestIdentityIndexPreservesBinaryNamesAndFacets` | Duplicate facets incorrectly return true |
| Omit the checked commit | `TestIdentityIndexCommitFailureStopsAccounting` | The blocked-commit request incorrectly returns a true mark and nil error |
| Omit the accounting-failure latch | `TestIdentityIndexRollsBackAndStopsAfterFailure` | Later accounting incorrectly resumes after the constraint failure |

The [isolated resource probe](probes/2026-09-11-delete-index-resource_test.go.txt) uses the same final index implementation, streams 1,000 generated 1,024-byte names and both facets per transaction, and repeats the full accumulated range after each growth step.
Its owned cache is on XFS with 4,096-byte filesystem blocks; `/tmp` is overlayfs and was not used for the index.
The process has no race instrumentation for this memory observation; the focused race tests are separate evidence.
Values below are measured after the repeated pass and an explicit Go GC.

| Distinct names | Indexed facets | Held Go heap | Process RSS | SQLite logical file size |
| ---: | ---: | ---: | ---: | ---: |
| 10,000 | 20,000 | 946,056 bytes | 21,245,952 bytes | 93,294,592 bytes |
| 50,000 | 100,000 | 1,001,960 bytes | 21,934,080 bytes | 466,702,336 bytes |
| 100,000 | 200,000 | 1,056,544 bytes | 22,749,184 bytes | 933,384,192 bytes |

Every repeated pass adds no rows or file growth.
Each open checkpoint reports one connection, eight descriptors and three goroutines; checked close removes the owned directory and returns those counts to zero connections, seven descriptors and two goroutines.
The probe exits 0 after 8.169 seconds.
The approximately 933 MB disk cost for 100,000 long names is material; the index moves distinct membership to storage and does not make its storage cost constant.
These observations do not establish a global memory ceiling, peak journal size, crash durability or the complete executor's resource behaviour.

For reproduction on Linux, copy the archived resource probe into `internal/deletion/resource_probe_test.go` in a disposable checkout, set `AERONAUTES_INDEX_PROBE_CACHE` to a new owned absolute cache directory, record its filesystem backing and run `go test -mod=readonly -count=1 -timeout=5m -run '^TestIdentityIndexResourceProbe$' -v ./internal/deletion` with the pinned toolchain.
The executed probe source SHA-256 is `cf6f5493a142aa05a96008a07a55e85dbd90b5e15c25019f64b708d16cd32a90`; its raw output SHA-256 is `5135691141b1f328b6e03ab2c95de225ca52bd82c44a5dafe875d0bbca89e7c1`.
The final implementation and maintained test source hashes are `18931379a286694329bb8aa54811731177ff816f389b082cddcd503dc5b451f6` and `3cc3de821d7e01b781dd660ed134073a8ae540b441f526276a7d06b7333a7e16`.

## Remaining limits

This slice settles what a run knows about each requested name and how that is stated, and nothing else.
It does not make successful deletions individually identifiable, because the protocol does not report them, so a validated reply is still trusted by count.
It does not change the 200-record display cap, the segment-deleting helper that returns only counts, or the force flow that has no failure sink, which are the rest of DS-08.
The wire spelling of error rows is established here only against the owned mock and fixture; the real middleware's exact spelling on a production cluster is not established by this work, and the validation is written to treat an unrecognised row as an anomaly rather than to assume a spelling.
No native platform behaviour is claimed, since the pipeline is Linux only.
The audit-register disposition remains separately sequenced and no finding closure follows from this experiment alone.
