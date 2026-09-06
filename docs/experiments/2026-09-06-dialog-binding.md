# Mutation dialog binding

This records P07's second implementation under §65.11.
The oracle is that an editor, confirmation or marker action owns only its captured target and lifetime.
An already admitted mutation may complete and refresh the existing audit trail, but its callbacks and subsequent navigation cannot disturb a newer view or dialog.
The existing C-04 contract still uses fetched detail identity independently of a subsequently changed raw row index.

## Implementation boundary

Object and container editors capture their initial detail, selection identity and UI scope.
Admission also requires the same current detail reference, so a refresh cannot pair an old working copy with a new fingerprint, including after a conflict and forced retry.
Working fields are disabled while saving or when the baseline is obsolete.
Close and unmount retire callbacks immediately; result, error and finalisation callbacks also require their original scope and selection.
The result check permits an owned successful save to close its editor after BrowserState publishes that save's updated portrait.
Filtering that merely reanchors the current row preserves the editor; a new selection, including selecting the same name again, ends it.

Manifest confirmation captures bookmark, container, name, detail and segment-location text together.
Its ownership request and forced deletion forward that captured identity through the existing DTO, and stale ownership replies cannot arm confirmation.
Incorrect confirmation, inconsistent initial identity, duplicate events and submission after closure or scope change are refused.
Container creation and marker actions have equivalent lifetime and duplicate-submission guards.
Marker deletion also requires a current loaded prefix before calling the bridge, including after a failed same-bookmark reconnect.

Creation and deletion keep their original scope and selection for list refreshes and navigation, and recheck after awaited follow-ups.
Dialog callers also pass a lifetime check: resource identity alone cannot distinguish a closed dialog from its replacement in the same view.
This prevents old creation or deletion from closing the replacement indirectly through navigation.
An unmounted Inspector likewise cannot initiate a marker-deletion refresh for its replacement.
Initial container reads use scope and request ordering; initial object-list replies, errors and loading callbacks also require their original scope.
These are UI publication guards, not cancellation of an admitted backend operation.

A mutation that retires an unfinished SLO/DLO preview now leaves an explicit unavailable message and a reload action.
The inspector renders a segment table only when it has a result, and distinguishes a pending read from a retired one.
Segment deletion still returns its original result; a follow-up refresh requires `manifestDeleted` for the displayed target.
This does not repair the separate partial-outcome presentation or establish ownership of any segment.

## UI scenario matrix

The maintained harnesses are `frontend/src/features/inspector/dialogBinding.test.ts` and `mutationDialogs.test.ts`.
They exercise production components and BrowserState with fixture-derived DTOs, a recording Api boundary and deferred replies.
Bounded microtask settling drives component updates without wall-clock sleeps.
These UI-to-bridge scenarios satisfy §65.10 rule 4's separate UI matrix and earn no Go service-scenario credit.

| Scenario | Maintained test or group |
| --- | --- |
| Object/container A saves after selecting and editing B | `an obsolete ... save cannot close a newer editor` |
| Reload-theirs, reopen before refresh returns, then attempt old-form/new-fingerprint save | `a refreshed container portrait cannot silently replace the editor baseline` |
| Current success, conflict, mismatch, failure and disabled working fields | `... edit lifetime` |
| Duplicate Save and force; changed baseline refuses force | `admits one Save` and `refuses a forced retry` |
| Closed, unmounted or scope-invalidated editors ignore late results/errors/finalisation | `close immediately retires`, `unmount retires` and `a scope change retires` |
| Exact manifest ownership/force DTO and typed confirmation | `manifest dialog intent` |
| Ownership/submission after bookmark, container, prefix, selection or change-and-return | `retires ownership and submission` |
| Current create success/failure, duplicate submit and frozen inputs | `container creation lifetime` |
| Marker duplicate submit, old result/error during newer action and reconnect admission | `marker action ownership` |
| Current segment-read retirement, unavailable display and explicit reload | `... distinguishes unavailable segment details` |
| Mutation result/audit/list wait followed by changed scope or selection | `mutation follow-up ownership` |
| Close/reopen or unmount/replacement in exactly the same view | `a closed ...` and `an unmounted inspector's marker deletion` |
| Different explicit delete target cannot refresh this view; failed manifest deletion does not refresh | `a ... operation for a different explicit target` and `segment deletion refreshes` |
| Obsolete initial container/object rows, errors and loading callbacks | `initial list reply ownership` |
| Filtering preserves the selected edit; selecting the same name again ends it | `filter reanchoring preserves an editor` |

## Evidence

With Node 22.23.2 and the unchanged locked dependencies, all **480 tests in 16 frontend files pass**, with zero failures or skips.
Whole-source statement coverage is **64.93%** (2,404 of 3,702 statements); this is not an audit completion percentage.
Frontend checking reports zero errors and warnings, the production build completes with 205 modules, and `go build ./...` passes with the final assets.
No Go implementation, generated bridge model, dependency or workflow changed, so this slice adds no local Go runtime or service-scenario claim.
The normal five PR and subsequent master checks remain required.

The identical **101 new cases** on parent `ad579e400a8b6b61` production produce **89 assertion failures and 12 passes**, zero skips or setup failures.
Passing-before cases include existing current failure handling and preserved explicit-target behavior; some changed-contract cases fail on the old lack of disabled working fields.
The initial three maintained editor reproductions separately failed before the editor changes and passed afterwards.
A later three-case reproduction against the intermediate implementation exposed the indirect close/reopen navigation defect: three intended assertion failures, zero passes and 54 unselected cases.
The final suite covers those three cases and Inspector replacement, and all pass.
An early guard-removal control exposed a fixture that supplied a raw detail object instead of BrowserState's current reactive object; correcting that fixture made the intended identity guard independently load-bearing.
Only the final complete control run below is credited.

The scratch runner restores parent production or breaks one named safeguard at a time.
Every variant must load all 101 cases, fail assertions, skip none and produce no import, transform or unhandled-promise error.
All 28 deliberately broken variants are caught:

| Removed safeguard | Failing cases |
| --- | ---: |
| Object editor lifetime | 5 |
| Container editor lifetime | 5 |
| Object baseline | 1 |
| Container baseline | 2 |
| Editor duplicate submission | 2 |
| Editor result ownership | 10 |
| Editor error ownership | 4 |
| Editor finalisation ownership | 8 |
| Manifest dialog lifetime | 6 |
| Manifest scope | 3 |
| Manifest ownership result | 7 |
| Manifest submission | 11 |
| Manifest initial identity | 1 |
| Create dialog lifetime | 3 |
| Create scope | 7 |
| Create submission | 7 |
| Marker duplicate submission | 1 |
| Marker result/error/finalisation ownership | 2 |
| Marker deletion admission | 1 |
| Follow-up scope | 4 |
| Follow-up selection | 4 |
| Follow-up recheck after container list | 1 |
| Follow-up component lifetime | 4 |
| Initial container-list scope | 3 |
| Initial container-list generation | 2 |
| Initial object-list scope | 2 |
| Explicit deletion target | 2 |
| Manifest-deleted follow-up condition | 1 |

Local artifacts are under `/tmp/aeronautes-remediation-20260905`: `p07-dialog-final.*`, `p07-dialog-controls.py`, complete `p07-dialog-controls-v3/results.json` with per-variant logs and `p07-dialog-reopen-before.*`.
Earlier editor subsets and intermediate control runs remain separate from this final denominator.

## Remaining work

P07's complete connection reset, active-bookmark edit/delete invalidation, serial Connect admission and reset-crossing Find/pages remain the third slice.
Ordinary paging deduplication/errors and the wider Find lifecycle remain P08.
P10 native authority/cleanup, P11 backend capabilities/replay/expiry, P13 partial outcomes, P14 external writers and P15 ownership remain open.
An unavailable preview and exact captured UI identity do not establish remote mutation correctness, segment ownership, native accessibility or platform readiness.
The valuable-data release hold remains.
