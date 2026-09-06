<!-- Find in container (§16): an explicit cancellable scan with honest
  progress — scanned count and throughput — and streamed matches.
  A result must retain its original scope until selection takes ownership. -->
<script lang="ts">
  import { onDestroy, untrack } from "svelte";
  import type { BrowserState } from "../browser/browserState.svelte";
  import { formatBytes } from "../../lib/format";

  let { browser, onClose }: { browser: BrowserState; onClose: () => void } = $props();

  let query = $state("");
  let navigating = $state(false);
  let navigationError: string | null = $state(null);
  const owner = untrack(() => browser);
  const panelRevision = owner.findPanelRevision;
  let closed = false;
  onDestroy(() => { closed = true; owner.closeFind(panelRevision); });

  $effect(() => {
    if (browser !== owner) owner.closeFind(panelRevision);
  });

  function ownsPanel() {
    return !closed && browser === owner && panelRevision === owner.findPanelRevision;
  }

  async function go(name: string) {
    if (!ownsPanel() || navigating) return;
    navigating = true;
    navigationError = null;
    try {
      if (await browser.openFindMatch(name, ownsPanel) && ownsPanel()) close();
    } catch (error) {
      if (ownsPanel()) navigationError = error instanceof Error ? error.message : String(error);
    } finally {
      if (ownsPanel()) navigating = false;
    }
  }

  function close() {
    if (!ownsPanel()) return;
    closed = true;
    browser.closeFind(panelRevision);
    onClose();
  }
</script>

<div class="panel num">
  <form
    class="controls"
    onsubmit={(e) => {
      e.preventDefault();
      if (ownsPanel() && !navigating && !browser.findRunning && query.trim() !== "") void browser.startFind(query);
    }}
  >
    <input
      class="type-mono"
      placeholder="find in {browser.containerName}/{browser.prefix}…"
      bind:value={query}
    />
    <button type="submit" disabled={browser.findRunning || navigating || query.trim() === ""}>
      {browser.findRunning ? "Scanning…" : "Find"}
    </button>
    {#if browser.findRunning}
      <button type="button" onclick={() => { if (ownsPanel()) void browser.cancelFind(); }}>Stop</button>
    {/if}
    <button type="button" class="close" onclick={close} aria-label="Close find">✕</button>
  </form>

  {#if browser.findNotice}
    <p class="stats" role="status">{browser.findNotice}</p>
  {/if}
  {#if navigationError}
    <p class="stats bad" role="alert">Could not open the result: {navigationError}</p>
  {/if}

  {#if browser.findStatus}
    {@const status = browser.findStatus}
    <p class="stats" aria-live="polite">
      {status.scanned} scanned
      {#if browser.findRunning && status.objectsPerSecond > 0}
        · {Math.round(status.objectsPerSecond)}/s
      {/if}
      · {status.matches?.length ?? 0} match{(status.matches?.length ?? 0) === 1 ? "" : "es"}
      {#if status.truncated}
        · <em class="warn">buffer full — narrow the query</em>
      {/if}
      {#if !status.done}
        · {browser.findRunning ? "scanning…" : "updates stopped"}
      {/if}
      {#if status.error}
        · <em class="bad">{status.error}</em>
      {/if}
    </p>
    <div class="matches">
      {#each status.matches ?? [] as match (match.name)}
        <button class="match" disabled={navigating} onclick={() => go(match.name)}>
          <span class="type-mono name">{match.name}</span>
          <span class="size">{formatBytes(match.bytes)}</span>
        </button>
      {/each}
    </div>
  {/if}
</div>

<style>
  .panel {
    border-bottom: 1px solid var(--color-border);
    padding: var(--space-2) var(--space-4);
    display: flex;
    flex-direction: column;
    gap: var(--space-2);
    background: var(--color-bg-card);
  }

  .controls {
    display: flex;
    gap: var(--space-2);
    align-items: center;
  }

  .controls input {
    flex: 1;
    font: inherit;
    font-size: var(--text-sm);
    background: var(--color-bg-pane);
    color: var(--color-text);
    border: 1px solid var(--color-border);
    border-radius: var(--radius-control);
    padding: 2px var(--space-2);
  }

  .controls button {
    font: inherit;
    font-size: var(--text-sm);
    padding: 2px var(--space-3);
    border: 1px solid var(--color-border);
    border-radius: var(--radius-control);
    background: var(--color-bg-pane);
    color: var(--color-text);
    cursor: pointer;
  }

  .controls button:disabled {
    opacity: 0.5;
  }

  .close {
    border: none;
    background: none;
    color: var(--color-text-faint);
  }

  .stats {
    margin: 0;
    font-size: var(--text-xs);
    color: var(--color-text-secondary);
    overflow-wrap: anywhere;
  }

  .warn {
    color: var(--color-warning);
    font-style: normal;
  }

  .bad {
    color: var(--color-danger);
    font-style: normal;
  }

  .matches {
    max-height: 11rem;
    overflow-y: auto;
    display: flex;
    flex-direction: column;
  }

  .match {
    display: flex;
    justify-content: space-between;
    gap: var(--space-3);
    font: inherit;
    font-size: var(--text-xs);
    padding: 1px var(--space-2);
    background: none;
    border: none;
    border-radius: var(--radius-control);
    color: var(--color-text);
    cursor: pointer;
    text-align: left;
  }

  .match:hover {
    background: var(--color-bg-pane);
  }

  .name {
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .size {
    color: var(--color-text-faint);
  }
</style>
