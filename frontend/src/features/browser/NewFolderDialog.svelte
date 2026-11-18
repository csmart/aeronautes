<!-- New folder (ADR 0125 decision 5): a name, and the folder's marker
  object is created where the operator is browsing, as "Create marker
  object" makes one for a folder that objects already imply. -->
<script lang="ts">
  import { onDestroy, untrack } from "svelte";
  import { trapFocus } from "../../lib/trapFocus";
  import type { BrowserState } from "./browserState.svelte";

  let { browser, onClose }: { browser: BrowserState; onClose: () => void } = $props();

  // The folder belongs to the account, container and place that opened
  // this dialog; the portrait names the write's scope (ADR 0079).
  // svelte-ignore state_referenced_locally
  const target = {
    browser, scope: browser.scopeRevision, portrait: browser.portrait,
    container: browser.containerName ?? "", at: browser.folderBeingBrowsed,
  };
  let closed = $state(false);

  function isCurrent() {
    return !closed && browser === target.browser && !browser.connecting && !!browser.connectedBookmarkId
      && browser.scopeRevision === target.scope && browser.containerName === target.container;
  }

  function close() {
    if (closed) return;
    closed = true;
    onClose();
  }

  onDestroy(() => { closed = true; });
  $effect(() => { if (!isCurrent()) untrack(close); });

  let name = $state("");
  let saving = $state(false);
  let error: string | null = $state(null);
  const path = $derived(folderPath(name));

  // Spaces are kept as typed, a trailing slash is the delimiter the marker
  // adds itself, and a leading one would make an empty segment.
  function folderPath(typed: string): string | null {
    const trimmed = typed.replace(/\/+$/, "");
    if (trimmed === "" || trimmed.startsWith("/") || trimmed.includes("//")) return null;
    return `${target.at}${trimmed}/`;
  }

  async function create() {
    if (saving || !isCurrent() || path === null) return;
    saving = true;
    error = null;
    try {
      await browser.createFolder(target.container, path, target.portrait, isCurrent);
      if (isCurrent()) close();
    } catch (err) {
      if (isCurrent()) error = err instanceof Error ? err.message : String(err);
    } finally {
      if (isCurrent()) saving = false;
    }
  }
</script>

<div class="scrim" role="presentation" onclick={(e) => e.target === e.currentTarget && close()}>
  <div role="dialog" aria-modal="true" aria-label="New folder" tabindex="-1" use:trapFocus={close}>
  <form
    class="dialog"
    aria-label="New folder"
    onsubmit={(e) => {
      e.preventDefault();
      create();
    }}
  >
    <h2 class="title">New folder</h2>
    <label class="field">
      <span>Name</span>
      <!-- svelte-ignore a11y_autofocus -->
      <input class="type-mono" bind:value={name} autofocus required placeholder="reports" disabled={saving || !isCurrent()} />
    </label>
    <p class="where">
      {#if path !== null}
        Creates the marker object <span class="type-mono">{target.container}/{path}</span>, an empty object that keeps the folder listed while nothing is in it.
      {:else if name.trim() !== ""}
        A folder name cannot start with "/" or hold "//".
      {:else}
        In <span class="type-mono">{target.container}/{target.at}</span>
      {/if}
    </p>
    {#if error}
      <p class="error" role="alert">{error}</p>
    {/if}
    <div class="actions">
      <button type="button" class="secondary" onclick={close} disabled={saving}>Cancel</button>
      <button type="submit" class="primary" disabled={saving || !isCurrent() || path === null}>
        {saving ? "Creating…" : "Create"}
      </button>
    </div>
  </form>
  </div>
</div>

<style>
  .scrim {
    position: fixed;
    inset: 0;
    display: grid;
    place-items: center;
    background: rgb(20 16 12 / 0.45);
    z-index: 10;
  }

  .dialog {
    width: min(22rem, calc(100vw - 2 * var(--space-5)));
    padding: var(--space-4);
    display: flex;
    flex-direction: column;
    gap: var(--space-3);
    background: var(--color-bg-pane);
    border: 1px solid var(--color-border);
    border-radius: var(--radius-card);
    box-shadow: var(--shadow-raised);
  }

  .where {
    margin: 0;
    font-size: var(--text-sm);
    color: var(--color-text-secondary);
    overflow-wrap: anywhere;
  }

  .title {
    margin: 0;
    font-size: var(--text-lg);
    font-weight: var(--weight-semibold);
  }

  .field {
    display: flex;
    flex-direction: column;
    gap: 2px;
    font-size: var(--text-sm);
    color: var(--color-text-secondary);
  }

  .field input {
    padding: var(--space-1) var(--space-2);
    font: inherit;
    color: var(--color-text);
    background: var(--color-bg-card);
    border: 1px solid var(--color-border);
    border-radius: var(--radius-control);
  }

  .error {
    margin: 0;
    padding: var(--space-2);
    font-size: var(--text-sm);
    color: var(--color-danger);
    background: var(--color-danger-soft);
    border-radius: var(--radius-control);
    overflow-wrap: anywhere;
  }

  .actions {
    display: flex;
    justify-content: flex-end;
    gap: var(--space-2);
  }

  .actions button {
    padding: var(--space-1) var(--space-3);
    font: inherit;
    font-size: var(--text-sm);
    border-radius: var(--radius-control);
    cursor: pointer;
  }

  .secondary {
    color: var(--color-text-secondary);
    background: var(--color-bg-card);
    border: 1px solid var(--color-border);
  }

  .primary {
    color: var(--color-text-invert);
    background: var(--color-accent);
    border: 1px solid var(--color-accent-strong);
    font-weight: var(--weight-medium);
  }

  .actions button:disabled {
    opacity: 0.6;
    cursor: default;
  }
</style>
