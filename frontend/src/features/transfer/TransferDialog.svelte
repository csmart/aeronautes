<!-- Mirror keeps its comparison preview while ADR 0020 prevents an
  execution that cannot protect concurrent changes. -->
<script lang="ts">
  import { trapFocus } from "../../lib/trapFocus";
  import type { BrowserState } from "../browser/browserState.svelte";
  import type { CopyPlanDTO, CopyRequestDTO } from "../../lib/api";
  import { formatBytes } from "../../lib/format";

  let { browser, onClose }: { browser: BrowserState; onClose: () => void } = $props();

  let mode: "copy" | "mirror" = $state("copy");
  // The dialog remounts per open, so the initial value is the intent.
  // svelte-ignore state_referenced_locally
  let destBookmarkId = $state(browser.connectedBookmarkId ?? "");
  // §40 performance profile, seeded from the application default.
  // svelte-ignore state_referenced_locally
  let profile = $state(browser.appSettings?.defaultProfile ?? "auto");
  let destContainer = $state("");
  let destPrefix = $state("");
  let preview: CopyPlanDTO | null = $state(null);
  // Per-job §44.1 overrides; empty/zero = defaults.
  let chunkSize = $state("");
  let transfers = $state("");
  let bwLimit = $state("");
  let previewing = $state(false);
  let starting = $state(false);
  let error: string | null = $state(null);
  let startedJobId: string | null = $state(null);

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

  // Any input change invalidates the preview — Start is gated on a
  // preview of exactly what would run.
  function invalidate() {
    preview = null;
    startedJobId = null;
    error = null;
  }

  async function runPreview() {
    previewing = true;
    error = null;
    try {
      preview = await browser.planCopy(request());
    } catch (e) {
      preview = null;
      error = e instanceof Error ? e.message : String(e);
    } finally {
      previewing = false;
    }
  }

  async function start() {
    if (mode !== "copy") return;
    starting = true;
    error = null;
    try {
      startedJobId = await browser.startCopy(request());
    } catch (e) {
      error = e instanceof Error ? e.message : String(e);
    } finally {
      starting = false;
    }
  }
</script>

<div class="scrim" role="presentation" onclick={(e) => e.target === e.currentTarget && onClose()}>
  <div class="dialog" role="dialog" aria-label="Transfer" tabindex="-1" use:trapFocus>
    <header>
      <div class="modes" role="tablist">
        <button
          role="tab"
          class:active={mode === "copy"}
          aria-selected={mode === "copy"}
          onclick={() => (mode = "copy")}>Copy</button
        >
        <button
          role="tab"
          class:active={mode === "mirror"}
          aria-selected={mode === "mirror"}
          onclick={() => (mode = "mirror")}>Mirror</button
        >
      </div>
      <button class="close" onclick={onClose} aria-label="Close">✕</button>
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
          <input class="type-mono" placeholder="5Gi (default)" bind:value={chunkSize} onchange={invalidate} />
        </label>
        <label>
          <span>Transfers</span>
          <input class="type-mono" placeholder="4 (default)" bind:value={transfers} onchange={invalidate} />
        </label>
        <label>
          <span>Bandwidth</span>
          <input class="type-mono" placeholder="unlimited" bind:value={bwLimit} onchange={invalidate} />
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
        <button class="primary" onclick={onClose}>Close</button>
      {:else}
        <button onclick={runPreview} disabled={previewing || !destContainer.trim()}>
          {previewing ? "Planning…" : "Preview"}
        </button>
        {#if mode === "copy"}
          <button class="primary" onclick={start} disabled={!preview || starting}>
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
