# ADR 0019 — acknowledged HTTP response faults

**Status:** Design accepted after independent local review; implementation and its validation record are present.
**Scope:** The minimum P03 HTTP seam needed by the audit containment regressions.

## Problem and oracle

The existing mock's AfterRequest callback receives only a method and decoded path after the handler has written its response.
It has no bounded arrival wait, explicit release, occurrence receipt or response-loss contract.
A handler that flushes can expose the response before that callback runs.
The accepted audit plan requires a test to establish the intended protocol boundary before introducing a competing writer or losing an acknowledgement.
An injected failure must describe the actual server effect separately from the outcome observed by the client.

The behaviour oracle is the [P03 fault/barrier contract](../audits/2026-09-05/remediation-plan.md#the-faultbarrier-harness-is-included), the conditional planned-new PUT contract in DESIGN §28.6 and its R-01/F-04 follow-ups, and ADR 0018's independent representation snapshots.
This slice does not choose a concurrency primitive for overwrites or Mirror deletion.

## Decision

Add one test-only HTTP response barrier in `internal/swifttest` that wraps an existing handler.
Use it with the existing mock and, for one real-server gate, the existing reverse-proxy approach to the owned SAIO fixture.
Do not add a production fault API, change application request headers or replace the existing service harness.

A selector records a test operation identity, exact method, escaped path, raw query and positive request occurrence.
The operation identity belongs to this fixture instance; it is not an assertion that the server knows an application job ID.
Each independent actor or job uses its own controlled endpoint when identical requests need different treatment.
Count matching requests on arrival, before dispatch, so reordered response completion cannot change which request was selected.
Unmatched requests and other occurrences continue through the original handler and remain observable through the existing request log or independent reader.

The typed response-ready event records the selector identity, matched occurrence and final response status.
It is acknowledged only after the selected handler returns, while none of that selected response has reached the client.
The test must explicitly wait for this event and verify the expected status before acting.
The event proves the handler returned; a successful status alone is not proof of a durable or correct storage effect.
Tests establish effects separately through the original backend and independent state oracle.

Capture only the selected response, with a fixed small body limit suitable for HEAD and mutation acknowledgements.
Overflow, unsupported response behaviour and a handler failure are fixture errors; they must not become a fabricated successful response or an indefinite arrival wait.
The first implementation captures at most 64 KiB and its callers use five-second arrival and selected-request bounds.
Flushing, hijacking, informational responses and trailers are unsupported by this first buffered seam.
Selected requests with Expect are refused before the handler because [Go can send 100 Continue when the handler reads the request body](https://pkg.go.dev/net/http#ResponseWriter), outside its replacement response writer.
The lost-PUT controls therefore use a raw HTTP client without Expect, with no retries; native and child retry/continue behaviour remain later dimensions.
Do not use this buffered seam to claim streaming, throughput or arbitrary large-response behaviour.
Unselected responses retain their existing streaming behaviour.

After acknowledgement, an explicit release either delivers the captured response or drops it according to the fixture's configured fault.
Response loss closes the client exchange without sending a substitute HTTP success or error response.
It neither undoes an accepted mutation nor retries it.
Every response-ready wait and held response has a finite bound.
Request cancellation and fixture closure abort a held exchange; teardown releases waiting handlers before shutting down its HTTP server.
The control cannot leave its own waits blocked after test failure.
Wrapped fixtures must honour cancellation and have bounded handlers or proxy timeouts; this helper cannot terminate arbitrary code that ignores both.
Only one request occurrence is selected, and release/closure are safe to repeat during cleanup.

| Scenario | Required evidence |
| --- | --- |
| Final planned-new HEAD returns absence | Event reports that exact HEAD and 404 before a competing writer is planted; conditional PUT refuses and the competitor's bytes survive |
| PUT succeeds but its response is lost | Event reports the completed PUT; independent snapshot shows the landed data; the client receives a transport failure, with exactly one server mutation |
| No matching request arrives | Bounded wait returns a fixture error with the intended selector |
| Wrong method, escaped name, query or occurrence | Traffic passes normally and cannot satisfy the selected event |
| Concurrent matching requests finish out of order | The selected arrival occurrence remains the one controlled |
| Cancel, close or timeout while held | The exchange terminates and cleanup drains the handler/client without delivering success |
| Handler flushes or emits too much response data | No selected response escapes before acknowledgement/release; overflow refuses as a fixture error |

## Implementation sequence and gates

Implement the bounded response barrier and its lifecycle controls, then replace the existing planned-new final-HEAD test's ad hoc callback with the acknowledged event.
Strengthen that caller's final assertion with an independent snapshot of the raced object and unrelated data.
Add a landed-but-lost PUT acknowledgement control through the same helper, using both a mock and the owned real Swift fixture.
Use the independent oracle outside the fault endpoint so the injected failure cannot corrupt its observation.
The mock reader uses a second listener over the same storage; the real-Swift reader uses the original endpoint outside the controlled proxy.
Keep real-server evidence distinct from mock evidence and do not credit a service scenario for a raw fixture operation.

Exercise wrong selectors, exact occurrence, out-of-order completion, arrival timeout, held timeout, client cancellation, closure, flush, overflow and repeated cleanup under the race detector.
Deliberately weaken event ordering, response holding, response loss and selector matching; the corresponding behavioural assertions must fail.
The converted planned-new race must also fail when its conditional commit safeguard is removed.
Run the existing relevant suites and the full internal suite with two owned SAIO accounts and pinned rclone before local review and protected PR delivery.

P03 remains broader than this first seam.
Final-hop overwrite and Mirror races, recovery history loss, process death, durable-write faults, native filesystem placement and child lifetime land with their dependent containment or contract work.
The next implementation slice follows P01/P04: reconcile the ownership and durability contract, retain uncertain recovery residue and keep unresolved records visible.
This harness change does not resolve those product hazards or lift the valuable-data release hold.

The [validation record](../experiments/2026-09-06-http-response-barriers.md) separates the mock, real-Swift, refusal and deliberately weakened safeguards.
