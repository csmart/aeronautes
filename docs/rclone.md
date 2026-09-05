# The enumerated rclone surface

The exact rclone configuration and flags Aeronautes sets — nothing else is ever passed (§44,
ADR 0007). Every entry below was verified against the pinned **rclone v1.74.3** by the seam
spike (§60, run 2026-08-29 against upstream containerized Swift). A behaviour with no entry
here is a gap to resolve deliberately, never a free-form argument.

## Remote configuration (per child)

| Key | Value | Driven by | Spike verification |
|---|---|---|---|
| `RCLONE_CONFIG` | job-scoped empty/minimal file, or `/dev/null` | every spawn (§26.2: the operator's own rclone.conf must never leak in) | all spike runs used it |
| `RCLONE_CONFIG_<R>_TYPE` | `swift` | — | ✓ |
| `RCLONE_CONFIG_<R>_ENV_AUTH` | `false` | — | ✓ |
| `RCLONE_CONFIG_<R>_STORAGE_URL` | bookmark's storage URL | bookmark (§11) | ✓ token-only remotes list/copy/upload |
| `RCLONE_CONFIG_<R>_AUTH_TOKEN` | short-lived token — **environment only, never argv, never the key** | session (§26.2) | ✓ |
| `override.ca_cert` / `override.no_check_certificate` | **config-file only** (env forms silently ignored) | bookmark TLS (§12.3) | ✓ honoured via file; ✗ env forms; **trust leaks across remotes in one child** → same-trust child only, else two children (K-SEC-008). When used, the remote's non-secret keys move into the job config file; the token stays in the environment (file+env merge verified) |
| `RCLONE_CONFIG_<R>_CHUNK_SIZE` / `--swift-chunk-size` | §25 single knob (default 5 GiB capped at `max_file_size`) | Large Objects setting (§43.4) | ✓ 1 MiB test segmentation |

Remote names are fixed: `aeronautes_src:` / `aeronautes_dst:` (§26.2).

## Gateway request policy (proposed)

[ADR 0014](adr/0014-child-storage-gateway.md) proposes applying the following allowlist before a child request can reach Swift.
The gateway is not implemented yet.
The role limit is additional authority control; a local token without these restrictions would delegate the upstream account's authority for its lifetime.

| Remote and resource | Allowed requests | Limit |
| --- | --- | --- |
| Source account | `GET`, `HEAD` | Fixed upstream account; no mutation methods |
| Destination account reads | `GET`, `HEAD` | Fixed upstream account; preserve exact listing parameters |
| Destination staged objects | `PUT`, `DELETE` | Exact `CopyTo` staged name or fresh `Copy` batch prefix; never a live name or another invocation's stage |
| Destination generated segments | `PUT`, `DELETE` | Matching sidecar container and segment paths rooted in that invocation's staged object/batch path; the sidecar container alone is not authority to delete its other segments |
| Destination account abort cleanup | `DELETE` with `bulk-delete=1` | Bounded complete `text/plain` body; validate every URL-encoded `/container/object` against the same staged/segment scope before forwarding; refuse the whole request on any invalid or out-of-scope name |
| Matching sidecar container | Bare creation `PUT`; `GET`/`HEAD` | Refuse ACL, metadata, versioning, history and storage-policy headers; no container deletion |
| Anonymous capability request | `GET` on the exact library-derived `/info` path | No upstream token; this class comes from the wire-library contract and was not emitted in the run below |
| Everything else | Refused locally | `POST`, `COPY`, unscoped bulk delete, absolute request targets and out-of-scope paths remain forbidden unless a separate reviewed requirement changes this table |

Staged DLO `PUT` requests must keep `X-Object-Manifest` inside the invocation's matching sidecar prefix.
Bulk-delete validation decodes each name once according to Swift's wire format, refuses malformed encoding and container-only entries, and applies explicit byte and name-count limits before any upstream request.
The response passes through unchanged because bulk middleware can report per-name failures inside HTTP 200.

A request-recording extension of the existing two-account SAIO compatibility fixture ran on 5 September 2026 with the official Linux amd64 rclone 1.74.3 pin.
It exercised selected-name batch Copy, plain and DLO writes, CopyTo upload/download, and failure cleanup after an injected HTTP 403 on the second segment PUT.
The original recording logged method, escaped path, query and token presence, without token values or request bodies.
The preserved fixture now also records the bounded bulk-delete body containing only names in test-owned containers, to make abort-cleanup scope directly inspectable.
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
The bulk `DELETE` body names the failed upload's first successfully written generated segment; it is the observed segment cleanup, not a staged-object deletion.
No `POST`, `COPY`, anonymous `/info` or per-object `DELETE` was observed in this run.
Per-object staged/segment `DELETE` remains an allowance for the documented abort-cleanup contract, with real cancellation coverage still required before implementation approval.
The experiment's download bytes matched its source and the injected upload failed as intended.
The underlying measurement lane still reports only 11/16 names exact, the known five unsafe-name omissions, DLO prefix capture and missing user metadata; its passing exit remains a harness result, not behavioural compatibility approval.
Cancellation, TLS, header restrictions, platform behaviour and the gateway itself remain untested by this experiment and retain their separate gates.

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

## Flags (per invocation)

| Flag | Driven by | Spike verification |
|---|---|---|
| `--files-from-raw <list>` | the planned selected set (§28.4). **`--files-from0` does not exist in 1.74.3**; raw is unprocessed but newline-delimited, so newline-bearing names can never ride rclone — they route native (§14.3) | ✓ space-edged names exact through raw list |
| `--ignore-times` | forced-set contract: transfer exactly the planned set, no mtime skipping (§28.4) | ✓ re-copied unchanged objects |
| `--use-json-log -vv` | progress + **generated segment prefix capture** (`Uploading segment file "<obj>/<ts>-<random>/<seq>" into "<container>_segments"`) feeding §26.4 ownership evidence | ✓ prefix captured in real time |
| `--retries N --low-level-retries N` | bounded failure budget (§26.2: control must return quickly) | ✓ 401 returns in ~2.6 s default, ~50 ms at 1/1 |
| `--transfers`, `--checkers`, `--tpslimit`, `--bwlimit` | performance profile (§40–41) | ✓ bwlimit throttling exercised |
| `--stats-one-line` / stats via JSON log | drawer progress (§47) | ✓ |

## Deliberately never set

| Flag | Why |
|---|---|
| `--swift-storage-url`, `--swift-auth-token` | argv is readable at `/proc/<pid>/cmdline`; tokens go in env (§26.2) |
| `--swift-leave-parts-on-error` | rclone's default cleanup on abort is required (§26.4) — and even so, cancel cleanup is best-effort (a residual segment was observed), so reconciliation always follows |
| global `--ca-cert` / `--no-check-certificate` | process-global trust (§12.3, K-SEC-008) |
| `--max-duration` | HARD kills mid-transfer, SOFT still overruns — bounded batches instead (§26.2) |
| `sync`, `purge`, `delete`, `--swift-no-large-objects` toggling mid-job | the absolute destruction boundary (ADR 0004) and §25's single chunk knob |

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

The §28.6 staging path uses `rclone copyto <src-remote:container/name> <dst-remote:container/staged-name>`
for single-object copies under a different (always Aeronautes-generated, unique, rclone-safe)
destination name — batch `copy` cannot rename. Same flag surface as `copy` minus
`--files-from-raw`; validated against real Swift in the executor integration suite
(stage-and-commit overwrites, segment-prefix capture on staged large objects).
