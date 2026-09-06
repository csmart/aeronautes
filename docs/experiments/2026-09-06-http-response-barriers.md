# Acknowledged HTTP response barriers

Date: 2026-09-06.
Scope: the first P03 seam in [ADR 0019](../adr/0019-http-response-barriers.md), based on master `33e1727f9fabf8bc035093e72e1dfe41eed54b2c` after PR32.
This adds test controls and strengthens a regression; it does not fix the remaining recovery, overwrite or Mirror defects.

## Boundary and observations

`HTTPResponseBarrier` selects an exact method, escaped path, raw query and request arrival occurrence for one fixture operation.
The operation label is local test identity; it does not authenticate a job on the server.
The existing mock request log contains decoded paths, so it must not be substituted for the escaped selector.
Only the selected response is captured, up to 64 KiB, with headers frozen when its final status is written.
The response-ready event follows handler return and precedes delivery.
Release either delivers that response or aborts the exchange without an HTTP substitute.
The control performs no retry and never undoes the handler's effect.

Callers use a five-second arrival bound and a five-second selected-request bound that includes both the handler and the held response.
Fixture servers and clients also bound network work, and cleanup cancels and drains active test operations before shutting down listeners.
Missing arrival, cancellation, closure, timeout, overflow and unsupported response behaviour produce fixture errors.
Callers inspect the barrier error separately from the client outcome before teardown.
Unselected traffic retains streaming behaviour, verified by reading its first chunk while its handler still waits to produce the next.

The converted `TestPlainCommitRacerSurvives` waits for the exact final destination HEAD and its 404, then plants the competing object before releasing that answer.
The product's conditional PUT reports a conflict, and the independent reader proves the competitor's size and digest plus unchanged collateral state.
Its snapshot uses a second listener over the mock's storage and never passes through the fault endpoint.

The mock lost-PUT test and the real-Swift delivery/loss controls use raw HTTP PUTs without retries.
At the acknowledged 201, the independent snapshot already contains the new object with its expected digest while the sentinel is unchanged.
Dropping the response produces a transport failure without a final HTTP status, while the data remains unchanged and exactly one raw PUT is observed.
The real-Swift reader accesses the original endpoint outside the controlled proxy.
These raw operations receive no service-scenario credit.

## Unsupported handshake

The initial native-PUT real-Swift probe refused a 100 Continue response as a fixture error.
This was a fixture limitation, not a product data-loss finding.
Go can automatically send that interim response when a handler reads an Expect request body, bypassing the replacement response writer.
The first seam therefore rejects selected requests carrying Expect before invoking the handler and refuses informational responses, flushing, hijacking and declared trailers.
The permanent Expect control proves the handler performs no effect on that refusal.
The raw PUT controls exercise the accepted small-response contract; they do not establish native-PUT or child retry/continue behaviour.

## Verification

The full internal race run passed **465 top-level tests across 20 packages, with zero failures or skips**, using Go 1.27.1, both owned SAIO accounts, the required private keychain fixture and official rclone 1.74.3 with its matrix enabled.
Statement coverage is **83.4%**.
The reconciled scenario report is valid at **10/104**, with **47/48 actions still carrying gaps**.
The barrier and copy packages also passed three race repetitions; the real-Swift delivery and response-loss controls passed independently.
Application build, vet, the internal build without CGO, formatting and diff checks pass.
Frontend, bindings, production transfer behaviour and workflow configuration are unchanged.

Thirteen separately compiled temporary variants deliberately removed safeguards and failed their corresponding behavioural tests:

| Weakened safeguard | Failing leaf tests |
| --- | ---: |
| Acknowledge before handler return | 1 |
| Deliver without holding | 2 |
| Deliver the response selected for loss | 3, across the barrier, mock PUT and real-Swift PUT controls |
| Ignore method | 1 |
| Ignore escaped path | 1 |
| Ignore raw query | 1 |
| Ignore occurrence | 1 |
| Remove selected-request deadline | 1 |
| Remove arrival deadline | 1 |
| Remove closure cancellation | 1 |
| Permit selected Expect requests | 1 |
| Ignore capture overflow | 1 |
| Remove the planned-new conditional PUT guard | 1 |

No variant passed merely because of a build error or the Go test process timing out.
The conditional-guard variant reports a copied object and overwrites the competitor with `ours`; the independent digest assertion fails as well as the outcome assertion.
These are catching controls over one new test seam and an existing product guard, not thirteen newly fixed product defects.

## Limits and next work

The capture is for small, complete responses and cannot establish streaming or throughput properties of selected requests.
It cannot terminate arbitrary handlers that ignore cancellation; wrapped fixtures need bounded, cooperative handlers or proxy timeouts.
Snapshots cover the selected containers and modelled object state, not whole-account completeness, persistent outcomes or physical durability.
P03's remaining fault dimensions and P01/P04/P05 recovery and destructive-race containment remain open.
Native credential authority/cleanup and the valuable-data release hold also remain open.
