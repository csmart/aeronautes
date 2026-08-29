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
- SIGINT: cleans manifest + segments best-effort (one residual observed). SIGKILL: orphans
  remain, no manifest. Both paths end in reconciliation.
- Custom `X-Object-Meta-*` is not copied (with or without `-M`); Aeronautes re-applies
  metadata natively (§27.4). Destination DLOs get their own manifest.

Re-run the seam spike suite on any pin upgrade; dot-segment name safety in particular is
verified for 1.74.3 only.
