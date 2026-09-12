# ADR 0037 — own staged-content reads

**Status:** Accepted; the native prerequisite and first consumer have passed separate local implementation reviews and require one combined current-base delivery with ADR 0038.
**Scope:** One native-read prerequisite and its first plain-source to staged-DLO consumer within DS-04/P16.

## Problem and oracle

Copy presently accepts a staged large object on logical length and listing evidence before publishing it.
The retained actual-service reproduction replaces a staged segment with different bytes of the same length after the first name is published, and Copy publishes the second name with copied and verified counters despite the wrong bytes.
The delivered aggregate stamp detects some later changes; it does not establish that the original content matched the source, and valid copies may use different segment boundaries.
The accepted staged-content direction in the copy-verification experiment is one complete assembled staged read against a plain source's entity tag, with segmented-source comparison requiring separate work.

The available native Call cannot supply the required ownership contract unchanged.
Its header timeout can return while a hidden request worker remains alive, and its successful-body idle reader can return while another goroutine still writes the borrowed buffer.
Separate before evidence shows Call blocked in raw successful-auth and 401 drains, and in a text-error excerpt read, after headers and beyond the private header/read-idle timer observations.
Deferring caller cancellation until Call returns cannot interrupt those pre-return paths.
Automatic gzip handling can also replace the stored content bytes with decoded bytes.

The behaviour oracle is DESIGN §§6.5, 9, 26.2, 28.4, 38 and 65.11; K-CON-001, K-PERF-001, K-XFER-014 and K-SEG-004; ADRs 0015, 0016, 0018, 0020, 0021 and 0030; and the accepted [staged-content direction](../experiments/2026-09-11-copy-verification-strength.md#the-staged-half-and-its-dependency).
This decision adds a scoped ownership and content-read contract; the before probes do not retroactively establish that legacy authentication promised it.
The retained [native-read experiment](../experiments/2026-09-11-native-content-read.md) records the original sources, actual outcomes, failed controls and archival probe bytes.

## Decision

### One opt-in native path

Provide one opt-in full-object GET path through the existing pinned native connection and an Aeronautes Session adapter.
The adapter explicitly checks `Session.AdmitRequest`, supplies an empty initial target, and requires `OnReAuth: session.auth.storageTarget` so the accepted URL and token are selected together under the native authentication lock on every permitted attempt.
Use the same authenticator, captured TLS policy, redirect refusal and transport admission check; every authentication and storage dispatch remains subject to current-generation admission.
A callback error or empty selected target refuses before storage I/O.

Preserve native lazy authentication, its advertised-expiry check and ForceReauth invalidation.
The native path refreshes below 60 seconds of advertised lifetime and requires nonempty native URL/token fields before using the callback.
It does not use Session's unknown-expiry ten-minute age refresh or update `authedAt`; that existing distinction from fresh-child credential planning remains explicit.
The reader exports no credential tuple and does not change `Credentials`, `refreshIfStale`, existing public authentication methods, or their callers.
Keep the new policy per call, including implicit authentication; temporarily changing shared Connection fields around a call is not an opt-in policy.

The object call is an ordinary assembled GET without a request body, raw-manifest query, Range, If-Range or NoResponse semantics.
Authentication keeps its existing Request/Response interface, including authenticators whose own requests carry a body and whose responses consume body data.
Do not migrate unrelated GET, HEAD, upload, deletion, local-transfer, rclone or metadata operations to the new path in this change.

### Request and buffer ownership

Create an operation-owned cancellable context and a separately owned child context for each authentication or storage request attempt.
Closing a completed authentication response or a rejected 401 cancels that attempt, not the whole still-eligible operation or another concurrent request.
Cancellation of the operation cancels its active attempt.
The existing normal 401 retry must still reach fresh authentication and a successful subsequent GET when the earlier response closes successfully.

Apply the connection's existing per-attempt `ConnectTimeout` to the actual request context until headers arrive, and its existing per-read idle `Timeout` to body I/O.
Defaults remain 10 seconds and 60 seconds respectively; do not add an overall object deadline or borrow the raw client's ten-minute timeout.
Stop the header watchdog after receiving headers, and permit a complete object read to exceed either timer in total while it makes progress.
Own and stop or join watchdog work so a late timer cannot affect a later attempt, another read or an unrelated Session caller.
Distinguish an owned timer expiry from cancellation of the caller; cancelling a child context to enforce a timeout must not make that timeout appear to be operator cancellation.

The local Do operation must finish before the request helper hands out a response, retries or returns an error.
Each Read must finish its underlying read before returning control of the caller's buffer, including on cancellation or timeout.
Use synchronous I/O with cancelling watchdogs or an equivalent join of the exact local helper; do not return an in-use buffer and rely on eventual cleanup or a buffer-retirement registry.
Retain and close any response returned alongside an error or after a timeout; a final nil response is not proof that earlier attempts owned no body.

A successful open transfers the response body's ownership to the new reader; it is not a claim that the object has already been consumed.
Completion of the full read requires EOF and checked Close, cancellation of its owned context, and completion of its owned request/read/watchdog work.
On any read, validation or close failure, accept no content result and retain both the primary error and any distinct cleanup error through normal wrapped-error handling.
No hidden object-body restart or retry is added after a stream is accepted.
Retain existing bounded native request/auth retry budgets, with credential reselection and admission on every permitted attempt; cancelled or refused requests must not reach the wire.

### Error and authentication bodies

Close unused or known-invalid bodies promptly without unbounded draining or provider-body excerpt reads.
The opt-in must cover raw non-success parsing, 401 handling and implicit authentication, not only the final successful object's body wrapper.
Preserve useful HTTP/error classification without incorporating an untrusted response body into Copy's public diagnostics.
Do not run a later authentication/storage attempt after a failed required close.
For a storage 401, invalidate the rejected native tuple even when body closure fails; a retained snapshot must not let this read replay that rejected tuple.

Keep generic `Auth.Response` able to read its response under the new owned I/O lifetime.
Aeronautes v1 uses headers only, so its unused response body can close without being drained; generic body-consuming authenticators must not receive an already closed body.
On non-2xx authentication responses, do not invoke successful-response acceptance or replace accepted credentials.

Preserve coherent accepted authentication publication before reporting a subsequent close failure.
Today v1 publishes its snapshot during `Auth.Response`, and native URL/token/expiry fields are assigned before deferred closure returns.
Closing after Response but returning before assigning those fields could leave the new snapshot beside an old native token; closing before Response could break generic body consumers.
A checked auth-close failure must therefore refuse this read before its storage callback/GET while leaving any accepted published tuple coherent.
It does not prove that all snapshots were erased or that unchanged Credentials callers cannot observe a previously accepted snapshot.
Distinguish old rejected credentials from newly accepted authentication headers in tests; do not add a global auth transaction or shared-session invalidation policy here.

### Complete opaque object content

Send explicit `Accept-Encoding: identity` on the new object GET and apply no content decompressor.
Require HTTP 200 and absence of any Content-Range field, including an empty field; inspect the original response headers rather than a flattened first-value map.
Partial status or range evidence cannot pass merely because the received bytes would match the expected digest.
Preserve opaque content coding while removing normal HTTP transfer framing.

This is a documented opt-in request choice, not wire equivalence to ADR 0018's independent oracle, which disables decompression and sends no Accept-Encoding.
The retained loopback experiment establishes that identity suppresses Go's automatic gzip decoding at this seam; it does not establish how an arbitrary negotiating origin or intermediary behaves.
If decoded bytes are returned against a plain source HEAD's stored length/MD5 expectation, the new guard normally refuses rather than verifies within the existing honest Swift integrity model.
The compatibility cost is possible refusal of a valid operation, not permission to decode silently or weaken comparison.
Actual Swift with the pinned rclone must pass positive opaque valid/malformed gzip and unknown-coding controls through the real staged route before delivery.
Two distinct gzip representations of equal decoded content must remain distinguishable.
Any failure of that compatibility gate returns to a separately reviewed representation scope; this proposal does not change the engine or another author's transfer policy to make the test pass.

### First content-check consumer

Use the reader only when the existing post-engine source HEAD identifies a non-symlink plain object and the staged object is a DLO.
After existing staged length/listing checks and before destination re-HEAD, committing intent or live publication, stream the complete assembled staged content and compare its byte count and MD5 with that source observation.
Require a nonnegative source length and a valid 32-hex MD5 expectation; malformed or unavailable expectations refuse this covered path.
Consume exactly the expected length through EOF, handle a final read returning bytes plus EOF, and reject truncation, extra content and read/close failures without integer overflow in the overrun check.
Use a fixed-size buffer and the existing Copy execution concurrency, without whole-object memory, temporary content copies or new background read workers per chunk.
The added cost is one complete assembled staged read per covered candidate; record actual workload, bytes, runtime and limits before claiming acceptable performance.

Logical bytes may match across different segment boundaries; do not compare segment structure as content equality.
Successful comparison continues through the existing conditional new-name publication and record checks.
Failed comparison retains the staged manifest, its backing segments and recovery facts, adds no committing/committed fact for that name, and performs no live PUT or new cleanup for it.
Keep the existing typed execution failure with unresolved effects after the engine's staging work, and the existing public item/job/audit projections.
A same-length content mismatch is a staging `unverified` failure; other failures use the existing appropriate incomplete, I/O, timeout, cancellation or persistence classification.
Required record failure still stops dependent work under ADR 0030; do not invent a new schema, public job family or generic failure framework.

The two-name corruption fixture must retain the first 19-byte control and refuse the second name before publication, yielding partial, copied 1, verified 1, failed 1 distinct identity and copied bytes 19.
Its public item totals are failed 1, unresolved 1 and problems 1; Unverified remains 0 under the existing rule for copied items without a verified comparison.
Assert the exact durable audit and Get/List/History/item results after the job body and binding pins finish, rather than treating a terminal row as proof of completion.

## Required maintained proof

Tests must exercise the new API/consumer and assert observable ownership and data outcomes.
The archived probes deliberately identify legacy raw stacks and contain always-red diagnostics; retain them as source-bound evidence outside the normal Go test tree, not as copied acceptance assertions.

| Boundary | Maintained assertion and sensitivity control |
| --- | --- |
| Request lifetime | A delayed-header request returns the correct timeout/cancellation only after its local Do completes and any returned body is owned/closed. A held late response or response-plus-error cannot be abandoned. Removing cancellation or the join fails the matching assertion. |
| Borrowed buffer | Read cannot return before a held underlying write into its buffer ends. Test cancellation, idle timeout and completion races with owned barriers, plus a progressing stream lasting longer than both per-I/O timers overall. Removing the join or accidentally retaining the header timer fails its own case. |
| Auth/error response | Stalled unused v1 auth, 401 and text-error bodies close without a raw drain/excerpt wait; normal matched controls complete. Close failures refuse before dependent dispatch, with a generic body-consuming authenticator control and coherent tuple assertions. Removing close/error checking must fail even where headers/body content otherwise look successful. |
| Credentials/admission | Cold auth, advertised expiry, unknown-expiry old native credentials, ForceReauth followed by failed auth, 401 followed by failed close/auth, URL/account changes, storage override, captured trust and revocation before retry preserve their exact wire boundaries. Concurrent legacy callers retain their existing behaviour. |
| Complete representation | Normal/chunked and zero-byte objects, bytes plus EOF, truncation/overrun, malformed expected MD5, partial status and empty/nonempty Content-Range are distinguished. Opaque valid/malformed gzip, unknown coding and different encodings of equal decoded content retain their bytes. Removing status/range/length/EOF/close/encoding safeguards independently fails even when another check could otherwise match. |
| Staged content | Real executor/service fixtures cover same-size corruption, permutation and missing content compensated by duplicate bytes so earlier length checks do not mask the missing guard. Valid resegmentation passes. Removing the digest comparison restores unsafe publication and fails the intended namespace/public-record assertion. |
| Public effects and records | Hold the first already-applied live PUT response in the two-name fixture, mutate the second acknowledged stage, and inspect full source/destination/collateral and recovery records after join. Include cancellation and required-record/completion refusal, preserving earlier effects and truthful unavailable outcomes. |

Preserve existing `pairing_lifetime_test.go`, `callcredentials_test.go`, `credentials_test.go`, Session trust/refresh tests and `TestRequestAdmissionPrecedesEveryDispatch`.
Preserve unchanged-stamp, legitimately resegmented, segmented-source and materialised-plain decision controls in `segment_change_detection_test.go`; change the covered wrong-initial-staged-bytes case to the required refusal.
Keep `TestStaleAggregateObservationStillSkipsGap` as a named unresolved later-observation limitation.
Reuse the service record/recovery barriers in `copy_record_faults_test.go` and `copy_dlo_records_test.go`; do not route a large GET through a small-body response-capture limit to manufacture a fixture failure.
Exercise the actual supported HTTP/TLS transport, including a separately observed HTTP/2 control where it is negotiated, and record protocol/platform identity rather than inferring it from a build.

## Implementation and delivery sequence

1. Review this proposed contract and DESIGN amendment with the preserved before evidence before changing production code.
2. Implement the opt-in native request/read lifetime and Session adapter with focused maintained tests as one separately reviewed prerequisite commit.
   Record the complete vendor delta as a new ordered patch after the unchanged credential-pairing, listing-response and listing-fidelity patches.
   Update `third_party/ncw-swift/UPSTREAM.json`, `scripts/check-swift-source.py` and its rejection tests; retain the module pin and verified upstream archive comparison required by ADR 0016.
3. Implement the narrow plain-source to staged-DLO comparison and its service/public-outcome regressions as a second separately reviewed implementation commit.
   These commits form one eventual change: do not merge or ship the prerequisite as an unused standalone API.
4. Reconcile the complete combined range against its actual base, retain every failed producer and unique test/skip inventory, and complete the source gate, explicit nested native root mock, appropriate application race/build/static gates and real-Swift/rclone compatibility evidence.
   The application's `go test ./...` excludes the nested native module; `make test-swift-core` is mandatory alongside `make verify-swift-source` and its rejection tests.
   Obtain independent local review, require all five current-head/current-base PR jobs, merge only the reviewed complete change, and check the resulting master.

Candidate production files are `third_party/ncw-swift/source/swift.go`, new native `read_request.go`/tests, the new ordered patch/provenance gate, new `internal/swiftcontrol/objectread.go`/tests, and `internal/transfer/copy/executor.go` with a private staged-content helper/tests.
Add service regressions in the existing system-test package and update the relevant Copy test; no shared Session, job-runner, frontend, module dependency, local/download or rclone change is proposed.
If the implementation requires such a change, return to scope review before proceeding.

## Limits

The native authentication mutex is not context-aware, and a concurrent legacy authentication can hold it while stalled; this proposal does not impose a hard completion deadline on that wait.
Joining likewise cannot make an arbitrary transport, body or authenticator that ignores cancellation finish within a promised wall-clock bound.
Do not detach such work and release its resource claim while calling it quiescent.
The contract joins owned local Do/Read/watchdog work; it does not promise simultaneous shutdown of all pooled HTTP goroutines, remote operations, native writers or child processes.
Already admitted operations may finish after revocation under ADR 0015, and pins remain governed by the whole job-body/cleanup boundary.

A cancellation during the final staged name can still reach the existing later `Verify(background)` comparison because an ordinary failed staging observation does not necessarily latch the executor stop.
This is a source-derived boundary requiring a focused maintained test, not a reproduced cancellation outcome or a promise that all later read-only verification stops.
The covered name must retain its cancellation cause and unresolved staging evidence with no committing fact or live PUT; any stronger caller stop policy requires a separate scope decision.

MD5 here is Swift's existing plain-object integrity expectation, not collision-resistant authenticity against forged evidence or a hostile source/provider.
The source observation is the existing post-engine HEAD, not a version pinned to planning or the engine's original read.
The staged GET is an observation, not a freeze of manifest membership or a binding through final publication; source changes, stage/segment changes after read and final-publication races remain P16 work.
Segmented sources, symlinks, other destination classes, replication/metadata fidelity, legacy strength labels and stale later skip evidence are not upgraded.
The new refusal retains staging, but existing earlier precheck/cleanup paths do not thereby gain an ownership proof.
No Mirror start, overwrite, source deletion, automatic segment cleanup or physical credential deletion is authorised.
Loopback and Linux fixture evidence does not certify the other native platforms, six-target release behaviour, process crash, power loss or general resource ceilings.
DS-04/P16 and the valuable-data release hold remain open after this bounded change.
