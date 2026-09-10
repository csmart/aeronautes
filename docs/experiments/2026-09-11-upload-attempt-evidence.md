# Upload staging attempt evidence

## Scope and oracle

The bounded question is what an extra observed attempt on a staging identity actually was.
When the recording proxy lists one staging PUT identity twice where exactly one attempt is expected, the retained trace cannot say whether the first attempt was rejected before it applied, whether it applied and its response was lost, or whether two writes were applied.
This plan makes those cases separable, and it changes no assertion.

The request does not travel from the fixture to the engine directly.
The runner starts an invocation-local gateway for each remote before invoking the child, so the path is child, then gateway, then fixture, then service.
That gateway turns an upstream transport error into an HTTP 502 answer to the child, so a response aborted at the fixture is a transport failure on one hop and an HTTP error on the next.

Four facts must therefore be recorded separately, because collapsing any two of them is how the current trace loses the answer.

1. The service-to-fixture outcome, meaning the status the fixture obtained or the transport failure it hit.
2. The fixture-to-gateway outcome, which is either a status or an explicitly aborted response carrying no status at all.
3. The gateway-to-child outcome, recorded only where it is observed, and labelled unobserved otherwise rather than inferred from the injected fault.
4. Whether the write applied, witnessed independently of the recorder under test by the harness oracle client, which builds its own direct session and does not pass through the proxy or the stored bookmark endpoint.

The downstream record needs an explicit absent-response outcome rather than only an integer status, because an aborted response has no status code.
The gateway is not modified by this experiment.
The applicable contract is the exact upload mutation identity and attempt accounting in the item-record system tests, which stays as it is.
This settles nothing about why a hosted runner rejects a request, nothing about the separate copy binding case, and nothing about platform acceptance or the valuable-data hold.

## Source finding

A master integration job failed `TestUploadItemRecordsRealSwift/native_false_failed_401` on differing mutation identities or attempt counts, and the same tree passed on the retry, as the [intermittent failure record](2026-09-08-integration-time-budget.md#2026-09-10-intermittent-master-test-failures) sets out.
Comparing the observed and expected maps gives 805 distinct request identities on each side with no missing or extra identity, and exactly one identity observed twice, so 806 observed attempts against 805 expected.
The doubled identity was `f-0164-étude�.bin`, a staging PUT under the job's own batch prefix, which the fixture forwards rather than answering itself.

The front handler appends the method and path to its write list before forwarding, and keeps no outcome on any hop and no applied-effect identity.
A rejected attempt, an applied attempt whose response was lost, and two applied writes are therefore identical in the retained trace, and the final object cannot separate them because two writes of the same bytes to the same staging key leave the same final state as one.

Two retry layers exist above that request, and which one acts depends on the fault class and on the hop that fails.
The engine is invoked with one retry and two low-level retries whenever those options are unset, which is emitted configuration rather than a proven bound.
The upload run additionally retries a whole engine group once when the result is an authentication failure, forcing reauthentication first.
Neither layer establishes that a single transient upstream failure yields exactly one extra request for one identity, so this plan measures counts under controlled faults instead of asserting a budget.

## The target identity and its baseline expectations

The chosen identity is `f-0164-étude�.bin`, one of the selected names, and its staging path is the exact request path each fault arms on.
Its live name must be independently absent in the fresh owned namespace before any control runs.
Every control states its own expectations rather than reusing another control's, because the injected fault changes what the run should produce.

Under the unfaulted 401-item case the expectations are these.
The live PUT for a selected name is answered by the fixture with 403 without forwarding, so that name has no live object.
Its staged object exists with the seeded body bytes, and the item record is failed with an unresolved effect, commit stage and HTTP 403, with no verification.
The separate `z-engine.bin` is the one live success, its staged object is deleted after commit, and its record is copied and verified.
The audit summary counts the selected names as failed, unresolved and problematic.

A permanently rejected staging write does not leave those expectations intact.
When an engine group's copy fails, every item in that group is recorded failed with an unresolved effect at the engine stage, and the live commits for that group are skipped.
All the seeded files sit in one local directory and therefore in one engine group, so a permanent staging rejection is expected to remove the live `z-engine.bin` object and change every item record and the audit summary.

## What the record must capture

For every request through the front handler, in addition to the existing method and path list, which keeps its present shape and contents:

1. A monotonic sequence number, so attempts on one identity can be ordered against the barriers below.
2. The service-to-fixture outcome: answered by the fixture without forwarding, forwarded and answered by the service, or forwarded and failed in transport.
3. The service status code when one was received.
4. The fixture-to-gateway outcome, as a status code or as an explicit aborted response with no status.
5. The gateway-to-child outcome where it is observed, and otherwise an explicit unobserved marker.
6. The elapsed time of the request.

The existing write list, request counter and every assertion over them stay exactly as they are, and the new record is additional evidence rather than a replacement.
No production file changes, and the attempt-count assertion keeps comparing exact identities and exact counts, because it is the guard against a duplicated write.

## Controls

Each control names its fault, its expected request shape, and its expected staged namespace, live namespace, item records, counters and audit.
Every control keeps the existing collateral checks: unchanged namespaces on the untouched account, unchanged local bytes, and no writes outside the approved set.
Every witness is gated, meaning no later attempt on the target identity may reach the service until the witness for the attempt under examination has completed, and the barrier and attempt ordering are recorded so each witness is attributable to one attempt rather than to the name.

1. Applied effect, no fault.
   Every staging identity is expected exactly once, forwarded, answered by the service with success and returned unchanged to the gateway.
   The baseline expectations above apply in full.

2. Rejected before applying, then retried.
   The fixture answers the first attempt on the target staging path with a service-unavailable status without forwarding it, and holds that refusal until the independent witness has read the staged name and found it absent, blocking any further attempt on that path in the meantime.
   Without that gate the engine could create the object before the read, and a present object would be misattributed to the rejected attempt.
   The witness must find the staged name absent, which is what makes this a rejection rather than a lost response, and the target's live name must also still be absent.
   Later attempts are then passed through, so that identity is expected more than once, with the first recorded as answered by the fixture and not forwarded.
   Because staging eventually succeeds, the staged object, item records, counters and audit are expected to match the baseline, and every other identity is expected exactly once.

3. Rejected throughout.
   The fixture answers every attempt on the target staging path without forwarding.
   The measured number of attempts is recorded as the count for this fault, not as a general budget, under a predeclared limit of twenty attempts on that path or one hundred and twenty seconds for the run, whichever comes first.
   Exceeding that limit is reported as an incomplete observation of an engine that did not terminate under this fault, and never as a completed outcome.
   The expectations differ from the baseline by design: no service success for that identity, its staged object absent, and, because the whole engine group fails, every item recorded failed and unresolved at the engine stage with the live commits skipped, so the live `z-engine.bin` object is absent and the audit reflects the group failure.

4. Applied, then its response lost.
   The fixture forwards the first attempt on the target staging path, waits for the service answer, and holds that answer at a barrier while blocking any further attempt on that path.
   The independent witness then reads the staged name directly and must find it present with the seeded bytes, which attributes application to that attempt, and reads the target live name, which must still be absent while the fixture shows no live commit forwarded for it.
   Only then does the fixture abort the response to the gateway and release the barrier, so the caller sees a failure for a write that in fact applied.
   The record must carry the service success, the aborted fixture-to-gateway response and the child-observed outcome, or an explicit unobserved marker, as separate facts on that attempt.
   The effect of that attempt is retained as applied because the witness saw it, and the effect of any later attempt on the same identity is retained as unresolved unless a gated witness can separate it.

   Its final expectations are predeclared for each engine outcome the control allows.
   If staging eventually succeeds, the baseline contract holds, meaning the selected name keeps its live 403 and absent live object, `z-engine.bin` is the live success with its staged object deleted, and the item records, counters and audit match the baseline, with the target's attempts recorded separately.
   If the engine group fails instead, the live commits are skipped and the group is recorded failed and unresolved at the engine stage, while the already-witnessed target staging object is retained rather than absent, which is what distinguishes this outcome from control three.
   If the predeclared limit or deadline is reached, the run is an incomplete observation and is reported as such, never as one of the two completed outcomes.
   Each branch checks the exact permitted namespace delta, the execution and verification facets, the counters and the audit, and an unexpected live publication or an omitted item fails the control even when the first attempt was classified correctly.

5. Negative control that confuses attempts with effects.
   A deliberately wrong classifier that treats an aborted fixture-to-gateway response as proof that nothing applied is run against control four and must fail it, because the gated witness recorded the application that the classifier denies.
   The classifier must target that precise hop and effect confusion, and a variant that merely drops an outcome field is not sufficient, since controls one to three can pass without it.

## Required proof

Controls one to four run against both the containerised Swift fixture and the mock, and each produces its stated request shape, hop outcomes, namespaces, records, counters and audit.
The unmodified 401-item case runs separately with the instrumentation in place and every existing assertion unchanged, and its per-request record is retained.
The negative control fails control four.
Every producer is retained with its command, the engine binary and version, the emitted arguments, the injected fault, the barrier and attempt ordering, the measured counts and the exit result.

## Sequence

1. Review this plan before changing any test.
2. Extend the recorder and add the gated fault switch and barriers, in test files only, with the existing lists and assertions untouched.
3. Run the controls and the unmodified case, and retain every record.
4. Record the observed result, and state it as unresolved if the evidence cannot establish what the original extra attempt was.
5. Only then decide whether any test or production change is warranted, in its own reviewed commit.

## Observed result, partial

These runs are a partial execution of the approved proof.
They establish the target identity's behaviour under each fault and they do not complete the collateral, whole-inventory or sensitivity gates, which stay pending and are named below.
Each control ran once under the race detector, with the pinned engine on the path and the two-account fixture configured, on the mock and on the containerised Swift fixture, and both harnesses produced the same shapes.

| Control | Target staging writes | Service outcome | Response to the gateway | Gated witness | Staged after | Live control after | Target item |
| --- | ---: | --- | --- | --- | --- | --- | --- |
| Applied, no fault | 1 | 201 | 201 | not armed | present | present | failed, unresolved, commit, 403 |
| Rejected then retried | 2 | fixture 503, then 201 | 503, then 201 | staged absent at the refusal | present | present | failed, unresolved, commit, 403 |
| Rejected throughout | 2 | fixture 503 twice | 503 twice | staged absent at both | absent | absent | failed, unresolved, engine |
| Applied then aborted | 1 | 201 | aborted, no status | staged present | present | absent | failed, unresolved, engine |

What these runs checked is the target's staging writes and their hop outcomes, the gated witness readings, the target's staged and live objects and the live control object, the target item's execution facets, the aggregate counters and item totals, and the audit summary.
The permanent rejection failed the whole engine group, so its live commits were skipped, the live control object is absent and the audit counts 402 failed names rather than 401.
The aborted response also failed the group and retained the staged object the witness had already seen, which is what separates that outcome from the permanent rejection.

What these runs did not check is everything else the approved proof requires.
They do not compare the namespace before and after, do not observe the second account or the local files, do not run the existing collateral assertion, do not validate all 402 item records, and do not compare the complete mutation identity map, so an unrelated lost object or an extra write on another identity would not have been caught.
The separate unmodified 401-item run with the instrumentation in place was not executed.
Those gates remain pending, and until they are met this experiment is not complete.

The ambiguity the experiment set out to break is separable in these records.
The rejected-then-retried control and the applied-then-aborted control end with the same staged object present, so a final object cannot tell them apart, while the per-hop record and the gated witness can: at the refusal the witness found the staged name absent, and at the abort it found the staged name present while the response carried no status at all.
The wrong classifier that reads an abort as proof that nothing applied is contradicted by those records on inspection.
That contradiction is not yet the required sensitivity proof, because the producer only reports it and no checked oracle fails when the expected rejection does not occur; that control is pending.

Three defects in the instrumentation were found by running it and corrected before these runs.
The fault first fired on any request on the armed path, so an early run refused a probe rather than the staging write; the fault counter counted probes, so a measured count would not have been a count of writes; and the recorder captured an informational 100 Continue as the status returned to the gateway.

## What this says about the original failure

The original run observed one staging identity twice and otherwise produced its ordinary outcome, with the staged object present and the item recorded failed and unresolved at commit with HTTP 403.
That is the shape of the rejected-then-retried control rather than of the applied-then-aborted control, because an abort failed the whole engine group here and the original group did not fail.
The original extra attempt is therefore consistent with a first attempt that did not apply followed by a successful retry.

It is not established.
The comparison holds only against these particular controls, it does not exclude other faults that apply a write and lose its response without failing the group, the original first attempt's outcome was never recorded, and no run here reproduces a hosted runner.

Attempt counts belong to the cases that produced them.
Each of the two rejection controls made two target staging writes; the abort control made one, and the engine made no further attempt on that identity.
These are counts for these faults and not a general retry bound.

## Mechanisms this producer does not yet implement

The predeclared boundary of twenty attempts on the identity or one hundred and twenty seconds is not implemented here.
The attempt counter only records that a limit was passed, the abort path never reaches it, and no incomplete outcome is ever populated, while the job wait fails at its own existing ninety-second bound instead.
The observed counts and durations sit below the proposed thresholds, so these runs neither exercised nor established that mechanism.
The aborted records carry no duration, because the deliberate abort bypasses the recorder's completion path; that is a missing measurement rather than a measured zero.

The retained producer record holds the effective commands, the harness and control identities, exits, durations, the environment including the gating variable that the run requires, hashes of the two uncommitted producer sources and of every observation record.
It does not hold raw test output or the engine's emitted arguments, so the engine identity and its arguments for these runs are unestablished provenance rather than recorded fact.

## Limits

This is diagnosis and not repair, and it introduces no retry into any test.
Where the application of an individual attempt cannot be established, the record retains an unresolved effect rather than inferring a rejection or a second effect from a transport outcome.
A measured attempt count belongs to the fault that produced it and is not a general retry bound, and reaching the predeclared limit is an incomplete observation rather than proof of non-termination.
Reading a final object cannot count historical writes, so the live and staged reads at a barrier are boundary evidence and never a substitute for the gated application witness.
If no extra attempt occurs during the unfaulted instrumented runs, the original failure remains unresolved, and the instrumentation is retained so that a later occurrence is diagnosable rather than ambiguous.
Hosted-runner behaviour cannot be summoned on demand, so no run here can prove what the observed extra attempt was.
The separate copy binding investigation is not addressed.
No macOS or Windows evidence is produced, and the valuable-data release hold is unchanged.
