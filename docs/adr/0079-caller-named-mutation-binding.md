# ADR 0079: caller-named mutation binding

**Status:** Proposed on 24 September 2026 as the contract for the operator's decision 18 of the same day in the [Linux finish line](../audits/2026-09-05/linux-finish-line.md) ("do it"); reviewed before its implementation, which follows as its own commit. It closes the mutation half of L11 that [ADR 0075](0075-live-reader-generation-acceptance.md) left open.
**Scope:** the four mutation actions v1 keeps: `MutateService.UpdateObjectMetadata`, `MutateService.UpdateContainer`, `MutateService.CreateContainer` and `MutateService.CreateMarker`. `DeleteMarker` and `DeleteManifest` are refused by the v1 gate and keep their signatures.

## Problem

Each of the four resolves the active connection when the call arrives (`Connections.activeSnapshot`) and writes to it. The view the operator acted on was built under a portrait, the bookmark and generation of `ListingScopeDTO`; between that view and the call the active connection can move, because `Connect` is bridge-reachable and a bookmark save or key change advances the generation. The write then lands on the other account, or on the same account under a generation the operator's view never saw. The records attribute the write to the connection that received it ([ADR 0058](0058-object-metadata-durable-facts.md), [ADR 0062](0062-container-metadata-durable-facts.md), [ADR 0077](0077-creation-durable-facts.md) and the creation attribution of 23 September), so the ledger is honest, but the effect is on the wrong target. ADR 0075 closed this window for the six adjacent readers and named this half as open.

## Decision

1. Each of the four signatures gains a leading `scope ListingScopeDTO` argument: the bookmark id and generation of the portrait the caller's view was built under. The generated bindings and the frontend API mirrors change with them.
2. Before `RecordIntent` and before any request to the store, the service refuses the call with a named error, `ErrMutationScopeChanged` ("the connected account changed; nothing was written"), when the scope is empty, when the active bookmark is not the scope's bookmark, or when `WithCurrentGeneration` for that bookmark reports the generation moved. A refusal records nothing: no intent, no audit event, no request.
3. The session the write uses is the one the scope named, taken in the same snapshot as the check, and the request binding of [ADR 0066](0066-named-read-session-snapshot.md) already refuses a request issued after the generation moved; so the active connection cannot change between the check and the write without the write itself being refused.
4. The frontend passes the portrait it captured when the action's dialog or panel opened, the `#portrait()` value the readers capture, not the portrait at the time of the click. A dialog left open across a reconnect is refused and shows the message with its inputs intact, so the operator can reconnect and retry.
5. The scenario contract of [ADR 0069](0069-preserve-covered-scenario-cells.md) keys actions by name, kind and required scenarios; a signature change does not drift that set, and every covered cell keeps a passing witness, so the baseline is unaffected.

## Required proof

Tier 1 under DESIGN §65.12 (account binding). Backend, on the mock and the owned Swift, for each of the four actions: a call under the current portrait applies and records as today; a call under the other account's portrait while the first account is active is refused, the cluster reads back unchanged, and no intent or audit row exists; a call under the same bookmark's earlier generation after a key change is refused likewise. Frontend: Vitest cases around the real `BrowserState` where a dialog opened under generation 1 saves after the connection moved to generation 2 and is refused with the message and its inputs intact, and where the same save under the current portrait proceeds. Mutants: the scope check removed (the write goes to the active account), the check comparing the bookmark only (a moved generation admitted), and the check placed after `RecordIntent` (an intent recorded for a refused write); each must be caught.

## What this does not establish

Broader live-read atomicity across several reads stays open. The two refused deletions keep their signatures until they return. Native acceptance and the valuable-data hold are unchanged. L11 closes for v1 when this record is implemented and the finish line says so.
