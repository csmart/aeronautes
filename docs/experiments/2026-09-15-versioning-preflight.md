# Versioning preflight: an unestablished mode admits the edit

15 September 2026.
Finish-line item L09, the portion v1 keeps: unknown or changed mode
refuses, and modern editing remains refused.
The correction in [ADR 0054](../adr/0054-versioning-preflight-authority.md)
establishes the mode from the write preflight and refuses when it cannot.
The legacy transfer, delete and cleanup matrix is deferred with those
routes and is not examined here.

## What was reproduced

One run through `MutateService.UpdateContainer` against the real
two-account fixture, exit 1, two intended failures and two passing
controls.

| Case | Result |
| --- | --- |
| A modern-versioned container refuses the edit | passes, as designed |
| A plain container edit applies | passes |
| One failing container HEAD, intent `off` | edit admitted |
| One failing container HEAD, intent `history_location` | container rewritten to a legacy shape |

The fault is one container HEAD refused with 503 by a proxy in front of
the fixture, with every later request passing.
That is the shape of a transient refusal landing on the service's
versioning probe while the write that follows succeeds.
Both failing cases report `proxy failed 1 container HEADs, passed 2`,
so the probe read failed and the adapter's own preflight read did not.

The `off` case applied its metadata and left modern versioning in place,
because an off intent emits only the legacy removal headers, which do
not touch `X-Versions-Enabled`.
The operator was told `verify_mismatch`.

The `history_location` case is the one the refusal names.
The container read back `history_location` with the archive set: a
container using modern object versioning now carries a legacy history
location header.

## Why it happens

`currentVersioningMode` returns the empty mode on any error, and the
guard refuses only on an exact `enabled`.
An unestablished mode is indistinguishable from an established absence,
and the code treats it as the latter.

The refusal also rests on a different read from the one the write uses:
the probe issues its own container HEAD, and `UpdateContainer` issues
another for its preflight.

`X-Versions-Enabled` is not added to the editable header set, so it is
excluded from `EditableFingerprint`, and the conflict check cannot see a
container becoming modern-versioned between those two reads.
The field's own documentation says the fingerprint covers versioning
headers, which is true of the legacy pair and false of the modern flag.

## No conditional write is available, measured on containers

Object metadata has the same read-then-replace shape, and the
conditional headers were measured there.
Those results were not assumed to carry over.

Against the fixture, a container carries no entity tag at all, and a
container POST with `If-Match: "bogus"`, with `If-None-Match: *`, and
with `If-Unmodified-Since` in the past each returned 204 with the write
applied.
There is not even a value a precondition could reference, so the honest
correction is an accurate refusal rather than a prevention.

Three ignored headers on one fixture do not establish that no store
offers a precondition; the conclusion is scoped to what was tested.

## Where this can be reached

`UpdateContainer` is the only production path that writes a versioning
header.
Container creation cannot set one: the create request carries no
versioning field, and its metadata keys are always prefixed, so a caller
cannot smuggle `X-Versions-Enabled` through it.

## What this does not establish

Whether a legacy rewrite destroys existing version history on a
production cluster.
The header shape was measured; the fate of stored versions was not.

## Evidence

Durable private packet `~/code/.aeronautes-work/20260915-claude/l09/`,
digests in `SHA256SUMS` beside the files.
`probe-before.log` is the four-case run, `probe-consequence.log` the
after-state read, `conditional-container-post.log` the container
precondition measurement, and `versioning_preflight_probe_test.go.txt`
the archived before-probe. The four cases are maintained in
`internal/systemtest/versioning_preflight_test.go`.
