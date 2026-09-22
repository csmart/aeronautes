# Download inventory peak memory — 22 September 2026

L10 requires reviewed bounds for queued work, browser retention, child batches and aggregate resources, and the [finish line](../audits/2026-09-05/linux-finish-line.md) requires the missing workload-limit decisions before dependent implementation.
The [download preflight bounds record](2026-09-16-download-preflight-bounds.md) established that the preflight grows rather than truncates, measured cumulative allocation to 20 000 objects, and left peak resident memory unmeasured.
This record measures the Go process's peak resident set, retained heap and wall time for `local.PreflightDownload` from one thousand to half a million objects, so the operator's object and RAM ceiling decision can rest on numbers.
It adopts no limit, changes no code and closes nothing.

## Method

The standalone `cmd/mockswift` from master `bf229c39` ran as a separate process on an ephemeral loopback port with its canonical fixtures disabled, so the probe process's resident set excludes the mock's own storage.
A private test in `internal/transfer/local` seeded one-byte objects named `scope/dNNN/sNNN/object-NNNNNN.bin` (33 characters each) through thirty-two concurrent PUTs, cumulatively in one container, then called `PreflightDownload` for the `scope/` prefix into an empty destination directory.
Around each call it read `runtime.MemStats` and `/proc/self/status`, resetting the peak (`VmHWM`) through `/proc/self/clear_refs` before each size, and read them again after dropping the report, `runtime.GC` and `debug.FreeOSMemory`.
Every size returned exactly the seeded count of items with no problems.

Host: 24 cores, 55 874 MiB of RAM, load average 0.30 before and 3.50 after the 25-second run, no other suite running, Go 1.27.1, race detector off.
The probe source, the raw JSON rows and the host notes are retained privately in `.aeronautes-work/20260922-l10-peak-memory/` with `SHA256SUMS`.

## Measurements

| Objects | Preflight | Retained heap with the report alive | Resident growth | Peak resident set | Resident after release | Cumulative allocation | Internal report as JSON |
| --- | --- | --- | --- | --- | --- | --- | --- |
| 1 000 | 0.010 s | 1.5 MiB | 2.6 MiB | 18.2 MiB | 16.2 MiB | 3.9 MiB | 0.12 MiB |
| 10 000 | 0.080 s | 5.3 MiB | 13.4 MiB | 30.5 MiB | 17.4 MiB | 41.5 MiB | 1.17 MiB |
| 50 000 | 0.392 s | 28.2 MiB | 37.3 MiB | 66.7 MiB | 18.5 MiB | 204.8 MiB | 5.87 MiB |
| 100 000 | 0.865 s | 52.5 MiB | 86.6 MiB | 110.0 MiB | 23.7 MiB | 410.5 MiB | 11.73 MiB |
| 250 000 | 3.244 s | 109.4 MiB | 313.2 MiB | 352.0 MiB | 24.0 MiB | 1 130.5 MiB | 29.33 MiB |
| 500 000 | 10.134 s | 338.3 MiB | 584.9 MiB | 634.7 MiB | 28.9 MiB | 2 256.0 MiB | 58.65 MiB |

Resident growth is the difference between the resident set after the call and before it; the peak column is the process high-water mark during the call, which includes the roughly 16 MiB the test binary held before any inventory.
Retained heap is what the live report holds after the call; it is smaller than resident growth because the Go runtime keeps freed spans resident until it returns them.

Per object at 100 000, 250 000 and 500 000 objects: cumulative allocation 4.2, 4.6 and 4.6 KiB; peak resident growth (peak minus the resident set before that call) 0.94, 1.34 and 1.25 KiB; retained heap 0.54, 0.45 and 0.69 KiB; internal JSON 123 bytes at every size; wall time 8.7, 13.0 and 20.3 microseconds.
Wall time grows faster than the count: 250 000 objects took 3.2 s and 500 000 took 10.1 s.
Resident memory is released once the report is dropped: the final released figure, 28.9 MiB, is 13.4 MiB above the 15.5 MiB the process held before the first call.

A million objects is arithmetic from these rows, not a measurement: doubling the measured half-million peak gives 1.24 GiB of peak resident memory, with tens of seconds of preflight on this host.

## What this does not measure

- Real Swift listing latency: the mock answers each ten-thousand-name page in microseconds, so the preflight seconds above are almost entirely local work; against a deployed endpoint the listing round trips dominate and the memory figures stand.
- Longer names: the retained cost scales with name length because the preflight keeps the name, the relative path and two case-folded index keys per item.
- The engine's own batch: `StartDownload` hands the complete selected file list to the pinned rclone in one `files-from-raw` batch; that child's memory and the size of that list are not measured here.
- Browser retention, queued-job startup resources and aggregate endpoint resources, which remain the other open L10 items.

## Proposal for the operator's decision

The finish line names the ceiling as an owner decision, so this record proposes and does not decide.
The measured points give three candidate per-selection object ceilings with their cost on this host:

| Candidate ceiling | Peak resident set | Preflight on the mock |
| --- | --- | --- |
| 100 000 objects | 110 MiB | 0.9 s |
| 250 000 objects | 352 MiB | 3.2 s |
| 500 000 objects | 635 MiB | 10.1 s |

The recommendation is 250 000 objects per download selection as the supported v1 maximum, refused with the counted total and the ceiling named, because it keeps the Go process under half a gibibyte for the inventory alone while covering namespaces well beyond the 20 000-object cases measured so far.
A refusal can stop the listing once the count passes the ceiling, so an oversize selection costs at most the ceiling's inventory before it is refused.
The equivalent memory-based rule, refusing once the counted inventory would exceed a byte budget at the measured per-object cost, is possible but harder to state to an operator and harder to test.
If the operator accepts a ceiling, the implementation and its refusal contract belong in a separate reviewed slice with a new decision record; nothing in this record enables one.

## Limits

One host, one mock, one name shape and one-byte bodies; the figures bound the inventory's own cost, not a deployment's.
No finding, gate, scenario credit or release acceptance moves, and L10 stays open.
