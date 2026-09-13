# Public native HTTP error messages

An owned auth endpoint reflected a saved synthetic key in an HTTP reason phrase, which reached the public Connect error.
The K-SEC-002/006 and DESIGN §§49/50/55 oracle is that provider-controlled native HTTP text cannot supply the public message.

## Behaviour

For native `*swift.Error`, Normalize now uses Go's local `http.StatusText`, or the fixed fallback Swift request failed for an unknown status.
Numeric status, classification, retryability, cause identity, cancellation/normalization precedence and transaction-ID policy remain unchanged.
The preserved private cause can still contain provider text and must not be formatted as public output.

This deliberately changes some display wording, such as native 401 to Unauthorized and unknown 498 to the fallback.
It does not sanitize arbitrary network/deadline/unknown errors, joined cancellation text, already-normalized errors or transaction headers.
No auth retry, backend, cleanup or authority policy changes.

## Verification and limits

The original immutable before source was `2f576f77`; the relevant error/auth path was checked against authoring base `836ad0d2`.
Public BookmarkService saved a synthetic key and public BrowseService.Connect sent it to an owned loopback HTTP/1.1 server.
The custom-reason 500 failed the public secrecy assertion, while mapped-403, standard-500 and healthy controls passed.
The initial listener-permission failure remains recorded and has no regression credit.

Contract `b72391f0` preceded the correction.
The maintained three-package selection went from 40 failures/11 controls to 51 passing leaves under the race detector.
Restoring only native Text reproduced the same 40 failures, including the system assertion after authority and complete namespace checks.
Compatibility passed 21 leaves before and after; focused vet, contract tests and reconciler controls passed.
The corrected collector preserved its original VCS-stamping/build failure rather than recasting it as success.

Only the fixed public system run credits BrowseService.Connect/refusal; the isolated happy gap remains.
Before and omission streams fail and credit no passing refusal.
Raw folders are `aeronautes-public-error-before-2f576f77.cVh5wjxz` and `aeronautes-public-http-errors-author.Z2JLybFh`.

This is owned-loopback Go service evidence, without a rendered Wails journey, TLS/HTTP2, deployed Swift or native keychain acceptance.
Broader P10/error boundaries, protected delivery and the valuable-data hold remain open.

## Evidence retention

The original record, probes, structured evidence, failed runs and raw verification outputs are retained in the private packet `/home/csmart/code/.aeronautes-work/evidence/20260913-service-records`.
Its `SHA256SUMS` digest is `d0ce6823082a0aab3ddb7f0b563ccc4028aba68c6471b124cad41c925650ae99`.
`original/docs/experiments/` preserves the record and probe paths at checkpoint `860ddec9ce72c241f349f491ca3b63dbab8ed5ce`; `INDEX.json` maps the raw packets and lists excluded dependency/cache directories.
Historical receipts remain unchanged; this archive does not claim to retain those excluded execution inputs or constitute a hermetic environment.
The maintained regression tests remain in the repository.
