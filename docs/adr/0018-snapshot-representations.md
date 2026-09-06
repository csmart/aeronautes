# ADR 0018 — preserve snapshot representations

**Status:** Proposed for independent local review before implementation.
**Scope:** Audit P02 / TEST-01 and the independent HTTP oracle in DESIGN §65.10.

## Problem and oracle

The archived encoding probe still fails against master after PR31.
A gzip-encoded object and an unencoded object with the same decoded content produce identical snapshots because Go transparently decodes the body and removes its encoding header.
This can let the test harness certify a change that it did not actually observe.
The governing requirement is an independent before/after comparison of the selected Swift representation, including its content headers and bytes.

[Go's transport contract](https://pkg.go.dev/net/http#Transport) explains when it requests gzip and decodes the response automatically.
[HTTP content coding](https://www.rfc-editor.org/rfc/rfc9110.html#section-8.4) is part of the representation; it differs from transfer framing such as chunked delivery.
An assembled large-object GET also differs from decoding that GET's content coding.

## Decision

Disable automatic response decompression on an oracle-owned transport without mutating the process-wide default transport.
Retain connection timeouts, redirect refusal and explicit transport cleanup for the oracle's lifetime.
The snapshot's existing Size and Digest describe the complete response body after HTTP transfer framing is removed, with content coding still applied.
For DLO and SLO objects this is the assembled GET representation; backing segments use the same byte rule.
Record Content-Encoding independently, preserving every field value in order, so changing only the encoding metadata remains observable.
Do not add a decoded-content field until a scenario needs one.

| Response or change | Required observation |
| --- | --- |
| Unchanged plain or gzip representation | Equal size, digest and encoding evidence |
| Gzip becomes plain with equal decoded content | Changed encoding and representation bytes |
| Different compressed bytes decode to the same content | Changed representation digest |
| Encoding header added, removed or changed with identical bytes | Changed content-header evidence |
| Malformed gzip or an unknown content coding | Record the opaque bytes and encoding; make no claim that they decode successfully |
| Partial response to a complete snapshot read | Refuse the snapshot; never hash the partial body as the whole object |
| Truncated response body | Refuse the snapshot |

Full object, backing-segment and raw SLO-manifest reads require HTTP 200 and no Content-Range field.
Namespace listing keeps its existing valid empty-page behaviour, but a partial response cannot complete an inventory.
The [HTTP range contract](https://www.rfc-editor.org/rfc/rfc9110.html#section-15.3.7) does not make a 206 response a complete representation.
The oracle will not assemble ranges in this slice.
Structured listings and raw SLO manifests must still parse as supported JSON.
An encoded JSON body that cannot be parsed is refused rather than implicitly decoded; the opaque-byte rule applies to object and segment content.

Existing symlink pointer semantics and unsupported nested/ranged SLO refusals remain; this does not complete the broader symlink-header or whole-account model.
Product transfer behaviour, metadata copying, scenario coverage policy and native keychain selection are outside this slice.

## Implementation and evidence

After design approval, make two separately reviewed implementation commits: preserve encoded bytes and content-encoding evidence, then refuse incomplete snapshot responses.
Keep the preserved audit probe as the original reproduction and add permanent transition, same-decoded-content, malformed/unknown coding, header-only and complete-response controls.
Exercise ordinary object reads, backing-segment reads and raw SLO-manifest reads; demonstrate that restoring each removed safeguard fails its regression.
Run the focused oracle race suite and the full internal suite with both owned SAIO accounts and the pinned real rclone before protected PR delivery.
No scenario credit or release claim follows merely from making the oracle stricter.

P10 native authority, bounded prompts and physical credential cleanup remain open under ADR0015/0017.
Continuing P02 now follows the accepted audit sequence into the minimum fault/barrier and recovery-containment work without representing P10 as complete.
The valuable-data release hold remains.
