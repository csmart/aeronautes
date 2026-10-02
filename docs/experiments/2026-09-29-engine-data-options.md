# What the pinned engine's options do to DATA, and which defaults we lean on

**Question** (operator, 27 September 2026, deferred by agreement and taken up
now): not performance knobs, but further gotchas of the
`leave_parts_on_error` kind — options we have never discovered, used or
exposed, which could produce unexpected behaviour or orphaned data. It
sharpens because this application now deletes separately from the engine
rather than letting the engine delete for it, so the arguments we pass have
to be the right ones.

**Scope settled here**: which options change DATA rather than speed; which of
those this application already decides about; which defaults it depends on
without saying so; and, for each, whether to record the reliance, expose it,
or leave it.

## Method, and what it does and does not establish

The flag list and every default below are the **pinned binary's own output**,
`rclone help flags` from `rclone v1.74.3` at
`/home/csmart/code/.aeronautes-work/rclone-pin/rclone`, not a documentation
page for some other version. That is a measurement of the pin.

It is NOT a behavioural measurement of each option. Where behaviour has been
measured against real Swift, the row says so and names the packet. Where it
has not, the row says what the option claims and treats that as a claim. A
row that matters enough to depend on, and is not yet measured, is named as
owed rather than quietly credited.

`--swift-storage-policy` is largely out of scope, with one qualification: user
containers are created natively, so the engine's policy flag does not choose
theirs — but the engine MAY create a permitted segments container of its own
during a large upload, and the policy that container gets is bound by the
gateway rather than by this flag. It is listed here so the exclusion is not read
as "the engine never creates a container".

## The options that change data, and what we do about them

### Swift backend

| Option | Pinned default | What we do | Data effect if it moved |
|---|---|---|---|
| `--swift-leave-parts-on-error` | off (abort cleans up) | **Never set** (`docs/rclone.md`), relying on the default | The engine's own failed large upload leaves its parts behind, as residue nothing in this application recorded. **Measured** 2026-09-27 (`.aeronautes-work/20260927-overwrite-mechanism/`): with it true the parts stay. |
| `--swift-no-large-objects` | off (SLO/DLO supported) | Never toggled mid-job (`docs/rclone.md`) | **Measured 2026-10-02** (`2026-10-02-engine-claims-measured.md`): an upload past the chunk size does NOT fail, it is sent as one object; an existing manifest is not recognised (a listing reports size 0) and a read of it FAILS the digest check rather than returning the wrong bytes; and a replacement orphans the old segments, which the engine deletes when the flag is absent. |
| `--swift-no-chunk` | off | **Unrecorded reliance, and narrower than it looks** | The pinned help scopes it to uploads of UNKNOWN length: a streaming upload is sent whole rather than chunked. The runner uses `copy` and `copyto` on known-size sources, which still chunk above the configured size, so this is not a chunk-size dependency of our normal copies. Recorded because an unknown-length path would acquire one. Help text, unmeasured. |
| `--swift-use-segments-container` | unset (the child logs `Auto set use_segments_container to true`) | **Recorded** in `docs/rclone.md` with the option that moves it | **Measured 2026-10-02**: `=false` puts segments inside the DATA container under `.file-segments/`, not in the sidecar. The capture would still record the right container, because it reads the container out of the child's own notice — but our own scoped gateway admits segment writes only to `<container>_segments` under the staged prefix, and its manifest check requires that container by name, so a changed placement **fails the transfer** rather than moving segments quietly. |
| `--swift-chunk-size` | 5Gi | **Set explicitly** per job | — |
| `--swift-encoding` | `Slash,InvalidUtf8` | **Unrecorded reliance.** The name-form section of `docs/rclone.md` measures behaviour that follows from it | The NAMES written change, which is an identity change, and the native routing rules for unsafe names were measured under this default. Claim, unmeasured. |
| `--swift-fetch-until-empty-page` | off | **Unrecorded reliance**, and mostly moot: `--no-traverse --files-from-raw` means the engine does not list the source for our copies | Listing completeness during pagination. Where the engine does not list, it cannot bite. |
| `--swift-partial-page-fetch-threshold` | 0 | as above | as above |

### Local backend — it applies to us, because downloads write through it

`copyIntoStage` in `internal/transfer/local/download_execute.go` gives the
engine a local filesystem destination, so the local backend's defaults govern
what a download leaves on disk.

| Option | Pinned default | What we do | Data effect if it moved |
|---|---|---|---|
| `--inplace` | off (temp name, then rename) | **Unrecorded reliance, inside the stage only** | The name it affects is one inside this application's private download stage, so what changes is stage RESIDUE after an interrupted child. It is not what keeps a partial body out of the final path: publication is gated by the child's outcome, the stage's identity and the native held-file size and digest verification in `copyIntoStage` and `placeDownloaded`. **Measured 2026-10-02**: a cancelled download leaves a truncated body under the FINAL name with the flag, and leaves nothing at all without it — the engine removes its own temporary file when the child is signalled. |
| `--partial-suffix` | `.partial` | Unrecorded reliance | The temporary name a crash leaves behind, which reconciliation and the operator both see. |
| `--local-no-check-updated` | off (the check happens) | Unrecorded reliance | A source file changing during an upload would stop being detected. |
| `--local-no-sparse` | off (sparse files used for multi-thread downloads) | Unrecorded reliance | A cancelled large download's file shape on disk. |
| `--local-no-preallocate` | off (preallocation happens) | Unrecorded reliance | Space accounting and the shape of a partial file. |
| `--local-no-set-modtime` | off (modtime set) | Unrecorded reliance | Timestamps on downloaded files. |
| `--local-encoding` | `Slash,Dot` | Unrecorded reliance | Local NAMES written, and the dot-segment behaviour measured 2026-08-29 follows from it. |

### Global

| Option | Pinned default | What we do | Data effect if it moved |
|---|---|---|---|
| `--multi-thread-cutoff` | 256Mi | **Unrecorded reliance** | Above it a download is written by several concurrent streams into one file; below it, one. |
| `--multi-thread-streams` | 4 | Unrecorded reliance | How many streams write that file at once. |
| `--multi-thread-chunk-size` | 64Mi | Unrecorded reliance | The write granularity of the above. |
| `--max-transfer` / `--cutoff-mode` | off / HARD | Unrecorded reliance | A transfer would stop part way by policy; HARD kills mid-transfer, which is the same objection that keeps `--max-duration` off the list already. |
| `--ignore-existing` | off | **Never set, and a guard forbids it** (`internal/transfer/rclone/runner_test.go`) | `--ignore-times` does NOT neutralise it: the public `NeedTransfer` checks `ignore-existing` first and skips the name outright, so a planned object would silently not be transferred. It belongs with the never-set flags, not with the harmless defaults. |
| `--no-check-dest`, `--size-only`, `--no-update-modtime`, `--immutable` | all off | Not set | Comparison and refusal policy. `--ignore-times` forces the transfer, so these change little for our copies; stated so it is a decision rather than an inference. |
| `--error-on-no-transfer` | off | Not set; completeness is verified by listing diff, never by exit code (`docs/rclone.md`) | — |

## What this changes

Nothing in the product. Every default above is the one this application wants,
and two of them are already decided in `docs/rclone.md`'s never-set table with
their reasons. What was missing is the **statement of reliance** for the other
sixteen, so this record adds a table to `docs/rclone.md` naming them. A
reliance that is written down survives a version bump, because the bump has
something to be checked against.

**Deliberately not done here**: passing any of these as explicit pins. The
obvious candidates are exactly the two `docs/rclone.md` already forbids on the
command line — `--swift-leave-parts-on-error` because the default cleanup is
required, and `--swift-no-large-objects` because toggling it mid-job crosses
the destruction boundary — and the guard in
`internal/transfer/rclone/runner_test.go` refuses either string in argv. Pinning
a value that equals the default would preserve behaviour and break that
contract for no behavioural gain, so the reliance is recorded instead. If the
operator would rather the dependence be enforced in argv than recorded, that is
a contract change to `docs/rclone.md` and its guard, and it should be proposed
as one.

## Exposure

**Recommended: expose none of them.** The operator's question asked which the
interface should offer, and the answer this record reaches is none, for one
reason each:

- The options whose default we depend on would, if exposed, let an operator
  turn off a behaviour the rest of the application's records assume —
  segment placement, manifest recognition, atomic download naming. An
  affordance that can invalidate a durable record is not a setting.
- `--swift-leave-parts-on-error` is the one an operator might plausibly want,
  to keep the parts of a failed upload for inspection. It still should not be
  exposed: what it leaves is residue no record names, and this application's
  answer to uncertain residue is to retain what it KNOWS about
  ([ADR 0020](../adr/0020-recovery-containment.md)) rather than to create
  more that it does not.
- The performance knobs that already are exposed (`--transfers`,
  `--checkers`, `--tpslimit`, `--bwlimit`, `--buffer-size`,
  `--swift-chunk-size`) stay as they are; none of them changes what the store
  ends up holding.

## Owed after this

- **Done 2026-10-02**: the three worth measuring were measured against SAIO with
  the pinned binary, and the rows above carry the results —
  `2026-10-02-engine-claims-measured.md` holds the method, the raw evidence and
  the three places where the measurement disagreed with this record. The claims
  still marked as claims above are the ones nothing has measured yet.
- Unmeasured and worth knowing: the pinned engine wrote a DLO in every case, so
  the SLO path is untouched, and no case reached the store's own maximum for a
  single object, which is what actually bounds a `--swift-no-large-objects`
  upload. The measurement also ran the engine STRAIGHT at the store, so it says
  nothing about the scoped gateway, the staged destination or the native
  publication and reclamation that the application's own routes use.
- Nothing here examined the engine's **remote-control** interface or its
  config-file options, only command-line flags and the environment this
  application already whitelists.
