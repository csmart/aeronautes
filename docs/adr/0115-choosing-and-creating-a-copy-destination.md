# ADR 0115 — choosing and creating a copy's destination container

**Status:** Proposed for 0.5.0 (12 November 2026), from the final-candidate plan's item 6 ("copy destination container picker with create-if-missing", operator, 12 November).
**Scope:** how the copy dialog offers the destination account's containers, and how a missing destination container is created.
**Amends:** [ADR 0079](0079-caller-named-mutation-binding.md), whose mutations are bound to the active view, by adding one write bound to a destination listing's scope instead.

## Context

The copy dialog takes its destination container as free text.
The operator must know the name exactly, and a container that does not exist is found only when the preview's plan reads it and fails with a not-found error, after which the operator has to connect to that account, create the container, and come back.

The listing and the container create the window already has both work on the active connection only: a read through `scopedSession` and a write through ADR 0079's binding refuse any bookmark but the active one.
A copy's destination is often another account.
The copy plan itself already reads the destination through the per-bookmark session registry (§65.4), so reading the destination account is not new; offering its containers, and creating one, are.

## Decision

1. **The dialog lists the destination account's containers.**
   A new read, `TransferService.DestinationContainers(destBookmarkID)`, returns the first page of the destination account's container names (at most 1,000), whether more exist, and the scope it read under: the bookmark and its generation.
   It reads through the same registry session the copy plan uses, so it signs in no differently from the copy.
   The destination container field offers these names as the operator types and still accepts a name that is not listed, since the page may be partial and the account may change.
2. **A missing destination container is offered for creation, not reported as an error.**
   When the preview's read of the destination container answers not found, the dialog says so and offers "Create container *name* on *bookmark*", instead of the plan's error.
   Any other failure of that read is reported as it is today.
3. **Creating it is its own action, bound to the destination listing's scope.**
   `TransferService.CreateDestinationContainer(scope, name)` creates the container on the scope's bookmark with the cluster's default storage policy and no metadata, through the session the scope names.
   It is refused before any request when the scope is empty or the bookmark's generation has moved since the listing (the rule of ADR 0079 decision 2, applied to the destination listing rather than the active view).
   It goes through the existing create: a container that already exists is refused, never treated as created; the intent and outcome are durable facts (ADR 0077); and the audit trail records the creation against the destination bookmark.
   After it succeeds the dialog runs the preview again; it never starts a copy.
4. **A copy never creates its destination.**
   Starting a copy still requires the destination container to exist when the plan reads it, so a container appears only when the operator asked for it by name.
5. **Both actions join the inventory.**
   `DestinationContainers` is a read row and `CreateDestinationContainer` a mutate row (happy and refusal) in the scenario contract (ADR 0011); both are kept in v1 (ADR 0052); the baseline changes through the drift route of [ADR 0069](0069-preserve-covered-scenario-cells.md).

## Consequences

- The operator chooses an existing destination from a list and can create a missing one without leaving the dialog or changing the active connection.
- One write, the container create, can now be made on an account that is not the active one, bound to the generation the operator was shown.
- A storage policy or initial metadata for the new container is not offered here; the container editor sets them afterwards, or the container is created from its own account's view as today.

## Tests

- The listing returns the destination account's names and its scope for a bookmark that is not the active one, and says when more exist.
- A preview whose destination container is missing offers the create rather than the error; another read failure is shown unchanged.
- The create on a non-active bookmark makes the container, records its intent, outcome and audit event, and the preview then succeeds; an existing container is refused; a moved generation and an empty scope are refused before any request.
- A copy start with a missing destination container still fails, and creates nothing.
