<!-- Create container (§20): name plus the storage-policy picker fed by
  connect-time capabilities — one advertised policy collapses to no
  chrome, several offer a select with the default marked ('s rule
  about not cluttering the UI unnecessarily). -->
<script lang="ts">
  import { onDestroy, untrack } from "svelte";
  import { trapFocus } from "../../lib/trapFocus";
  import type { BrowserState } from "./browserState.svelte";

  let { browser, onClose }: { browser: BrowserState; onClose: () => void } = $props();

  // Container creation belongs to the account and view that opened this dialog.
  // svelte-ignore state_referenced_locally
  const target = { browser, scope: browser.scopeRevision, selection: browser.detailIdentity };
  let closed = $state(false);
  function isCurrent() {
    return !closed && browser === target.browser && !browser.connecting && !!browser.connectedBookmarkId
      && browser.scopeRevision === target.scope && browser.detailIdentity === target.selection;
  }
  function close() {
    if (closed) return;
    closed = true;
    onClose();
  }
  onDestroy(() => { closed = true; });
  $effect(() => { if (!isCurrent()) untrack(close); });

  const policies = $derived(browser.capabilities?.storagePolicies ?? []);
  const showPicker = $derived(policies.length > 1);

  let name = $state("");
  let policy = $state("");
  let saving = $state(false);
  let error: string | null = $state(null);

  async function create() {
    if (saving || !isCurrent()) return;
    saving = true;
    error = null;
    try {
      await browser.createContainer({ name, policy, meta: {} }, isCurrent);
      if (isCurrent()) close();
    } catch (err) {
      if (isCurrent()) error = err instanceof Error ? err.message : String(err);
    } finally {
      if (isCurrent()) saving = false;
    }
  }
</script>

<div class="scrim" role="presentation" onclick={(e) => e.target === e.currentTarget && close()}>
  <div role="dialog" aria-modal="true" aria-label="Create container" tabindex="-1" use:trapFocus={close}>
  <form
    class="dialog"
    aria-label="Create container"
    onsubmit={(e) => {
      e.preventDefault();
      create();
    }}
  >
    <h2 class="title">Create container</h2>

    <label class="field">
      <span>Name</span>
      <!-- svelte-ignore a11y_autofocus -->
      <input class="type-mono" bind:value={name} autofocus required placeholder="my-container" disabled={saving || !isCurrent()} />
    </label>

    {#if showPicker}
      <label class="field">
        <span>Storage policy</span>
        <select bind:value={policy} disabled={saving || !isCurrent()}>
          <option value="">cluster default</option>
          {#each policies as p (p.name)}
            <option value={p.name}>{p.name}{p.default ? " (default)" : ""}</option>
          {/each}
        </select>
      </label>
    {/if}

    {#if error}
      <p class="error" role="alert">{error}</p>
    {/if}

    <div class="actions">
      <button type="button" class="secondary" onclick={close} disabled={saving}>Cancel</button>
      <button type="submit" class="primary" disabled={saving || !isCurrent()}>
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

  .field input,
  .field select {
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
