# Bookmark outcomes and list observations

This is the bounded UI-10 bookmark observation contract following the reviewed audit-query correction.
It does not close UI-10, P28, UI-09 or the valuable-data hold.
The oracle is UI-10's honest action/read status, DESIGN §§11/12/55, K-UI-005, K-SEC-002, K-SEC-006 and the existing bookmark/connection lifecycle tests.
`BookmarkService.Save` returns its acknowledged identity after publication; the following `BookmarkService.List` is a separate observation and cannot reverse that acknowledged result.
A Save rejection can include an uncertain effect and must remain a rejection without an invented identity.

## Before evidence

The unchanged source is `4f9e356da7b8ae118bfa1bdbfc2c04623c434af4`.
BrowserState awaits Save, then awaits List before returning the saved DTO; List propagates errors without recording read status.
After a successful new Save and failed List, BookmarkEditor remains an Add form and admits another empty-ID create.
The startup catch discards List failure, leaving the rail indistinguishable from an empty list.
List has no reply ownership: a late startup result can erase an acknowledged Save, and an older post-save result can restore a successfully deleted bookmark.

The [exact ten-case probe](probes/2026-09-12-bookmark-observations.test.ts.txt) ran against an isolated archive of that unchanged source.
Its [producer](probes/2026-09-12-bookmark-observations.producer.py.txt) and [evidence twin](probes/2026-09-12-bookmark-observations.before.json) preserve the command, all 102 original frontend input hashes, complete raw outputs, source archive and actual exit.
The packet is `/tmp/aeronautes-bookmark-observation-before-hr_yd7nf`.
Actual exit was 1 after 2.316 seconds: six intended assertion failures and four healthy controls, with no skipped case, timeout, setup failure or unhandled rejection.
The unchanged probe SHA256 is `f13b6d85a4353506909ef7c2e451303c88bd2080bbff77380279ff03fa3701e0`; the original RESULT.json SHA256 is `1d099db54bc05572386e1bd117578e6c9a4efe183fef77e22f93e5332a42b1b1`.

| Probe | Actual unchanged result |
| --- | --- |
| New Save succeeds, List fails | Stored `bm-1`; wrapper returned the List error instead of its acknowledged DTO. |
| Existing Save succeeds, List fails | Updated `bm-1`; wrapper returned the List error instead of its acknowledged DTO. |
| Mounted Add, Save, failed List, Save again | Save IDs were `["", ""]`; the fake store contained both `bm-1` and `bm-2`. |
| Mounted startup List fails | Rail contained only Add, with no unavailable alert. |
| Mounted startup List finishes after Add/Save | Created λ appeared, then disappeared while the fake store retained `bm-1`. |
| Older post-save List finishes after Delete | State restored `bm-2`, although the fake store retained only `bm-1`. |
| Healthy create control | Passed: one acknowledged ID returned and listed. |
| Existing held await control | Passed: Save stayed pending until its following List settled. |
| Actual Save rejection control | Passed: exact error preserved, no List call. |
| Uncertain Save control | Passed: fake store had an effect but Save rejected; no identity invented or List called. |

These are production BrowserState and mounted Svelte tests using the repository's per-test FakeApi, not a native keychain or persistent Go store.
Three failures mount production components; the Delete/read interleaving is exercised through BrowserState methods and is not claimed as a mounted native journey.
Its enabled UI route permits closing a Save awaiting List through the existing backdrop/Escape path, then deleting a different bookmark under the existing per-bookmark authority.

## Observation and mutation contract

Give bookmark List independent loaded, loading, error and stale state.
An initial or retired observation is not proof of an empty list.
Only a successful current List can establish successful empty or nonempty observations; retain previous rows only with a stale or previously-loaded qualifier while refreshing, unavailable or retired by a mutation.
Use fixed safe unavailability text and escaped Svelte text, never raw bridge/database errors.
Show status/alert semantics in the rail and a narrowly labelled read-only List retry; disable duplicate user retries while the current read is pending.
Retry calls List only, with no Save, Delete, Connect, automatic retry loop or guessed row upsert.
Keep Add available during startup loading and preserve all existing bookmark connect/edit actions.

Each List request captures a dedicated generation before awaiting the API.
Only the current generation may publish success, failure or loading completion; newer reads supersede earlier reads even when initiated by a mutation's existing post-save refresh.
Mutation admission retires observations that predate the possible effect, and effect settlement retires observations begun while a different mutation was pending.
This includes acknowledged and rejected/uncertain mutation outcomes, since an error does not establish absence of an effect.
A retired request cannot clear a newer pending read, overwrite a newer error or republish deleted data.
Retirement must also end the retired loading state without leaving an unfinishable spinner.
The Save's own existing awaited List remains eligible after its acknowledged effect unless a newer read or mutation retires it.
Do not retire that successful observation again merely because the wrapper finally releases its per-bookmark lifecycle.

Preserve the existing Save and Delete arguments, per-bookmark change map, connection/selection revisions, scope gates and connection slot.
Preserve every existing mutation await and effect order, including awaiting the post-save List before returning the exact acknowledged DTO and releasing per-bookmark authority.
List failure records read unavailability and fulfils, so the acknowledged Save return reaches the unchanged editor.
Actual or uncertain Save failure still rejects with the original error; do not manufacture success or retry a mutation automatically.
Successful Delete still filters only its acknowledged ID; no older List may restore it.
Keep credential-status invalidation and its detached finally refresh after every success, failure and uncertain result.

Save-only retains the editor's existing successful close behaviour.
Save & connect uses the acknowledged ID and the existing current-form and connection-scope gates after the existing List await, even when that observation fails.
Closing or replacing the form, or changing connection scope, must keep preventing obsolete follow-up Connect work.
BookmarkEditor remains byte-identical; any need to change it returns for separately allocated scope.

## Implementation and validation sequence

1. Review this concrete contract and preserved unchanged-source proof before production changes.
2. Correct only bookmark-list fields/loadBookmarks and observation retirement at existing Save/Delete boundaries in BrowserState, status/read-only retry in BookmarkRail and the obsolete startup List catch in BrowserScreen.
   Add dedicated `bookmarkStatus.test.ts` tests; preserve every existing assertion and all audit, settings, editor, connection, backend and DTO code.
3. Prove known new/existing Save identity through List failure, held await, true/uncertain Save rejections, no repeated empty-ID creation and Save & connect with current/closed/replaced/changed-scope forms.
   Exercise initial loading, successful empty, initial failure, stale rows/zero, explicit read-only retry, old success/error/finally and List requests admitted both before and during another pending mutation.
   Cover startup/Save, Delete/read and overlapping independent bookmark mutations without changing their authority.
4. Replay the identical ten-case original probe on final source and run independent controls removing nonthrowing observations, request ownership, guarded catch/finally and mutation admission/settlement retirement.
   Each control must fail its named unchanged assertion with actual exits, source hashes and unchanged deadlines recorded.
   Run focused new and existing connection, credential, keyboard, container-mode and transfer-identity tests, Svelte check and build, then obtain independent implementation review.
5. Renew current-base verification and range review, require all five current-head/current-base PR checks, protected merge and all five resulting-master checks before recording delivery.

The existing startup info/List sequence is preserved.
No list sorting, deduplication, storage reconciliation, additional backend calls, polling, native-platform acceptance or broader publication guarantee is introduced.
Settings, raw mutation-error sanitisation, unresolved backend publication, UI-09 and all other release gates remain separate.
Stop for ambiguous unacknowledged Save outcomes, mutation timing/authority changes, conflicting ownership, source/evidence drift or any need to weaken tests or budgets.
This contract commit claims no production correction or after result.
