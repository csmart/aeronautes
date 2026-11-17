# The enumerated rclone surface

The exact rclone configuration and flags Aeronautes sets — nothing else is ever passed (§44, ADR 0007 and ADR 0014).
The original seam spike ran against **rclone v1.74.3** and upstream containerized Swift on 29 August 2026.
The gateway changes below have separate implementation evidence from 6 September.
A behaviour with no entry here is a gap to resolve deliberately, never a free-form argument.

## Remote configuration (per child)

| Key | Value | Driven by | Verification |
|---|---|---|---|
| `--config` | job-scoped empty file, mode 0600 | every spawn (§26.2: the operator's own rclone.conf must never leak in) | child config and environment regressions |
| `RCLONE_CONFIG_<R>_TYPE` | `swift` | — | ✓ |
| `RCLONE_CONFIG_<R>_ENV_AUTH` | `false` | — | ✓ |
| `RCLONE_CONFIG_<R>_STORAGE_URL` | invocation-local `http://127.0.0.1:<port>` gateway with the captured account path | runner (§26.2, ADR 0014) | real-child redirect matrix and two-account Swift |
| `RCLONE_CONFIG_<R>_AUTH_TOKEN` | fresh random gateway token — **environment only, never argv, never the durable key or real Swift token** | runner (§26.2) | child hygiene and scoped request regressions |
| TLS overrides | none in the child; the parent gateway applies captured system/custom-CA/insecure policy per upstream | captured session trust (§12.3) | real-child TLS, native TLS and mixed-trust refusal regressions |
| `RCLONE_CONFIG_<R>_CHUNK_SIZE` / `--swift-chunk-size` | §25 single knob (default 5 GiB capped at `max_file_size`) | Large Objects setting (§43.4) | ✓ 1 MiB test segmentation |

Remote names are fixed: `aeronautes_src:` / `aeronautes_dst:` (§26.2).
The historical seam found that `override.*` works only in a config file and leaks trust across remotes in one child.
ADR 0014 removes those child overrides; the existing refusal of different trust identities in one invocation remains in force.

## Gateway request policy

[ADR 0014](adr/0014-child-storage-gateway.md) applies the following allowlist before a child request can reach Swift.
The role limit is additional authority control; a local token without these restrictions would delegate the upstream account's authority for its lifetime.

| Remote and resource | Allowed requests | Limit |
| --- | --- | --- |
| Source account | `GET`, `HEAD` | Fixed upstream account; no mutation methods |
| Destination account reads | `GET`, `HEAD` | Fixed upstream account; preserve exact listing parameters; a read may name an empty path segment, which a write may not |
| Upload or copy destination | `PUT`, `DELETE`; `POST` to an object for a child told to compare | The `Copy` of an upload or of a copy writes each object at its name ([ADR 0122](adr/0122-the-stores-answer-is-the-result.md) decisions 3 and 4): everything under the destination folder in the container and in its sidecar container, or the whole of both for a destination at the container's top; every `DELETE`, bulk included, is refused where a replaced large object's segments must be kept. A child of a confirmed upload, told to compare, may `POST` to an object it may write, which is how it sets the time of one it found the same but for its time (decision 12); a `POST` to a container, or carrying a manifest header, is refused |
| Destination staged objects | `PUT`, `DELETE` | Exact `CopyTo` staged name or fresh `Copy` batch prefix; never a live name or another invocation's stage. No job asks for this scope since the copy step of ADR 0122; it stays until that record's removal step |
| Destination generated segments | `PUT`, `DELETE` | Matching sidecar container and segment paths rooted in that invocation's staged object/batch path; the sidecar container alone is not authority to delete its other segments |
| Destination account abort cleanup | `DELETE` with `bulk-delete=1` | Bounded complete `text/plain` body; validate every URL-encoded `/container/object` against the same staged/segment scope before forwarding; refuse the whole request on any invalid or out-of-scope name |
| Matching sidecar container | Zero-body creation `PUT`; `GET`/`HEAD` | The gateway issues a fresh authenticated destination-container HEAD at validation time; require its one exact policy value, or no header when the HEAD omitted it; refuse other policy/ACL/metadata/versioning/history changes; no container deletion |
| Anonymous capability request | `GET` on the exact library-derived `/info` path | No upstream token; this class comes from the wire-library contract and was not emitted in the run below |
| Everything else | Refused locally | Any other `POST`, `COPY`, unscoped bulk delete, absolute request targets and out-of-scope paths remain forbidden unless a separate reviewed requirement changes this table |

DLO `PUT` requests, staged or at an upload's own name, must keep `X-Object-Manifest` inside the sidecar container, under the manifest object's own name.
Bulk-delete validation decodes each name once according to Swift's wire format, refuses malformed encoding and container-only entries, and applies explicit byte and name-count limits before any upstream request.
The response passes through unchanged because bulk middleware can report per-name failures inside HTTP 200.
The bulk body is limited to 4 MiB and 10,000 names, with two body buffers per gateway.
Each invocation limits connections to twice its transfer/checker counts plus 16, clamped to 64–1024 per remote.
Streaming uses a 32 KiB copy buffer, ten-second connect/TLS handshake limits, thirty-second response-header limits and sixty-second I/O idle limits.
Creation policy refusals may perform the validation HEAD but never forward a mutation; other structural refusals make no upstream request.
Only `close` and `keep-alive` are accepted as `Connection` options, preventing validated headers from being removed through a child-controlled hop-header list.

A request-recording extension of the existing two-account SAIO compatibility fixture ran on 5 September 2026 with the official Linux amd64 rclone 1.74.3 pin.
It exercised selected-name batch Copy, plain and DLO writes, CopyTo upload/download, and failure cleanup after an injected HTTP 403 on the second segment PUT.
The original recording logged method, escaped path, query and token presence, without token values or request bodies.
The preserved fixture now also records the bounded bulk-delete body containing only names in test-owned containers, and the sidecar storage-policy value, to make cleanup and creation scope directly inspectable.
The 73 observed requests were:

| Role | Request class | Count |
| --- | --- | ---: |
| Source | Container listing `GET` | 5 |
| Source | Object `GET` / `HEAD` | 13 / 14 |
| Destination | Container listing `GET` / `HEAD` | 2 / 8 |
| Destination | Staged object `HEAD` / `PUT` | 8 / 13 |
| Destination | Generated segment `PUT` | 8 |
| Destination | Sidecar-container `PUT` | 1 |
| Destination | Account-root bulk `DELETE` | 1 |

Every observed request carried an auth token.
The storage-policy follow-up repeated the 73-request experiment on 6 September and recorded `X-Storage-Policy: 1replica` on sidecar creation.
The independently reviewed [ADR amendment](adr/0014-child-storage-gateway.md#storage-policy-compatibility-amendment-2026-09-06) permits only the policy bound by the gateway's fresh authenticated HEAD of the destination container.
The bulk `DELETE` body names the failed upload's first successfully written generated segment; it is the observed segment cleanup, not a staged-object deletion.
No `POST`, `COPY`, anonymous `/info` or per-object `DELETE` was observed in this run.
Per-object staged/segment `DELETE` remains an allowance for the documented abort-cleanup contract.
The experiment's download bytes matched its source and the injected upload failed as intended.
The underlying measurement lane still reports only 11/16 names exact, the known five unsafe-name omissions, DLO prefix capture and missing user metadata; its passing exit remains a harness result, not behavioural compatibility approval.
Repeating this fixture through the implemented gateway produced 74 upstream requests: the same classes plus its fresh destination-container policy HEAD.
The segmented uploads, captured DLO prefix, byte-equal download and injected failure cleanup survived the gateway.
Separate maintained tests cover cancellation, TLS, header restrictions and listener teardown; [the validation record](experiments/2026-09-06-child-gateway.md) keeps platform gaps explicit.

The [recording fixture](experiments/2026-09-05-child-requests.go.txt) replaces `internal/integration/rclonematrix_test.go` through a Go overlay without changing the normal test suite.
With the existing test-owned SAIO environment and `AERONAUTES_MATRIX_RCLONE` pointing to the verified pin, reproduce it from the repository root:

```sh
python3 - <<'PY'
import json, pathlib, tempfile
repo = pathlib.Path.cwd()
overlay = pathlib.Path(tempfile.mkdtemp(prefix="aeronautes-requests-")) / "overlay.json"
overlay.write_text(json.dumps({"Replace": {
    str(repo / "internal/integration/rclonematrix_test.go"):
    str(repo / "docs/experiments/2026-09-05-child-requests.go.txt")
}}))
print(overlay)
PY
go test -overlay=<printed-overlay-path> -count=1 -run '^TestRcloneMatrix$' -v ./internal/integration
```

## Reserved runes (measured 2026-09-11 on v1.74.3)

The engine decodes every listed remote name through its backend encoding and quotes a literal substitute with U+201B, doubling a literal quote rune.
A raw selected-set entry whose decoded spelling differs from the raw name matches nothing the engine listed, so the engine copies nothing for it and exits 0: measured for U+201B anywhere, a C0 control picture (U+2401–U+241F), the DEL picture (U+2421) and a full-width full stop (U+FF0E) as a whole path component, against real Swift and the mock on both the download and copy routes; a full-width full stop component through the gateway fails the whole batch.
The full-width slash, colon and backslash, the NUL picture and a trailing space picture round-trip unchanged on Linux because both backends treat them alike, while the Windows local default also quotes the full-width forms of the seven forbidden characters and the backslash and the trailing space and full-stop pictures, which is a reading of the frozen source rather than an observation.
Names containing any rune of the pinned set (U+201B, U+2400–U+241F, U+2421, U+2420, U+FF0F, U+FF3C, U+FF0E, U+FF1A, U+FF1F, U+FF02, U+FF0A, U+FF1C, U+FF1E, U+FF5C) route native on every platform since PR97, through the same predicate that routes newline and dot-segment names; the set is derived from the frozen v1.74.3 encoder source whose digests the [experiment manifest](experiments/2026-09-11-download-name-encoding-probes.json) records, and it must be re-derived on any engine upgrade.

## Flags (per invocation)

| Flag | Driven by | Spike verification |
|---|---|---|
| `--files-from-raw <list>` | the planned selected set (§28.4). **`--files-from0` does not exist in 1.74.3**; raw is unprocessed but newline-delimited, so newline-bearing names can never ride rclone — they route native (§14.3) | ✓ space-edged names exact through raw list |
| `--ignore-times` | forced-set contract: transfer exactly the planned set, no mtime skipping (§28.4); left out for an upload or a download the operator confirmed may replace what differs, whose engine compares each name as rclone copy does ([ADR 0122](adr/0122-the-stores-answer-is-the-result.md) decision 12) | ✓ re-copied unchanged objects |
| `--no-traverse` with `--files-from-raw` | selected names are addressed directly; applies to remote Copy and local upload/download under [ADR 0022](adr/0022-complete-rclone-selection.md) | [Maintained parent/after/removed-option evidence](experiments/2026-09-07-selected-set-copy.md#maintained-implementation-and-evidence): complete capped mixed Copy, nested/DLO/SLO content and exact local transfers through the actual pin |
| `--use-json-log -vv` | progress + **generated segment prefix capture** (`Uploading segment file "<obj>/<ts>-<random>/<seq>" into "<container>_segments"`) feeding §26.4 ownership evidence | ✓ prefix captured in real time |
| `--retries N --low-level-retries N` | bounded failure budget (§26.2: control must return quickly) | ✓ 401 returns in ~2.6 s default, ~50 ms at 1/1 |
| `--transfers`, `--checkers`, `--tpslimit`, `--bwlimit` | performance profile (§40–41) | ✓ bwlimit throttling exercised |
| `--buffer-size` | the child's per-transfer read buffer, emitted explicitly for every job since 24 September 2026 (16Mi, the engine's own default, unless the job overrides it; [ADR 0074](adr/0074-workload-bounds.md) decision 3) so the record names it and transfers × buffer is the child memory to reason about | maintained argv test (`TestCopyRepresentationOptionArgs`) |
| `--stats-one-line` / stats via JSON log | drawer progress (§47) | ✓ |
| `--no-gzip-encoding` | fixed Copy-only transport policy under [ADR 0038](adr/0038-copy-representations.md); local runners retain the false default | [Locally reviewed representation tests](experiments/2026-09-12-copy-representations.md) cover both builders, both constructor paths, actual-Swift stored bytes and deliberately broken scope controls; combined delivery remains required |

The locally reviewed [ADR 0038 Copy representation correction](adr/0038-copy-representations.md) adds one fixed `--no-gzip-encoding` argument to both `copy` and `copyto` only for a runner explicitly selected by the Copy service.
It disables automatic gzip negotiation/decoding for requests through the child's Swift HTTP transport, sends no `Accept-Encoding` header itself, and retains the false default for local upload/download runners.
The [representation experiment](experiments/2026-09-12-copy-representations.md) preserves the unchanged-baseline gzip failures and the five original DLO plus four plain success cases after this correction.
The owned identity staged read precedes DLO publication; plain stages retain the existing native publication read, and neither set establishes encoded-listing or Range-retry behaviour.
The native reader, first staged-content consumer and representation correction still require their combined current-base delivery.
Keep the flag before `--` and preserve the existing gateway, credentials, TLS, retry, cleanup and metadata contracts.

## Deliberately never set

| Flag | Why |
|---|---|
| `--swift-storage-url`, `--swift-auth-token` | argv is readable at `/proc/<pid>/cmdline`; tokens go in env (§26.2) |
| `--swift-leave-parts-on-error` | rclone's default cleanup on abort is required (§26.4) — and even so, cancel cleanup is best-effort (a residual segment was observed), so reconciliation always follows |
| global `--ca-cert` / `--no-check-certificate` | process-global trust (§12.3, K-SEC-008) |
| `--max-duration` | HARD kills mid-transfer, SOFT still overruns — bounded batches instead (§26.2) |
| `sync`, `purge`, `delete`, `--swift-no-large-objects` toggling mid-job | the absolute destruction boundary (ADR 0004) and §25's single chunk knob. What that flag costs is measured in [the 2026-10-02 record](experiments/2026-10-02-engine-claims-measured.md): a replacement leaves the replaced object's segments behind, where the engine deletes them without it, and a read of an existing manifest fails the digest check |
| `--ignore-existing`, for a copy to a store | the public `NeedTransfer` checks it before `--ignore-times` can matter, so a planned object would be skipped silently rather than transferred; the runner refuses it for a remote destination and a guard in `internal/transfer/rclone/runner_test.go` keeps it out of that argv. A download into a local folder passes it on purpose ([ADR 0122](adr/0122-the-stores-answer-is-the-result.md) decision 5): there a file that exists must be left alone, and the engine says which files it left |

## Defaults this application depends on without setting them

Every row is a DATA outcome, not a speed one, and every default is the pinned
binary's own (`rclone help flags`, v1.74.3). Written down so a version bump has
something to be checked against; the enumeration and the reasoning are in
[docs/experiments/2026-09-29-engine-data-options.md](experiments/2026-09-29-engine-data-options.md).

| Default relied on | Pinned value | What breaks if it moves |
|---|---|---|
| `--swift-no-chunk` off | uploads of unknown length chunk | nothing for `copy`/`copyto` on known-size sources, which chunk above the configured size regardless; an unknown-length path would depend on it |
| `--swift-use-segments-container` unset | `<container>_segments` sidecar; `=false` puts them in the DATA container under `.file-segments/` (measured 2026-10-02) | the transfer itself: the scoped gateway admits segment writes only to `<container>_segments` under the staged prefix and its manifest check requires that container by name, so the dotted placement is refused. The capture is not what breaks — it reads the container out of the child's own notice rather than assuming one |
| `--swift-encoding` | `Slash,InvalidUtf8` | the names written change, and the native routing rules for unsafe names were measured under this value |
| `--swift-fetch-until-empty-page` off, `--swift-partial-page-fetch-threshold` 0 | pagination as measured | listing completeness — mostly moot, because `--no-traverse --files-from-raw` means the engine does not list our sources |
| `--inplace` off | the child writes each file as `<name>.<8 hex>.partial` in its destination folder and renames it to the name when it is whole, so no partial body is ever under the final name; interrupted, the child removes the partial file, and killed, it stays in the chosen folder, where the flag would leave a truncated body under the final name (measured 2026-10-02 and 2026-11-16) | that a stopped download leaves at most a `.partial` file and never a short file under a final name; the job says when one may be there ([ADR 0122](adr/0122-the-stores-answer-is-the-result.md) decision 5) |
| `--partial-suffix` | `.partial` | the temporary name a crash leaves for reconciliation and the operator to find |
| `--local-no-check-updated` off | the check happens | a source file changing during an upload stops being detected |
| `--local-no-sparse`, `--local-no-preallocate`, `--local-no-set-modtime` off | sparse, preallocated, modtime set | the on-disk shape of a partial download, and downloaded timestamps |
| `--local-encoding` | `Slash,Dot` | local names written; the dot-segment behaviour measured 2026-08-29 follows from it |
| `--multi-thread-cutoff` 256Mi, `--multi-thread-streams` 4, `--multi-thread-chunk-size` 64Mi | multi-stream above the cutoff | how many streams write one destination file at once, and its write granularity |
| `--max-transfer` off, `--cutoff-mode` HARD | no policy truncation | a transfer would stop part way; HARD kills mid-transfer, the same objection that keeps `--max-duration` off the never-set list |
| `--no-check-dest`, `--size-only`, `--no-update-modtime`, `--immutable` all off | comparison policy | little, because `--ignore-times` forces the transfer — stated so it is a decision rather than an inference. `--ignore-existing` is NOT in this row: it is checked before `--ignore-times` can matter, is forbidden for a copy to a store and is passed for a download (never-set table above) |

None of these is passed on the command line. Two more that would belong here
are instead in the never-set table above, because setting them either way is
forbidden rather than merely unnecessary.

## Observed 1.74.3 behaviours the planner depends on

- Segment prefix: `<object>/<date>-<time>-<22-char-random>/<seq>` in the auto-created
  `<container>_segments` sidecar; unique per upload; unknowable in advance.
- `copy` **exits 0 while silently skipping** rclone-unsafe names (leading `/`, trailing `/`,
  `//`) — completeness is verified by listing diff, never exit codes.
- **Dot-segment names** (`a/./b`, `a/../b`): `--files-from-raw` **silently skips** them
  (exit 0); a traversal copy **silently renames** them to fullwidth dots (U+FF0E, `a/．/b`).
  Measured 2026-08-29 against real Swift; both are identity violations → routed native.
  (Supersedes the first spike run's "dot segments survived", which measured the traversal
  path and mistook the renamed arrival for a round-trip.)
- SIGINT: cleans manifest + segments best-effort (one residual observed). SIGKILL: orphans
  remain, no manifest. Both paths end in reconciliation.
- Custom `X-Object-Meta-*` is not copied (with or without `-M`); Aeronautes re-applies
  metadata natively (§27.4). Destination DLOs get their own manifest.
- Store to store, at final names, measured 2026-11-16 on the owned Swift with the copy
  flags ([ADR 0122](adr/0122-the-stores-answer-is-the-result.md) decision 4):
  - one info line for each object written, `Copied (new)` or `Copied (replaced existing)`,
    naming the object as it was listed; a listed name that is not at the source gets no
    line and the child exits 0; a source that cannot be read whole gets an error line for
    that object, a non-zero exit, and nothing at the destination name;
  - the content type is copied, and `X-Object-Meta-Mtime` is the source's own when it has
    one and the time the source was last changed when it has none; user metadata,
    `Content-Disposition` and `Content-Encoding` are not copied;
  - a dynamic or static source larger than the chunk size arrives as a dynamic large
    object, and when cut at the source's chunk size it has the source's entity tag;
  - a link is copied as its target's bytes, as a plain object;
  - writing over a large object removes that object's old segments;
  - a POST to the dynamic manifest that does not repeat `X-Object-Manifest` leaves an
    empty plain object; with the header the object stays whole and its metadata is replaced.
- Store to a local folder, measured 2026-11-16 on the owned Swift with the download flags
  ([ADR 0122](adr/0122-the-stores-answer-is-the-result.md) decision 5):
  - one info line for each file written, `Copied (new)`, naming the object as it was
    listed; folders are made as needed; a listed name that is not at the source gets no
    line and the child exits 0;
  - the file's modification time is the source's `X-Object-Meta-Mtime` when it has one,
    and the time the source was last changed when it has none;
  - a dynamic or static large object downloads whole, and a link downloads as its
    target's bytes;
  - a local file that exists is REPLACED with the flags above alone; with
    `--ignore-existing` it is left untouched and the child prints a debug line for it,
    `Destination exists, skipping`, and no `Copied` line; with `--immutable` it is left
    untouched and the child exits 6 with an error line;
  - while a file is written it is `<name>.<8 hex>.partial` in the destination folder; on
    an interrupt the child removes it and exits 130; on a kill it stays;
  - a source that cannot be read whole gets an error line for that file, a non-zero exit,
    its partial file removed, and nothing at the final name.
- Without `--ignore-times`, in both directions, measured 2026-11-17 on the owned Swift with
  the runner's other flags, for a confirmed upload or download
  ([ADR 0122](adr/0122-the-stores-answer-is-the-result.md) decision 12):
  - a name of another size is replaced, `Copied (replaced existing)`; a local file by
    renaming the partial file over it, so a hard link to the old file still reads the old
    bytes;
  - a name of the same size and modification time is left, with the debug line
    `Unchanged skipping`, also when its bytes differ;
  - a name of the same size with only another time, whose MD5 matches, gets the info line
    `Updated modification time in destination` and then `Unchanged skipping`; to a store
    that is a POST to the object, which the application's gateway lets through only for
    such a run and only to an object under the destination folder; one whose MD5 differs
    is replaced (`md5 differ`);
  - a dynamic or static large object, and a file the engine uploads in segments, has no
    MD5 (`Src hash empty` or `Dst hash empty`), so one whose time alone differs is sent
    again in full and replaced;
  - with `--ignore-times` every existing name is transferred again and reported
    `Copied (replaced existing)`.

Re-run the seam spike suite on any pin upgrade; the unsafe-name classification (dot
segments included) is verified per version by the compatibility matrix below.

## Compatibility matrix

`make rclone-matrix` fetches official builds (zips verified against their published
SHA256SUMS), drives each through the §14.3 suite and the runner's load-bearing behaviours on
the SAIO, and reports findings. **A pin change is gated on a recorded run here.**

Recorded run 2026-08-30 (linux-amd64, SAIO):

| behaviour | v1.74.3 (pin) | v1.75.0 (latest) |
|---|---|---|
| `--files-from-raw` | yes | yes |
| `--files-from0` (NUL-delimited) | **no** | **yes** |
| silently dropped at exit 0: leading `/`, trailing `/`, `//`, `.`/`..` segments | yes (5/16 suite names) | yes (identical set) |
| newline names in any list | impossible | untested via files-from0 (future §14.3 path) |
| 3 MiB at 1 Mi chunks → destination class | DLO | DLO |
| segment-prefix capture (§26.4 log regex) | works | works |
| custom `X-Object-Meta-*` carried | no | no |

v1.75.0 is behaviourally identical on every measured hazard; its `--files-from0` is the first
concrete improvement path for newline-bearing names (they currently route native), to be
validated end-to-end before any pin change relies on it.

## copyto

No job uses `copyto` since the copy step of ADR 0122; what follows describes the staged copy it served, and goes with the removal step.

The §28.6 staging path uses `rclone copyto <src-remote:container/name> <dst-remote:container/staged-name>`
for single-object copies under a different (always Aeronautes-generated, unique, rclone-safe)
destination name — batch `copy` cannot rename. Same flag surface as `copy` minus
`--files-from-raw`; validated against real Swift in the executor integration suite
(stage-and-commit overwrites, segment-prefix capture on staged large objects).
