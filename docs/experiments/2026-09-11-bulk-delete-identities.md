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
At the prerequisite commit, neither executor called it: the old maps, job completion and public interface were unchanged.
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

## Integrated executor and caller correction

The coherent ADR 0036 implementation replaces both run-wide maps with the checked private index.
The dispatch guard stops new requests after an unindexable outcome, joins already-admitted workers and returns their bounded attempt records separately from distinct identity counts.
Index setup refusal dispatches no destructive request; failed accounting after dispatch is partial even with zero credited deletions.
A checked close or removal failure stops completion without making successfully recorded totals unavailable.
Disk exhaustion never falls back to namespace-sized memory or a new cleanup sweep.

Deletion jobs and all four benchmark deletion calls retain the result and stop follow-up destruction after an accounting-storage error.
The forced DLO caller also discloses that its manifest was already deleted before segment accounting failed.
The optional counters/segment-result qualifier, generated Wails bindings, drawer and history distinguish unavailable final totals from last-confirmed progress.
Deletion and benchmark completion reuse the existing version-zero `jobs.Finish` transaction for counters, qualifier, reason, state and the controlled terminal audit.
No item-record version, durable recovery ledger or new deletion authority is introduced.
Healthy parallel reported-failure classification remains unchanged; its separate unresolved stream remains DS-08 work.

### Failure boundaries and public outcomes

The combined focused race run on the implementation's exact working-tree sources passed 91 leaf tests: 49 app, 38 deletion and four public system cases.
The actual Go process exited 0 after 16.138 seconds, with 539 complete JSON events, no skipped or failing test, no stderr and no race diagnostic.
This includes the 20 prerequisite index tests; those are not counted again as newly added integrated cases.
The separate focused race/coverage run passed and measured `accounting.go` at 77/83 statements (92.8 percent), the attempt writer at 31/31, the shared caller reason at 6/7 and the prerequisite index at 96/116.
These percentages describe the named files under the selected tests, not application-wide or native-platform coverage.

| Boundary | Maintained tests | Required observation |
| --- | --- | --- |
| Cache setup | `TestDeletionIndexSetupRefusesDispatch`, `TestDeleteAccountingStorageLifecycle` | Both public executors and the delete job refuse destructive dispatch; exact selected objects survive. |
| Bulk index write and repeated identities | `TestDeletionAccountingBulkAdmittedFailure`, `TestDeleteAccountingFailureIsPublic` | Earlier committed facets stay exact; only the held request enters separate attempt evidence; no later batch, sweep or verification is issued. |
| Parallel queue and drain | `TestDeletionAccountingParallelQueuedFailure` | Two admitted outcomes are retained and four buffered names are never dispatched after the latch; the healthy control preserves the existing classification. |
| Caller cancellation after effect | `TestDeletionAccountingCancelledBulkOutcome`, `TestDeleteCancellationRetainsAdmittedOutcomes` | Independently observed effects do not disappear when the response is lost; healthy indexed unresolved names and unindexed admitted attempts remain distinct. |
| Evidence storage | `TestRetainDeletionAttempts*`, `TestWriteDeletionAttempts*`, `TestDeleteJobDisclosesAnUnretainableUnresolvedSet` | Exact UTF-8 identities/details, checked buffered writes and close, explicit loss when unavailable, and unchanged exact counts for ordinary retained-list failure. |
| Deletion and benchmark completion | `TestDeleteAccountingCompletionIsAtomic`, `TestBenchmarkAccountingCompletionIsAtomic` | Separate counters, reason, state and audit refusal triggers leave the prior durable row unchanged after reopening; Get/List/History report unavailable rather than an unqualified terminal result. |
| Benchmark callers | `TestBenchmarkDeletionAccounting` | All four call sites stop on setup or indexing failure, retain the original problem, avoid a second sweep/container delete and withhold recommendations; all four healthy controls preserve existing measurement/cleanup behaviour. |
| DLO caller | `TestDLOAccountingErrorReportsManifestEffect` | Setup and indexing failures disclose the gone manifest; incomplete attempts are retained separately; an audit-write failure is included in the returned error and no applied audit is emitted. |
| Index cleanup | `TestDeleteAccountingStorageLifecycle/cleanup` | Obstruction after the final verification response preserves two credited deletions and the complete-accounting flag while refusing container deletion and disclosing removal failure. |
| Rendered result | `JobsDrawer.test.ts` | Recent and older history show the saved reason; an explicit incomplete flag labels progress, suppresses an unqualified failed total and hides numeric verification; absent/false historical flags preserve existing rendering. |

The indexed-storage tests use owned real SQLite failures and HTTP response barriers rather than substituting a fabricated executor result.
The cleanup obstruction uses a Unix rename of the fixture's own open index parent after all outcomes committed; that particular test explicitly skips Windows.
The attempt writer's ENOSPC evidence uses `/dev/full` for buffered flush and larger writes, and a closed owned file checks that flush/close errors are joined.
It does not establish a naturally occurring close-only fault, power-loss durability or native-platform acceptance.
The focused frontend run passed 50 tests, `svelte-check` reported zero errors and warnings, and the production build succeeded.
Pinned Wails beta.16 binding generation completed with CGO enabled and no warnings; the earlier CGO-disabled invocation's native-symbol warnings receive no clean-generation credit.
Native vet/build, the no-CGO internal build, Windows/macOS amd64/arm64 internal vet/build, module tidy, gofmt, goimports and diff checks pass.

The eight-case engine setup reproduction had four intended before failures and four passing controls.
The four public delete-job before cases lost the required qualifier/reason or offered unqualified retained-list claims; all four passed after integration.
The three rendered-result before cases failed their intended label/history assertions and pass after integration; the other eight tests were unselected in that before run.
Restoring only the old benchmark caller makes all eight storage-fault cases attempt a forbidden container delete, while all four healthy controls still pass.
Private admission and drain mutations separately produce six retained attempts instead of two, and one instead of two; their healthy controls pass.
Those two mutants stop at the intended attempt-set assertion, so later wire/observer assertions receive no negative-evidence credit.
The 13 pre-existing reply, encoding, retention, audit and hook-race controls were rebuilt for the integrated source: all 13 fail at their maintained intended assertions and all 11 unique unchanged baselines pass.
The intentionally unsynchronised hook control includes its expected race; passing production runs do not.

Initial socket/formatter setup failures, a test helper compilation mistake, and corrected test assumptions about an empty terminal listing page or an invalidated bookmark observer are preserved as failed development attempts.
They are not counted as behavioural reproductions or successful verification.
The final local suite is recorded below; the current-head/current-base five-job PR run, protected merge and resulting-master checks remain separate required delivery gates.
The earlier full-suite result on the map-based implementation is historical and does not satisfy them.

### Maintained public system matrix

| Public action | Required scenario | Maintained test | Observed boundary and required outcome | Evidence scope |
|---|---|---|---|---|
| `DeleteService.StartDelete` | `partial` | `TestDeleteAccountingFailureRetainsIssuedNamesMock` | A real first bulk request deletes its one named object; the owned HTTP wrapper withholds its response, installs a refusal trigger in that invocation's private identity index, and sends an untrusted reply. The public job remains partial with zero credited, verified and reported-failure progress plus `deletionAccountingIncomplete=true`; final identity totals are explicitly unavailable. A separately labelled JSONL file retains exactly the issued name and available detail. No later request, sweep, verification or container DELETE is issued, two selected but unissued objects survive, and both account namespaces have no collateral change. | Linux Go service/native-deletion engine against two owned HTTP mocks; receipt-based partial credit; no real-Swift or native-UI claim. |
| `DeleteService.StartDelete` | `partial` | `TestDeleteAccountingFailureReportsLostEvidenceMock` | The same admitted effect and index failure occur while only the separate attempt-evidence directory is obstructed by an owned regular file. The public partial job retains the incomplete-totals qualifier and explicitly says the issued identities could not all be retained, without claiming a complete list or an available attempt file. Later destruction remains unissued and both account namespaces retain the same exact delta. | Linux owned-cache/open-failure control; no universal retention-durability claim. |
| `DeleteService.StartDelete` | `partial` | `TestDeleteAccountingHealthyIndexRetainsUnresolvedMock` | The same real first effect and untrusted reply with a non-firing index trigger retain a known unresolved count of one and a normal retained-list reference, without setting the incomplete-accounting qualifier or producing admitted-attempt evidence. Later requests remain stopped and the independent namespace delta is unchanged. | Matched healthy-index control; the public count and retained-list reference are asserted, not a new normal-writer durability proof. |
| `DeleteService.StartDelete` | `happy` | `TestDeleteAccountingHealthyContainerDeleteMock` | The non-firing trigger and original trusted replies allow all three one-name bulk requests, exact three-object credited/verified progress, successful container deletion and no attempt-evidence file or incomplete-accounting qualifier. Primary and secondary sentinels remain unchanged. | Matched successful public container-deletion control. |

These tests bind the failure claim to the existing `StartDelete/partial` system cell using the actual public service.
Their raw-HTTP oracle checks both throwaway account namespaces and modelled object/container identity; it is not a complete account, recovery or native-platform witness.
The focused mock-only scenario reconciliation is valid with the existing 116-cell denominator and two selected-run cells, `StartDelete/happy` and `StartDelete/partial`.
Combined-suite coverage must come from the complete producer's own terminal-event reconciliation.
Three private system mutations are caught: failed instead of partial (two failures), a queued name substituted for the issued identity (one failure), and a later container DELETE (two failures).
Both healthy controls pass in all three mutation runs.

### Integrated resource observations

The [executed consumer probe](probes/2026-09-11-delete-consumer-resource_linux_test.go.txt) uses the unchanged public executors with generated 115-byte ordinal names.
It streams pages of at most 10,000 rows, bounds bulk requests at 1,000 names and uses scalar callback/order oracles rather than another namespace-sized set.
The `DeleteNames` baseline includes its real caller-owned selection, kept alive throughout measurement and checked unchanged after return.
The held sample occurs inside `OnProgress` after the outcome transaction commits and before the index closes, with explicit GC and process RSS sampled before opening the separate SQL observer.
That observer checks exact committed facets one row at a time and closes its rows, reserved connection and database.
Its connection-local settings are not presented as the writer's settings; the unchanged private index and prerequisite proof establish those.

| Held consumer | Names | Held Go heap | Held process RSS | SQLite logical bytes |
| --- | ---: | ---: | ---: | ---: |
| Prefix, bulk failures | 1,000 | 683,928 | 20,918,272 | 278,528 |
| Prefix, bulk failures | 10,000 | 3,208,160 | 27,332,608 | 2,666,496 |
| Prefix, bulk failures | 50,000 | 3,244,576 | 29,712,384 | 13,312,000 |
| Prefix, bulk zero-facet control | 50,000 | 3,206,128 | 29,859,840 | 8,192 |
| Prefix, parallel failures | 1,000 | 558,064 | 22,040,576 | 143,360 |
| Prefix, parallel failures | 10,000 | 598,104 | 28,340,224 | 1,421,312 |
| Names, bulk failures | 50,000 | 7,789,496 | 39,485,440 | 13,312,000 |

The last row's caller-selection baseline is 7,592,824 heap bytes, including 50,000 names containing 5,750,000 name bytes.
There is no second run-sized selection or oracle collection.
The index cache is on an owned XFS directory with 4,096-byte filesystem blocks, verified both outside and inside the test process.
At 50,000 bulk-failure names its 100,000 facets occupy 13,312,000 logical and 16,777,216 allocated bytes.
The earlier 1,024-byte-name prerequisite still demonstrates the materially larger 933,384,192-byte disk cost at 100,000 names; this shorter-name experiment does not replace it.

Bulk cases cover 1,000, 10,000 and 50,000 failures for both entry points, with 50,000-name zero-facet controls.
Parallel cases cover 1,000 and 10,000 failures for both entry points and both 50,000-name zero-facet controls.
Eight engine workers use a serial response acknowledgement schedule so the exact callback oracle remains scalar; these are not peak-concurrency memory measurements.
The bulk held stack still has its listing page; the parallel stack may already have released its last page, so their absolute heap values are not a like-for-like peak comparison.
The parallel 10,000-failure calls took 164.183 seconds for Prefix and 165.496 seconds for Names with one FULL-sync transaction per indexed outcome.
A 50,000-failure parallel point was deliberately omitted after observing that cost.
Elapsed times include the fixture, forced GC and SQL observer, and are not performance acceptance thresholds.

Three-pass controls revisit 10,000 failing bulk identities and 1,000 failing parallel identities, with one credited sentinel each pass so the existing sweep continues.
They publish each applicable failure/unresolved callback once, keep the same exact facets and file size at every checkpoint, and do not assert renewed destructive authority over the revisited names.
Every successful public return leaves no invocation-owned index entries or file descriptors before fixture cleanup.
The nine bulk and seven parallel non-race cases passed in 10.871 and 384.762 seconds respectively, with no skips, timeouts or stream/lifecycle failures.
Four 1,000-name cases also passed under race instrumentation using the final probe source in 43.094 seconds.
No repository or CI time budget was changed for these private experiments.

The source SHA-256 is `cf36b95acb8af14e815bfec0beefe80e30f43319d6192c4800b5cee706163cae`.
Raw bulk, parallel and final race streams have SHA-256 `9ceb98814801f4265e3004123416c3a31e7f79c82fb5022c731d40232a58aa31`, `88af8893d4c596ffda89278fda3452c09c54912b7f8134c9348d3b840b637aa7` and `d09998ce0481d75a17726783a4c29e7f90f780892a413d1782baa17cda74f41f` respectively.
To reproduce on Linux, copy the archived text into `internal/deletion/consumer_resource_linux_test.go` in a disposable checkout, set `AERONAUTES_CONSUMER_CACHE` to a new owned absolute XFS cache directory, and run pinned Go 1.27.1 with `go test -mod=readonly -count=1 -parallel=1 -timeout=10m -run '^TestDeletionIntegratedResource$' -v ./internal/deletion`.
The original before probe remains byte-identical to its committed archive; this consumer probe changes its observation point and does not pretend to be the identical before test.
These finite measurements and source checks establish the inspected collection bounds, not a universal asymptotic proof, a hard process-memory ceiling, peak journal size or durable recovery guarantee.

## Final local verification

The coherent implementation at `9106a26c50e6cecc4e8433a2de8236a97c797e9b`, based on delivered master `859ec53c53dbb00851bfbcc796b1a89706d07310`, passed the complete local race suite on 11 September 2026.
Pinned Go 1.27.1 ran `go test -mod=readonly -race -count=1 -parallel=3 -timeout=25m -json ./...` against the owned Swift fixture on port 18081 with the checked official rclone 1.74.3 binary.
The producer exited zero after 836.128 seconds, within the unchanged package and process budgets.
Before and after checks found the same clean head and identical tracked source hashes.

Complete event-lifecycle reconciliation found 3,666 passing leaves, no failures and one explicitly skipped `TestRcloneMatrix`; executed slash-separated ancestors are excluded from the leaf count.
Twenty-two packages passed and the separate `cmd/mockswift` package reported no test files.
The skip is the optional characterization matrix, not an omission of the maintained real-engine or real-Swift regressions.
The required real-Swift bulk encoding, executor effect-substitution and public-service effect-substitution tests all passed.
The raw stdout SHA-256 is `21bbcc5f3c5a88ba6c7bfe5f17758f1ae2b6e7a8ffc2e7b58a7c44aef0e4f45d`; stderr is empty.

The maintained scenario reconciler consumed that complete stream, the producer's actual exit and the independently listed package set.
It exited zero with schema version 3, `valid:true`, 41 of 116 cells covered and 38 actions with gaps; `coverageComplete` remains false.
The report SHA-256 is `227f34f9cfe0cd1ef341e1ca4405c2f3f4ab04917efa32305faeec10c61f7a05`.
The complete frontend suite also passed on this unchanged implementation: 1,141 tests in 29 files, no failures or pending tests, and an actual zero exit after 11.545 seconds.
These results supplement the focused storage-failure, resource, UI, binding and static evidence above; they do not replace native acceptance or the five-job PR and resulting-master checks.

## CI cancellation observer correction

[PR94 run 34629240691](https://github.com/csmart/aeronautes/actions/runs/34629240691) failed the Go job at `TestIdentityIndexCancellationAfterInserts/cancelled_true` on `fa11097c290e74e4007c69c0991d0948bc0eaa86`.
The frontend, smoke and real-Swift integration jobs passed; the aggregate gate correctly failed.
An unchanged-source local race run reproduced the same two-row assertion in two of 200 cancelled cases, with all 200 healthy controls passing; the actual Go exit was one.

The old observer queried the writer's pinned connection, which can still see its uncommitted insertion while automatic context rollback is pending.
Go 1.27.1 marks the transaction done before the driver rollback runs, so a competing rollback can return `ErrTxDone` without joining it.
The correction opens an independent reader using the existing private `mode=rw` URI and checks its query and close errors.
Before cancellation, the maintained test now requires the transaction to see two rows while the independent committed view sees only the previous row.
That assertion deterministically fails with the old observer in both cases; the corrected observer passes 200 healthy and 200 cancelled cases under the race detector.
The test still requires no published insertion marks, one committed row after cancellation and refusal of later accounting.
No production code, sleep, retry, count expectation or CI budget changes.

A separate owned probe using the unchanged production index and the pinned Go/SQLite sources held the real SQLite rollback callback.
Production connection close remained blocked and the index file remained present until rollback was released; close then returned nil and removed the private directory.
Its three leaf cases passed under race instrumentation.
This establishes the observed driver-rollback and connection-release ordering, not a checked rollback result from Go's automatic rollback path, a hard filesystem interruption deadline or every goroutine's final exit.

The corrected test file has SHA-256 `292e6b4d110b8c82f5e5481dfdd2ab3821355eed7cb0970b8a1bad52e69b9cd9`.
The whole deletion package then passed `go test -mod=readonly -race -count=1 -parallel=3 -timeout=3m -json ./internal/deletion` with the same owned Swift and pinned tools: actual exit zero in 14.173 seconds, 110 passing leaves and no failures or skips.
Both maintained real-Swift groups passed, source hashes stayed unchanged during verification, and gofmt, goimports, package vet and `git diff --check` were clean.
The package stdout SHA-256 is `2b086e29cd19c145b54779e20cbd670e86f6520519d535d9741f2571342f97ae`.
The earlier complete application suite is evidence for the unchanged production implementation; this test correction still requires independent review and all five jobs on its new PR head before merge.

## Remaining limits

This slice settles what a run knows about each requested name and how that is stated, and nothing else.
It does not make successful deletions individually identifiable, because the protocol does not report them, so a validated reply is still trusted by count.
It does not change the 200-record display cap or provide a complete ordinary reported-failure sink for forced segment cleanup, which remain DS-08 work.
The DLO accounting-failure path now discloses incomplete segment totals and bounded admitted-attempt evidence; that exceptional path is not a complete ordinary failure ledger.
The wire spelling of error rows is established here only against the owned mock and fixture; the real middleware's exact spelling on a production cluster is not established by this work, and the validation is written to treat an unrecognised row as an anomaly rather than to assume a spelling.
No native platform behaviour is claimed, since the pipeline is Linux only.
The audit-register disposition remains separately sequenced and no finding closure follows from this experiment alone.
