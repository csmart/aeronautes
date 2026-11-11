<!-- Mirror keeps its comparison preview while ADR 0020 prevents an
  execution that cannot protect concurrent changes. -->
<script lang="ts">
  import { onDestroy, tick, untrack } from "svelte";
  import { activeModal, trapFocus } from "../../lib/trapFocus";
  import type { BrowserState } from "../browser/browserState.svelte";
  import type { CopyPlanDTO, CopyRequestDTO } from "../../lib/api";
  import { formatBytes } from "../../lib/format";
  import { v1Offers } from "../../lib/v1.svelte";
  import JobSettingsFields from "./JobSettingsFields.svelte";
  import { newJobSettings, requestSettings } from "./jobSettings";

  let { browser, onClose }: { browser: BrowserState; onClose: () => void } = $props();

  type TransferMode = "copy" | "mirror";
  let mode: TransferMode = $state("copy");
  // The dialog remounts per open, so the initial value is the intent.
  // svelte-ignore state_referenced_locally
  let destBookmarkId = $state(browser.connectedBookmarkId ?? "");
  // The profile is seeded from the application default (§40).
  // svelte-ignore state_referenced_locally
  let jobSettings = $state(newJobSettings(browser.settings.document?.defaultProfile ?? "auto"));
  let destContainer = $state("");
  let destPrefix = $state("");
  let previewing = $state(false);
  let starting = $state(false);
  let error: string | null = $state(null);
  let startedJobId: string | null = $state(null);
  let closed = $state(false);
  let generation = 0;
  let cancelPreviewFocus: (() => void) | undefined;
  // The scope is the copy's identity: every field a late navigation could
  // change. acknowledgeOverwrite is NOT part of it — it is the operator's
  // answer to the overwrite question, carried on the request this dialog
  // starts and kept out of the identity so acknowledging is not mistaken for a
  // scope change. The plan is advisory and ignores the flag (PlanCopy), so the
  // plan shown is the plan acknowledged; the authorising question lives on the
  // native dialog StartCopy raises (ADR 0081 decision 2), not on this page.
  type Scope = Omit<CopyRequestDTO, "acknowledgeOverwrite">;
  interface Intent {
    generation: number;
    browser: BrowserState;
    scopeRevision: number;
    mode: TransferMode;
    scope: Scope;
  }
  let prepared = $state.raw<{ intent: Intent; plan: CopyPlanDTO; request: CopyRequestDTO } | null>(null);
  const preview = $derived(prepared && isCurrent(prepared.intent) ? prepared.plan : null);
  const canPreview = $derived(!closed && !previewing && !starting && !browser.connecting
    && !!browser.connectedBookmarkId && !!browser.containerName && destContainer !== "");
  // The overwrite question only has an answer once a copy preview shows a
  // destination that exists: Changed is a listed difference, Unknown a pair the
  // listing could not prove equal. Both are refused by default and both can be
  // replaced once acknowledged — the executor replaces what still differs at
  // the fixed point and skips what is identical.
  const overwriteCandidates = $derived(mode === "copy" && preview ? preview.summary.changed + preview.summary.unknown : 0);
  const acknowledged = $derived(!!preview && prepared?.request.acknowledgeOverwrite === true);

  function scope(): Scope {
    return {
      sourceBookmarkId: browser.connectedBookmarkId ?? "",
      sourceContainer: browser.containerName ?? "",
      sourcePrefix: browser.prefix,
      destBookmarkId,
      destContainer,
      destPrefix,
      profile: jobSettings.profile,
      settings: requestSettings(jobSettings),
    };
  }

  function isCurrent(intent: Intent): boolean {
    return !closed && !browser.connecting && intent.generation === generation
      && intent.browser === browser && intent.scopeRevision === browser.scopeRevision
      && intent.mode === mode && JSON.stringify(intent.scope) === JSON.stringify(scope());
  }

  function invalidate() {
    cancelPreviewFocus?.();
    generation++;
    prepared = null;
    previewing = false;
    startedJobId = null;
    error = null;
  }

  // A synchronous scope revision also catches leaving and returning before
  // Svelte runs this effect; request equality alone would miss that history.
  $effect(() => {
    void browser.scopeRevision;
    untrack(invalidate);
  });

  function close() {
    closed = true;
    invalidate();
    onClose();
  }
  onDestroy(() => { closed = true; invalidate(); });

  function changeMode(next: TransferMode) {
    if (mode === next) return;
    mode = next;
    invalidate();
  }

  function trackPreviewFocus(button: HTMLButtonElement, intent: Intent) {
    const modal = activeModal();
    if (document.activeElement !== button || !modal?.contains(button)) return async () => {};
    let cancelled = false;
    const navigationEvents = ["keydown", "pointerdown", "click"] as const;
    function cleanup() {
      for (const event of navigationEvents) document.removeEventListener(event, cancel, true);
      document.removeEventListener("focusin", changedFocus, true);
      window.removeEventListener("blur", cancel);
      if (cancelPreviewFocus === cancel) cancelPreviewFocus = undefined;
    }
    function cancel() { cancelled = true; cleanup(); }
    function changedFocus(event: FocusEvent) {
      if (event.target !== button) cancel();
    }
    // An empty focus target at completion is not enough: deliberate navigation
    // can also leave the body focused, and must retire this request's claim.
    for (const event of navigationEvents) document.addEventListener(event, cancel, true);
    document.addEventListener("focusin", changedFocus, true);
    window.addEventListener("blur", cancel);
    cancelPreviewFocus = cancel;
    return async () => {
      // WebKit can discard focus after the first render frame. Check while
      // this request still owns the disabled control, before re-enabling.
      const lost = button.disabled && document.activeElement === document.body;
      // Close or another request may win before re-enabling renders.
      await tick();
      const restore = lost && !cancelled && isCurrent(intent) && activeModal() === modal
        && button.isConnected && !button.disabled && document.activeElement === document.body;
      cleanup();
      if (restore) button.focus();
    };
  }

  async function runPreview(button: HTMLButtonElement) {
    if (!canPreview) return;
    invalidate();
    const capturedScope = scope();
    Object.freeze(capturedScope.settings);
    Object.freeze(capturedScope);
    const intent: Intent = { generation, browser, scopeRevision: browser.scopeRevision, mode, scope: capturedScope };
    // A preview is unacknowledged: it asks what the copy would do, refusing
    // every overwrite. Replacing is a second, deliberate step.
    const req = Object.freeze({ ...capturedScope, acknowledgeOverwrite: false });
    const finishFocus = trackPreviewFocus(button, intent);
    previewing = true;
    try {
      const plan = await browser.planCopy(req);
      if (isCurrent(intent)) prepared = { intent, plan, request: req };
    } catch (e) {
      if (isCurrent(intent)) error = e instanceof Error ? e.message : String(e);
    } finally {
      const focusCompletion = finishFocus();
      if (isCurrent(intent)) previewing = false;
      await focusCompletion;
    }
  }

  // Record that the operator asked to replace the differing destinations. The
  // plan is not re-read: it does not depend on this flag, so re-listing would
  // only risk showing a different set than the one they chose to replace. The
  // flag is an accident guard — StartCopy raises the one native confirmation,
  // naming both accounts and the live names, before it writes anything, and the
  // executor still refuses at the fixed point anything whose identity no longer
  // matches what was acknowledged.
  function replace() {
    const current = prepared;
    if (previewing || starting || mode !== "copy" || !current || !isCurrent(current.intent)
      || current.request.acknowledgeOverwrite
      || current.plan.summary.changed + current.plan.summary.unknown === 0) return;
    const request = Object.freeze({ ...current.request, acknowledgeOverwrite: true });
    prepared = { intent: current.intent, plan: current.plan, request };
  }

  async function start() {
    const current = prepared;
    if (mode !== "copy" || starting || previewing || !current || !isCurrent(current.intent)) return;
    prepared = null;
    starting = true;
    error = null;
    try {
      const jobId = await browser.startCopy(current.request);
      if (isCurrent(current.intent)) startedJobId = jobId;
    } catch (e) {
      if (isCurrent(current.intent)) error = e instanceof Error ? e.message : String(e);
    } finally {
      // Only one Start can be pending in this dialog, even after intent edits.
      starting = false;
    }
  }
</script>

<div class="scrim" role="presentation" onclick={(e) => e.target === e.currentTarget && close()}>
  <div class="dialog" role="dialog" aria-modal="true" aria-label="Transfer" tabindex="-1" use:trapFocus={close}>
    <header>
      {#if v1Offers.mirrorPreview}
      <div class="modes" role="tablist">
        <button
          role="tab"
          class:active={mode === "copy"}
          aria-selected={mode === "copy"}
          onclick={() => changeMode("copy")}>Copy</button
        >
        <button
          role="tab"
          class:active={mode === "mirror"}
          aria-selected={mode === "mirror"}
          onclick={() => changeMode("mirror")}>Mirror</button
        >
      </div>
      {:else}
        <h2 class="title">Copy</h2>
      {/if}
      <button class="close" onclick={close} aria-label="Close">✕</button>
    </header>

    {#if mode === "mirror"}
      <p class="warn" role="status">
        Mirror is unavailable while concurrent changes cannot be protected.
        Preview compares the two scopes; it does not start a transfer or delete objects.
      </p>
    {:else}
      <p class="warn" role="status">
        Copy adds missing objects. An existing object that differs is refused unless you
        choose to replace it, which the application asks you to confirm before it writes anything.
      </p>
    {/if}

    <div class="scope num">
      <div class="side">
        <span class="side-label">Source</span>
        <div class="scope-text type-mono" role="group" aria-label="Source scope">
          <span>Container: {browser.containerName == null ? "—" : JSON.stringify(browser.containerName)}</span>
          <span>Prefix: {JSON.stringify(browser.prefix)}{browser.prefix === "" ? " (container root)" : ""}</span>
        </div>
      </div>
      <span class="arrow">→</span>
      <div class="side fields">
        <span class="side-label">Destination</span>
        <select
          aria-label="Destination bookmark"
          value={destBookmarkId}
          onchange={(e) => {
            destBookmarkId = e.currentTarget.value;
            invalidate();
          }}
        >
          {#each browser.bookmarks as bookmark (bookmark.id)}
            <option value={bookmark.id}>{bookmark.displayName}</option>
          {/each}
        </select>
        <input
          class="type-mono"
          aria-label="Destination container"
          placeholder="container"
          value={destContainer}
          oninput={(e) => {
            destContainer = e.currentTarget.value;
            invalidate();
          }}
        />
        <input
          class="type-mono"
          aria-label="Destination prefix"
          placeholder="folder path/ (optional)"
          value={destPrefix}
          oninput={(e) => {
            destPrefix = e.currentTarget.value;
            invalidate();
          }}
        />
        <div class="scope-text type-mono" role="group" aria-label="Destination scope">
          <span>Container: {JSON.stringify(destContainer)}</span>
          <span>Prefix: {JSON.stringify(destPrefix)}{destPrefix === "" ? " (container root)" : ""}</span>
        </div>
        {#if destPrefix.startsWith("/")}
          <!-- Swift keeps the slash as part of each name, so "/" is a folder
            named "/" rather than the container's top; operators expect the
            latter (0.5.0 test run). Allowed, because such names are legal. -->
          <p class="callout warning slash-warning">
            A folder path starting with "/" makes names that start with a slash, under a folder named "/".
            Leave it empty to copy to the top of the container.
          </p>
        {/if}
      </div>
    </div>

    <JobSettingsFields bind:settings={jobSettings} defaultProfile={browser.settings.document?.defaultProfile} onchange={invalidate} />

    {#if error}
      <p class="error callout danger" role="alert">{error}</p>
    {/if}

    {#if !preview && !startedJobId && mode === "copy"}
      <!-- Start stays disabled until a preview exists, and nothing else on
        the page says why. -->
      <p class="next-step">
        Preview first: it plans what will be copied and names anything it would replace, then Start copy is
        ready.
      </p>
    {/if}

    {#if preview}
      {@const s = preview.summary}
      <div class="preview num">
        <div class="counts">
          <span class="count"><strong>{s.new}</strong> new</span>
          <span class="count"><strong>{s.changed}</strong> {mode === "mirror"
            ? "changed"
            : acknowledged
              ? "to replace"
              : `${s.changed === 1 ? "overwrite" : "overwrites"} refused`}</span>
          <span class="count"><strong>{s.same}</strong> unchanged</span>
          <span class="count conservative" title="Equality is not established by listings">
            <strong>{s.unknown}</strong> unable to prove unchanged
          </span>
          <span class="count" title="Listed source bytes for new, changed and uncertain objects; not a transfer estimate">
            <strong>{formatBytes(s.transferBytes)}</strong> listed bytes in comparison
          </span>
        </div>
        {#if mode === "copy"}
          <div class="notes">
            {#if acknowledged}
              <p class="warn">
                This copy will REPLACE what still differs at the destination. The application
                will ask you to confirm — naming both accounts and the container — before it
                writes anything. Identical objects are skipped either way.
              </p>
            {/if}
            {#if s.unknown > 0}
              <p>
                {s.unknown} uncertain object{s.unknown === 1 ? "" : "s"} will be checked.
                Equal objects are skipped; {acknowledged
                  ? "unequal existing destinations are replaced once you confirm"
                  : "unequal existing destinations are refused"}.
                A missing destination can be created conditionally.
              </p>
            {/if}
            {#if s.nativeRouted > 0}
              <p>
                {s.nativeRouted} name{s.nativeRouted === 1 ? "" : "s"} require native handling
                if eligible for copying.
              </p>
            {/if}
            {#if preview.largeObjectsAsDlo}
              <p>
                Eligible large objects become destination DLOs: their reads depend on container-listing
                consistency.
              </p>
            {/if}
            {#if preview.versionedDestination}
              <p class="warn">
                The destination container reported versioning when it was read. {acknowledged
                  ? "Whether a replaced object's earlier version is kept is the cluster's to decide and is not verified here; the confirmation states what it observed."
                  : "Existing changed objects are refused unless you choose to replace them."}
              </p>
            {/if}
            <p class="data-path">
              {preview.dataPath}{preview.overwriteBytes > 0
                ? ` · ${formatBytes(preview.overwriteBytes)} listed bytes in ${acknowledged ? "changed objects to replace" : "refused changed objects"}`
                : ""}
            </p>
          </div>
        {/if}

        {#if mode === "mirror"}
          <div class="mirror num">
            <h3>Only at the destination</h3>
            <p class="would-delete">
              <strong>{s.destOnly}</strong> destination-only object{s.destOnly === 1 ? "" : "s"}
              · {formatBytes(s.destOnlyBytes)}
            </p>
          </div>
        {/if}
      </div>
    {/if}

    <footer>
      {#if startedJobId}
        <span class="started">Job {startedJobId} started.</span>
        <button
          onclick={() => {
            const jobId = startedJobId;
            close();
            if (jobId) void browser.jobs.reveal(jobId);
          }}>Show in jobs</button
        >
        <button class="primary" onclick={close}>Close</button>
      {:else}
        <!-- The highlight marks the next step: Preview until a preview is
          shown, then the start, which stays plain and disabled before it. -->
        <button class:primary={!preview} onclick={(event) => runPreview(event.currentTarget)} disabled={!canPreview}>
          {previewing ? "Planning…" : "Preview"}
        </button>
        {#if mode === "copy"}
          {#if overwriteCandidates > 0 && !acknowledged}
            <button onclick={replace} disabled={previewing || starting || closed}>
              Replace existing objects…
            </button>
          {/if}
          <button class:primary={!!preview} onclick={start} disabled={!preview || previewing || starting || closed}>
            {starting ? "Starting…" : "Start copy"}
          </button>
        {:else}
          <button disabled>Mirror unavailable</button>
        {/if}
      {/if}
    </footer>
  </div>
</div>

<style>
  .scrim {
    position: fixed;
    inset: 0;
    background: color-mix(in srgb, var(--color-bg) 60%, transparent);
    display: grid;
    place-items: center;
    z-index: 30;
  }

  .dialog {
    width: min(44rem, calc(100vw - 4rem));
    background: var(--color-bg-pane);
    border: 1px solid var(--color-border);
    border-radius: var(--radius-card);
    padding: var(--space-4);
    display: flex;
    flex-direction: column;
    gap: var(--space-3);
  }

  header {
    display: flex;
    justify-content: space-between;
    align-items: center;
  }

  .title {
    margin: 0;
    font-size: var(--text-base);
  }

  .modes {
    display: flex;
    gap: var(--space-2);
  }

  .modes [role="tab"] {
    font: inherit;
    font-size: var(--text-sm);
    padding: 2px var(--space-3);
    border: 1px solid var(--color-border);
    border-radius: var(--radius-pill);
    background: none;
    color: var(--color-text-secondary);
    cursor: pointer;
  }

  .modes [role="tab"].active {
    color: var(--color-text);
    background: var(--color-bg-card);
    border-color: var(--color-text-faint);
  }

  .close {
    font: inherit;
    background: none;
    border: none;
    color: var(--color-text-faint);
    cursor: pointer;
  }

  .scope {
    display: flex;
    align-items: flex-start;
    gap: var(--space-3);
    font-size: var(--text-sm);
  }

  .side {
    flex: 1;
    min-width: 0;
    display: flex;
    flex-direction: column;
    gap: var(--space-1);
  }

  .side-label {
    font-size: var(--text-xs);
    color: var(--color-text-faint);
  }

  .scope-text {
    display: flex;
    flex-direction: column;
    white-space: pre-wrap;
    overflow-wrap: anywhere;
  }

  .fields select,
  .fields input {
    min-width: 0;
    font: inherit;
    font-size: var(--text-sm);
    background: var(--color-bg-card);
    color: var(--color-text);
    border: 1px solid var(--color-border);
    border-radius: var(--radius-control);
    padding: 2px var(--space-2);
  }

  .arrow {
    margin-top: 1.1rem;
    color: var(--color-text-faint);
  }

  .error {
    margin: 0;
    font-size: var(--text-sm);
    color: var(--color-danger);
  }

  .preview {
    border: 1px solid var(--color-border);
    border-radius: var(--radius-card);
    padding: var(--space-3);
    display: flex;
    flex-direction: column;
    gap: var(--space-2);
  }

  .counts {
    display: flex;
    flex-wrap: wrap;
    gap: var(--space-3);
    font-size: var(--text-sm);
  }

  .conservative {
    color: var(--color-warning);
  }

  .notes {
    font-size: var(--text-xs);
    color: var(--color-text-secondary);
  }

  .notes p {
    margin: 0 0 2px;
  }

  .data-path {
    margin: 0;
    font-size: var(--text-xs);
    color: var(--color-text-secondary);
  }

  .warn {
    color: var(--color-warning);
  }

  .mirror {
    border-top: 1px dashed var(--color-border);
    padding-top: var(--space-2);
  }

  .mirror h3 {
    margin: 0 0 var(--space-1);
    font-size: var(--text-sm);
    color: var(--color-danger);
  }

  .would-delete {
    margin: 0 0 var(--space-1);
    font-size: var(--text-sm);
  }

  footer {
    display: flex;
    justify-content: flex-end;
    align-items: center;
    gap: var(--space-2);
  }

  .started {
    margin-right: auto;
    font-size: var(--text-sm);
    color: var(--color-success);
  }

  footer button {
    font: inherit;
    font-size: var(--text-sm);
    padding: var(--space-1) var(--space-3);
    border: 1px solid var(--color-border);
    border-radius: var(--radius-control);
    background: var(--color-bg-card);
    color: var(--color-text);
    cursor: pointer;
  }

  footer button:disabled {
    opacity: 0.5;
    cursor: default;
  }

  footer .primary {
    background: var(--color-accent);
    color: var(--color-bg);
    border-color: var(--color-accent);
  }
  .next-step {
    margin: 0;
    font-size: var(--text-sm);
    color: var(--color-text-secondary);
  }
</style>
