# The three engine claims, measured

The 2026-09-29 engine data-options record ended with three claims it had not
measured, ranked by what they would cost us if wrong. This measures all three
against a real store and corrects that record where the measurement disagrees
with it — which it does, in three places.

**Method.** The engine is the pinned binary, `rclone v1.74.3`, given the same
child configuration shape `internal/transfer/rclone/runner.go` builds: a
pre-obtained token and storage URL in the environment, `ENV_AUTH` false, an empty
`--config`, `--use-json-log -vv --stats 1s`, and nothing of the operator's
environment but `PATH` and a private `HOME`. It is NOT driven through the
application: this producer talks straight to SAIO, so the scoped gateway, the
staged destination, the native publication and the recovery route are not
exercised here, and nothing below should be read as a statement about them. The store is SAIO. Every claim about what the store holds is read
back independently through the Swift REST API rather than from the engine's own
report, and the probe interprets nothing: exits are integers and bodies are raw.
The source material is one deterministic 3 MiB file and one 6-byte file, and the
chunk size is 1 MiB, so a large object is three segments. The containers the
probe uses are its own; the SAIO fixture containers are untouched.

Evidence: `.aeronautes-work/20261002-engine-claims/run` — `PROBE.json`, every
child's raw log, and `SHA256SUMS`.

## 1. `--swift-no-large-objects` against an existing manifest

The earlier record claimed: *an upload past the chunk size fails, and an existing
manifest is not recognised as one — a replacement could then treat a manifest as
ordinary bytes.* Measured, the first half is false, and the second half is true in
a narrower and more useful way than that sentence says.

- **An upload past the chunk size does not fail.** With the flag set and a 1 MiB
  chunk size, the 3 MiB file was sent as ONE object: exit 0, `Copied (new) to:
  with-flag.bin`, and the store reports `Content-Length: 3145728` with no
  `X-Object-Manifest`. The flag does not bound an upload by the chunk size; it
  removes chunking, so the bound becomes the store's own maximum for a single
  object, which this measurement does not reach and so does not establish.
- **An existing manifest is not recognised.** `lsjson` of the same DLO reports
  `"Size":0` with the flag and `"Size":3145728` without it. The manifest object's
  own zero length is what the engine reports when it will not read the manifest.
- **A read of that manifest fails rather than quietly returning the wrong
  bytes.** With the flag, the engine fetched the concatenated body and compared it
  with the DLO's own ETag — which Swift computes from the segment ETags, not from
  the body — reported `corrupted on transfer: md5 hashes differ`, logged
  `Removing failed copy`, made three attempts in total and left no file at all. Without the
  flag the same read succeeded and matched the source digest. So the hazard here is
  a failed read, not a silent one.
- **A replacement with the flag orphans the old segments.** Replacing a 3 MiB DLO
  with a 6-byte object left the name holding 6 bytes with no manifest in both
  modes, but the segment count tells them apart: with the flag the replaced
  object's three segments were still there afterwards, and without the flag the
  engine deleted them. That is the claim's consequence, and it is real.

**What it means for us — and what it does not.** The runner never sets this
flag, so an engine upload chunks. What this application never does is let the
engine replace the live object: `Upload` sends the engine to
`.aeronautes/stage/<job>/batch/`, publishes the live object itself with a native
`PutObject`, and reclaims the predecessor's recorded segments natively after
verification (`internal/transfer/local/upload.go`, `upload_recording.go`). ADR
0081 draws that line explicitly, and K-XFER-014 with K-SEG-005 preserve it. So
the engine's own replacement cleanup, measured above, is **not reached by our
upload route**, and ADR 0081 decision 5 is the application's mechanism for
reclaiming a replaced large object's segments rather than a backstop to the
engine's. The measured engine behaviour matters to someone who points the engine
at a live object, which this application does not do, and the flag's own hazard
needs the flag, which `docs/rclone.md` forbids mid-job.

## 2. `--inplace` against a cancelled download

The earlier record claimed the flag changes the shape of stage residue after an
interrupted child, and is not what keeps a partial body out of the final path.
Measured, both halves hold, and the first is stronger than "shape".

A download of the 3 MiB DLO was slowed to 256 KiB/s and the whole process group
was sent SIGTERM four seconds in.

- With `--inplace`: the final name exists holding 520192 of 3145728 bytes, and
  its digest does not match the source. A truncated body under the name the
  caller asked for.
- Without it: the directory is EMPTY. The engine removed its own temporary file
  when the child was signalled, so the default leaves no residue at all rather
  than residue under a different name.

**What it means for us.** Nothing changes about final-path safety, which is the
child's outcome, the stage's identity, and the native held-file size and digest
verification in `copyIntoStage` and `placeDownloaded`. What changes is what a
stage sweep would find: with the flag, a file that looks finished and is not.

## 3. `--swift-use-segments-container` against the recovery records

The earlier record claimed that this option would make segments *land somewhere
other than where every ownership and recovery record this application writes
expects them*. Measured, that is wrong, and the reason is worth stating.

Placement, read back from the store:

| mode | manifest value | segments actually in |
| --- | --- | --- |
| unset | `engine-claims-p3_segments/seg-unset.bin/<run id>` | the `_segments` sidecar |
| `=true` | `engine-claims-p3_segments/seg-true.bin/<run id>` | the `_segments` sidecar |
| `=false` | `engine-claims-p3/.file-segments/seg-false.bin/<run id>` | the DATA container, under `.file-segments/` |

With the option unset the child says so itself: `Auto set use_segments_container
to true`. All three modes produced DLOs; `X-Static-Large-Object` was absent
throughout, so the pinned engine's SLO path stays unmeasured.

What this application records is not a convention it assumes. `PrefixFromEvent`
reads the container out of the child's own notice —

    Uploading segment file ".file-segments/seg-false.bin/<run id>/00000000" into "engine-claims-p3"

— so in every mode the recorded container is the container the segments are
actually in, which the probe checked against the store's own listing. The claim
is corrected in the earlier record and in `docs/rclone.md`.

That is evidence about the capture, and only about the capture. It does not
establish that this application could complete such an upload, and it could not:
**our own scoped gateway would refuse the dotted placement.** `Runner.copy`
starts that gateway before it builds the child's environment, and
`gatewayScope.allowsObject` admits a write to the data container only under the
staged destination prefix, and a write to another container only when that
container is `<container>_segments` and the object is under the same staged
prefix. A segment at `.file-segments/<name>/…` in the data container satisfies
neither, and a DLO publication naming the data container fails the separate
manifest check, which requires the sidecar container by name. The gateway's
handler refuses those writes, so a changed placement **fails the transfer** — it
does not quietly move the segments.

Beyond that, one display remark, for containers this application did not write:
`evidence.SuspectedName`, the §24.3 naming heuristic over container names
(`_segments`, `-segments`, `segments`, `.segments`), would not mark a container
holding dotted segments as suspected, because the prefix is inside it rather than
in its name. That heuristic is display grouping and authorises nothing.

**Maintained from here, exactly this far.** The three real notices are now a
test case in `internal/transfer/rclone/runner_test.go`, including the one where
the sidecar is refused. It supplies those retained strings to the parser; it does
not start the engine. So it catches a PARSER regression against the formats
recorded here — three mutants of the parser each fail it by name, and the
sidecar-only one fails only on the refused case — and it would NOT notice a
future engine emitting a different line shape while the parser and these strings
stay as they are. That gap wants a live-engine case, which this slice does not
add.

## What this still does not measure

- The engine's remote-control interface and its config-file options. Only
  command-line flags and the environment this application whitelists were used.
- The SLO path. The pinned engine wrote DLOs in every case here.
- The store's maximum for a single object, which bounds `--swift-no-large-objects`
  uploads. The 3 MiB case is far below it.
