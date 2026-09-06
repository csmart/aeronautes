<!-- Mirror keeps its comparison preview while ADR 0020 prevents an
  execution that cannot protect concurrent changes. -->
<script lang="ts">
  import { onDestroy, untrack } from "svelte";
  import { trapFocus } from "../../lib/trapFocus";
  import type { BrowserState } from "../browser/browserState.svelte";
  import type { CopyPlanDTO, CopyRequestDTO } from "../../lib/api";
  import { formatBytes } from "../../lib/format";

  let { browser, onClose }: { browser: BrowserState; onClose: () => void } = $props();

  type TransferMode = "copy" | "mirror";
  let mode: TransferMode = $state("copy");
  // The dialog remounts per open, so the initial value is the intent.
  // svelte-ignore state_referenced_locally
  let destBookmarkId = $state(browser.connectedBookmarkId ?? "");
  // §40 performance profile, seeded from the application default.
  // svelte-ignore state_referenced_locally
  let profile = $state(browser.appSettings?.defaultProfile ?? "auto");
  let destContainer = $state("");
  let destPrefix = $state("");
  // Per-job §44.1 overrides; empty/zero = defaults.
  let chunkSize = $state("");
  let transfers = $state("");
  let bwLimit = $state("");
  let previewing = $state(false);
  let starting = $state(false);
  let error: string | null = $state(null);
  let startedJobId: string | null = $state(null);
  let closed = $state(false);
  let generation = 0;
  interface Intent {
    generation: number;
    browser: BrowserState;
    scopeRevision: number;
    mode: TransferMode;
    request: CopyRequestDTO;
  }
  let prepared = $state.raw<{ intent: Intent; plan: CopyPlanDTO } | null>(null);
  const preview = $derived(prepared && isCurrent(prepared.intent) ? prepared.plan : null);
  const canPreview = $derived(!closed && !previewing && !starting && !browser.connecting
    && !!browser.connectedBookmarkId && !!browser.containerName && !!destContainer.trim());

  function request(): CopyRequestDTO {
    return {
      sourceBookmarkId: browser.connectedBookmarkId ?? "",
      sourceContainer: browser.containerName ?? "",
      sourcePrefix: browser.prefix,
      destBookmarkId,
      destContainer: destContainer.trim(),
      destPrefix: destPrefix.trim(),
      profile,
      settings: {
        chunkSize: chunkSize.trim(),
        transfers: Number.parseInt(transfers, 10) || 0,
        checkers: 0,
        tpsLimit: 0,
        bwLimit: bwLimit.trim(),
      },
    };
  }

  function isCurrent(intent: Intent): boolean {
    return !closed && !browser.connecting && intent.generation === generation
      && intent.browser === browser && intent.scopeRevision === browser.scopeRevision
      && intent.mode === mode && JSON.stringify(intent.request) === JSON.stringify(request());
  }

  function invalidate() {
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

  async function runPreview() {
    if (!canPreview) return;
    invalidate();
    const req = request();
    Object.freeze(req.settings);
    Object.freeze(req);
    const intent: Intent = { generation, browser, scopeRevision: browser.scopeRevision, mode, request: req };
    previewing = true;
    try {
      const plan = await browser.planCopy(req);
      if (isCurrent(intent)) prepared = { intent, plan };
    } catch (e) {
      if (isCurrent(intent)) error = e instanceof Error ? e.message : String(e);
    } finally {
      if (isCurrent(intent)) previewing = false;
    }
  }

  async function start() {
    const current = prepared;
    if (mode !== "copy" || starting || previewing || !current || !isCurrent(current.intent)) return;
    prepared = null;
    starting = true;
    error = null;
    try {
      const jobId = await browser.startCopy(current.intent.request);
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
  <div class="dialog" role="dialog" aria-label="Transfer" tabindex="-1" use:trapFocus>
    <header>
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
      <button class="close" onclick={close} aria-label="Close">✕</button>
    </header>

    {#if mode === "mirror"}
      <p class="warn" role="status">
        Mirror is unavailable while concurrent changes cannot be protected.
        Preview compares the two scopes; it does not start a transfer or delete objects.
      </p>
    {:else}
      <p class="warn" role="status">
        Copy adds missing objects. Existing changed objects are refused because concurrent
        changes cannot be protected.
      </p>
    {/if}

    <div class="scope num">
      <div class="side">
        <span class="side-label">Source</span>
        <span class="type-mono">{browser.containerName ?? "—"}/{browser.prefix}</span>
      </div>
      <span class="arrow">→</span>
      <div class="side fields">
        <span class="side-label">Destination</span>
        <select
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
          placeholder="container"
          value={destContainer}
          oninput={(e) => {
            destContainer = e.currentTarget.value;
            invalidate();
          }}
        />
        <input
          class="type-mono"
          placeholder="prefix/ (optional)"
          value={destPrefix}
          oninput={(e) => {
            destPrefix = e.currentTarget.value;
            invalidate();
          }}
        />
      </div>
    </div>

    <details class="advanced">
      <summary>Per-job settings</summary>
      <div class="advanced-fields num">
        <label>
          <span>Profile</span>
          <select bind:value={profile} onchange={invalidate}>
            <option value="auto">Auto</option>
            <option value="balanced">Balanced</option>
            <option value="aggressive">Aggressive</option>
            <option value="custom">Custom</option>
          </select>
        </label>
        <label>
          <span>Chunk size</span>
          <input class="type-mono" placeholder="5Gi (default)" bind:value={chunkSize} oninput={invalidate} />
        </label>
        <label>
          <span>Transfers</span>
          <input class="type-mono" placeholder="4 (default)" bind:value={transfers} oninput={invalidate} />
        </label>
        <label>
          <span>Bandwidth</span>
          <input class="type-mono" placeholder="unlimited" bind:value={bwLimit} oninput={invalidate} />
        </label>
      </div>
    </details>

    {#if error}
      <p class="error">{error}</p>
    {/if}

    {#if preview}
      {@const s = preview.summary}
      <div class="preview num">
        <div class="counts">
          <span class="count"><strong>{s.new}</strong> new</span>
          <span class="count"><strong>{s.changed}</strong> {mode === "copy" ? `${s.changed === 1 ? "overwrite" : "overwrites"} refused` : "changed"}</span>
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
            {#if s.unknown > 0}
              <p>
                {s.unknown} uncertain object{s.unknown === 1 ? "" : "s"} will be checked.
                Equal objects are skipped; unequal existing destinations are refused.
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
                The destination container is versioned. Existing changed objects are still refused.
              </p>
            {/if}
            <p class="data-path">
              {preview.dataPath}{preview.overwriteBytes > 0
                ? ` · ${formatBytes(preview.overwriteBytes)} listed bytes in refused changed objects`
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
        <span class="started">Job {startedJobId} started — follow it in the drawer.</span>
        <button class="primary" onclick={close}>Close</button>
      {:else}
        <button onclick={runPreview} disabled={!canPreview}>
          {previewing ? "Planning…" : "Preview"}
        </button>
        {#if mode === "copy"}
          <button class="primary" onclick={start} disabled={!preview || previewing || starting || closed}>
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
    display: flex;
    flex-direction: column;
    gap: var(--space-1);
  }

  .side-label {
    font-size: var(--text-xs);
    color: var(--color-text-faint);
  }

  .fields {
    flex: 1;
  }

  .fields select,
  .fields input {
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

  .advanced summary {
    font-size: var(--text-xs);
    color: var(--color-text-faint);
    cursor: pointer;
  }

  .advanced-fields {
    display: flex;
    gap: var(--space-3);
    padding-top: var(--space-2);
  }

  .advanced-fields label {
    display: flex;
    flex-direction: column;
    gap: 2px;
    font-size: var(--text-xs);
    color: var(--color-text-faint);
  }

  .advanced-fields select {
    font: inherit;
    font-size: var(--text-sm);
    background: var(--color-bg-card);
    color: var(--color-text);
    border: 1px solid var(--color-border);
    border-radius: var(--radius-control);
    padding: 2px var(--space-2);
  }

  .advanced-fields input {
    width: 8rem;
    font: inherit;
    font-size: var(--text-sm);
    background: var(--color-bg-card);
    color: var(--color-text);
    border: 1px solid var(--color-border);
    border-radius: var(--radius-control);
    padding: 2px var(--space-2);
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
</style>
