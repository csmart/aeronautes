# Bounded native credential operations — 15 September 2026

The credential adapter accepts a context and discards it.
This record reproduces what that costs, proposes the correction in [ADR 0053](../adr/0053-bounded-credential-operations.md), and states what it leaves open.

## What was reproduced

Against the private Secret Service fixture in `internal/secrets/protocoltest`, with one reply held open by the existing read barrier.
Three intended failures and two healthy controls in one run, so the fixture is not the cause of the failures:

| Case | Observed |
| --- | --- |
| Cancelled credential write | Did not return within two seconds of cancellation |
| Cancelled credential read | Did not return within two seconds of cancellation |
| Backend selection at startup | Did not settle within two seconds; no fallback was chosen |
| Control: healthy round trip | Write, read and delete complete normally |
| Control: unrelated name | A read of another name returns while one reply is held |

The two-second figure is the probe's patience, not a measurement of how long the calls would have taken.
They were still running when the probe stopped waiting; nothing here estimates an upper bound, because there is none.

Evidence: `~/code/.aeronautes-work/20260915-claude/cred2/before/`.
`probe-before.log` sha256 `9f3f50301c98608bc25a7276e051fdba11b2315217a2718838f8b2890f931aef`.
`bounded_probe_linux_test.go.txt` sha256 `95c38a9c8979f6e0c94fb55032b43b3ebc8e018618af064aa689f821b8b5845f`.

## Why the startup case is the serious one

`main.go` selects the backend through `secrets.OpenDefault` before the shell exists, and `NewKeychainStore` probes the keychain with a write, a read and a delete.
DESIGN §12.1 requires the fallback to the session store to be visible and never silent.
A keychain that accepts the probe and holds the reply prevents the fallback from being selected at all, because the probe that would reject the keychain never returns.
The operator gets no window and no warning, and there is nothing to cancel.

## Two corroborations already in the tree

`internal/secrets/protocoltest/main_linux_test.go` aborts its own binary after forty-five seconds, commented "the pinned client's prompt wait has no context".
The test binary was given a guard; the product was not.

`internal/bookmarks.Store.Save` already tells the operator that "an unused key may remain in credential storage" when a write fails, because the backend may have accepted the write before reporting failure.
An abandoned write is the same fact arrived at by a different route, so the correction has an existing honest message to reuse rather than a new claim to invent.

## Blast radius, counted where the values are read

Two call sites reach the adapter: `bookmarks.Store.ConnectionConfig` reads, and `bookmarks.Store.Save` writes.
Nothing calls the adapter's delete, which is consistent with physical cleanup being unstarted.
Five places read the package's error sentinels: `app/connections.go` maps both to operator text, `bookmarks/publication.go` returns them from the read path, and `bookmarks/jobpin.go` returns not-found for its own unrelated reason.
A new unresolved error is additive, and the existing default branch in `app/connections.go` already passes an unrecognised error through unchanged.

## One thing attempted and not established

A case was written to show the consequence at the service boundary — that an operator cancelling work cannot withdraw from `ConnectionConfig` while the native store holds its reply.
It did not reproduce: with a reply held for the same reference, the acquisition completed normally, so the held reply was not the one the acquisition read.
Rather than reshape the fixture until it agreed, the case was removed.
The consequence at that boundary follows from the code path, since `ConnectionConfig` passes its context to a method that discards it, but it is an argument here and not a measurement.

## What this does not claim

No native operation is cancelled by the proposed correction, and no bound is placed on an abandoned call beyond the life of the process.

Native keychain behaviour on the six targets is not established; this evidence is the Linux Secret Service fixture alone, and that acceptance remains separately required.

L01 does not close.
Verified persistent-backend selection and physical cleanup and retry are untouched.
