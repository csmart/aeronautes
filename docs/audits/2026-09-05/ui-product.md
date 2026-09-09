# Aeronautes UI, user journey, and product audit

Audit date: 2026-09-05.
Scope: read-only audit of `<repo>` at `cc6465ac69ec00756d9976bbf6cf7531b07da66d`.
References below are relative to that checkout and describe that snapshot.
No product source or external user data was changed for this audit.

## Method and evidence

Loaded project `AGENTS.md`, `docs/REQUIREMENTS.md`, relevant `docs/DESIGN.md` sections, common coding-review guidance, and ECC `click-path-audit`, `product-lens`, and `frontend-a11y` skills.
The behavior oracle is DESIGN §§3.4, 4, 14, 16–19, 27–28, 46–55, 63, and the stated K-* requirements.
The product lens was applied as an operator journey and capability assessment, without speculative revenue or growth scoring.
The frontend-a11y skill's semantic HTML, focus, and labelling principles apply to Svelte; its React-specific implementation examples were not copied.

Inspected every production Svelte feature component, the central state, bridge API seam, existing frontend tests, relevant Go entry points, and stored browser/Copy/jobs screenshots.
Stored screenshots are historical 1280×800 captures, not evidence of a current live desktop run.
No native Wails accessibility, OS picker, platform keyboard, display scaling, screen-reader, or real transfer exercise was performed for this domain review.
The [verification record](verification.md) contains the full current frontend build/check/suite and system evidence.

Twelve deliberately expected-safe probes all failed for the predicted current behaviour, with no setup failures.
These are separate audit reproductions, not failures in the unchanged normal repository suite.
The [probe sources and portable setup](probes/README.md), [original result log](evidence/ui-probes.log), and [preservation rerun](evidence/preserved-ui.log) are retained.
The tests import audited production state/components directly and inject deterministic deferred API responses.

Production does not silently fall back to fixture data.
`BrowserScreen.svelte:20` always constructs `BrowserState(liveApi)`, and the only production imports of fixture data are inside the unreferenced test fake (`fakeApi.ts` explicitly labels itself test-only).
The live API calls generated Wails services directly, normalises nullable list results, and rejects missing required plan/preflight/status objects.
Bridge absence is inferred from the failed Info call (`BrowserScreen.svelte:29`–`:35`) and displayed as `service bridge unavailable` in the footer.
There is no retry/startup failure screen; the separate failed bookmark load is swallowed, while jobs failure is visible only inside the opened drawer.
A browser-only render with no service calls cannot be treated as proof of successful native application journeys.

## State and touchpoint map

There is one `BrowserState`, constructed in `BrowserScreen.svelte`, and local component state for dialog/edit intent.
This is a useful, small seam for deterministic UI-to-bridge tests.

| Action family | Reads and writes | Safety-relevant side effects |
| --- | --- | --- |
| `connect`, bookmark Save/Delete | Active bookmark, account, capabilities, containers, entries | Partial clearing; no global connection generation; Save does not reflect backend invalidation |
| `openContainer`, `goTo`, `up` | Container/prefix, listing, selection, detail | Initial listing has a sequence guard; leaving root and changing connection do not consistently invalidate all outstanding reads |
| `loadMoreContainers`, `loadMoreEntries` | Current scope and marker, then append to current arrays | No in-flight gate, scope check, response generation, or error presentation |
| `filter` | Loaded rows and selected index | Reanchors selection by name and clears detail if excluded; good existing protection |
| `select` | Current entry and container | Clears object/prefix/manifest detail and advances `#detailSeq`; normal selection read races are guarded |
| Metadata/container/marker mutation | Captures part of target at submission | Completed mutations write results into current detail without checking current identity |
| Copy/Mirror/Delete | Local inputs plus current BrowserState scope | A local boolean/preview gates Start; asynchronous previews can restore invalidated plans |
| Local upload/download | Opaque picker tokens plus current BrowserState scope | Preflight report gates Start; immutable request/plan token remains absent |
| Find | Container/prefix/query, scan ID, running flag | Stop-before-ID, overlapping completion, navigation, and alternate close paths are not fully coordinated |
| Jobs/audit | Filter and recent page | Poll/search responses may arrive out of order; errors inconsistently surfaced; counts are drawn from a filtered recent slice |

Reviewed touchpoints include bookmark add/edit/save/connect/delete/theme; container modes/create/open/more; prefix/breadcrumb/up/filter/find/results/more; inspector metadata/ACL/quota/versioning/marker/segments/delete; Copy/Mirror fields/preview/confirm/start; upload/download picker/preflight/start; jobs open/filter/budget/benchmark/cancel/rerun/details and audit filter/more; and About/open/close.

## Findings

### UI-01 — P1: a late preview can authorize a different destructive operation

Evidence: `frontend/src/features/transfer/TransferDialog.svelte:58`, `:68`, `:83`, `:265`; `frontend/src/features/transfer/DeleteDialog.svelte:36`, `:49`, `:68`, `:86`.
Trigger: start previewing prefix deletion, change the radio to Delete container while the request is pending, then receive the prefix preview and type the container name.
Actual: the late response sets `preview` and clears `busy`, enabling Delete container using counts for the former prefix.
Copy/Mirror has the same race after changing destination fields during a pending plan; Start reconstructs a request from new inputs.
Expected: a preview can arm only the exact resource, mode, settings, and connection identity it analysed.
Both races were reproduced by mounted-component tests.
This is concrete frontend evidence for already acknowledged W-01/H-14 plan-binding work; do not count it as a separate backend architecture discovery.
Oracle: DESIGN §§3.4, 28, 31, 65.10 and explicit descriptive/confirmation safety.
Fix: capture immutable intent plus a preview generation; invalidate generation and confirmation on every relevant change; ignore stale results; start using the captured request and a backend-issued one-shot plan capability.
Tests: deterministic late-response component cases plus UI-to-bridge tests that compare preview identity with execution identity; system tests for replay, expiry, changed destination, and increased scope.
Confidence: high.

### UI-02 — P1: mutation completion replaces another selection's authoritative detail

Evidence: `frontend/src/features/browser/browserState.svelte.ts:615`, `:621`, `:623`, `:636`, `:644`, `:655`, `:672`.
Trigger: save metadata on object A, select B before the save returns, then let A's successful response arrive.
Actual: `detailIdentity` remains B while `objectDetail` becomes A; the inspector combines B's row/title with A's classification/metadata and can seed a subsequent edit from the wrong object.
The initial mutation correctly captures A's container/name; the bug is the unchecked response application.
A deterministic probe produced `detailIdentity.name=external-dlo` and `objectDetail.name=empty-object`.
Container saves/refresh and marker creation use the same unchecked response pattern and need coverage.
Oracle: selection identity in DESIGN §§14.2, 17–18 and the existing C-04 contract.
Fix: capture bookmark/session generation plus exact resource identity and detail request generation; apply a returned detail only if all still match, otherwise record completion without replacing the current inspector.
Tests: save A/select B/resolve A, cross-container equal-name, reconnect, and overlapping refresh cases; assert later mutations use the correct complete working copy.
Confidence: high for object save reproduction; high static confidence for the analogous assignments.

### UI-03 — P1: load-more responses contaminate the newly opened namespace

Evidence: `frontend/src/features/browser/browserState.svelte.ts:329`–`:340`, compared with the guarded initial listing at `:303`–`:325`; container paging at `:214`–`:220`.
Trigger: load page 2 in container A, navigate to B, then receive A's page.
Actual: A's rows and pagination marker are appended into B's current list; selecting such a row asks the backend for that name in B.
A deferred-response probe reproduced the cross-container contamination.
Double-clicking Load more concurrently also appends the same page twice; a second probe reproduced duplicate rows.
Container rows are keyed by name, so duplicate container pages additionally risk a duplicate-key render failure; that render failure was not separately exercised.
Oracle: DESIGN §§14–16 and exact resource identity.
Fix: scope/generation checks and one outstanding page request per marker, with a disabled/pending Load more affordance and explicit paging errors; apply equivalent account-generation protection to container pages.
Tests: out-of-order navigation, reconnect, same-name containers across accounts, duplicate clicks, page failure/retry, and stale marker rejection.
Confidence: high.

### UI-04 — P1: destination prefix input is silently normalised

Evidence: `frontend/src/features/transfer/TransferDialog.svelte:43`–`:44`.
Trigger: enter a destination prefix containing intentional leading or trailing spaces.
Actual: `destPrefix.trim()` changes the bytes before preview and execution; an all-space prefix becomes the whole destination scope.
The component probe observed `"  object prefix  "` sent as `"object prefix"`.
This affects object identity and can widen a Mirror scope; it is not harmless cosmetic cleanup.
Oracle: K-TEST-001, DESIGN §14.3 and explicit destructive scope.
Fix: preserve resource-name bytes, show whitespace visibly in intent/confirmation, and validate rather than silently normalise any unsupported container name.
Tests: leading/trailing/all-space prefixes in Copy and Mirror through UI, plan, native fallback, and whole-account negative-space assertions.
Confidence: high for normalization; remote Mirror damage was not executed.

### UI-05 — P2: bookmark edits/deletion leave stale connected state

Evidence: `frontend/src/features/browser/browserState.svelte.ts:166`–`:179`; `frontend/src/features/browser/BrowserScreen.svelte:116`; `internal/app/bookmarkservice.go:80`–`:84`, `:106`–`:107`; `internal/app/connections.go:115`–`:125`.
Trigger: Save an edit to the active bookmark without Save & connect, or delete that bookmark.
Actual: the Go service invalidates its active session, but Save preserves all frontend connected state; Delete clears only ID/container arrays/name, leaving account/capabilities/entries/detail and connected footer content.
The deletion probe confirmed account detail remains after active deletion.
Subsequent operations can fail as disconnected while the interface still says connected, and pending old reads can restore more stale state.
Oracle: K-UI-005 and truthful app/connection state.
Fix: one disconnect/reset action used by edit invalidation, deletion, and reconnect; stop finds and invalidate all asynchronous scope generations; reconnect explicitly when desired.
Tests: Save-only, Save & connect failure, delete-active, edits to inactive bookmarks, and pending reads crossing invalidation.
Confidence: high.

### UI-06 — P2: Stop Find can leave a scan running and results can outlive their scope

Evidence: `frontend/src/features/browser/browserState.svelte.ts:516`–`:552`; `frontend/src/features/browser/WorkingList.svelte:84`, `:122`; `frontend/src/features/browser/FindPanel.svelte:13`–`:21`; `internal/app/findservice.go:85`–`:89`, `:152`–`:163`.
Trigger: Stop before `StartFind` returns its ID.
Actual: Stop sees no ID to cancel; the late ID is assigned after the flag is false and is never cancelled, leaving the detached backend scan running.
The probe observed no call to `cancelFind('late-find')`.
Closing by the main Find toggle also only hides the component, and navigating between containers neither cancels nor clears old result scope.
Clicking a surviving match navigates its name under the current container, not a captured original container, and never selects the actual found object.
Oracle: DESIGN §16's explicit cancellable scan and truthful results.
Fix: bind a scan to request generation and immutable bookmark/container/prefix, cancel any late-started superseded scan, centralise close/navigation cleanup, and navigate/select results by exact identity.
Tests: stop-before-start-return, poll-after-stop, start/stop/start interleavings, main toggle, navigation, and result selection beyond the first listing page.
Confidence: high for lost cancellation and static scope trace.

### UI-07 — P2: the Prefix field cannot perform a literal server prefix query

Evidence: `frontend/src/features/browser/WorkingList.svelte:33`–`:35`.
Trigger: type `external` to list `external-dlo` and `external-slo`.
Actual: the change handler sends `external/`; the named objects do not match.
A component probe confirmed the appended slash.
Expected: a field explicitly labelled server-side prefix passes the literal Swift prefix; folder navigation may separately append separators.
Oracle: DESIGN §16 Prefix and K-TEST-001.
Fix: separate literal prefix filtering from folder navigation, including clear breadcrumbs when a filter ends mid-component.
Test exact literal prefixes and real names containing spaces, slashes, repeated separators, and unicode.
Confidence: high.

### UI-08 — P2 required capability gap: there is no single plain-object delete journey

Evidence: `frontend/src/features/inspector/Inspector.svelte:126`, `:176`, `:224`; `frontend/src/features/browser/WorkingList.svelte:114`; `frontend/src/features/browser/browserState.test.ts:276`; `internal/app/mutateservice.go:299`.
The inspector only offers deletion inside DLO/SLO branches; the only general toolbar deletion is explicitly Delete scope.
The manifest-delete endpoint intentionally refuses a plain object.
A mounted plain-object inspector probe found no delete control.
Oracle: DESIGN §63 Delete item 1 and §48 single-object deletion.
This is a missing v1 capability, not an accusation that the intentionally restricted manifest method is broken.
Fix: add an exact-object typed intent/confirmation route with authoritative identity checks and refusal cases; never emulate it with a prefix delete because sibling names can share the prefix.
Tests: plain object, empty object, symlink, object changing to DLO/SLO, hostile names, concurrent replacement, and unaffected siblings.
Confidence: high.

### UI-09 — P2: jobs and audit omit essential operational diagnostics

Evidence: `frontend/src/features/jobs/JobsDrawer.svelte:239`, `:258`–`:295`, `:329`–`:336`; `frontend/src/features/browser/browserState.svelte.ts:600`.
Active jobs expose only Cancel, so engine/effective settings/verified diagnostics are expandable only after termination.
Rows do not visibly identify operation type/mode; every operation starts its counts with objectsCopied, leaving delete rows at `0/N obj` beside separate deletions.
The audit view discards available transaction IDs and has no bookmark filter because `searchAudit` always sends `bookmarkId: ""`.
The same-named paths in two accounts cannot be disambiguated in the audit display.
Oracle: DESIGN §§47, 49–50; the same bookmark filter in jobs and audit is an explicit owner decision in §47.
Fix: operation-specific counts, live expandable diagnostics, bookmark-scoped audit, transaction IDs and failure-detail access; render unavailable telemetry as unavailable.
Tests: running and partial copy/mirror/delete jobs, two accounts with identical path names, audit paging/filter changes, and transaction-ID visibility.
Confidence: high static evidence.

### UI-10 — P2: operational errors are swallowed or presented as empty state

Evidence: `frontend/src/features/jobs/JobsDrawer.svelte:47`–`:52`, `:90`–`:97`; `frontend/src/features/browser/BrowserScreen.svelte:37`–`:41`; `frontend/src/features/browser/browserState.svelte.ts:214`, `:329`, `:593`.
Cancel failure is discarded if reloading jobs succeeds; settings save, paging, and audit search errors have no visible handling; bookmark load failure is shown as an empty rail.
Expected: distinguish no data from unavailable data and show whether an attempted action was accepted, failed, or is unresolved.
Fix: per-operation structured status/error state and useful retry paths, preserving prior usable data while clearly marking staleness.
Tests: bridge rejection for each touchpoint, retry success, and normal empty-state controls.
Confidence: high static evidence; failure displays were not run against a real Wails bridge.

### UI-11 — P2: keyboard and dialog semantics are incomplete and interfere

Evidence: `frontend/src/features/browser/BrowserScreen.svelte:44`–`:85`; `frontend/src/features/inspector/ManifestDeleteDialog.svelte:82`–`:86`; `frontend/src/features/browser/BookmarkEditor.svelte:83`; `frontend/src/features/browser/CreateContainerDialog.svelte:34`; `frontend/src/lib/trapFocus.ts:8`.
The global arrow handler treats a native select as non-editing and redirects focus to the working list, interfering with jobs filters/budgets and container versioning selection.
Its modal guard omits the inspector-owned manifest delete dialog, so arrow keys can redirect focus behind that dialog and change selection while it is open.
Cmd+Up, Cmd/Ctrl+K, Cmd/Ctrl+I, and the safe Delete shortcut from DESIGN §4.4 are absent.
Bookmark/create overlays are forms without a dialog role; other dialogs have no `aria-modal` or inert background, and tabs lack roving focus/arrow navigation and associated panels.
Several important inputs lack connected labels: transfer destination account/container/prefix, Find query, ACL raw/add, audit filters, benchmark confirmation, and segment-force confirmation.
Positive foundation: reusable initial/return focus and Tab cycling, labelled icon controls in many places, semantic listbox/active-descendant support, visible focus token, and reduced-motion support exist.
Fix: central modal ownership, keyboard handlers respecting target/defaultPrevented, complete semantic labelling/dialog/tab patterns, and only then additional shortcuts.
Tests: component keyboard/focus tests plus native NVDA/VoiceOver/Orca smoke on relevant platforms; include selects, screenreader background isolation, close while busy, hidden details, and removed opener.
Confidence: high static evidence; native accessibility behavior remains unverified.

Current disposition: [PR72's bounded keyboard/modal correction](../../experiments/2026-09-09-keyboard-scope.md#protected-delivery) is delivered with independent review and all five PR/master checks.
Global browser keys now respect editable/select targets, consumed events and active modal ownership; the focus action accounts for visible controls, nested modals, inert backgrounds and opener availability.
The [separate asynchronous Preview correction](../../experiments/2026-09-09-preview-focus.md) addresses native focus loss during planning without authorising Start.
The [bounded sensitive-label correction](../../experiments/2026-09-09-sensitive-labels.md#protected-delivery) connects Find, ACL add/raw, audit-filter, benchmark-confirmation and segment-force labels to the appropriate current controls with per-instance IDs, preserving literal values, exact guards and explicit submission.
Transfer destination controls already have accessible names and remain covered by an unchanged positive control.
The historical findings above remain the before evidence; tab/shortcut patterns, native screen-reader/select journeys and broader UI-11/P29 acceptance remain open.

### UI-12 — P2: text contrast is insufficient for the dense control typography

Evidence: `frontend/src/design/tokens.css:17`–`:26`, `:35`, `:83`–`:90`; `frontend/src/design/typography.css:37`–`:42`; usages include inspector labels, fields, footers, and primary action labels.
Calculated contrast from the exact opaque sRGB tokens: faint text on pane/card is 2.98:1/3.12:1 in light mode and 3.67:1/3.43:1 in dark mode; light primary button text is 3.64:1 against the accent background.
These are normal small text (approximately 11–14px at the default root size), not large-text exceptions.
Historical screenshots also show low emphasis on important counts/labels, but calculations are source-token evidence rather than a live contrast-tool run.
Oracle: DESIGN §60 Phase 7 accessibility gate and the already tracked a11y/contrast program in §65.10; the chosen normal-text AA target is 4.5:1, with applicable exceptions defined by [W3C contrast guidance](https://www.w3.org/WAI/WCAG22/Understanding/contrast-minimum.html).
Fix: increase text/primary-action contrast while preserving the warm palette; use faint styling only where it remains legible.
Tests: automated color-pair checks plus real rendering in both themes, high contrast/forced colors, scaling, focus, disabled, error, and hover states.
Confidence: high for ratio calculations; whole-app accessibility compliance not assessed.

### UI-13 — P2 product gap: responsive and large-scale navigation remain unfinished

Evidence: `frontend/src/features/browser/BrowserScreen.svelte:154`–`:172`; `frontend/src/features/browser/WorkingList.svelte:358`, `:378`; `main.go:137`–`:140`.
The design calls for resizable panes and a narrow rail/list plus inspector slide-over; implementation only shrinks four fixed columns and has no resizing/collapse mechanism.
The native minimum width is 960px, so the below-920px breakpoint is normally unreachable at default scale; it does not demonstrate the promised laptop layout.
Working-list size/timestamp columns consume 18rem before name/icon/gaps and cannot be hidden; at the 19rem working-pane minimum this leaves very little room for object names.
Dialog bodies generally have no viewport max-height/scroll handling, a concern for the 600px minimum window with Mirror/advanced options or large font scaling.
Container rows are all rendered and Load more retains all loaded rows; virtualising the object DOM is useful but does not prove bounded memory for prolonged million-name exploration.
Fix: implement named breakpoints, resizable/collapsible panes, responsive columns, bounded dialogs, and an explicit large-namespace paging/window strategy.
Tests: 960×600 and larger windows, increased text/DPI scaling, long names/errors, Mirror expanded, 100k+ loaded rows, and measured memory/interaction latency.
Confidence: high for absent layout mechanisms; actual clipping/performance severity requires live layout profiling.

### UI-14 — P2: inherited JavaScript property names break metadata input/diff

Evidence: `frontend/src/lib/metadataDiff.ts:22`, `:29`, `:62`–`:74`.
A user-entered literal `__proto__` key is assigned into `{}` and disappears rather than becoming an own string property; the helper returns no duplicate/error.
The diff also tests `in`, so a new `constructor` key is misreported as changed instead of added.
Two isolated helper probes reproduce those outcomes.
Do not infer that existing Swift objects universally retain literal `__proto__` metadata: provider/WSGI header normalization can convert underscores, and that wire behavior was not probed here.
The demonstrated defect is accepting and silently dropping frontend input; an existing remote-key loss claim needs a real Swift round trip before elevation.
Oracle: accurate complete working copy and meaningful diff in DESIGN §17.
Fix: dictionary storage using null-prototype maps or a Map and own-property membership; explicitly validate unsupported provider keys instead of silently losing them.
Tests: `__proto__`, `constructor`, case variants, canonical provider header behavior, and clear validation messages.
Confidence: high frontend-only; remote existing-data impact unconfirmed.

## Capability and suitability assessment

The product has a clear audience: Swift/SwiftStack operators who need to inspect control-plane semantics and move bulk data between accounts while understanding DLO/SLO consequences.
That focus is coherent and more valuable than adding unrelated protocol compatibility or a generic file-manager appearance.
The current shell provides a credible working foundation, but its interaction identity races and missing operation coverage prevent treating it as a finished operator workbench for valuable data.
The broader existing W-* architecture blockers remain material even when individual UI paths work.

| Area | Useful current behavior | Completion or explicit boundary |
| --- | --- | --- |
| Onboarding/auth | Multi-bookmark TempAuth v1; write-only API key input; keychain/session warning; custom CA and persistent insecure flag | Save-only invalidation/reconnect UX; visible bookmark-load failure; stale HasCredential is known deferred; Keystone is an explicit non-goal |
| Browser | Paged API; virtual object rows; literal object names; native prefix/object distinction; local filter labelled by placeholder | Paging concurrency and literal prefix defects; no refresh shortcut; no arbitrary field/multi-select/sort capability promised here |
| Inspector | Authoritative selection HEAD, metadata/raw headers, DLO/SLO distinction, marker-versus-implied teaching, ownership caution | Account raw headers and account metadata editing from §§53/63 are absent; SLO ETags/raw fields need fidelity; symlink and modern-versioning edits are intentionally restricted |
| Mutation | Full metadata working copy, removal chips/warning, duplicate case refusal, conflict/reload/force, container ACL public acknowledgement | Late results cross identities; no full before/after ACL/versioning/quota/content-type/delete-at diff; CORS/staticweb configuration UI absent |
| Transfers | Explicit Copy/Mirror, new/changed/unknown/overwrite/destination-only counts, data path, DLO/versioning warning, typed Mirror | Plan binding; no source bookmark label in dialog; settings narrow; no single-object selected transfer journey; directory upload is already deferred |
| Local IO | Native picker token seam, complete collision-report gate, no collision overwrite convenience | Directory upload absent by current picker; per-object download absent; six-platform picker/materialization proof still required |
| Destruction | Prefix/empty/delete-container preview and typed consent; manifest-only default; force warning for segments | Plain-object action missing; immutable session/plan binding; whole-account system proof and partial outcome access |
| Jobs/performance | Durable jobs, account filter, visible counters/rates, cancel/rerun copy, active-job ceiling, benchmark consent | Live diagnostics/operation-specific counts; settings page and bookmark resource budgets from §§38–45 not exposed; default profile and retention have API fields but no UI |
| Audit/support | Recent change rows, operation/result filters, Object HEAD transaction ID | Account filter, transaction IDs, failure detail/export, errors; audit persistence architecture remains a backend concern |
| Privacy/distribution | Local system fonts, local theme preference, no apparent external frontend assets, About licences/version | Live native-platform accessibility, scaling and release evidence are outside this UI evidence |

Features such as S3, mount/FUSE, scheduling, full version history, TempURL generation, clone-container, SLO upload, and arbitrary rclone arguments are documented non-goals or future work.
They should not be inflated into current defects.
Directory upload, modern-versioning editing, adaptive concurrency, plan capabilities, audit outbox/export, and the a11y/responsive program are already acknowledged deferred work; retain that history in the integrated plan.

Current local selection behavior is narrower than a general desktop file manager.
`main.go:167` opens a multi-file chooser for upload; `main.go:175` opens a directory-only chooser for download.
`internal/app/localtransferservice.go:79`–`:91` returns upload basenames and an opaque token, and `:110`–`:120` removes upload absolute paths from the preflight DTO.
Download uses the current entire container/prefix plus a destination-directory token (`:35`–`:44`), regardless of the object row selected in the list.
Its `PickedDirDTO.Dir` is currently the selected directory string (`:99`–`:103`), displayed by the UI, so do not claim that no local path of any kind crosses the bridge; upload path secrecy and download display-path behavior differ.
`LocalTransferDialog.svelte:51` stores the chosen upload names but never renders them; a successful report only shows the item count and total bytes (`:123`–`:127`), while rejected/warning paths are shown individually.
Cancellation leaves no runnable report; tokens may remain locally, but Start is gated on a new accepted report, a useful existing protection.
For better reviewable intent, show bounded selected-name summaries, explicit bookmark identity, exact current scope, and immutable preflight/start binding.

README §§Quickstart currently advertises bulk scope and manifest deletion, not ordinary-object deletion or directory upload (`README.md:92`–`:98`).
The latter remain documented v1/Phase-5 capabilities in DESIGN §63/§60 rather than fulfilled current README journeys.
README `:97`–`:98` says recorded ownership proof alone can permit segment destruction, which is stale relative to the current force-only containment described in `ManifestDeleteDialog.svelte:50`–`:68` and DESIGN §65.9.

## Recommended sequencing and test scope

1. Preserve the reproduced frontend regressions as permanent expected-safe tests and connect each to named UI action/bridge contract coverage.
2. Include the fault/barrier harness in this audit's remediation plan: it is directly needed for plan identity, cross-request/session races, cancellation, and backend mutation boundary failures.
3. Fix UI-01/02/03/04 with the backend plan/session identity work before widening destructive capabilities; keep one logical fix per commit and rerun the exact failing probes.
4. Fix connection/find lifecycle and error visibility, then the missing exact-object deletion flow with refusal twins and whole-account negative-space snapshots.
5. Complete jobs/audit diagnostics and real settings controls so operators can understand outcomes and recovery.
6. Complete the accessibility/contrast/responsive work as measured release gates, followed by native platform user-journey runs and realistic large-namespace performance measurements.

The deterministic frontend API response barriers are complementary to the Go fault/barrier harness.
The latter proves remote state and recovery at durable/mutation boundaries; it cannot prove that the displayed preview belongs to the request the UI sends.
Conversely, JSDOM component tests cannot prove Swift semantics, native dialogs, real process cancellation, or platform accessibility.
The existing §65.10 design already calls for these as separate, cooperating layers and is the appropriate place to integrate this evidence.
