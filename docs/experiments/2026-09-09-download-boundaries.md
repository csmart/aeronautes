# Download safety and recording prerequisites

## Scope and oracle

These before-fix reproductions establish the download starting point for [ADR 0030](../adr/0030-copy-item-recording.md) step five and the local safety work in P18/P19/P20/P28.
The identity probe ran on reviewed upload source `039e77584631307448c1d0f5d37691fe3b9d222b`.
The other four ran on `99a2a4795b9de3760b1df5a37592275615aa2843`, which changes only an upload fixture wait and its documentation.
Of 311 Go/SQL inputs, only that upload test differs; all download application and executor code is identical.
The original probes add test files through Go overlays without replacing production code.
Their exact executed bytes are archived as inert text in [the probe manifest](2026-09-09-download-probes.json).
The separate cleanup-isolation experiment explicitly replaces one executor line and is not a proposed production patch.

The behaviour oracle is the exact selected identity and bytes, the backend-captured destination path, independently observed local effects and the retained job/item/audit state.
Successful counters cannot replace full content and collateral checks.
The service fixtures use injected backend pickers, actual preflight and job services, pinned rclone on the engine route, native HTTP on the other route, and owned mock Swift and two-account SAIO.
They do not exercise native OS dialogs or certify six platforms.

| Boundary | Scope | Expected failing leaves | Passing controls |
| --- | --- | ---: | ---: |
| [Failure identity](probes/2026-09-09-download-identity_test.go.txt) | Actual services, both routes and providers | 8 | 8 |
| [Captured destination root](probes/2026-09-09-download-root_test.go.txt) | Actual services, both routes and providers | 4 | 4 |
| [Native stream closure](probes/2026-09-09-download-close_test.go.txt) | Actual native services and both providers | 2 | 2 |
| [Placement followed by unlink refusal](probes/2026-09-09-download-placement_test.go.txt) | Local executor, existing test engine, real POSIX filesystem | 1 | 1 |
| [Unrelated staging data](probes/2026-09-09-download-cleanup_test.go.txt) | Actual services, real child, response barrier, both providers | 2 | 2 |

Each original producer exits 1 for its expected observations, with no skipped cases or unrelated failures.
The 34 leaves comprise 17 deliberate failures and 17 passing controls; their nine parents also fail.
These are before-fix evidence, not passing CI or new scenario coverage.

## Failure identities and exact totals

Each actual download selects 0, 1, 201 or 401 failing names plus one successful file.
For native execution, a newline in the remote prefix selects the route while the relative local filenames remain ordinary valid Unicode.
The proxy refuses each selected GET exactly once; the successful file still lands.
For engine execution, an acknowledged GET response barrier holds the actual child after complete preflight while the fixture creates selected local target collisions.
After release, real no-replace placement refuses those targets and preserves their injected bytes, while the distinct successful file lands.

Both routes and providers report 201 or 401 failed names as 200.
All jobs remain record version zero with no retained item identities and unavailable item history marked `not_captured`.
The 0/1-name controls retain their correct existing counters, but also have no item ledger.
The cause remains the 200-entry sample in [Download](../../internal/transfer/local/local.go), whose length supplies the failed total in [local completion](../../internal/app/localtransferservice.go).
One sampled example cannot identify every failed path or supply an exact total.

Every source and successful or refused local target has independently checked full content.
The identity matrix preserves selected and unselected files, another job's local and remote staging data, both owned accounts, and shared DLO/SLO aliases and their backing bytes.
Public/durable jobs, audit and recovery state agree; backend rerun creates no new job or request.
Orderly child reopening preserves the recorded state without HTTP or cleanup.

## Captured root redirected by lexical cleaning

The captured path `lexical/link/../selected` resolves through `link` to `actual/nested`, so the OS selects `actual/selected`.
The fixture proves that selection by reading its distinct marker through the unchanged captured spelling before transfer.
Preflight accepts the captured path, but the executor joins each target with `filepath.Join`, producing `lexical/selected/proof.bin`.
Both engine and native routes write the complete 21-byte file into that other directory and report a complete job.
The originally selected directory has no downloaded file.
All four canonical controls write into `actual/selected` and pass.

Complete local snapshots preserve both directory markers and the symlink, and permit only the independently observed new file in the wrongly selected directory for the before-fix run.
The separate failed assertion still rejects that redirection; the permitted observed delta is not approval of it.
Both owned remote namespaces remain unchanged, public and durable jobs agree, and orderly reopening performs no remote request.
This proves a backend-captured spelling mismatch; it does not prove that a native picker emits the spelling or that a clean spelling binds a stable directory identity.

## Native close error hidden after placement

The proxy streams the real upstream GET without a fixed response length, forcing the native reader's length lookup through HEAD.
The fault changes that HEAD length from 21 to 22 while leaving all 21 source bytes intact.
An independent read through the same native reader succeeds, then its actual `Close` returns `ObjectCorrupted`.
The download helper defers and ignores that close result after placing the file.
On both providers it reports `complete`, one copied and verified object, 21 copied bytes and no error; the terminal audit reports `applied`.
The matching ordinary-length controls pass.

The probe checks complete local bytes and collateral, unchanged owned remote namespaces, public/durable equality, refused rerun and orderly reopening without HTTP.
The fault is an injected provider-response disagreement, not a synthetic Go close method or a claim that SAIO spontaneously corrupts responses.
The next implementation must decide closure before irreversible placement when possible and retain every error that can arrive after a witnessed effect.

## Placed bytes lost from the report after unlink denial

This smaller fixture calls `local.Download` with its existing filesystem test engine and mock Swift.
After that engine writes the selected staging file, the wrapper removes write permission from its owned staging directory.
The executor's actual `os.Link` succeeds into the writable destination, then `os.Remove(staged)` fails with permission denied.
An independent same-file check proves the final file and retained staged file are the actual linked object, and an independent unlink attempt confirms the permission refusal.
The report nevertheless contains zero copied objects, zero copied bytes and no post-commit issues, despite the complete 21-byte destination file.
It retains one `place` failure and silently ignores the later recursive cleanup error.
The normal-permission control reports the successful copy and removes staging.

Complete local snapshots include file contents, types, permissions and the retained stage; complete remote snapshots remain unchanged.
The test restores only its owned directory permissions during cleanup.
This is unprivileged POSIX executor evidence, not an actual application job, real rclone child, SAIO placement failure or Windows proof.

## Recursive cleanup deletes unrelated writer data

An acknowledged GET response barrier pauses the real download child after the application has created its staging directory.
The fixture then creates and independently reads an unrelated `other-writer-keep` file inside that directory.
Only then does it release the response and allow the selected download to finish.
On both providers the unrelated file disappears, yet the job reports complete with one copied file and a successful audit.
The matching controls add no unrelated file and pass.
Both owned remote namespaces, unselected local paths and the final selected file retain their expected bytes.

The executor's deferred `os.RemoveAll(stageDir)` recursively deletes every child, including names outside the admitted download selection.
A private isolation overlay omits only that deferred call and repeats the actual-service cases with the exact retained file and directory included in the expected local delta.
Both providers then retain the unrelated bytes while still completing the selected download, with no remote writes or requests during orderly reopening.
This isolates the recursive cleanup as the cause; generating a stage name and creating a directory did not establish exclusive ownership of every later child.
The first isolation run retained the marker but failed its local comparison because the expected delta omitted the retained parent directory.
The corrected comparison explicitly requires that directory too; the failed first run is preserved separately.
Omitting cleanup is diagnostic evidence, not a reviewed implementation or permission to report uncertain residue as fully clean.

## Replay and evidence limits

Original commands, exit statuses, complete Go JSON streams, source/overlay hashes and parsed observations are retained outside the repository.
Validation joins split output chunks before interpreting the large JSON observations and checks the exact expected failures and passing controls.
The archived probes remain byte-identical to the executed files.
The combined archive replay on unchanged `99a2a479` reproduces all 17 expected failing leaves and 17 passing controls, with nine failing parents and two failing packages.
It has no skips or unrelated diagnostics; the system-test package takes 133.847 seconds and the local-executor package takes 0.025 seconds under the race detector.
All 311 Go/SQL source hashes and all five archived probe hashes match their recorded inputs.
These deliberate before-fix failures add no scenario credit.

For a disposable replay, use protected master `2b68d28e000e676537d131b00610cffe0b60205a`, whose entire tree is identical to source `99a2a479`.
From the checkout containing this archive:

```sh
git worktree add --detach /tmp/aeronautes-download-before 2b68d28e000e676537d131b00610cffe0b60205a
python3 - <<'PY'
import json
from pathlib import Path
root = Path('/tmp/aeronautes-download-before')
archive = Path('docs/experiments').resolve()
manifest = json.loads((archive / '2026-09-09-download-probes.json').read_text())
replace = {
    str(root / probe['package'] / ('download_' + probe['case'] + '_probe_test.go')):
        str(archive / probe['path'])
    for probe in manifest['probes']
}
Path('/tmp/aeronautes-download-before-overlay.json').write_text(json.dumps({'Replace': replace}))
PY
GOTOOLCHAIN=go1.27.1 go -C /tmp/aeronautes-download-before test -json -race -count=1 -timeout=15m \
  -overlay /tmp/aeronautes-download-before-overlay.json ./internal/systemtest ./internal/transfer/local \
  -run '^TestDownload(IdentityBefore(Mock|RealSwift)|RootBefore(Mock|RealSwift)|CloseBefore(Mock|RealSwift)|PlacementBefore|CleanupBefore(Mock|RealSwift))$'
```

Configure only owned SAIO fixtures through `AERONAUTES_SAIO_AUTH_URL` with `AERONAUTES_REQUIRE_TWO_ACCOUNTS=1`.
Expose the verified pinned rclone through PATH, `AERONAUTES_OFFICIAL_RCLONE_DIR` and `AERONAUTES_MATRIX_RCLONE`.
Provider skips or a missing engine invalidate the complete evidence above.
The actual files and symlinks ran on Linux; the permission fixture explicitly requires an unprivileged POSIX process.
These fixtures do not establish native approval, stable root/inode ownership, physical durability, full-app restart or child-kill semantics.
The identity matrix's shared DLO/SLO collateral proof is broader than the other four fixtures' owned plain-object namespaces; do not imply every probe exercises shared manifests.

## Next contract

Review the archived evidence and a concrete download-specific contract before changing the caller or enabling record version one.
First reject captured root spellings that cannot be used unchanged, prevent recursive deletion of unrelated staging children, handle stream closure before placement where possible and preserve witnessed copied bytes beside later cleanup errors.
Maintain actual service regressions for each bounded correction, with explicit permitted local/remote deltas and failure controls.
Before adding those maintained cases, address the measured integration scheduling constraint from the [upload delivery](2026-09-08-checked-upload-records.md#protected-delivery).
Its master system package uses 1,074.871 of the available 1,200 seconds; project further growth conservatively using the observed hosted/local ratio of up to 1.38.
Keep every existing test and required check, verify complete execution and coverage after any scheduling change, and treat any budget change as a separate measured correction.
Then define checked admission, final execution/verification observations, directory-marker accounting, sticky recording failures and item-derived terminal totals for downloads.
Native temporary files, engine stage paths and cleanup issues affecting a whole stage need explicit treatment in that contract; reusing the upload adapter without those decisions is insufficient.
Retained local paths must remain display/history data and must not become picker or rerun authority.

The five boundaries extend the existing P13/P15/P18/P19/P20/P28 evidence; no complete audit package or release gate is closed.
Mirror starts, changed Copy overwrites and automatic recovery reclamation remain refused.
The [valuable-data release hold](../audits/2026-09-05/remediation-plan.md#acceptance-gates-and-completion) remains in force.
