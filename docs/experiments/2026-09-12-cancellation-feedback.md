# Cancellation request feedback

## Before evidence

Unchanged master `3c5f7f7fe61bedc8199c72e8439400181643058a` contains two reproducible cancellation presentation defects.
`JobsDrawer.svelte` catches a rejected `browser.cancelJob` call and reloads the job list without recording that action's failure.
A successful reload leaves the running row and no cancellation alert.
Two clicks before the request resolves call the API twice for the same job because the action has no pending guard.

The [preserved probe](probes/2026-09-12-cancellation-feedback.test.ts.txt) mounts the actual Svelte drawer with the actual BrowserState and a held fake API.
Its [evidence summary](probes/2026-09-12-cancellation-feedback.before.json) records the exact source, dependency lock, command exit and source hashes.
The command executed eight tests, exited 1 after 2.290556 seconds, and had no timeout or skipped test.
Five assertions failed as intended: the two cancellation cases and three audit query/paging races.
Two controls passed: an exact-job cancellation followed by the provider's terminal state, and a successful empty audit query.
The eighth passing diagnostic observes the existing bad audit state: Recent swallows a read error while Search rejects, with no distinct unavailable state.
It is not successful error handling.
All 70 frontend source files matched the archived master before and after execution.
One warning about the private configuration's extensionless Vite import remains in the raw stderr; none of the five failures is a setup failure.

The audit probes separately show a late filter response replacing the selected filter's result, overlapping More calls appending the same page twice, and a late Recent reply replacing a filtered result.
Those probes exercise BrowserState; the audit error-as-empty rendering was source inspected and is not a native UI observation.
No production change, Wails bridge, Swift fixture, native accessibility run or after result is included in this before evidence.

## Oracle and cancellation contract

UI-10 and P28 require operational errors and unavailable data to remain distinguishable from accepted actions and empty results.
DESIGN §§26.4/47/49/50/55, K-UI-005 and K-SEC-002/006 constrain this presentation correction.
`TransferService.CancelJob` requests graceful cancellation and can refuse a job that is no longer running in this process.
Its successful reply does not establish a cancelled terminal outcome, removal of retained data, cleanup success or child quiescence.
`BrowserState.cancelJob` also awaits its existing job-list refresh, which records read errors in `jobsError` rather than rejecting.
The component therefore cannot identify the instant of bridge acknowledgement separately from that refresh.

| Request state | Presentation | Permitted next action |
| --- | --- | --- |
| Pending | “Requesting cancellation; waiting for the request and status refresh.” | Read-only status refresh; no second cancellation or dismissal for this job while the request remains pending. |
| Requested | “Cancellation requested. The job's reported state determines its outcome.” | Read-only status refresh or explicit notice dismissal; do not infer or assign a terminal state. |
| Unconfirmed | “Cancellation could not be confirmed. The job may still be running. Refresh status before retrying.” | Read-only status refresh, explicit retry for the same currently visible active job, or explicit notice dismissal. |

Snapshot the job ID before dispatch and retain it in every notice and retry.
Never attribute a reply to the selected account, a replacement DTO or whichever row happens to occupy the old position.
Use escaped text and fixed cancellation wording, without rendering raw exceptions or adding resource names or credential details to the notice.
Retry is a new operator action, never an automatic consequence of a failed request or a status refresh.
Only offer it for an unconfirmed notice whose exact job ID is present as active in the current successful job-list view; when the row is absent or the list is unavailable, direct the operator to refresh or select the relevant account.
This condition uses the available view and makes no new freshness or backend-state guarantee.
A rejected request retains the existing read-only job refresh, but its notice is stored independently and survives either success or failure of that read.

Keep the notices above the selected tab's contents so an account filter, tab change, empty view or jobs error cannot remove them.
The collapsed drawer reports that cancellation notices exist, and reopening reveals the same attributed messages.
A requested or unconfirmed notice remains until the operator explicitly dismisses it or the component is disposed, even if a later poll observes a terminal job.
Dismissal only hides the notice; its label and nearby explanation must make clear that it does not stop a job or establish an outcome.
Pending notices cannot be dismissed, because forgetting their admission guard could permit a duplicate request.

Retain at most 50 notices in this component, matching the current job-list page bound.
At capacity, disable cancellation for a job without a retained notice and display that the operator must wait for a pending reply or dismiss a settled notice to make room.
Never silently evict pending or unconfirmed feedback to admit another action.
An existing unconfirmed notice can be retried within its own slot; another job remains independently cancellable while capacity remains.
This bounded presentation limit changes no backend concurrency budget, job lifetime or cancellation semantics.
On disposal, suppress late component updates and any rejection-triggered refresh initiated by this component; the existing BrowserState operation may still complete its own read.

## Commit and verification sequence

1. Commit this contract, its inert before probe and evidence summary, and the bounded DESIGN §65.11 sequence; obtain independent review before production changes.
2. Add dedicated `cancellationStatus.test.ts` component regressions and run them against the unchanged drawer before implementing only the cancellation presentation in `JobsDrawer.svelte`.
   Cover duplicate clicks, acknowledgement without a terminal result, a terminal outcome other than cancelled, rejection plus successful and failed reads, explicit retry, independent job IDs, literal attribution, filtered or missing rows, tab and collapse persistence, capacity and dismissal, and disposal while pending.
3. Preserve the original cancellation assertions and passing control, run the maintained tests against the corrected drawer, and independently remove the pending, feedback-retention and attribution guards in private copies to confirm their tests fail for the intended reason.
   Preserve existing drawer, retained-record, recovery and rerun tests; run Svelte type checking and the production build without changing their gates.
4. Obtain independent implementation approval, then integrate onto the actual current master with renewed verification and review before the existing five-job PR and resulting-master delivery gates.

This is a component-level system reproduction using the real drawer and BrowserState with a fake API, not native Wails or platform acceptance evidence.
No after result or UI-10 closure is claimed by this contract commit.

## Separate audit follow-up

After cancellation approval and explicit BrowserState ownership, review the next audit contract before implementation.
It must distinguish initial loading, successful empty data, unavailable reads and stale rows that retain their original query identity.
Capture immutable query/cursor ownership, ignore late successes and failures, and guard overlapping or non-advancing pages.
Separate background Recent refreshes from the selected paginated view.
Audit-read failures after successful mutations must remain read failures rather than changing the mutation outcome or encouraging a blind retry.
The maintained follow-up needs the three reproduced races, failed initial/retry reads, successful empty controls, stale-row attribution, filter changes during More, mounted retry/error presentation and post-mutation read-failure controls.
Settings, bookmarks, UI-09 features, native accessibility acceptance and the valuable-data hold remain open.

## Maintained correction and local evidence

The drawer now stores pending, requested and unconfirmed notices by the job ID admitted to the cancellation call.
The request guard acts before a render can disable the button, and a separate job remains cancellable while notice capacity remains.
A successful call updates only its request notice; the returned job state and counters remain unchanged.
A rejected call records fixed unconfirmed feedback before the existing read-only refresh, so either a successful or unavailable job view preserves that feedback.
Notice storage is independent of account filters, tabs, drawer collapse and row lifetime, with the reviewed explicit-dismissal and 50-notice admission rules.
The component suppresses its late completion updates and rejection-triggered reads after disposal.

The [source-bound evidence](probes/2026-09-12-cancellation-feedback.after.json) records the exact final frontend inputs, commands, raw output hashes and local results.
All 20 maintained cancellation cases fail against the unchanged drawer, then pass with the correction.
The original exact-job passing control and the two original failing cancellation probes also pass unchanged against the correction.
Their five audit peers were explicitly unselected in that focused replay and remain separate open work.
Earlier red runs are retained separately: the final tests strengthen the replacement-row fixture and make the missing-notice assertion explicit, without loosening any outcome, deadline or capacity assertion.

Six independent private controls remove pending admission, filter retention, immutable reply attribution, notice capacity, disposal protection or the distinction between acknowledgement and a terminal result.
They produce eight intended assertion failures: duplicate requests, lost feedback, a notice left pending under the wrong reply identity, capacity admission, an extra read after disposal, and two fabricated-cancelled outcomes.
Each control preserves the final maintained tests and changes only its named production guard; unselected tests are explicitly counted.
No control times out or fails during test setup.

The final focused run passes all 76 cancellation, drawer, retained-record and recovery tests without a skip.
Svelte checking reports zero errors and zero warnings, and the production build passes.
All final command inputs remain unchanged during execution and match the recorded source hashes.
These component checks provide no native Wails, platform, accessibility, Swift or full-repository acceptance evidence.
Independent implementation and current-base range review, the full applicable frontend gates and all five PR/master jobs are still required before delivery; UI-10 and the valuable-data hold remain open.
